package matching

import (
	"slices"
	"testing"
)

type testSandbox struct {
	name        string
	workspace   string
	createdAt   string
	containerID *string
}

func (s testSandbox) GetName() string {
	return s.name
}

func sampleSandboxes() []testSandbox {
	return []testSandbox{
		{
			name:        "sb-my-app-a1b2c3d4",
			workspace:   "/home/user/projects/my-app",
			createdAt:   "2025-01-01T00:00:00Z",
			containerID: stringPtr("abc123"),
		},
		{
			name:        "sb-web-frontend-e5f6a7b8",
			workspace:   "/home/user/projects/web-frontend",
			createdAt:   "2025-01-02T00:00:00Z",
			containerID: stringPtr("def456"),
		},
		{
			name:        "sb-api-server-c9d0e1f2",
			workspace:   "/home/user/projects/api-server",
			createdAt:   "2025-01-03T00:00:00Z",
			containerID: stringPtr("ghi789"),
		},
	}
}

func stringPtr(value string) *string {
	return &value
}

func sandboxNames(sandboxes []testSandbox) []string {
	names := make([]string, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		names = append(names, sandbox.name)
	}

	return names
}

func TestExtractDirname(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "standard name", input: "sb-my-app-a1b2c3d4", expected: "my-app"},
		{name: "single word dirname", input: "sb-project-abcd1234", expected: "project"},
		{name: "multi hyphen dirname", input: "sb-my-cool-app-a1b2c3d4", expected: "my-cool-app"},
		{name: "no match returns full name", input: "not-a-sandbox-name", expected: "not-a-sandbox-name"},
		{name: "wrong prefix", input: "xx-app-a1b2c3d4", expected: "xx-app-a1b2c3d4"},
		{name: "short hash", input: "sb-app-abc", expected: "sb-app-abc"},
		{name: "non hex hash", input: "sb-app-zzzzzzzz", expected: "sb-app-zzzzzzzz"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extractDirname(tt.input)
			if got != tt.expected {
				t.Fatalf("extractDirname(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestScoreMatch(t *testing.T) {
	t.Parallel()

	sandbox := testSandbox{name: "sb-my-app-a1b2c3d4"}
	tests := []struct {
		name      string
		query     string
		expected  int
		shouldHit bool
	}{
		{name: "exact match", query: "sb-my-app-a1b2c3d4", expected: 0, shouldHit: true},
		{name: "exact match case insensitive", query: "SB-MY-APP-A1B2C3D4", expected: 0, shouldHit: true},
		{name: "prefix match", query: "sb-my", expected: 10, shouldHit: true},
		{name: "dirname exact match", query: "my-app", expected: 20, shouldHit: true},
		{name: "dirname prefix match", query: "my-a", expected: 30, shouldHit: true},
		{name: "dirname contains", query: "app", expected: 40, shouldHit: true},
		{name: "substring match", query: "a1b2", expected: 50, shouldHit: true},
		{name: "no match", query: "zzz", shouldHit: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ScoreMatch(tt.query, sandbox)
			if ok != tt.shouldHit {
				t.Fatalf("ScoreMatch(%q, %q) hit = %t, want %t", tt.query, sandbox.name, ok, tt.shouldHit)
			}
			if !tt.shouldHit {
				return
			}
			if got != tt.expected {
				t.Fatalf("ScoreMatch(%q, %q) = %d, want %d", tt.query, sandbox.name, got, tt.expected)
			}
		})
	}
}

func TestFindMatchingSandboxes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		query    string
		expected []string
	}{
		{name: "exact match returns single", query: "sb-my-app-a1b2c3d4", expected: []string{"sb-my-app-a1b2c3d4"}},
		{name: "dirname match", query: "my-app", expected: []string{"sb-my-app-a1b2c3d4"}},
		{name: "partial match sorted by quality", query: "app", expected: []string{"sb-my-app-a1b2c3d4"}},
		{name: "prefix match multiple", query: "sb-", expected: []string{"sb-my-app-a1b2c3d4", "sb-web-frontend-e5f6a7b8", "sb-api-server-c9d0e1f2"}},
		{name: "no match empty", query: "zzz-nonexistent", expected: []string{}},
		{name: "results sorted by score", query: "web", expected: []string{"sb-web-frontend-e5f6a7b8"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results := FindMatchingSandboxes(tt.query, sampleSandboxes())
			if got := sandboxNames(results); !slices.Equal(got, tt.expected) {
				t.Fatalf("FindMatchingSandboxes(%q) = %v, want %v", tt.query, got, tt.expected)
			}
		})
	}
}

func TestFindMatchingSandboxesEmptyList(t *testing.T) {
	t.Parallel()

	results := FindMatchingSandboxes("anything", []testSandbox{})
	if len(results) != 0 {
		t.Fatalf("FindMatchingSandboxes on empty list = %v, want []", sandboxNames(results))
	}
}
