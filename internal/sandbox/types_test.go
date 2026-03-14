package sandbox

import (
	"os"
	"slices"
	"testing"
)

func TestDefaultImageName(t *testing.T) {
	t.Parallel()

	if DefaultImageName != "sb-sandbox:latest" {
		t.Fatalf("DefaultImageName = %q, want %q", DefaultImageName, "sb-sandbox:latest")
	}
}

func TestSandboxInfoGetName(t *testing.T) {
	t.Parallel()

	sandbox := SandboxInfo{Name: "sb-my-app-a1b2c3d4"}
	if got := sandbox.GetName(); got != sandbox.Name {
		t.Fatalf("SandboxInfo.GetName() = %q, want %q", got, sandbox.Name)
	}
}

func TestSandboxInfoHasContainerID(t *testing.T) {
	t.Parallel()

	empty := ""
	valid := "abc123"

	tests := []struct {
		name string
		info SandboxInfo
		want bool
	}{
		{"nil pointer", SandboxInfo{}, false},
		{"empty string", SandboxInfo{ContainerID: &empty}, false},
		{"non-empty string", SandboxInfo{ContainerID: &valid}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.info.hasContainerID(); got != tt.want {
				t.Fatalf("hasContainerID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSensitiveDirs(t *testing.T) {
	t.Parallel()

	if !slices.Contains(SensitiveDirs, "/") {
		t.Fatalf("SensitiveDirs = %v, want to include /", SensitiveDirs)
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" && !slices.Contains(SensitiveDirs, home) {
		t.Fatalf("SensitiveDirs = %v, want to include home directory %q", SensitiveDirs, home)
	}

	for _, expected := range []string{"/etc", "/var", "/usr", "/bin", "/sbin"} {
		if !slices.Contains(SensitiveDirs, expected) {
			t.Fatalf("SensitiveDirs = %v, want to include %q", SensitiveDirs, expected)
		}
	}
}

func TestDefaultMounts(t *testing.T) {
	t.Parallel()

	expected := []MountSpec{
		{Host: "~/.claude/", Container: "/home/sandbox/.claude/", Mode: MountModeReadWrite},
		{Host: "~/.claude.json", Container: "/home/sandbox/.claude.json", Mode: MountModeReadWrite},
		{Host: "~/.config/claude-code/", Container: "/home/sandbox/.config/claude-code/", Mode: MountModeReadWrite},
		{Host: "~/.codex/", Container: "/home/sandbox/.codex/", Mode: MountModeReadWrite},
		{Host: "~/.pi/", Container: "/home/sandbox/.pi/", Mode: MountModeReadWrite},
		{Host: "~/.gitconfig", Container: "/home/sandbox/.gitconfig", Mode: MountModeReadOnly},
		{Host: "~/.config/sb/zshrc", Container: "/home/sandbox/.zshrc", Mode: MountModeReadOnly},
		{Host: "~/.config/sb/starship.toml", Container: "/home/sandbox/.config/starship.toml", Mode: MountModeReadOnly},
		{Host: "~/.config/sb/nvim/", Container: "/home/sandbox/.config/nvim/", Mode: MountModeReadWrite},
	}

	if !slices.Equal(DefaultMounts, expected) {
		t.Fatalf("DefaultMounts = %#v, want %#v", DefaultMounts, expected)
	}
}
