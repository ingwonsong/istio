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

package app

// nolint: envvarlint
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/spf13/cobra"
	"google.golang.org/api/cloudresourcemanager/v1"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // Import client auth libraries
	"sigs.k8s.io/yaml"

	"istio.io/istio/pilot/cmd/pilot-discovery/app/mcpinit"
	"istio.io/istio/pilot/pkg/bootstrap"
	"istio.io/istio/pilot/pkg/features"
	"istio.io/istio/pilot/pkg/gcpmonitoring"
	"istio.io/istio/pilot/pkg/xds"
	"istio.io/istio/pkg/asm"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/cmd"
	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/file"
	kubelib "istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/security"
	"istio.io/pkg/log"
)

// newMCPCommand provides a custom entrypoint to the standard discovery command used by in-cluster Istiod.
// This is mostly just setting up various configuration options, then starting Istiod like normal, replacing the old istiod-gcp.sh script.
// For now, it also sets up parts of the cluster when needed (configmaps, webhooks, etc). In the future, scriptaro or AFC will do this.
// To run locally, using the current kubeconfig cluster: `LOCAL_MCP=true go run ./pilot/cmd/pilot-discovery mcp`
func newMCPCommand() *cobra.Command {
	var mcpParams MCPParameters
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP control plane.",
		Args:  cobra.ExactArgs(0),
		PreRunE: func(c *cobra.Command, args []string) error {
			// Configure local environment when LOCAL_MCP is enable, for local development
			setupLocalEnv()
			var err error

			// Read core MCP configuration. This is generally set in the KService, which in turn is configured by Thetis.
			mcpParams, err = MCPParametersFromEnv()
			if err != nil {
				return err
			}

			// For MCP we have custom logging to be compatible with SD and tee to the consumer project's logs
			if err := configureMCPLogs(mcpParams, loggingOptions); err != nil {
				return err
			}

			// Complete args, matching OSS
			if err := validateFlags(serverArgs); err != nil {
				return err
			}
			if err := serverArgs.Complete(); err != nil {
				return err
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			client, err := initializeMCP(mcpParams)
			if err != nil {
				return fmt.Errorf("initialize MCP: %v", err)
			}

			// Create the stop channel for all of the servers.
			stop := make(chan struct{})

			// Create the server for the discovery service. This is the same as the standard OSS code, except we
			// already have a kube client initialized, so we pre-set that to avoid creating two clients.
			discoveryServer, err := bootstrap.NewServer(serverArgs, bootstrap.SetKubeClient(client))
			if err != nil {
				return fmt.Errorf("failed to create discovery service: %v", err)
			}

			// Start the server
			if err := discoveryServer.Start(stop); err != nil {
				return fmt.Errorf("failed to start discovery service: %v", err)
			}
			cmd.WaitSignal(stop)
			// Wait until we shut down. In theory this could block forever; in practice we will get
			// forcibly shut down after 30s in Kubernetes.
			discoveryServer.WaitUntilCompletion()
			return nil
		},
	}
}

func generateTemplateParameters(p MCPParameters, options AsmOptions, client kubelib.Client) (TemplateParameters, error) {
	cniEnabled, err := getCniEnabled(options, client)
	if err != nil {
		return TemplateParameters{}, err
	}
	templateParams := TemplateParameters{
		MCPParameters:           p,
		CNIEnabled:              cniEnabled,
		ProxyResourceParameters: createProxyParameters(client),
	}
	if options.CAOptions.CAType == PrivatecaOption {
		templateParams.CA = string(PrivatecaOption)
		templateParams.CATrustAnchor = options.CAOptions.TrustAnchorLoc
		templateParams.CAAddress = options.CAOptions.CAAddr
	} else if options.CAOptions.CAType == ManagedCaOption {
		templateParams.CA = security.GkeWorkloadCertificateProvider
	} else {
		templateParams.CA = string(MeshcaOption)
		templateParams.CAAddress = "meshca.googleapis.com:443"
		templateParams.CATrustAnchor = ""
	}
	return templateParams, nil
}

