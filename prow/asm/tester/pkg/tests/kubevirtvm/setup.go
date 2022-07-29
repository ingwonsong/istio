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

package kubevirtvm

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// Setup runs the test setups for kubevirt vm tests.
func Setup(settings *resource.Settings) error {
	gcsFolder := os.Getenv("KUBEVIRT_VM_ECHO_ARTIFACTS_GCS_FOLDER")
	serviceAccountPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")

	log.Println("Copying echo artifacts to GCS which will be downloaded in kubevirt vm")
	gsutilCmds := []string{
		fmt.Sprintf("gsutil cp out/linux_amd64/server gs://%s/", gcsFolder),
		fmt.Sprintf("gsutil cp out/linux_amd64/client gs://%s/", gcsFolder),
		fmt.Sprintf("gsutil cp tests/testdata/certs/cert.crt gs://%s/", gcsFolder),
		fmt.Sprintf("gsutil cp tests/testdata/certs/cert.key gs://%s/", gcsFolder),
	}
	if err := exec.RunMultiple(gsutilCmds); err != nil {
		return err
	}

	// Pass service account and gcs folder to all clusters as config map to enable kubevirt vm to access gcs in tests
	configs := filepath.SplitList(settings.Kubeconfig)
	for _, config := range configs {
		if err := exec.Run(fmt.Sprintf("kubectl create configmap kubevirtconfigmap --from-file %s --from-literal gcsfolder=%s --kubeconfig=%s",
			serviceAccountPath, gcsFolder, config)); err != nil {
			return err
		}
	}

	return nil
}