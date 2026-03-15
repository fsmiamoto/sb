package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGetDefaultConfigPath(t *testing.T) {
	t.Parallel()

	path := GetDefaultConfigPath()
	if filepath.Base(path) != "config.toml" {
		t.Fatalf("GetDefaultConfigPath() base = %q, want %q", filepath.Base(path), "config.toml")
	}
	if filepath.Base(filepath.Dir(path)) != "sb" {
		t.Fatalf("GetDefaultConfigPath() parent = %q, want %q", filepath.Base(filepath.Dir(path)), "sb")
	}
}

func TestExpandPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("expands tilde", func(t *testing.T) {
		result := expandPaths([]string{"~/foo", "~/bar"})
		want := []string{filepath.Join(home, "foo"), filepath.Join(home, "bar")}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("expandPaths returned %v, want %v", result, want)
		}
	})

	t.Run("preserves absolute", func(t *testing.T) {
		result := expandPaths([]string{"/tmp/foo"})
		want := []string{"/tmp/foo"}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("expandPaths returned %v, want %v", result, want)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		result := expandPaths([]string{})
		if len(result) != 0 {
			t.Fatalf("expandPaths returned %v, want empty slice", result)
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("nonexistent file returns defaults", func(t *testing.T) {
		config, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.toml"))
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}
		assertConfigEqual(t, config, DefaultConfig())
	})

	t.Run("valid config", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "[defaults]\nenv_passthrough = [\"MY_VAR\", \"OTHER_VAR\"]\n\n[docker]\nimage = \"custom:latest\"\n")

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Defaults.EnvPassthrough = []string{"MY_VAR", "OTHER_VAR"}
		want.Docker.Image = "custom:latest"
		assertConfigEqual(t, config, want)
	})

	t.Run("partial config preserves defaults", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "[docker]\nimage = \"custom:latest\"\n")

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Docker.Image = "custom:latest"
		assertConfigEqual(t, config, want)
	})

	t.Run("directory path returns defaults and error", func(t *testing.T) {
		dir := t.TempDir()
		config, err := LoadConfig(dir)
		if err == nil {
			t.Fatal("LoadConfig should return an error when path is a directory")
		}
		if !strings.Contains(err.Error(), "path is a directory") {
			t.Fatalf("LoadConfig error = %q, want message containing %q", err.Error(), "path is a directory")
		}
		assertConfigEqual(t, config, DefaultConfig())
	})

	t.Run("invalid toml returns defaults and error", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "not valid toml [[[")

		config, err := LoadConfig(configFile)
		if err == nil {
			t.Fatal("LoadConfig should return an error for invalid TOML")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "failed to load config") {
			t.Fatalf("LoadConfig error = %q, want message containing %q", err.Error(), "failed to load config")
		}
		assertConfigEqual(t, config, DefaultConfig())
	})

	t.Run("extra mounts expanded", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "[defaults]\nextra_mounts = [\"~/my-configs\"]\n")

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Defaults.ExtraMounts = []string{filepath.Join(home, "my-configs")}
		assertConfigEqual(t, config, want)
	})

	t.Run("tilde path is accepted", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		configDir := filepath.Join(home, "configs")
		configFile := filepath.Join(configDir, "sb.toml")
		writeFile(t, configFile, "[docker]\nimage = \"test:1\"\n")

		config, err := LoadConfig(filepath.Join("~", "configs", "sb.toml"))
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Docker.Image = "test:1"
		assertConfigEqual(t, config, want)
	})

	t.Run("empty path uses default path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		configFile := filepath.Join(home, ".config", "sb", "config.toml")
		writeFile(t, configFile, "[docker]\nimage = \"default:path\"\n")

		config, err := LoadConfig("")
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Docker.Image = "default:path"
		assertConfigEqual(t, config, want)
	})

	t.Run("sensitive dirs expanded", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "[defaults]\nsensitive_dirs = [\"~/important\", \"/opt/data\"]\n")

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Defaults.SensitiveDirs = []string{filepath.Join(home, "important"), "/opt/data"}
		assertConfigEqual(t, config, want)
	})

	t.Run("all defaults fields together", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, `[defaults]
extra_mounts = ["~/mounts/a"]
env_passthrough = ["TOKEN", "SECRET"]
sensitive_dirs = ["~/critical"]

[docker]
image = "myimage:v1"
`)

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		want := DefaultConfig()
		want.Defaults.ExtraMounts = []string{filepath.Join(home, "mounts/a")}
		want.Defaults.EnvPassthrough = []string{"TOKEN", "SECRET"}
		want.Defaults.SensitiveDirs = []string{filepath.Join(home, "critical")}
		want.Docker.Image = "myimage:v1"
		assertConfigEqual(t, config, want)
	})

	t.Run("ignores invalid types", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, configFile, "[defaults]\nextra_mounts = \"not-a-list\"\nenv_passthrough = 42\n")

		config, err := LoadConfig(configFile)
		if err != nil {
			t.Fatalf("LoadConfig returned unexpected error: %v", err)
		}

		assertConfigEqual(t, config, DefaultConfig())
	})
}