func initializeMCP(p MCPParameters) (kubelib.Client, error) {
	t0 := time.Now()
	defer func() {
		log.Infof("MCP initialization complete in %v for options %+v", time.Since(t0), p)
	}()

	asm.RunCloudProfiler()

	log.Infof("Initializing MCP with options %+v", p)

	if err := mcpinit.ConstructKubeConfigFile(context.Background(), p.Project, p.Zone, p.Cluster, "/tmp/kubeconfig.yaml"); err != nil {
		return nil, fmt.Errorf("construct kube config: %v", err)
	}
	// Configure Istiod to read or configured kubeconfig file
	serverArgs.RegistryOptions.KubeConfig = "/tmp/kubeconfig.yaml"

	// Setup kube client. We will use it to do some MCP specific initialization, then later pass the
	// same client to the main server to avoid re-initializing.
	ko := serverArgs.RegistryOptions.KubeOptions
	client, err := mcpinit.CreateKubeClient("/tmp/kubeconfig.yaml", ko.KubernetesAPIQPS, ko.KubernetesAPIBurst)
	if err != nil {
		return nil, err
	}

	asmOptsStartTime := time.Now()
	asmOptions, err := fetchAsmOptions(client)
	if err != nil {
		return nil, err
	}
	log.Infof("Fetched ASM options in %v: %+v", time.Since(asmOptsStartTime), asmOptions)

	setupInjectEnvironment(client)

	// We multiplex on a single port, so disable everything else
	serverArgs.ServerOptions.HTTPSAddr = ""
	serverArgs.ServerOptions.SecureGRPCAddr = ""
	serverArgs.ServerOptions.MonitoringAddr = ""
	serverArgs.ServerOptions.GRPCAddr = ""

	// Disable webhook config patching - manual configs used, proper DNS certs means no cert patching needed.
	// TODO: oss bug, cannot disable validation
	features.EnableRemoteJwks = true
	features.InjectionWebhookConfigName = ""
	bootstrap.Revision = p.Revision
	bootstrap.PodName = p.PodName
	features.ClusterName = fmt.Sprintf("cn-%s-%s-%s", p.Project, p.Zone, p.Cluster)
	serverArgs.RegistryOptions.KubeOptions.ClusterID = cluster.ID(features.ClusterName)
	features.SharedMeshConfig = fmt.Sprintf("istio-%s", p.Revision)

	// In cloudrun we have relatively small memory limits. To avoid OOM, we drop the OSS defaults for rate
	// limit/throttle to avoid too many concurrent pushes.
	// If they are explicitly configured, do not override the settings. This allows overriding outside the code.
	if _, f := os.LookupEnv("PILOT_MAX_REQUESTS_PER_SECOND"); !f {
		features.RequestLimit = 25
	}
	if _, f := os.LookupEnv("PILOT_PUSH_THROTTLE"); !f {
		features.PushThrottle = 25
	}

	features.WorkloadEntryCrossCluster = true
	features.PilotCertProvider = constants.CertProviderNone
	features.MultiRootMesh = true
	security.TokenAudiences = []string{p.TrustDomain, "istio-ca"}

	// Re-apply defaults since we have changed some options
	serverArgs.ApplyDefaults()

	mcpEnv := "prod"
	if v, f := os.LookupEnv("MCP_ENV"); f {
		mcpEnv = v
	}
	if bootstrap.JwtRule == "" {
		bootstrap.JwtRule = fmt.Sprintf(`{
	"audiences":["%s:%s"],
  "issuer":"cloud-services-platform-thetis@system.gserviceaccount.com",
  "jwks_uri":"https://www.googleapis.com/service_accounts/v1/metadata/jwk/cloud-services-platform-thetis@system.gserviceaccount.com"
}`, mcpEnv, p.TrustDomain)
	}
	xds.AuthPlaintext = true

	// Old script allowed detecting Mesh CA vs Citadel; since we don't plan to do that any longer we only do mesh ca
	features.EnableCAServer = false

	templateParams, err := generateTemplateParameters(p, asmOptions, client)
	if err != nil {
		return nil, err
	}
	createConfig := time.Now()
	if err := createSystemNamespace(client); err != nil {
		return nil, fmt.Errorf("create namespace: %v", err)
	}
	// We do not use in cluster mesh config, instead use a file. With SharedMeshConfig users can create
	// a configmap in cluster that we merge with.
	if err := executeTemplateTo(mcpinit.GetMCPFile(mcpinit.MeshTemplateFile), "./etc/istio/config/mesh", templateParams); err != nil {
		return nil, fmt.Errorf("write mesh config: %v", err)
	}
	// Same as mesh config - nothing in cluster. We do not support any injection customizations.
	if err := executeTemplateTo(mcpinit.GetMCPFile(mcpinit.ValuesTemplateFile), filepath.Join(mcpinit.InjectDir, "values"), templateParams); err != nil {
		return nil, fmt.Errorf("write injection values: %v", err)
	}
	if err := file.AtomicCopy(mcpinit.GetMCPFile(mcpinit.InjectionTemplateFile), mcpinit.InjectDir, "config"); err != nil {
		return nil, fmt.Errorf("write injection config template: %v", err)
	}

	// Create a tag-specific configmap, including the settings. This is intended for install_asm and tools.
	// Managed dataplane uses `TAG` to determine if the running version of a proxy matches
	// the configured version (TAG) for the specific revision.
	// Addon migration uses TRUST_DOMAIN to determine the trust domain to configure citadel to trust
	envProvisioned, err := createConfigmap(client, fmt.Sprintf("env-%s", p.Revision), map[string]string{
		"CLOUDRUN_ADDR": p.CloudrunAddr,
		"TAG":           p.Tag,
		"TRUST_DOMAIN":  p.TrustDomain,
	}, true)
	if err != nil {
		return nil, fmt.Errorf("create env configmap: %v", err)
	}

	if !p.AFCManagedWebhook {
		mwh, err := executeTemplate(mcpinit.GetMCPFile(mcpinit.MutatingWebhookFile), templateParams)
		if err != nil {
			return nil, fmt.Errorf("mutating webhook template: %v", err)
		}
		if err := createOrSetWebhook(client, mwh); err != nil {
			return nil, fmt.Errorf("create webhook: %v", err)
		}
	}

	// This was our first time provisioning the environment, so we also need to write configmaps
	if envProvisioned {
		crdTemplate, err := os.ReadFile(mcpinit.GetMCPFile(mcpinit.CRDsFile))
		if err != nil {
			return nil, fmt.Errorf("crd file: %v", err)
		}
		// Write to cluster for users to view, typically with old kube-inject
		if err := mcpinit.CreateCRDs(context.Background(), client, crdTemplate); err != nil {
			return nil, fmt.Errorf("create crd: %v", err)
		}

		// Provision an empty stub user meshconfig. This just gives the users an indication they can edit this file;
		// It has no runtime impact until they add some real settings.
		if _, err := createConfigmap(client, fmt.Sprintf("istio-%s", p.Revision), map[string]string{
			"mesh": `
# This section can be updated with user configuration settings from https://istio.io/latest/docs/reference/config/istio.mesh.v1alpha1/
# Some options required for ASM to not be modified will be ignored`,
		}, false); err != nil {
			return nil, fmt.Errorf("create mesh configmap: %v", err)
		}

	}

	log.Infof("Created MCP configurations in %v", time.Since(createConfig))

	return client, nil
}

