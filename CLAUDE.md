# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`sb` is a CLI tool for managing Docker sandbox containers designed for coding agents. It creates isolated development environments with workspace directory mounts and pre-configured tools (Claude Code, Codex, etc.).

## Build & Development Commands

```bash
# Install in development mode
pip install -e .

# Run the CLI
sb --help
sb create           # Create sandbox for current directory
sb attach [name]    # Attach to sandbox (auto-starts if stopped)
sb stop [name]      # Stop a running sandbox
sb destroy [name]   # Remove sandbox completely
sb list             # List all sandboxes with status
```

## Architecture

### Core Components

- **sb/sandbox.py**: `SandboxManager` class - core business logic for container lifecycle management (create, attach, stop, destroy). Uses Docker labels for sandbox metadata.

- **sb/cli.py**: Click-based CLI that wraps `SandboxManager`. All commands operate on current directory by default unless `--name` specified.

- **sb/naming.py**: Generates deterministic sandbox names from paths using format `sb-{sanitized_dirname}-{path_hash[:8]}`.

- **sb/config.py**: TOML config loading from `~/.config/sb/config.toml`. Merges file config with CLI args (CLI takes precedence).

- **sb/docker/Dockerfile**: Arch Linux-based image with dev tools (Python, Node, Go, Rust, Claude Code, Codex).

### Key Patterns

- Sandbox names are auto-generated from workspace paths, ensuring one sandbox per directory
- Workspace is mounted read-write at `/workspace`; all other mounts (config dirs) are read-only
- Container runs as current user's UID:GID for file permission compatibility
- Docker is the single source of truth: sandbox metadata stored via container labels (`sb.managed`, `sb.name`, `sb.workspace`)
