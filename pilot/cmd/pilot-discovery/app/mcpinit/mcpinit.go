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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"google.golang.org/api/option"
	containerpb "google.golang.org/genproto/googleapis/container/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"

	// Import client auth libraries
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"

	"istio.io/istio/pkg/config/schema/gvk"
	"istio.io/istio/pkg/config/schema/resource"
	kubelib "istio.io/istio/pkg/kube"
	"istio.io/pkg/env"
	"istio.io/pkg/log"
)

var LocalMCP = env.RegisterBoolVar("LOCAL_MCP", false, "If true, use local files and cluster for MCP.").Get()

// ConstructKubeConfigFile constructs a kubeconfig file that can be
// used to access the cluster referenced by the project/location/cluster.
// This will lookup up the cluster information from the GKE api, and
// construct a "fake" kubeconfig file that will later be read. See
// https://ahmet.im/blog/authenticating-to-gke-without-gcloud/
func ConstructKubeConfigFile(ctx context.Context, project, location, cluster, outFile string) error {
	if project == "" {
		return fmt.Errorf("project is empty")
	}
	if location == "" {
		return fmt.Errorf("location is empty")
	}
	if cluster == "" {
		return fmt.Errorf("cluster is empty")
	}
	if outFile == "" {
		return fmt.Errorf("outFile is empty")
	}

	if err := pollIAMPropagation(); err != nil {
		return err
	}
	t0 := time.Now()
	c, err := container.NewClusterManagerClient(ctx, option.WithQuotaProject(project))
	if err != nil {
		return fmt.Errorf("create cluster manager client: %v", err)
	}
	defer c.Close()
	var cl *containerpb.Cluster
	// We add retries to account for IAM propagation delays. Even with pollIAMPropagation, sometimes it doesn't universally apply
	// to downstream services yet, etc, so we need to retry on all calls.
	for attempts := 0; attempts < 50; attempts++ {
		cl, err = c.GetCluster(ctx, &containerpb.GetClusterRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, cluster),
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
	publicEndpoint := cl.Endpoint
	// For private cluster with public endpoint disabled, the default API server endpoint
	// is a private IP address.
	// https://cloud.google.com/kubernetes-engine/docs/concepts/private-cluster-concept#overview
	// Replace the default Kubernetes API server endpoint (private endpoint),
	// with its public endpoint.
	if pcc := cl.GetPrivateClusterConfig(); pcc != nil {
		publicEndpoint = pcc.PublicEndpoint
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
`, publicEndpoint, cl.MasterAuth.ClusterCaCertificate)

	log.Infof("Fetched cluster endpoint in %s: %v", time.Since(t0), cl.Endpoint)
	if err := os.WriteFile(outFile, []byte(kubeConfig), 0o644); err != nil {
		return err
	}
	return nil
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
	TelemetrySDFile       = "telemetry-sd.yaml"
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
		case MutatingWebhookFile, TelemetrySDFile:
			return filepath.Join(outDir, name)
		default:
			panic(fmt.Sprintf("file not found: %v", name))
		}
	}
	switch name {
	case ValuesTemplateFile, InjectionTemplateFile, MutatingWebhookFile:
		return filepath.Join(InjectDir, name)
	case TelemetrySDFile, CRDsFile:
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
