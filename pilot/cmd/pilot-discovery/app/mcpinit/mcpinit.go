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

// Package mcpinit holds functions that are shared by MCP's istiod
// initialization and the mcputils binary.
package mcpinit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	containerpb "google.golang.org/genproto/googleapis/container/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // Import client auth libraries
	"k8s.io/client-go/rest"

	"istio.io/istio/pkg/asm"
	"istio.io/istio/pkg/config/schema/gvk"
	"istio.io/istio/pkg/config/schema/resource"
	kubelib "istio.io/istio/pkg/kube"
	"istio.io/pkg/env"
	"istio.io/pkg/log"
)

var LocalMCP = env.RegisterBoolVar("LOCAL_MCP", false, "If true, use local files and cluster for MCP.").Get()

// KubeConfigParameters represents the set of inputs to construct kubeconfig file.
type KubeConfigParameters struct {
	Project            string
	Location           string
	Cluster            string
	FleetProjectNumber string
	HubMembership      string
	OutputFile         string
}

type gkeHubMembership struct {
	endpoint string
	location string
	name     string
}

// ConstructKubeConfigFile constructs a kubeconfig file that can be
// used to access the cluster referenced by the project/location/cluster.
// This will lookup up the cluster information from the GKE api, and
// construct a "fake" kubeconfig file that will later be read. See
// https://ahmet.im/blog/authenticating-to-gke-without-gcloud/
func ConstructKubeConfigFile(ctx context.Context, p KubeConfigParameters) error {
	if p.Project == "" {
		return fmt.Errorf("project is empty")
	}
	if p.Location == "" {
		return fmt.Errorf("location is empty")
	}
	if p.Cluster == "" {
		return fmt.Errorf("cluster is empty")
	}
	if p.OutputFile == "" {
		return fmt.Errorf("outFile is empty")
	}

	if err := pollIAMPropagation(); err != nil {
		return err
	}
	t0 := time.Now()
	c, err := container.NewClusterManagerClient(ctx, option.WithQuotaProject(p.Project))
	if err != nil {
		return fmt.Errorf("create cluster manager client: %v", err)
	}
	defer c.Close()
	var cl *containerpb.Cluster
	// We add retries to account for IAM propagation delays. Even with pollIAMPropagation, sometimes it doesn't universally apply
	// to downstream services yet, etc, so we need to retry on all calls.
	for attempts := 0; attempts < 50; attempts++ {
		cl, err = c.GetCluster(ctx, &containerpb.GetClusterRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.Project, p.Location, p.Cluster),
		})
		if err == nil {
			break
		}
		log.Warnf("failed to fetch cluster, will retry: %v", err)
		time.Sleep(time.Second)
	}
	if cl == nil {
		return fmt.Errorf("exceeded retry budget fetching cluster: %v", err)
	}
	endpoint := cl.Endpoint
	caCertificate := cl.MasterAuth.ClusterCaCertificate

	if cl.GetPrivateClusterConfig() != nil {
		endpoint = cl.GetPrivateClusterConfig().GetPublicEndpoint()
		// For GKE private clusters, after migration, we rely on Connect Gateway to provide API
		// server access from CloudRun.
		// http://cloud/anthos/multicluster-management/gateway
		if asm.IsConnectGateway() {
			cgwURL, err := connectGatewayURL(ctx, p.FleetProjectNumber, p.HubMembership)
			if err != nil {
				log.Errorf("failed to setup Connect Gateway: %v", err)
			} else {
				endpoint = strings.TrimPrefix(cgwURL, "https://")
				caCertificate = ""
			}
		}
	}
	kubeConfig := fmt.Sprintf(`
apiVersion: v1
kind: Config
current-context: cluster
contexts: [{name: cluster, context: {cluster: cluster, user: user}}]
users: [{name: user, user: {auth-provider: {name: gcp}}}]
clusters:
- name: cluster
  cluster:
    server: "https://%s"
    certificate-authority-data: "%s"
`, endpoint, caCertificate)

	log.Infof("Fetched cluster endpoint in %s: %v", time.Since(t0), endpoint)
	if err := os.WriteFile(p.OutputFile, []byte(kubeConfig), 0o644); err != nil {
		return err
	}
	return nil
}

