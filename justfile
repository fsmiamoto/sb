# Default: list available recipes
default:
    @just --list

# Install in development mode
install:
    pip install -e .

# Build the Go CLI binary from cmd/sb into ./build/
go-build:
    mkdir -p build
    go build -o build/sb ./cmd/sb

# Install the Go CLI binary into GOBIN/GOPATH/bin
go-install:
    go install ./cmd/sb

# Run Go tests
go-test:
    go test ./...

# Run Go linting (prefer golangci-lint when available)
go-lint:
    if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not found; falling back to go vet ./..." >&2; go vet ./...; fi

# Build the Docker sandbox image
build:
    docker build -t sb-sandbox:latest assets/docker/

# Rebuild the Docker image from scratch (no cache)
rebuild:
    docker build --no-cache -t sb-sandbox:latest assets/docker/

# Run linting with ruff
lint:
    ruff check sb/

# Auto-fix lint issues
lint-fix:
    ruff check --fix sb/

# Format code with ruff
fmt:
    ruff format sb/

# Check formatting without modifying
fmt-check:
    ruff format --check sb/

# Run tests
test *args:
    .venv/bin/pytest {{args}}

# Run tests with coverage
test-cov:
    .venv/bin/coverage run -m pytest
    .venv/bin/coverage report

# Run all checks (lint + format check + tests)
check: lint fmt-check test

# Clean build artifacts
clean:
    rm -rf build/ dist/ *.egg-info sb.egg-info __pycache__ sb/__pycache__
