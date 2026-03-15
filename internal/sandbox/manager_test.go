package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	dockerclient "github.com/docker/docker/client"
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
	mounts := []mount.Mount{{Type: mount.TypeBind, Source: workspace, Target: workspaceMountTarget, ReadOnly: false}}

	var ensuredBundledImage string
	var createdConfig *containertypes.Config
	var createdHostConfig *containertypes.HostConfig
	var createdContainerName string
	warns := make([]string, 0)
	ensureConfigsCalled := 0

	manager := &SandboxManager{
		imageName:      DefaultImageName,
		envPassthrough: []string{"TOKEN", "EMPTY"},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					// getSandbox lookup — not found
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
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
				if !slices.Equal(extraCLIMounts, []string{"~/extra", "~/missing"}) {
					t.Fatalf("Build() extraCLIMounts = %#v, want %#v", extraCLIMounts, []string{"~/extra", "~/missing"})
				}
				return mounts, []string{"~/missing"}, nil
			},
		},
		getUIDGID: func() (int, int) { return 1000, 1001 },
		lookupenv: func(key string) (string, bool) {
			switch key {
			case "TOKEN":
				return "secret", true
			case "EMPTY":
				return "", false
			default:
				return "", false
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

	if got, want := ensuredBundledImage, DefaultImageName; got != want {
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
	if got, want := createdConfig.Image, DefaultImageName; got != want {
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
	if !slices.Equal(createdConfig.Env, wantEnv) {
		t.Fatalf("ContainerCreate() env = %#v, want %#v", createdConfig.Env, wantEnv)
	}
	wantLabels := map[string]string{
		managedLabelKey:   managedLabelValue,
		nameLabelKey:      "sb-project-f630ad93",
		workspaceLabelKey: workspace,
	}
	if !maps.Equal(createdConfig.Labels, wantLabels) {
		t.Fatalf("ContainerCreate() labels = %#v, want %#v", createdConfig.Labels, wantLabels)
	}
	if !reflect.DeepEqual(createdHostConfig.Mounts, mounts) {
		t.Fatalf("ContainerCreate() mounts = %#v, want %#v", createdHostConfig.Mounts, mounts)
	}
	wantWarns := []string{"Mount path does not exist, skipping: ~/missing"}
	if !slices.Equal(warns, wantWarns) {
		t.Fatalf("warns = %#v, want %#v", warns, wantWarns)
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Create() sandbox name = %q, want %q", got, want)
	}
	if got, want := sandbox.Workspace, workspace; got != want {
		t.Fatalf("Create() sandbox workspace = %q, want %q", got, want)
	}
	if sandbox.ContainerID != createdID {
		t.Fatalf("Create() sandbox container ID = %q, want %q", sandbox.ContainerID, createdID)
	}
	if sandbox.Status != "created" {
		t.Fatalf("Create() sandbox status = %q, want %q", sandbox.Status, "created")
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
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
				createFunc: func(_ context.Context, config *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
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
			ensureCustomImageFunc: func(_ context.Context, imageName string) error {
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

func TestSandboxManagerCreatePullsConfigLevelCustomImage(t *testing.T) {
	ctx := context.Background()
	workspace := "/tmp/project"
	configImage := "ghcr.io/example/custom:latest"
	customEnsured := ""
	bundledCalled := false

	manager := &SandboxManager{
		imageName: configImage, // set via config file, not CLI
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
				createFunc: func(_ context.Context, config *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
					if got, want := config.Image, configImage; got != want {
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
			ensureCustomImageFunc: func(_ context.Context, imageName string) error {
				customEnsured = imageName
				return nil
			},
		},
		mountBuilder:       &fakeManagerMountBuilder{buildFunc: func(string, []string) ([]mount.Mount, []string, error) { return nil, nil, nil }},
		ensureShellConfigs: func() error { return nil },
	}

	// No opts.Image — the custom image comes from the manager's config, not CLI.
	if _, err := manager.Create(ctx, CreateOptions{Workspace: workspace}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if bundledCalled {
		t.Fatal("Create() called EnsureImage() for a config-level custom image; expected EnsureCustomImage()")
	}
	if got, want := customEnsured, configImage; got != want {
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
	if got, want := err.Error(), "sandbox 'sb-project-f630ad93' already exists; use --force to recreate"; got != want {
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
	if got, want := err.Error(), "workspace is a sensitive directory; use --force to override"; got != want {
		t.Fatalf("Create() error = %q, want %q", got, want)
	}
}

func TestCreateWorkspaceResolutionError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getwd: func() (string, error) { return "", errors.New("cwd gone") },
	}
	_, err := manager.Create(context.Background(), CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "cwd gone") {
		t.Fatalf("Create() error = %v, want cwd error", err)
	}
}

func TestCreateGetSandboxClientError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, errors.New("docker down")
		},
	}
	_, err := manager.Create(context.Background(), CreateOptions{Workspace: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "docker down") {
		t.Fatalf("Create() error = %v, want docker down", err)
	}
}

func TestCreateExistingConfirmCancels(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, id string) (containertypes.InspectResponse, error) {
					return managedInspect("id1", "sb-project-f630ad93", "/tmp/project", "2026-01-01T00:00:00Z", "running"), nil
				},
			}, nil
		},
	}
	_, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Confirm:   func(string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled by user") {
		t.Fatalf("Create() error = %v, want cancelled", err)
	}
}

func TestCreateForceRecreatesExisting(t *testing.T) {
	t.Parallel()
	var removedID string
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					// getSandbox — existing sandbox
					return managedInspect("old-id", "sb-project-f630ad93", "/tmp/project", "2026-01-01T00:00:00Z", "exited"), nil
				},
				removeFunc: func(_ context.Context, id string, _ containertypes.RemoveOptions) error {
					removedID = id
					return nil
				},
				createFunc: func(_ context.Context, _ *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
					return containertypes.CreateResponse{ID: "new-id"}, nil
				},
			}, nil
		},
		imageManager:       &fakeManagerImageManager{},
		mountBuilder:       &fakeManagerMountBuilder{},
		ensureShellConfigs: func() error { return nil },
	}
	sandbox, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if removedID != "old-id" {
		t.Fatalf("removed container = %q, want %q", removedID, "old-id")
	}
	if sandbox.ContainerID != "new-id" {
		t.Fatalf("sandbox container ID = %q, want %q", sandbox.ContainerID, "new-id")
	}
}

