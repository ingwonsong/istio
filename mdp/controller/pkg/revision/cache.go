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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"
	v1admission "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/api/annotation"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/globalerrors"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/mdp/controller/pkg/util"
	"istio.io/istio/pkg/kube/inject"
	"istio.io/pkg/log"
)

// Mapper maps between pods, revisions, and DataPlaneControl.  Information may be returned from a cache, or retrieved from k8s.
type Mapper interface {
	// RevisionForPod uses the pod's controller's podTemplate to identify which revision a newly created pod will point to.
	RevisionForPod(ctx context.Context, pod *v1.Pod) (string, error)
	// PodsFromRevision iterates over all namespaces and controllers to identify pods which belong to controllers
	// that create pods belonging to the specified revision.
	PodsFromRevision(ctx context.Context, rev string) ([]*v1.Pod, error)
	// DataPlaneControlFromCPRevision retrieves the DataPlaneControl corresponding to the specified Control Plane Revision
	DataPlaneControlFromCPRevision(ctx context.Context, rev string) (reconcile.Request, error)
	// RevisionFromDPRevision retrieves the Control Plane Revision corresponding to the specified DataPlaneControl
	RevisionFromDPRevision(ctx context.Context, req reconcile.Request) (string, error)
	// NamespaceIsEnabled returns a tri-state boolean indicating if ns enablement is missing, explicitly true, or
	// explicitly false.
	NamespaceIsEnabled(ctx context.Context, ns string) (*bool, error)
	// KnownRevisions returns a list of control plane revisions which are known to the cluster.
	KnownRevisions(ctx context.Context) ([]string, error)
	// ObjectIsMDPEnabled returns a tri-state boolean indicating if ns enablement is missing, explicitly true, or
	// explicitly false.
	ObjectIsMDPEnabled(object client.Object) (*bool, error)
}

const (
	appKey        = "app"
	injectorValue = "sidecar-injector"
)

// NewMapper returns a Mapper for navigating from Pods to Control Plane and Data Plane Revisions.
func NewMapper(client client.Client) Mapper {
	return &naiveCache{
		client:  client,
		lsCache: util.NewLSCache(),
	}
}

// naiveCache is not a cache, but implements the same methods for simplicity.
type naiveCache struct {
	client  client.Client
	lsCache *util.LabelSelectorCache
}

// RevisionForPod implements the Mapper interface.  Returns the revision which will handle injection for the new pod
// when this pod is evicted, deleted, or otherwise killed.  In the event of no revision, that is a pod which is not
// injected by any revision, or of a pod explicitly ignored by MDP, returns empty string.
func (n *naiveCache) RevisionForPod(ctx context.Context, pod *v1.Pod) (string, error) {
	if !mdpControlsPod(pod) || !PodShouldBeInjected(pod) {
		return "", nil
	}
	ns := &v1.Namespace{}
	if err := n.client.Get(ctx, client.ObjectKey{Name: pod.Namespace}, ns); err != nil {
		return "", err
	}
	webhooks := &v1admission.MutatingWebhookConfigurationList{}
	whlabels := map[string]string{appKey: injectorValue}
	if err := n.client.List(ctx, webhooks, client.MatchingLabels(whlabels)); err != nil {
		return "", err
	}
	rs, err := n.controllingReplicaset(ctx, pod)
	// if pod is not controlled by replicaset, we shouldn't manage it.  revision ""
	if rs == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, whc := range webhooks.Items {
		for _, wh := range whc.Webhooks {
			if n.hookMatchesObj(&wh, ns, rs.Spec.Template.Labels) {
				return whc.Labels[name.IstioRevisionLabel], nil
			}
		}
	}
	return "", nil
}

