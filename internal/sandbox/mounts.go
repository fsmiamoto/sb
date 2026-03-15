package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/fsmiamoto/sb/internal/pathutil"
)

const workspaceMountTarget = sandboxHomeDir + "/workspace"

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

	return deduplicateMounts(mounts), missing, nil
}

// deduplicateMounts removes mounts with duplicate target paths, keeping the
// last occurrence. This gives proper precedence: CLI > config > defaults.
func deduplicateMounts(mounts []dockermount.Mount) []dockermount.Mount {
	seen := make(map[string]int, len(mounts))
	for i, m := range mounts {
		seen[m.Target] = i
	}
	if len(seen) == len(mounts) {
		return mounts
	}
	result := make([]dockermount.Mount, 0, len(seen))
	for i, m := range mounts {
		if seen[m.Target] == i {
			result = append(result, m)
		}
	}
	return result
}

func buildDefaultMounts(specs []MountSpec) ([]dockermount.Mount, error) {
	mounts := make([]dockermount.Mount, 0, len(specs))
	for _, spec := range specs {
		hostPath := pathutil.ExpandHome(spec.Host)
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
		expandedHost := pathutil.ExpandHome(hostPath)
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
	return filepath.Abs(pathutil.ExpandHome(path))
}

func mapContainerPath(hostPath string) string {
	switch {
	case hostPath == "~":
		return sandboxHomeDir
	case strings.HasPrefix(hostPath, "~/"), strings.HasPrefix(hostPath, "~\\"):
		return sandboxHomeDir + "/" + filepath.ToSlash(filepath.Clean(hostPath[2:]))
	default:
		return hostPath
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
