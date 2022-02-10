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

package revision

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/api/annotation"
	"istio.io/istio/mdp/controller/pkg/apis"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/pkg/config/constants"
)

const (
	myRev    = "myRev"
	otherRev = "otherrev"
	noRev    = "norev"
)

var (
	addr     = func(v bool) *bool { return &v }
	myrevCfg *v1alpha1.DataPlaneControl
)

// buildClient returns a fake client with nine pods and replicasets divided across three namespaces, to test all
// possible combinations of revision label mapping, along with two known revisions
func buildClient() client.Client {
	enabledAnnotation := map[string]string{name.MDPEnabledAnnotation: "{\"managed\":\"true\"}"}
	myRevCM := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:        fmt.Sprintf("%s%s", name.EnablementCMPrefix, myRev),
			Namespace:   name.IstioSystemNamespace,
			Annotations: enabledAnnotation,
		},
	}
	otherRevCM := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:        fmt.Sprintf("%s%s", name.EnablementCMPrefix, otherRev),
			Namespace:   name.IstioSystemNamespace,
			Annotations: enabledAnnotation,
		},
	}
	nss := []client.Object{
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: myRev, Labels: map[string]string{name.IstioRevisionLabel: myRev}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: otherRev, Labels: map[string]string{name.IstioRevisionLabel: otherRev}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: noRev}},
	}
	var rspods []client.Object
	// replicaset.spec.replicas needs *int32, and that's hard to do with a literal.
	one := int32(1)
	for _, ns := range nss {
		for _, rsmeta := range nss {
			rs := &appsv1.ReplicaSet{
				ObjectMeta: v12.ObjectMeta{
					Name:      rsmeta.GetName(),
					Namespace: ns.GetName(),
					Labels:    rsmeta.GetLabels(),
				},
				Spec: appsv1.ReplicaSetSpec{
					Template: v1.PodTemplateSpec{
						ObjectMeta: v12.ObjectMeta{
							Labels: rsmeta.GetLabels(),
						},
					},
					Selector: &v12.LabelSelector{
						MatchLabels: map[string]string{appKey: rsmeta.GetName()},
					},
					Replicas: &one,
				},
			}
			pod := &v1.Pod{
				ObjectMeta: v12.ObjectMeta{
					Name:      rsmeta.GetName() + "Pod",
					Namespace: ns.GetName(),
					OwnerReferences: []v12.OwnerReference{
						{
							Name:       rsmeta.GetName(),
							Kind:       "ReplicaSet",
							Controller: addr(true),
						},
					},
					Labels: map[string]string{appKey: rsmeta.GetName()},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Image: "gcr.io/istio/" + name.IstioProxyImageName + ":0.0.1",
						},
					},
				},
			}
			rspods = append(rspods, rs, pod)
		}
	}
	rspods = append(rspods, &v1.Pod{
		ObjectMeta: v12.ObjectMeta{
			Name:      "UncontrolledPod",
			Namespace: nss[0].GetName(),
			OwnerReferences: []v12.OwnerReference{
				{
					Kind:       "FakeKind",
					Controller: addr(true),
				},
			},
		},
	}, &v1.Pod{
		ObjectMeta: v12.ObjectMeta{
			Name:      "UncontrolledPod2",
			Namespace: nss[0].GetName(),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Image: "gcr.io/istio/" + name.IstioProxyImageName + ":0.0.1",
				},
			},
		},
	})
	kubesystempod := rspods[1].DeepCopyObject().(*v1.Pod)
	kubesystempod.Namespace = constants.KubeSystemNamespace
	annotatedpod := rspods[1].DeepCopyObject().(*v1.Pod)
	annotatedpod.Annotations = map[string]string{annotation.SidecarInject.Name: "n"}
	annotatedpod.Name = "annotated-notinjected"
	annotatedpod.Labels = map[string]string{appKey: myRev}
	rspods = append(rspods, kubesystempod, annotatedpod)
	myrevwh := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: v12.ObjectMeta{
			Labels: map[string]string{
				appKey:                  injectorValue,
				name.IstioRevisionLabel: myRev,
			},
		},
		Webhooks: []admissionv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      name.IstioRevisionLabel,
							Operator: v12.LabelSelectorOpIn,
							Values:   []string{"myRev"},
						},
						{
							Key:      "istio-injection",
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
					},
				},
				ObjectSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "sidecar.istio.io/inject",
							Operator: "NotIn",
							Values:   []string{"false"},
						},
					},
				},
			},
			{
				Name: "rev.object.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      name.IstioRevisionLabel,
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
						{
							Key:      "istio-injection",
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
					},
				},
				ObjectSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "sidecar.istio.io/inject",
							Operator: "NotIn",
							Values:   []string{"false"},
						},
						{
							Key:      name.IstioRevisionLabel,
							Operator: "In",
							Values:   []string{"myRev"},
						},
					},
				},
			},
		},
	}
	otherrevwh := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: v12.ObjectMeta{
			Name: "otherwh",
			Labels: map[string]string{
				"app":                   "sidecar-injector",
				name.IstioRevisionLabel: otherRev,
			},
		},
		Webhooks: []admissionv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      name.IstioRevisionLabel,
							Operator: v12.LabelSelectorOpIn,
							Values:   []string{"otherrev"},
						},
						{
							Key:      "istio-injection",
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
					},
				},
				ObjectSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "sidecar.istio.io/inject",
							Operator: "NotIn",
							Values:   []string{"false"},
						},
					},
				},
			},
			{
				Name: "rev.object.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      name.IstioRevisionLabel,
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
						{
							Key:      "istio-injection",
							Operator: v12.LabelSelectorOpDoesNotExist,
						},
					},
				},
				ObjectSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "sidecar.istio.io/inject",
							Operator: "NotIn",
							Values:   []string{"false"},
						},
						{
							Key:      name.IstioRevisionLabel,
							Operator: "In",
							Values:   []string{"otherrev"},
						},
					},
				},
			},
		},
	}
	myrevCfg = &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      myRev,
			Namespace: myRev,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:               myRev,
			ProxyVersion:           "0.0.1",
			ProxyTargetBasisPoints: 1,
		},
	}
	rspods = append(rspods, myrevwh, otherrevwh, myrevCfg, otherRevCM, myRevCM)

	s := scheme.Scheme
	_ = apis.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(append(nss, rspods...)...).
		Build()
}

