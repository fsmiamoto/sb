package sandbox

import "os"

// MountMode describes whether a bind mount is read-only or read-write.
type MountMode string

const (
	// MountModeReadOnly mounts a path read-only inside the container.
	MountModeReadOnly MountMode = "ro"
	// MountModeReadWrite mounts a path read-write inside the container.
	MountModeReadWrite MountMode = "rw"

	// DefaultImageName is the bundled sb sandbox image tag.
	DefaultImageName = "sb-sandbox:latest"

	// sandboxHomeDir is the home directory for the sandbox user inside the container.
	sandboxHomeDir = "/home/sandbox"
)

// MountSpec describes a bind mount between the host and sandbox container.
type MountSpec struct {
	Host      string
	Container string
	Mode      MountMode
}

// SandboxInfo captures the metadata sb tracks for a sandbox container.
type SandboxInfo struct {
	Name        string
	Workspace   string
	CreatedAt   string
	ContainerID *string
	Status      string
}

// GetName returns the sandbox name so SandboxInfo can participate in fuzzy matching.
func (s SandboxInfo) GetName() string {
	return s.Name
}

// hasContainerID reports whether the sandbox has a non-empty container ID.
func (s SandboxInfo) hasContainerID() bool {
	return s.ContainerID != nil && *s.ContainerID != ""
}

// SensitiveDirs contains the built-in directories that should trigger a warning
// when users try to sandbox them directly.
//
// The current user's home directory is resolved during package initialization,
// matching the Python implementation's import-time Path.home() behavior.
var SensitiveDirs = buildSensitiveDirs()

// DefaultMounts is the built-in set of user config mounts applied to sandboxes.
var DefaultMounts = []MountSpec{
	{
		Host:      "~/.claude/",
		Container: sandboxHomeDir + "/.claude/",
		Mode:      MountModeReadWrite,
	},
	{
		Host:      "~/.claude.json",
		Container: sandboxHomeDir + "/.claude.json",
		Mode:      MountModeReadWrite,
	},
	{
		Host:      "~/.config/claude-code/",
		Container: sandboxHomeDir + "/.config/claude-code/",
		Mode:      MountModeReadWrite,
	},
	{
		Host:      "~/.codex/",
		Container: sandboxHomeDir + "/.codex/",
		Mode:      MountModeReadWrite,
	},
	{
		Host:      "~/.pi/",
		Container: sandboxHomeDir + "/.pi/",
		Mode:      MountModeReadWrite,
	},
	{
		Host:      "~/.gitconfig",
		Container: sandboxHomeDir + "/.gitconfig",
		Mode:      MountModeReadOnly,
	},
	{
		Host:      "~/.config/sb/zshrc",
		Container: sandboxHomeDir + "/.zshrc",
		Mode:      MountModeReadOnly,
	},
	{
		Host:      "~/.config/sb/starship.toml",
		Container: sandboxHomeDir + "/.config/starship.toml",
		Mode:      MountModeReadOnly,
	},
	{
		Host:      "~/.config/sb/nvim/",
		Container: sandboxHomeDir + "/.config/nvim/",
		Mode:      MountModeReadWrite,
	},
}

func buildSensitiveDirs() []string {
	dirs := []string{"/"}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		dirs = append(dirs, home)
	}

	dirs = append(dirs,
		"/etc",
		"/var",
		"/usr",
		"/bin",
		"/sbin",
	)

	return dirs
}
