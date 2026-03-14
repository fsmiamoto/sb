package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type fakeSandboxClient struct {
	inspectFunc func(context.Context, string) (containertypes.InspectResponse, error)
	listFunc    func(context.Context, containertypes.ListOptions) ([]containertypes.Summary, error)
	createFunc  func(context.Context, *containertypes.Config, *containertypes.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (containertypes.CreateResponse, error)
	startFunc   func(context.Context, string, containertypes.StartOptions) error
	stopFunc    func(context.Context, string, containertypes.StopOptions) error
	removeFunc  func(context.Context, string, containertypes.RemoveOptions) error
}

func (c *fakeSandboxClient) ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
	if c.inspectFunc == nil {
		return containertypes.InspectResponse{}, nil
	}
	return c.inspectFunc(ctx, containerID)
}

func (c *fakeSandboxClient) ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
	if c.listFunc == nil {
		return nil, nil
	}
	return c.listFunc(ctx, options)
}

func (c *fakeSandboxClient) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	if c.createFunc == nil {
		return containertypes.CreateResponse{}, nil
	}
	return c.createFunc(ctx, config, hostConfig, networkingConfig, platform, containerName)
}

func (c *fakeSandboxClient) ContainerStart(ctx context.Context, containerID string, options containertypes.StartOptions) error {
	if c.startFunc == nil {
		return nil
	}
	return c.startFunc(ctx, containerID, options)
}

func (c *fakeSandboxClient) ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error {
	if c.stopFunc == nil {
		return nil
	}
	return c.stopFunc(ctx, containerID, options)
}

func (c *fakeSandboxClient) ContainerRemove(ctx context.Context, containerID string, options containertypes.RemoveOptions) error {
	if c.removeFunc == nil {
		return nil
	}
	return c.removeFunc(ctx, containerID, options)
}

type fakeManagerImageManager struct {
	ensureImageFunc       func(context.Context, string) error
	ensureCustomImageFunc func(context.Context, string) error
}

func (m *fakeManagerImageManager) EnsureImage(ctx context.Context, imageName string) error {
	if m.ensureImageFunc == nil {
		return nil
	}
	return m.ensureImageFunc(ctx, imageName)
}

func (m *fakeManagerImageManager) EnsureCustomImage(ctx context.Context, imageName string) error {
	if m.ensureCustomImageFunc == nil {
		return nil
	}
	return m.ensureCustomImageFunc(ctx, imageName)
}

type fakeManagerMountBuilder struct {
	buildFunc func(string, []string) ([]mount.Mount, []string, error)
}

func (b *fakeManagerMountBuilder) Build(workspace string, extraCLIMounts []string) ([]mount.Mount, []string, error) {
	if b.buildFunc == nil {
		return nil, nil, nil
	}
	return b.buildFunc(workspace, extraCLIMounts)
}

