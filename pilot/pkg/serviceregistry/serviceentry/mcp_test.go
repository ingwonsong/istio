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

package serviceentry

import (
	"testing"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config"
)

type mockMCPStatusController struct {
	setServiceGetterCallCount   int
	handleConfigCallCount       int
	handleIPAllocationCallCount int
}

var _ MCPServiceStatusController = &mockMCPStatusController{}

func (c *mockMCPStatusController) SetServiceGetter(cs func() []*model.Service) {
	c.setServiceGetterCallCount++
}

// HandleConfig updates the stored current status and keep
func (c *mockMCPStatusController) HandleConfig(config config.Config, cs []*model.Service) {
	c.handleConfigCallCount++
}

func (c *mockMCPStatusController) HandleIPAllocation(allServices []*model.Service) bool {
	c.handleIPAllocationCallCount++
	return false
}

func TestWithMCPServiceEntryStatusController(t *testing.T) {
	mockStatusController := &mockMCPStatusController{}
	seController := &Controller{}
	WithMCPServiceEntryStatusController(mockStatusController)(seController)
	if mockStatusController.setServiceGetterCallCount != 1 {
		t.Errorf("WithMCPServiceEntryStatusController did not call SetServiceGetter")
	}
	if seController.statusController != mockStatusController {
		t.Errorf("WithMCPServiceEntryStatusController did not set the status controller to serviceentry.Controller.statusController")
	}
}

func TestIntegration(t *testing.T) {
	mockStatusController := &mockMCPStatusController{}
	store, sd, _ := initServiceDiscoveryWithOpts(t, false, WithMCPServiceEntryStatusController(mockStatusController))
	inputServiceEntries := []*config.Config{httpDNS, httpDNSRR, tcpStatic}
	createConfigs(inputServiceEntries, store, t)
	_ = sd.Services()
	if mockStatusController.handleConfigCallCount != len(inputServiceEntries) {
		t.Errorf("HandleConfig was called %d times, but want %d", mockStatusController.handleConfigCallCount, len(inputServiceEntries))
	}

	if mockStatusController.handleIPAllocationCallCount != 1 {
		t.Errorf("HandleIPAllocation was called %d times, but want %d", mockStatusController.handleConfigCallCount, 1)
	}
}
