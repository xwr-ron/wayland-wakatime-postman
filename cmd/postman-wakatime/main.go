package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	defaultAddr      = "127.0.0.1:8765"
	defaultPlugin    = "postman/0.1.0"
	defaultWakaCLI   = "wakatime-cli"
	maxHeartbeatBody = 1 << 20 // 1 MiB
	maxRequestAge    = time.Minute
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("detect home dir: %w", err)
	}

	root := getenv("POSTMAN_WAKA_ROOT", filepath.Join(home, ".local", "share", "postman-wakatime"))
	stateDir := filepath.Join(root, ".state")

	c := &Collector{
		root:        root,
		stateDir:    stateDir,
		wakaCLI:     getenv("WAKATIME_CLI", defaultWakaCLI),
		plugin:      getenv("WAKATIME_PLUGIN", defaultPlugin),
		minInterval: 2 * time.Minute,
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir %q: %w", stateDir, err)
	}

	addr := getenv("POSTMAN_WAKA_ADDR", defaultAddr)
	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", c.handleHeartbeat)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("postman-wakatime listening on http://%s/heartbeat", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Print("shutting down postman-wakatime")
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return <-errCh
	}
}

// region Handle Heartbeat
func (c *Collector) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxHeartbeatBody)
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if p.RequestID == "" {
		p.RequestID = sha1hex([]byte(strings.Join(p.Location, "/") + "|" + p.URL))
	}
	if p.RequestName == "" {
		p.RequestName = "unknown"
	}

	project := detectProject(p)
	dir := c.entityDir(project, p.Location, p.RequestName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metaBytes, err := buildMetaFile(p, project)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bodyBytes, bodyExt, err := buildBodyFile(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metaHash := sha1hex(metaBytes)
	bodyHash := sha1hex(bodyBytes)

	metaFile := filepath.Join(dir, slug(p.RequestName)+".meta.json")
	bodyFile := filepath.Join(dir, slug(p.RequestName)+"."+bodyExt)
	runFile := filepath.Join(dir, slug(p.RequestName)+".http")

	if err := os.WriteFile(metaFile, metaBytes, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(bodyBytes) > 0 {
		if err := os.WriteFile(bodyFile, bodyBytes, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := os.WriteFile(runFile, []byte(fmt.Sprintf("%s %s\n", p.Method, p.URL)), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	snapPath := filepath.Join(c.stateDir, slug(p.RequestID)+".json")

	c.mu.Lock()
	defer c.mu.Unlock()

	prev, err := readSnapshot(snapPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	entity, language := selectEntity(prev, metaHash, bodyHash, bodyBytes, bodyExt, metaFile, bodyFile, runFile)
	if prev != nil && prev.LastEntity == entity && now.Sub(prev.LastSentAt) < c.minInterval {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"skipped": true,
			"reason":  "same entity within min interval",
			"entity":  entity,
			"project": project,
		})
		return
	}

	heartbeatTime := cappedHeartbeatTime(prev, p)
	if err := c.sendToWakaTime(entity, project, language, p, heartbeatTime); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	next := Snapshot{
		RequestID:   p.RequestID,
		RequestName: p.RequestName,
		Project:     project,
		MetaHash:    metaHash,
		BodyHash:    bodyHash,
		LastEntity:  entity,
		LastSentAt:  now,
	}
	if err := writeSnapshot(snapPath, &next); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"entity":   entity,
		"project":  project,
		"language": language,
	})
}

// region Select Entity
func selectEntity(prev *Snapshot, metaHash, bodyHash string, bodyBytes []byte, bodyExt, metaFile, bodyFile, runFile string) (entity, language string) {
	switch {
	case prev == nil:
		if len(bodyBytes) > 0 {
			return bodyFile, languageFromExt(bodyExt)
		}
		return metaFile, "JSON"
	case prev.BodyHash != bodyHash && len(bodyBytes) > 0:
		return bodyFile, languageFromExt(bodyExt)
	case prev.MetaHash != metaHash:
		return metaFile, "JSON"
	default:
		return runFile, "HTTP"
	}
}

// region Send To WakaTime
func cappedHeartbeatTime(prev *Snapshot, p Payload) float64 {
	if p.Time <= 0 {
		return 0
	}
	if prev == nil || !strings.EqualFold(p.Phase, "finish") {
		return p.Time
	}

	prevAt := float64(prev.LastSentAt.UnixNano()) / float64(time.Second)
	maxFinishAt := prevAt + maxRequestAge.Seconds()
	if p.Time > maxFinishAt {
		return maxFinishAt
	}
	return p.Time
}

func (c *Collector) sendToWakaTime(entity, project, language string, p Payload, heartbeatTime float64) error {
	args := []string{
		"--entity", entity,
		"--project", project,
		"--language", language,
		"--category", "coding",
		"--plugin", c.plugin,
	}

	if heartbeatTime > 0 {
		args = append(args, "--time", fmt.Sprintf("%.3f", heartbeatTime))
	}
	if p.IsWrite {
		args = append(args, "--write")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.wakaCLI, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("wakatime-cli timeout: %w", ctx.Err())
		}
		return fmt.Errorf("wakatime-cli failed: %w; output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// region Detect Project
func detectProject(p Payload) string {
	if len(p.Location) > 0 && strings.TrimSpace(p.Location[0]) != "" {
		return p.Location[0]
	}
	if host := hostFromURL(p.URL); host != "" {
		return host
	}
	return "postman"
}

// region Entity Dir
func (c *Collector) entityDir(project string, location []string, requestName string) string {
	parts := []string{c.root, slug(project)}
	if len(location) > 1 {
		for _, s := range location[1:] {
			if strings.TrimSpace(s) == "" {
				continue
			}
			parts = append(parts, slug(s))
		}
	} else {
		parts = append(parts, slug(requestName))
	}
	return filepath.Join(parts...)
}

// region Build Meta File
func buildMetaFile(p Payload, project string) ([]byte, error) {
	type meta struct {
		Project     string    `json:"project"`
		RequestID   string    `json:"requestId"`
		RequestName string    `json:"requestName"`
		Location    []string  `json:"location"`
		Method      string    `json:"method"`
		URL         string    `json:"url"`
		Headers     []Header  `json:"headers"`
		Auth        *Auth     `json:"auth,omitempty"`
		EventName   string    `json:"eventName,omitempty"`
		ExportedAt  time.Time `json:"exportedAt"`
		OS          string    `json:"os"`
	}

	headers := append([]Header(nil), p.Headers...)
	sort.Slice(headers, func(i, j int) bool {
		return headers[i].Key+headers[i].Value < headers[j].Key+headers[j].Value
	})

	return json.MarshalIndent(meta{
		Project:     project,
		RequestID:   p.RequestID,
		RequestName: p.RequestName,
		Location:    p.Location,
		Method:      p.Method,
		URL:         p.URL,
		Headers:     headers,
		Auth:        p.Auth,
		EventName:   p.EventName,
		ExportedAt:  time.Now(),
		OS:          runtime.GOOS,
	}, "", "  ")
}

// region Build Body File
func buildBodyFile(p Payload) ([]byte, string, error) {
	if p.Body == nil {
		return nil, "txt", nil
	}

	mode, _ := p.Body["mode"].(string)

	switch mode {
	case "raw":
		raw, _ := p.Body["raw"].(string)
		if raw == "" {
			return nil, "txt", nil
		}
		if pretty, ok := tryPrettyJSON(raw); ok {
			return pretty, "json", nil
		}
		return []byte(raw), "txt", nil
	case "graphql":
		b, err := marshalBodyJSON(p.Body)
		return b, "graphql.json", err
	case "urlencoded":
		b, err := marshalBodyJSON(p.Body)
		return b, "urlencoded.json", err
	case "formdata":
		b, err := marshalBodyJSON(p.Body)
		return b, "formdata.json", err
	case "file":
		b, err := marshalBodyJSON(p.Body)
		return b, "file.json", err
	default:
		b, err := marshalBodyJSON(p.Body)
		return b, "body.json", err
	}
}

func marshalBodyJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func languageFromExt(ext string) string {
	switch ext {
	case "json":
		return "JSON"
	case "graphql", "graphql.json":
		return "GraphQL"
	default:
		if strings.HasSuffix(ext, ".json") {
			return "JSON"
		}
		return "Text"
	}
}

func readSnapshot(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeSnapshot(path string, s *Snapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func hostFromURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	u, err := url.Parse(s)
	if err == nil {
		if u.Host != "" {
			return u.Hostname()
		}
		if u.Path != "" && !strings.Contains(u.Path, " ") {
			return strings.Split(u.Path, "/")[0]
		}
	}

	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func tryPrettyJSON(s string) ([]byte, bool) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return b, err == nil
}

func sha1hex(b []byte) string {
	sum := sha1.Sum(bytes.TrimSpace(b))
	return hex.EncodeToString(sum[:])
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func slug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "unknown"
	}
	return s
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
