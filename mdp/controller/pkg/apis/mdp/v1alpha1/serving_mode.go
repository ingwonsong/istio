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

package v1alpha1

// NOTE: this package is copied from cloud/services_platform/thetis/meshconfig/proto/serving_mode.proto

// If SERVICE_MODE_TD, GSM-TD serves.
// Otherwise GSM-istiod serves.
type ServingMode int32

const (
	// unspecified, GSM-istiod serves as the control plane.
	//nolint: all
	ServingMode_SERVING_MODE_UNSPECIFIED ServingMode = 0
	// GSM-istiod serves as the control plane.
	//nolint: all
	ServingMode_SERVING_MODE_ISTIOD ServingMode = 1
	// GSM-istiod serves as the control plane, but LRS is enabled.
	//nolint: all
	ServingMode_SERVING_MODE_ISTIOD_WITH_LRS ServingMode = 3
	// GSM-TD serves as the control plane.
	//nolint: all
	ServingMode_SERVING_MODE_TD ServingMode = 2
)
