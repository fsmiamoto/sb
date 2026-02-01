"""Fuzzy matching utilities for sandbox names."""

from __future__ import annotations

import re
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from sb.sandbox import SandboxInfo


def _extract_dirname(sandbox_name: str) -> str:
    """Extract the dirname portion from a sandbox name.

    Sandbox names follow the format: sb-{dirname}-{hash}
    where hash is 8 hex characters.

    Args:
        sandbox_name: The full sandbox name.

    Returns:
        The dirname portion, or the full name if pattern doesn't match.
    """
    # Match pattern: sb-{dirname}-{8-char-hash}
    match = re.match(r"^sb-(.+)-[a-f0-9]{8}$", sandbox_name)
    if match:
        return match.group(1)
    return sandbox_name


def _score_match(query: str, sandbox: SandboxInfo) -> int | None:
    """Score how well a query matches a sandbox.

    Lower scores are better matches. Returns None for no match.

    Matching priority:
    - 0: Exact match (query equals sandbox name)
    - 10: Prefix match (sandbox name starts with query)
    - 20: Dirname exact match (extracted dirname equals query)
    - 30: Dirname prefix (dirname starts with query)
    - 40: Dirname contains (dirname contains query)
    - 50: Substring (query appears anywhere in name)

    Args:
        query: The search query.
        sandbox: The sandbox to match against.

    Returns:
        Match score (lower is better), or None if no match.
    """
    name = sandbox.name
    query_lower = query.lower()
    name_lower = name.lower()

    # Exact match
    if query_lower == name_lower:
        return 0

    # Prefix match
    if name_lower.startswith(query_lower):
        return 10

    # Extract dirname for dirname-based matching
    dirname = _extract_dirname(name)
    dirname_lower = dirname.lower()

    # Dirname exact match
    if query_lower == dirname_lower:
        return 20

    # Dirname prefix match
    if dirname_lower.startswith(query_lower):
        return 30

    # Dirname contains
    if query_lower in dirname_lower:
        return 40

    # Substring match (query appears anywhere in full name)
    if query_lower in name_lower:
        return 50

    return None


def find_matching_sandboxes(query: str, sandboxes: list[SandboxInfo]) -> list[SandboxInfo]:
    """Find sandboxes matching a query, sorted by match quality.

    If an exact match exists, returns only that match.

    Args:
        query: The search query.
        sandboxes: List of sandboxes to search.

    Returns:
        List of matching sandboxes, sorted by match quality (best first).
    """
    scored: list[tuple[int, SandboxInfo]] = []

    for sandbox in sandboxes:
        score = _score_match(query, sandbox)
        if score is not None:
            scored.append((score, sandbox))

    # Sort by score (lower is better)
    scored.sort(key=lambda x: x[0])

    # If we have an exact match (score 0), return only that
    if scored and scored[0][0] == 0:
        return [scored[0][1]]

    return [sandbox for _, sandbox in scored]
