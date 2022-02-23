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

package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ExportLogs will export the Pod logs in the given namespace to the artifacts
// directory, if the tests are run in CI.
// TODO(chizhg): reuse the functions in istio.io/istio/pkg/test/kube/dump.go
// instead of making a new one here.
func ExportLogs(kubeconfig, namespace, selector, dir string) error {
	cli, err := NewClient(kubeconfig)
	if err != nil {
		return err
	}

	return exportLogs(context.Background(), cli, kubeconfig, namespace, selector, dir)
}

func exportLogs(ctx context.Context, kubeClient kubernetes.Interface, kubeconfig, namespace, selector, dir string) error {
	// Create a directory for the namespace.
	logPath := filepath.Join(dir, filepath.Base(kubeconfig), namespace)
	if err := os.MkdirAll(logPath, os.ModePerm); err != nil {
		return fmt.Errorf("error creating directory %q: %w", namespace, err)
	}

	// List all the Pods in the namespace.
	podsClient := kubeClient.CoreV1().Pods(namespace)
	pods, err := podsClient.List(context.Background(), metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return fmt.Errorf("error listing pods in namespace %q: %w", namespace, err)
	}

	var errs error
	for _, pod := range pods.Items {
		for _, ct := range pod.Spec.Containers {
			// Create the log file.
			fn := filepath.Join(logPath, fmt.Sprintf("%s-%s.log", pod.Name, ct.Name))
			log.Printf("Exporting logs in pod %q container %q to %q", pod.Name, ct.Name, fn)
			f, err := os.OpenFile(fn, os.O_APPEND|os.O_CREATE|os.O_WRONLY, os.ModePerm)
			defer f.Close()
			if err != nil {
				errs = multierror.Append(errs, fmt.Errorf("error creating file %q: %w", fn, err))
			}

			// Get the log for the container.
			result := podsClient.GetLogs(pod.Name, &corev1.PodLogOptions{
				Container: ct.Name,
			}).Do(ctx)
			rawLog, err := result.Raw()
			if err != nil {
				errs = multierror.Append(errs, fmt.Errorf("error getting logs for pod %q container %q: %w", pod.Name, ct.Name, err))
			}
			// Write the log to the log file.
			_, err = f.Write(rawLog)
			if err != nil {
				errs = multierror.Append(errs, fmt.Errorf("error writing logs into file %q: %w", fn, err))
			}
			// Also append the pod status to the log file.
			podStatus, _ := json.MarshalIndent(pod, "", "  ")
			_, err = f.Write([]byte(fmt.Sprintf("====Pod status====\n%s", podStatus)))
			if err != nil {
				errs = multierror.Append(errs, fmt.Errorf("error writing logs into file %q: %w", fn, err))
			}
		}
	}

	return multierror.Flatten(err)
}