func TestSandboxManagerCreateBuildsBundledImageAndCreatesContainer(t *testing.T) {
	ctx := context.Background()
	workspace := "/tmp/project"
	createdID := "new-container-id"
	createdAt := "2026-03-08T10:00:00Z"
	mounts := []mount.Mount{{Type: mount.TypeBind, Source: workspace, Target: workspaceMountTarget, ReadOnly: false}}

	var ensuredBundledImage string
	var createdConfig *containertypes.Config
	var createdHostConfig *containertypes.HostConfig
	var createdContainerName string
	warns := make([]string, 0)
	ensureConfigsCalled := 0

	manager := &SandboxManager{
		imageName:      "sb-sandbox:test",
		envPassthrough: []string{"TOKEN", "EMPTY"},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					if containerID == "sb-project-f630ad93" {
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					}
					if containerID != createdID {
						t.Fatalf("ContainerInspect() id = %q, want %q or sandbox lookup", containerID, createdID)
					}
					return managedInspect(createdID, "sb-project-f630ad93", workspace, createdAt, "created"), nil
				},
				createFunc: func(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
					createdConfig = config
					createdHostConfig = hostConfig
					createdContainerName = containerName
					return containertypes.CreateResponse{ID: createdID}, nil
				},
			}, nil
		},
		imageManager: &fakeManagerImageManager{
			ensureImageFunc: func(ctx context.Context, imageName string) error {
				ensuredBundledImage = imageName
				return nil
			},
		},
		mountBuilder: &fakeManagerMountBuilder{
			buildFunc: func(gotWorkspace string, extraCLIMounts []string) ([]mount.Mount, []string, error) {
				if gotWorkspace != workspace {
					t.Fatalf("Build() workspace = %q, want %q", gotWorkspace, workspace)
				}
				if !reflect.DeepEqual(extraCLIMounts, []string{"~/extra", "~/missing"}) {
					t.Fatalf("Build() extraCLIMounts = %#v, want %#v", extraCLIMounts, []string{"~/extra", "~/missing"})
				}
				return mounts, []string{"~/missing"}, nil
			},
		},
		getUIDGID: func() (int, int) { return 1000, 1001 },
		getenv: func(key string) string {
			switch key {
			case "TOKEN":
				return "secret"
			case "EMPTY":
				return ""
			default:
				return ""
			}
		},
		ensureShellConfigs: func() error {
			ensureConfigsCalled++
			return nil
		},
	}

	sandbox, err := manager.Create(ctx, CreateOptions{
		Workspace:   workspace,
		ExtraMounts: []string{"~/extra", "~/missing"},
		EnvVars:     []string{"FEATURE=enabled", "TOKEN=override", "CLI_ONLY"},
		Warn: func(message string) {
			warns = append(warns, message)
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if got, want := ensuredBundledImage, "sb-sandbox:test"; got != want {
		t.Fatalf("EnsureImage() image = %q, want %q", got, want)
	}
	if ensureConfigsCalled != 1 {
		t.Fatalf("ensureShellConfigs called %d times, want 1", ensureConfigsCalled)
	}
	if createdContainerName != "sb-project-f630ad93" {
		t.Fatalf("ContainerCreate() name = %q, want %q", createdContainerName, "sb-project-f630ad93")
	}
	if createdConfig == nil {
		t.Fatal("Create() did not call ContainerCreate() with a container config")
	}
	if createdHostConfig == nil {
		t.Fatal("Create() did not call ContainerCreate() with a host config")
	}
	if got, want := createdConfig.Image, "sb-sandbox:test"; got != want {
		t.Fatalf("ContainerCreate() image = %q, want %q", got, want)
	}
	if got, want := createdConfig.WorkingDir, workspaceMountTarget; got != want {
		t.Fatalf("ContainerCreate() working dir = %q, want %q", got, want)
	}
	if !createdConfig.OpenStdin || !createdConfig.Tty {
		t.Fatalf("ContainerCreate() OpenStdin/Tty = %v/%v, want true/true", createdConfig.OpenStdin, createdConfig.Tty)
	}
	wantEnv := []string{
		"FEATURE=enabled",
		"HOST_GID=1001",
		"HOST_UID=1000",
		"TOKEN=override",
	}
	if !reflect.DeepEqual(createdConfig.Env, wantEnv) {
		t.Fatalf("ContainerCreate() env = %#v, want %#v", createdConfig.Env, wantEnv)
	}
	wantLabels := map[string]string{
		managedLabelKey:   managedLabelValue,
		nameLabelKey:      "sb-project-f630ad93",
		workspaceLabelKey: workspace,
	}
	if !reflect.DeepEqual(createdConfig.Labels, wantLabels) {
		t.Fatalf("ContainerCreate() labels = %#v, want %#v", createdConfig.Labels, wantLabels)
	}
	if !reflect.DeepEqual(createdHostConfig.Mounts, mounts) {
		t.Fatalf("ContainerCreate() mounts = %#v, want %#v", createdHostConfig.Mounts, mounts)
	}
	wantWarns := []string{"Mount path does not exist, skipping: ~/missing"}
	if !reflect.DeepEqual(warns, wantWarns) {
		t.Fatalf("warns = %#v, want %#v", warns, wantWarns)
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Create() sandbox name = %q, want %q", got, want)
	}
	if got, want := sandbox.Workspace, workspace; got != want {
		t.Fatalf("Create() sandbox workspace = %q, want %q", got, want)
	}
	if got, want := sandbox.CreatedAt, createdAt; got != want {
		t.Fatalf("Create() sandbox created_at = %q, want %q", got, want)
	}
	if sandbox.ContainerID == nil || *sandbox.ContainerID != createdID {
		t.Fatalf("Create() sandbox container ID = %#v, want %q", sandbox.ContainerID, createdID)
	}
}

func TestSandboxManagerCreateUsesCustomImageWhenCLIImageProvided(t *testing.T) {
	ctx := context.Background()
	workspace := "/tmp/project"
	customImage := "ghcr.io/example/sb:latest"
	customEnsured := ""
	bundledCalled := false

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					if containerID == "sb-project-f630ad93" {
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					}
					return managedInspect("new-id", "sb-project-f630ad93", workspace, "2026-03-08T10:00:00Z", "created"), nil
				},
				createFunc: func(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
					if got, want := config.Image, customImage; got != want {
						t.Fatalf("ContainerCreate() image = %q, want %q", got, want)
					}
					return containertypes.CreateResponse{ID: "new-id"}, nil
				},
			}, nil
		},
		imageManager: &fakeManagerImageManager{
			ensureImageFunc: func(context.Context, string) error {
				bundledCalled = true
				return nil
			},
			ensureCustomImageFunc: func(ctx context.Context, imageName string) error {
				customEnsured = imageName
				return nil
			},
		},
		mountBuilder:       &fakeManagerMountBuilder{buildFunc: func(string, []string) ([]mount.Mount, []string, error) { return nil, nil, nil }},
		ensureShellConfigs: func() error { return nil },
	}

	if _, err := manager.Create(ctx, CreateOptions{Workspace: workspace, Image: customImage}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if bundledCalled {
		t.Fatal("Create() called EnsureImage() for an explicit custom image override")
	}
	if got, want := customEnsured, customImage; got != want {
		t.Fatalf("EnsureCustomImage() image = %q, want %q", got, want)
	}
}

func TestSandboxManagerCreateRejectsExistingSandboxWithoutForceOrConfirmation(t *testing.T) {
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					if containerID != "sb-project-f630ad93" {
						t.Fatalf("ContainerInspect() id = %q, want %q", containerID, "sb-project-f630ad93")
					}
					return managedInspect("existing-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
				},
			}, nil
		},
	}

	_, err := manager.Create(ctx, CreateOptions{Workspace: "/tmp/project"})
	if err == nil {
		t.Fatal("Create() error = nil, want existing sandbox error")
	}
	if got, want := err.Error(), "Sandbox 'sb-project-f630ad93' already exists. Use --force to recreate."; got != want {
		t.Fatalf("Create() error = %q, want %q", got, want)
	}
}

