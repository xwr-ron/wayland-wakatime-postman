# Postman → WakaTime 

[![Go](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go&logoColor=white)](#)
[![Platform](https://img.shields.io/badge/platform-Linux-333?logo=linux&logoColor=white)](#)
[![Session](https://img.shields.io/badge/session-systemd%20--user-6C2DC7)](#)
[![WM](https://img.shields.io/badge/Wayland-Hyprland-58E1FF)](#)
[![Tracking](https://img.shields.io/badge/tracking-Postman%20%E2%86%92%20WakaTime-000000)](#)

Локальный collector, который отправляет активность из **Postman** в **WakaTime** без `wakatime-desktop`.

A local collector that sends **Postman** activity to **WakaTime** without `wakatime-desktop`.

---

## Navigation

- [Русская версия](#русская-версия)
- [English version](#english-version)

---
## Table of contents

- [What it is](#what-it-is)
- [Why it exists](#why-it-exists)
- [Features](#features)
- [Quick start](#quick-start)
- [How project and entity are detected](#how-project-and-entity-are-detected)
- [Installation](#installation)
- [Environment variables](#environment-variables)
- [Debugging and troubleshooting](#debugging-and-troubleshooting)
- [Limitations](#limitations)

## What it is

`postman-wakatime` is a local HTTP collector that receives events from **Postman scripts**, converts them into heartbeat calls for `wakatime-cli`, and sends them to **WakaTime**.

It is especially useful in environments where desktop tracking is either unreliable or simply the wrong approach:

- **Arch Linux**
- **Hyprland**
- **Wayland**
- Linux sessions where **X11-oriented** desktop watchers are not a good fit

## Why it exists

Typical `wakatime-desktop` usage is based on desktop/window tracking. That is often not ideal for API-heavy workflows in Postman.

This project takes a different approach:

- it does not track the active window;
- it does not depend on X11/XWayland;
- it receives events directly from Postman;
- it can count **response waiting time** as work time (capped at 60 seconds per request);
- it can run as a **systemd --user service**.

## Features

- sends WakaTime heartbeats on behalf of **Postman**;
- detects the **project** from `pm.execution.location`;
- builds a **mirrored file structure** on disk;
- distinguishes changes in:
  - request **body**;
  - request **meta** (`url`, `method`, `headers`, `auth`);
  - plain request execution;
- counts **response waiting time** as work time (capped at 60 seconds per request);
- runs through **systemd --user**;
- does not depend on desktop watchers.

## Quick start

### 1. Build the binary

```bash
mkdir -p ~/.local/bin
go build -o ~/.local/bin/postman-wakatime /path/to/main.go
chmod +x ~/.local/bin/postman-wakatime
```

### 2. Create the env file

File `~/.config/postman-wakatime.env`:

```bash
POSTMAN_WAKA_ADDR=127.0.0.1:8765
POSTMAN_WAKA_ROOT=/home/<user>/.local/share/postman-wakatime
WAKATIME_PLUGIN=postman/0.1.0
WAKATIME_CLI=wakatime-cli
```

### 3. Create the user service

File `~/.config/systemd/user/postman-wakatime.service`:


### 4. Enable the service

```bash
systemctl --user daemon-reload
systemctl --user enable --now postman-wakatime.service
```

### 5. Verify it is running

```bash
systemctl --user status postman-wakatime.service
journalctl --user -u postman-wakatime.service -f
ss -ltnp | grep 8765
```

### 6. Add Postman scripts

You need:

- **Pre-request Script**
- **Post-response Script / Tests**

The recommended place is the **collection level** so the logic applies to all requests.

```text
Postman Pre-request Script   ─┐
                              ├──> localhost collector ───> wakatime-cli ───> WakaTime
Postman Post-response Script ─┘
```

### Time accounting

The collector sends **two heartbeats**:

1. when the request starts;
2. when the request finishes.

Example:

- request sent at `12:00:00`
- response received at `12:00:15`

As a result, the waiting interval between those two points is counted as work time.

If a single request takes longer than 60 seconds, only 60 seconds are credited.

### Entity selection

The collector stores mirrored files and selects the entity based on what changed in the request:

- `order-list.json` — body changed;
- `order-list.meta.json` — `url`, `method`, `headers`, or `auth` changed;
- `order-list.http` — request was simply executed.

## How project and entity are detected

### Project

By default, project detection works like this:

1. first item of `pm.execution.location`;
2. if unavailable, host from the URL;
3. otherwise fallback to `postman`.

Example:

```text
["axis", "orders", "order-list"]
```

Project:

```text
axis
```

### Entity

Example mirrored structure:

```text
~/.local/share/postman-wakatime/
└── axis/
    └── orders/
        └── order-list/
            ├── order-list.json
            ├── order-list.meta.json
            └── order-list.http
```

## Installation

### Requirements

- Go 1.21+
- installed `wakatime-cli`
- configured `~/.wakatime.cfg`
- Postman with collection/request script support
- Linux with a user systemd session

### Build

```bash
go build -o ~/.local/bin/postman-wakatime ./cmd/postman-wakatime
```

If your code is still in one file:

```bash
go build -o ~/.local/bin/postman-wakatime /path/to/main.go
```

## Environment variables

| Variable            | Description                  | Default                           |
| ------------------- | ---------------------------- | --------------------------------- |
| `POSTMAN_WAKA_ADDR` | collector HTTP address       | `127.0.0.1:8765`                  |
| `POSTMAN_WAKA_ROOT` | mirrored file structure root | `~/.local/share/postman-wakatime` |
| `WAKATIME_PLUGIN`   | editor/plugin identifier     | `postman/0.1.0`                   |
| `WAKATIME_CLI`      | path to `wakatime-cli`       | `wakatime-cli`                    |

## Debugging and troubleshooting

### Service status

```bash
systemctl --user enable postman-wakatime.service
systemctl --user start postman-wakatime.service
systemctl --user status postman-wakatime.service
```

### Logs

```bash
journalctl --user -u postman-wakatime.service -f
```

### Port check

```bash
ss -ltnp | grep 8765
```

### Manual collector test

```bash
curl -X POST http://127.0.0.1:8765/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{
    "phase": "start",
    "requestId": "test-id",
    "requestName": "order-list",
    "location": ["axis", "orders", "order-list"],
    "current": "order-list",
    "eventName": "prerequest",
    "method": "POST",
    "url": "https://example.com/orders",
    "headers": [],
    "auth": null,
    "body": {"mode": "raw", "raw": "{\"id\":1}"},
    "time": 1713000000,
    "isWrite": false
  }'
```

### Common issues

#### 1. WakaTime does not show the editor as `Postman`

Try changing `WAKATIME_PLUGIN`:

```bash
WAKATIME_PLUGIN=postman/0.1.0
```

or use a different identifier.

#### 2. Heartbeats are not being sent

Check:

- whether `postman-wakatime.service` is running;
- whether `wakatime-cli` is installed;
- whether `~/.wakatime.cfg` is valid;
- whether the collector address matches the one used in the Postman scripts.

#### 3. `%h` is not expanded

Sometimes absolute paths are simpler:

```bash
POSTMAN_WAKA_ROOT=/home/<user>/.local/share/postman-wakatime
```

#### 4. Waiting time is not counted

Make sure that:

- `phase: "start"` is sent;
- `phase: "finish"` is sent;
- `responseTimeMs` is actually present in the Postman payload.

## Limitations

At the moment, the collector **cannot reliably track Postman script source code** as separate entities such as:

- `order-list.pre-request.js`
- `order-list.tests.js`

So the idea “body changed → `order-list.json`, script changed → `order-list.js`” is only partially implemented for now.

What already works:

- body tracking;
- meta tracking;
- execution/wait-time accounting;
- project/entity mapping.
