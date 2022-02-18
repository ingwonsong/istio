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

package addonmigration

import (
	"testing"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/label"
)

var inst istio.Instance

func TestMain(t *testing.M) {
	// Integration test for testing migration of workloads from Istio on GKE (with citadel) to
	// Google Mesh CA based managed control plane
	framework.NewSuite(t).
		Label(label.CustomSetup).
		Setup(istio.Setup(&inst, nil)).
		Run()
}