func mdpcfgRequest() reconcile.Request {
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: myrevCfg.Namespace,
			Name:      myrevCfg.Name,
		},
	}
	return req
}

func TestDataPlaneRevisionFromCPRevision(t *testing.T) {
	cl := buildClient()
	nc := NewMapper(cl)
	got, _ := nc.DataPlaneControlFromCPRevision(context.TODO(), myRev)
	if !reflect.DeepEqual(got, mdpcfgRequest()) {
		t.Errorf("DataPlaneControlFromCPRevision() = %v, want %v", got, mdpcfgRequest())
	}
}

func TestPodOperations(t *testing.T) {
	cl := buildClient()
	nc := NewMapper(cl)
	mypods, _ := nc.PodsFromRevision(context.TODO(), myRev)
	if len(mypods) != 4 {
		t.Fatalf("expected 4 pods in revision, but got %d", len(mypods))
	}
	mypodkeys := map[client.ObjectKey]struct{}{}
	for _, pod := range mypods {
		mypodkeys[client.ObjectKeyFromObject(pod)] = struct{}{}
	}
	expectedKeys := map[client.ObjectKey]struct{}{
		{Name: myRev + "Pod", Namespace: myRev}:    {},
		{Name: otherRev + "Pod", Namespace: myRev}: {},
		{Name: noRev + "Pod", Namespace: myRev}:    {},
		{Name: myRev + "Pod", Namespace: noRev}:    {},
	}
	if !reflect.DeepEqual(mypodkeys, expectedKeys) {
		t.Fatal("PodsFromRevision is missing an expected pod.")
	}
	allpods := &v1.PodList{}
	err := cl.List(context.TODO(), allpods)
	if err != nil {
		t.Fatalf("error listing all pods: %s", err)
	}
	for _, pod := range allpods.Items {
		podrev, _ := nc.RevisionForPod(context.TODO(), &pod)
		podkey := client.ObjectKeyFromObject(&pod)
		_, ismypod := mypodkeys[podkey]
		ismyrev := podrev == myRev
		if ismypod != ismyrev {
			t.Fatalf("expected pod %s in myRev to be %t, was %s", podkey.String(), ismypod, podrev)
		}
	}
	g := gomega.NewGomegaWithT(t)
	_, err = nc.PodsFromRevision(context.TODO(), "foo")
	g.Expect(err).To(gomega.HaveOccurred())
}

func Test_naiveCache_GetRevisionFromMDPConfig(t *testing.T) {
	cl := buildClient()
	nc := NewMapper(cl)
	got, _ := nc.RevisionFromDPRevision(context.TODO(), mdpcfgRequest())
	if !reflect.DeepEqual(got, myRev) {
		t.Errorf("RevisionFromDPRevision() = %v, want %v", got, myRev)
	}
}

