package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/fsmiamoto/sb/internal/matching"
	"github.com/fsmiamoto/sb/internal/naming"
)

const (
	managedLabelKey   = "sb.managed"
	managedLabelValue = "true"
	nameLabelKey      = "sb.name"
	workspaceLabelKey = "sb.workspace"

	unknownContainerStatus = "unknown"
)

// ConfirmFunc asks the caller whether a potentially destructive operation
// should continue.
type ConfirmFunc func(string) bool

// WarnFunc reports non-fatal warnings back to the caller.
type WarnFunc func(string)

// CreateOptions describes the inputs for creating a sandbox container.
type CreateOptions struct {
	Workspace   string
	Name        string
	Force       bool
	ExtraMounts []string
	EnvVars     []string
	Image       string
	Confirm     ConfirmFunc
	Warn        WarnFunc
}

// DestroyOptions describes the inputs for destroying a sandbox container.
type DestroyOptions struct {
	Name      string
	Workspace string
	Force     bool
	Confirm   ConfirmFunc
}

// SandboxManagerOptions configures a sandbox manager instance.
type SandboxManagerOptions struct {
	ImageName            string
	ExtraMounts          []string
	EnvPassthrough       []string
	CustomSensitiveDirs  []string
	DockerClientProvider *DockerClientProvider
}