func TestCreateDestroyContainerErrorDuringRecreate(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return managedInspect("old-id", "sb-project-f630ad93", "/tmp/project", "2026-01-01T00:00:00Z", "running"), nil
				},
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					return errors.New("remove failed")
				},
			}, nil
		},
	}
	_, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Force:     true,
	})
	if err == nil || !strings.Contains(err.Error(), "remove failed") {
		t.Fatalf("Create() error = %v, want remove failed", err)
	}
}

func TestCreateSensitiveDirConfirmCancels(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		customSensitiveDirs: []string{"/tmp/project"},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}
	_, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Confirm:   func(string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled by user") {
		t.Fatalf("Create() error = %v, want cancelled", err)
	}
}

func TestCreateSensitiveDirConfirmProceeds(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		customSensitiveDirs: []string{"/tmp/project"},
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
				createFunc: func(_ context.Context, _ *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
					return containertypes.CreateResponse{ID: "new-id"}, nil
				},
			}, nil
		},
		imageManager:       &fakeManagerImageManager{},
		mountBuilder:       &fakeManagerMountBuilder{},
		ensureShellConfigs: func() error { return nil },
	}
	sandbox, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Confirm:   func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if sandbox.Name != "sb-project-f630ad93" {
		t.Fatalf("Name = %q, want sb-project-f630ad93", sandbox.Name)
	}
}

func TestCreateEnsureShellConfigsError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
		ensureShellConfigs: func() error { return errors.New("config write failed") },
	}
	_, err := manager.Create(context.Background(), CreateOptions{Workspace: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("Create() error = %v, want config write failed", err)
	}
}

func TestCreateEnsureImageError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
		imageManager: &fakeManagerImageManager{
			ensureImageFunc: func(context.Context, string) error {
				return errors.New("build failed")
			},
		},
		ensureShellConfigs: func() error { return nil },
	}
	_, err := manager.Create(context.Background(), CreateOptions{Workspace: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("Create() error = %v, want build failed", err)
	}
}

