package sandbox

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	cerrdefs "github.com/containerd/errdefs"
	buildtypes "github.com/docker/docker/api/types/build"
	dockerimage "github.com/docker/docker/api/types/image"
)

type fakeImageClient struct {
	inspectFunc func(context.Context, string) (dockerimage.InspectResponse, []byte, error)
	buildFunc   func(context.Context, io.Reader, buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error)
	pullFunc    func(context.Context, string, dockerimage.PullOptions) (io.ReadCloser, error)
}

func (c *fakeImageClient) ImageInspectWithRaw(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
	if c.inspectFunc == nil {
		return dockerimage.InspectResponse{}, nil, nil
	}
	return c.inspectFunc(ctx, imageName)
}

func (c *fakeImageClient) ImageBuild(ctx context.Context, buildContext io.Reader, options buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error) {
	if c.buildFunc == nil {
		return buildtypes.ImageBuildResponse{}, nil
	}
	return c.buildFunc(ctx, buildContext, options)
}

func (c *fakeImageClient) ImagePull(ctx context.Context, imageName string, options dockerimage.PullOptions) (io.ReadCloser, error) {
	if c.pullFunc == nil {
		return nil, nil
	}
	return c.pullFunc(ctx, imageName, options)
}

func TestImageManagerEnsureImageReturnsWithoutBuildingWhenImageExists(t *testing.T) {
	t.Parallel()

	buildCalled := false
	manager := &ImageManager{
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					if imageName != "existing:latest" {
						t.Fatalf("ImageInspectWithRaw image = %q, want %q", imageName, "existing:latest")
					}
					return dockerimage.InspectResponse{}, nil, nil
				},
				buildFunc: func(ctx context.Context, buildContext io.Reader, options buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error) {
					buildCalled = true
					return buildtypes.ImageBuildResponse{}, nil
				},
			}, nil
		},
	}

	if err := manager.EnsureImage(context.Background(), "existing:latest"); err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if buildCalled {
		t.Fatal("EnsureImage() built an image that already existed")
	}
}

func TestImageManagerEnsureImageBuildsEmbeddedContextWhenMissing(t *testing.T) {
	t.Parallel()

	dockerContext := fstest.MapFS{
		"Dockerfile":             {Data: []byte("FROM scratch\n"), Mode: 0o644},
		"entrypoint.sh":          {Data: []byte("#!/bin/sh\necho hi\n"), Mode: 0o755},
		"configs/zshrc":          {Data: []byte("export TEST=1\n"), Mode: 0o644},
		"configs/nvim/init.lua":  {Data: []byte("print('hi')\n"), Mode: 0o644},
		"configs/nvim/lua/a.lua": {Data: []byte("return {}\n"), Mode: 0o644},
	}

	var extractedDir string
	var removedDir string
	manager := &ImageManager{
		dockerContext: dockerContext,
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					if imageName != "sb-sandbox:test" {
						t.Fatalf("ImageInspectWithRaw image = %q, want %q", imageName, "sb-sandbox:test")
					}
					return dockerimage.InspectResponse{}, nil, cerrdefs.ErrNotFound
				},
				buildFunc: func(ctx context.Context, buildContext io.Reader, options buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error) {
					if options.Dockerfile != "Dockerfile" {
						t.Fatalf("ImageBuild Dockerfile = %q, want %q", options.Dockerfile, "Dockerfile")
					}
					if !reflect.DeepEqual(options.Tags, []string{"sb-sandbox:test"}) {
						t.Fatalf("ImageBuild Tags = %#v, want %#v", options.Tags, []string{"sb-sandbox:test"})
					}
					if !options.Remove {
						t.Fatal("ImageBuild Remove = false, want true")
					}

					entries, files := readTarArchive(t, buildContext)
					for _, name := range []string{
						"Dockerfile",
						"entrypoint.sh",
						"configs/zshrc",
						"configs/nvim/init.lua",
						"configs/nvim/lua/a.lua",
					} {
						if !contains(entries, name) {
							t.Fatalf("build context tar missing %q; entries = %#v", name, entries)
						}
					}
					if got, want := files["Dockerfile"], "FROM scratch\n"; got != want {
						t.Fatalf("Dockerfile contents = %q, want %q", got, want)
					}
					if got, want := files["entrypoint.sh"], "#!/bin/sh\necho hi\n"; got != want {
						t.Fatalf("entrypoint.sh contents = %q, want %q", got, want)
					}

					return buildtypes.ImageBuildResponse{
						Body: jsonStream(
							`{"stream":"Step 1/1 : FROM scratch"}`,
							`{"stream":"Successfully built abc123"}`,
						),
					}, nil
				},
			}, nil
		},
		mkdirTemp: func(dir, pattern string) (string, error) {
			tmpDir, err := os.MkdirTemp(t.TempDir(), pattern)
			if err != nil {
				return "", err
			}
			extractedDir = tmpDir
			return tmpDir, nil
		},
		removeAll: func(path string) error {
			removedDir = path
			return os.RemoveAll(path)
		},
	}

	if err := manager.EnsureImage(context.Background(), "sb-sandbox:test"); err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if extractedDir == "" {
		t.Fatal("EnsureImage() did not create a temporary Docker context directory")
	}
	if removedDir != extractedDir {
		t.Fatalf("EnsureImage() removed %q, want %q", removedDir, extractedDir)
	}
	if _, err := os.Stat(extractedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary Docker context dir still exists or unexpected error: %v", err)
	}
}

