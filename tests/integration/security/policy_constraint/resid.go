//go:build integ
// +build integ

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

// User auth tests are purposed for User Auth Service, they are executed only
// if the user auth service installed.
package policyconstaint

import (
	"strings"

	"istio.io/istio/pkg/config"
)

type ResID struct {
	gvk config.GroupVersionKind

	name string

	namespace string
}

const (
	separator = "|"
)

// NewResID creates new resource identifier
func NewResID(
	k config.GroupVersionKind, n string) ResID {
	return ResID{gvk: k, name: n}
}

// String of ResID based on GVK, name and namespace
func (n ResID) String() string {
	return strings.Join(
		[]string{n.gvk.String(), n.name, n.namespace}, separator)
}

// Gvk returns Group/Version/Kind of the resource.
func (n ResID) Gvk() config.GroupVersionKind {
	return n.gvk
}

// Name returns resource name.
func (n ResID) Name() string {
	return n.name
}

// Namespace returns resource namespace.
func (n ResID) Namespace() string {
	return n.namespace
}
