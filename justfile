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

# Run all checks (vet + lint + test)
check: lint test

# Build the Docker sandbox image
docker-build:
    docker build -t sb-sandbox:latest assets/docker/

# Rebuild the Docker image from scratch (no cache)
docker-rebuild:
    docker build --no-cache -t sb-sandbox:latest assets/docker/

# Clean build artifacts
clean:
    rm -rf build/ dist/