func TestImageManagerEnsureImageReturnsInspectErrors(t *testing.T) {
	t.Parallel()

	inspectErr := errors.New("inspect failed")
	buildCalled := false
	manager := &ImageManager{
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					return dockerimage.InspectResponse{}, nil, inspectErr
				},
				buildFunc: func(ctx context.Context, buildContext io.Reader, options buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error) {
					buildCalled = true
					return buildtypes.ImageBuildResponse{}, nil
				},
			}, nil
		},
	}

	err := manager.EnsureImage(context.Background(), "broken:latest")
	if err == nil {
		t.Fatal("EnsureImage() error = nil, want inspect error")
	}
	if !strings.Contains(err.Error(), `inspect Docker image "broken:latest"`) {
		t.Fatalf("EnsureImage() error = %q, want inspect context", err)
	}
	if !errors.Is(err, inspectErr) {
		t.Fatalf("EnsureImage() error should unwrap inspect failure")
	}
	if buildCalled {
		t.Fatal("EnsureImage() attempted a build after inspect failed")
	}
}

func TestImageManagerEnsureImageFailsWhenEmbeddedDockerfileMissing(t *testing.T) {
	t.Parallel()

	manager := &ImageManager{
		dockerContext: fstest.MapFS{
			"entrypoint.sh": {Data: []byte("#!/bin/sh\n"), Mode: 0o644},
		},
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					return dockerimage.InspectResponse{}, nil, cerrdefs.ErrNotFound
				},
			}, nil
		},
	}

	err := manager.EnsureImage(context.Background(), "sb-sandbox:test")
	if err == nil {
		t.Fatal("EnsureImage() error = nil, want missing Dockerfile error")
	}
	if !strings.Contains(err.Error(), "embedded Docker build context is missing Dockerfile") {
		t.Fatalf("EnsureImage() error = %q, want missing Dockerfile context", err)
	}
}

func TestImageManagerEnsureCustomImageReturnsWithoutPullingWhenImageExists(t *testing.T) {
	t.Parallel()

	pullCalled := false
	manager := &ImageManager{
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					if imageName != "alpine:latest" {
						t.Fatalf("ImageInspectWithRaw image = %q, want %q", imageName, "alpine:latest")
					}
					return dockerimage.InspectResponse{}, nil, nil
				},
				pullFunc: func(ctx context.Context, imageName string, options dockerimage.PullOptions) (io.ReadCloser, error) {
					pullCalled = true
					return nil, nil
				},
			}, nil
		},
	}

	if err := manager.EnsureCustomImage(context.Background(), "alpine:latest"); err != nil {
		t.Fatalf("EnsureCustomImage() error = %v", err)
	}
	if pullCalled {
		t.Fatal("EnsureCustomImage() pulled an image that already existed")
	}
}

