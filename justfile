# Default: list available recipes
default:
    @just --list

# Build the CLI binary into ./build/
build:
    mkdir -p build
    go build -o build/sb ./cmd/sb

# Install the CLI binary into GOBIN/GOPATH/bin
install:
    go install ./cmd/sb

# Run tests
test:
    go test ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Run linting (prefer golangci-lint when available)
lint:
    if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not found; falling back to go vet ./..." >&2; go vet ./...; fi

# Check for known vulnerabilities in dependencies
vulncheck:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Run all checks (lint + vulncheck + test)
check: lint vulncheck test

# Build the Docker sandbox image
docker-build:
    docker build -t sb-sandbox:latest assets/docker/

# Rebuild the Docker image from scratch (no cache)
docker-rebuild:
    docker build --no-cache -t sb-sandbox:latest assets/docker/

# Clean build artifacts
clean:
    rm -rf build/ dist/

# Bump version, create a git tag, and push it to trigger a release.
# Usage: just bump major|minor|patch
bump part:
    #!/usr/bin/env bash
    set -euo pipefail
    latest=$(git tag --list 'v*' --sort=-v:refname | head -n1)
    latest=${latest:-v0.0.0}
    IFS='.' read -r major minor patch <<< "${latest#v}"
    case "{{ part }}" in
        major) major=$((major + 1)); minor=0; patch=0 ;;
        minor) minor=$((minor + 1)); patch=0 ;;
        patch) patch=$((patch + 1)) ;;
        *) echo "Usage: just bump major|minor|patch" >&2; exit 1 ;;
    esac
    next="v${major}.${minor}.${patch}"
    echo "Current: ${latest}"
    echo "Next:    ${next}"
    read -rp "Tag and push ${next}? [y/N] " confirm
    [[ "$confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
    git tag -a "${next}" -m "Release ${next}"
    git push origin "${next}"
    echo "Pushed tag ${next} — release workflow will run automatically."