func TestCreateEnsureCustomImageError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
		imageManager: &fakeManagerImageManager{
			ensureCustomImageFunc: func(context.Context, string) error {
				return errors.New("pull failed")
			},
		},
		ensureShellConfigs: func() error { return nil },
	}
	_, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Image:     "custom:latest",
	})
	if err == nil || !strings.Contains(err.Error(), "pull failed") {
		t.Fatalf("Create() error = %v, want pull failed", err)
	}
}

func TestCreateMountBuilderError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
		imageManager: &fakeManagerImageManager{},
		mountBuilder: &fakeManagerMountBuilder{
			buildFunc: func(string, []string) ([]mount.Mount, []string, error) {
				return nil, nil, errors.New("mount build error")
			},
		},
		ensureShellConfigs: func() error { return nil },
	}
	_, err := manager.Create(context.Background(), CreateOptions{Workspace: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "mount build error") {
		t.Fatalf("Create() error = %v, want mount build error", err)
	}
}

func TestCreateContainerCreateError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
				createFunc: func(_ context.Context, _ *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
					return containertypes.CreateResponse{}, errors.New("create failed")
				},
			}, nil
		},
		imageManager:       &fakeManagerImageManager{},
		mountBuilder:       &fakeManagerMountBuilder{},
		ensureShellConfigs: func() error { return nil },
	}
	_, err := manager.Create(context.Background(), CreateOptions{Workspace: "/tmp/project"})
	if err == nil || !strings.Contains(err.Error(), "create failed") {
		t.Fatalf("Create() error = %v, want create failed", err)
	}
}

func TestCreateWithCustomName(t *testing.T) {
	t.Parallel()
	var createdName string
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
				createFunc: func(_ context.Context, _ *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (containertypes.CreateResponse, error) {
					createdName = name
					return containertypes.CreateResponse{ID: "new-id"}, nil
				},
			}, nil
		},
		imageManager:       &fakeManagerImageManager{},
		mountBuilder:       &fakeManagerMountBuilder{},
		ensureShellConfigs: func() error { return nil },
	}
	sandbox, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Name:      "my-sandbox",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if createdName != "my-sandbox" {
		t.Fatalf("container name = %q, want %q", createdName, "my-sandbox")
	}
	if sandbox.Name != "my-sandbox" {
		t.Fatalf("sandbox name = %q, want %q", sandbox.Name, "my-sandbox")
	}
}

func TestCreateExistingConfirmProceeds(t *testing.T) {
	t.Parallel()
	var destroyed bool
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					// getSandbox — existing sandbox
					return managedInspect("old-id", "sb-project-f630ad93", "/tmp/project", "2026-01-01T00:00:00Z", "running"), nil
				},
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					destroyed = true
					return nil
				},
				createFunc: func(_ context.Context, _ *containertypes.Config, _ *containertypes.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (containertypes.CreateResponse, error) {
					return containertypes.CreateResponse{ID: "new-id"}, nil
				},
			}, nil
		},
		imageManager:       &fakeManagerImageManager{},
		mountBuilder:       &fakeManagerMountBuilder{},
		ensureShellConfigs: func() error { return nil },
	}
	sandbox, err := manager.Create(context.Background(), CreateOptions{
		Workspace: "/tmp/project",
		Confirm:   func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !destroyed {
		t.Fatal("expected old container to be destroyed")
	}
	if sandbox.ContainerID != "new-id" {
		t.Fatalf("container ID = %q, want new-id", sandbox.ContainerID)
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
	if !slices.Equal(startedIDs, []string{"container-id"}) {
		t.Fatalf("Attach() started IDs = %#v, want %#v", startedIDs, []string{"container-id"})
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Attach() sandbox name = %q, want %q", got, want)
	}
}

func TestAttachAlreadyRunningSkipsStart(t *testing.T) {
	ctx := context.Background()
	startCalled := false
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, containerID string) (containertypes.InspectResponse, error) {
					return managedInspect("cid-1", "sb-proj-abc12345", "/tmp/proj", "2026-03-08T10:00:00Z", "running"), nil
				},
				startFunc: func(_ context.Context, _ string, _ containertypes.StartOptions) error {
					startCalled = true
					return nil
				},
			}, nil
		},
	}

	sb, err := manager.Attach(ctx, "sb-proj-abc12345", "")
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if startCalled {
		t.Fatal("Attach() should not start an already-running container")
	}
	if sb.Name != "sb-proj-abc12345" {
		t.Fatalf("Attach() name = %q, want %q", sb.Name, "sb-proj-abc12345")
	}
}

