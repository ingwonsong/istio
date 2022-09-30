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
	"os"

	"cloud.google.com/go/profiler"

	"istio.io/pkg/env"
	"istio.io/pkg/log"
	"istio.io/pkg/version"
)

var (
	cloudRunServiceVar = env.RegisterStringVar("K_SERVICE", "", "cloud run service name")
	enableCloudESFEnv  = env.RegisterBoolVar("ENABLE_CLOUD_ESF", false,
		"If this is set to true, cloudesf based gateway is enabled.").Get()
	enableConnectGateway = env.RegisterBoolVar("ENABLE_CONNECT_GATEWAY", false,
		"If enabled, Connect Gateway will be used to communicate with GKE Private Cluster").Get()
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

func IsConnectGateway() bool {
	return enableConnectGateway
}

var CloudProfilerEnabled = env.RegisterBoolVar("CLOUD_PROFILER_ENABLED", false, "").Get()

func RunCloudProfiler() {
	if !CloudProfilerEnabled {
		return
	}
	cfg := profiler.Config{
		Service:        cloudRunServiceVar.Get(),
		ServiceVersion: version.Info.Version,
		Instance:       os.Getenv("K_REVISION"),
	}
	// Start the profiler. This will run in the background and provides trivial overhead.
	// The profiling logic takes into account how many instances of a cfg.Service+cfg.ServiceVersion we have,
	// and aims to produce 1 profile/minute for this key.
	// In practice this means that each service will have 1 profile per minute unless we roll out a new revision.
	// https://cloud.google.com/profiler/docs/profiling-go#svc-name-and-version
	if err := profiler.Start(cfg); err != nil {
		// Profiling is optional, we should not fail closed.
		log.Errorf("failed to start profiler: %v", err)
	} else {
		log.Infof("profiler started for %v/%v/%v", cfg.Service, cfg.ServiceVersion, cfg.Instance)
	}
}
