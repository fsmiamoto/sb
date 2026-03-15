package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome replaces a leading ~ in path with the user's home directory.
// Returns path unchanged if it does not start with ~ or if the home directory
// cannot be determined.
func ExpandHome(path string) string {
	if path == "" {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}

	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\"):
		return filepath.Join(home, path[2:])
	default:
		return path
	}
}