func TestAttachNoContainerID(t *testing.T) {
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, containerID string) (containertypes.InspectResponse, error) {
					// Return an inspect response with no ID field set → empty ContainerID.
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							Name:    "/sb-proj-abc12345",
							Created: "2026-03-08T10:00:00Z",
							State:   &containertypes.State{Status: "running"},
						},
						Config: &containertypes.Config{
							Labels: map[string]string{
								managedLabelKey:   managedLabelValue,
								nameLabelKey:      "sb-proj-abc12345",
								workspaceLabelKey: "/tmp/proj",
							},
						},
					}, nil
				},
			}, nil
		},
	}

	_, err := manager.Attach(ctx, "sb-proj-abc12345", "")
	if err == nil {
		t.Fatal("Attach() expected error for sandbox with no container ID")
	}
	if !strings.Contains(err.Error(), "no container ID") {
		t.Fatalf("Attach() error = %q, want mention of 'no container ID'", err)
	}
}

func TestAttachSandboxNotFound(t *testing.T) {
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

	_, err := manager.Attach(ctx, "sb-nonexistent-12345678", "")
	if err == nil {
		t.Fatal("Attach() expected error for nonexistent sandbox")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Attach() error = %q, want mention of 'not found'", err)
	}
}

func TestAttachClientError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("docker unavailable")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, wantErr
		},
	}

	_, err := manager.Attach(ctx, "sb-proj-abc12345", "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Attach() error = %v, want %v", err, wantErr)
	}
}

func TestAttachStartError(t *testing.T) {
	ctx := context.Background()
	startErr := errors.New("cannot start container")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return managedInspect("cid-1", "sb-proj-abc12345", "/tmp/proj", "2026-03-08T10:00:00Z", "exited"), nil
				},
				startFunc: func(_ context.Context, _ string, _ containertypes.StartOptions) error {
					return startErr
				},
			}, nil
		},
	}

	_, err := manager.Attach(ctx, "sb-proj-abc12345", "")
	if err == nil {
		t.Fatal("Attach() expected error when start fails")
	}
	if !strings.Contains(err.Error(), "start sandbox") {
		t.Fatalf("Attach() error = %q, want mention of 'start sandbox'", err)
	}
}

func TestAttachResolvesFromWorkspace(t *testing.T) {
	ctx := context.Background()
	startedIDs := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "sb-project-f630ad93", "cid-1":
						return managedInspect("cid-1", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "exited"), nil
					default:
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					}
				},
				startFunc: func(_ context.Context, containerID string, _ containertypes.StartOptions) error {
					startedIDs = append(startedIDs, containerID)
					return nil
				},
			}, nil
		},
	}

	sb, err := manager.Attach(ctx, "", "/tmp/project")
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if !slices.Equal(startedIDs, []string{"cid-1"}) {
		t.Fatalf("Attach() started IDs = %v, want %v", startedIDs, []string{"cid-1"})
	}
	if sb.Workspace != "/tmp/project" {
		t.Fatalf("Attach() workspace = %q, want %q", sb.Workspace, "/tmp/project")
	}
}

func TestStopAlreadyStoppedSkipsStop(t *testing.T) {
	ctx := context.Background()
	stopCalled := false
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return managedInspect("cid-1", "sb-proj-abc12345", "/tmp/proj", "2026-03-08T10:00:00Z", "exited"), nil
				},
				stopFunc: func(_ context.Context, _ string, _ containertypes.StopOptions) error {
					stopCalled = true
					return nil
				},
			}, nil
		},
	}

	sb, err := manager.Stop(ctx, "sb-proj-abc12345", "")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopCalled {
		t.Fatal("Stop() should not stop an already-stopped container")
	}
	if sb.Name != "sb-proj-abc12345" {
		t.Fatalf("Stop() name = %q, want %q", sb.Name, "sb-proj-abc12345")
	}
}

