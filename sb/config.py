"""Configuration loading and merging for sb."""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Any

# Use tomllib for Python 3.11+, tomli for earlier versions
if sys.version_info >= (3, 11):
    import tomllib
else:
    import tomli as tomllib


# Default configuration values
DEFAULT_CONFIG: dict[str, Any] = {
    "defaults": {
        "extra_mounts": [],
        "env_passthrough": [],
        "sensitive_dirs": [],
    },
    "docker": {
        "image": None,  # None means use built-in image
    },
}


def get_default_config_path() -> Path:
    """Return the default configuration file path.

    Returns:
        Path to ~/.config/sb/config.toml
    """
    return Path.home() / ".config" / "sb" / "config.toml"


def _expand_paths(paths: list[str]) -> list[str]:
    """Expand ~ in a list of paths.

    Args:
        paths: List of paths that may contain ~.

    Returns:
        List of paths with ~ expanded to the user's home directory.
    """
    return [os.path.expanduser(p) for p in paths]


def load_config(path: Path | str | None = None) -> dict[str, Any]:
    """Load configuration from a TOML file.

    Args:
        path: Path to the config file. If None, uses the default path.

    Returns:
        Dictionary with configuration values. Returns default configuration
        if the file does not exist or cannot be read.
    """
    if path is None:
        path = get_default_config_path()
    elif isinstance(path, str):
        path = Path(os.path.expanduser(path))

    # Start with default configuration
    config: dict[str, Any] = {
        "defaults": dict(DEFAULT_CONFIG["defaults"]),
        "docker": dict(DEFAULT_CONFIG["docker"]),
    }

    # If file doesn't exist, return defaults
    if not path.exists():
        return config

    # Try to load the file
    try:
        with open(path, "rb") as f:
            file_config = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError):
        # If file can't be read or parsed, return defaults
        return config

    # Merge file config into defaults
    if "defaults" in file_config:
        defaults = file_config["defaults"]
        if "extra_mounts" in defaults and isinstance(defaults["extra_mounts"], list):
            config["defaults"]["extra_mounts"] = _expand_paths(defaults["extra_mounts"])
        if "env_passthrough" in defaults and isinstance(
            defaults["env_passthrough"], list
        ):
            config["defaults"]["env_passthrough"] = list(defaults["env_passthrough"])
        if "sensitive_dirs" in defaults and isinstance(
            defaults["sensitive_dirs"], list
        ):
            config["defaults"]["sensitive_dirs"] = _expand_paths(
                defaults["sensitive_dirs"]
            )

    if "docker" in file_config:
        docker = file_config["docker"]
        if "image" in docker and isinstance(docker["image"], str):
            config["docker"]["image"] = docker["image"]

    return config


def merge_config(
    file_config: dict[str, Any],
    cli_args: dict[str, Any],
) -> dict[str, Any]:
    """Merge file configuration with CLI arguments.

    CLI arguments take precedence over file configuration.

    Args:
        file_config: Configuration loaded from file.
        cli_args: Arguments from the command line. Expected keys:
            - mount: list of additional mount paths
            - env: list of environment variable names
            - image: Docker image override

    Returns:
        Merged configuration dictionary with the following structure:
            - extra_mounts: list of mount paths (expanded)
            - env_passthrough: list of env var names
            - sensitive_dirs: list of sensitive directory paths (expanded)
            - image: Docker image name or None
    """
    # Extract file config values
    defaults = file_config.get("defaults", {})
    docker = file_config.get("docker", {})

    # Start with file config values
    extra_mounts = list(defaults.get("extra_mounts", []))
    env_passthrough = list(defaults.get("env_passthrough", []))
    sensitive_dirs = list(defaults.get("sensitive_dirs", []))
    image = docker.get("image")

    # CLI mounts extend file config mounts
    cli_mounts = cli_args.get("mount", [])
    if cli_mounts:
        extra_mounts.extend(_expand_paths(cli_mounts))

    # CLI env vars extend file config env vars
    cli_env = cli_args.get("env", [])
    if cli_env:
        env_passthrough.extend(cli_env)

    # CLI image overrides file config image
    cli_image = cli_args.get("image")
    if cli_image:
        image = cli_image

    return {
        "extra_mounts": extra_mounts,
        "env_passthrough": env_passthrough,
        "sensitive_dirs": sensitive_dirs,
        "image": image,
    }
