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
	regularEnabled  = "regular-enabled"
	regularDisabled = "regular-disabled"
	rapid           = "rapid"
	defaultTag      = "default"
	noRev           = "noRev"
	regularRevision = "regular"
	rapidRevision   = rapid

	enabledAnnotationOnValue  = `{"managed":"true"}`
	enabledAnnotationOffValue = `{"managed":"false"}`
)

var (
	addr     = func(v bool) *bool { return &v }
	myrevCfg *v1alpha1.DataPlaneControl
	myRevCPR = &v1alpha1.ControlPlaneRevision{
		ObjectMeta: v12.ObjectMeta{
			Name:      regularRevision,
			Namespace: name.IstioSystemNamespace,
		},
	}
	otherRevCPR = &v1alpha1.ControlPlaneRevision{
		ObjectMeta: v12.ObjectMeta{
			Name:      rapidRevision,
			Namespace: name.IstioSystemNamespace,
		},
	}
)

func testNss() []client.Object {
	return []client.Object{
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{
			Name: regularEnabled, Labels: map[string]string{name.IstioRevisionLabel: regularRevision},
			Annotations: enabledAnnotation(boolPtr(true)),
		}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{
			Name: regularDisabled, Labels: map[string]string{name.IstioRevisionLabel: regularRevision},
			Annotations: enabledAnnotation(boolPtr(false)),
		}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: rapid, Labels: map[string]string{name.IstioRevisionLabel: rapid}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: noRev, Labels: map[string]string{}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: defaultTag, Labels: map[string]string{"istio-injection": "enabled"}}},
	}
}

// buildClient returns a fake client with a matrix of test cases across pod
// labels, namespace labels, and enablement annotations.  Additionally, a few
// edge case pods are created outside the matrix.
// Five namespaces are created across two revisions, a default tag, with one
// namespace enabled and one explicitly disabled/
// Likewise each namespace has five pods across two revisions and a default
// tag, with one pod explicitly enabled and another explicitly disabled.
// The net effect should be that all possible combinations of revisions and
// enablement are tested.
func buildClient(cpr *v1alpha1.ControlPlaneRevision) (client.Client, map[string]map[string]client.ObjectKey) {
	nss := testNss()
	podMap := map[string]map[string]client.ObjectKey{}

	var rspods []client.Object
	// replicaset.spec.replicas needs *int32, and that's hard to do with a literal.
	one := int32(1)
	for _, ns := range nss {
		podMap[ns.GetName()] = map[string]client.ObjectKey{}
		for _, rsmeta := range nss {
			// if we directly reference labels, then change them, both references get updated
			rscopy := rsmeta.DeepCopyObject().(*v1.Namespace)
			rs := &appsv1.ReplicaSet{
				ObjectMeta: v12.ObjectMeta{
					Name:      rsmeta.GetName(),
					Namespace: ns.GetName(),
					Labels:    rscopy.GetLabels(),
				},
				Spec: appsv1.ReplicaSetSpec{
					Template: v1.PodTemplateSpec{
						ObjectMeta: v12.ObjectMeta{
							Labels: rscopy.GetLabels(),
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
					Name:      rsmeta.GetName(),
					Namespace: ns.GetName(),
					OwnerReferences: []v12.OwnerReference{
						{
							Name:       rsmeta.GetName(),
							Kind:       "ReplicaSet",
							Controller: addr(true),
						},
					},
					Labels:      rscopy.GetLabels(),
					Annotations: rscopy.GetAnnotations(),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Image: "gcr.io/istio/" + name.IstioProxyImageName + ":0.0.1",
						},
					},
				},
			}
			// namespace default injection label is different from pod
			if _, ok := pod.Labels["istio-injection"]; ok {
				pod.Labels["sidecar.istio.io/inject"] = "true"
				delete(pod.Labels, "istio-injection")
			}
			pod.ObjectMeta.Labels[appKey] = rsmeta.GetName()
			podMap[ns.GetName()][pod.GetName()] = client.ObjectKeyFromObject(pod)
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
	annotatedpod.Labels = map[string]string{appKey: regularEnabled}
	rspods = append(rspods, kubesystempod, annotatedpod)
	myrevwh := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: v12.ObjectMeta{
			Name: "myrevwh",
			Labels: map[string]string{
				appKey:                  injectorValue,
				name.IstioRevisionLabel: regularRevision,
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
							Values:   []string{regularRevision},
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
							Values:   []string{regularRevision},
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
				appKey:                  injectorValue,
				name.IstioRevisionLabel: rapidRevision,
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
							Values:   []string{rapidRevision},
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
							Values:   []string{rapidRevision},
						},
					},
				},
			},
		},
	}
	defaultTagRegular := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: v12.ObjectMeta{
			Name: "defaultTagRegular",
			Labels: map[string]string{
				appKey:                  injectorValue,
				name.IstioRevisionLabel: regularRevision,
				name.IstioTagLabel:      defaultTag,
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
							Values:   []string{defaultTag},
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
							Values:   []string{defaultTag},
						},
					},
				},
			},
			{
				Name: "namespace.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "istio-injection",
							Operator: "In",
							Values:   []string{"enabled"},
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
				Name: "object.sidecar-injector.istio.io",
				NamespaceSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "istio-injection",
							Operator: "DoesNotExist",
						},
						{
							Key:      "istio.io/rev",
							Operator: "DoesNotExist",
						},
					},
				},
				ObjectSelector: &v12.LabelSelector{
					MatchExpressions: []v12.LabelSelectorRequirement{
						{
							Key:      "sidecar.istio.io/inject",
							Operator: "In",
							Values:   []string{"true"},
						},
						{
							Key:      "istio.io/rev",
							Operator: "DoesNotExist",
						},
					},
				},
			},
		},
	}
	myrevCfg = &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      regularRevision,
			Namespace: name.IstioSystemNamespace,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:               regularRevision,
			ProxyVersion:           "0.0.1",
			ProxyTargetBasisPoints: 1,
		},
	}
	otherRevCfg := &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      rapidRevision,
			Namespace: name.IstioSystemNamespace,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:               rapidRevision,
			ProxyVersion:           "0.0.1",
			ProxyTargetBasisPoints: 1,
		},
	}
	rspods = append(rspods, myrevwh, otherrevwh, defaultTagRegular, myrevCfg, otherRevCfg, cpr, otherRevCPR)

	s := scheme.Scheme
	_ = apis.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(append(nss, rspods...)...).
		Build(), podMap
}

