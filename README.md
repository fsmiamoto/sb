# sb

A CLI tool for managing Docker sandbox containers designed for coding agents. Creates isolated development environments with workspace mounts and pre-configured tools (Claude Code, Codex, etc.).

## Why?

Running coding agents directly on your system means giving them access to everything. `sb` creates isolated Docker containers where agents can only access the specific project directory you choose, while still having access to your API keys and credentials.

## Installation

```bash
pip install -e .
```

Requires Docker to be installed and running.

## Quick Start

```bash
# Create a sandbox for your current project
cd ~/projects/my-app
sb create --attach

# You're now in an isolated container with:
# - Your project mounted at ~/workspace
# - Claude Code and Codex pre-installed
# - Your API keys and git config available
```

## Commands

```bash
sb create           # Create sandbox for current directory
sb attach [name]    # Attach to sandbox (auto-starts if stopped)
sb stop [name]      # Stop a running sandbox
sb destroy [name]   # Remove sandbox completely
sb list             # List all sandboxes with status
```

All commands operate on the current directory by default. Use `--name` to target a specific sandbox.

### Create Options

```bash
sb create --attach              # Create and immediately attach
sb create --force               # Skip confirmation for sensitive directories
sb create --mount ~/.npmrc      # Add extra file/directory mount
sb create --env MY_VAR          # Pass through environment variable
sb create --image my-image:tag  # Use custom Docker image
```

## Configuration

Create `~/.config/sb/config.toml` for persistent settings:

```toml
[defaults]
extra_mounts = ["~/.npmrc", "~/.cargo/config.toml"]
env_passthrough = ["ANTHROPIC_API_KEY", "OPENAI_API_KEY"]

[docker]
image = "custom-image:latest"
```

CLI arguments override config file settings.

## What's Inside the Sandbox

The Docker image is Arch Linux-based and includes:

- **Languages:** Python, Node.js, Go, Rust
- **Coding Agents:** Claude Code (`claude`), Codex (`codex`)
- **Tools:** git, neovim, ripgrep, fd, jq, lazygit
- **Shell:** zsh with syntax highlighting and starship prompt

## Mounts

| Host Path | Container Path | Access |
|-----------|---------------|--------|
| Current directory | `~/workspace` | read-write |
| `~/.claude/` | `~/.claude/` | read-write |
| `~/.claude.json` | `~/.claude.json` | read-write |
| `~/.codex/` | `~/.codex/` | read-write |
| `~/.gitconfig` | `~/.gitconfig` | read-only |

Additional mounts can be added via `--mount` or config file.

## How It Works

1. **Deterministic naming:** Each directory gets a unique sandbox name based on its path (`sb-{dirname}-{hash}`)
2. **UID/GID mapping:** Container runs as your user ID, so files created inside have correct ownership
3. **Docker labels:** Sandbox metadata stored on containers themselves—no external state files
4. **Sensitive directory protection:** Warns before creating sandboxes for system directories

## Requirements

- Python 3.9+
- Docker

## License

MIT