func TestStopNoContainerID(t *testing.T) {
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{
						ContainerJSONBase: &containertypes.ContainerJSONBase{
							Name:    "/sb-proj-abc12345",
							Created: "2026-03-08T10:00:00Z",
							State:   &containertypes.State{Status: "running"},
						},
						Config: &containertypes.Config{
							Labels: map[string]string{
								managedLabelKey:   managedLabelValue,
								nameLabelKey:      "sb-proj-abc12345",
								workspaceLabelKey: "/tmp/proj",
							},
						},
					}, nil
				},
			}, nil
		},
	}

	_, err := manager.Stop(ctx, "sb-proj-abc12345", "")
	if err == nil {
		t.Fatal("Stop() expected error for sandbox with no container ID")
	}
	if !strings.Contains(err.Error(), "no container ID") {
		t.Fatalf("Stop() error = %q, want mention of 'no container ID'", err)
	}
}

func TestStopSandboxNotFound(t *testing.T) {
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

	_, err := manager.Stop(ctx, "sb-nonexistent-12345678", "")
	if err == nil {
		t.Fatal("Stop() expected error for nonexistent sandbox")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Stop() error = %q, want mention of 'not found'", err)
	}
}

func TestStopClientError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("docker unavailable")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, wantErr
		},
	}

	_, err := manager.Stop(ctx, "sb-proj-abc12345", "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want %v", err, wantErr)
	}
}

func TestStopStopError(t *testing.T) {
	ctx := context.Background()
	stopErr := errors.New("cannot stop container")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return managedInspect("cid-1", "sb-proj-abc12345", "/tmp/proj", "2026-03-08T10:00:00Z", "running"), nil
				},
				stopFunc: func(_ context.Context, _ string, _ containertypes.StopOptions) error {
					return stopErr
				},
			}, nil
		},
	}

	_, err := manager.Stop(ctx, "sb-proj-abc12345", "")
	if err == nil {
		t.Fatal("Stop() expected error when stop fails")
	}
	if !strings.Contains(err.Error(), "stop sandbox") {
		t.Fatalf("Stop() error = %q, want mention of 'stop sandbox'", err)
	}
}

func TestStopResolvesByName(t *testing.T) {
	ctx := context.Background()
	stoppedIDs := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, containerID string) (containertypes.InspectResponse, error) {
					switch containerID {
					case "sb-proj-abc12345", "cid-1":
						return managedInspect("cid-1", "sb-proj-abc12345", "/tmp/proj", "2026-03-08T10:00:00Z", "running"), nil
					default:
						return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
					}
				},
				stopFunc: func(_ context.Context, containerID string, _ containertypes.StopOptions) error {
					stoppedIDs = append(stoppedIDs, containerID)
					return nil
				},
			}, nil
		},
	}

	sb, err := manager.Stop(ctx, "sb-proj-abc12345", "")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !slices.Equal(stoppedIDs, []string{"cid-1"}) {
		t.Fatalf("Stop() stopped IDs = %v, want %v", stoppedIDs, []string{"cid-1"})
	}
	if sb.Name != "sb-proj-abc12345" {
		t.Fatalf("Stop() name = %q, want %q", sb.Name, "sb-proj-abc12345")
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
	if !slices.Equal(stoppedIDs, []string{"running-id"}) {
		t.Fatalf("Stop() stopped IDs = %#v, want %#v", stoppedIDs, []string{"running-id"})
	}
	if got, want := sandbox.Workspace, "/tmp/project"; got != want {
		t.Fatalf("Stop() sandbox workspace = %q, want %q", got, want)
	}
}

func TestSandboxManagerDestroyRemovesContainerWhenConfirmed(t *testing.T) {
	ctx := context.Background()
	removedIDs := make([]string, 0)
	confirmedMessages := make([]string, 0)
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(_ context.Context, _ string) (containertypes.InspectResponse, error) {
					return managedInspect("running-id", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
				},
				removeFunc: func(_ context.Context, containerID string, opts containertypes.RemoveOptions) error {
					if !opts.Force {
						t.Fatal("RemoveOptions.Force = false, want true")
					}
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
	if !slices.Equal(confirmedMessages, wantMessages) {
		t.Fatalf("Destroy() confirmation messages = %#v, want %#v", confirmedMessages, wantMessages)
	}
	if !slices.Equal(removedIDs, []string{"running-id"}) {
		t.Fatalf("Destroy() removed IDs = %#v, want %#v", removedIDs, []string{"running-id"})
	}
	if got, want := sandbox.Name, "sb-project-f630ad93"; got != want {
		t.Fatalf("Destroy() sandbox name = %q, want %q", got, want)
	}
}

func TestDestroyUserCancels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					return managedInspect("cid", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
				},
			}, nil
		},
	}

	_, err := manager.Destroy(ctx, DestroyOptions{
		Name: "sb-project-f630ad93",
		Confirm: func(message string) bool {
			return false // user says no
		},
	})
	if err == nil {
		t.Fatal("Destroy() expected error when user cancels")
	}
	if !strings.Contains(err.Error(), "cancelled by user") {
		t.Fatalf("Destroy() error = %q, want 'cancelled by user'", err)
	}
}

