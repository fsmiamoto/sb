package naming

import "testing"

func TestSanitizeDirname(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "lowercase", input: "MyApp", expected: "myapp"},
		{name: "spaces to hyphens", input: "my app", expected: "my-app"},
		{name: "special chars removed", input: "my@app!v2", expected: "myappv2"},
		{name: "keeps hyphens and underscores", input: "my-app_v2", expected: "my-app_v2"},
		{name: "collapses multiple hyphens", input: "my---app", expected: "my-app"},
		{name: "strips leading trailing hyphens", input: "-my-app-", expected: "my-app"},
		{name: "empty returns sandbox", input: "", expected: "sandbox"},
		{name: "all special chars returns sandbox", input: "@#$%", expected: "sandbox"},
		{name: "mixed spaces and special", input: "My App @ v2.0", expected: "my-app-v20"},
		{name: "numeric only", input: "123", expected: "123"},
		{name: "already clean", input: "myapp", expected: "myapp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SanitizeDirname(tt.input)
			if got != tt.expected {
				t.Fatalf("SanitizeDirname(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGenerateNameDeterministic(t *testing.T) {
	t.Parallel()

	const path = "/home/user/projects/my-app"

	name1, err := GenerateName(path)
	if err != nil {
		t.Fatalf("GenerateName(%q) returned error: %v", path, err)
	}

	name2, err := GenerateName(path)
	if err != nil {
		t.Fatalf("GenerateName(%q) returned error on second call: %v", path, err)
	}

	if name1 != name2 {
		t.Fatalf("GenerateName(%q) should be deterministic: %q != %q", path, name1, name2)
	}
}

func TestGenerateNameDifferentPathsDifferentHashes(t *testing.T) {
	t.Parallel()

	name1, err := GenerateName("/home/user/projects/my-app")
	if err != nil {
		t.Fatalf("GenerateName for first path returned error: %v", err)
	}

	name2, err := GenerateName("/home/user/projects/other-app")
	if err != nil {
		t.Fatalf("GenerateName for second path returned error: %v", err)
	}

	if name1 == name2 {
		t.Fatalf("expected different names for different paths, got %q and %q", name1, name2)
	}
}

func TestGenerateNameExpectedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "format", path: "/home/user/projects/my-app", expected: "sb-my-app-b9703f12"},
		{name: "same dirname different parent one", path: "/home/user/projects/app", expected: "sb-app-1cde9f97"},
		{name: "same dirname different parent two", path: "/home/user/work/app", expected: "sb-app-20ad4b8f"},
		{name: "uses basename", path: "/deeply/nested/path/to/cool-project", expected: "sb-cool-project-50265831"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := GenerateName(tt.path)
			if err != nil {
				t.Fatalf("GenerateName(%q) returned error: %v", tt.path, err)
			}

			if got != tt.expected {
				t.Fatalf("GenerateName(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}
