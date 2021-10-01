/*
 Copyright 2021 The Kubernetes Authors.

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

package revision

import (
	"context"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	rtclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/istio/mdp/controller/pkg/metrics"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/mdp/controller/pkg/set"
	"istio.io/istio/mdp/controller/pkg/util"
	"istio.io/pkg/log"
)

type podEventHandler struct {
	podCache WritePodCache
	mapper   Mapper
}

// PodWorkItem is used as item in the ratelimit queue of update worker
type PodWorkItem struct {
	NamespacedName types.NamespacedName
	// FromVer is the original version of the upgraded proxies, used for reporting upgraded proxies metric
	FromVer string
}

func NewPodWorkItem(name, namespace, fromVersion string) PodWorkItem {
	return PodWorkItem{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		},
		FromVer: fromVersion,
	}
}

// NewPodHandler returns an EventHandler which will enqueue a reconcile request for any pod create or delete of
// MDP managed pods, as well as for updates which change the MDP enrollment or revision of a Pod.
func NewPodHandler(mapper Mapper, podCache WritePodCache) handler.EventHandler {
	return &podEventHandler{
		podCache: podCache,
		mapper:   mapper,
	}
}

// Create implements EventHandler Interface.
func (p *podEventHandler) Create(event event.CreateEvent, q workqueue.RateLimitingInterface) {
	rev := p.podCache.AddPod(event.Object)
	if rev == "" {
		return
	}
	p.enqueueForRev(rev, q)
}

func (p *podEventHandler) enqueueForRev(rev string, q workqueue.RateLimitingInterface) {
	req, err := p.mapper.DataPlaneControlFromCPRevision(context.Background(), rev)
	if err != nil {
		p.podCache.MarkDirty()
		log.Errorf("error retrieving dataplanecontrol: %s", err)
		return
	}
	empty := reconcile.Request{}
	if req == empty {
		log.Infof("Revision %s has no corresponding DataPlaneControl, skipping", rev)
		return
	}
	log.Debugf("Enqueueing change for DPC '%s' from pod event", req.Name)
	q.Add(req)
}

// Update Implements EventHandler Interface
func (p *podEventHandler) Update(event event.UpdateEvent, q workqueue.RateLimitingInterface) {
	oldPod := event.ObjectOld.(*v1.Pod)
	newPod := event.ObjectNew.(*v1.Pod)
	oldver, _ := util.ProxyVersion(oldPod)
	newver, _ := util.ProxyVersion(newPod)
	if oldver == newver && oldPod.Labels[name.IstioRevisionLabel] == newPod.Labels[name.IstioRevisionLabel] {
		return
	}
	oldrev := p.podCache.RemovePod(event.ObjectOld)
	p.enqueueForRev(oldrev, q)
	newrev := p.podCache.AddPod(event.ObjectNew)
	p.enqueueForRev(newrev, q)
}

// Delete Implements EventHandler Interface
func (p *podEventHandler) Delete(event event.DeleteEvent, q workqueue.RateLimitingInterface) {
	rev := p.podCache.RemovePod(event.Object)
	if rev == "" {
		return
	}
	p.enqueueForRev(rev, q)
}

// Generic Implements EventHandler Interface
func (p *podEventHandler) Generic(event event.GenericEvent, q workqueue.RateLimitingInterface) {
}

// ReadPodCache is the (almost) read-only reference to a PodCache, allowing callers to rapidly access the pods
// in each enabled revision, namespace, and version.
type ReadPodCache interface {
	// RemovePodByName removes the named pod from the cache.  This is included to reduce the lag from the time a pod
	// is evicted and the time a delete event is received, which would allow a pod to be evicted twice, resulting
	// in an error.
	RemovePodByName(rev, namespace, version, podname string)
	// GetProxyVersionCount returns a map, keyed by version, with a value being the number of pods using that proxy
	// version for 	the specified revision, as well as an integer indicating the total number of enabled pods in
	// the revision
	GetProxyVersionCount(rev string) (map[string]int, int)
	// GetPodsInRevisionOutOfVersion returns pods that are enabled in the specified revision, but are not running
	// the specified proxy version.  This will be used to choose pods for upgrading.
	GetPodsInRevisionOutOfVersion(rev, version string) set.Set
}

type WritePodCache interface {
	// AddPod adds a pod to the cache.
	AddPod(object rtclient.Object) string
	// RemovePod removes a pod from the cache.
	RemovePod(object rtclient.Object) string
	// RecalculateNamespaceMembers removes all pods who are members of the namespace and specified revision from the
	// cache, then re-adds them.  This is called in response to a change in namespace enablement and revision membership.
	// The resulting value is the list of revisions which are impacted by this change.
	RecalculateNamespaceMembers(ns string, oldrev string, client rtclient.Client) []string
	// MarkDirty indicates that the cache needs to be rebuilt, and may not represent the actual cluster state.
	MarkDirty()
}

type ReadWritePodCache interface {
	ReadPodCache
	WritePodCache
	// Start initializes a goroutine to rebuild the cache from the API server if it is dirty.
	Start(ctx context.Context)
}

type podCache struct {
	state  perRevisionNamespace
	mapper Mapper
	mu     sync.RWMutex
	rec    *EnablementCache

	dirtymu         sync.Mutex
	dirty           bool
	rebuildInterval time.Duration
}

type (
	perVersionPodSet     map[string]*set.Set
	perNamespaceVersion  map[string]perVersionPodSet
	perRevisionNamespace map[string]perNamespaceVersion
)

// NewPodCache
func NewPodCache(mapper Mapper, rec *EnablementCache) ReadWritePodCache {
	return &podCache{
		state:           make(perRevisionNamespace),
		mapper:          mapper,
		rec:             rec,
		rebuildInterval: 5 * time.Minute,
	}
}

// MarkDirty implements WritePodCache
func (p *podCache) MarkDirty() {
	p.dirtymu.Lock()
	defer p.dirtymu.Unlock()
	p.dirty = true
}

// Start implements WritePodCache
func (p *podCache) Start(ctx context.Context) {
	t := time.NewTicker(p.rebuildInterval)
	go func() {
		select {
		case <-t.C:
			p.maybeRebuildCache(ctx)
		case <-ctx.Done():
			t.Stop()
			return
		}
	}()
}

func (p *podCache) maybeRebuildCache(ctx context.Context) {
	p.dirtymu.Lock()
	defer p.dirtymu.Unlock()
	if !p.dirty {
		return
	}
	revisions, err := p.mapper.KnownRevisions(ctx)
	if err != nil {
		log.Errorf("encountered error rebuilding cache, results will be stale: %s", err)
		return
	}
	tempCache := NewPodCache(p.mapper, p.rec)
	for _, rev := range revisions {
		pods, err := p.mapper.PodsFromRevision(ctx, rev)
		if err != nil {
			log.Errorf("encountered error rebuilding cache, results will be stale: %s", err)
			return
		}
		for _, pod := range pods {
			tempCache.AddPod(pod)
		}
	}
	p.state = tempCache.(*podCache).state
	p.dirty = false
}

// AddPod implements WritePodCache
func (p *podCache) AddPod(object rtclient.Object) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addPodUnsafe(object)
}

func (p *podCache) addPodUnsafe(object rtclient.Object) string {
	pod := object.(*v1.Pod)
	rev, err := p.mapper.RevisionForPod(context.Background(), object.(*v1.Pod))
	if err != nil {
		p.MarkDirty()
		log.Errorf("can't identify revision for pod %s: %s", pod.Name, err)
		return ""
	}
	if !p.podIsEnabled(pod, rev) {
		// the cache only cares about managed pods, discard this one.
		return ""
	}
	// get proxy version before locking
	proxyVersion, _ := util.ProxyVersion(pod)
	nsmap, ok := p.state[rev]
	if !ok {
		nsmap = make(perNamespaceVersion)
		p.state[rev] = nsmap
	}
	pvmap, ok := nsmap[pod.Namespace]
	if !ok {
		pvmap = make(perVersionPodSet)
		nsmap[pod.Namespace] = pvmap
	}
	pods, ok := pvmap[proxyVersion]
	if !ok {
		pods = &set.Set{}
		pods.Insert(pod.Name)
		pvmap[proxyVersion] = pods
	} else {
		pods.Insert(pod.Name)
	}
	metrics.ReportProxiesSingleVersion(proxyVersion, rev, pods.Length())
	return rev
}

func (p *podCache) podIsEnabled(pod *v1.Pod, rev string) bool {
	// NamespaceIsEnabled errors on json parsing and kube client errors, which effectively means enablement is not specified
	nse, err := p.mapper.NamespaceIsEnabled(context.Background(), pod.Namespace)
	if err != nil {
		if errors.ReasonForError(err) != v12.StatusReasonUnknown {
			// if we had trouble connecting to k8s, log, mark the cache as dirty, and mark pod as not enabled.  This
			// prevents us from accidentally upgrading namespaces which are disabled.
			log.Errorf("error getting namespace from k8s: %s", err)
			p.MarkDirty()
		} else {
			// If it's not a k8s communication error, it must be json parsing.  log, but don't mark the cache as dirty,
			// since rebuilding won't help.
			log.Errorf("failed to parse annotation %s of namespace %s: %s",
				name.MDPEnabledAnnotation, rtclient.ObjectKeyFromObject(pod).String(), err)
		}
	}
	pe, err := p.mapper.ObjectIsMDPEnabled(pod)
	if err != nil {
		log.Errorf("failed to parse annotation %s of pod %s/%s: %s", name.MDPEnabledAnnotation, pod.Namespace, pod.Name, err)
	}
	re := p.rec.IsRevisionEnabled(rev)
	// enablement preference favors pods, then namespaces, then revisions
	out := prefer(pe, nse, re)
	if out != nil {
		return *out
	}
	// nil is the default, which is false
	return false
}

// prefer returns the first non-nil of the boolean pointers, or nil if all are nil.
func prefer(inputs ...*bool) *bool {
	for i, val := range inputs {
		if val != nil {
			return inputs[i]
		}
	}
	return inputs[len(inputs)-1]
}

// RemovePod implements WritePodCache
func (p *podCache) RemovePod(object rtclient.Object) string {
	pod := object.(*v1.Pod)
	rev, err := p.mapper.RevisionForPod(context.Background(), object.(*v1.Pod))
	if err != nil {
		p.MarkDirty()
		log.Errorf("can't identify revision for pod %s: %s", pod.Name, err)
		return ""
	}
	proxyVersion, _ := util.ProxyVersion(pod)
	p.RemovePodByName(rev, pod.Namespace, proxyVersion, pod.Name)
	return rev
}

// RemovePodByName implements ReadPodCache
func (p *podCache) RemovePodByName(rev, namespace, version, podname string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	nsmap, ok := p.state[rev]
	// TODO: telemetry on cache miss, maybe rebuild cache
	if !ok {
		return
	}
	pvmap, ok := nsmap[namespace]
	if !ok {
		return
	}
	if version == "" {
		// we may not know the version of the pod when removing from the cache, but we can safely remove from all
		// versions since each pod should exist in only one.
		for version, pods := range pvmap {
			pods.Delete(podname)
			// if empty, remove dead branches
			if len(*pods) < 1 {
				delete(pvmap, version)
			}
			metrics.ReportProxiesSingleVersion(version, rev, pods.Length())
		}
	} else {
		pods, ok := pvmap[version]
		if !ok {
			return
		}
		pods.Delete(podname)
		// if empty, remove dead branches
		if len(*pods) < 1 {
			delete(pvmap, version)
		}
		metrics.ReportProxiesSingleVersion(version, rev, pods.Length())
	}
	if len(pvmap) < 1 {
		delete(nsmap, namespace)
	}
	if len(nsmap) < 1 {
		delete(p.state, namespace)
	}
}

// GetProxyVersionCount implements ReadPodCache
func (p *podCache) GetProxyVersionCount(rev string) (map[string]int, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]int)
	total := 0
	nsmap, ok := p.state[rev]
	if !ok {
		return result, total
	}
	for _, pvmap := range nsmap {
		for vers, pods := range pvmap {
			if cur, ok := result[vers]; !ok {
				result[vers] = len(*pods)
			} else {
				result[vers] = cur + len(*pods)
			}
			total += len(*pods)
		}
	}
	return result, total
}

// GetPodsInRevisionOutOfVersion implements ReadPodCache
func (p *podCache) GetPodsInRevisionOutOfVersion(rev, version string) set.Set {
	result := set.Set{}
	p.mu.RLock()
	defer p.mu.RUnlock()
	nsmap, ok := p.state[rev]
	if !ok {
		return result
	}
	for ns, pvmap := range nsmap {
		for vers, pods := range pvmap {
			if vers == version {
				continue
			}
			for pod := range *pods {
				result.Insert(
					PodWorkItem{
						NamespacedName: types.NamespacedName{
							Namespace: ns,
							Name:      pod.(string),
						},
						FromVer: vers,
					})
			}
		}
	}

	return result
}

// RecalculateNamespaceMembers implements WritePodCache
func (p *podCache) RecalculateNamespaceMembers(ns string, oldrev string, client rtclient.Client) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	affectedRev, ok := p.state[oldrev]
	if ok {
		delete(affectedRev, ns)
	}
	// use client to get all pods in namespace
	allPodList := &v1.PodList{}
	err := client.List(context.Background(), allPodList, rtclient.InNamespace(ns))
	if err != nil {
		log.Fatalf(err)
	}
	unique := set.Set{}
	var result []string
	for _, pod := range allPodList.Items {
		rev := p.addPodUnsafe(&pod)
		if !unique.Has(rev) {
			result = append(result, rev)
			unique.Insert(rev)
		}
	}
	return result
}