func connectGatewayURL(ctx context.Context, fleetProjectNum, hubMembership string) (string, error) {
	if fleetProjectNum == "" {
		return "", errors.New("environment variable FLEET_PROJECT_NUMBER is required")
	}
	if hubMembership == "" {
		return "", errors.New("environment variable GKE_HUB_MEMBERSHIP is required")
	}
	components, err := parseGKEHubMembership(hubMembership)
	if err != nil {
		return "", fmt.Errorf("failed to parse GKE Hub membership: %v", err)
	}
	// TODO(ruigu): Obtain fleet project number from CloudResourceManager.
	// nolint: lll
	cgwURL, err := url.JoinPath("https://"+connectGatewayEndpointFromHubEndpoint(components.endpoint), "v1", "projects", fleetProjectNum, "locations", components.location, "gkeMemberships", components.name)
	if err != nil {
		return "", fmt.Errorf("failed to create Connect Gateway URL: %v", err)
	}
	if err := validateCGWAccess(ctx, cgwURL); err != nil {
		return "", fmt.Errorf("failed to validate Connect Gateway URL: %v", err)
	}

	return cgwURL, nil
}

func parseGKEHubMembership(membership string) (*gkeHubMembership, error) {
	u, err := url.Parse(membership)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GKE Hub membership %v: %v", membership, err)
	}

	cgwPathRegex := regexp.MustCompile(`^/projects/([^/]+)/locations/([^/]+)/memberships/([^/]+)$`)
	pathMatches := cgwPathRegex.FindStringSubmatch(u.Path)
	if len(pathMatches) == 0 {
		return nil, fmt.Errorf("cannot parse path regex from path: %s", u.Path)
	}

	return &gkeHubMembership{
		endpoint: u.Host,
		location: pathMatches[2],
		name:     pathMatches[3],
	}, nil
}

func connectGatewayEndpointFromHubEndpoint(hubEndpoint string) string {
	switch {
	case strings.HasPrefix(hubEndpoint, "autopush-"):
		return "autopush-connectgateway.sandbox.googleapis.com"
	case strings.HasPrefix(hubEndpoint, "staging-"):
		return "staging-connectgateway.sandbox.googleapis.com"
	default:
		return "connectgateway.googleapis.com"
	}
}

// pollIAMPropagation waits until the default service account token is available, to workaround
// some IAM propagation delays
func pollIAMPropagation() error {
	if LocalMCP {
		return nil
	}

	timeout := time.Now().Add(time.Minute * 4)
	attempt := 0
	for {
		if time.Now().After(timeout) {
			return fmt.Errorf("timed out waiting for IAM propagation after %v attempts", attempt)
		}
		attempt++
		err := checkIAM()
		if err == nil {
			log.Infof("IAM propagation succeeded after %v attempt", attempt)
			break
		}
		log.Warnf("IAM propagation attempt %v failed: %v", attempt, err)
		time.Sleep(time.Second * 5)
	}
	return nil
}

// validateCGWAccess tests the following:
// 1) If the CGW API is enabled.
// 2) Whether the caller has permission to call CGW and permission to access the API server resource.
// 3) Whether the resource exists.
func validateCGWAccess(ctx context.Context, url string) error {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return err
	}
	// ConnectGateway doesn't support gRPC client.
	resp, err := oauth2.NewClient(ctx, creds.TokenSource).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return googleapi.CheckResponse(resp)
}

func checkIAM() error {
	req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got non 200 status code: %v", resp.StatusCode)
	}
	return nil
}

