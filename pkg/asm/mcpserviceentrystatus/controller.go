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

package mcpserviceentrystatus

import (
	"sync"
	"sync/atomic"

	"sigs.k8s.io/yaml"

	"istio.io/api/meta/v1alpha1"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/model/status"
	statusctl "istio.io/istio/pilot/pkg/status"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/env"
	istiolog "istio.io/istio/pkg/log"
	"istio.io/istio/pkg/maps"
	"istio.io/istio/pkg/slices"
)

const ipAllocationStatusCondName = "AutoAllocatedIPs"

var (
	enableServiceEntryStatusController = env.RegisterBoolVar("ENABLE_SERVICE_ENTRY_STATUS_CONTROLLER", false,
		"If this is true, the status controller for ServiceEntry will be started.").Get()
	enableServiceEntryIPAutoAllocationStatus = env.RegisterBoolVar("ENABLE_SERVICE_ENTRY_IP_AUTO_ALLOCATION_STATUS", false,
		"If this is true, the auto allocated IP address will be written in status field of ServiceEntry. "+
			"If ENABLE_SERVICE_ENTRY_STATUS_CONTROLLER is not true, this flag will be ignored.").Get()
	log = istiolog.RegisterScope("mcpserviceentry", "ServiceEntry registry in MCP")
)

type Controller struct {
	mu          sync.Mutex
	curStatuses map[statusctl.Resource]string

	statusController atomic.Pointer[statusctl.Controller]
	curServices      func() []*model.Service
}

type serviceEntryIP struct {
	IP string `json:"ip"`
}

type ipAllocation struct {
	Hostname string `json:"hostname"`
	// Similar to `podIPs`, let's show the IP addresses as follows:
	// "serviceEntryIPs" comes from "podIPs".
	// serviceEntryIPs:
	// - ip: 1.1.1.1
	// - ip: 2.2.2.2
	ServiceEntryIPs []serviceEntryIP `json:"serviceEntryIPs"`
}

func MaybeNewController() *Controller {
	if !enableServiceEntryStatusController {
		return nil
	}
	return &Controller{
		curStatuses: make(map[statusctl.Resource]string),
	}
}

func (c *Controller) SetServiceGetter(cs func() []*model.Service) {
	c.curServices = cs
}

func setIstioStatus(st *v1alpha1.IstioStatus, context any) *v1alpha1.IstioStatus {
	if st == nil {
		return nil
	}

	if context == nil {
		// If empty status, let's remove the condition itself.
		st.Conditions = removeCondition(st.GetConditions(), ipAllocationStatusCondName)
		return st
	}

	newStatus := context.(string)
	cond := status.GetCondition(st.GetConditions(), ipAllocationStatusCondName)
	if cond == nil {
		cond = &v1alpha1.IstioCondition{
			Type: ipAllocationStatusCondName,
		}
		st.Conditions = append(st.Conditions, cond)
		log.Debugf("Creates a new IstioStatusCondition")
	}
	cond.Status = newStatus
	return st
}

func (c *Controller) SetStatusWrite(enabled bool, statusManager *statusctl.Manager) {
	c.setStatusWrite(enabled, statusManager, true)
}

func (c *Controller) setStatusWrite(enabled bool, statusManager *statusctl.Manager, updateStatus bool) {
	if enabled && statusManager != nil {
		log.Debugf("Starting service entry status writer")
		c.statusController.Store(
			statusManager.CreateIstioStatusController(setIstioStatus),
		)
		if updateStatus {
			c.HandleIPAllocation(c.curServices())
		}
	} else {
		log.Debugf("Stopping service entry status writer")
		c.statusController.Store(nil)
	}
}

// HandleConfig updates the stored current status and keep
func (c *Controller) HandleConfig(config config.Config, cs []*model.Service) {
	if c == nil {
		return
	}
	cond := status.GetConditionFromSpec(config, ipAllocationStatusCondName)
	rsrc := statusctl.ResourceFromModelConfig(config)
	for _, svc := range cs {
		svc.Attributes.MCPServiceEntryRef = toResourceRef(rsrc)
	}

	if cond == nil {
		return
	}

	c.mu.Lock()
	c.curStatuses[rsrc] = cond.Status
	c.mu.Unlock()
}

func (c *Controller) HandleIPAllocation(allServices []*model.Service) bool {
	if c == nil {
		return false
	}
	ipAllocsByResource := make(map[statusctl.Resource]map[string]ipAllocation)
	if enableServiceEntryIPAutoAllocationStatus {
		for _, svc := range allServices {
			rsrc := resourceFromModelService(svc)
			if svc.AutoAllocatedIPv4Address == "" && svc.AutoAllocatedIPv6Address == "" {
				continue
			}

			if ipAllocsByResource[rsrc] == nil {
				ipAllocsByResource[rsrc] = make(map[string]ipAllocation)
			}
			if _, ok := ipAllocsByResource[rsrc][svc.Hostname.String()]; !ok {
				ipAllocsByResource[rsrc][svc.Hostname.String()] = ipAllocation{
					Hostname: svc.Hostname.String(),
					ServiceEntryIPs: []serviceEntryIP{
						{svc.AutoAllocatedIPv4Address},
						{svc.AutoAllocatedIPv6Address},
					},
				}
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var deleted []statusctl.Resource
	// Remove the statuses not observed from the all services.
	for rsrc := range c.curStatuses {
		if _, ok := ipAllocsByResource[rsrc]; !ok {
			delete(c.curStatuses, rsrc)
			deleted = append(deleted, rsrc)
		}
	}

	var enqueued bool
	if statusController := c.statusController.Load(); statusController != nil {
		// Write the status only in the leader.
		for rsrc, ipAllocs := range ipAllocsByResource {
			sorted := slices.SortBy(maps.Values(ipAllocs), func(i ipAllocation) string {
				return i.Hostname
			})
			b, err := yaml.Marshal(sorted)
			if err != nil {
				continue
			}
			newStatus := string(b)
			curStatus, ok := c.curStatuses[rsrc]
			if ok && curStatus == newStatus {
				continue
			}
			c.curStatuses[rsrc] = newStatus
			statusController.EnqueueStatusUpdateResource(newStatus, rsrc)
			enqueued = true
		}

		for _, rsrc := range deleted {
			statusController.EnqueueStatusUpdateResource(nil, rsrc)
			enqueued = true
		}
	}
	return enqueued
}

func removeCondition(conds []*v1alpha1.IstioCondition, condition string) []*v1alpha1.IstioCondition {
	var newConds []*v1alpha1.IstioCondition
	for _, c := range conds {
		if c.Type != condition {
			newConds = append(newConds, c)
		}
	}
	return newConds
}

func resourceFromModelService(svc *model.Service) statusctl.Resource {
	r := svc.Attributes.MCPServiceEntryRef
	return statusctl.Resource{
		GroupVersionResource: r.GroupVersionResource,
		Namespace:            svc.Attributes.Namespace,
		Name:                 r.Name,
		Generation:           r.Generation,
	}
}

func toResourceRef(r statusctl.Resource) model.MCPServiceEntryRef {
	return model.MCPServiceEntryRef{
		GroupVersionResource: r.GroupVersionResource,
		Name:                 r.Name,
		Generation:           r.Generation,
	}
}
