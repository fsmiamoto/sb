package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// resolveDockerHost replicates the Docker CLI's context resolution to find the
// daemon endpoint when DOCKER_HOST is not explicitly set.
//
// Resolution order:
//  1. If DOCKER_HOST is set, return "" (let FromEnv handle it)
//  2. If DOCKER_CONTEXT is set, use that context name
//  3. Read currentContext from ~/.docker/config.json
//  4. If context is "default" or empty, return "" (use default socket)
//  5. Hash the context name with SHA-256, read the corresponding meta.json
//  6. Extract and return Endpoints.docker.Host
//
// Returns "" on any failure — the caller falls through to the SDK default.
func resolveDockerHost(getenv func(string) string, userHomeDir func() (string, error)) string {
	if getenv("DOCKER_HOST") != "" {
		return ""
	}

	contextName := getenv("DOCKER_CONTEXT")

	home, err := userHomeDir()
	if err != nil {
		return ""
	}

	if contextName == "" {
		contextName = readCurrentContext(filepath.Join(home, ".docker", "config.json"))
	}

	if contextName == "" || contextName == "default" {
		return ""
	}

	return readContextEndpoint(home, contextName)
}

// dockerConfig is a minimal representation of ~/.docker/config.json.
type dockerConfig struct {
	CurrentContext string `json:"currentContext"`
}

// readCurrentContext reads the currentContext field from a Docker config.json file.
func readCurrentContext(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// contextMeta matches the structure of a Docker context meta.json file.
type contextMeta struct {
	Endpoints map[string]contextEndpoint `json:"Endpoints"`
}

// contextEndpoint represents a single endpoint within a Docker context.
type contextEndpoint struct {
	Host string `json:"Host"`
}

// readContextEndpoint reads the Docker endpoint host from a context's meta.json.
//
// The meta.json path is derived by hashing the context name with SHA-256:
// ~/.docker/contexts/meta/<hex(sha256(name))>/meta.json.
func readContextEndpoint(home, contextName string) string {
	hash := sha256.Sum256([]byte(contextName))
	hexHash := hex.EncodeToString(hash[:])

	metaPath := filepath.Join(home, ".docker", "contexts", "meta", hexHash, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}

	var meta contextMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}

	ep, ok := meta.Endpoints["docker"]
	if !ok {
		return ""
	}
	return ep.Host
}
