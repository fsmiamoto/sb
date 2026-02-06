# Default: list available recipes
default:
    @just --list

# Install in development mode
install:
    pip install -e .

# Build the Docker sandbox image
build:
    docker build -t sb-sandbox:latest sb/docker/

# Rebuild the Docker image from scratch (no cache)
rebuild:
    docker build --no-cache -t sb-sandbox:latest sb/docker/

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

# Run all checks (lint + format check)
check: lint fmt-check

# Clean build artifacts
clean:
    rm -rf build/ dist/ *.egg-info sb.egg-info __pycache__ sb/__pycache__
