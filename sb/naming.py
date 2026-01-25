"""Sandbox naming utilities."""

import hashlib
import os
import re


def sanitize_dirname(name: str) -> str:
    """Remove special characters from a directory name.

    Keeps alphanumeric characters, hyphens, and underscores.
    Converts to lowercase and replaces spaces with hyphens.

    Args:
        name: The directory name to sanitize.

    Returns:
        A sanitized version of the name suitable for use in sandbox names.
    """
    # Convert to lowercase and replace spaces with hyphens
    name = name.lower().replace(" ", "-")
    # Remove any characters that aren't alphanumeric, hyphen, or underscore
    name = re.sub(r"[^a-z0-9\-_]", "", name)
    # Collapse multiple hyphens into one
    name = re.sub(r"-+", "-", name)
    # Remove leading/trailing hyphens
    name = name.strip("-")
    # If empty after sanitization, use a default
    return name or "sandbox"


def generate_name(path: str) -> str:
    """Generate a sandbox name from a filesystem path.

    Format: sb-{dirname}-{hash[:8]}

    Args:
        path: The filesystem path (typically a workspace directory).

    Returns:
        A unique sandbox name based on the path.

    Example:
        >>> generate_name("/home/user/projects/my-app")
        'sb-my-app-a1b2c3d4'
    """
    # Get absolute path for consistent hashing
    abs_path = os.path.abspath(path)

    # Extract the directory name
    dirname = os.path.basename(abs_path)
    sanitized = sanitize_dirname(dirname)

    # Generate hash of the full absolute path
    path_hash = hashlib.sha256(abs_path.encode()).hexdigest()[:8]

    return f"sb-{sanitized}-{path_hash}"
