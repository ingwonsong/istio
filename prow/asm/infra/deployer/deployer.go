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

package deployer

import (
	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/deployer/gke"
	"istio.io/istio/prow/asm/infra/deployer/tailorbird"
	"istio.io/istio/prow/asm/infra/types"
)

// Instance of the deployer.
type Instance interface {
	Name() string
	Run() error
}

// New creates a new instance of the deployer Instance.
func New(cfg config.Instance) Instance {
	// GKE-on-GCP cluster with VPC_SC/COMPOSITE_GATEWAY features and
	// topology Multi-project (WIP=HUB) still need to be migrated to Tailorbird.
	// GKE-on-GCP cluster upgrade is not supported by Tailorbird.
	if (cfg.Cluster == types.GKEOnGCP && cfg.Topology == types.MultiProject && cfg.WIP == types.HUB) ||
		(cfg.Cluster == types.GKEOnGCP &&
			(cfg.Features.Has(string(types.VPCServiceControls)) ||
				cfg.Features.Has(string(types.CompositeGateway)) ||
				len(cfg.UpgradeClusterVersion) != 0)) ||
		cfg.Cluster == types.GKEOnGCPWithAutoPilot {
		return gke.NewInstance(cfg)
	}
	return tailorbird.NewInstance(cfg)
}
