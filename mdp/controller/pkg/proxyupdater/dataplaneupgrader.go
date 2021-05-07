/*
 Copyright Istio Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package proxyupdater

import (
	"context"

	"k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// DataPlaneUpgrader abstracts the actual execution of a single data plane upgrade.  For now, the only implementation
// evicts the target pod, with the assumption that it's replacement will have an upgraded data plane.
type DataPlaneUpgrader interface {
	// Upgrade the dataplane of the pod identified by podname.
	PerformUpgrade(ctx context.Context, podName types.NamespacedName) error
}

type evicterUpgrader struct {
	// unfortunately, client.Client is not capable of eviction, so we need a standard client here.
	// https://github.com/kubernetes-sigs/controller-runtime/issues/172
	v1.CoreV1Interface
}

// PerformUpgrade implements DataPlaneUpgrader interface
func (e evicterUpgrader) PerformUpgrade(ctx context.Context, podName types.NamespacedName) error {
	eviction := &v1beta1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName.Name,
			Namespace: podName.Namespace,
		},
	}
	scope.Infof("Restarting Pod %s/%s.", podName.Namespace, podName.Name)

	err := e.CoreV1Interface.Pods(podName.Namespace).Evict(ctx, eviction)
	return err
}

// NewEvictorUpgrader returns a DataPlaneUpgrader which will upgrade pods by eviction.
func NewEvictorUpgrader(cs *kubernetes.Clientset) DataPlaneUpgrader {
	return &evicterUpgrader{CoreV1Interface: cs.CoreV1()}
}
