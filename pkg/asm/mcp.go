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
	"fmt"
	"os"
	"strconv"
	"time"

	"istio.io/istio/pkg/env"
)

var (
	cloudRunServiceVar = env.RegisterStringVar("K_SERVICE", "", "cloud run service name")
	enableCloudESFEnv  = env.RegisterBoolVar("ENABLE_CLOUD_ESF", false,
		"If this is set to true, cloudesf based gateway is enabled.").Get()
	enableConnectGateway = env.RegisterBoolVar("ENABLE_CONNECT_GATEWAY", false,
		"If enabled, Connect Gateway will be used to communicate with GKE Private Cluster").Get()
	disableInitPhasePrivateClusterIPFallback = env.RegisterBoolVar("DISABLE_INIT_PHASE_PRIVATE_CLUSTER_IP_FALLBACK", false,
		"If disabled, Istiod initialization will return an error if setting up Connect Gateway failed for GKE private cluster.").Get()
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

func IsInitPhasePrivateClusterIPFallbackDisabled() bool {
	return disableInitPhasePrivateClusterIPFallback
}

// MCPParameters represents the set of inputs from the CloudRun service environment variables
// This is currently configured from google3/cloud/services_platform/thetis/meshconfig/cloudrun.go
type MCPParameters struct {
	Project            string
	ProjectNumber      string
	Zone               string
	Cluster            string
	KRevision          string
	Revision           string
	TrustDomain        string
	PodName            string
	CloudrunAddr       string
	Hub                string
	Tag                string
	XDSAddr            string
	XDSAuthProvider    string
	GKEClusterURL      string
	FleetProjectNumber string
	AFCManagedWebhook  bool
	GKEHubMembership   string
	CAAddr             string
	CAType             string
}

// nolint: golint
func MCPParametersFromEnv() (MCPParameters, error) {
	p := MCPParameters{}
	p.Project = os.Getenv("PROJECT")
	if p.Project == "" {
		return p, fmt.Errorf("PROJECT is a required environment variable")
	}
	p.ProjectNumber = os.Getenv("PROJECT_NUMBER")
	if p.ProjectNumber == "" {
		return p, fmt.Errorf("PROJECT_NUMBER is a required environment variable")
	}
	p.Zone = os.Getenv("ZONE")
	if p.Zone == "" {
		return p, fmt.Errorf("ZONE is a required environment variable")
	}
	p.Cluster = os.Getenv("CLUSTER")
	if p.Cluster == "" {
		return p, fmt.Errorf("CLUSTER is a required environment variable")
	}
	p.KRevision = os.Getenv("K_REVISION")
	if p.KRevision == "" {
		return p, fmt.Errorf("K_REVISION is a required environment variable")
	}
	p.Revision = os.Getenv("REV")
	if p.Revision == "" {
		p.Revision = "asm-managed"
	}
	p.CloudrunAddr = os.Getenv("CLOUDRUN_ADDR")
	if p.CloudrunAddr == "" {
		return p, fmt.Errorf("CLOUDRUN_ADDR is a required environment variable")
	}
	p.XDSAddr = os.Getenv("XDS_ADDR")
	if p.XDSAddr == "" {
		return p, fmt.Errorf("XDS_ADDR is a required environment variable")
	}
	p.XDSAuthProvider = os.Getenv("XDS_AUTH_PROVIDER")
	if p.XDSAuthProvider == "" {
		p.XDSAuthProvider = "gcp"
	}
	// TODO(ruigu): Obtain IdentityProvider on the fly.
	// Currently, Thetis construct IdentityProvider URL and pass it to Istiod through env.
	// It was suggested to directly obtain this info from hub instead of constructing it
	// by ourself.
	p.GKEClusterURL = os.Getenv("GKE_CLUSTER_URL")
	p.FleetProjectNumber = os.Getenv("FLEET_PROJECT_NUMBER")
	// GKE Hub membership full resource name (https://google.aip.dev/122) with owning API prepended.
	// e.g. //gkehub.googleapis.com/project/foo/locations/global/memberships/bar
	p.GKEHubMembership = os.Getenv("GKE_HUB_MEMBERSHIP")
	p.Tag = os.Getenv("TAG")
	p.Hub = os.Getenv("HUB")
	tdProj := os.Getenv("FLEET_PROJECT_ID")
	if tdProj == "" {
		tdProj = p.Project
	}
	p.TrustDomain = fmt.Sprintf("%s.svc.id.goog", tdProj)
	p.PodName = fmt.Sprintf("%s-%d", p.KRevision, time.Now().Nanosecond())
	p.CAAddr = os.Getenv("CAAddr")
	p.CAType = os.Getenv("CA")
	if v := os.Getenv("AFC_MANAGED_WEBHOOK"); v != "" {
		var err error
		p.AFCManagedWebhook, err = strconv.ParseBool(v)
		if err != nil {
			return p, fmt.Errorf("parsing AFC_MANAGED_WEBHOOK: %w", err)
		}
	}
	return p, nil
}