func (n *naiveCache) hookMatchesObj(wh *v1admission.MutatingWebhook, ns *v1.Namespace, podLabels map[string]string) bool {
	var namespaceMatch, objectMatch bool
	namespaceMatch, err := n.lsCache.Matches(wh.NamespaceSelector, ns.Labels)
	if err != nil {
		log.Errorf("Failed to parse label selector %v: %s", wh.NamespaceSelector, err)
		return false
	}
	if !namespaceMatch {
		return false
	}
	objectMatch, err = n.lsCache.Matches(wh.ObjectSelector, podLabels)
	if err != nil {
		log.Errorf("Failed to parse label selector %v: %s", wh.ObjectSelector, err)
		return false
	}
	return objectMatch
}

// PodsFromRevision returns the pods which, if evicted, will be injected by the specified revision.  This is /slightly/
// different from the Pods currently controlled by the revision, as revision labels may change after pod creation.
// There can only be one webhook selecting each revision, unless they have tags.
// Errors encountered when communicating with K8s will be logged, marking the cache as invalid, and returning a nil slice.
func (n *naiveCache) PodsFromRevision(ctx context.Context, rev string) ([]*v1.Pod, error) {
	webhooks := &v1admission.MutatingWebhookConfigurationList{}
	appRequirement, err := labels.NewRequirement(appKey, selection.Equals, []string{injectorValue})
	if err != nil {
		return nil, err
	}
	revisionLabelRequirement, err := labels.NewRequirement(name.IstioRevisionLabel, selection.Equals, []string{rev})
	if err != nil {
		return nil, err
	}
	if err := n.client.List(ctx, webhooks, client.MatchingLabelsSelector{
		Selector: labels.NewSelector().Add(*appRequirement, *revisionLabelRequirement),
	}); err != nil {
		return nil, err
	}
	if len(webhooks.Items) < 1 {
		// TODO: @iamwen alert users to this condition.  Cannot manage dataplane.
		err := fmt.Errorf("no webhooks detected for revision %s, exiting", rev)
		globalerrors.ErrorOnRevision(rev, err)
		return nil, err
	}
	globalerrors.ClearErrorOnRevision(rev)
	result := make([]*v1.Pod, 0)
	for _, wh := range webhooks.Items {
		namespaces := &v1.NamespaceList{}
		if err := n.client.List(ctx, namespaces); err != nil {
			return nil, err
		}
		for _, ns := range namespaces.Items {
			rslist := &appsv1.ReplicaSetList{}
			if err := n.client.List(ctx, rslist, client.InNamespace(ns.Name)); err != nil {
				return nil, err
			}
			for _, rs := range rslist.Items {
				if *rs.Spec.Replicas < 1 {
					// skip empty replicasets
					continue
				}
				for _, hook := range wh.Webhooks {
					if n.hookMatchesObj(&hook, &ns, rs.Spec.Template.Labels) {
						pods := &v1.PodList{}
						sel, ierr := v12.LabelSelectorAsSelector(rs.Spec.Selector)
						if ierr != nil {
							err = multierror.Append(fmt.Errorf("failed to parse label selector %v: %s", rs.Spec.Selector, ierr))
							continue
						}
						if ierr := n.client.List(ctx, pods,
							client.MatchingLabelsSelector{Selector: sel}, client.InNamespace(rs.Namespace)); ierr != nil {
							err = multierror.Append(err, ierr)
							continue
						}
						for i, pod := range pods.Items {
							if mdpControlsPod(&pod) && PodShouldBeInjected(&pod) {
								result = append(result, &pods.Items[i]) // can't use &pod as that pointer is reused on iteration
							}
						}
						break
					}
				}
			}
		}
	}
	return result, err
}

func (n *naiveCache) KnownRevisions(ctx context.Context) ([]string, error) {
	webhooks := &v1admission.MutatingWebhookConfigurationList{}
	whlabels := map[string]string{
		appKey: injectorValue,
	}
	if err := n.client.List(ctx, webhooks, client.MatchingLabels(whlabels)); err != nil {
		return nil, err
	}
	var result []string
	for _, whc := range webhooks.Items {
		result = append(result, whc.Labels[name.IstioRevisionLabel])
	}
	return result, nil
}

