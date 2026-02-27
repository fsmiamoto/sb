"""Tests for sb.config module."""

from __future__ import annotations

import warnings
from pathlib import Path

import pytest

from sb.config import (
    DEFAULT_CONFIG,
    _expand_paths,
    get_default_config_path,
    load_config,
    merge_config,
)


class TestGetDefaultConfigPath:
    def test_returns_path(self):
        path = get_default_config_path()
        assert isinstance(path, Path)
        assert path.name == "config.toml"
        assert path.parent.name == "sb"


class TestExpandPaths:
    def test_expands_tilde(self):
        result = _expand_paths(["~/foo", "~/bar"])
        assert all(not p.startswith("~") for p in result)

    def test_preserves_absolute(self):
        result = _expand_paths(["/tmp/foo"])
        assert result == ["/tmp/foo"]

    def test_empty_list(self):
        assert _expand_paths([]) == []


class TestLoadConfig:
    def test_nonexistent_file_returns_defaults(self, tmp_path):
        config = load_config(tmp_path / "nonexistent.toml")
        assert config["defaults"]["extra_mounts"] == []
        assert config["defaults"]["env_passthrough"] == []
        assert config["defaults"]["sensitive_dirs"] == []
        assert config["docker"]["image"] is None

    def test_valid_config(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            '[defaults]\n'
            'env_passthrough = ["MY_VAR", "OTHER_VAR"]\n'
            '\n'
            '[docker]\n'
            'image = "custom:latest"\n'
        )
        config = load_config(config_file)
        assert config["defaults"]["env_passthrough"] == ["MY_VAR", "OTHER_VAR"]
        assert config["docker"]["image"] == "custom:latest"

    def test_partial_config_preserves_defaults(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text('[docker]\nimage = "custom:latest"\n')
        config = load_config(config_file)
        # Defaults preserved for unspecified sections
        assert config["defaults"]["extra_mounts"] == []
        assert config["docker"]["image"] == "custom:latest"

    def test_invalid_toml_warns_and_returns_defaults(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text("not valid toml [[[")
        with warnings.catch_warnings(record=True) as w:
            warnings.simplefilter("always")
            config = load_config(config_file)
            assert len(w) == 1
            assert "Failed to load config" in str(w[0].message)
        assert config["docker"]["image"] is None

    def test_extra_mounts_expanded(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            '[defaults]\nextra_mounts = ["~/my-configs"]\n'
        )
        config = load_config(config_file)
        paths = config["defaults"]["extra_mounts"]
        assert len(paths) == 1
        assert not paths[0].startswith("~")

    def test_string_path_accepted(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text('[docker]\nimage = "test:1"\n')
        config = load_config(str(config_file))
        assert config["docker"]["image"] == "test:1"

    def test_none_uses_default_path(self):
        # Should not raise — returns defaults if file doesn't exist
        config = load_config(None)
        assert "defaults" in config

    def test_ignores_invalid_types(self, tmp_path):
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            '[defaults]\n'
            'extra_mounts = "not-a-list"\n'
            'env_passthrough = 42\n'
        )
        config = load_config(config_file)
        # Invalid types are silently ignored, defaults preserved
        assert config["defaults"]["extra_mounts"] == []
        assert config["defaults"]["env_passthrough"] == []


class TestMergeConfig:
    def test_file_config_only(self):
        file_config = {
            "defaults": {
                "extra_mounts": ["/mnt/data"],
                "env_passthrough": ["MY_VAR"],
                "sensitive_dirs": ["/secret"],
            },
            "docker": {"image": "custom:latest"},
        }
        result = merge_config(file_config, {})
        assert result["extra_mounts"] == ["/mnt/data"]
        assert result["env_passthrough"] == ["MY_VAR"]
        assert result["sensitive_dirs"] == ["/secret"]
        assert result["image"] == "custom:latest"

    def test_cli_mounts_extend(self):
        file_config = {
            "defaults": {"extra_mounts": ["/mnt/data"]},
            "docker": {},
        }
        cli_args = {"mount": ["/mnt/other"]}
        result = merge_config(file_config, cli_args)
        assert len(result["extra_mounts"]) == 2
        assert "/mnt/data" in result["extra_mounts"]

    def test_cli_env_extends(self):
        file_config = {
            "defaults": {"env_passthrough": ["VAR1"]},
            "docker": {},
        }
        cli_args = {"env": ["VAR2"]}
        result = merge_config(file_config, cli_args)
        assert result["env_passthrough"] == ["VAR1", "VAR2"]

    def test_cli_image_overrides(self):
        file_config = {
            "defaults": {},
            "docker": {"image": "file-image:latest"},
        }
        cli_args = {"image": "cli-image:v2"}
        result = merge_config(file_config, cli_args)
        assert result["image"] == "cli-image:v2"

    def test_empty_config_and_args(self):
        result = merge_config({"defaults": {}, "docker": {}}, {})
        assert result["extra_mounts"] == []
        assert result["env_passthrough"] == []
        assert result["sensitive_dirs"] == []
        assert result["image"] is None

    def test_missing_sections_handled(self):
        result = merge_config({}, {})
        assert result["extra_mounts"] == []
        assert result["image"] is None
