package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v2"

	"github.com/fsmiamoto/sb/internal/config"
	"github.com/fsmiamoto/sb/internal/sandbox"
)

func TestFormatCreatedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		createdAt string
		want      string
	}{
		{
			name:      "empty string",
			createdAt: "",
			want:      "",
		},
		{
			name:      "RFC3339 timestamp",
			createdAt: "2026-03-08T10:30:00Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "RFC3339 with timezone offset",
			createdAt: "2026-03-08T10:30:00+03:00",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "RFC3339Nano timestamp",
			createdAt: "2026-03-08T10:30:00.123456789Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "Docker high-precision nanoseconds",
			createdAt: "2026-03-08T10:30:00.1234567890Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "bare ISO timestamp without timezone",
			createdAt: "2026-03-08T10:30:00",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "short fallback returns first 16 chars",
			createdAt: "2026-03-08 10:30:00 garbage",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "very short unparseable returns as-is",
			createdAt: "unknown",
			want:      "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatCreatedAt(tc.createdAt); got != tc.want {
				t.Fatalf("formatCreatedAt(%q) = %q, want %q", tc.createdAt, got, tc.want)
			}
		})
	}
}

func TestExitError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  string
		args    []any
		wantMsg string
	}{
		{
			name:    "simple message",
			format:  "something went wrong",
			args:    nil,
			wantMsg: "something went wrong",
		},
		{
			name:    "formatted message",
			format:  "sandbox %q not found",
			args:    []any{"my-sandbox"},
			wantMsg: `sandbox "my-sandbox" not found`,
		},
		{
			name:    "empty message",
			format:  "",
			args:    nil,
			wantMsg: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := exitError(tc.format, tc.args...)
			if err == nil {
				t.Fatal("exitError() returned nil, want non-nil error")
			}

			if got := err.Error(); got != tc.wantMsg {
				t.Fatalf("exitError().Error() = %q, want %q", got, tc.wantMsg)
			}

			var exitErr cli.ExitCoder
			if !errors.As(err, &exitErr) {
				t.Fatal("exitError() did not return a cli.ExitCoder")
			}
			if code := exitErr.ExitCode(); code != 1 {
				t.Fatalf("exitError().ExitCode() = %d, want 1", code)
			}
		})
	}
}

func TestWarn(t *testing.T) {
	// Capture stderr by redirecting os.Stderr to a pipe.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = w

	warn("disk is almost full")

	_ = w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom pipe error = %v", err)
	}

	got := buf.String()
	want := "Warning: disk is almost full\n"
	if got != want {
		t.Fatalf("warn() wrote %q to stderr, want %q", got, want)
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "y accepts", input: "y\n", want: true},
		{name: "yes accepts", input: "yes\n", want: true},
		{name: "Y accepts (case insensitive)", input: "Y\n", want: true},
		{name: "YES accepts (case insensitive)", input: "YES\n", want: true},
		{name: "n rejects", input: "n\n", want: false},
		{name: "empty rejects", input: "\n", want: false},
		{name: "arbitrary text rejects", input: "maybe\n", want: false},
		{name: "y with whitespace", input: "  y  \n", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Redirect stdin and stderr for the confirm function.
			origStdin := os.Stdin
			origStderr := os.Stderr

			stdinR, stdinW, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe() error = %v", err)
			}
			// Discard stderr output from the prompt.
			stderrR, stderrW, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe() error = %v", err)
			}

			os.Stdin = stdinR
			os.Stderr = stderrW
			defer func() {
				os.Stdin = origStdin
				os.Stderr = origStderr
				_ = stdinR.Close()
				_ = stderrR.Close()
				_ = stderrW.Close()
			}()

			_, _ = fmt.Fprint(stdinW, tc.input)
			_ = stdinW.Close()

			got := confirm("Continue?")
			if got != tc.want {
				t.Fatalf("confirm() with input %q = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestLoadMergedConfig(t *testing.T) {
	// loadMergedConfig expects a *cli.Context. Create one with a nonexistent config
	// path to verify it returns defaults (warning printed to stderr is ignored).
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config"},
		},
		Action: func(cCtx *cli.Context) error {
			merged := loadMergedConfig(cCtx)
			if merged.Image != "" {
				t.Fatalf("loadMergedConfig() Image = %q, want empty", merged.Image)
			}
			return nil
		},
	}

	// Suppress stderr warnings from loadMergedConfig.
	origStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		_ = w.Close()
		os.Stderr = origStderr
	}()

	if err := app.Run([]string{"sb", "--config", "/nonexistent/path/config.toml"}); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	// newManager should return a non-nil manager from any MergedConfig.
	merged := config.MergedConfig{
		Image:          "custom:latest",
		ExtraMounts:    []string{"~/extra"},
		EnvPassthrough: []string{"FOO"},
	}
	mgr := newManager(merged)
	if mgr == nil {
		t.Fatal("newManager() returned nil")
	}
}

func TestExitErrorFormatsWithPercent(t *testing.T) {
	t.Parallel()

	// Verify that %v formatting works correctly.
	inner := errors.New("inner failure")
	err := exitError("%v", inner)
	if !strings.Contains(err.Error(), "inner failure") {
		t.Fatalf("exitError(%%v, err) = %q, want to contain 'inner failure'", err.Error())
	}
}

