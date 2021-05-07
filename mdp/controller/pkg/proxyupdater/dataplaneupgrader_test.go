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
	"testing"

	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	testing2 "k8s.io/client-go/testing"
)

func TestEvictorPerformUpgrade(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	nsn := types.NamespacedName{
		Namespace: "myNS",
		Name:      "myPod",
	}
	cs := fake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: v12.ObjectMeta{
			Name:      nsn.Name,
			Namespace: nsn.Namespace,
		},
	})

	eu := evicterUpgrader{
		CoreV1Interface: cs.CoreV1(),
	}
	// assert upgrade does not error and causes the desired eviction
	err := eu.PerformUpgrade(context.TODO(), nsn)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	var actionOccurred bool
	for _, act := range cs.Actions() {
		if act.Matches("create", "pods/eviction") && act.GetNamespace() == nsn.Namespace &&
			act.(testing2.CreateAction).GetObject().(*v1beta1.Eviction).Name == nsn.Name {
			actionOccurred = true
		}
	}
	g.Expect(actionOccurred).To(gomega.BeTrue())
}