// DataPlaneControlFromCPRevision implements Mapper
func (n *naiveCache) DataPlaneControlFromCPRevision(ctx context.Context, rev string) (reconcile.Request, error) {
	mdpcs := &v1alpha1.DataPlaneControlList{}
	if err := n.client.List(ctx, mdpcs); err != nil {
		return reconcile.Request{}, err
	}
	for _, mdpc := range mdpcs.Items {
		if mdpc.Spec.Revision == rev {
			return reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: mdpc.Namespace,
				Name:      mdpc.Name,
			}}, nil
		}
	}
	return reconcile.Request{}, nil
}

func (n *naiveCache) RevisionFromDPRevision(ctx context.Context, req reconcile.Request) (string, error) {
	mdpc := &v1alpha1.DataPlaneControl{}
	if err := n.client.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, mdpc); err != nil {
		return "", err
	}
	return mdpc.Spec.Revision, nil
}

func parseEnabledAnnotation(val string) (*bool, error) {
	var parsed map[string]string
	err := json.Unmarshal([]byte(val), &parsed)
	if err != nil {
		outErr := fmt.Errorf("failed to parse json annotation [%s]: %s", val, err)
		return nil, outErr
	}
	enabled := parsed["managed"]
	result := strings.EqualFold(enabled, "true")
	return &result, nil
}

func (n *naiveCache) ObjectIsMDPEnabled(object client.Object) (*bool, error) {
	if val, ok := object.GetAnnotations()[name.MDPEnabledAnnotation]; ok {
		return parseEnabledAnnotation(val)
	}
	return nil, nil
}

func (n *naiveCache) NamespaceIsEnabled(ctx context.Context, ns string) (*bool, error) {
	namespace := &v1.Namespace{}
	err := n.client.Get(ctx,
		client.ObjectKey{Name: ns},
		namespace)
	if err != nil {
		return nil, err
	}
	return n.ObjectIsMDPEnabled(namespace)
}

func PodShouldBeInjected(pod *v1.Pod) bool {
	// WARNING: this code duplicated from  istio/istio/pkg/kube/inject/inject.go.  Please keep in sync.
	if pod.Spec.HostNetwork {
		return false
	}

	// skip special kubernetes system namespaces
	if inject.IgnoredNamespaces.Contains(pod.Namespace) {
		return false
	}

	annos := pod.GetAnnotations()
	if annos == nil {
		return true
	}

	var inject bool
	switch strings.ToLower(annos[annotation.SidecarInject.Name]) {
	// the pilot code uses meshconfig to understand default injection.
	// since we don't have meshconfig, and mcp requires default injection, we assume true.
	// http://yaml.org/type/bool.html
	case "y", "yes", "true", "on", "":
		inject = true
	}
	return inject
}

// mdpControlsPod returns true if this pod's dataplane may be controlled by MDP.  Currently, this means the pod itself
// has a replicaset controller, but this eligibility check may change in the future.
func mdpControlsPod(pod *v1.Pod) bool {
	ctrl := v12.GetControllerOf(pod)
	if ctrl == nil {
		return false
	}
	// skip pods without existing proxy https://b.corp.google.com/issues/191769400
	if _, hasProxy := util.ProxyVersion(pod); hasProxy {
		return ctrl.Kind == "ReplicaSet"
	}
	return false
}

func (n *naiveCache) controllingReplicaset(ctx context.Context, pod *v1.Pod) (*appsv1.ReplicaSet, error) {
	ctrl := v12.GetControllerOf(pod)
	if ctrl == nil {
		return nil, nil
	}
	rs := &appsv1.ReplicaSet{}
	err := n.client.Get(ctx, client.ObjectKey{Name: ctrl.Name, Namespace: pod.Namespace}, rs)
	if err != nil {
		return nil, err
	}
	return rs, nil
}
