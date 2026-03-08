# homebrew-tap

Homebrew tap for [sb](https://github.com/fsmiamoto/sb) — a CLI tool for managing Docker sandbox containers for coding agents.

## Installation

```bash
brew tap fsmiamoto/tap
brew install sb
```

Or install directly:

```bash
brew install fsmiamoto/tap/sb
```

## Requirements

- Docker must be installed and running

## How it works

This tap is automatically updated by [GoReleaser](https://goreleaser.com/) when a new version of `sb` is released. The Cask formula in `Casks/sb.rb` is generated — do not edit it manually.

## Upgrading

```bash
brew upgrade sb
```

## Uninstalling

```bash
brew uninstall sb
brew untap fsmiamoto/tap
```