func TestDestroyNoConfirmFuncWithoutForce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					return managedInspect("cid", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "running"), nil
				},
			}, nil
		},
	}

	_, err := manager.Destroy(ctx, DestroyOptions{
		Name: "sb-project-f630ad93",
		// No Confirm func and Force is false
	})
	if err == nil {
		t.Fatal("Destroy() expected error when no confirm func and not forced")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Destroy() error = %q, want '--force' hint", err)
	}
}

func TestDestroyForceSkipsConfirmation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var removed bool
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					return managedInspect("cid", "sb-project-f630ad93", "/tmp/project", "2026-03-08T10:00:00Z", "exited"), nil
				},
				stopFunc: func(ctx context.Context, containerID string, options containertypes.StopOptions) error {
					return nil
				},
				removeFunc: func(ctx context.Context, containerID string, options containertypes.RemoveOptions) error {
					removed = true
					return nil
				},
			}, nil
		},
	}

	sb, err := manager.Destroy(ctx, DestroyOptions{
		Name:  "sb-project-f630ad93",
		Force: true,
	})
	if err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if !removed {
		t.Fatal("Destroy() container was not removed")
	}
	if sb.Name != "sb-project-f630ad93" {
		t.Fatalf("Destroy() name = %q, want %q", sb.Name, "sb-project-f630ad93")
	}
}

func TestDestroySandboxNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
					return containertypes.InspectResponse{}, cerrdefs.ErrNotFound
				},
			}, nil
		},
		getwd: func() (string, error) {
			return "/tmp/project", nil
		},
	}

	_, err := manager.Destroy(ctx, DestroyOptions{})
	if err == nil {
		t.Fatal("Destroy() expected error when sandbox not found")
	}
	if !strings.Contains(err.Error(), "nothing to destroy") {
		t.Fatalf("Destroy() error = %q, want 'nothing to destroy'", err)
	}
}

func TestDestroyClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, errors.New("docker unavailable")
		},
	}

	_, err := manager.Destroy(ctx, DestroyOptions{Name: "sb-test"})
	if err == nil {
		t.Fatal("Destroy() expected error on client failure")
	}
	if !strings.Contains(err.Error(), "docker unavailable") {
		t.Fatalf("Destroy() error = %q, want 'docker unavailable'", err)
	}
}

func TestFindSandboxesListError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				listFunc: func(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
					return nil, errors.New("list failed")
				},
			}, nil
		},
	}

	_, err := manager.FindSandboxes(ctx, "test")
	if err == nil {
		t.Fatal("FindSandboxes() expected error when List fails")
	}
	if !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("FindSandboxes() error = %q, want 'list failed'", err)
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
	if sandboxes[0].ContainerID != "one-id" {
		t.Fatalf("List() first container ID = %q, want %q", sandboxes[0].ContainerID, "one-id")
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
	runningSandbox := SandboxInfo{Name: "sb-project-f630ad93", ContainerID: "running-id"}
	missingSandbox := SandboxInfo{Name: "sb-missing-0f5f4f0e", ContainerID: "missing-id"}
	noIDSandbox := SandboxInfo{Name: "sb-no-id-0f5f4f0e"}
	brokenSandbox := SandboxInfo{Name: "sb-broken-0f5f4f0e", ContainerID: "broken-id"}

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

func TestListGetClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientErr := errors.New("docker unavailable")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, clientErr
		},
	}

	_, err := manager.List(ctx)
	if !errors.Is(err, clientErr) {
		t.Fatalf("List() error = %v, want %v", err, clientErr)
	}
}

func TestGetContainerStatusGetClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientErr := errors.New("docker unavailable")
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return nil, clientErr
		},
	}

	_, err := manager.GetContainerStatus(ctx, SandboxInfo{
		Name:        "sb-test-12345678",
		ContainerID: "some-id",
	})
	if !errors.Is(err, clientErr) {
		t.Fatalf("GetContainerStatus() error = %v, want %v", err, clientErr)
	}
}