func setupInjectEnvironment(client kubelib.Client) {
	// For https://github.com/istio/istio/issues/26882. Basically, older Kubernetes versions we needed
	// to set the pod securityContext.fsGroup to read the projected JWT token. This also breaks some customer
	// volume mounts though. In Kubernetes 1.19+, this is no longer required. In OSS, this is determined
	// at install time; in this case we need to do it at runtime.
	// NOTE: The 1.19 version dependency is on the *node* versions. We don't have a reasonable way to check
	// the node version. Instead, we can check the control plane version, and assume based on the
	// supported version skew (https://cloud.google.com/kubernetes-engine/docs/how-to/upgrading-a-cluster#upgrade_nodes)
	// that their nodes are recent enough as well.
	if kubelib.IsAtLeastVersion(client, 21) {
		// This needs to literally set the env, not just a local go variable, because the injection template
		// is looking for `env "ENABLE_LEGACY_FSGROUP_INJECTION" "true"`.
		os.Setenv("ENABLE_LEGACY_FSGROUP_INJECTION", "false")
	}
}

func createSystemNamespace(client kubelib.Client) error {
	_, err := client.Kube().CoreV1().Namespaces().Get(context.Background(), constants.IstioSystemNamespace, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		log.Infof("namespace %q not found, creating it now", constants.IstioSystemNamespace)
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: constants.IstioSystemNamespace,
			},
		}
		_, err := client.Kube().CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		return mcpinit.IgnoreConflict(err)
	}
	return err
}

