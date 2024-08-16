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
	"strings"
	"time"

	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/slices"
)

var (
	cloudRunServiceVar = env.RegisterStringVar("K_SERVICE", "", "cloud run service name")
	enableCloudESFEnv  = env.RegisterBoolVar("ENABLE_CLOUD_ESF", false,
		"If this is set to true, cloudesf based gateway is enabled.").Get()
	disableInitPhasePrivateClusterIPFallback = env.RegisterBoolVar("DISABLE_INIT_PHASE_PRIVATE_CLUSTER_IP_FALLBACK", false,
		"If disabled, Istiod initialization will return an error if setting up Connect Gateway failed for GKE private cluster.").Get()
	enableRegionalConnectGateway = env.RegisterBoolVar("ENABLE_REGIONAL_CONNECT_GATEWAY", false,
		"If enabled, regional Connect Gateway will be used to communicate with GKE Private Cluster when the membership is regional").Get()
	connectGatewayForPublicCluster = env.RegisterStringVar("CONNECT_GATEWAY_FOR_PUBLIC_CLUSTER", "DISABLED",
		"If enabled, Connect Gateway will be used to communicate with GKE Public Cluster. Values are DISABLED, ENABLED_WITH_FALLBACK, ENABLED_WITHOUT_FALLBACK.").Get() // nolint: lll
	connectGatewayForPublicRemoteCluster = env.RegisterStringVar("CONNECT_GATEWAY_FOR_PUBLIC_REMOTE_CLUSTER", "DISABLED",
		"If enabled, Connect Gateway will be used to communicate with remote clusters for GKE Public Cluster. Values are DISABLED, ENABLED_WITH_FALLBACK, ENABLED_WITHOUT_FALLBACK.").Get() // nolint: lll
)

const (
	CGWForPublicClusterDisabled               = "DISABLED"
	CGWForPublicClusterEnabledWithFallback    = "ENABLED_WITH_FALLBACK"
	CGWForPublicClusterEnabledWithoutFallback = "ENABLED_WITHOUT_FALLBACK"
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

func IsEnableRegionalConnectGateway() bool {
	return enableRegionalConnectGateway
}

func IsInitPhasePrivateClusterIPFallbackDisabled() bool {
	return disableInitPhasePrivateClusterIPFallback
}

func ConnectGatewayForPublicCluster() string {
	return connectGatewayForPublicCluster
}

func ConnectGatewayForPublicRemoteCluster() string {
	return connectGatewayForPublicRemoteCluster
}

// For unit testing only.
func SetConnectGatewayForPublicRemoteCluster(val string) {
	connectGatewayForPublicRemoteCluster = val
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
	GKEHubMembership   string
	CAAddr             string
	CAType             string
	AFCManagedWebhook  bool
	EnableManagedCNI   bool
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
	if p.CloudrunAddr == "" && os.Getenv("PROXY_ENV_ISTIO_META_CONTROL_PLANE") == "" {
		return p, fmt.Errorf("CLOUDRUN_ADDR or PROXY_ENV_ISTIO_META_CONTROL_PLANE is a required environment variable")
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

	var err error
	p.AFCManagedWebhook, err = getBoolEnv("AFC_MANAGED_WEBHOOK")
	if err != nil {
		return p, err
	}
	p.EnableManagedCNI, err = getBoolEnv("ENABLE_MANAGED_CNI")
	if err != nil {
		return p, err
	}
	cgwForPublicClusterStates := []string{
		CGWForPublicClusterDisabled,
		CGWForPublicClusterEnabledWithFallback,
		CGWForPublicClusterEnabledWithoutFallback,
	}
	if !slices.Contains(cgwForPublicClusterStates, ConnectGatewayForPublicCluster()) {
		return p, fmt.Errorf("invalid value for CONNECT_GATEWAY_FOR_PUBLIC_CLUSTER: %s", ConnectGatewayForPublicCluster())
	}
	if !slices.Contains(cgwForPublicClusterStates, ConnectGatewayForPublicRemoteCluster()) {
		return p, fmt.Errorf("invalid value for CONNECT_GATEWAY_FOR_PUBLIC_REMOTE_CLUSTER: %s", ConnectGatewayForPublicRemoteCluster())
	}
	return p, nil
}

// InjectProxyEnvFromIstiodEnv set the proxy environment variables based on Istiod environment variables,
// which has "PROXY_ENV_" prefix. The prefix will be trucated in the proxy environment variable.
func InjectProxyEnvFromIstiodEnv(m map[string]string) {
	const proxyEnvPrefix = "PROXY_ENV_"
	envs := os.Environ()
	for _, e := range envs {
		if strings.HasPrefix(e, proxyEnvPrefix) {
			if key, value, found := strings.Cut(e, "="); found {
				m[strings.TrimPrefix(key, proxyEnvPrefix)] = value
			}
		}
	}
}

func getBoolEnv(env string) (bool, error) {
	val := os.Getenv(env)
	if val == "" {
		return false, nil
	}

	boolVal, err := strconv.ParseBool(val)
	if err != nil {
		return false, fmt.Errorf("parsing %s: %w", env, err)
	}
	return boolVal, nil
}