func TestGetContainerStatusNilAndEmptyState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name    string
		inspect containertypes.InspectResponse
	}{
		{
			name: "nil State",
			inspect: containertypes.InspectResponse{
				ContainerJSONBase: &containertypes.ContainerJSONBase{
					ID:    "nil-state-id",
					Name:  "/sb-test-12345678",
					State: nil,
				},
			},
		},
		{
			name: "empty Status",
			inspect: containertypes.InspectResponse{
				ContainerJSONBase: &containertypes.ContainerJSONBase{
					ID:   "empty-status-id",
					Name: "/sb-test-12345678",
					State: &containertypes.State{
						Status: "",
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inspect := tc.inspect
			manager := &SandboxManager{
				getClient: func(context.Context) (dockerSandboxClient, error) {
					return &fakeSandboxClient{
						inspectFunc: func(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
							return inspect, nil
						},
					}, nil
				},
			}

			status, err := manager.GetContainerStatus(ctx, SandboxInfo{
				Name:        "sb-test-12345678",
				ContainerID: "some-id",
			})
			if err != nil {
				t.Fatalf("GetContainerStatus() error = %v", err)
			}
			if status != unknownContainerStatus {
				t.Fatalf("GetContainerStatus() = %q, want %q", status, unknownContainerStatus)
			}
		})
	}
}

func TestSandboxManagerBuildEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		envPassthrough []string
		extraEnvVars   []string
		lookupenv      func(string) (string, bool)
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
			lookupenv:      func(key string) (string, bool) { v, ok := map[string]string{"MY_TOKEN": "secret"}[key]; return v, ok },
			uid:            500, gid: 500,
			want: []string{"HOST_GID=500", "HOST_UID=500", "MY_TOKEN=secret"},
		},
		{
			name:           "unset passthrough vars are excluded",
			envPassthrough: []string{"UNSET_VAR"},
			lookupenv:      func(string) (string, bool) { return "", false },
			uid:            1000, gid: 1000,
			want: []string{"HOST_GID=1000", "HOST_UID=1000"},
		},
		{
			name:           "passthrough var set to empty string is preserved",
			envPassthrough: []string{"EMPTY_VAR"},
			lookupenv:      func(string) (string, bool) { return "", true },
			uid:            1000, gid: 1000,
			want: []string{"EMPTY_VAR=", "HOST_GID=1000", "HOST_UID=1000"},
		},
		{
			name:           "extra vars override passthrough",
			envPassthrough: []string{"TOKEN"},
			extraEnvVars:   []string{"TOKEN=override"},
			lookupenv:      func(key string) (string, bool) { v, ok := map[string]string{"TOKEN": "from-env"}[key]; return v, ok },
			uid:            1000, gid: 1000,
			want: []string{"HOST_GID=1000", "HOST_UID=1000", "TOKEN=override"},
		},
		{
			name:           "passthrough and explicit are merged and sorted",
			envPassthrough: []string{"ALPHA"},
			extraEnvVars:   []string{"ZEBRA=last", "MIDDLE=mid"},
			lookupenv:      func(key string) (string, bool) { v, ok := map[string]string{"ALPHA": "first"}[key]; return v, ok },
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

			lookupenv := tc.lookupenv
			if lookupenv == nil {
				lookupenv = func(string) (string, bool) { return "", false }
			}

			manager := &SandboxManager{
				envPassthrough: tc.envPassthrough,
				getUIDGID:      func() (int, int) { return tc.uid, tc.gid },
				lookupenv:      lookupenv,
			}

			got := manager.buildEnvironment(tc.extraEnvVars)
			if !slices.Equal(got, tc.want) {
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
				ContainerID: "abc123",
				Status:      "running",
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
				ContainerID: "def456",
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
				ContainerID: "ghi789",
			},
		},
		{
			name: "nil ContainerJSONBase yields empty ContainerID",
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
				ContainerID: "",
			},
		},
		{
			name:    "empty ID yields empty ContainerID",
			inspect: containertypes.InspectResponse{},
			want: SandboxInfo{
				Name:        "",
				Workspace:   "",
				CreatedAt:   "",
				ContainerID: "",
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
				ContainerID: "abc123",
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
				ContainerID: "def456",
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
				ContainerID: "ghi789",
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
				ContainerID: "jkl012",
			},
		},
		{
			name: "state is captured as status",
			summary: containertypes.Summary{
				ID:    "mno345",
				State: "running",
				Names: []string{"/sb-running"},
				Labels: map[string]string{
					managedLabelKey:   managedLabelValue,
					nameLabelKey:      "sb-running",
					workspaceLabelKey: "/workspace",
				},
			},
			want: SandboxInfo{
				Name:        "sb-running",
				Workspace:   "/workspace",
				ContainerID: "mno345",
				Status:      "running",
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
			if !maps.Equal(got, tc.want) {
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
		ContainerID: "",
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
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					return cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: "deadbeef",
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil for already-gone container", err)
	}
}

func TestDestroyContainerForceRemoved(t *testing.T) {
	t.Parallel()
	var removedID string
	var removedOpts containertypes.RemoveOptions

	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				removeFunc: func(_ context.Context, id string, opts containertypes.RemoveOptions) error {
					removedID = id
					removedOpts = opts
					return nil
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: "deadbeef",
	})
	if err != nil {
		t.Fatalf("destroyContainer() error = %v, want nil", err)
	}
	if removedID != "deadbeef" {
		t.Fatalf("removedID = %q, want %q", removedID, "deadbeef")
	}
	if !removedOpts.Force {
		t.Fatal("RemoveOptions.Force = false, want true")
	}
}

