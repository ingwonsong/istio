//go:build integ
// +build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package authn

import (
	"fmt"
	"os/exec"
	"strings"

	"istio.io/istio/pkg/test/framework/components/istio/ingress"
)

func SetupEtcHostsFile(ingr ingress.Instance, host string) error {
	cmd := exec.Command("ssh", "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
		"-i", ingr.Cluster().SSHKey(), ingr.Cluster().SSHUser(),
		"grep", host, "/etc/hosts")
	out, _ := cmd.Output()
	addr, _ := ingr.HTTPAddress()
	hostEntry := addr + " " + host
	if !strings.Contains(string(out), hostEntry) {
		cmd = exec.Command("ssh", "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
			"-i", ingr.Cluster().SSHKey(), ingr.Cluster().SSHUser(),
			"sudo sed", "-i", "'/"+host+"/d'", "/etc/hosts")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command %s failed: %q %v", cmd.String(), string(out), err)
		}
		cmd := exec.Command("ssh", "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
			"-i", ingr.Cluster().SSHKey(), ingr.Cluster().SSHUser(),
			"echo", "\""+hostEntry+"\"", " | sudo tee -a /etc/hosts")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("command %s failed: %q %v", cmd.String(), string(out), err)
		}
	}
	return nil
}