func TestStatusText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantDisplay string
	}{
		{name: "running", raw: "running", wantDisplay: "running"},
		{name: "exited maps to stopped", raw: "exited", wantDisplay: "stopped"},
		{name: "stopped maps to stopped", raw: "stopped", wantDisplay: "stopped"},
		{name: "created maps to stopped", raw: "created", wantDisplay: "stopped"},
		{name: "empty maps to unknown", raw: "", wantDisplay: "unknown"},
		{name: "unexpected status maps to unknown", raw: "paused", wantDisplay: "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDisplay, _ := statusText(tc.raw)
			if gotDisplay != tc.wantDisplay {
				t.Fatalf("statusText(%q) display = %q, want %q", tc.raw, gotDisplay, tc.wantDisplay)
			}
		})
	}
}

func TestPrintSandboxJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sandboxes []sandbox.SandboxInfo
		want      []sandboxJSON
	}{
		{
			name:      "empty list outputs empty array",
			sandboxes: nil,
			want:      []sandboxJSON{},
		},
		{
			name: "single running sandbox",
			sandboxes: []sandbox.SandboxInfo{
				{
					Name:      "sb-myapp-abc12345",
					Workspace: "/home/user/myapp",
					Status:    "running",
					CreatedAt: "2026-03-08T10:00:00Z",
				},
			},
			want: []sandboxJSON{
				{
					Name:      "sb-myapp-abc12345",
					Workspace: "/home/user/myapp",
					Status:    "running",
					CreatedAt: "2026-03-08T10:00:00Z",
				},
			},
		},
		{
			name: "maps exited to stopped",
			sandboxes: []sandbox.SandboxInfo{
				{
					Name:      "sb-proj-def67890",
					Workspace: "/tmp/proj",
					Status:    "exited",
				},
			},
			want: []sandboxJSON{
				{
					Name:      "sb-proj-def67890",
					Workspace: "/tmp/proj",
					Status:    "stopped",
				},
			},
		},
		{
			name: "empty status maps to unknown",
			sandboxes: []sandbox.SandboxInfo{
				{
					Name:      "sb-test-11111111",
					Workspace: "/tmp/test",
					Status:    "",
				},
			},
			want: []sandboxJSON{
				{
					Name:      "sb-test-11111111",
					Workspace: "/tmp/test",
					Status:    "unknown",
				},
			},
		},
		{
			name: "multiple sandboxes",
			sandboxes: []sandbox.SandboxInfo{
				{Name: "sb-a-11111111", Workspace: "/a", Status: "running", CreatedAt: "2026-01-01T00:00:00Z"},
				{Name: "sb-b-22222222", Workspace: "/b", Status: "exited"},
			},
			want: []sandboxJSON{
				{Name: "sb-a-11111111", Workspace: "/a", Status: "running", CreatedAt: "2026-01-01T00:00:00Z"},
				{Name: "sb-b-22222222", Workspace: "/b", Status: "stopped"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := printSandboxJSON(&buf, tc.sandboxes); err != nil {
				t.Fatalf("printSandboxJSON() error = %v", err)
			}

			var got []sandboxJSON
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v\nraw output: %s", err, buf.String())
			}

			if len(got) != len(tc.want) {
				t.Fatalf("got %d items, want %d\nraw output: %s", len(got), len(tc.want), buf.String())
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("item[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPrintSandboxTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sandboxes  []sandbox.SandboxInfo
		wantParts  []string
		wantAbsent []string
	}{
		{
			name: "single running sandbox",
			sandboxes: []sandbox.SandboxInfo{
				{
					Name:      "sb-myapp-abc12345",
					Workspace: "/home/user/myapp",
					Status:    "running",
					CreatedAt: "2026-03-08T10:30:00Z",
				},
			},
			wantParts: []string{
				"sb-myapp-abc12345",
				"/home/user/myapp",
				"running",
				"2026-03-08 10:30",
				"NAME",
				"WORKSPACE",
				"STATUS",
				"CREATED",
			},
		},
		{
			name: "exited maps to stopped",
			sandboxes: []sandbox.SandboxInfo{
				{
					Name:      "sb-proj-def67890",
					Workspace: "/tmp/proj",
					Status:    "exited",
				},
			},
			wantParts:  []string{"sb-proj-def67890", "stopped"},
			wantAbsent: []string{"exited"},
		},
		{
			name: "multiple sandboxes",
			sandboxes: []sandbox.SandboxInfo{
				{Name: "sb-a-11111111", Workspace: "/a", Status: "running"},
				{Name: "sb-b-22222222", Workspace: "/b", Status: "exited"},
			},
			wantParts: []string{
				"sb-a-11111111", "/a", "running",
				"sb-b-22222222", "/b", "stopped",
			},
		},
		{
			name: "unknown status for empty",
			sandboxes: []sandbox.SandboxInfo{
				{Name: "sb-x-aaaaaaaa", Workspace: "/x", Status: ""},
			},
			wantParts: []string{"sb-x-aaaaaaaa", "unknown"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			printSandboxTable(&buf, tc.sandboxes)
			got := buf.String()

			if got == "" {
				t.Fatal("printSandboxTable() produced no output")
			}

			for _, part := range tc.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("output missing %q\ngot: %s", part, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("output should not contain %q\ngot: %s", absent, got)
				}
			}
		})
	}
}