func TestDestroyContainerRemoveError(t *testing.T) {
	t.Parallel()
	manager := &SandboxManager{
		getClient: func(context.Context) (dockerSandboxClient, error) {
			return &fakeSandboxClient{
				removeFunc: func(_ context.Context, _ string, _ containertypes.RemoveOptions) error {
					return errors.New("permission denied")
				},
			}, nil
		},
	}

	err := manager.destroyContainer(context.Background(), SandboxInfo{
		Name:        "sb-test-abc123",
		ContainerID: "deadbeef",
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
		ContainerID: "deadbeef",
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
	if !strings.Contains(err.Error(), "no sandbox found for workspace") {
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

	_, err := manager.resolveSandbox(ctx, "", workspace, "nothing to destroy")
	if err == nil {
		t.Fatal("resolveSandbox() error = nil, want error")
	}
	wantMessage := fmt.Sprintf("no sandbox found for workspace '%s'; nothing to destroy", workspace)
	if err.Error() != wantMessage {
		t.Fatalf("error = %q, want %q", err.Error(), wantMessage)
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
	if got.ContainerID != want.ContainerID {
		t.Fatalf("ContainerID = %q, want %q", got.ContainerID, want.ContainerID)
	}
	if got.Status != want.Status {
		t.Fatalf("Status = %q, want %q", got.Status, want.Status)
	}
}

func managedInspect(id string, name string, workspace string, createdAt string, status string) containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			ID:      id,
			Name:    "/" + name,
			Created: createdAt,
			State: &containertypes.State{
				Status: status,
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
		lookupenv:          func(string) (string, bool) { return "", false },
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
	if count != 1 {
		// When config and CLI both specify the same path, deduplication
		// keeps only the last occurrence (CLI takes precedence over config).
		// Docker rejects containers with duplicate mount targets, so this
		// deduplication prevents a container creation error.
		t.Fatalf("shared mount appeared %d times in mounts, want 1", count)
	}
}

func TestCloseReleasesProvider(t *testing.T) {
	t.Parallel()

	closeCalls := 0
	provider := &DockerClientProvider{
		closeClient: func(cli *dockerclient.Client) error {
			closeCalls++
			return nil
		},
	}
	// Simulate a cached client so Close actually invokes closeClient.
	provider.client = &dockerclient.Client{}

	mgr := NewSandboxManager(SandboxManagerOptions{
		DockerClientProvider: provider,
	})

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("expected closeClient to be called once, got %d", closeCalls)
	}

	// Second Close should be a no-op (client already cleared).
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("expected closeClient to still be 1 after second Close, got %d", closeCalls)
	}
}

func TestCloseNoClient(t *testing.T) {
	t.Parallel()

	mgr := NewSandboxManager(SandboxManagerOptions{})
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() on fresh manager should not error, got %v", err)
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
