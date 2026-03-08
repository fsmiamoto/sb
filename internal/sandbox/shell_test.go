package sandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestShellConfigManagerEnsureConfigsCopiesMissingDefaultsWithoutOverwritingExisting(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), ".config", "sb")
	mustWriteFile(t, filepath.Join(configDir, "starship.toml"), "custom = true\n")

	manager := NewShellConfigManager(configDir)
	manager.defaultsFS = fstest.MapFS{
		"zshrc":                      {Data: []byte("export TEST=1\n"), Mode: 0o644},
		"starship.toml":              {Data: []byte("format = '$all'\n"), Mode: 0o644},
		"nvim/init.lua":              {Data: []byte("print('init')\n"), Mode: 0o644},
		"nvim/lua/plugins/init.lua":  {Data: []byte("return {}\n"), Mode: 0o644},
	}

	if err := manager.EnsureConfigs(); err != nil {
		t.Fatalf("EnsureConfigs() error = %v", err)
	}

	assertFileContent(t, filepath.Join(configDir, "zshrc"), "export TEST=1\n")
	assertFileContent(t, filepath.Join(configDir, "starship.toml"), "custom = true\n")
	assertFileContent(t, filepath.Join(configDir, "nvim", "init.lua"), "print('init')\n")
	assertFileContent(t, filepath.Join(configDir, "nvim", "lua", "plugins", "init.lua"), "return {}\n")

	info, err := os.Stat(filepath.Join(configDir, "zshrc"))
	if err != nil {
		t.Fatalf("Stat(zshrc) error = %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o644); got != want {
		t.Fatalf("zshrc mode = %v, want %v", got, want)
	}
}

func TestShellConfigManagerEnsureConfigsUsesDefaultHomeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	manager := NewShellConfigManager("")
	manager.defaultsFS = fstest.MapFS{
		"zshrc":             {Data: []byte("export DEFAULT=1\n"), Mode: 0o644},
		"starship.toml":     {Data: []byte("format = '$directory'\n"), Mode: 0o644},
		"nvim/init.lua":     {Data: []byte("vim.opt.number = true\n"), Mode: 0o644},
	}

	if err := manager.EnsureConfigs(); err != nil {
		t.Fatalf("EnsureConfigs() error = %v", err)
	}

	configDir := filepath.Join(home, ".config", "sb")
	assertFileContent(t, filepath.Join(configDir, "zshrc"), "export DEFAULT=1\n")
	assertFileContent(t, filepath.Join(configDir, "starship.toml"), "format = '$directory'\n")
	assertFileContent(t, filepath.Join(configDir, "nvim", "init.lua"), "vim.opt.number = true\n")
}

func TestSandboxManagerExecShellRunsDockerExecWithSetpriv(t *testing.T) {
	stdinBuffer := bytes.NewBufferString("input")
	stdoutBuffer := &bytes.Buffer{}
	stderrBuffer := &bytes.Buffer{}

	var gotCommand string
	var gotArgs []string
	var gotStdin io.Reader
	var gotStdout io.Writer
	var gotStderr io.Writer

	manager := &SandboxManager{
		getUIDGID: func() (int, int) { return 1000, 1001 },
		stdin:     stdinBuffer,
		stdout:    stdoutBuffer,
		stderr:    stderrBuffer,
		runShellCommand: func(ctx context.Context, command string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
			gotCommand = command
			gotArgs = append([]string{}, args...)
			gotStdin = stdin
			gotStdout = stdout
			gotStderr = stderr
			return 17, nil
		},
	}

	exitCode, err := manager.ExecShell(context.Background(), SandboxInfo{
		Name:        "sb-project-f630ad93",
		ContainerID: stringPointer("container-id"),
	})
	if err != nil {
		t.Fatalf("ExecShell() error = %v", err)
	}
	if got, want := exitCode, 17; got != want {
		t.Fatalf("ExecShell() exit code = %d, want %d", got, want)
	}
	if got, want := gotCommand, "docker"; got != want {
		t.Fatalf("ExecShell() command = %q, want %q", got, want)
	}

	wantArgs := []string{
		"exec",
		"-it",
		"-e",
		"HOME=/home/sandbox",
		"-e",
		"USER=sandbox",
		"-w",
		workspaceMountTarget,
		"container-id",
		"setpriv",
		"--reuid",
		"1000",
		"--regid",
		"1001",
		"--init-groups",
		"/bin/zsh",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("ExecShell() args = %#v, want %#v", gotArgs, wantArgs)
	}
	if gotStdin != stdinBuffer || gotStdout != stdoutBuffer || gotStderr != stderrBuffer {
		t.Fatal("ExecShell() did not pass the manager's stdio handles to the command runner")
	}
}

