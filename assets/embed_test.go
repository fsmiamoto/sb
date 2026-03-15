package assets

import (
	"io/fs"
	"testing"
)

func TestEmbeddedDockerContextContainsExpectedFiles(t *testing.T) {
	t.Parallel()

	expectedFiles := []string{
		"docker/Dockerfile",
		"docker/entrypoint.sh",
		"docker/configs/zshrc",
		"docker/configs/starship.toml",
		"docker/configs/nvim/init.lua",
		"docker/configs/nvim/lua/plugins/init.lua",
	}

	for _, name := range expectedFiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := fs.ReadFile(Files, name)
			if err != nil {
				t.Fatalf("read embedded file %q: %v", name, err)
			}
			if len(data) == 0 {
				t.Fatalf("embedded file %q is empty", name)
			}
		})
	}
}

func TestDockerContextFSIsRootedAtDockerDirectory(t *testing.T) {
	t.Parallel()

	dockerFS := DockerContextFS()
	expectedFiles := []string{
		"Dockerfile",
		"entrypoint.sh",
		"configs/zshrc",
		"configs/starship.toml",
		"configs/nvim/init.lua",
		"configs/nvim/lua/plugins/init.lua",
	}

	for _, name := range expectedFiles {
		if _, err := fs.Stat(dockerFS, name); err != nil {
			t.Fatalf("stat embedded docker context file %q: %v", name, err)
		}
	}
}
