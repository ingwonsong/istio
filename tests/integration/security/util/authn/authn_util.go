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
	"os"
	"os/exec"
	"strconv"
	"strings"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/client"
	"istio.io/istio/pkg/test/echo/common/response"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/cluster"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/istio/ingress"
)

type TestCase struct {
	Name               string
	Config             string
	ExpectResponseCode string
	// Use empty value to express the header with such key must not exist.
	ExpectHeaders    map[string]string
	CallOpts         echo.CallOptions
	DestClusters     cluster.Clusters
	SkipMultiCluster bool
}

func (c *TestCase) String() string {
	return fmt.Sprintf("requests to %s%s expected code %s, headers %v",
		c.CallOpts.Target.Config().Service,
		c.CallOpts.Path,
		c.ExpectResponseCode,
		c.ExpectHeaders)
}

// CheckAuthn checks a request based on ExpectResponseCode.
func (c *TestCase) CheckAuthn(responses client.ParsedResponses, err error) error {
	if len(responses) == 0 {
		return fmt.Errorf("%s: no response", c)
	}
	if responses[0].Code != c.ExpectResponseCode {
		return fmt.Errorf("%s: got response code %s, err %v", c, responses[0].Code, err)
	}
	// Checking if echo backend see header with the given value by finding them in response body
	// (given the current behavior of echo convert all headers into key=value in the response body)
	for k, v := range c.ExpectHeaders {
		matcher := fmt.Sprintf("%s=%s", k, v)
		if len(v) == 0 {
			if strings.Contains(responses[0].Body, matcher) {
				return fmt.Errorf("%s: expect header %s does not exist, got response\n%s", c, k, responses[0].Body)
			}
		} else {
			if !strings.Contains(responses[0].Body, matcher) {
				return fmt.Errorf("%s: expect header %s=%s in body, got response\n%s", c, k, v, responses[0].Body)
			}
		}
	}
	if c.ExpectResponseCode == response.StatusCodeOK && c.DestClusters.IsMulticluster() {
		return responses.CheckReachedClusters(c.DestClusters)
	}
	return nil
}

// CheckIngressOrFail checks a request for the ingress gateway.
func CheckIngressOrFail(ctx framework.TestContext, ingr ingress.Instance, host string, path string,
	headers map[string][]string, token string, expectResponseCode int) {
	if headers == nil {
		headers = map[string][]string{
			"Host": {host},
		}
	} else {
		headers["Host"] = []string{host}
	}
	if os.Getenv("CLUSTER_TYPE") == "bare-metal" || os.Getenv("CLUSTER_TYPE") == "apm" || os.Getenv("CLUSTER_TYPE") == "hybrid-gke-and-bare-metal" {
		// Request will be sent to host in the host header with HTTP proxy
		// Modify the /etc/hosts file on the bootstrap VM to direct the request to ingress gateway
		if len(ingr.Cluster().SSHKey()) > 0 {
			if err := SetupEtcHostsFile(ingr, host); err != nil {
				ctx.Fatal(err)
			}
		}
	}
	opts := echo.CallOptions{
		Port: &echo.Port{
			Protocol: protocol.HTTP,
		},
		Path:      path,
		Headers:   headers,
		Validator: echo.ExpectCode(strconv.Itoa(expectResponseCode)),
	}
	if len(token) != 0 {
		opts.Headers["Authorization"] = []string{
			fmt.Sprintf("Bearer %s", token),
		}
	}
	ingr.CallWithRetryOrFail(ctx, opts)
}

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
