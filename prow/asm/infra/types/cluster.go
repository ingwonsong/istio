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

package types

// Cluster is an enum for supported clusters.
type Cluster string

var (
	GKEOnGCP                      = addCluster("gke")
	GKEOnGCPWithAutoPilot         = addCluster("gke-autopilot")
	GKEOnGCPWithAnthosPrivateMode = addCluster("apm")
	GKEOnPrem                     = addCluster("gke-on-prem")
	GKEOnAWS                      = addCluster("aws")
	GKEOnEKS                      = addCluster("eks")
	GKEOnAKS                      = addCluster("aks")
	GKEOnBareMetal                = addCluster("bare-metal")
	HybridGKEAndGKEOnBareMetal    = addCluster("hybrid-gke-and-bare-metal")
	HybridGKEAndEKS               = addCluster("hybrid-gke-and-eks")

	SupportedClusters []Cluster
)

func addCluster(t Cluster) Cluster {
	SupportedClusters = append(SupportedClusters, t)
	return t
}
