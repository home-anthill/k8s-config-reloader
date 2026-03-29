# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

k8s-config-reloader is a single-binary Go sidecar for Kubernetes that watches config directories for file changes (using fsnotify) and sends a configurable signal (default: SIGHUP) to a target process to trigger a reload. Part of the [home-anthill](https://github.com/home-anthill/docs) project.

## Build & Development Commands

```bash
# Install required tools (staticcheck, shadow, air, go-cover-treemap) and update deps
make deps

# Build (runs vet + lint first, outputs to ./build/k8s-config-reloader)
make build

# Lint only
make lint        # runs staticcheck
make vet         # runs go vet + shadow

# Run tests with race detection and coverage (runs vet + lint first)
make test        # outputs coverage to ./coverage/

# Run locally with hot-reload (uses air)
PROCESS_NAME=<name> CONFIG_DIR=./config-folder make run
```

Tests use [Ginkgo v2](https://onsi.github.io/ginkgo/) and [Gomega](https://onsi.github.io/gomega/) (`github.com/onsi/ginkgo/v2`, `github.com/onsi/gomega`).

## Environment Variables (runtime)

| Variable | Required | Description |
|---|---|---|
| `CONFIG_DIR` | Yes | Comma-separated list of directories to watch |
| `PROCESS_NAME` | Yes | Name of the process to signal on config change |
| `RELOAD_SIGNAL` | No | Signal name (e.g. `SIGHUP`, `SIGUSR1`). Defaults to `SIGHUP` |

## Architecture

Single-file Go application (`main.go`). The flow is:
1. `parseConfig()` reads env vars and returns a `Config` struct
2. `main()` creates an fsnotify watcher and calls `run()`
3. `run()` watches directories; on Write or Remove events → debounces → `ProcessReloader.Reload()` finds the target process by name via gopsutil → sends the configured signal

Key interfaces (`Watcher`, `ProcessReloader`) allow dependency injection for testing without real filesystem watchers or processes.

Key dependencies: `fsnotify` (file watching), `gopsutil` (process lookup), `golang.org/x/sys/unix` (signal resolution).

Docker image uses a multi-stage build: Go builder → hardened `dhi.io/golang:1-alpine3.23` runtime image.
