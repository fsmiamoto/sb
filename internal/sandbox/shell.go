package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/fsmiamoto/sb/assets"
)

const (
	sandboxHomeDir = "/home/sandbox"
	sandboxUser    = "sandbox"
)

type interactiveCommandRunner func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error)

// ShellConfigManager ensures the user-editable shell config files mounted into
// sandbox containers exist on the host, copying bundled defaults when missing.
type ShellConfigManager struct {
	configDir  string
	defaultsFS fs.FS

	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
	copyFS    func(string, fs.FS) error
}

// NewShellConfigManager returns a shell config manager. When configDir is empty,
// the default ~/.config/sb directory is used.
func NewShellConfigManager(configDir string) *ShellConfigManager {
	return &ShellConfigManager{configDir: configDir}
}

// EnsureConfigs creates the shell config directory and copies any missing
// bundled defaults (zshrc, starship.toml, and the nvim config tree).
func (m *ShellConfigManager) EnsureConfigs() error {
	m.initDefaults()

	configDir, err := m.resolveConfigDir()
	if err != nil {
		return err
	}

	defaultsFS, err := m.resolveDefaultsFS()
	if err != nil {
		return fmt.Errorf("load bundled shell configs: %w", err)
	}

	if err := m.mkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create shell config directory %q: %w", configDir, err)
	}

	entries := []struct {
		name  string
		isDir bool
	}{
		{name: "zshrc"},
		{name: "starship.toml"},
		{name: "nvim", isDir: true},
	}

	for _, entry := range entries {
		destination := filepath.Join(configDir, entry.name)
		if pathExists(destination) {
			continue
		}

		if entry.isDir {
			if err := m.copyDirectory(defaultsFS, entry.name, destination); err != nil {
				return err
			}
			continue
		}

		if err := m.copyFile(defaultsFS, entry.name, destination); err != nil {
			return err
		}
	}

	return nil
}

// ExecShell launches an interactive zsh session inside a running sandbox using
// docker exec and the same setpriv-based privilege drop as the Python version.
func (m *SandboxManager) ExecShell(ctx context.Context, sandbox SandboxInfo) (int, error) {
	m.initDefaults()

	if !sandbox.hasContainerID() {
		return 0, fmt.Errorf("Sandbox '%s' has no container ID. It may need to be recreated.", sandbox.Name)
	}

	uid, gid := m.getUIDGID()
	exitCode, err := m.runShellCommand(
		ctx,
		"docker",
		buildExecShellArgs(*sandbox.ContainerID, uid, gid),
		m.stdin,
		m.stdout,
		m.stderr,
	)
	if err != nil {
		return 0, fmt.Errorf("execute shell in sandbox %q: %w", sandbox.Name, err)
	}

	return exitCode, nil
}

func (m *ShellConfigManager) initDefaults() {
	if m.mkdirAll == nil {
		m.mkdirAll = os.MkdirAll
	}
	if m.writeFile == nil {
		m.writeFile = os.WriteFile
	}
	if m.copyFS == nil {
		m.copyFS = os.CopyFS
	}
}

func (m *ShellConfigManager) resolveConfigDir() (string, error) {
	if m.configDir != "" {
		return m.configDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory for shell configs: %w", err)
	}
	if home == "" {
		return "", errors.New("resolve user home directory for shell configs: empty home directory")
	}

	return filepath.Join(home, ".config", "sb"), nil
}

func (m *ShellConfigManager) resolveDefaultsFS() (fs.FS, error) {
	if m.defaultsFS != nil {
		return m.defaultsFS, nil
	}

	return fs.Sub(assets.DockerContextFS(), "configs")
}

func (m *ShellConfigManager) copyFile(defaultsFS fs.FS, source string, destination string) error {
	data, err := fs.ReadFile(defaultsFS, source)
	if err != nil {
		return fmt.Errorf("read default shell config %q: %w", source, err)
	}

	info, err := fs.Stat(defaultsFS, source)
	if err != nil {
		return fmt.Errorf("stat default shell config %q: %w", source, err)
	}

	if err := m.mkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create parent directory for shell config %q: %w", destination, err)
	}

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}

	if err := m.writeFile(destination, data, mode); err != nil {
		return fmt.Errorf("write shell config %q: %w", destination, err)
	}

	return nil
}

func (m *ShellConfigManager) copyDirectory(defaultsFS fs.FS, source string, destination string) error {
	subFS, err := fs.Sub(defaultsFS, source)
	if err != nil {
		return fmt.Errorf("read default shell config directory %q: %w", source, err)
	}

	if err := m.mkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create shell config directory %q: %w", destination, err)
	}

	if err := m.copyFS(destination, subFS); err != nil {
		return fmt.Errorf("copy default shell config directory %q: %w", destination, err)
	}

	return nil
}

func buildExecShellArgs(containerID string, uid int, gid int) []string {
	return []string{
		"exec",
		"-it",
		"-e",
		"HOME=" + sandboxHomeDir,
		"-e",
		"USER=" + sandboxUser,
		"-w",
		workspaceMountTarget,
		containerID,
		"setpriv",
		"--reuid",
		strconv.Itoa(uid),
		"--regid",
		strconv.Itoa(gid),
		"--init-groups",
		"/bin/zsh",
	}
}

func runInteractiveCommand(ctx context.Context, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, err
	}

	if cmd.ProcessState == nil {
		return 0, nil
	}

	return cmd.ProcessState.ExitCode(), nil
}