func TestDataPlaneRevisionFromCPRevision(t *testing.T) {
	cl, _ := buildClient(myRevCPR)
	nc := NewMapper(cl)
	got, _ := nc.DataPlaneControlFromCPRevision(context.TODO(), regularRevision)
	if !reflect.DeepEqual(got, mdpcfgRequest()) {
		t.Errorf("DataPlaneControlFromCPRevision() = %v, want %v", got, mdpcfgRequest())
	}
}

func TestPodMapper(t *testing.T) {
	cl, podMap := buildClient(myRevCPR)
	nc := NewMapper(cl)
	expectedRegular := regularRevisionPods(podMap)
	expectedRapid := rapidRevisionPods(podMap)
	actual, _ := nc.PodsFromRevision(context.TODO(), regularRevision)

	g := gomega.NewGomegaWithT(t)
	g.Expect(makeSet(actual)).To(gomega.ConsistOf(expectedRegular))
	actualRapid, _ := nc.PodsFromRevision(context.TODO(), rapid)
	g.Expect(makeSet(actualRapid)).To(gomega.ConsistOf(expectedRapid))

	allpods := &v1.PodList{}
	err := cl.List(context.TODO(), allpods)
	if err != nil {
		t.Fatalf("error listing all pods: %s", err)
	}
	for _, pod := range allpods.Items {
		actualrev, _ := nc.RevisionForPod(context.TODO(), &pod)
		podKey := client.ObjectKeyFromObject(&pod)
		expectedRev := ""
		if contains(expectedRegular, podKey) {
			expectedRev = regularRevision
		} else if contains(expectedRapid, podKey) {
			expectedRev = rapid
		}
		g.Expect(actualrev).To(gomega.Equal(expectedRev))

		// Test Namespace revisions.
		if pod.GetNamespace() == constants.KubeSystemNamespace {
			continue
		}
		ns := &v1.Namespace{}
		err := cl.Get(context.TODO(), types.NamespacedName{Name: pod.GetNamespace()}, ns)
		if err != nil {
			t.Fatalf("error getting the associated namespace %s for pod %s: %v", pod.GetNamespace(), pod.GetName(), err)
		}
		nsActualRev, _ := nc.RevisionForNamespace(context.TODO(), ns)
		var nsExpectedRev string
		switch ns := ns.GetName(); ns {
		case noRev:
			nsExpectedRev = ""
		case rapid:
			nsExpectedRev = rapid
		default:
			nsExpectedRev = regularRevision
		}
		g.Expect(nsActualRev).To(gomega.Equal(nsExpectedRev))
	}
	_, err = nc.PodsFromRevision(context.TODO(), "foo")
	g.Expect(err).To(gomega.HaveOccurred())
}

