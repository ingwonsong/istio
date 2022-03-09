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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	istioctlcmd "istio.io/istio/istioctl/cmd"
	"istio.io/istio/pilot/cmd/pilot-discovery/app/mcpinit"
	"istio.io/istio/pkg/cmd"
	"istio.io/istio/pkg/config/constants"
	kubelib "istio.io/istio/pkg/kube"
	"istio.io/pkg/log"
	"istio.io/pkg/version"
)

func main() {
	log.EnableKlogWithCobra()
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		log.Error(err)
		os.Exit(-1)
	}
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "mcputils",
		Short:        "MCP Utilities Server",
		SilenceUsage: true,
		PreRunE: func(c *cobra.Command, args []string) error {
			cmd.AddFlags(c)
			return nil
		},
	}

	rootCmd.AddCommand(newMCPServeCommand())
	return rootCmd
}

func newMCPServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Starts the mcp utils server",
		RunE: func(c *cobra.Command, args []string) error {
			params, err := paramsFromEnv()
			if err != nil {
				return err
			}
			log.Info("Initializing mcputils serve")
			mux := http.NewServeMux()
			s := &server{
				project:     params.project,
				kubeconfigs: map[string]bool{},
			}
			mux.Handle("/mcp-install-crds", handler{s.installCRDs})
			mux.Handle("/mcp-run-precheck", handler{s.runPrecheck})
			mux.Handle("/mcp-update-webhooks", handler{s.updateWebhooks})

			addr := fmt.Sprintf(":%s", params.port)
			log.Infof("Listening on: %s", addr)
			return http.ListenAndServe(addr, mux)
		},
	}
}

type handler struct {
	fn func(*http.Request) (string, *httpError)
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Infof("Request: %s %s", r.Method, r.URL)
	message, httpErr := h.fn(r)
	if httpErr != nil {
		http.Error(w, httpErr.message, httpErr.code)
		log.Warnf("Response: %d %s", httpErr.code, httpErr.message)
		return
	}
	log.Infof("Response: 200 %s", message)
	w.Write([]byte(message)) // nolint: errcheck
}

type httpError struct {
	code    int
	message string
}

// Also shared by mcpcrds.
type parameters struct {
	project string
	port    string
}

// nolint: envvarlint
func paramsFromEnv() (parameters, error) {
	p := parameters{}
	p.project = os.Getenv("PROJECT")
	if p.project == "" {
		return parameters{}, fmt.Errorf("PROJECT is a required environment variable")
	}
	p.port = os.Getenv("PORT")
	if p.port == "" {
		p.port = "8080"
	}
	return p, nil
}

type RequestShared struct {
	// Cluster is the name of the cluster.
	Cluster string `json:"cluster"`
	// Location is the location of the cluster.
	Location string `json:"location"`
}

type installCRDsRequest struct {
	RequestShared
	// Version must match the current istiod version. Used as a
	// safeguard against accidentally installing the wrong crds
	// version.
	Version string `json:"version"`
	// Force indicates that the CRDs should be installed without
	// checking to see if the user has any MCP webhooks installed
	// first.
	Force bool `json:"force"`
}

type updateWehooksRequest struct {
	RequestShared
	// Revision signals which webhook/controlplane should be updated
	Revision string `json:"revision"`
}

type server struct {
	project string

	mu          sync.Mutex
	kubeconfigs map[string]bool
}

func (s *server) createKubeClient(ctx context.Context, project, location, cluster string) (kubelib.Client, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kubecfgName := fmt.Sprintf("/tmp/%s-%s-%s-kubeconfig.yaml", project, location, cluster)

	if !s.kubeconfigs[kubecfgName] {
		if err := mcpinit.ConstructKubeConfigFile(ctx, project, location, cluster, kubecfgName); err != nil {
			return nil, "", fmt.Errorf("could not construct kube config: %v", err)
		}
		s.kubeconfigs[kubecfgName] = true
	}

	client, err := mcpinit.CreateKubeClient(kubecfgName, 0, 0)
	if err != nil {
		return nil, "", fmt.Errorf("could not create kube client: %v", err)
	}

	return client, kubecfgName, nil
}

func hasAnyMCPWebhooks(ctx context.Context, client kubelib.Client) bool {
	webhooks := []string{
		"istiod-asm-managed",
		"istiod-asm-managed-rapid",
		"istiod-asm-managed-stable",
	}
	for _, name := range webhooks {
		_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			log.Warnf("Could not access mutating webhook %s: %v", name, err)
			continue
		}
		log.Infof("Mutating webhook exists: %s", name)
		return true
	}
	return false
}

