package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	dockermount "github.com/docker/docker/api/types/mount"
)

const workspaceMountTarget = "/home/sandbox/workspace"

// MountBuilder assembles the bind mounts used by sandbox containers.
//
// The workspace is always mounted read-write. Built-in config mounts are added
// when they exist on disk and silently skipped otherwise. User-provided extra
// mounts are always read-only and any missing paths are reported back to the
// caller using their original input strings.
//
// Like the Python implementation, container path mapping only treats host paths
// beginning with "~" specially. Other paths are mounted at the same absolute
// path inside the container.
type MountBuilder struct {
	extraMounts []string
}

// NewMountBuilder returns a mount builder seeded with config-provided extra
// mount paths.
func NewMountBuilder(extraMounts []string) *MountBuilder {
	return &MountBuilder{extraMounts: slices.Clone(extraMounts)}
}

// Build constructs the workspace, default, and user-specified bind mounts.
//
// The returned missing slice only contains user-specified extra mounts from
// config or the CLI; missing built-in defaults are intentionally ignored.
func (b *MountBuilder) Build(workspace string, extraCLIMounts []string) ([]dockermount.Mount, []string, error) {
	workspaceSource, err := expandAndAbsPath(workspace)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve workspace mount path %q: %w", workspace, err)
	}

	mounts := []dockermount.Mount{buildBindMount(workspaceSource, workspaceMountTarget, false)}

	defaultMounts, err := buildDefaultMounts(DefaultMounts)
	if err != nil {
		return nil, nil, err
	}
	mounts = append(mounts, defaultMounts...)

	configMounts, configMissing, err := buildReadOnlyExtraMounts(b.extraMounts)
	if err != nil {
		return nil, nil, err
	}
	mounts = append(mounts, configMounts...)

	cliMounts, cliMissing, err := buildReadOnlyExtraMounts(extraCLIMounts)
	if err != nil {
		return nil, nil, err
	}
	mounts = append(mounts, cliMounts...)

	missing := slices.Concat(configMissing, cliMissing)

	return mounts, missing, nil
}

func buildDefaultMounts(specs []MountSpec) ([]dockermount.Mount, error) {
	mounts := make([]dockermount.Mount, 0, len(specs))
	for _, spec := range specs {
		hostPath := expandHomePath(spec.Host)
		if !pathExists(hostPath) {
			continue
		}

		source, err := filepath.Abs(hostPath)
		if err != nil {
			return nil, fmt.Errorf("resolve default mount path %q: %w", spec.Host, err)
		}

		mounts = append(mounts, buildBindMount(source, spec.Container, spec.Mode == MountModeReadOnly))
	}

	return mounts, nil
}

func buildReadOnlyExtraMounts(paths []string) ([]dockermount.Mount, []string, error) {
	mounts := make([]dockermount.Mount, 0, len(paths))
	missing := make([]string, 0)

	for _, hostPath := range paths {
		expandedHost := expandHomePath(hostPath)
		if !pathExists(expandedHost) {
			missing = append(missing, hostPath)
			continue
		}

		source, err := filepath.Abs(expandedHost)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve extra mount path %q: %w", hostPath, err)
		}

		mounts = append(mounts, buildBindMount(source, mapContainerPath(hostPath), true))
	}

	return mounts, missing, nil
}

func buildBindMount(source string, target string, readOnly bool) dockermount.Mount {
	return dockermount.Mount{
		Type:     dockermount.TypeBind,
		Source:   source,
		Target:   target,
		ReadOnly: readOnly,
	}
}

func expandAndAbsPath(path string) (string, error) {
	return filepath.Abs(expandHomePath(path))
}

func expandHomePath(path string) string {
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

func mapContainerPath(hostPath string) string {
	switch {
	case hostPath == "~", strings.HasPrefix(hostPath, "~/"), strings.HasPrefix(hostPath, "~\\"):
		// Continue below.
	default:
		return hostPath
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return hostPath
	}

	expandedHost := expandHomePath(hostPath)
	relPath, err := filepath.Rel(home, expandedHost)
	if err != nil {
		return hostPath
	}

	return "/home/sandbox/" + filepath.ToSlash(relPath)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