func TestImageManagerEnsureCustomImagePullsWhenMissing(t *testing.T) {
	t.Parallel()

	pullCalled := false
	manager := &ImageManager{
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					return dockerimage.InspectResponse{}, nil, cerrdefs.ErrNotFound
				},
				pullFunc: func(ctx context.Context, imageName string, options dockerimage.PullOptions) (io.ReadCloser, error) {
					pullCalled = true
					if imageName != "ghcr.io/example/sb:latest" {
						t.Fatalf("ImagePull image = %q, want %q", imageName, "ghcr.io/example/sb:latest")
					}
					return jsonStream(
						`{"status":"Pulling from ghcr.io/example/sb"}`,
						`{"status":"Digest: sha256:abc123"}`,
					), nil
				},
			}, nil
		},
	}

	if err := manager.EnsureCustomImage(context.Background(), "ghcr.io/example/sb:latest"); err != nil {
		t.Fatalf("EnsureCustomImage() error = %v", err)
	}
	if !pullCalled {
		t.Fatal("EnsureCustomImage() did not pull a missing image")
	}
}

func TestImageManagerEnsureCustomImagePropagatesPullStreamErrors(t *testing.T) {
	t.Parallel()

	manager := &ImageManager{
		getClient: func(context.Context) (dockerImageClient, error) {
			return &fakeImageClient{
				inspectFunc: func(ctx context.Context, imageName string) (dockerimage.InspectResponse, []byte, error) {
					return dockerimage.InspectResponse{}, nil, cerrdefs.ErrNotFound
				},
				pullFunc: func(ctx context.Context, imageName string, options dockerimage.PullOptions) (io.ReadCloser, error) {
					return jsonStream(
						`{"errorDetail":{"message":"pull failed"},"error":"pull failed"}`,
					), nil
				},
			}, nil
		},
	}

	err := manager.EnsureCustomImage(context.Background(), "broken:latest")
	if err == nil {
		t.Fatal("EnsureCustomImage() error = nil, want pull stream failure")
	}
	if !strings.Contains(err.Error(), `pull Docker image "broken:latest"`) {
		t.Fatalf("EnsureCustomImage() error = %q, want pull context", err)
	}
}

func TestCreateBuildContextArchiveUsesRelativePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "configs", "nvim"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Dockerfile) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "configs", "nvim", "init.lua"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(init.lua) error = %v", err)
	}

	archive, err := createBuildContextArchive(root)
	if err != nil {
		t.Fatalf("createBuildContextArchive() error = %v", err)
	}
	defer archive.Close()

	entries, files := readTarArchive(t, archive)
	for _, name := range []string{"Dockerfile", "configs/nvim/init.lua"} {
		if !contains(entries, name) {
			t.Fatalf("archive missing %q; entries = %#v", name, entries)
		}
	}
	if contains(entries, root) {
		t.Fatalf("archive unexpectedly included absolute root path %q", root)
	}
	if got, want := files["configs/nvim/init.lua"], "print('hi')\n"; got != want {
		t.Fatalf("configs/nvim/init.lua contents = %q, want %q", got, want)
	}
}

func readTarArchive(t *testing.T, r io.Reader) ([]string, map[string]string) {
	t.Helper()

	tr := tar.NewReader(r)
	entries := []string{}
	files := map[string]string{}

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}

		name := strings.TrimSuffix(header.Name, "/")
		entries = append(entries, name)
		if header.Typeflag != tar.TypeReg {
			continue
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("ReadAll(%q) error = %v", name, err)
		}
		files[name] = string(data)
	}

	return entries, files
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func jsonStream(lines ...string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}