func TestMergeConfig(t *testing.T) {
	t.Parallel()

	t.Run("file config only", func(t *testing.T) {
		fileConfig := Config{
			Defaults: DefaultsConfig{
				ExtraMounts:    []string{"/mnt/data"},
				EnvPassthrough: []string{"MY_VAR"},
				SensitiveDirs:  []string{"/secret"},
			},
			Docker: DockerConfig{Image: "custom:latest"},
		}

		result := MergeConfig(fileConfig, CLIArgs{})
		want := MergedConfig{
			ExtraMounts:    []string{"/mnt/data"},
			EnvPassthrough: []string{"MY_VAR"},
			SensitiveDirs:  []string{"/secret"},
			Image:          "custom:latest",
		}
		assertMergedConfigEqual(t, result, want)
	})

	t.Run("cli mounts extend", func(t *testing.T) {
		fileConfig := Config{Defaults: DefaultsConfig{ExtraMounts: []string{"/mnt/data"}}}
		result := MergeConfig(fileConfig, CLIArgs{Mount: []string{"/mnt/other"}})
		want := MergedConfig{
			ExtraMounts:    []string{"/mnt/data", "/mnt/other"},
			EnvPassthrough: []string{},
			SensitiveDirs:  []string{},
		}
		assertMergedConfigEqual(t, result, want)
	})

	t.Run("cli env extends", func(t *testing.T) {
		fileConfig := Config{Defaults: DefaultsConfig{EnvPassthrough: []string{"VAR1"}}}
		result := MergeConfig(fileConfig, CLIArgs{Env: []string{"VAR2"}})
		want := MergedConfig{
			ExtraMounts:    []string{},
			EnvPassthrough: []string{"VAR1", "VAR2"},
			SensitiveDirs:  []string{},
		}
		assertMergedConfigEqual(t, result, want)
	})

	t.Run("cli image overrides", func(t *testing.T) {
		fileConfig := Config{Docker: DockerConfig{Image: "file-image:latest"}}
		result := MergeConfig(fileConfig, CLIArgs{Image: "cli-image:v2"})
		want := MergedConfig{
			ExtraMounts:    []string{},
			EnvPassthrough: []string{},
			SensitiveDirs:  []string{},
			Image:          "cli-image:v2",
		}
		assertMergedConfigEqual(t, result, want)
	})

	t.Run("empty config and args", func(t *testing.T) {
		result := MergeConfig(Config{}, CLIArgs{})
		want := MergedConfig{
			ExtraMounts:    []string{},
			EnvPassthrough: []string{},
			SensitiveDirs:  []string{},
		}
		assertMergedConfigEqual(t, result, want)
	})

	t.Run("missing sections handled", func(t *testing.T) {
		result := MergeConfig(Config{}, CLIArgs{})
		want := MergedConfig{
			ExtraMounts:    []string{},
			EnvPassthrough: []string{},
			SensitiveDirs:  []string{},
		}
		assertMergedConfigEqual(t, result, want)
	})
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) returned error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) returned error: %v", path, err)
	}
}

func assertConfigEqual(t *testing.T, got Config, want Config) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func assertMergedConfigEqual(t *testing.T, got MergedConfig, want MergedConfig) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged config = %#v, want %#v", got, want)
	}
}
