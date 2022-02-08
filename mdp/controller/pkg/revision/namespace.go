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

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/pkg/log"
)

type nameSpaceHandler struct {
	podCache WritePodCache
	client   client.Client
	mapper   Mapper
}

func NewNamespaceHandler(cache WritePodCache, client client.Client, mapper Mapper) handler.EventHandler {
	return &nameSpaceHandler{
		podCache: cache,
		client:   client,
		mapper:   mapper,
	}
}

// Create implements EventHandler Interface.
func (n *nameSpaceHandler) Create(event event.CreateEvent, limitingInterface workqueue.RateLimitingInterface) {
}

// Update Implements EventHandler Interface
func (n *nameSpaceHandler) Update(event event.UpdateEvent, limitingInterface workqueue.RateLimitingInterface) {
	// if label controlling revision has changed, need to update cache.
	oldrev := event.ObjectOld.GetLabels()[name.IstioRevisionLabel]
	newrev := event.ObjectNew.GetLabels()[name.IstioRevisionLabel]
	oldann := event.ObjectOld.GetAnnotations()[name.MDPEnabledAnnotation]
	newann := event.ObjectNew.GetAnnotations()[name.MDPEnabledAnnotation]
	if newrev != oldrev || newann != oldann {
		//  all pods from oldrev in namespace should be removed from cache, recalculated
		log.Infof("Namespace %s has moved to revision %s, annotation %s", event.ObjectOld.GetName(), newrev, newann)
		revs := n.podCache.RecalculateNamespaceMembers(event.ObjectOld.GetName(), oldrev, n.client)
		for _, rev := range revs {
			dpc, err := n.mapper.DataPlaneControlFromCPRevision(context.Background(), rev)
			if err != nil {
				log.Errorf("Error retrieving dataplanecontrol: %s", err)
				return
			}
			empty := reconcile.Request{}
			if dpc == empty {
				log.Infof("Revision %s has no corresponding DataPlaneControl, skipping", rev)
				return
			}
			log.Debugf("Enqueueing change for DPC %s from namespace event", dpc.Name)
			limitingInterface.Add(dpc)
		}
	}
}

// Delete Implements EventHandler Interface
func (n *nameSpaceHandler) Delete(event event.DeleteEvent, limitingInterface workqueue.RateLimitingInterface) {
}

// Generic Implements EventHandler Interface
func (n *nameSpaceHandler) Generic(event event.GenericEvent, limitingInterface workqueue.RateLimitingInterface) {
}
