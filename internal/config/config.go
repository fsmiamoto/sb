package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/fsmiamoto/sb/internal/pathutil"
)

// DefaultsConfig contains the values loaded from the [defaults] TOML section.
type DefaultsConfig struct {
	ExtraMounts    []string
	EnvPassthrough []string
	SensitiveDirs  []string
}

// DockerConfig contains the values loaded from the [docker] TOML section.
type DockerConfig struct {
	Image string
}

// Config is the structured representation of sb's config.toml file.
type Config struct {
	Defaults DefaultsConfig
	Docker   DockerConfig
}

// MergedConfig is the flattened configuration used by later CLI and sandbox code.
type MergedConfig struct {
	ExtraMounts    []string
	EnvPassthrough []string
	SensitiveDirs  []string
	Image          string
}

type rawConfig struct {
	Defaults map[string]any `toml:"defaults"`
	Docker   map[string]any `toml:"docker"`
}

// DefaultConfig returns a fresh copy of sb's default configuration.
func DefaultConfig() Config {
	return Config{
		Defaults: DefaultsConfig{
			ExtraMounts:    make([]string, 0),
			EnvPassthrough: make([]string, 0),
			SensitiveDirs:  make([]string, 0),
		},
		Docker: DockerConfig{},
	}
}

// GetDefaultConfigPath returns ~/.config/sb/config.toml.
func GetDefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config", "sb", "config.toml")
	}

	return filepath.Join(home, ".config", "sb", "config.toml")
}

// LoadConfig loads sb configuration from TOML, returning defaults when the file
// does not exist. Parse and filesystem errors return defaults plus an error so
// callers can surface a warning while continuing.
func LoadConfig(path string) (Config, error) {
	config := DefaultConfig()

	if path == "" {
		path = GetDefaultConfigPath()
	} else {
		path = pathutil.ExpandHome(path)
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}

		return config, fmt.Errorf("failed to load config from %s: %w", path, err)
	}

	if info.IsDir() {
		return config, fmt.Errorf("failed to load config from %s: path is a directory", path)
	}

	var raw rawConfig
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return config, fmt.Errorf("failed to load config from %s: %w", path, err)
	}

	if mounts, ok := stringSlice(raw.Defaults["extra_mounts"]); ok {
		config.Defaults.ExtraMounts = expandPaths(mounts)
	}
	if envPassthrough, ok := stringSlice(raw.Defaults["env_passthrough"]); ok {
		config.Defaults.EnvPassthrough = slices.Clone(envPassthrough)
	}
	if sensitiveDirs, ok := stringSlice(raw.Defaults["sensitive_dirs"]); ok {
		config.Defaults.SensitiveDirs = expandPaths(sensitiveDirs)
	}
	if image, ok := raw.Docker["image"].(string); ok {
		config.Docker.Image = image
	}

	return config, nil
}

// MergeConfig flattens a file-loaded Config into the MergedConfig structure
// consumed by downstream CLI and sandbox code.
func MergeConfig(fileConfig Config) MergedConfig {
	return MergedConfig{
		ExtraMounts:    slices.Clone(fileConfig.Defaults.ExtraMounts),
		EnvPassthrough: slices.Clone(fileConfig.Defaults.EnvPassthrough),
		SensitiveDirs:  slices.Clone(fileConfig.Defaults.SensitiveDirs),
		Image:          fileConfig.Docker.Image,
	}
}

func expandPaths(paths []string) []string {
	expanded := make([]string, 0, len(paths))
	for _, path := range paths {
		expanded = append(expanded, pathutil.ExpandHome(path))
	}

	return expanded
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return slices.Clone(typed), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, value)
		}
		return result, true
	default:
		return nil, false
	}
}
