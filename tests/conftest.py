"""Shared test fixtures."""

from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from sb.sandbox import SandboxInfo, SandboxManager


@pytest.fixture
def mock_docker_client():
    """Provide a mock Docker client that skips real Docker connections."""
    client = MagicMock()
    client.ping.return_value = True
    client.containers.list.return_value = []
    return client


@pytest.fixture
def mock_manager(mock_docker_client):
    """Provide a SandboxManager with an injected mock Docker client."""
    return SandboxManager(_client=mock_docker_client)


@pytest.fixture
def sample_sandboxes():
    """Provide a list of sample SandboxInfo objects for matching tests."""
    return [
        SandboxInfo(
            name="sb-my-app-a1b2c3d4",
            workspace="/home/user/projects/my-app",
            created_at="2025-01-01T00:00:00Z",
            container_id="abc123",
        ),
        SandboxInfo(
            name="sb-web-frontend-e5f6a7b8",
            workspace="/home/user/projects/web-frontend",
            created_at="2025-01-02T00:00:00Z",
            container_id="def456",
        ),
        SandboxInfo(
            name="sb-api-server-c9d0e1f2",
            workspace="/home/user/projects/api-server",
            created_at="2025-01-03T00:00:00Z",
            container_id="ghi789",
        ),
    ]