func TestPodCacheAndHandlers(t *testing.T) {
	cl := buildClient()
	mapper := NewMapper(cl)
	cmHandler, rec := NewConfigMapHandler(mapper)
	ctx := context.Background()
	pc := NewPodCache(mapper, rec)
	sut := podEventHandler{
		podCache: pc,
		mapper:   mapper,
	}
	n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	cms := &v1.ConfigMapList{}
	_ = cl.List(ctx, cms)
	for _, cm := range cms.Items {
		cmHandler.Create(event.CreateEvent{Object: &cm}, n)
	}
	pods := &v1.PodList{}
	_ = cl.List(ctx, pods)
	for _, p := range pods.Items {
		sut.Create(event.CreateEvent{Object: &p}, n)
	}
	upod := pods.Items[0].DeepCopy()
	// increment pod to v2
	upod.Spec.Containers[0].Image = "gcr.io/istio/" + name.IstioProxyImageName + ":0.0.2"
	sut.Update(event.UpdateEvent{
		ObjectOld: &pods.Items[0],
		ObjectNew: upod,
	}, n)
	g := gomega.NewGomegaWithT(t)
	_, mytotal := pc.GetProxyVersionCount(myRev)
	_, othertotal := pc.GetProxyVersionCount(otherRev)
	g.Expect(mytotal).To(gomega.Equal(4))
	g.Expect(othertotal).To(gomega.Equal(4))

	// Update a namespace label and check results
	nsHandler := NewNamespaceHandler(pc, cl, mapper)
	ns := &v1.Namespace{}
	_ = cl.Get(ctx, types.NamespacedName{Name: myRev}, ns)
	nsnew := ns.DeepCopy()
	nsnew.Labels[name.IstioRevisionLabel] = otherRev
	err := cl.Update(context.Background(), nsnew)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	nsHandler.Update(event.UpdateEvent{
		ObjectOld: ns,
		ObjectNew: nsnew,
	}, n)
	_, mytotal = pc.GetProxyVersionCount(myRev)
	_, othertotal = pc.GetProxyVersionCount(otherRev)
	g.Expect(mytotal).To(gomega.Equal(1))
	g.Expect(othertotal).To(gomega.Equal(7))

	// these functions are no-ops, but they still need coverage
	nsHandler.Create(event.CreateEvent{Object: ns}, n)
	nsHandler.Delete(event.DeleteEvent{Object: ns}, n)
	nsHandler.Generic(event.GenericEvent{Object: ns}, n)

	// delete a pod and check results
	podToDelete := &v1.Pod{}
	err = cl.Get(ctx, client.ObjectKey{Namespace: otherRev, Name: "myRevPod"}, podToDelete)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	err = cl.Delete(ctx, podToDelete)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	sut.Delete(event.DeleteEvent{Object: podToDelete}, n)
	_, othertotal = pc.GetProxyVersionCount(otherRev)
	g.Expect(othertotal).To(gomega.Equal(6))
	podset := pc.GetPodsInRevisionOutOfVersion(otherRev, "notmyversion")
	g.Expect(podset).To(gomega.HaveLen(6))
}

func TestDirtyPodCache(t *testing.T) {
	cl := buildClient()
	mapper := NewMapper(cl)
	cmHandler, rec := NewConfigMapHandler(mapper)
	ctx, cancel := context.WithCancel(context.Background())
	pc := NewPodCache(mapper, rec)
	n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	cms := &v1.ConfigMapList{}
	_ = cl.List(ctx, cms)
	for _, cm := range cms.Items {
		cmHandler.Create(event.CreateEvent{Object: &cm}, n)
	}
	pc.MarkDirty()
	pc.(*podCache).maybeRebuildCache(ctx)
	g := gomega.NewGomegaWithT(t)
	g.Expect(pc.(*podCache).dirty).To(gomega.BeFalse())
	pc.Start(ctx)
	_, mytotal := pc.GetProxyVersionCount(myRev)
	_, othertotal := pc.GetProxyVersionCount(otherRev)
	g.Expect(mytotal).To(gomega.Equal(4))
	g.Expect(othertotal).To(gomega.Equal(4))
	cancel()
}

func TestHostNetworkDisabled(t *testing.T) {
	p := &v1.Pod{
		Spec: v1.PodSpec{
			HostNetwork: true,
		},
	}
	actual := PodShouldBeInjected(p)
	g := gomega.NewGomegaWithT(t)
	g.Expect(actual).To(gomega.BeFalse())
}