func TestSandboxManagerExecShellRejectsMissingContainerID(t *testing.T) {
	manager := &SandboxManager{}

	_, err := manager.ExecShell(context.Background(), SandboxInfo{Name: "sb-project-f630ad93"})
	if err == nil {
		t.Fatal("ExecShell() error = nil, want missing container ID error")
	}
	if got, want := err.Error(), "Sandbox 'sb-project-f630ad93' has no container ID. It may need to be recreated."; got != want {
		t.Fatalf("ExecShell() error = %q, want %q", got, want)
	}
}

func TestSandboxManagerCreateUsesRealShellConfigManagerByDefault(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace", "project")
	mustMkdirAll(t, workspace)

	const sandboxName = "sb-custom"
	const createdID = "created-id"
	const createdAt = "2026-03-08T10:00:00Z"

	var createdHostConfig *containertypes.HostConfig

	manager := &SandboxManager{
		imageManager: &fakeManagerImageManager{
			ensureImageFunc: func(context.Context, string) error { return nil },
		},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case sandboxName:
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					case createdID:
						return managedInspect(createdID, sandboxName, workspace, createdAt, "created"), nil
					default:
						t.Fatalf("ContainerInspect() id = %q, want %q or %q", containerID, sandboxName, createdID)
						return containertypes.InspectResponse{}, nil
					}
				},
				createFunc: func(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
					if got, want := containerName, sandboxName; got != want {
						t.Fatalf("ContainerCreate() name = %q, want %q", got, want)
					}
					createdHostConfig = hostConfig
					return containertypes.CreateResponse{ID: createdID}, nil
				},
			}, nil
		},
		getUIDGID: func() (int, int) { return 1000, 1001 },
		getenv:    func(string) string { return "" },
	}

	sandbox, err := manager.Create(ctx, CreateOptions{Workspace: workspace, Name: sandboxName})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got, want := sandbox.Name, sandboxName; got != want {
		t.Fatalf("Create() sandbox name = %q, want %q", got, want)
	}

	configDir := filepath.Join(home, ".config", "sb")
	assertPathExists(t, filepath.Join(configDir, "zshrc"))
	assertPathExists(t, filepath.Join(configDir, "starship.toml"))
	assertPathExists(t, filepath.Join(configDir, "nvim", "init.lua"))

	if createdHostConfig == nil {
		t.Fatal("Create() did not call ContainerCreate() with a host config")
	}

	gotMounts := createdHostConfig.Mounts
	wantMounts := []string{
		workspace,
		filepath.Join(configDir, "zshrc"),
		filepath.Join(configDir, "starship.toml"),
		filepath.Join(configDir, "nvim"),
	}
	if len(gotMounts) != len(wantMounts) {
		t.Fatalf("Create() produced %d mounts, want %d", len(gotMounts), len(wantMounts))
	}
	for index, wantSource := range wantMounts {
		if got, want := gotMounts[index].Source, wantSource; got != want {
			t.Fatalf("Create() mount[%d].Source = %q, want %q", index, got, want)
		}
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, got, want)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
}
