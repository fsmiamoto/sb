package pathutil

import (
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		path string
		want string
	}{
		{"empty", "", ""},
		{"bare tilde", "~", home},
		{"tilde slash", "~/foo", filepath.Join(home, "foo")},
		{"tilde backslash", "~\\bar", filepath.Join(home, "bar")},
		{"absolute", "/usr/bin", "/usr/bin"},
		{"relative", "some/path", "some/path"},
		{"tilde in middle", "/a/~/b", "/a/~/b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandHome(tc.path); got != tc.want {
				t.Fatalf("ExpandHome(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