type dockerSandboxClient interface {
	ContainerInspect(context.Context, string) (containertypes.InspectResponse, error)
	ContainerList(context.Context, containertypes.ListOptions) ([]containertypes.Summary, error)
	ContainerCreate(context.Context, *containertypes.Config, *containertypes.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (containertypes.CreateResponse, error)
	ContainerStart(context.Context, string, containertypes.StartOptions) error
	ContainerStop(context.Context, string, containertypes.StopOptions) error
	ContainerRemove(context.Context, string, containertypes.RemoveOptions) error
}

type sandboxImageManager interface {
	EnsureImage(context.Context, string) error
	EnsureCustomImage(context.Context, string) error
}

type sandboxMountBuilder interface {
	Build(workspace string, extraCLIMounts []string) ([]dockermount.Mount, []string, error)
}

// SandboxManager coordinates container lifecycle operations for sb sandboxes.
type SandboxManager struct {
	imageName           string
	extraMounts         []string
	envPassthrough      []string
	customSensitiveDirs []string
	provider            *DockerClientProvider

	getClient          func(context.Context) (dockerSandboxClient, error)
	imageManager       sandboxImageManager
	mountBuilder       sandboxMountBuilder
	getwd              func() (string, error)
	getUIDGID          func() (int, int)
	getenv             func(string) string
	ensureShellConfigs func() error
	stdin              io.Reader
	stdout             io.Writer
	stderr             io.Writer
	runShellCommand    interactiveCommandRunner
}

// NewSandboxManager returns a sandbox manager configured with the shared Docker
// client provider and the default image/mount helpers.
func NewSandboxManager(opts SandboxManagerOptions) *SandboxManager {
	manager := &SandboxManager{
		imageName:           opts.ImageName,
		extraMounts:         cloneStrings(opts.ExtraMounts),
		envPassthrough:      cloneStrings(opts.EnvPassthrough),
		customSensitiveDirs: cloneStrings(opts.CustomSensitiveDirs),
		provider:            opts.DockerClientProvider,
	}
	manager.initDefaults()
	return manager
}

// Create creates a new sandbox container for the requested workspace.
func (m *SandboxManager) Create(ctx context.Context, opts CreateOptions) (SandboxInfo, error) {
	m.initDefaults()

	workspace, err := m.resolveWorkspacePath(opts.Workspace)
	if err != nil {
		return SandboxInfo{}, err
	}

	sandboxName := opts.Name
	if sandboxName == "" {
		sandboxName, err = naming.GenerateName(workspace)
		if err != nil {
			return SandboxInfo{}, fmt.Errorf("generate sandbox name for workspace %q: %w", workspace, err)
		}
	}

	existing, err := m.getSandbox(ctx, sandboxName)
	if err != nil {
		return SandboxInfo{}, err
	}
	if existing != nil {
		if !opts.Force {
			if opts.Confirm != nil {
				message := fmt.Sprintf("Sandbox '%s' already exists.\nDo you want to recreate it?", sandboxName)
				if !opts.Confirm(message) {
					return SandboxInfo{}, errors.New("sandbox creation cancelled by user")
				}
			} else {
				return SandboxInfo{}, fmt.Errorf("sandbox '%s' already exists; use --force to recreate", sandboxName)
			}
		}

		if err := m.destroyContainer(ctx, *existing); err != nil {
			return SandboxInfo{}, err
		}
	}

	if sensitivePath, ok, err := m.checkSensitiveDir(workspace); err != nil {
		return SandboxInfo{}, err
	} else if ok && !opts.Force {
		if opts.Confirm != nil {
			message := fmt.Sprintf(
				"Warning: Creating sandbox with access to '%s' is potentially dangerous.\nThis gives the sandbox write access to this directory.\nContinue?",
				sensitivePath,
			)
			if !opts.Confirm(message) {
				return SandboxInfo{}, errors.New("sandbox creation cancelled by user")
			}
		} else {
			return SandboxInfo{}, errors.New("workspace is a sensitive directory; use --force to override")
		}
	}

	if err := m.ensureShellConfigs(); err != nil {
		return SandboxInfo{}, fmt.Errorf("ensure shell configs: %w", err)
	}

	imageName := m.imageName
	if opts.Image != "" {
		imageName = opts.Image
	}
	imageName = normalizeImageName(imageName)

	if imageName != DefaultImageName {
		if err := m.imageManager.EnsureCustomImage(ctx, imageName); err != nil {
			return SandboxInfo{}, err
		}
	} else {
		if err := m.imageManager.EnsureImage(ctx, imageName); err != nil {
			return SandboxInfo{}, err
		}
	}

	mounts, missingMounts, err := m.mountBuilder.Build(workspace, opts.ExtraMounts)
	if err != nil {
		return SandboxInfo{}, err
	}
	if opts.Warn != nil {
		for _, path := range missingMounts {
			opts.Warn(fmt.Sprintf("Mount path does not exist, skipping: %s", path))
		}
	}

	environment := m.buildEnvironment(opts.EnvVars)
	labels := map[string]string{
		managedLabelKey:   managedLabelValue,
		nameLabelKey:      sandboxName,
		workspaceLabelKey: workspace,
	}

	cli, err := m.getClient(ctx)
	if err != nil {
		return SandboxInfo{}, err
	}

	response, err := cli.ContainerCreate(
		ctx,
		&containertypes.Config{
			Image:      imageName,
			Env:        environment,
			Labels:     labels,
			OpenStdin:  true,
			Tty:        true,
			WorkingDir: workspaceMountTarget,
		},
		&containertypes.HostConfig{
			Mounts: mounts,
		},
		nil,
		nil,
		sandboxName,
	)
	if err != nil {
		return SandboxInfo{}, fmt.Errorf("create sandbox container %q: %w", sandboxName, err)
	}

	inspect, err := cli.ContainerInspect(ctx, response.ID)
	if err != nil {
		return SandboxInfo{}, fmt.Errorf("inspect newly created sandbox %q: %w", sandboxName, err)
	}

	return sandboxInfoFromInspect(inspect), nil
}

// Attach resolves a sandbox by name or workspace and starts it if it is not running.
func (m *SandboxManager) Attach(ctx context.Context, name string, workspace string) (SandboxInfo, error) {
	m.initDefaults()

	sandbox, err := m.resolveSandbox(ctx, name, workspace, "")
	if err != nil {
		return SandboxInfo{}, err
	}

	if !sandbox.hasContainerID() {
		return SandboxInfo{}, fmt.Errorf("sandbox '%s' has no container ID; it may need to be recreated", sandbox.Name)
	}

	status, err := m.GetContainerStatus(ctx, sandbox)
	if err != nil {
		return SandboxInfo{}, err
	}
	if status != "running" {
		cli, err := m.getClient(ctx)
		if err != nil {
			return SandboxInfo{}, err
		}
		if err := cli.ContainerStart(ctx, *sandbox.ContainerID, containertypes.StartOptions{}); err != nil {
			return SandboxInfo{}, fmt.Errorf("start sandbox %q: %w", sandbox.Name, err)
		}
	}

	return sandbox, nil
}

// Stop resolves a sandbox by name or workspace and stops it if it is running.
func (m *SandboxManager) Stop(ctx context.Context, name string, workspace string) (SandboxInfo, error) {
	m.initDefaults()

	sandbox, err := m.resolveSandbox(ctx, name, workspace, "")
	if err != nil {
		return SandboxInfo{}, err
	}

	if !sandbox.hasContainerID() {
		return SandboxInfo{}, fmt.Errorf("sandbox '%s' has no container ID; it may need to be recreated", sandbox.Name)
	}

	status, err := m.GetContainerStatus(ctx, sandbox)
	if err != nil {
		return SandboxInfo{}, err
	}
	if status == "running" {
		cli, err := m.getClient(ctx)
		if err != nil {
			return SandboxInfo{}, err
		}
		if err := cli.ContainerStop(ctx, *sandbox.ContainerID, containertypes.StopOptions{}); err != nil {
			return SandboxInfo{}, fmt.Errorf("stop sandbox %q: %w", sandbox.Name, err)
		}
	}

	return sandbox, nil
}

// Destroy removes a sandbox container after optional confirmation.
func (m *SandboxManager) Destroy(ctx context.Context, opts DestroyOptions) (SandboxInfo, error) {
	m.initDefaults()

	sandbox, err := m.resolveSandbox(
		ctx,
		opts.Name,
		opts.Workspace,
		"No sandbox found for workspace '%s'. Nothing to destroy.",
	)
	if err != nil {
		return SandboxInfo{}, err
	}

	if !opts.Force {
		if opts.Confirm != nil {
			message := fmt.Sprintf("Are you sure you want to destroy sandbox '%s'?\nThis will stop and remove the container.", sandbox.Name)
			if !opts.Confirm(message) {
				return SandboxInfo{}, errors.New("sandbox destruction cancelled by user")
			}
		} else {
			return SandboxInfo{}, fmt.Errorf("use --force to destroy sandbox '%s' without confirmation", sandbox.Name)
		}
	}

	if err := m.destroyContainer(ctx, sandbox); err != nil {
		return SandboxInfo{}, err
	}

	return sandbox, nil
}

// List returns metadata for every sb-managed container.
func (m *SandboxManager) List(ctx context.Context) ([]SandboxInfo, error) {
	m.initDefaults()

	cli, err := m.getClient(ctx)
	if err != nil {
		return nil, err
	}

	containers, err := cli.ContainerList(ctx, containertypes.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", managedLabelKey+"="+managedLabelValue),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list sandbox containers: %w", err)
	}

	sandboxes := make([]SandboxInfo, 0, len(containers))
	for _, container := range containers {
		sandboxes = append(sandboxes, sandboxInfoFromSummary(container))
	}

	return sandboxes, nil
}

