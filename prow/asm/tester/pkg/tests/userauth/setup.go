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

package userauth

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// Setup runs the test setups for userauth tests.
func Setup(settings *resource.Settings) error {
	if err := installASMUserAuth(settings); err != nil {
		return fmt.Errorf("error installing user auth: %w", err)
	}
	if err := downloadUserAuthDependencies(settings); err != nil {
		return fmt.Errorf("error installing user auth dependencies: %w", err)
	}
	return nil
}

func installASMUserAuth(settings *resource.Settings) error {
	label := "istio-injection=enabled"
	if settings.RevisionConfig != "" {
		label = "istio-injection- istio.io/rev=asm-master"
	}

	// Config install pkg
	var res map[string]interface{}
	// TODO(b/182940034): use ASM owned account once created
	odicKeys, err := ioutil.ReadFile(fmt.Sprintf("%s/user-auth/userauth_oidc.json", settings.ConfigDir))
	if err != nil {
		return fmt.Errorf("error reading the odic key file: %w", err)
	}
	err = json.Unmarshal(odicKeys, &res)
	if err != nil {
		return fmt.Errorf("error reading the odic key file: %w", err)
	}
	oidcClientID := res["client_id"].(string)
	oidcClientSecret := res["client_secret"].(string)
	oidcIssueURI := res["issuer"].(string)
	// TODO(b/182918059): Fetch image from GCR release repo and GitHub packet.
	userAuthImage := "gcr.io/gke-release-staging/ais:staging"
	cmds := []string{
		fmt.Sprintf("kpt pkg get https://github.com/GoogleCloudPlatform/asm-user-auth.git@main %s/user-auth", settings.ConfigDir),
		fmt.Sprintf("kpt cfg set %s/user-auth/asm-user-auth/pkg anthos.servicemesh.user-auth.image %s", settings.ConfigDir, userAuthImage),
		fmt.Sprintf("kpt cfg set %s/user-auth/asm-user-auth/pkg anthos.servicemesh.user-auth.oidc.clientID %s", settings.ConfigDir, oidcClientID),
		fmt.Sprintf("kpt cfg set %s/user-auth/asm-user-auth/pkg anthos.servicemesh.user-auth.oidc.clientSecret %s", settings.ConfigDir, oidcClientSecret),
		fmt.Sprintf("kpt cfg set %s/user-auth/asm-user-auth/pkg anthos.servicemesh.user-auth.oidc.issuerURI %s", settings.ConfigDir, oidcIssueURI),
		fmt.Sprintf("kpt cfg set %s/user-auth/asm-user-auth/pkg anthos.servicemesh.user-auth.oidc.redirectURIPath %s", settings.ConfigDir, "/_gcp_anthos_callback"),
	}
	if err := exec.RunMultiple(cmds); err != nil {
		return err
	}

	for _, context := range settings.KubeContexts {
		cmds := []string{
			fmt.Sprintf("kubectl create namespace asm-user-auth --context %s", context),
			fmt.Sprintf("kubectl label namespace asm-user-auth %s --overwrite --context %s", label, context),

			fmt.Sprintf("kubectl create namespace userauth-test --context %s", context),
			fmt.Sprintf("kubectl label namespace userauth-test %s --overwrite --context %s", label, context),

			// TODO(b/182914654): deploy app in go code
			fmt.Sprintf("kubectl -n userauth-test apply -f https://raw.githubusercontent.com/istio/istio/master/samples/httpbin/httpbin.yaml --context %s", context),

			// Create the kubernetes secret for the encryption and signing key.
			fmt.Sprintf(`kubectl create secret generic secret-key  \
			--from-file="session_cookie.key"="%s/user-auth/asm-user-auth/samples/cookie_encryption_key.json"  \
			--from-file="rctoken.key"="%s/user-auth/asm-user-auth/samples/rctoken_signing_key.json"  \
			--namespace=asm-user-auth --context %s`, settings.ConfigDir, settings.ConfigDir, context),

			fmt.Sprintf("kubectl apply -f %s/user-auth/asm-user-auth/pkg/asm_user_auth_config_v1beta1.yaml --context %s", settings.ConfigDir, context),
			fmt.Sprintf("kubectl apply -R -f %s/user-auth/asm-user-auth/pkg/ --context %s", settings.ConfigDir, context),
			fmt.Sprintf("kubectl apply -f %s/user-auth/httpbin-route.yaml --context %s", settings.ConfigDir, context),
		}
		if err := exec.RunMultiple(cmds); err != nil {
			return err
		}

		if settings.ClusterTopology == resource.MultiCluster {
			if err := exec.Run(fmt.Sprintf("kubectl apply -f %s/user-auth/multicluster-failover.yaml --context %s", settings.ConfigDir, context)); err != nil {
				return err
			}
		}

		cmds = []string{
			fmt.Sprintf("kubectl wait --for=condition=Ready --timeout=10m --namespace=userauth-test --all pod --context %s", context),
			fmt.Sprintf("kubectl wait --for=condition=Ready --timeout=10m --namespace=asm-user-auth --all pod --context %s", context),
		}
		if err := exec.RunMultiple(cmds); err != nil {
			return err
		}
	}

	return nil
}

func downloadUserAuthDependencies(settings *resource.Settings) error {
	// need this mkdir for installing jre: https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=863199
	if err := os.MkdirAll("/usr/share/man/man1", 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(settings.ConfigDir, "user-auth/dependencies"), 0755); err != nil {
		return err
	}

	latestChangeURL := "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/Linux_x64%2FLAST_CHANGE?alt=media"
	revision, err := exec.RunWithOutput("curl -s -S " + latestChangeURL)
	if err != nil {
		return err
	}
	chromiumURL := "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/Linux_x64%2F" + revision + "%2Fchrome-linux.zip?alt=media"
	driverURL := "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/Linux_x64%2F" + revision + "%2Fchromedriver_linux64.zip?alt=media"
	seleniumURL := "https://selenium-release.storage.googleapis.com/3.141/selenium-server-standalone-3.141.59.jar"

	cmds := []string{
		// TODO(b/182939536): add apt-get to https://github.com/istio/tools/blob/master/docker/build-tools/Dockerfile
		fmt.Sprintf("bash -c 'echo %s >> %s'", "deb http://dl.google.com/linux/chrome/deb/ stable main", "/etc/apt/sources.list.d/google.list"),
		"bash -c 'wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add -'",
		"bash -c 'apt-get update && apt-get install -y --no-install-recommends unzip openjdk-11-jre xvfb google-chrome-stable'",

		fmt.Sprintf("bash -c 'curl -# %s > %s/user-auth/dependencies/chrome-linux.zip'", chromiumURL, settings.ConfigDir),
		fmt.Sprintf("bash -c 'curl -# %s > %s/user-auth/dependencies/chromedriver-linux.zip'", driverURL, settings.ConfigDir),
		fmt.Sprintf("unzip %s/user-auth/dependencies/chrome-linux.zip -d %s/user-auth/dependencies", settings.ConfigDir, settings.ConfigDir),
		fmt.Sprintf("unzip %s/user-auth/dependencies/chromedriver-linux.zip -d %s/user-auth/dependencies", settings.ConfigDir, settings.ConfigDir),
		fmt.Sprintf("bash -c 'curl -# %s > %s/user-auth/dependencies/selenium-server.jar'", seleniumURL, settings.ConfigDir),
	}
	if err = exec.RunMultiple(cmds); err != nil {
		return err
	}

	// need it for DevToolsActivePorts error, https://yaqs.corp.google.com/eng/q/5322136407900160
	return os.Setenv("DISPLAY", ":20")
}
