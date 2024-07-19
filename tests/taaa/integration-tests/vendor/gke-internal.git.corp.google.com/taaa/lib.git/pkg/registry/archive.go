package registry

import (
	"fmt"
	"os"
	"os/exec"
)

const (
	// For this to change, code must be written to modify the config.yml associated with the registry
	localRegistryURL = "localhost:5000"
)

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func system(cmd string) error {
	return run("bash", "-c", cmd)
}

func cpFromImage(image, srcPath, destPath string) error {
	return system(fmt.Sprintf("docker cp $(docker create --rm %s):%s %s", image, srcPath, destPath))
}

// Archive a registry so the artifact entrypoint can restart it
// Three files should be in the returned directory:
// 1. config.yml
// 2. registry
// 3. varlibregistry (a directory)
//
// The artifact Dockerfile should
// 1. Copy the contents of varlibregistry to /var/lib/registry of the image
// 2. Copy the other two files somewhere (this should be standardized)
//
// The entrypoint should run
//  /path/to/registry serve /path/to/config.yml
// (StartRegistry helps with this)
// Then the registry is at localhost:5000
func Archive(archiveDir string) (ret string, err error) {
	d, err := os.MkdirTemp("/tmp", "taaa-archive-")
	if err != nil {
		return
	}
	regDir := d + "/varlibregistry"
	err = os.Mkdir(regDir, os.ModePerm)
	err = run("rsync", "-r", archiveDir+"/", regDir)
	if err != nil {
		return
	}
	// Get what we need to run registry server
	// See https://github.com/distribution/distribution/blob/main/Dockerfile#L26
	err = cpFromImage(registryImage, "/etc/docker/registry/config.yml", d)
	if err != nil {
		return
	}
	err = cpFromImage(registryImage, "/bin/registry", d)
	if err != nil {
		return
	}
	ret = d
	return
}
