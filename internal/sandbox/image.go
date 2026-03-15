package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	buildtypes "github.com/docker/docker/api/types/build"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/fsmiamoto/sb/assets"
)

type dockerImageClient interface {
	ImageInspectWithRaw(context.Context, string) (dockerimage.InspectResponse, []byte, error)
	ImageBuild(context.Context, io.Reader, buildtypes.ImageBuildOptions) (buildtypes.ImageBuildResponse, error)
	ImagePull(context.Context, string, dockerimage.PullOptions) (io.ReadCloser, error)
}

// ImageManager ensures Docker images are available for sandbox containers.
type ImageManager struct {
	provider *DockerClientProvider

	getClient           func(context.Context) (dockerImageClient, error)
	dockerContext       fs.FS
	buildContextArchive func(fs.FS) (io.ReadCloser, error)
	consumeDockerStream func(io.ReadCloser) error
}

// NewImageManager returns an image manager backed by the shared Docker client provider.
func NewImageManager(provider *DockerClientProvider) *ImageManager {
	return &ImageManager{provider: provider}
}

// EnsureImage makes sure the bundled sb image exists locally, building it from
// the embedded Docker context when necessary.
func (m *ImageManager) EnsureImage(ctx context.Context, imageName string) error {
	m.initDefaults()
	imageName = normalizeImageName(imageName)

	cli, err := m.getClient(ctx)
	if err != nil {
		return err
	}

	exists, err := imageExistsLocally(ctx, cli, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if _, err := fs.Stat(m.dockerContext, "Dockerfile"); err != nil {
		return fmt.Errorf("embedded Docker build context is missing Dockerfile: %w", err)
	}

	buildContext, err := m.buildContextArchive(m.dockerContext)
	if err != nil {
		return fmt.Errorf("create Docker build context archive for %q: %w", imageName, err)
	}
	defer func() {
		_ = buildContext.Close()
	}()

	response, err := cli.ImageBuild(ctx, buildContext, buildtypes.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{imageName},
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("build Docker image %q: %w", imageName, err)
	}

	if err := m.consumeDockerStream(response.Body); err != nil {
		return fmt.Errorf("build Docker image %q: %w", imageName, err)
	}

	return nil
}

// EnsureCustomImage makes sure a non-bundled image exists locally, pulling it
// from a registry when it is not already present.
func (m *ImageManager) EnsureCustomImage(ctx context.Context, imageName string) error {
	m.initDefaults()
	imageName = normalizeImageName(imageName)

	cli, err := m.getClient(ctx)
	if err != nil {
		return err
	}

	exists, err := imageExistsLocally(ctx, cli, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	stream, err := cli.ImagePull(ctx, imageName, dockerimage.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull Docker image %q: %w", imageName, err)
	}

	if err := m.consumeDockerStream(stream); err != nil {
		return fmt.Errorf("pull Docker image %q: %w", imageName, err)
	}

	return nil
}

func (m *ImageManager) initDefaults() {
	if m.getClient == nil {
		if m.provider == nil {
			m.getClient = func(context.Context) (dockerImageClient, error) {
				return nil, errors.New("docker client provider is not configured")
			}
		} else {
			m.getClient = func(ctx context.Context) (dockerImageClient, error) {
				return m.provider.Client(ctx)
			}
		}
	}
	if m.dockerContext == nil {
		m.dockerContext = assets.DockerContextFS()
	}
	if m.buildContextArchive == nil {
		m.buildContextArchive = createBuildContextArchive
	}
	if m.consumeDockerStream == nil {
		m.consumeDockerStream = consumeDockerStream
	}
}

// imageExistsLocally checks whether the given image is available in the local
// Docker store. Returns true when found, false when not found, or an error for
// other failures (e.g. daemon unreachable).
func imageExistsLocally(ctx context.Context, cli dockerImageClient, imageName string) (bool, error) {
	_, _, err := cli.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect Docker image %q: %w", imageName, err)
}

func normalizeImageName(imageName string) string {
	if imageName == "" {
		return DefaultImageName
	}
	return imageName
}

func createBuildContextArchive(root fs.FS) (io.ReadCloser, error) {
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)

	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = path
		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		file, err := root.Open(path)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}

		return nil
	})
	if err != nil {
		_ = tw.Close()
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(buffer.Bytes())), nil
}

func consumeDockerStream(body io.ReadCloser) error {
	if body == nil {
		return nil
	}
	defer func() {
		_ = body.Close()
	}()

	err := jsonmessage.DisplayJSONMessagesStream(body, io.Discard, 0, false, nil)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
