package registry

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"time"

	"gke-internal.git.corp.google.com/taaa/lib.git/internal/utilities"
)

// DockerRegistryServer contains information needed to use a running registry server.
type DockerRegistryServer struct {
	URL        string
	ServerName string
}

var (
	randStr = utilities.RandStr
)

func runDocker(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Create starts a registry running on the local host with docker.
//
// archiveDir must be a directory the current user can read/write to.
// It should be empty. It's intended to be saved into the Test Artifact,
//  and when the Test is executed a registry server is spun up locally,
//  then all images are copied to somewhere accessible to the SUT.
//
// This requires sudoless docker to be available.
func Create(archiveDir string) (*DockerRegistryServer, error) {
	user, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("unable to determine current user: %v", err)
	}
	// Ensure registry image is available
	err = runDocker("pull", registryImage)
	if err != nil {
		return nil, fmt.Errorf("couldn't pull registry image: %v", err)
	}
	for i := 0; i < 10; i++ {
		port := utilities.R.Intn(5000) + 5000
		ret := DockerRegistryServer{
			ServerName: fmt.Sprintf("taaa-registry-p%d-%s", port, randStr(4)),
			URL:        fmt.Sprintf("localhost:%d", port),
		}
		err := runDocker("run", "-d", "--rm", "-p", fmt.Sprintf("%d:5000", port), "-v", fmt.Sprintf("%s:/var/lib/registry", archiveDir), "--name", ret.ServerName, "--user", fmt.Sprintf("%s:%s", user.Uid, user.Gid), registryImage)
		if err != nil {
			log.Printf("Iteration %d: Error starting image registry server: %v", i, err)
		} else {
			// Is there some way to verify the server is up?
			time.Sleep(time.Second)
			return &ret, nil
		}
	}
	return nil, errors.New("Unable to start registry server, see log")
}

func (o *DockerRegistryServer) Shutdown() error {
	return runDocker("stop", o.ServerName)
}
