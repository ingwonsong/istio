package registry

import (
	"os"
	"os/exec"
	"time"
)

type RegistryServer struct {
	cmd       *exec.Cmd
	serverURL string
}

// Call this from inside the entrypoint to standup the local registry server.
// Requires files in particular places, see Archive()
// Usually you'll do a defer ret.Shutdown() after.
// TODO: consider outputing to stdout only if there is an error with the registry. Otherwise it's a lot of log.
func StartRegistry(pathToRegistryAndConfigyml string) (*RegistryServer, error) {
	ret := &RegistryServer{
		cmd:       exec.Command(pathToRegistryAndConfigyml+"/registry", "serve", pathToRegistryAndConfigyml+"/config.yml"),
		serverURL: localRegistryURL,
	}
	ret.cmd.Stdout = os.Stdout
	ret.cmd.Stderr = os.Stderr
	err := ret.cmd.Start()
	if err != nil {
		return nil, err
	}
	time.Sleep(5 * time.Second)

	return ret, err
}

// Shutdown kills the running registry server
func (o *RegistryServer) Shutdown() error {
	return o.cmd.Process.Kill()
}