// FindSandboxes resolves a fuzzy sandbox query using the same priority rules as
// the Python implementation.
func (m *SandboxManager) FindSandboxes(ctx context.Context, query string) ([]SandboxInfo, error) {
	sandboxes, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	return matching.FindMatchingSandboxes(query, sandboxes), nil
}

// GetContainerStatus returns the current status string for a sandbox container.
func (m *SandboxManager) GetContainerStatus(ctx context.Context, sandbox SandboxInfo) (string, error) {
	m.initDefaults()

	if !sandbox.hasContainerID() {
		return unknownContainerStatus, nil
	}

	cli, err := m.getClient(ctx)
	if err != nil {
		return "", err
	}

	inspect, err := cli.ContainerInspect(ctx, *sandbox.ContainerID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return unknownContainerStatus, nil
		}
		return "", fmt.Errorf("inspect container for sandbox %q: %w", sandbox.Name, err)
	}

	if inspect.State == nil || inspect.State.Status == "" {
		return unknownContainerStatus, nil
	}

	return string(inspect.State.Status), nil
}

func (m *SandboxManager) initDefaults() {
	if m.imageName == "" {
		m.imageName = DefaultImageName
	}
	if m.provider == nil {
		m.provider = NewDockerClientProvider()
	}
	if m.getClient == nil {
		provider := m.provider
		m.getClient = func(ctx context.Context) (dockerSandboxClient, error) {
			return provider.Client(ctx)
		}
	}
	if m.imageManager == nil {
		m.imageManager = NewImageManager(m.provider)
	}
	if m.mountBuilder == nil {
		m.mountBuilder = NewMountBuilder(m.extraMounts)
	}
	if m.getwd == nil {
		m.getwd = os.Getwd
	}
	if m.getUIDGID == nil {
		m.getUIDGID = func() (int, int) {
			return os.Getuid(), os.Getgid()
		}
	}
	if m.getenv == nil {
		m.getenv = os.Getenv
	}
	if m.ensureShellConfigs == nil {
		m.ensureShellConfigs = NewShellConfigManager("").EnsureConfigs
	}
	if m.stdin == nil {
		m.stdin = os.Stdin
	}
	if m.stdout == nil {
		m.stdout = os.Stdout
	}
	if m.stderr == nil {
		m.stderr = os.Stderr
	}
	if m.runShellCommand == nil {
		m.runShellCommand = runInteractiveCommand
	}
}

