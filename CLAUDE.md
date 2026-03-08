# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`sb` is a CLI tool for managing Docker sandbox containers designed for coding agents. It creates isolated development environments with workspace directory mounts and pre-configured tools (Claude Code, Codex, etc.). Written in Go, it compiles to a single static binary with all Docker assets (Dockerfile, shell configs) embedded via `go:embed`.

## Build & Development Commands

```bash
# Build the binary into ./build/
just build              # or: go build -o build/sb ./cmd/sb

# Install into GOBIN
just install            # or: go install ./cmd/sb

# Run tests
just test               # or: go test ./...
just test-race          # or: go test -race ./...

# Lint (golangci-lint preferred, falls back to go vet)
just lint

# Run all checks (lint + test)
just check

# Build/rebuild the Docker sandbox image directly
just docker-build
just docker-rebuild

# Clean build artifacts
just clean

# Run the CLI
sb --help
sb create               # Create sandbox for current directory
sb attach [name]        # Attach to sandbox (auto-starts if stopped)
sb stop [name]          # Stop a running sandbox
sb destroy [name]       # Remove sandbox completely
sb list                 # List all sandboxes with status
```

## Architecture

### Directory Layout

```
cmd/sb/              CLI entrypoint (urfave/cli v2 app)
  main.go            Commands: create, attach, stop, destroy, list
  completion.go      Shell completion generation (bash/zsh/fish)

internal/
  naming/            Deterministic sandbox name generation (sb-{dirname}-{hash})
  config/            TOML config loading from ~/.config/sb/config.toml
  matching/          Fuzzy name matching with priority scoring (0–50)
  sandbox/
    types.go         Shared types: SandboxInfo, MountSpec, constants
    client.go        Lazy Docker client initialization with ping check
    image.go         Image management: build from embedded assets or pull custom
    mounts.go        Bind mount assembly (workspace rw, config dirs ro)
    manager.go       Container lifecycle: Create, Attach, Stop, Destroy, List
    shell.go         Interactive shell exec via docker CLI, shell config manager

assets/
  embed.go           go:embed directives exposing docker/ as embed.FS
  docker/
    Dockerfile       Arch Linux image with dev tools
    entrypoint.sh    Container entrypoint with user setup
    configs/         Default shell configs (zshrc, starship.toml, nvim/)
```

### Core Components

- **cmd/sb/main.go**: urfave/cli v2 app with subcommands and aliases (`c`→create, `a`→attach, `d`→destroy). Global `--config` flag. Confirmation prompts via stderr/stdin.

- **internal/sandbox/manager.go**: `SandboxManager` — core business logic for container lifecycle management. Uses Docker labels (`sb.managed`, `sb.name`, `sb.workspace`) as source of truth. Dependency-injected for testability.

- **internal/naming/naming.go**: `SanitizeDirname()` and `GenerateName()` — deterministic names from paths using `sb-{sanitized_dirname}-{sha256[:8]}`. Hash-compatible with the original Python implementation.

- **internal/config/config.go**: TOML config from `~/.config/sb/config.toml` via BurntSushi/toml. `LoadConfig()` returns defaults on missing file. `MergeConfig()` applies CLI overrides with precedence.

- **internal/matching/matching.go**: `ScoreMatch()` and `FindMatchingSandboxes()` — fuzzy name resolution for attach/stop/destroy commands. Generic over `NamedSandbox` interface.

- **internal/sandbox/image.go**: `ImageManager` — builds the bundled image from embedded Dockerfile/context or pulls a custom image from registry.

- **assets/embed.go**: `go:embed docker/*` bundles Dockerfile, entrypoint.sh, and shell configs into the binary. Zero runtime file dependencies.

### Key Patterns

- Sandbox names are auto-generated from workspace paths, ensuring one sandbox per directory
- Workspace is mounted read-write at `/workspace`; config mounts (e.g., `~/.ssh`) are read-only by default
- Container runs as current user's UID:GID for file permission compatibility
- Docker is the single source of truth: sandbox metadata stored via container labels
- All Docker assets are embedded in the binary via `go:embed` — no external files needed at runtime
- Packages under `internal/` are private — nothing is a public library
- `SandboxManager` uses dependency injection (function fields) for Docker client, image manager, mount builder, and shell exec — enables unit testing without a Docker daemon

### Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/urfave/cli/v2` | CLI framework with subcommands and aliases |
| `github.com/docker/docker` | Official Go Docker SDK for container operations |
| `github.com/BurntSushi/toml` | TOML config file parsing |
| `charm.land/lipgloss/v2` | Colored terminal table output for `sb list` |
