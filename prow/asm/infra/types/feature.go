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

// Feature is an enum for supported cluster topologies.
type Feature string

var (
	VPCServiceControls               = addFeature("VPC_SC")
	Addon                            = addFeature("ADDON")
	UserAuth                         = addFeature("USER_AUTH")
	PrivateClusterUnrestrictedAccess = addFeature("PRIVATE_CLUSTER_UNRESTRICTED_ACCESS")
	PrivateClusterLimitedAccess      = addFeature("PRIVATE_CLUSTER_LIMITED_ACCESS")
	PrivateClusterNoAccess           = addFeature("PRIVATE_CLUSTER_NO_ACCESS")
	ContainerNetworkInterface        = addFeature("CNI")
	Autopilot                        = addFeature("AUTOPILOT")

	SupportedFeatures []Feature
)

func addFeature(v Feature) Feature {
	SupportedFeatures = append(SupportedFeatures, v)
	return v
}