func (m *SandboxManager) getSandbox(ctx context.Context, name string) (*SandboxInfo, error) {
	if name == "" {
		return nil, nil
	}

	cli, err := m.getClient(ctx)
	if err != nil {
		return nil, err
	}

	inspect, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect sandbox %q: %w", name, err)
	}

	if !isManagedInspect(inspect) {
		return nil, nil
	}

	info := sandboxInfoFromInspect(inspect)
	return &info, nil
}

func (m *SandboxManager) resolveSandbox(ctx context.Context, name string, workspace string, notFoundMessage string) (SandboxInfo, error) {
	if name != "" {
		sandbox, err := m.getSandbox(ctx, name)
		if err != nil {
			return SandboxInfo{}, err
		}
		if sandbox == nil {
			return SandboxInfo{}, fmt.Errorf("sandbox '%s' not found", name)
		}
		return *sandbox, nil
	}

	resolvedWorkspace, err := m.resolveWorkspacePath(workspace)
	if err != nil {
		return SandboxInfo{}, err
	}

	sandboxName, err := naming.GenerateName(resolvedWorkspace)
	if err != nil {
		return SandboxInfo{}, fmt.Errorf("generate sandbox name for workspace %q: %w", resolvedWorkspace, err)
	}

	sandbox, err := m.getSandbox(ctx, sandboxName)
	if err != nil {
		return SandboxInfo{}, err
	}
	if sandbox == nil {
		if notFoundMessage == "" {
			notFoundMessage = "No sandbox found for workspace '%s'. Use 'sb create' to create one."
		}
		return SandboxInfo{}, fmt.Errorf(notFoundMessage, resolvedWorkspace)
	}

	return *sandbox, nil
}

