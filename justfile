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