// setupLocalEnv sets some fake values for environment variables so 10s of variables are not required to get running
// This allows running like `LOCAL_MCP=true ./out/linux_amd64/pilot-discovery mcp` without any other parameters
func setupLocalEnv() {
	if !mcpinit.LocalMCP {
		return
	}

	log.Infof("Local run detected, filling in missing environment variables")

	// Just set dummy values, this only impacts injection which is probably not used for local run.
	// If they are really needed they can be overridden
	setEnvIfUnset("CLOUDRUN_ADDR", "fake-local-run")
	setEnvIfUnset("K_REVISION", "fake-local-run")
	setEnvIfUnset("K_SERVICE", "fake-local-run")
	setEnvIfUnset("XDS_ADDR", "fake-local-run:80")
	// Avoid JSON logs, as they are harder to read locally
	setEnvIfUnset("USE_STACKDRIVER_LOGGING_FORMAT", "false")

	// Auto detect project/cluster/local variables from kubeconfig if not set
	parseLocalClusterName()
}

func setEnvIfUnset(k, v string) {
	if _, f := os.LookupEnv(k); !f {
		if err := os.Setenv(k, v); err != nil {
			log.Error(err)
		} else {
			log.Infof("set env var %v=%v", k, v)
		}
	}
}

// parseLocalClusterName sets up environment variables based on the current kubeconfig.
// This is meant for local development only.
func parseLocalClusterName() {
	cc, err := kubelib.BuildClientCmd("", "").RawConfig()
	if err != nil {
		log.Warnf("failed to setup local project variables: %v", err)
		return
	}
	c := cc.Contexts[cc.CurrentContext].Cluster
	// If this isn't a gke cluster, skip it
	if !strings.HasPrefix(c, "gke_") {
		return
	}
	// Otherwise, gke clusters have a standardized format we can parse to get project/zone/cluster name
	parts := strings.Split(c, "_")
	if len(parts) != 4 {
		return
	}
	setEnvIfUnset("PROJECT", parts[1])
	setEnvIfUnset("ZONE", parts[2])
	setEnvIfUnset("CLUSTER", parts[3])
	// we are missing PROJECT_NUMBER, but we can fetch it from the API if not set
	if _, f := os.LookupEnv("PROJECT_NUMBER"); !f {
		cloudresourcemanagerService, err := cloudresourcemanager.NewService(context.Background())
		if err != nil {
			log.Warnf("failed to start cloud resource manager for local run: %v", err)
			return
		}
		res, err := cloudresourcemanagerService.Projects.Get(parts[1]).Do()
		if err != nil {
			log.Warnf("failed to fetch project number for local run: %v", err)
			return
		}
		setEnvIfUnset("PROJECT_NUMBER", fmt.Sprint(res.ProjectNumber))
	}
}

// createOrSetWebhook setups up the mutating webhook configuration
func createOrSetWebhook(client kubelib.Client, mwh string) error {
	mwc := &admissionv1.MutatingWebhookConfiguration{}
	if err := yaml.Unmarshal([]byte(mwh), mwc); err != nil {
		return err
	}

	_, err := client.Kube().AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), mwc.Name, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		log.Infof("mutating webhook not found, creating it now")
		_, err := client.Kube().AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.Background(), mwc, metav1.CreateOptions{})
		return mcpinit.IgnoreConflict(err)
	}
	if err != nil {
		return err
	}
	log.Infof("mutating webhook already exists, skipping creation")
	return nil
}

// createConfigmap creates a configmap in the system namespace
// Returns true if the configmap had to be created, false if the configmap exists.
func createConfigmap(client kubelib.Client, name string, data map[string]string, update bool) (bool, error) {
	cmAPI := client.Kube().CoreV1().ConfigMaps(constants.IstioSystemNamespace)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: constants.IstioSystemNamespace,
		},
		Data: data,
	}

	_, err := cmAPI.Get(context.Background(), name, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		log.Infof("configmap %q not found, creating it now", name)
		_, err := cmAPI.Create(context.Background(), cm, metav1.CreateOptions{})
		return true, mcpinit.IgnoreConflict(err)
	}

	if err != nil {
		return false, err
	}

	if !update {
		log.Infof("configmap %q already exists, skipping creation", name)
		return false, nil
	}

	_, err = cmAPI.Update(context.Background(), cm, metav1.UpdateOptions{})
	if err == nil {
		log.Infof("configmap %q updated", name)
	}

	return false, mcpinit.IgnoreConflict(err)
}

