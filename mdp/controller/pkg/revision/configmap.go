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
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type configMapHandler struct {
	cache  *EnablementCache
	mapper Mapper
}

// NewConfigMapHandler returns an event handler designed to handle only events for
func NewConfigMapHandler(mapper Mapper) (handler.EventHandler, *EnablementCache) {
	result := &configMapHandler{
		cache: &EnablementCache{
			cache: make(map[string]*bool),
		},
		mapper: mapper,
	}
	return result, result.cache
}

// Create implements EventHandler Interface.
func (c *configMapHandler) Create(event event.CreateEvent, limitingInterface workqueue.RateLimitingInterface) {
	c.updateEnablement(event.Object)
}

// Update Implements EventHandler Interface
func (c *configMapHandler) Update(event event.UpdateEvent, limitingInterface workqueue.RateLimitingInterface) {
	c.updateEnablement(event.ObjectNew)
}

// Delete Implements EventHandler Interface
func (c *configMapHandler) Delete(event event.DeleteEvent, limitingInterface workqueue.RateLimitingInterface) {
	rev := getRevisionNameFromCMName(event.Object.GetName())
	c.cache.UpdateRevisionEnablement(rev, nil)
}

// Generic Implements EventHandler Interface
func (c *configMapHandler) Generic(event event.GenericEvent, limitingInterface workqueue.RateLimitingInterface) {
}

func (c *configMapHandler) updateEnablement(object client.Object) {
	rev := getRevisionNameFromCMName(object.GetName())
	// typedRevisionEnabled errors on json parsing, which effectively means enablement is not specified
	enabled, _ := c.mapper.ObjectIsMDPEnabled(object.(*v1.ConfigMap))
	c.cache.UpdateRevisionEnablement(rev, enabled)
}

func getRevisionNameFromCMName(cmName string) string {
	parts := strings.Split(cmName, "-")
	return parts[len(parts)-1]
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