func Test_naiveCache_GetRevisionFromMDPConfig(t *testing.T) {
	cl, _ := buildClient(myRevCPR)
	nc := NewMapper(cl)
	got, _ := nc.RevisionFromDPRevision(context.TODO(), mdpcfgRequest())
	if !reflect.DeepEqual(got, regularRevision) {
		t.Errorf("RevisionFromDPRevision() = %v, want %v", got, regularRevision)
	}
}

func TestPodCacheAndHandlers(t *testing.T) {
	cl, podMap := buildClient(myRevCPR)
	mapper := NewMapper(cl)
	cprHandler, rec := NewCPRHandler(mapper)
	ctx := context.Background()
	pc := NewPodCache(mapper, rec)
	sut := podEventHandler{
		podCache: pc,
		mapper:   mapper,
	}
	n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	cpr, otherCPR := *myRevCPR, *otherRevCPR
	cpr.Annotations = enabledAnnotation(boolPtr(true))
	otherCPR.Annotations = enabledAnnotation(boolPtr(true))
	cprHandler.Create(event.CreateEvent{Object: &cpr}, n)
	cprHandler.Create(event.CreateEvent{Object: &otherCPR}, n)
	sendNsEvents(cl, NewNamespaceHandler(pc, cl, mapper), map[string]*bool{})

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
	_, mytotal := pc.GetProxyVersionCount(regularRevision)
	_, othertotal := pc.GetProxyVersionCount(rapid)
	expectedRegular := 0
	for _, name := range regularRevisionPods(podMap) {
		// at this point, all regular revision pods are enabled, unless disabled by pod or namespace annotation
		if name.Name == regularDisabled || (name.Name != regularEnabled && name.Namespace == regularDisabled) {
			continue
		}
		expectedRegular++
	}
	g.Expect(mytotal).To(gomega.Equal(expectedRegular))
	// one of the rapid pods will be explicitly disabled by pod annotation.
	expectedRapid := len(rapidRevisionPods(podMap)) - 1
	g.Expect(othertotal).To(gomega.Equal(expectedRapid))

	// Update a namespace label and check results
	nsHandler := NewNamespaceHandler(pc, cl, mapper)
	ns := &v1.Namespace{}
	_ = cl.Get(ctx, types.NamespacedName{Name: regularEnabled}, ns)
	nsnew := ns.DeepCopy()
	nsnew.Labels[name.IstioRevisionLabel] = rapidRevision
	err := cl.Update(context.Background(), nsnew)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	nsHandler.Update(event.UpdateEvent{
		ObjectOld: ns,
		ObjectNew: nsnew,
	}, n)
	_, mytotal = pc.GetProxyVersionCount(regularRevision)
	_, othertotal = pc.GetProxyVersionCount(rapid)
	// n - 1 pods will have moved revisions in cache, since 1 is disabled.
	g.Expect(mytotal).To(gomega.Equal(expectedRegular - len(testNss()) + 1))
	g.Expect(othertotal).To(gomega.Equal(expectedRapid + len(testNss()) - 1))

	// these functions are no-ops, but they still need coverage
	nsHandler.Create(event.CreateEvent{Object: ns}, n)
	nsHandler.Delete(event.DeleteEvent{Object: ns}, n)
	nsHandler.Generic(event.GenericEvent{Object: ns}, n)

	// delete a pod and check results
	podToDelete := &v1.Pod{}
	err = cl.Get(ctx, podMap[regularEnabled][regularEnabled], podToDelete)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	err = cl.Delete(ctx, podToDelete)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	sut.Delete(event.DeleteEvent{Object: podToDelete}, n)
	_, othertotal = pc.GetProxyVersionCount(rapid)
	g.Expect(othertotal).To(gomega.Equal(expectedRapid + len(testNss()) - 2))
	podset := pc.GetPodsInRevisionOutOfVersion(rapid, "notmyversion")
	g.Expect(podset).To(gomega.HaveLen(expectedRapid + len(testNss()) - 2))
}

