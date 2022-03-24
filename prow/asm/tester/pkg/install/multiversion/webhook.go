//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package multiversion

import (
	"fmt"
	"log"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
)

const (
	webhookPrefix = "istio-sidecar-injector"
)

// ReplaceWebhook creates a webhook with per-revision object selectors.
// this is useful when performing compat testing with older ASM versions.
func ReplaceWebhook(rev *revision.Config, contextName string) error {
	// If no version or name specified we are at master or install only one revision
	// and the webhooks have the desired behavior as is.
	if rev.Version == "" || rev.Name == "" {
		return nil
	}

	log.Printf("Generating webhook for revision %q: context: %s",
		rev.Name, contextName)

	webhookName := fmt.Sprintf("%s-%s",
		webhookPrefix, rev.Name)
	webhookCreateCmd := fmt.Sprintf("istioctl x revision tag set %s --revision %s --context %s --webhook-name %s --overwrite -y",
		rev.Name, rev.Name, contextName, webhookName)

	if err := exec.Run(webhookCreateCmd); err != nil {
		return fmt.Errorf("failed running tag set command: %w", err)
	}
	return nil
}
