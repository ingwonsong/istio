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

package tests

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests/caproxy"
	"istio.io/istio/prow/asm/tester/pkg/tests/kubevirtvm"
	"istio.io/istio/prow/asm/tester/pkg/tests/policyconstraint"
	"istio.io/istio/prow/asm/tester/pkg/tests/userauth"
)

func Teardown(settings *resource.Settings) error {
	log.Println("🎬 start tearing down the tests...")

	updateMetadataJson(settings)

	if settings.ControlPlane == resource.Unmanaged && settings.FeaturesToTest.Has(string(resource.UserAuth)) {
		return userauth.Teardown(settings)
	}

	if settings.UseKubevirtVM {
		log.Printf("Start running the teardown for kubevirt vm tests")
		if err := kubevirtvm.TearDown(settings); err != nil {
			return err
		}
	}

	if settings.FeaturesToTest.Has(string(resource.PolicyConstraint)) {
		return policyconstraint.Teardown(settings)
	}

	if settings.FeaturesToTest.Has(string(resource.CAProxy)) {
		return caproxy.Teardown(settings)
	}

	// Unset the proxy if the tests are run on proxied clusters.
	if len(settings.ClusterProxy) != 0 {
		os.Unsetenv("HTTPS_PROXY")
	}

	return nil
}

func updateMetadataJson(settings *resource.Settings) {
	logDirSplit := strings.Split(settings.Kubeconfig, ".kubetest2-tailorbird")
	var logDir string
	if len(logDirSplit) > 0 {
		logDir = logDirSplit[0]
	} else {
		log.Printf("unable to find log Dir from kubeconfig path : %v", settings.Kubeconfig)
		return
	}
	metadataFilePath := logDir + "metadata.json"

	metadata, err := unmarshalJsonFromFile(metadataFilePath)
	if err != nil {
		log.Printf("unable to read metadata file %v : %v", metadataFilePath, err)
		return
	}
	metadataArgsFilePath := os.TempDir() + string(os.PathSeparator) + "metadata_args.json"
	metadataArgs, err := unmarshalJsonFromFile(metadataArgsFilePath)
	if err != nil {
		log.Printf("unable to read metadata_args file to export %v : %v", metadataArgsFilePath, err)
		return
	}
	for k, v := range metadataArgs {
		metadata[k] = v
	}
	jsonStr, err := json.Marshal(metadata)
	if err != nil {
		log.Printf("unable to marshal exported metadata map %v : %v", metadata, err)
		return
	}
	err = os.WriteFile(metadataFilePath, jsonStr, os.ModePerm)
	if err != nil {
		log.Printf("unable to write to metadata file %v : %v", metadataFilePath, err)
		return
	}
}

func unmarshalJsonFromFile(filepath string) (map[string]interface{}, error) {
	var metadata map[string]interface{}
	byteValue, errRead := os.ReadFile(filepath)
	if errRead != nil {
		return metadata, errRead
	}
	errUnmarshal := json.Unmarshal(byteValue, &metadata)
	if errUnmarshal != nil {
		return metadata, errUnmarshal
	}
	return metadata, nil
}