func TestDirtyPodCache(t *testing.T) {
	cl, podMap := buildClient(myRevCPR)
	mapper := NewMapper(cl)
	_, rec := NewCPRHandler(mapper)
	ctx, cancel := context.WithCancel(context.Background())
	pc := NewPodCache(mapper, rec)
	pc.MarkDirty()
	pc.maybeRebuildCache(ctx)
	g := gomega.NewGomegaWithT(t)
	g.Expect(pc.dirty).To(gomega.BeFalse())
	pc.Start(ctx)
	_, mytotal := pc.GetProxyVersionCount(regularRevision)
	_, othertotal := pc.GetProxyVersionCount(rapid)
	g.Expect(mytotal).To(gomega.Equal(len(regularEnabledPods(podMap))))
	g.Expect(othertotal).To(gomega.Equal(1)) // only one pod in rapid NS is enabled (called regular-enabled)
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

func sendNsEvents(cl client.Client, nsHandler *NameSpaceHandler, enablementMap map[string]*bool) {
	n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	for _, ns := range testNss() {
		nsObj := &v1.Namespace{}
		cl.Get(context.Background(), types.NamespacedName{Name: regularRevision}, nsObj)
		nsObjnew := nsObj.DeepCopy()
		nsObjnew.SetAnnotations(enabledAnnotation(enablementMap[ns.GetName()]))
		cl.Update(context.Background(), nsObjnew)
		nsHandler.Update(event.UpdateEvent{
			ObjectOld: ns,
			ObjectNew: nsObjnew,
		}, n)
	}
}

func enabledAnnotation(isOn *bool) map[string]string {
	if isOn == nil {
		return nil
	}
	val := enabledAnnotationOffValue
	if *isOn {
		val = enabledAnnotationOnValue
	}
	return map[string]string{name.MDPEnabledAnnotation: val}
}

func boolPtr(b bool) *bool {
	out := b
	return &out
}

func makeSet(in []*v1.Pod) []client.ObjectKey {
	result := []client.ObjectKey{}
	for _, o := range in {
		result = append(result, client.ObjectKeyFromObject(o))
	}
	return result
}

func mapToSet(in map[string]client.ObjectKey) []client.ObjectKey {
	result := []client.ObjectKey{}
	for _, o := range in {
		result = append(result, o)
	}
	return result
}

func contains(in []client.ObjectKey, key client.ObjectKey) bool {
	for _, i := range in {
		if key == i {
			return true
		}
	}
	return false
}

func regularEnabledPods(podMap map[string]map[string]client.ObjectKey) []client.ObjectKey {
	result := []client.ObjectKey{}
	regularRevisionPods := regularRevisionPods(podMap)
	for ns, pods := range podMap {
		for pod, key := range pods {
			// if pod is enabled
			if pod == regularEnabled || (ns == regularEnabled && pod != regularDisabled) {
				// and if pod is in regular revision
				if contains(regularRevisionPods, key) {
					result = append(result, key)
				}
			}
		}
	}
	return result
}

func rapidRevisionPods(podMap map[string]map[string]client.ObjectKey) []client.ObjectKey {
	// all pods in rapid namespaces
	result := mapToSet(podMap[rapid])
	// plus pods in noRev ns with pod labels for enablement
	result = append(result, podMap[noRev][rapid])
	return result
}

func regularRevisionPods(podMap map[string]map[string]client.ObjectKey) []client.ObjectKey {
	// all pods in default, regular-enabled, and regular-disabled namespaces
	result := mapToSet(podMap[regularEnabled])
	result = append(result, mapToSet(podMap[regularDisabled])...)
	result = append(result, mapToSet(podMap[defaultTag])...)
	// plus pods in noRev ns with pod labels for enablement
	result = append(result, podMap[noRev][regularEnabled],
		podMap[noRev][regularDisabled], podMap[noRev][defaultTag])
	return result
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
