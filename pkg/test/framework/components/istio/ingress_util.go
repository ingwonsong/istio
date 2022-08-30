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

package istio

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"istio.io/istio/pkg/test/framework/components/istio/ingress"
)

var empty struct{}

// Storing already added hosts to reduce the no. of ssh commands.
var hostsAdded = sync.Map{}

// The ingress address for some platforms is private and connected via proxy. Ingress tests uses fake host headers but the proxy used
// in the middle treated those hosts as real targeted hosts and tries to resolve which results in 404.
// This setup modified /etc/hosts file on the host and would force the resolution to be the private ingress address.
func SetupIngressViaProxy(ingr ingress.Instance, host string) error {
	if (os.Getenv("CLUSTER_TYPE") == "bare-metal" || os.Getenv("CLUSTER_TYPE") == "hybrid-gke-and-bare-metal" ||
		os.Getenv("CLUSTER_TYPE") == "azure" || os.Getenv("CLUSTER_TYPE") == "openshift") &&
		len(host) > 0 && len(ingr.Cluster().SSHUser()) > 0 {
		if _, ok := hostsAdded.Load(host + ingr.Cluster().Name()); !ok {
			return SetupEtcHostsFile(ingr, host)
		}
	}
	return nil
}

func SetupEtcHostsFile(ingr ingress.Instance, host string) error {
	addr, _ := ingr.HTTPAddress()
	hostEntry := addr + " " + host
	cmd := exec.Command("ssh", "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
		"-i", ingr.Cluster().SSHKey(), ingr.Cluster().SSHUser(),
		"sudo grep", "-qxF", host, "/etc/hosts", "|| echo \""+hostEntry+"\"  | sudo tee -a /etc/hosts")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command %s failed: %q %v", cmd.String(), string(out), err)
	}
	hostsAdded.Store(host+ingr.Cluster().Name(), empty)
	return nil
}
