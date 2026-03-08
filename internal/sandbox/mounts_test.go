package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dockermount "github.com/docker/docker/api/types/mount"
)

func TestMapContainerPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name     string
		hostPath string
		want     string
	}{
		{
			name:     "home relative directory",
			hostPath: "~/configs/nvim",
			want:     "/home/sandbox/configs/nvim",
		},
		{
			name:     "home relative dotfile",
			hostPath: "~/.gitconfig",
			want:     "/home/sandbox/.gitconfig",
		},
		{
			name:     "home root",
			hostPath: "~",
			want:     "/home/sandbox/.",
		},
		{
			name:     "absolute path unchanged",
			hostPath: "/etc/hosts",
			want:     "/etc/hosts",
		},
		{
			name:     "relative path unchanged",
			hostPath: "configs/local",
			want:     "configs/local",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapContainerPath(tc.hostPath); got != tc.want {
				t.Fatalf("mapContainerPath(%q) = %q, want %q", tc.hostPath, got, tc.want)
			}
		})
	}
}

func TestMountBuilderBuildIncludesWorkspaceDefaultsAndExtraMounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace", "project")
	mustMkdirAll(t, workspace)

	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustWriteFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = sb\n")
	mustWriteFile(t, filepath.Join(home, ".config", "sb", "zshrc"), "export TEST=1\n")
	mustMkdirAll(t, filepath.Join(home, ".config", "sb", "nvim"))

	configExtraHome := filepath.Join(home, "shared")
	absoluteExtra := filepath.Join(t.TempDir(), "abs-extra")
	cliExtra := filepath.Join(home, "cli-extra.env")
	mustMkdirAll(t, configExtraHome)
	mustMkdirAll(t, absoluteExtra)
	mustWriteFile(t, cliExtra, "TOKEN=abc\n")

	builder := NewMountBuilder([]string{"~/shared", absoluteExtra, "~/missing-config"})
	gotMounts, missing, err := builder.Build("~/workspace/project", []string{"~/cli-extra.env", "~/missing-cli"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	wantMounts := []dockermount.Mount{
		{Type: dockermount.TypeBind, Source: workspace, Target: workspaceMountTarget, ReadOnly: false},
		{Type: dockermount.TypeBind, Source: filepath.Join(home, ".claude"), Target: "/home/sandbox/.claude/", ReadOnly: false},
		{Type: dockermount.TypeBind, Source: filepath.Join(home, ".gitconfig"), Target: "/home/sandbox/.gitconfig", ReadOnly: true},
		{Type: dockermount.TypeBind, Source: filepath.Join(home, ".config", "sb", "zshrc"), Target: "/home/sandbox/.zshrc", ReadOnly: true},
		{Type: dockermount.TypeBind, Source: filepath.Join(home, ".config", "sb", "nvim"), Target: "/home/sandbox/.config/nvim/", ReadOnly: false},
		{Type: dockermount.TypeBind, Source: configExtraHome, Target: "/home/sandbox/shared", ReadOnly: true},
		{Type: dockermount.TypeBind, Source: absoluteExtra, Target: absoluteExtra, ReadOnly: true},
		{Type: dockermount.TypeBind, Source: cliExtra, Target: "/home/sandbox/cli-extra.env", ReadOnly: true},
	}

	if !reflect.DeepEqual(gotMounts, wantMounts) {
		t.Fatalf("Build() mounts = %#v, want %#v", gotMounts, wantMounts)
	}

	wantMissing := []string{"~/missing-config", "~/missing-cli"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Fatalf("Build() missing = %#v, want %#v", missing, wantMissing)
	}
}

func TestMountBuilderBuildKeepsExpandedExtraMountTargetAsAbsolutePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	mustMkdirAll(t, workspace)

	expandedExtra := filepath.Join(home, "already-expanded")
	mustMkdirAll(t, expandedExtra)

	gotMounts, missing, err := NewMountBuilder([]string{expandedExtra}).Build(workspace, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("Build() missing = %#v, want no missing mounts", missing)
	}
	if len(gotMounts) != 2 {
		t.Fatalf("Build() produced %d mounts, want 2 (workspace + expanded extra)", len(gotMounts))
	}

	extra := gotMounts[1]
	want := dockermount.Mount{
		Type:     dockermount.TypeBind,
		Source:   expandedExtra,
		Target:   expandedExtra,
		ReadOnly: true,
	}
	if !reflect.DeepEqual(extra, want) {
		t.Fatalf("expanded extra mount = %#v, want %#v", extra, want)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
