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

// This file describes the abstract model of services (and their instances) as
// represented in Istio. This model is independent of the underlying platform
// (Kubernetes, Mesos, etc.). Platform specific adapters found populate the
// model object with various fields, from the metadata found in the platform.
// The platform independent proxy code uses the representation in the model to
// generate the configuration files for the Layer 7 proxy sidecar. The proxy
// code is specific to individual proxy implementations

package model

import "k8s.io/apimachinery/pkg/runtime/schema"

// Reference for a ServiceEntry which was a source of model.Service.
// This will be used for generating status.Resource which is a descriptor of
// a resource in the status controller.
type MCPServiceEntryRef struct {
	// GVR of the service entry.
	schema.GroupVersionResource
	// Name is a name of service entry.
	Name string
	// Generation which of a resource was used for generating model.Service.
	Generation string
}