func getCniEnabled(options AsmOptions, client kubelib.Client) (bool, error) {
	switch options.CNI {
	case CheckOption:
		// If we are in check mode, look to see if there is any CNI pods. If there is, we will enable CNI
		pl, err := client.Kube().CoreV1().Pods(constants.KubeSystemNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "k8s-app=istio-cni-node",
			Limit:         1,
		})
		if err != nil {
			return false, err
		}
		if len(pl.Items) > 0 {
			return true, nil
		}
		return false, nil
	case OnOption:
		return true, nil
	default:
		return false, nil
	}
}

// fetchAsmOptions fetches and parses internal options configmap, which conditionally enables certain features
func fetchAsmOptions(client kubelib.Client) (AsmOptions, error) {
	defaultOpts := AsmOptions{
		CNI: OffOption,
	}
	// We add retries to account for IAM propagation delays. Even with pollIAMPropagation, sometimes it doesn't universally apply
	// to downstream services yet, etc, so we need to retry on all calls that access the default service account.
	var cm *corev1.ConfigMap
	var err error
	for attempts := 0; attempts < 50; attempts++ {
		cm, err = client.Kube().CoreV1().ConfigMaps(constants.IstioSystemNamespace).Get(context.Background(), "asm-options", metav1.GetOptions{})
		if err == nil {
			break
		}
		if kerrors.IsNotFound(err) {
			return defaultOpts, nil
		}
		log.Warnf("failed to fetch config map, will retry: %v", err)
		time.Sleep(time.Second)
	}
	if cm == nil {
		return defaultOpts, fmt.Errorf("exceeded retry budget fetching config map: %v", err)
	}
	opts, f := cm.Data["ASM_OPTS"]
	if !f {
		return defaultOpts, nil
	}
	var option AsmOptions
	for _, cmOption := range strings.Split(opts, ";") {
		if strings.Contains(cmOption, "CNI=check") {
			option.CNI = CheckOption
		} else if strings.Contains(cmOption, "CNI=on") {
			option.CNI = OnOption
		}
		if strings.Contains(cmOption, "CA=PRIVATECA") {
			option.CAOptions.CAType = PrivatecaOption
		} else if strings.Contains(cmOption, "CA=MANAGEDCAS") {
			option.CAOptions.CAType = ManagedCaOption
		} else if strings.Contains(cmOption, "CA=MESHCA") {
			option.CAOptions.CAType = MeshcaOption
		}
		if strings.Contains(cmOption, "CAAddr=") {
			option.CAOptions.CAAddr = strings.Split(cmOption, "=")[1]
		}
		if strings.Contains(cmOption, "TrustAnchorLoc=") {
			option.CAOptions.TrustAnchorLoc = strings.Split(cmOption, "=")[1]
		}
	}
	return option, nil
}

// executeTemplate executes a go template over fromFile, with inputs from params
func executeTemplate(fromFile string, params TemplateParameters) (string, error) {
	by, err := os.ReadFile(fromFile)
	if err != nil {
		return "", err
	}
	tmpl := template.Must(template.New("").Funcs(sprig.TxtFuncMap()).Parse(string(by)))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute template %v: %v", fromFile, err)
	}
	return buf.String(), nil
}

func executeTemplateTo(fromFile, toFile string, params TemplateParameters) error {
	expanded, err := executeTemplate(fromFile, params)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(toFile), 0o755); err != nil {
		// This may be ok, so just warn here. If its not we will fail on the write.
		log.Warnf("failed to create directory %v: %v", filepath.Dir(toFile), err)
	}
	return os.WriteFile(toFile, []byte(expanded), 0o644)
}

