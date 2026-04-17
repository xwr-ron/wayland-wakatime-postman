package main

import (
	"sync"
	"time"
)

type Header struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type Auth struct {
	Type string `json:"type"`
}

type Payload struct {
	Phase       string         `json:"phase"`
	RequestID   string         `json:"requestId"`
	RequestName string         `json:"requestName"`
	Location    []string       `json:"location"`
	Current     string         `json:"current"`
	EventName   string         `json:"eventName"`
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	Headers     []Header       `json:"headers"`
	Auth        *Auth          `json:"auth"`
	Body        map[string]any `json:"body"`
	Time        float64        `json:"time"`
	IsWrite     bool           `json:"isWrite"`
}

type Snapshot struct {
	RequestID   string    `json:"requestId"`
	RequestName string    `json:"requestName"`
	Project     string    `json:"project"`
	MetaHash    string    `json:"metaHash"`
	BodyHash    string    `json:"bodyHash"`
	LastEntity  string    `json:"lastEntity"`
	LastSentAt  time.Time `json:"lastSentAt"`
}

type Collector struct {
	root        string
	stateDir    string
	wakaCLI     string
	plugin      string
	minInterval time.Duration
	mu          sync.Mutex
}