func (s *server) installCRDs(req *http.Request) (string, *httpError) {
	var r installCRDsRequest
	if err := json.NewDecoder(req.Body).Decode(&r); err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: fmt.Sprintf("json decoding error: %v", err),
		}
	}
	log.Infof("Received install crds request: %+v", r)

	client, _, err := s.createKubeClient(req.Context(), s.project, r.Location, r.Cluster)
	if err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: err.Error(),
		}
	}

	if r.Version != version.Info.Version {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: fmt.Sprintf("version does not match istiod version, expected %q", version.Info.Version),
		}
	}
	if !r.Force && !hasAnyMCPWebhooks(req.Context(), client) {
		return "", &httpError{
			code:    http.StatusPreconditionFailed,
			message: "cluster does not have any mcp webhooks installed, not installing CRDs",
		}
	}

	crdTemplate, err := ioutil.ReadFile(mcpinit.GetMCPFile(mcpinit.CRDsFile))
	if err != nil {
		return "", &httpError{
			code:    http.StatusInternalServerError,
			message: fmt.Sprintf("crd file: %v", err),
		}
	}
	if err := mcpinit.CreateCRDs(req.Context(), client, crdTemplate); err != nil {
		return "", &httpError{
			code:    http.StatusInternalServerError,
			message: fmt.Sprintf("creating crds: %v", err),
		}
	}
	return fmt.Sprintf("CRDs installed (version: %s)\n", version.Info.Version), nil
}

func (s *server) fetchWebhookURLForMigration(ctx context.Context, client kubelib.Client, revision string) (string, error) {
	cmAPI := client.CoreV1().ConfigMaps(constants.IstioSystemNamespace)
	name := fmt.Sprintf("env-%s", revision)
	cm, err := cmAPI.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	addr := cm.Data["CLOUDRUN_ADDR"]
	if addr == "" {
		return "", fmt.Errorf("cloudrun_addr does not exist in %s", name)
	}
	// In asm version 1.13+ we indicate whether a webhook should be proxied using
	// the WEBHOOK_PROXY flag set in the config map.
	//
	// Note: VPC-SC uses a webhook proxy prior to 1.13, but VPC-SC webhooks should
	// not be updated in the webhook migration since VPC-SC is an AFC only feature.
	if cm.Data["WEBHOOK_PROXY"] == "true" {
		return fmt.Sprintf("https://meshconfig.googleapis.com/v1alpha1/projects/%s/webhooks/inject/ISTIO_META_CLOUDRUN_ADDR/%s", s.project, addr), nil
	}
	return fmt.Sprintf("https://%s/inject/ISTIO_META_CLOUDRUN_ADDR/%s", addr, addr), nil
}

func (s *server) getAndUpdateWebhook(ctx context.Context, client kubelib.Client, revision string) error {
	mwh, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, fmt.Sprintf("istiod-%s", revision), metav1.GetOptions{})
	if err != nil {
		return err
	}
	// We don't need to update AFC webhooks since AFC already fixes webhooks to
	// use the value set in the configmap.
	if mwh.Labels["istio.io/owned-by"] == "mesh.googleapis.com" {
		log.Infof("webhook is owned by AFC")
		return nil
	}
	webhookURL, err := s.fetchWebhookURLForMigration(ctx, client, revision)
	if err != nil {
		return err
	}
	for i := range mwh.Webhooks {
		webhook := &mwh.Webhooks[i]
		webhook.ClientConfig.URL = &webhookURL
	}
	_, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(ctx, mwh, metav1.UpdateOptions{})
	return err
}

func (s *server) updateWebhooks(req *http.Request) (string, *httpError) {
	var r updateWehooksRequest
	if err := json.NewDecoder(req.Body).Decode(&r); err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: fmt.Sprintf("json decoding error: %v", err),
		}
	}
	log.Infof("Received update webhooks request: %+v", r)

	client, _, err := s.createKubeClient(req.Context(), s.project, r.Location, r.Cluster)
	if err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: err.Error(),
		}
	}

	if err := s.getAndUpdateWebhook(req.Context(), client, r.Revision); err != nil {
		return "", &httpError{
			code:    http.StatusPreconditionFailed,
			message: err.Error(),
		}
	}

	return fmt.Sprintf("Webhooks updated (project: %s, location: %s, cluster: %s, revision: %s)\n", s.project, r.Location, r.Cluster, r.Revision), nil
}

func (s *server) runPrecheck(req *http.Request) (string, *httpError) {
	var r RequestShared
	if err := json.NewDecoder(req.Body).Decode(&r); err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: fmt.Sprintf("json decoding error: %v", err),
		}
	}
	log.Infof("Received precheck request: %v", r)

	_, kubecfg, err := s.createKubeClient(req.Context(), s.project, r.Location, r.Cluster)
	if err != nil {
		return "", &httpError{
			code:    http.StatusBadRequest,
			message: err.Error(),
		}
	}

	o, err := runIstioctl(kubecfg, []string{"x", "precheck"})
	log.Infof("precheck output: %v", o)
	if err != nil {
		return "", &httpError{
			code:    http.StatusPreconditionFailed,
			message: o,
		}
	}
	return o, nil
}

func runIstioctl(kubeconfig string, args []string) (string, error) {
	cmdArgs := append([]string{
		"--kubeconfig",
		kubeconfig,
	}, args...)

	var out strings.Builder
	rootCmd := istioctlcmd.GetRootCmd(cmdArgs)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	fErr := rootCmd.Execute()
	resetLogs()
	return out.String(), fErr
}

func resetLogs() {
	// Istioctl run tampers with log levels, set them all back
	for _, s := range log.Scopes() {
		s.SetOutputLevel(log.InfoLevel)
	}
}