// configureMCPLogs configures our custom logging to be compatible with SD and tee to the consumer project's logs
func configureMCPLogs(p MCPParameters, options *log.Options) error {
	gcpmonitoring.SetTrustDomain(p.TrustDomain)
	gcpmonitoring.SetPodName(p.PodName)
	gcpmonitoring.SetPodNamespace(constants.IstioSystemNamespace)
	gcpmonitoring.SetMeshUID(fmt.Sprintf("proj-%s", p.ProjectNumber))

	// Setup variables required for logging
	platform.GCPMetadata = fmt.Sprintf("%s|%s|%s|%s", p.Project, p.ProjectNumber, p.Cluster, p.Zone)
	gcpmonitoring.TeeLogsToStackdriver = true
	gcpmonitoring.EnableSD = true
	loggingOpts := gcpmonitoring.ASMLogOptions(options)
	return log.Configure(loggingOpts)
}

type AsmOptionEnablement string

type CAType string

const (
	CheckOption AsmOptionEnablement = "check"
	OffOption   AsmOptionEnablement = "off"
	OnOption    AsmOptionEnablement = "on"

	MeshcaOption    CAType = "GoogleCA"
	PrivatecaOption CAType = "GoogleCAS"
	ManagedCaOption CAType = "ManagedCAS"
)

type ManagedCAOptions struct {
	CAAddr         string
	CAType         CAType
	TrustAnchorLoc string
}

type AsmOptions struct {
	CNI       AsmOptionEnablement
	CAOptions ManagedCAOptions
}

// TemplateParameters represents the set of inputs to the various template files we execute (mesh config,
// injection, etc)
type TemplateParameters struct {
	MCPParameters
	CNIEnabled    bool
	CAAddress     string
	CA            string
	CATrustAnchor string
	ProxyResourceParameters
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
}

type ProxyResourceParameters struct {
	ProxyMemoryRequest string
	ProxyCPURequest    string
	ProxyMemoryLimit   string
	ProxyCPULimit      string
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
	p.GKEClusterURL = os.Getenv("GKE_CLUSTER_URL")
	p.FleetProjectNumber = os.Getenv("FLEET_PROJECT_NUMBER")
	p.Tag = os.Getenv("TAG")
	p.Hub = os.Getenv("HUB")
	tdProj := os.Getenv("FLEET_PROJECT_ID")
	if tdProj == "" {
		tdProj = p.Project
	}
	p.TrustDomain = fmt.Sprintf("%s.svc.id.goog", tdProj)
	p.PodName = fmt.Sprintf("%s-%d", p.KRevision, time.Now().Nanosecond())

	if v := os.Getenv("AFC_MANAGED_WEBHOOK"); v != "" {
		var err error
		p.AFCManagedWebhook, err = strconv.ParseBool(v)
		if err != nil {
			return p, fmt.Errorf("parsing AFC_MANAGED_WEBHOOK: %w", err)
		}
	}
	return p, nil
}

func createProxyParameters(client kubelib.Client) ProxyResourceParameters {
	isAutopilot := getIfAutopilot(client)

	proxyCPURequest := "100m"
	proxyMemoryRequest := "128Mi"
	proxyCPULimit := "2000m"
	proxyMemoryLimit := "1024Mi"
	if isAutopilot {
		// Since Autopilot always sets limit == request, we need larger requests
		proxyCPURequest = "500m"
		proxyMemoryRequest = "512Mi"
		proxyCPULimit = proxyCPURequest
		proxyMemoryLimit = proxyMemoryRequest
	}

	return ProxyResourceParameters{
		ProxyCPURequest:    proxyCPURequest,
		ProxyMemoryRequest: proxyMemoryRequest,
		ProxyCPULimit:      proxyCPULimit,
		ProxyMemoryLimit:   proxyMemoryLimit,
	}
}

func getIfAutopilot(client kubelib.Client) bool {
	// TODO: wait till https://pkg.go.dev/google.golang.org/genproto/googleapis/container/v1#Cluster
	// publish the `Autopilot` field
	// This is a temporary workaround to check if a cluster is Autopilot or GKE
	// This CRD will be installed only if the cluster is Autopilot
	autoPilotCRD := "allowlistedworkloads.auto.gke.io"
	if _, err := client.Ext().ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), autoPilotCRD, metav1.GetOptions{}); err == nil {
		return true
	}
	return false
}