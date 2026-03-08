package sandbox

import (
	"context"
	"errors"
	"sync"

	dockerclient "github.com/docker/docker/client"
)

var (
	// ErrDockerUnavailable reports that sb could not connect to the Docker daemon.
	ErrDockerUnavailable = errors.New("failed to connect to Docker. Is Docker running?")
)

// DockerClientProvider lazily initializes and caches a Docker SDK client.
//
// The client is created from the current environment, mirroring Python's
// docker.from_env(), and verified with a ping before being cached.
type DockerClientProvider struct {
	mu     sync.Mutex
	client *dockerclient.Client

	newClient   func(...dockerclient.Opt) (*dockerclient.Client, error)
	pingClient  func(context.Context, *dockerclient.Client) error
	closeClient func(*dockerclient.Client) error
}

// DockerConnectionError wraps the underlying Docker SDK failure while keeping
// the user-facing error message aligned with the Python implementation.
type DockerConnectionError struct {
	cause error
}

// Error returns the user-facing Docker connectivity failure message.
func (e *DockerConnectionError) Error() string {
	return ErrDockerUnavailable.Error()
}

// Unwrap exposes the underlying Docker SDK error.
func (e *DockerConnectionError) Unwrap() error {
	return e.cause
}

// Is makes errors.Is(err, ErrDockerUnavailable) succeed for wrapped failures.
func (e *DockerConnectionError) Is(target error) bool {
	return target == ErrDockerUnavailable
}

// NewDockerClientProvider returns a Docker client provider that initializes
// clients from the standard Docker environment variables.
func NewDockerClientProvider() *DockerClientProvider {
	return &DockerClientProvider{}
}

// Client returns a cached Docker client, creating it on the first call.
//
// Initialization uses the current environment and negotiates the Docker API
// version automatically. Failed initialization is not cached, so later calls
// can retry after Docker becomes available.
func (p *DockerClientProvider) Client(ctx context.Context) (*dockerclient.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.initDefaults()

	if p.client != nil {
		return p.client, nil
	}

	cli, err := p.newClient(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, &DockerConnectionError{cause: err}
	}

	if err := p.pingClient(ctx, cli); err != nil {
		_ = p.closeClient(cli)
		return nil, &DockerConnectionError{cause: err}
	}

	p.client = cli
	return p.client, nil
}

// Close closes the cached Docker client, if one has been initialized.
func (p *DockerClientProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.initDefaults()

	if p.client == nil {
		return nil
	}

	err := p.closeClient(p.client)
	p.client = nil
	return err
}

func (p *DockerClientProvider) initDefaults() {
	if p.newClient == nil {
		p.newClient = dockerclient.NewClientWithOpts
	}
	if p.pingClient == nil {
		p.pingClient = defaultPingDockerClient
	}
	if p.closeClient == nil {
		p.closeClient = defaultCloseDockerClient
	}
}

func defaultPingDockerClient(ctx context.Context, cli *dockerclient.Client) error {
	_, err := cli.Ping(ctx)
	return err
}

func defaultCloseDockerClient(cli *dockerclient.Client) error {
	return cli.Close()
}