func (m *SandboxManager) destroyContainer(ctx context.Context, sandbox SandboxInfo) error {
	if !sandbox.hasContainerID() {
		return nil
	}

	cli, err := m.getClient(ctx)
	if err != nil {
		return err
	}

	inspect, err := cli.ContainerInspect(ctx, *sandbox.ContainerID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect container for sandbox %q: %w", sandbox.Name, err)
	}

	if inspect.State != nil && inspect.State.Status == "running" {
		if err := cli.ContainerStop(ctx, *sandbox.ContainerID, containertypes.StopOptions{}); err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("stop sandbox %q before removal: %w", sandbox.Name, err)
		}
	}

	if err := cli.ContainerRemove(ctx, *sandbox.ContainerID, containertypes.RemoveOptions{}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove sandbox %q: %w", sandbox.Name, err)
	}

	return nil
}

func (m *SandboxManager) checkSensitiveDir(path string) (string, bool, error) {
	resolvedPath, err := expandAndAbsPath(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace path %q: %w", path, err)
	}

	allSensitive := append(cloneStrings(SensitiveDirs), m.customSensitiveDirs...)
	for _, sensitiveDir := range allSensitive {
		resolvedSensitive, err := expandAndAbsPath(sensitiveDir)
		if err != nil {
			return "", false, fmt.Errorf("resolve sensitive path %q: %w", sensitiveDir, err)
		}
		if resolvedPath == resolvedSensitive {
			return resolvedPath, true, nil
		}
	}

	return "", false, nil
}

func (m *SandboxManager) resolveWorkspacePath(workspace string) (string, error) {
	if workspace == "" {
		cwd, err := m.getwd()
		if err != nil {
			return "", fmt.Errorf("get current working directory: %w", err)
		}
		workspace = cwd
	}

	resolvedWorkspace, err := expandAndAbsPath(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path %q: %w", workspace, err)
	}

	return resolvedWorkspace, nil
}

func (m *SandboxManager) buildEnvironment(extraEnvVars []string) []string {
	uid, gid := m.getUIDGID()
	env := map[string]string{
		"HOST_UID": fmt.Sprintf("%d", uid),
		"HOST_GID": fmt.Sprintf("%d", gid),
	}

	allEnvVars := append(cloneStrings(m.envPassthrough), extraEnvVars...)
	for _, entry := range allEnvVars {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
			continue
		}

		value := m.getenv(entry)
		if value != "" {
			env[entry] = value
		}
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}

	return result
}

func sandboxInfoFromInspect(inspect containertypes.InspectResponse) SandboxInfo {
	labels := inspectLabels(inspect)
	name := labels[nameLabelKey]

	var id, createdAt string
	if inspect.ContainerJSONBase != nil {
		id = inspect.ID
		createdAt = inspect.Created
		if name == "" {
			name = strings.TrimPrefix(inspect.Name, "/")
		}
	}

	return SandboxInfo{
		Name:        name,
		Workspace:   labels[workspaceLabelKey],
		CreatedAt:   createdAt,
		ContainerID: stringPointer(id),
	}
}

func sandboxInfoFromSummary(summary containertypes.Summary) SandboxInfo {
	name := summary.Labels[nameLabelKey]
	if name == "" && len(summary.Names) > 0 {
		name = strings.TrimPrefix(summary.Names[0], "/")
	}

	createdAt := ""
	if summary.Created > 0 {
		createdAt = time.Unix(summary.Created, 0).UTC().Format(time.RFC3339)
	}

	return SandboxInfo{
		Name:        name,
		Workspace:   summary.Labels[workspaceLabelKey],
		CreatedAt:   createdAt,
		ContainerID: stringPointer(summary.ID),
	}
}

func inspectLabels(inspect containertypes.InspectResponse) map[string]string {
	if inspect.Config == nil || inspect.Config.Labels == nil {
		return map[string]string{}
	}
	return inspect.Config.Labels
}

func isManagedInspect(inspect containertypes.InspectResponse) bool {
	return inspectLabels(inspect)[managedLabelKey] == managedLabelValue
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	valueCopy := value
	return &valueCopy
}

func cloneStrings(values []string) []string {
	return append([]string{}, values...)
}
