package assets

import (
	"embed"
	"io/fs"
)

// Files contains all embedded project assets, including the Docker build context.
//
//go:embed docker
var Files embed.FS

// DockerContextFS returns the embedded Docker build context rooted at assets/docker.
func DockerContextFS() fs.FS {
	dockerFS, err := fs.Sub(Files, "docker")
	if err != nil {
		panic(err)
	}
	return dockerFS
}
