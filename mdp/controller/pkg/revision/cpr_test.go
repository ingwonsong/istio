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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"istio.io/api/annotation"
	"istio.io/istio/mdp/controller/pkg/apis"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/pkg/config/constants"
)

const (
	ns1RegularNamespace    = "ns1_regular"
	ns2RegularNamespace    = "ns2_regular"
	ns3RapidNamespace      = "ns3_rapid"
	ns4NoRevisionNamespace = "ns4"
)

// buildCPRClient returns a fake client with 20 pods divided across four namespaces, to test all
// possible combinations of revision label mapping, along with two known revisions.
// 4 pods each in ns1, ns2, ns3, ns4.
// 2 pods unmanaged, 2 pods annotated but not injected.
func buildCPRClient(cpr *v1alpha1.ControlPlaneRevision, nsEnabled, podsEnabled map[string]*bool) client.Client {
	nss := []client.Object{
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: ns1RegularNamespace, Labels: map[string]string{name.IstioRevisionLabel: regularRevision}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: ns2RegularNamespace, Labels: map[string]string{name.IstioRevisionLabel: regularRevision}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: ns3RapidNamespace, Labels: map[string]string{name.IstioRevisionLabel: rapidRevision}}},
		&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: ns4NoRevisionNamespace}},
	}

	for _, ns := range nss {
		ns.SetAnnotations(enabledAnnotation(nsEnabled[ns.GetName()]))
	}

	var rspods []client.Object
	// replicaset.spec.replicas needs *int32, and that's hard to do with a literal.
	one := int32(1)
	for _, ns := range nss {
		for i, rsmeta := range nss {
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
					Name:      fmt.Sprintf("%d-%s-Pod", i, ns.GetName()),
					Namespace: ns.GetName(),
					OwnerReferences: []v12.OwnerReference{
						{
							Name:       rsmeta.GetName(),
							Kind:       "ReplicaSet",
							Controller: addr(true),
						},
					},
					Labels:      map[string]string{appKey: rsmeta.GetName()},
					Annotations: enabledAnnotation(podsEnabled[rsmeta.GetName()+"Pod"]),
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
	annotatedpod.Labels = map[string]string{appKey: regularRevision}
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
	myrevwhWithDefaultTag := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: v12.ObjectMeta{
			Name: "myrevwhWithDefaultTag",
			Labels: map[string]string{
				appKey:                  injectorValue,
				name.IstioRevisionLabel: regularRevision,
				name.IstioTagLabel:      "default",
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
			Revision:               regularRevision,
			ProxyVersion:           "0.0.1",
			ProxyTargetBasisPoints: 1,
		},
	}
	rspods = append(rspods, myrevwh, otherrevwh, myrevwhWithDefaultTag, myrevCfg, otherRevCfg, cpr, otherRevCPR)

	s := scheme.Scheme
	_ = apis.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(append(nss, rspods...)...).
		Build()
}

func TestPodOperations(t *testing.T) {
	cl := buildCPRClient(myRevCPR, nil, nil)
	nc := NewMapper(cl)

	cpr := *myRevCPR
	cpr.Annotations = enabledAnnotation(boolPtr(true))
	cprHandler, enablementCache := NewCPRHandler(nc)
	pc := NewPodCache(nc, enablementCache)

	// Populate event driven caches
	n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	cprHandler.Create(event.CreateEvent{Object: &cpr}, n)
	sendNsEvents(cl, NewNamespaceHandler(pc, cl, nc), map[string]*bool{})

	mypods, _ := nc.PodsFromRevision(context.TODO(), regularRevision)
	if len(mypods) != 20 {
		t.Fatalf("expected 20 pods in revision, but got %d", len(mypods))
	}
	got := map[string]bool{}
	for _, pod := range mypods {
		got[pod.GetName()] = true
	}
	want := stringSliceToMap([]string{
		"0-ns1_regular-Pod",
		"1-ns1_regular-Pod",
		"2-ns1_regular-Pod",
		"3-ns1_regular-Pod",
		"0-ns2_regular-Pod",
		"1-ns2_regular-Pod",
		"2-ns2_regular-Pod",
		"3-ns2_regular-Pod",
		"0-ns4-Pod",
		"1-ns4-Pod",
	})
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("TestPodOperations PodsFromRevision diff (got-, want+): \n%s", diff)
	}
	otherPods, _ := nc.PodsFromRevision(context.TODO(), rapidRevision)
	// 4 in ns3, one by annotation.
	if len(otherPods) != 5 {
		t.Fatalf("expected 5 pods in revision, but got %d", len(otherPods))
	}

	allpods := &v1.PodList{}
	err := cl.List(context.TODO(), allpods)
	if err != nil {
		t.Fatalf("error listing all pods: %s", err)
	}
	for _, pod := range allpods.Items {
		podrev, _ := nc.RevisionForPod(context.TODO(), &pod)
		if pod.GetNamespace() == ns1RegularNamespace &&
			!strings.HasPrefix(pod.GetName(), "Uncontrolled") &&
			!strings.HasPrefix(pod.GetName(), "annotated") &&
			podrev != regularRevision {
			t.Fatalf("pod %s revision got: %s, want: %s", pod.GetName(), podrev, regularRevision)
		}
	}
	g := gomega.NewGomegaWithT(t)
	_, err = nc.PodsFromRevision(context.TODO(), "foo")
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestEnablement(t *testing.T) {
	tests := []struct {
		name        string
		cprEnabled  *bool
		nsEnabled   map[string]*bool
		podsEnabled map[string]*bool
		wantPods    []string
	}{
		{
			name: "all off",
		},
		{
			name:       "only cpr on",
			cprEnabled: boolPtr(true),
			wantPods: []string{
				"0-ns1_regular-Pod",
				"1-ns1_regular-Pod",
				"2-ns1_regular-Pod",
				"3-ns1_regular-Pod",
				"0-ns2_regular-Pod",
				"1-ns2_regular-Pod",
				"2-ns2_regular-Pod",
				"3-ns2_regular-Pod",
				"0-ns4-Pod",
				"1-ns4-Pod",
			},
		},
		{
			name:       "cpr on ns1 off",
			cprEnabled: boolPtr(true),
			nsEnabled: map[string]*bool{
				ns1RegularNamespace: boolPtr(false),
			},
			wantPods: []string{
				"0-ns2_regular-Pod",
				"1-ns2_regular-Pod",
				"2-ns2_regular-Pod",
				"3-ns2_regular-Pod",
				"0-ns4-Pod",
				"1-ns4-Pod",
			},
		},
		{
			name:       "cpr off ns1 on",
			cprEnabled: boolPtr(false),
			nsEnabled: map[string]*bool{
				ns1RegularNamespace: boolPtr(true),
			},
			wantPods: []string{
				"0-ns1_regular-Pod",
				"1-ns1_regular-Pod",
				"2-ns1_regular-Pod",
				"3-ns1_regular-Pod",
			},
		},
		{
			name:       "cpr off ns1 off 0 on",
			cprEnabled: boolPtr(false),
			nsEnabled: map[string]*bool{
				ns1RegularNamespace: boolPtr(false),
			},
			podsEnabled: map[string]*bool{
				"0-ns1_regular-Pod": boolPtr(true),
			},
			wantPods: []string{
				"0-ns1_regular-Pod",
			},
		},
		{
			name:       "cpr off ns2_regular on 0 off",
			cprEnabled: boolPtr(false),
			nsEnabled: map[string]*bool{
				ns2RegularNamespace: boolPtr(true),
			},
			podsEnabled: map[string]*bool{
				"0-ns2_regular-Pod": boolPtr(false),
			},
			wantPods: []string{
				"1-ns2_regular-Pod",
				"2-ns2_regular-Pod",
				"3-ns2_regular-Pod",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpr := *myRevCPR
			cpr.Annotations = enabledAnnotation(tt.cprEnabled)
			cl := buildCPRClient(&cpr, tt.nsEnabled, tt.podsEnabled)
			mapper := NewMapper(cl)
			cprHandler, enablementCache := NewCPRHandler(mapper)
			pc := NewPodCache(mapper, enablementCache)

			// Populate event driven caches
			n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			cprHandler.Create(event.CreateEvent{Object: &cpr}, n)

			sendNsEvents(cl, NewNamespaceHandler(pc, cl, mapper), tt.nsEnabled)
			mypods, _ := mapper.PodsFromRevision(context.TODO(), regularRevision)
			got := make(map[string]bool)
			for _, p := range mypods {
				p.SetAnnotations(enabledAnnotation(tt.podsEnabled[p.GetName()]))
				pc.AddPod(p)
				if pc.podIsEnabled(p, regularRevision) {
					got[p.GetName()] = true
				}
			}

			want := stringSliceToMap(tt.wantPods)
			if diff := cmp.Diff(got, want); diff != "" {
				t.Errorf("%s: got-, want+): %s", tt.name, diff)
			}
		})
	}
}

func TestCPRDefaults(t *testing.T) {
	tests := []struct {
		name        string
		cprEnabled  *bool
		channel     v1alpha1.ReleaseChannel
		wantNumPods int
	}{
		{
			name:        "cpr unset, regular channel",
			cprEnabled:  nil,
			channel:     v1alpha1.ChannelRegular,
			wantNumPods: 0,
		},
		{
			name:        "cpr unset, rapid channel",
			cprEnabled:  nil,
			channel:     v1alpha1.ChannelRapid,
			wantNumPods: 20,
		},
		{
			name:        "cpr true, regular channel",
			cprEnabled:  boolPtr(true),
			channel:     v1alpha1.ChannelRegular,
			wantNumPods: 20,
		},
		{
			name:        "cpr false, rapid channel",
			cprEnabled:  boolPtr(false),
			channel:     v1alpha1.ChannelRapid,
			wantNumPods: 0,
		},
	}

	// Note: these tests are dependent on mdpEnabledByDefaultMap which is globally set.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpr := *myRevCPR
			cpr.Annotations = enabledAnnotation(tt.cprEnabled)
			cpr.Spec.Channel = tt.channel
			cl := buildCPRClient(&cpr, nil, nil)
			mapper := NewMapper(cl)
			cprHandler, enablementCache := NewCPRHandler(mapper)
			pc := NewPodCache(mapper, enablementCache)

			// Populate event driven caches
			n := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			cprHandler.Create(event.CreateEvent{Object: &cpr}, n)

			mypods, _ := mapper.PodsFromRevision(context.TODO(), regularRevision)
			enabledCount := 0
			for _, p := range mypods {
				pc.AddPod(p)
				if pc.podIsEnabled(p, regularRevision) {
					enabledCount++
				}
			}
			if got, want := enabledCount, tt.wantNumPods; got != want {
				t.Errorf("%s: got, want: %d, %d", tt.name, got, want)
			}
		})
	}
}

func stringSliceToMap(ss []string) map[string]bool {
	out := make(map[string]bool)
	for _, s := range ss {
		out[s] = true
	}
	return out
}
