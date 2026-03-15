package sandbox

import (
	"context"
	"errors"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

func TestDockerClientProviderClientLazilyInitializesAndCaches(t *testing.T) {
	t.Parallel()

	expectedClient := &dockerclient.Client{}
	newClientCalls := 0
	pingCalls := 0
	closeCalls := 0

	provider := &DockerClientProvider{
		newClient: func(opts ...dockerclient.Opt) (*dockerclient.Client, error) {
			newClientCalls++
			if len(opts) != 2 {
				t.Fatalf("expected 2 docker client opts, got %d", len(opts))
			}
			return expectedClient, nil
		},
		pingClient: func(ctx context.Context, cli *dockerclient.Client) error {
			pingCalls++
			if cli != expectedClient {
				t.Fatalf("expected ping to receive cached client pointer")
			}
			return nil
		},
		closeClient: func(cli *dockerclient.Client) error {
			closeCalls++
			return nil
		},
		resolveHost: func() string { return "" },
	}

	first, err := provider.Client(context.Background())
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}

	second, err := provider.Client(context.Background())
	if err != nil {
		t.Fatalf("second Client() error = %v", err)
	}

	if first != expectedClient || second != expectedClient {
		t.Fatalf("expected both Client() calls to return the cached client")
	}
	if newClientCalls != 1 {
		t.Fatalf("expected newClient to be called once, got %d", newClientCalls)
	}
	if pingCalls != 1 {
		t.Fatalf("expected pingClient to be called once, got %d", pingCalls)
	}
	if closeCalls != 0 {
		t.Fatalf("expected closeClient to remain unused on success, got %d", closeCalls)
	}
}

func TestDockerClientProviderClientRetriesAfterCreationFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	newClientCalls := 0

	provider := &DockerClientProvider{
		newClient: func(opts ...dockerclient.Opt) (*dockerclient.Client, error) {
			newClientCalls++
			return nil, boom
		},
		pingClient: func(ctx context.Context, cli *dockerclient.Client) error {
			t.Fatal("pingClient should not be called when client creation fails")
			return nil
		},
		closeClient: func(cli *dockerclient.Client) error {
			t.Fatal("closeClient should not be called when client creation fails")
			return nil
		},
		resolveHost: func() string { return "" },
	}

	_, err := provider.Client(context.Background())
	if err == nil {
		t.Fatal("Client() error = nil, want wrapped docker connectivity error")
	}
	if err.Error() != ErrDockerUnavailable.Error() {
		t.Fatalf("Client() error message = %q, want %q", err.Error(), ErrDockerUnavailable.Error())
	}
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("Client() error should match ErrDockerUnavailable")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Client() error should unwrap the client creation failure")
	}

	_, _ = provider.Client(context.Background())
	if newClientCalls != 2 {
		t.Fatalf("expected failed initialization to be retried, got %d attempts", newClientCalls)
	}
}

func TestDockerClientProviderClientClosesAndRetriesAfterPingFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("ping failed")
	newClientCalls := 0
	pingCalls := 0
	closeCalls := 0

	provider := &DockerClientProvider{
		newClient: func(opts ...dockerclient.Opt) (*dockerclient.Client, error) {
			newClientCalls++
			return &dockerclient.Client{}, nil
		},
		pingClient: func(ctx context.Context, cli *dockerclient.Client) error {
			pingCalls++
			return boom
		},
		closeClient: func(cli *dockerclient.Client) error {
			closeCalls++
			return nil
		},
		resolveHost: func() string { return "" },
	}

	_, err := provider.Client(context.Background())
	if err == nil {
		t.Fatal("Client() error = nil, want wrapped ping failure")
	}
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("Client() error should match ErrDockerUnavailable")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Client() error should unwrap the ping failure")
	}
	if closeCalls != 1 {
		t.Fatalf("expected failed ping to close the transient client once, got %d", closeCalls)
	}

	_, _ = provider.Client(context.Background())
	if newClientCalls != 2 {
		t.Fatalf("expected ping failure to trigger a fresh client creation, got %d attempts", newClientCalls)
	}
	if pingCalls != 2 {
		t.Fatalf("expected ping failure to be retried, got %d attempts", pingCalls)
	}
	if closeCalls != 2 {
		t.Fatalf("expected each failed ping to close its transient client, got %d closes", closeCalls)
	}
}

func TestDockerClientProviderClientPassesCustomHostWhenResolved(t *testing.T) {
	t.Parallel()

	expectedClient := &dockerclient.Client{}
	var receivedOptCount int

	provider := &DockerClientProvider{
		newClient: func(opts ...dockerclient.Opt) (*dockerclient.Client, error) {
			receivedOptCount = len(opts)
			return expectedClient, nil
		},
		pingClient: func(ctx context.Context, cli *dockerclient.Client) error {
			return nil
		},
		closeClient: func(cli *dockerclient.Client) error {
			return nil
		},
		resolveHost: func() string { return "unix:///custom/docker.sock" },
	}

	got, err := provider.Client(context.Background())
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}
	if got != expectedClient {
		t.Fatalf("Client() returned unexpected client pointer")
	}
	// When resolveHost returns a non-empty string, we expect 3 opts:
	// FromEnv, WithAPIVersionNegotiation, WithHost.
	if receivedOptCount != 3 {
		t.Fatalf("expected 3 docker client opts (FromEnv + APIVersionNegotiation + WithHost), got %d", receivedOptCount)
	}
}

func TestDockerClientProviderCloseClosesCachedClientAndResetsCache(t *testing.T) {
	t.Parallel()

	cachedClient := &dockerclient.Client{}
	newClientCalls := 0
	closeCalls := 0

	provider := &DockerClientProvider{
		newClient: func(opts ...dockerclient.Opt) (*dockerclient.Client, error) {
			newClientCalls++
			return cachedClient, nil
		},
		pingClient: func(ctx context.Context, cli *dockerclient.Client) error {
			return nil
		},
		closeClient: func(cli *dockerclient.Client) error {
			closeCalls++
			if cli != cachedClient {
				t.Fatalf("expected closeClient to receive the cached client")
			}
			return nil
		},
		resolveHost: func() string { return "" },
	}

	_, err := provider.Client(context.Background())
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("expected Close() to close the cached client once, got %d", closeCalls)
	}

	_, err = provider.Client(context.Background())
	if err != nil {
		t.Fatalf("Client() after Close() error = %v", err)
	}
	if newClientCalls != 2 {
		t.Fatalf("expected Close() to clear the cache and force reinitialization, got %d initializations", newClientCalls)
	}
}