func TestSandboxManagerCreateRejectsSensitiveWorkspaceWithoutForce(t *testing.T) {
	ctx := context.Background()
	manager := &SandboxManager{
		customSensitiveDirs: []string{"/tmp/project"},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	_, err := manager.Create(ctx, CreateOptions{Workspace: "/tmp/project"})
	if err == nil {
		t.Fatal("Create() error = nil, want sensitive workspace rejection")
	}
	if got, want := err.Error(), "Workspace is a sensitive directory. Use --force to override."; got != want {
		t.Fatalf("Create() error = %q, want %q", got, want)
	}
}

func TestSandboxManagerAttachStartsStoppedSandbox(t *testing.T) {
	ctx := context.Background()
	startedIDs := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "sb-project-f630ad93":
						return managedInspect("container-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "exited"), nil
					case "container-id":
						return managedInspect("container-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "exited"), nil
					default:
						t.Fatalf("ContainerInspect() id = %q, want sandbox name or container ID", containerID)
						return containertypes.InspectResponse{}, nil
					}
				},
				startFunc: func(ctx context.Context, containerID string, options containertypes.StartOptions) error {
					startedIDs = append(startedIDs, containerID)
					return nil
				},
			}, nil
		},
	}

	sandbox, err := manager.Attach(ctx, "sb-project-f630ad93", "")
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if !reflect.DeepEqual(startedIDs, []string{"container-id"}) {
		t.Fatalf("Attach() started IDs = %#v, want %#v", startedIDs, []string{"container-id"})
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Attach() sandbox name = %q, want %q", got, want)
	}
}

func TestSandboxManagerStopStopsRunningSandboxResolvedFromWorkspace(t *testing.T) {
	ctx := context.Background()
	stoppedIDs := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "sb-project-f630ad93", "running-id":
						return managedInspect("running-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
					default:
						t.Fatalf("ContainerInspect() id = %q, want generated sandbox name or container ID", containerID)
						return containertypes.InspectResponse{}, nil
					}
				},
				stopFunc: func(ctx context.Context, containerID string, options containertypes.StopOptions) error {
					stoppedIDs = append(stoppedIDs, containerID)
					return nil
				},
			}, nil
		},
	}

	sandbox, err := manager.Stop(ctx, "", "/tmp/project")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !reflect.DeepEqual(stoppedIDs, []string{"running-id"}) {
		t.Fatalf("Stop() stopped IDs = %#v, want %#v", stoppedIDs, []string{"running-id"})
	}
	if got, want := sandbox.Workspace, "/tmp/project"; got != want {
		t.Fatalf("Stop() sandbox workspace = %q, want %q", got, want)
	}
}

func TestSandboxManagerDestroyStopsAndRemovesContainerWhenConfirmed(t *testing.T) {
	ctx := context.Background()
	stoppedIDs := make([]string, 0)
	removedIDs := make([]string, 0)
	confirmedMessages := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "sb-project-f630ad93", "running-id":
						return managedInspect("running-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
					default:
						t.Fatalf("ContainerInspect() id = %q, want generated sandbox name or container ID", containerID)
						return containertypes.InspectResponse{}, nil
					}
				},
				stopFunc: func(ctx context.Context, containerID string, options containertypes.StopOptions) error {
					stoppedIDs = append(stoppedIDs, containerID)
					return nil
				},
				removeFunc: func(ctx context.Context, containerID string, options containertypes.RemoveOptions) error {
					removedIDs = append(removedIDs, containerID)
					return nil
				},
			}, nil
		},
	}

	sandbox, err := manager.Destroy(ctx, DestroyOptions{
		Workspace: "/tmp/project",
		Confirm: func(message string) bool {
			confirmedMessages = append(confirmedMessages, message)
			return true
		},
	})
	if err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	wantMessages := []string{"Are you sure you want to destroy sandbox 'sb-project-f630ad93'?\nThis will stop and remove the container."}
	if !reflect.DeepEqual(confirmedMessages, wantMessages) {
		t.Fatalf("Destroy() confirmation messages = %#v, want %#v", confirmedMessages, wantMessages)
	}
	if !reflect.DeepEqual(stoppedIDs, []string{"running-id"}) {
		t.Fatalf("Destroy() stopped IDs = %#v, want %#v", stoppedIDs, []string{"running-id"})
	}
	if !reflect.DeepEqual(removedIDs, []string{"running-id"}) {
		t.Fatalf("Destroy() removed IDs = %#v, want %#v", removedIDs, []string{"running-id"})
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Destroy() sandbox name = %q, want %q", got, want)
	}
}

func TestSandboxManagerListAndFindSandboxesUseManagedContainerLabels(t *testing.T) {
	ctx := context.Background()
	listCalls := 0
	containers := []containertypes.Summary{
		managedSummary("one-id", "sb-my-app-a1b2c3d4", "/home/user/projects/my-app", 1),
		managedSummary("two-id", "sb-api-server-e5f6a7b8", "/home/user/projects/api-server", 2),
	}

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				listFunc: func(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
					listCalls++
					if !options.All {
						t.Fatal("ContainerList() All = false, want true")
					}
					labels := options.Filters.Get("label")
					if !slices.Contains(labels, managedLabelKey+"="+managedLabelValue) {
						t.Fatalf("ContainerList() label filters = %#v, want managed label", labels)
					}
					return containers, nil
				},
			}, nil
		},
	}

	sandboxes, err := manager.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got, want := len(sandboxes), 2; got != want {
		t.Fatalf("List() returned %d sandboxes, want %d", got, want)
	}
	if got, want := sandboxes[0].Name, "sb-my-app-a1b2c3d4"; got != want {
		t.Fatalf("List() first sandbox = %q, want %q", got, want)
	}
	if sandboxes[0].ContainerID == nil || *sandboxes[0].ContainerID != "one-id" {
		t.Fatalf("List() first container ID = %#v, want %q", sandboxes[0].ContainerID, "one-id")
	}
	if got, want := sandboxes[0].CreatedAt, "1970-01-01T00:00:01Z"; got != want {
		t.Fatalf("List() first created_at = %q, want %q", got, want)
	}

	matches, err := manager.FindSandboxes(ctx, "api")
	if err != nil {
		t.Fatalf("FindSandboxes() error = %v", err)
	}
	if got, want := len(matches), 1; got != want {
		t.Fatalf("FindSandboxes() returned %d matches, want %d", got, want)
	}
	if got, want := matches[0].Name, "sb-api-server-e5f6a7b8"; got != want {
		t.Fatalf("FindSandboxes() first match = %q, want %q", got, want)
	}
	if got, want := listCalls, 2; got != want {
		t.Fatalf("ContainerList() called %d times, want %d", got, want)
	}
}

