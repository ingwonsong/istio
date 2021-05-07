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

package status

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"golang.org/x/time/rate"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"istio.io/istio/mdp/controller/pkg/apis"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
)

const myrev = "myrev"

func buildClient() (client.Client, *v1alpha1.DataPlaneControl) {
	proxyVersion := "0.0.1"
	myDPR := &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      myrev,
			Namespace: myrev,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:               myrev,
			ProxyVersion:           proxyVersion,
			ProxyTargetBasisPoints: 8000,
		},
	}

	s := scheme.Scheme
	_ = apis.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(myDPR).
		Build(), myDPR
}

func TestStatusWorker(t *testing.T) {
	cl, dpc := buildClient()
	sw := NewWorker(rate.Every(time.Millisecond), cl)
	dpc.Status = v1alpha1.DataPlaneControlStatus{
		ProxyTargetBasisPoints: 1234,
	}
	sw.EnqueueStatus(dpc)
	sw.Start(context.Background())
	g := gomega.NewGomegaWithT(t)
	g.Eventually(func() int32 {
		err := cl.Get(context.Background(), client.ObjectKey{Namespace: myrev, Name: myrev}, dpc)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		return dpc.Status.ProxyTargetBasisPoints
	}).Should(gomega.Equal(int32(1234)))
	sw.Stop()
}