const (
	MeshTemplateFile      = "mesh_template.yaml"
	ValuesTemplateFile    = "values_template.yaml"
	MutatingWebhookFile   = "mutatingwebhook.yaml"
	CRDsFile              = "gen-istio-cluster.yaml"
	InjectionTemplateFile = "config"

	InjectDir = "./var/lib/istio/inject"
	ConfigDir = "./var/lib/istio/config"
)

// GetMCPFile translates a filename to its source directory. This allows pulling the location from
// source or from the docker image.
//
// NOTE: Make sure the file exists in the image you're using before
// you use it. The mcputils image does not contain all of these files.
func GetMCPFile(name string) string {
	if LocalMCP {
		const outDir = "out/linux_amd64/knative"
		const toolsDir = "tools/packaging/knative"
		const manifestDir = "manifests/charts/base/files"
		switch name {
		case ValuesTemplateFile:
			return filepath.Join(toolsDir, "injection-values.yaml")
		case InjectionTemplateFile:
			return filepath.Join(outDir, "injection-template.yaml")
		case MeshTemplateFile:
			return filepath.Join(toolsDir, name)
		case CRDsFile:
			return filepath.Join(manifestDir, name)
		case MutatingWebhookFile:
			return filepath.Join(outDir, name)
		default:
			panic(fmt.Sprintf("file not found: %v", name))
		}
	}
	switch name {
	case ValuesTemplateFile, InjectionTemplateFile, MutatingWebhookFile:
		return filepath.Join(InjectDir, name)
	case CRDsFile:
		return filepath.Join(ConfigDir, name)
	case MeshTemplateFile:
		return filepath.Join("/etc/istio/config", name)
	default:
		log.Errorf("unknown file name: %v", name)
		// Do not panic here, but we will probably fail later
		return filepath.Join(ConfigDir, name)
	}
}

// CreateKubeClient sets up a simple kube client, following standard
// OSS code. kubeconfig is the path to kubeconfig file, QPS is the qps
// to set for the kubernetes client, and burst is the maximum burst.
// If qps or burst are 0, default values are used.
func CreateKubeClient(kubeconfig string, qps float32, burst int) (kubelib.Client, error) {
	if qps == 0.0 {
		qps = 80.0
	}
	if burst == 0 {
		burst = 160
	}

	// Used by validation
	kubeRestConfig, err := kubelib.DefaultRestConfig(kubeconfig, "", func(config *rest.Config) {
		config.QPS = qps
		config.Burst = burst
	})
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubelib.NewClient(kubelib.NewClientConfigForRestConfig(kubeRestConfig))
	if err != nil {
		return nil, fmt.Errorf("failed creating kube client: %v", err)
	}
	return kubeClient, nil
}

func CreateCRDs(ctx context.Context, client kubelib.Client, template []byte) error {
	reader := bytes.NewReader(template)

	// We store configs as a YaML stream; there may be more than one decoder.
	yamlDecoder := kubeyaml.NewYAMLOrJSONDecoder(reader, 512*1024)
	for {
		obj := &apiextensionsv1.CustomResourceDefinition{}
		err := yamlDecoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if obj == nil {
			continue
		}
		kgvk := obj.GroupVersionKind()
		if resource.FromKubernetesGVK(&kgvk) != gvk.CustomResourceDefinition {
			continue
		}
		_, err = client.Ext().ApiextensionsV1().CustomResourceDefinitions().Create(ctx, obj, metav1.CreateOptions{})
		if IgnoreConflict(err) != nil {
			return fmt.Errorf("failed to create %v: %v", obj.Name, err)
		}
		log.Infof("created CRD %v", obj.Name)
	}
	return nil
}

// IgnoreConflict is a helper to drop conflicts. This ensures that if two instances both attempt to initialize
// a resource, we just ignore the error if we lose the race.
func IgnoreConflict(err error) error {
	if kerrors.IsConflict(err) || kerrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
