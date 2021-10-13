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

package util

import (
	"fmt"
	"os"
	"os/exec"

	"istio.io/istio/pkg/test/scopes"
)

const (
	ServiceAccountCreadentialEnv = "E2E_GOOGLE_APPLICATION_CREDENTIALS"
	cloudSDKCIRepo               = "https://storage.googleapis.com/cloud-sdk-testing/ci/staging/components-2.json"
)

// UpdateCloudSDKToPiperHead updates gcloud SDK to piper head.
// CloudSDK provides a continuous build of what is currently HEAD in piper. It is updated every hour.
// go/cloud-sdk#continuous-builds
func UpdateCloudSDKToPiperHead() error {
	return updateCloudSDK(cloudSDKCIRepo)
}

// Update CloudSDK to use a trusted test repo. Should be the GCS path to the
// repository's json file. e.g. <BUCKET_URL>/components-2.json
func updateCloudSDK(repo string) error {
	scopes.Framework.Info("Updating CloudSDK to use a CI repository")

	if repo == "" {
		return fmt.Errorf("the CloudSDK repo cannot be empty")
	}

	// Save project configs before updating CloudSDK.
	project, err := exec.Command("gcloud", "config", "get-value", "project").CombinedOutput()
	if err != nil {
		scopes.Framework.Infof("failed getting project from gcloud config:\n%v", err)
		return fmt.Errorf("failed to get project from gcloud config:\n%v", err)
	}
	billingProject, err := exec.Command("gcloud", "config", "get-value", "billing/quota_project").CombinedOutput()
	if err != nil {
		scopes.Framework.Infof("failed getting billing project from gcloud config:\n%v", err)
		return fmt.Errorf("failed to get billing project from gcloud config:\n%v", err)
	}

	if out, err := exec.Command("gcloud", "components", "repositories", "add", repo).CombinedOutput(); err != nil {
		scopes.Framework.Infof("failed adding repo to CloudSDK:\n%s", string(out))
		return fmt.Errorf("failed to add repo to CloudSDK %v:\n%s", err, string(out))
	}

	if out, err := exec.Command("gcloud", "components", "update", "-q").CombinedOutput(); err != nil {
		scopes.Framework.Infof("failed updating CloudSDK components:\n%s", string(out))
		return fmt.Errorf("failed to update CloudSDK components %v:\n%s", err, string(out))
	}

	if cred := os.Getenv(ServiceAccountCreadentialEnv); cred != "" {
		if out, err := exec.Command("gcloud", "auth", "activate-service-account", fmt.Sprintf("--key-file=%s", cred)).CombinedOutput(); err != nil {
			scopes.Framework.Infof("failed activating service account for gcloud:\n%s", string(out))
			return fmt.Errorf("failed to activate service account for gcloud %v:\n%s", err, string(out))
		}
	} else {
		scopes.Framework.Infof("%s environment variable does not exist", ServiceAccountCreadentialEnv)
		return fmt.Errorf("%s environment variable does not exist", ServiceAccountCreadentialEnv)
	}

	// Restore the previous project configs.
	cmd := exec.Command("gcloud", "config", "set", "project", string(project))
	scopes.Framework.Infof("Running: %s", cmd.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		scopes.Framework.Infof("failed setting project in gcloud config:\n%s", string(out))
		return fmt.Errorf("failed to set project in gcloud config %v:\n%s", err, string(out))
	}
	cmd = exec.Command("gcloud", "config", "set", "billing/quota_project", string(billingProject))
	scopes.Framework.Infof("Running: %s", cmd.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		scopes.Framework.Infof("failed setting billing/quota_project in gcloud config:\n%s", string(out))
		return fmt.Errorf("failed to set billing/quota_project in gcloud config %v:\n%s", err, string(out))
	}

	out, _ := exec.Command("gcloud", "version").CombinedOutput()
	scopes.Framework.Infof("CloudSDK version:\n%s", string(out))

	return nil
}
