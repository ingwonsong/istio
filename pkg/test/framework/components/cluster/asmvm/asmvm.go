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

package asmvm

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/api/compute/v1"

	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/test/framework/components/cluster"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/pkg/log"
)

func init() {
	cluster.RegisterFactory(cluster.ASMVM, New)
}

var _ Cluster = &asmvm{}

// New takes a cluster config with the following format:
// kind: ASMVM
// name: name
// primaryClusterName: cn-asm-boskos1-prow-test1-us-central1-a
// meta:
//
//	project: asm-boskos1
//	projectNo: 1234567890
//	gkeLocation: us-central1-a
//	gkeCluster: prow-test1
//	gkeNetwork: prow-test-network
//	firewallTag: prow-to-vms
//	instanceMetadata:
//	- key: gce-service-proxy-agent-bucket
//	  value: gs://storage.googleapis.com/mesh-agent-canary/release.tgz
func New(cfg cluster.Config, topology cluster.Topology) (cluster.Cluster, error) {
	project := cfg.Meta.String("project")
	projectNo := strconv.Itoa(cfg.Meta.Int("projectNumber"))
	gkeLocation := cfg.Meta.String("gkeLocation")
	gkeCluster := cfg.Meta.String("gkeCluster")
	gkeNetwork := cfg.Meta.String("gkeNetwork")
	if project == "" || projectNo == "" || gkeLocation == "" || gkeCluster == "" || gkeNetwork == "" {
		return nil, fmt.Errorf(
			"project (%s), projectNumber (%s), gkeCluster (%s), gkeLocation (%s) and gkeNetwork (%s) must be in metadata",
			project, projectNo, gkeCluster, gkeLocation, gkeNetwork,
		)
	}

	var metadata []string
	for _, entry := range cfg.Meta.Slice("instanceMetadata") {
		k, v := entry.String("key"), entry.String("value")
		if k != "" && v != "" {
			metadata = append(metadata, k+"="+v)
		}
	}

	svc, err := compute.NewService(context.TODO())
	if err != nil {
		return nil, err
	}

	return &asmvm{
		Topology:         topology,
		project:          project,
		projectNo:        projectNo,
		gkeLocation:      gkeLocation,
		gkeCluster:       gkeCluster,
		gkeNetwork:       gkeNetwork,
		firewallTag:      cfg.Meta.String("firewallTag"),
		svc:              svc,
		instanceMetadata: metadata,
	}, nil
}

// Cluster is backed by a GCP project with ASM enabled.
type Cluster interface {
	echo.Cluster
	cluster.Cluster

	// InstanceMetadata includes the custom metadata to be included in the VM
	InstanceMetadata() []string
	// Project containing the cluster to connect to
	Project() string
	// ProjectNumber corresponding to Project
	ProjectNumber() string
	// Zone inferred from GKELocation. If GKELocation isn't specific enough, for example us-central1, "-a" is appended
	// to create a valid zone: us-central1-a.
	Zone() string
	// GKELocation containing the cluster to connect to
	GKELocation() string
	// GKEClusterName is the short name of the cluster (with out the `cn-`) VMs connect to.
	GKEClusterName() string
	// GKENetworkName is the network name VMs connect to
	GKENetworkName() string
	// FirewallTag if specified is used to select the VMs in firewall policies so test infra can access them.
	FirewallTag() string
	// Prefix URL for API objects in the format https://compute.googleapis.com/compute/v1/projects/{project}
	Prefix() string
	// Service gives a Google cloud compute service client
	Service() *compute.Service
}

type asmvm struct {
	kube.CLIClient
	cluster.Topology

	project     string
	projectNo   string
	gkeLocation string
	gkeCluster  string
	gkeNetwork  string
	firewallTag string

	instanceMetadata []string

	svc *compute.Service
}

func (a *asmvm) InstanceMetadata() []string {
	return a.instanceMetadata
}

func (a *asmvm) Service() *compute.Service {
	return a.svc
}

func (a *asmvm) Prefix() string {
	return "https://www.googleapis.com/compute/v1/projects/" + a.Project()
}

func (a *asmvm) Project() string {
	return a.project
}

func (a *asmvm) ProjectNumber() string {
	return a.projectNo
}

func (a *asmvm) Zone() string {
	match, err := regexp.Match(`^\w+-\w+$`, []byte(a.GKELocation()))
	if err != nil {
		log.Errorf("failed using regex: %v", err)
	}
	if match {
		return a.GKELocation() + "-a"
	}
	return a.GKELocation()
}

func (a *asmvm) GKELocation() string {
	return a.gkeLocation
}

func (a *asmvm) GKEClusterName() string {
	return a.gkeCluster
}

func (a *asmvm) GKENetworkName() string {
	return a.gkeNetwork
}

func (a *asmvm) FirewallTag() string {
	return a.firewallTag
}

func (a *asmvm) CanDeploy(config echo.Config) (echo.Config, bool) {
	if !config.DeployAsVM {
		return echo.Config{}, false
	}
	return config, true
}

func (a *asmvm) String() string {
	buf := &bytes.Buffer{}
	_, _ = fmt.Fprint(buf, a.Topology.String())
	_, _ = fmt.Fprintf(buf, "Project:            %s\n", a.Project())
	_, _ = fmt.Fprintf(buf, "ProjectNumber       %s\n", a.ProjectNumber())
	_, _ = fmt.Fprintf(buf, "GKELocation:        %s\n", a.GKELocation())
	_, _ = fmt.Fprintf(buf, "GKECluster:         %s\n", a.GKEClusterName())
	_, _ = fmt.Fprintf(buf, "GKENetwork:         %s\n", a.GKENetworkName())
	_, _ = fmt.Fprintf(buf, "FirewallTag:        %s\n", a.FirewallTag())
	_, _ = fmt.Fprintf(buf, "InstanceMetadata:   %s\n", strings.Join(a.InstanceMetadata(), " "))
	return buf.String()
}