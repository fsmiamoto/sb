package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	if !slices.Equal(missing, wantMissing) {
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

func TestDeduplicateMounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mounts []dockermount.Mount
		want   []dockermount.Mount
	}{
		{
			name:   "no duplicates",
			mounts: []dockermount.Mount{{Target: "/a"}, {Target: "/b"}},
			want:   []dockermount.Mount{{Target: "/a"}, {Target: "/b"}},
		},
		{
			name:   "duplicate keeps last",
			mounts: []dockermount.Mount{{Target: "/a", ReadOnly: true}, {Target: "/b"}, {Target: "/a", ReadOnly: false}},
			want:   []dockermount.Mount{{Target: "/b"}, {Target: "/a", ReadOnly: false}},
		},
		{
			name:   "all same target",
			mounts: []dockermount.Mount{{Target: "/a", Source: "1"}, {Target: "/a", Source: "2"}, {Target: "/a", Source: "3"}},
			want:   []dockermount.Mount{{Target: "/a", Source: "3"}},
		},
		{
			name:   "empty",
			mounts: []dockermount.Mount{},
			want:   []dockermount.Mount{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := deduplicateMounts(tc.mounts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("deduplicateMounts() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBuildDeduplicatesCLIOverridingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	mustMkdirAll(t, workspace)

	sharedDir := filepath.Join(home, "shared")
	mustMkdirAll(t, sharedDir)

	// Config and CLI both specify the same mount path.
	builder := NewMountBuilder([]string{"~/shared"})
	mounts, _, err := builder.Build(workspace, []string{"~/shared"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// Count how many mounts target /home/sandbox/shared.
	target := "/home/sandbox/shared"
	count := 0
	for _, m := range mounts {
		if m.Target == target {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 mount targeting %q, got %d", target, count)
	}
}

func TestBuildDeduplicatesCLIOverridingDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	mustMkdirAll(t, workspace)

	// Create .gitconfig so the default mount is included.
	mustWriteFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = test\n")

	// CLI specifies the same path as a default mount.
	builder := NewMountBuilder(nil)
	mounts, _, err := builder.Build(workspace, []string{"~/.gitconfig"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	target := "/home/sandbox/.gitconfig"
	count := 0
	for _, m := range mounts {
		if m.Target == target {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 mount targeting %q, got %d", target, count)
	}
}

func TestExpandAndAbsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		path    string
		wantAbs bool
	}{
		{
			name:    "tilde path becomes absolute",
			path:    "~/projects",
			wantAbs: true,
		},
		{
			name:    "absolute path stays absolute",
			path:    "/tmp/workspace",
			wantAbs: true,
		},
		{
			name:    "relative path becomes absolute",
			path:    "some/relative",
			wantAbs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandAndAbsPath(tc.path)
			if err != nil {
				t.Fatalf("expandAndAbsPath(%q) error = %v", tc.path, err)
			}
			if tc.wantAbs && !filepath.IsAbs(got) {
				t.Fatalf("expandAndAbsPath(%q) = %q, want absolute path", tc.path, got)
			}
		})
	}

	// Tilde is expanded before Abs
	got, err := expandAndAbsPath("~/mydir")
	if err != nil {
		t.Fatalf("expandAndAbsPath(~/mydir) error = %v", err)
	}
	want := filepath.Join(home, "mydir")
	if got != want {
		t.Fatalf("expandAndAbsPath(~/mydir) = %q, want %q", got, want)
	}
}

func TestBuildBindMount(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		target   string
		readOnly bool
	}{
		{
			name:     "read-write mount",
			source:   "/host/workspace",
			target:   "/container/workspace",
			readOnly: false,
		},
		{
			name:     "read-only mount",
			source:   "/host/.gitconfig",
			target:   "/home/sandbox/.gitconfig",
			readOnly: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildBindMount(tc.source, tc.target, tc.readOnly)
			if got.Type != dockermount.TypeBind {
				t.Fatalf("Type = %v, want TypeBind", got.Type)
			}
			if got.Source != tc.source {
				t.Fatalf("Source = %q, want %q", got.Source, tc.source)
			}
			if got.Target != tc.target {
				t.Fatalf("Target = %q, want %q", got.Target, tc.target)
			}
			if got.ReadOnly != tc.readOnly {
				t.Fatalf("ReadOnly = %v, want %v", got.ReadOnly, tc.readOnly)
			}
		})
	}
}

func TestPathExists(t *testing.T) {
	dir := t.TempDir()
	existingFile := filepath.Join(dir, "exists.txt")
	mustWriteFile(t, existingFile, "hello")

	if !pathExists(existingFile) {
		t.Fatalf("pathExists(%q) = false, want true", existingFile)
	}
	if !pathExists(dir) {
		t.Fatalf("pathExists(%q) = false, want true for directory", dir)
	}
	if pathExists(filepath.Join(dir, "nope.txt")) {
		t.Fatal("pathExists(nonexistent) = true, want false")
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
