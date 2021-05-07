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

package asm

import (
	"istio.io/pkg/env"
)

var (
	cloudRunServiceVar = env.RegisterStringVar("K_SERVICE", "", "cloud run service name")
	enableCloudESFEnv  = env.RegisterBoolVar("ENABLE_CLOUD_ESF", false,
		"If this is set to true, cloudesf based gateway is enabled.").Get()
)

func IsCloudRun() bool {
	if svc := cloudRunServiceVar.Get(); svc != "" {
		return true
	}
	return false
}

func IsCloudESF() bool {
	return enableCloudESFEnv
}