func TestSandboxManagerGetContainerStatusHandlesRunningAndMissingContainers(t *testing.T) {
	ctx := context.Background()
	inspectErr := errors.New("inspect failed")
	runningSandbox := SandboxInfo{Name: "sb-project-f630ad93", ContainerID: stringPointer("running-id")}
	missingSandbox := SandboxInfo{Name: "sb-missing-0f5f4f0e", ContainerID: stringPointer("missing-id")}
	noIDSandbox := SandboxInfo{Name: "sb-no-id-0f5f4f0e"}
	brokenSandbox := SandboxInfo{Name: "sb-broken-0f5f4f0e", ContainerID: stringPointer("broken-id")}

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "running-id":
						return managedInspect("running-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
					case "missing-id":
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					case "broken-id":
						return containertypes.InspectResponse{}, inspectErr
					default:
						t.Fatalf("ContainerInspect() id = %q, want a test container ID", containerID)
						return containertypes.InspectResponse{}, nil
					}
				},
			}, nil
		},
	}

	status, err := manager.GetContainerStatus(ctx, runningSandbox)
	if err != nil {
		t.Fatalf("GetContainerStatus(running) error = %v", err)
	}
	if got, want := status, "running"; got != want {
		t.Fatalf("GetContainerStatus(running) = %q, want %q", got, want)
	}

	status, err = manager.GetContainerStatus(ctx, missingSandbox)
	if err != nil {
		t.Fatalf("GetContainerStatus(missing) error = %v", err)
	}
	if got, want := status, unknownContainerStatus; got != want {
		t.Fatalf("GetContainerStatus(missing) = %q, want %q", got, want)
	}

	status, err = manager.GetContainerStatus(ctx, noIDSandbox)
	if err != nil {
		t.Fatalf("GetContainerStatus(no-id) error = %v", err)
	}
	if got, want := status, unknownContainerStatus; got != want {
		t.Fatalf("GetContainerStatus(no-id) = %q, want %q", got, want)
	}

	_, err = manager.GetContainerStatus(ctx, brokenSandbox)
	if err == nil {
		t.Fatal("GetContainerStatus(broken) error = nil, want inspect failure")
	}
	if !errors.Is(err, inspectErr) {
		t.Fatalf("GetContainerStatus(broken) error should unwrap inspect failure")
	}
	if !strings.Contains(err.Error(), `inspect container for sandbox "sb-broken-0f5f4f0e"`) {
		t.Fatalf("GetContainerStatus(broken) error = %q, want inspect context", err)
	}
}

func TestSandboxManagerBuildEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		envPassthrough []string
		extraEnvVars   []string
		getenv         func(string) string
		uid, gid       int
		want           []string
	}{
		{
			name: "only UID and GID when no extra vars",
			uid:  1000, gid: 1001,
			want: []string{"HOST_GID=1001", "HOST_UID=1000"},
		},
		{
			name:         "explicit KEY=VALUE vars are included",
			extraEnvVars: []string{"FOO=bar", "BAZ=qux"},
			uid:          1000, gid: 1001,
			want: []string{"BAZ=qux", "FOO=bar", "HOST_GID=1001", "HOST_UID=1000"},
		},
		{
			name:           "passthrough vars resolved from environment",
			envPassthrough: []string{"MY_TOKEN"},
			getenv:         func(key string) string { return map[string]string{"MY_TOKEN": "secret"}[key] },
			uid:            500, gid: 500,
			want: []string{"HOST_GID=500", "HOST_UID=500", "MY_TOKEN=secret"},
		},
		{
			name:           "empty passthrough vars are excluded",
			envPassthrough: []string{"UNSET_VAR"},
			getenv:         func(string) string { return "" },
			uid:            1000, gid: 1000,
			want: []string{"HOST_GID=1000", "HOST_UID=1000"},
		},
		{
			name:           "extra vars override passthrough",
			envPassthrough: []string{"TOKEN"},
			extraEnvVars:   []string{"TOKEN=override"},
			getenv:         func(key string) string { return map[string]string{"TOKEN": "from-env"}[key] },
			uid:            1000, gid: 1000,
			want: []string{"HOST_GID=1000", "HOST_UID=1000", "TOKEN=override"},
		},
		{
			name:           "passthrough and explicit are merged and sorted",
			envPassthrough: []string{"ALPHA"},
			extraEnvVars:   []string{"ZEBRA=last", "MIDDLE=mid"},
			getenv:         func(key string) string { return map[string]string{"ALPHA": "first"}[key] },
			uid:            0, gid: 0,
			want: []string{"ALPHA=first", "HOST_GID=0", "HOST_UID=0", "MIDDLE=mid", "ZEBRA=last"},
		},
		{
			name:         "explicit KEY=VALUE can override HOST_UID",
			extraEnvVars: []string{"HOST_UID=9999"},
			uid:          1000, gid: 1000,
			want: []string{"HOST_GID=1000", "HOST_UID=9999"},
		},
		{
			name:         "empty value in KEY= format is preserved",
			extraEnvVars: []string{"EMPTY="},
			uid:          1000, gid: 1000,
			want: []string{"EMPTY=", "HOST_GID=1000", "HOST_UID=1000"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			getenv := tc.getenv
			if getenv == nil {
				getenv = func(string) string { return "" }
			}

			manager := &SandboxManager{
				envPassthrough: tc.envPassthrough,
				getUIDGID:      func() (int, int) { return tc.uid, tc.gid },
				getenv:         getenv,
			}

			got := manager.buildEnvironment(tc.extraEnvVars)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildEnvironment() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSandboxInfoFromInspect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inspect containertypes.InspectResponse
		want    SandboxInfo
	}{
		{
			name:    "full inspect with all fields",
			inspect: managedInspect("abc123", "sb-project-f630ad93", "/home/user/project", "2026-03-08T10:30:00Z", "running"),
			want: SandboxInfo{
				Name:        "sb-project-f630ad93",
				Workspace:   "/home/user/project",
				CreatedAt:   "2026-03-08T10:30:00Z",
				ContainerID: ptrStr("abc123"),
			},
		},
		{
			name: "falls back to container name when label is empty",
			inspect: containertypes.InspectResponse{
				ContainerJSONBase: &containertypes.ContainerJSONBase{
					ID:      "def456",
					Name:    "/my-container",
					Created: "2026-01-01T00:00:00Z",
				},
				Config: &containertypes.Config{
					Labels: map[string]string{
						managedLabelKey:   managedLabelValue,
						workspaceLabelKey: "/workspace",
					},
				},
			},
			want: SandboxInfo{
				Name:        "my-container",
				Workspace:   "/workspace",
				CreatedAt:   "2026-01-01T00:00:00Z",
				ContainerID: ptrStr("def456"),
			},
		},
		{
			name: "nil config yields empty labels",
			inspect: containertypes.InspectResponse{
				ContainerJSONBase: &containertypes.ContainerJSONBase{
					ID:      "ghi789",
					Name:    "/fallback-name",
					Created: "2026-06-15T12:00:00Z",
				},
			},
			want: SandboxInfo{
				Name:        "fallback-name",
				Workspace:   "",
				CreatedAt:   "2026-06-15T12:00:00Z",
				ContainerID: ptrStr("ghi789"),
			},
		},
		{
			name: "nil ContainerJSONBase yields empty createdAt",
			inspect: containertypes.InspectResponse{
				Config: &containertypes.Config{
					Labels: map[string]string{
						nameLabelKey:      "sb-test",
						workspaceLabelKey: "/tmp/test",
					},
				},
			},
			want: SandboxInfo{
				Name:        "sb-test",
				Workspace:   "/tmp/test",
				CreatedAt:   "",
				ContainerID: nil,
			},
		},
		{
			name:    "empty ID yields nil ContainerID",
			inspect: containertypes.InspectResponse{},
			want: SandboxInfo{
				Name:        "",
				Workspace:   "",
				CreatedAt:   "",
				ContainerID: nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sandboxInfoFromInspect(tc.inspect)
			assertSandboxInfoEqual(t, got, tc.want)
		})
	}
}

func TestSandboxInfoFromSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary containertypes.Summary
		want    SandboxInfo
	}{
		{
			name:    "full summary with labels and created timestamp",
			summary: managedSummary("abc123", "sb-project-f630ad93", "/home/user/project", 1741427400),
			want: SandboxInfo{
				Name:        "sb-project-f630ad93",
				Workspace:   "/home/user/project",
				CreatedAt:   "2025-03-08T09:50:00Z",
				ContainerID: ptrStr("abc123"),
			},
		},
		{
			name: "falls back to first container name when label is empty",
			summary: containertypes.Summary{
				ID:    "def456",
				Names: []string{"/my-container"},
				Labels: map[string]string{
					workspaceLabelKey: "/workspace",
				},
			},
			want: SandboxInfo{
				Name:        "my-container",
				Workspace:   "/workspace",
				CreatedAt:   "",
				ContainerID: ptrStr("def456"),
			},
		},
		{
			name: "zero created timestamp yields empty createdAt",
			summary: containertypes.Summary{
				ID: "ghi789",
				Labels: map[string]string{
					nameLabelKey: "sb-zero",
				},
				Created: 0,
			},
			want: SandboxInfo{
				Name:        "sb-zero",
				Workspace:   "",
				CreatedAt:   "",
				ContainerID: ptrStr("ghi789"),
			},
		},
		{
			name: "nil labels and empty names",
			summary: containertypes.Summary{
				ID:      "jkl012",
				Created: 1704067200,
			},
			want: SandboxInfo{
				Name:        "",
				Workspace:   "",
				CreatedAt:   "2024-01-01T00:00:00Z",
				ContainerID: ptrStr("jkl012"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sandboxInfoFromSummary(tc.summary)
			assertSandboxInfoEqual(t, got, tc.want)
		})
	}
}

func TestInspectLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inspect containertypes.InspectResponse
		want    map[string]string
	}{
		{
			name:    "nil config returns empty map",
			inspect: containertypes.InspectResponse{},
			want:    map[string]string{},
		},
		{
			name:    "nil labels returns empty map",
			inspect: containertypes.InspectResponse{Config: &containertypes.Config{}},
			want:    map[string]string{},
		},
		{
			name: "returns labels from config",
			inspect: containertypes.InspectResponse{
				Config: &containertypes.Config{
					Labels: map[string]string{"key": "value"},
				},
			},
			want: map[string]string{"key": "value"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inspectLabels(tc.inspect)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("inspectLabels() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestIsManagedInspect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inspect containertypes.InspectResponse
		want    bool
	}{
		{
			name:    "managed container",
			inspect: managedInspect("id", "name", "/ws", "", "running"),
			want:    true,
		},
		{
			name: "unmanaged container with wrong label value",
			inspect: containertypes.InspectResponse{
				Config: &containertypes.Config{
					Labels: map[string]string{managedLabelKey: "false"},
				},
			},
			want: false,
		},
		{
			name:    "no config at all",
			inspect: containertypes.InspectResponse{},
			want:    false,
		},
		{
			name: "no managed label",
			inspect: containertypes.InspectResponse{
				Config: &containertypes.Config{
					Labels: map[string]string{"other": "label"},
				},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isManagedInspect(tc.inspect); got != tc.want {
				t.Fatalf("isManagedInspect() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringPointer(t *testing.T) {
	t.Parallel()

	if got := stringPointer(""); got != nil {
		t.Fatalf("stringPointer(\"\") = %v, want nil", got)
	}

	got := stringPointer("hello")
	if got == nil {
		t.Fatal("stringPointer(\"hello\") = nil, want non-nil")
	}
	if *got != "hello" {
		t.Fatalf("*stringPointer(\"hello\") = %q, want %q", *got, "hello")
	}

	// Verify it returns a new pointer (not aliased to the input).
	original := "test"
	ptr := stringPointer(original)
	original = "changed"
	if *ptr != "test" {
		t.Fatalf("stringPointer should copy the value, got %q after mutating original", *ptr)
	}
}

func TestCloneStrings(t *testing.T) {
	t.Parallel()

	original := []string{"a", "b", "c"}
	cloned := cloneStrings(original)

	if !reflect.DeepEqual(cloned, original) {
		t.Fatalf("cloneStrings() = %v, want %v", cloned, original)
	}

	// Mutating the clone should not affect the original.
	cloned[0] = "x"
	if original[0] != "a" {
		t.Fatal("cloneStrings() should produce an independent copy")
	}

	// Nil input yields an empty (non-nil) slice.
	nilClone := cloneStrings(nil)
	if nilClone == nil {
		t.Fatal("cloneStrings(nil) = nil, want non-nil empty slice")
	}
	if len(nilClone) != 0 {
		t.Fatalf("cloneStrings(nil) len = %d, want 0", len(nilClone))
	}
}

func TestNormalizeImageName(t *testing.T) {
	t.Parallel()

	if got := normalizeImageName(""); got != DefaultImageName {
		t.Fatalf("normalizeImageName(\"\") = %q, want %q", got, DefaultImageName)
	}

	if got := normalizeImageName("custom:v1"); got != "custom:v1" {
		t.Fatalf("normalizeImageName(\"custom:v1\") = %q, want %q", got, "custom:v1")
	}
}

func TestResolveWorkspacePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workspace string
		getwd     func() (string, error)
		want      string
		wantErr   string
	}{
		{
			name:      "explicit absolute path is returned as-is",
			workspace: "/home/user/project",
			want:      "/home/user/project",
		},
		{
			name:      "empty workspace falls back to getwd",
			workspace: "",
			getwd:     func() (string, error) { return "/home/user/default-dir", nil },
			want:      "/home/user/default-dir",
		},
		{
			name:      "empty workspace with getwd error",
			workspace: "",
			getwd:     func() (string, error) { return "", errors.New("no cwd") },
			wantErr:   "get current working directory",
		},
		{
			name:      "tilde path is expanded",
			workspace: "~/projects/myapp",
			want:      "/projects/myapp", // expandHomePath resolves ~ to home
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			manager := &SandboxManager{
				getwd: tc.getwd,
			}
			if manager.getwd == nil {
				manager.getwd = func() (string, error) { return "/fallback", nil }
			}

			got, err := manager.resolveWorkspacePath(tc.workspace)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveWorkspacePath() error = nil, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveWorkspacePath() error = %q, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWorkspacePath() error = %v", err)
			}
			// For tilde paths, just check the suffix since home dir varies
			if tc.workspace != "" && strings.HasPrefix(tc.workspace, "~") {
				if !strings.HasSuffix(got, "/projects/myapp") {
					t.Fatalf("resolveWorkspacePath() = %q, want suffix %q", got, "/projects/myapp")
				}
				return
			}
			if got != tc.want {
				t.Fatalf("resolveWorkspacePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckSensitiveDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		path                string
		customSensitiveDirs []string
		wantSensitive       bool
		wantErr             bool
	}{
		{
			name:          "root directory is sensitive",
			path:          "/",
			wantSensitive: true,
		},
		{
			name:          "/etc is sensitive",
			path:          "/etc",
			wantSensitive: true,
		},
		{
			name:          "/var is sensitive",
			path:          "/var",
			wantSensitive: true,
		},
		{
			name:          "/usr is sensitive",
			path:          "/usr",
			wantSensitive: true,
		},
		{
			name:          "/bin is sensitive",
			path:          "/bin",
			wantSensitive: true,
		},
		{
			name:          "/sbin is sensitive",
			path:          "/sbin",
			wantSensitive: true,
		},
		{
			name:          "regular project directory is not sensitive",
			path:          "/tmp/my-project",
			wantSensitive: false,
		},
		{
			name:          "subdirectory of sensitive dir is not sensitive",
			path:          "/etc/nginx",
			wantSensitive: false,
		},
		{
			name:                "custom sensitive dir is detected",
			path:                "/opt/secrets",
			customSensitiveDirs: []string{"/opt/secrets"},
			wantSensitive:       true,
		},
		{
			name:                "non-matching custom sensitive dir",
			path:                "/opt/safe",
			customSensitiveDirs: []string{"/opt/secrets"},
			wantSensitive:       false,
		},
		{
			name:                "multiple custom sensitive dirs",
			path:                "/data/important",
			customSensitiveDirs: []string{"/opt/secrets", "/data/important"},
			wantSensitive:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			manager := &SandboxManager{
				customSensitiveDirs: tc.customSensitiveDirs,
			}

			sensitivePath, isSensitive, err := manager.checkSensitiveDir(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("checkSensitiveDir() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("checkSensitiveDir() error = %v", err)
			}
			if isSensitive != tc.wantSensitive {
				t.Fatalf("checkSensitiveDir() sensitive = %v, want %v", isSensitive, tc.wantSensitive)
			}
			if isSensitive && sensitivePath == "" {
				t.Fatal("checkSensitiveDir() returned sensitive=true but empty path")
			}
			if !isSensitive && sensitivePath != "" {
				t.Fatalf("checkSensitiveDir() returned sensitive=false but path = %q", sensitivePath)
			}
		})
	}
}

func TestCheckSensitiveDirDetectsHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("cannot determine home directory")
	}

	manager := &SandboxManager{}
	sensitivePath, isSensitive, err := manager.checkSensitiveDir(home)
	if err != nil {
		t.Fatalf("checkSensitiveDir(home) error = %v", err)
	}
	if !isSensitive {
		t.Fatal("checkSensitiveDir(home) = false, want true (home directory should be sensitive)")
	}
	if sensitivePath != home {
		t.Fatalf("checkSensitiveDir(home) path = %q, want %q", sensitivePath, home)
	}
}

// --- destroyContainer tests ---

func TestDestroyContainerNilContainerID(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			t.Fatal("getClient should not be called when ContainerID is nil")
			return nil, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: nil,
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil", err)
	}
}

func TestDestroyContainerEmptyContainerID(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			t.Fatal("getClient should not be called when ContainerID is empty")
			return nil, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr(""),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil", err)
	}
}

func TestDestroyContainerAlreadyGone(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil for already-gone container", err)
	}
}

func TestDestroyContainerStoppedThenRemoved(t *testing.T) {
	t.Parallel()
	var removedID string

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "exited",
							},
						},
					}, nil
				},
				stopFunc: func(_ context.Context, _ string, _ containertypes.StopOptions) error {
					t.Fatal("ContainerStop should not be called for non-running container")
					return nil
				},
				removeFunc: func(_ context.Context, id string, _ containertypes.RemoveOptions) error {
					removedID = id
					return nil
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil", err)
	}
	if removedID != "deadbeef" {
		t.Fatalf("removedID = %q, want %q", removedID, "deadbeef")
	}
}

