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

package gcp

import (
	"fmt"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
)

const (
	CasSubCaIdPrefix        = "asm-testci-sub-pool"
	CasCertTemplateIdPrefix = "asm-testci-cert-template"
	CasRootCaLoc            = "us-central1"
)

func GetProjectNumber(projectId string) (string, error) {
	projectNum, err := exec.RunWithOutput(
		fmt.Sprintf("gcloud projects describe %s --format=value(projectNumber)", projectId))
	if err != nil {
		err = fmt.Errorf("Error getting the project number for %q: %w", projectId, err)
	}
	return strings.TrimSpace(projectNum), err
}

func GetServiceAccount() (string, error) {
	serviceAccount, err := exec.RunWithOutput("gcloud config get-value account")
	if err != nil {
		err = fmt.Errorf("Error getting service account: %w", err)
	}
	return strings.TrimSpace(serviceAccount), err
}

func GetPrivateCAPool(project, clusterLocation string) string {
	issuingCaPoolId := fmt.Sprintf("%s-%s", CasSubCaIdPrefix, clusterLocation)
	return fmt.Sprintf("projects/%s/locations/%s/caPools/%s",
		project, clusterLocation, issuingCaPoolId)
}

func GetPrivateCACertTemplate(project, clusterLocation string) string {
	caCertificateTemplateId := fmt.Sprintf("%s-%s", CasCertTemplateIdPrefix, clusterLocation)
	return fmt.Sprintf("projects/%s/locations/%s/certificateTemplates/%s",
		project, clusterLocation, caCertificateTemplateId)
}