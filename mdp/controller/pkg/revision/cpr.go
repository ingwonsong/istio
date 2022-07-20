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

package revision

import (
	"sync"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
)

// CPR is a handler for ControlPlaneRevisions.
type CPRHandler struct {
	cache    *EnablementCache
	podCache ReadWritePodCache
	mapper   Mapper
}

// NewCPRHandler returns an event handler designed to handle only events for
func NewCPRHandler(mapper Mapper) (*CPRHandler, *EnablementCache) {
	result := &CPRHandler{
		cache: &EnablementCache{
			cache: make(map[string]*bool),
		},
		mapper: mapper,
	}
	return result, result.cache
}

// Create implements EventHandler Interface.
func (c *CPRHandler) Create(event event.CreateEvent, limitingInterface workqueue.RateLimitingInterface) {
	c.updateEnablement(event.Object)
}

// Update Implements EventHandler Interface
func (c *CPRHandler) Update(event event.UpdateEvent, limitingInterface workqueue.RateLimitingInterface) {
	c.updateEnablement(event.ObjectNew)
}

// Delete Implements EventHandler Interface
func (c *CPRHandler) Delete(event event.DeleteEvent, limitingInterface workqueue.RateLimitingInterface) {
	c.cache.UpdateRevisionEnablement(event.Object.GetName(), nil)
}

// Generic Implements EventHandler Interface
func (c *CPRHandler) Generic(event event.GenericEvent, limitingInterface workqueue.RateLimitingInterface) {
}

func (c *CPRHandler) updateEnablement(object client.Object) {
	// typedRevisionEnabled errors on json parsing, which effectively means enablement is not specified
	enabled, _ := c.mapper.ObjectIsMDPEnabled(object.(*v1alpha1.ControlPlaneRevision))
	// Any time CPR enablement is touched, invalidate the entire pod cache.
	// These are rare events so it's not worth checking for state changes, given the ambiguity with using
	// bool pointers for enablement.
	c.invalidatePodCache()
	c.cache.UpdateRevisionEnablement(object.GetName(), enabled)
}

func (c *CPRHandler) SetPodCache(podCache ReadWritePodCache) {
	c.podCache = podCache
}

func (c *CPRHandler) invalidatePodCache() {
	if c.podCache == nil {
		return
	}
	c.podCache.MarkDirty()
}

// EnablementCache caches the enablement of revisions, controlled by configmap, for rapid, frequent acces.
type EnablementCache struct {
	mu    sync.Mutex
	cache map[string]*bool
}

// UpdateRevisionEnablement retrieves the cached enablement value for the specified revision
func (rec *EnablementCache) IsRevisionEnabled(rev string) *bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.cache[rev]
}

// UpdateRevisionEnablement changes the cached enablement value for the specified revision
func (rec *EnablementCache) UpdateRevisionEnablement(rev string, enabled *bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.cache[rev] = enabled
}