func TestDestroyContainerRunningStoppedThenRemoved(t *testing.T) {
	t.Parallel()
	var stoppedID, removedID string

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "running",
							},
						},
					}, nil
				},
				stopFunc: func(_ context.Context, id string, _ containertypes.StopOptions) error {
					stoppedID = id
					return nil
				},
				removeFunc: func(_ context.Context, id string, _ containertypes.RemoveOptions) error {
					removedID = id
					return nil
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil", err)
	}
	if stoppedID != "deadbeef" {
		t.Fatalf("stoppedID = %q, want %q", stoppedID, "deadbeef")
	}
	if removedID != "deadbeef" {
		t.Fatalf("removedID = %q, want %q", removedID, "deadbeef")
	}
}

func TestDestroyContainerDisappearsAfterStop(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "running",
							},
						},
					}, nil
				},
				stopFunc: func(_ context.Context, _ string, _ containertypes.StopOptions) error {
					return cerrdefs.ErrNotFound
				},
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					t.Fatal("ContainerRemove should not be called when stop returns not-found")
					return nil
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil for container gone during stop", err)
	}
}

func TestDestroyContainerDisappearsAfterRemove(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "exited",
							},
						},
					}, nil
				},
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					return cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil for container gone during remove", err)
	}
}

func TestDestroyContainerInspectError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, errors.New("connection refused")
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err == nil {
		t.Fatal("destroyContainer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "inspect container") {
		t.Fatalf("error = %q, want it to mention 'inspect container'", err.Error())
	}
}

func TestDestroyContainerStopError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "running",
							},
						},
					}, nil
				},
				stopFunc: func(_ context.Context, _ string, _ containertypes.StopOptions) error {
					return errors.New("timeout")
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err == nil {
		t.Fatal("destroyContainer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stop sandbox") {
		t.Fatalf("error = %q, want it to mention 'stop sandbox'", err.Error())
	}
}

func TestDestroyContainerRemoveError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							ID:   id,
							Name: "/sb-test-abc123",
							State: &containertypes.State{
								Status: "exited",
							},
						},
					}, nil
				},
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					return errors.New("permission denied")
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err == nil {
		t.Fatal("destroyContainer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "remove sandbox") {
		t.Fatalf("error = %q, want it to mention 'remove sandbox'", err.Error())
	}
}

func TestDestroyContainerClientError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, errors.New("docker not running")
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: ptrStr("deadbeef"),
	})
	if err == nil {
		t.Fatal("destroyContainer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "docker not running") {
		t.Fatalf("error = %q, want 'docker not running'", err.Error())
	}
}

// --- resolveSandbox tests ---

func TestResolveSandboxByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					if id == "sb-myproject-abc123" {
						return managedInspect("cid1", "sb-myproject-abc123", "/home/user/project", "2026-01-01T00:00:00Z", "running"), nil
					}
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	info, err := manager.resolveSandbox(ctx, "sb-myproject-abc123", "", "")
	if err != nil {
		t.Fatalf("resolveSandbox() error = %v, want nil", err)
	}
	if info.Name != "sb-myproject-abc123" {
		t.Fatalf("Name = %q, want %q", info.Name, "sb-myproject-abc123")
	}
	if info.Workspace != "/home/user/project" {
		t.Fatalf("Workspace = %q, want %q", info.Workspace, "/home/user/project")
	}
}

func TestResolveSandboxByNameNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	_, err := manager.resolveSandbox(ctx, "sb-nonexistent-000000", "", "")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want it to mention 'not found'", err.Error())
	}
}

func TestResolveSandboxByNameGetSandboxError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, errors.New("connection refused")
				},
			}, nil
		},
	}

	_, err := manager.resolveSandbox(ctx, "sb-test-abc123", "", "")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %q, want 'connection refused'", err.Error())
	}
}

func TestResolveSandboxByWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	workspace := t.TempDir()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					// Accept any name lookup — the generated name from workspace
					return managedInspect("cid1", id, workspace, "2026-01-01T00:00:00Z", "running"), nil
				},
			}, nil
		},
		getwd: func() (string, error) {
			return workspace, nil
		},
	}

	info, err := manager.resolveSandbox(ctx, "", workspace, "")
	if err != nil {
		t.Fatalf("resolveSandbox() error = %v, want nil", err)
	}
	if info.Workspace != workspace {
		t.Fatalf("Workspace = %q, want %q", info.Workspace, workspace)
	}
}

func TestResolveSandboxByWorkspaceNotFoundDefaultMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	workspace := t.TempDir()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	_, err := manager.resolveSandbox(ctx, "", workspace, "")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "No sandbox found for workspace") {
		t.Fatalf("error = %q, want default not-found message", err.Error())
	}
	if !strings.Contains(err.Error(), "sb create") {
		t.Fatalf("error = %q, want it to suggest 'sb create'", err.Error())
	}
}

func TestResolveSandboxByWorkspaceNotFoundCustomMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	workspace := t.TempDir()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	_, err := manager.resolveSandbox(ctx, "", workspace, "custom not found message")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want error")
	}
	if err.Error() != "custom not found message" {
		t.Fatalf("error = %q, want %q", err.Error(), "custom not found message")
	}
}

func TestResolveSandboxByWorkspaceResolveError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{}, nil
		},
		getwd: func() (string, error) {
			return "", errors.New("getwd failed")
		},
	}

	// Empty workspace + failing getwd
	_, err := manager.resolveSandbox(ctx, "", "", "")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "getwd failed") {
		t.Fatalf("error = %q, want 'getwd failed'", err.Error())
	}
}

func assertSandboxInfoEqual(t *testing.T, got, want SandboxInfo) {
	t.Helper()
	if got.Name != want.Name {
		t.Fatalf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Workspace != want.Workspace {
		t.Fatalf("Workspace = %q, want %q", got.Workspace, want.Workspace)
	}
	if got.CreatedAt != want.CreatedAt {
		t.Fatalf("CreatedAt = %q, want %q", got.CreatedAt, want.CreatedAt)
	}
	if (got.ContainerID == nil) != (want.ContainerID == nil) {
		t.Fatalf("ContainerID nil = %v, want nil = %v", got.ContainerID == nil, want.ContainerID == nil)
	}
	if got.ContainerID != nil && *got.ContainerID != *want.ContainerID {
		t.Fatalf("ContainerID = %q, want %q", *got.ContainerID, *want.ContainerID)
	}
}

func ptrStr(s string) *string {
	return &s
}

func managedInspect(id string, name string, workspace string, createdAt string, status string) containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			ID:      id,
			Name:    "/" + name,
			Created: createdAt,
			State: &containertypes.State{
				Status: containertypes.ContainerState(status),
			},
		},
		Config: &containertypes.Config{
			Labels: map[string]string{
				managedLabelKey:   managedLabelValue,
				nameLabelKey:      name,
				workspaceLabelKey: workspace,
			},
		},
	}
}

func TestSandboxManagerCreateDoesNotDuplicateMountsWhenConfigAndCLIOverlap(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace", "project")
	mustMkdirAll(t, workspace)

	// Create a shared mount directory used by both config and CLI levels.
	sharedMount := filepath.Join(home, "shared-mount")
	mustMkdirAll(t, sharedMount)

	const sandboxName = "sb-nodups"
	const createdID = "created-id"
	const createdAt = "2026-03-08T10:00:00Z"

	var createdHostConfig *containertypes.HostConfig

	// Simulate the correct separation: config-level mounts go to extraMounts,
	// CLI-level mounts go to CreateOptions.ExtraMounts.
	manager := &SandboxManager{
		extraMounts: []string{sharedMount},
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
						t.Fatalf("ContainerInspect() unexpected id = %q", containerID)
						return containertypes.InspectResponse{}, nil
					}
				},
				createFunc: func(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
					createdHostConfig = hostConfig
					return containertypes.CreateResponse{ID: createdID}, nil
				},
			}, nil
		},
		getUIDGID:          func() (int, int) { return 1000, 1001 },
		getenv:             func(string) string { return "" },
		ensureShellConfigs: func() error { return nil },
	}

	_, err := manager.Create(ctx, CreateOptions{
		Workspace:   workspace,
		Name:        sandboxName,
		ExtraMounts: []string{sharedMount},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if createdHostConfig == nil {
		t.Fatal("Create() did not call ContainerCreate() with a host config")
	}

	// Count how many times sharedMount appears as a mount source.
	count := 0
	for _, m := range createdHostConfig.Mounts {
		if m.Source == sharedMount {
			count++
		}
	}
	if count != 2 {
		// When config and CLI both specify the same path, it appears twice:
		// once from config-level (MountBuilder.extraMounts) and once from
		// CLI-level (CreateOptions.ExtraMounts). This is expected when the
		// same path is explicitly listed at both levels. The critical thing
		// is that it's 2, not 3 (which would indicate the CLI mount was
		// also merged into the config-level mounts).
		t.Fatalf("shared mount appeared %d times in mounts, want 2", count)
	}
}

func managedSummary(id string, name string, workspace string, created int64) containertypes.Summary {
	return containertypes.Summary{
		ID:      id,
		Names:   []string{"/" + name},
		Created: created,
		Labels: map[string]string{
			managedLabelKey:   managedLabelValue,
			nameLabelKey:      name,
			workspaceLabelKey: workspace,
		},
	}
}
