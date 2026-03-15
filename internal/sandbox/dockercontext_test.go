package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// setupDockerConfig writes a minimal ~/.docker/config.json under the given home dir.
func setupDockerConfig(t *testing.T, home, currentContext string) {
	t.Helper()
	configDir := filepath.Join(home, ".docker")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(dockerConfig{CurrentContext: currentContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// setupContextMeta writes a context meta.json with the given docker endpoint.
func setupContextMeta(t *testing.T, home, contextName, host string) {
	t.Helper()
	hash := sha256.Sum256([]byte(contextName))
	hexHash := hex.EncodeToString(hash[:])
	metaDir := filepath.Join(home, ".docker", "contexts", "meta", hexHash)
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := contextMeta{
		Endpoints: map[string]contextEndpoint{
			"docker": {Host: host},
		},
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// setupContextMetaNoDocker writes a context meta.json without a "docker" endpoint key.
func setupContextMetaNoDocker(t *testing.T, home, contextName string) {
	t.Helper()
	hash := sha256.Sum256([]byte(contextName))
	hexHash := hex.EncodeToString(hash[:])
	metaDir := filepath.Join(home, ".docker", "contexts", "meta", hexHash)
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := contextMeta{
		Endpoints: map[string]contextEndpoint{
			"kubernetes": {Host: "https://k8s.example.com"},
		},
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// setupMalformedMeta writes invalid JSON as a context's meta.json.
func setupMalformedMeta(t *testing.T, home, contextName string) {
	t.Helper()
	hash := sha256.Sum256([]byte(contextName))
	hexHash := hex.EncodeToString(hash[:])
	metaDir := filepath.Join(home, ".docker", "contexts", "meta", hexHash)
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte("{not valid json"), 0644); err != nil {
		t.Fatal(err)
	}
}

// makeGetenv returns a getenv function backed by the given key-value pairs.
func makeGetenv(vars map[string]string) func(string) string {
	return func(key string) string {
		return vars[key]
	}
}

func TestReadCurrentContext(t *testing.T) {
	t.Parallel()

	t.Run("malformed JSON returns empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		if err := os.WriteFile(configPath, []byte("{not valid json"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := readCurrentContext(configPath); got != "" {
			t.Fatalf("readCurrentContext(malformed) = %q, want empty", got)
		}
	})

	t.Run("valid JSON returns currentContext", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		if err := os.WriteFile(configPath, []byte(`{"currentContext":"colima"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if got := readCurrentContext(configPath); got != "colima" {
			t.Fatalf("readCurrentContext(valid) = %q, want %q", got, "colima")
		}
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		t.Parallel()
		if got := readCurrentContext("/nonexistent/config.json"); got != "" {
			t.Fatalf("readCurrentContext(missing) = %q, want empty", got)
		}
	})
}

func TestResolveDockerHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T) (getenv func(string) string, homeDir func() (string, error))
		expected string
	}{
		{
			name: "DOCKER_HOST set skips context resolution",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				return makeGetenv(map[string]string{
						"DOCKER_HOST": "unix:///custom/docker.sock",
					}), func() (string, error) {
						return "/unused", nil
					}
			},
			expected: "",
		},
		{
			name: "DOCKER_CONTEXT env var resolves to endpoint",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupContextMeta(t, home, "colima", "unix:///colima/docker.sock")
				return makeGetenv(map[string]string{
						"DOCKER_CONTEXT": "colima",
					}), func() (string, error) {
						return home, nil
					}
			},
			expected: "unix:///colima/docker.sock",
		},
		{
			name: "currentContext from config.json resolves to endpoint",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "orbstack")
				setupContextMeta(t, home, "orbstack", "unix:///orbstack/docker.sock")
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "unix:///orbstack/docker.sock",
		},
		{
			name: "currentContext is default returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "default")
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "missing config.json returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "empty currentContext returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "")
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "context with no matching meta.json returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "nonexistent")
				// No meta.json created for "nonexistent"
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "meta.json without docker endpoint returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "k8s-only")
				setupContextMetaNoDocker(t, home, "k8s-only")
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "malformed meta.json returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "broken")
				setupMalformedMeta(t, home, "broken")
				return makeGetenv(nil), func() (string, error) {
					return home, nil
				}
			},
			expected: "",
		},
		{
			name: "DOCKER_CONTEXT overrides currentContext from config.json",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupDockerConfig(t, home, "from-config")
				setupContextMeta(t, home, "from-config", "unix:///config/docker.sock")
				setupContextMeta(t, home, "from-env", "unix:///env/docker.sock")
				return makeGetenv(map[string]string{
						"DOCKER_CONTEXT": "from-env",
					}), func() (string, error) {
						return home, nil
					}
			},
			expected: "unix:///env/docker.sock",
		},
		{
			name: "userHomeDir failure returns empty",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				return makeGetenv(map[string]string{
						"DOCKER_CONTEXT": "colima",
					}), func() (string, error) {
						return "", errors.New("no home directory")
					}
			},
			expected: "",
		},
		{
			name: "DOCKER_HOST takes precedence over DOCKER_CONTEXT",
			setup: func(t *testing.T) (func(string) string, func() (string, error)) {
				home := t.TempDir()
				setupContextMeta(t, home, "colima", "unix:///colima/docker.sock")
				return makeGetenv(map[string]string{
						"DOCKER_HOST":    "tcp://remote:2375",
						"DOCKER_CONTEXT": "colima",
					}), func() (string, error) {
						return home, nil
					}
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv, homeDir := tt.setup(t)
			got := resolveDockerHost(getenv, homeDir)
			if got != tt.expected {
				t.Errorf("resolveDockerHost() = %q, want %q", got, tt.expected)
			}
		})
	}
}
