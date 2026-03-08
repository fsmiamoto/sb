package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultDirname = "sandbox"

var (
	invalidDirnameChars = regexp.MustCompile(`[^a-z0-9\-_]`)
	multipleHyphens     = regexp.MustCompile(`-+`)
)

// SanitizeDirname converts a directory name into a sandbox-safe slug.
func SanitizeDirname(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = invalidDirnameChars.ReplaceAllString(name, "")
	name = multipleHyphens.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")

	if name == "" {
		return defaultDirname
	}

	return name
}

// GenerateName creates a deterministic sandbox name from a filesystem path.
func GenerateName(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	dirname := filepath.Base(absPath)
	sanitized := SanitizeDirname(dirname)
	sum := sha256.Sum256([]byte(absPath))
	pathHash := hex.EncodeToString(sum[:])[:8]

	return "sb-" + sanitized + "-" + pathHash, nil
}
