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

package env

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	sharedGCPProject      = "istio-prow-build"
	configDir             = "prow/asm/tester/configs"
	scriptaroCommitConfig = "scriptaro/commit"
)

func Setup(settings *resource.Settings) error {
	log.Println("🎬 start setting up the environment...")

	// Validate the settings before proceeding.
	if err := resource.ValidateSettings(settings); err != nil {
		return err
	}

	// Populate the settings that will be used in runtime.
	if err := populateRuntimeSettings(settings); err != nil {
		return err
	}

	// Fix the cluster configs before proceeding.
	if err := fixClusterConfigs(settings); err != nil {
		return err
	}

	if settings.ClusterType == resource.GKEOnGCP {
		// Enable core dumps for Istio proxy
		if err := enableCoreDumps(settings); err != nil {
			return err
		}
	}

	// Inject system env vars that are required for the test flow.
	if err := injectEnvVars(settings); err != nil {
		return err
	}

	log.Printf("Running with %q CA, %q Workload Identity Pool, %q and --vm=%t control plane.", settings.CA, settings.WIP, settings.ControlPlane, settings.UseVMs)

	return nil
}

// enableCoreDumps configures the core_pattern kernel property to write core dumps to a writeable directory.
// This allows CI to grab cores from crashing proxies. In OSS this is done by docker exec, since its always a single node.
// Note: OSS also allows an init container on each pod. This option was favored to avoid making tests not representing real world
// deployments.
func enableCoreDumps(settings *resource.Settings) error {
	yaml := filepath.Join(settings.ConfigDir, "core-dump-daemonset.yaml")
	for _, kc := range strings.Split(settings.KubectlContexts, ",") {
		cmd := fmt.Sprintf("kubectl apply -f %s --context=%s", yaml, kc)
		if err := exec.Run(cmd); err != nil {
			return fmt.Errorf("error deploying core dump daemonset: %w", err)
		}
	}

	return nil
}

// populate extra settings that will be used during the runtime
func populateRuntimeSettings(settings *resource.Settings) error {
	settings.ConfigDir = filepath.Join(settings.RepoRootDir, configDir)

	var kubectlContexts string
	var err error
	kubectlContexts, err = kube.ContextStr()
	if err != nil {
		return err
	}
	settings.KubectlContexts = kubectlContexts

	var gcrProjectID string
	if settings.ClusterType == resource.GKEOnGCP {
		cs := kube.GKEClusterSpecsFromContexts(kubectlContexts)
		projectIDs := make([]string, len(cs))
		for i, c := range cs {
			projectIDs[i] = c.ProjectID
		}
		settings.ClusterGCPProjects = projectIDs
		// If it's using the gke clusters, use the first available project to hold the images.
		gcrProjectID = settings.ClusterGCPProjects[0]
	} else {
		// Otherwise use the shared GCP project to hold these images.
		gcrProjectID = sharedGCPProject
	}
	settings.GCRProject = gcrProjectID

	f := filepath.Join(settings.ConfigDir, scriptaroCommitConfig)
	bytes, err := ioutil.ReadFile(f)
	if err != nil {
		return fmt.Errorf("error reading the Scriptaro commit config file: %w", err)
	}
	settings.ScriptaroCommit = strings.Split(string(bytes), "\n")[0]

	return nil
}

func injectEnvVars(settings *resource.Settings) error {
	var hub, tag string
	tag = "BUILD_ID_" + getBuildID()
	if settings.ControlPlane == resource.Unmanaged {
		hub = fmt.Sprintf("gcr.io/%s/asm/%s", settings.GCRProject, getBuildID())
	} else {
		// TODO(ruigu): Move this back to asm-staging-images after b/191049493.
		hub = "gcr.io/wlhe-cr/asm-mcp-e2e-test"
	}

	var meshID string
	if settings.ClusterType == resource.GKEOnGCP {
		projectNum, err := gcp.GetProjectNumber(settings.ClusterGCPProjects[0])
		if err != nil {
			return err
		}
		meshID = "proj-" + strings.TrimSpace(projectNum)
	}
	// For onprem with Hub CI jobs, clusters are registered into the environ project
	if settings.WIP == resource.HUBWorkloadIdentityPool && settings.ClusterType == resource.OnPrem {
		environProjectID, err := kube.GetEnvironProjectID(settings.Kubeconfig)
		if err != nil {
			return err
		}
		projectNum, err := gcp.GetProjectNumber(environProjectID)
		if err != nil {
			return err
		}
		meshID = "proj-" + strings.TrimSpace(projectNum)
	}

	// TODO(chizhg): delete most, if not all, the env var injections after we convert all the
	// bash to Go and remove the env var dependencies.
	envVars := map[string]string{
		// Run the Go tests with verbose logging.
		"T": "-v",
		// Do not start a container to run the build.
		"BUILD_WITH_CONTAINER": "0",
		// The GCP project we use when testing with multicloud clusters, or when we need to
		// hold some GCP resources that are shared across multiple jobs that are run in parallel.
		"SHARED_GCP_PROJECT": sharedGCPProject,

		"GCR_PROJECT_ID":   settings.GCRProject,
		"CONTEXT_STR":      settings.KubectlContexts,
		"CONFIG_DIR":       settings.ConfigDir,
		"CLUSTER_TYPE":     settings.ClusterType.String(),
		"CLUSTER_TOPOLOGY": settings.ClusterTopology.String(),
		"FEATURE_TO_TEST":  settings.FeatureToTest.String(),

		// The scriptaro repo commit ID to use to download the script.
		"SCRIPTARO_COMMIT": settings.ScriptaroCommit,

		// exported TAG and HUB are used for ASM installation, and as the --istio.test.tag and
		// --istio-test.hub flags of the testing framework
		"TAG": tag,
		"HUB": hub,

		"MESH_ID": meshID,

		"CONTROL_PLANE":        settings.ControlPlane.String(),
		"CA":                   settings.CA.String(),
		"WIP":                  settings.WIP.String(),
		"REVISION_CONFIG_FILE": settings.RevisionConfig,
		"TEST_TARGET":          settings.TestTarget,
		"DISABLED_TESTS":       settings.DisabledTests,

		"USE_VM":               strconv.FormatBool(settings.UseVMs),
		"GCE_VMS":              strconv.FormatBool(settings.UseGCEVMs || settings.VMStaticConfigDir != ""),
		"VM_DISTRO":            settings.VMImageFamily,
		"IMAGE_PROJECT":        settings.VMImageProject,
		"VM_AGENT_BUCKET":      settings.VMServiceProxyAgentGCSPath,
		"VM_AGENT_ASM_VERSION": settings.VMServiceProxyAgentASMVersion,
	}

	for name, val := range envVars {
		log.Printf("Set env var: %s=%s", name, val)
		if err := os.Setenv(name, val); err != nil {
			return fmt.Errorf("error setting env var %q to %q", name, val)
		}
	}

	return nil
}

func getBuildID() string {
	return os.Getenv("BUILD_ID")
}

// Fix the cluster configs to meet the test requirements for ASM.
// These fixes are considered as hacky and temporary, ideally in the future they
// should all be handled by the corresponding deployer.
func fixClusterConfigs(settings *resource.Settings) error {
	switch resource.ClusterType(settings.ClusterType) {
	case resource.GKEOnGCP:
		return fixGKE(settings)
	case resource.OnPrem:
		return fixOnPrem(settings)
	case resource.BareMetal:
		return fixBareMetal(settings)
	case resource.GKEOnAWS:
		return fixAWS(settings)
	case resource.APM:
		return fixAPM(settings)
	}

	return nil
}

func fixGKE(settings *resource.Settings) error {
	// Add firewall rules to enable multi-cluster communication
	if err := addFirewallRules(settings); err != nil {
		return err
	}

	if settings.FeatureToTest == resource.VPCSC {
		networkName := "prow-test-network"

		// Create router and NAT
		if err := exec.Run(fmt.Sprintf(
			"gcloud compute routers create test-router --network %s --region us-central1",
			networkName)); err != nil {
			log.Println("test-router already exists")
		}
		if err := exec.Run("gcloud compute routers nats create test-nat" +
			" --router=test-router --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges" +
			" --router-region=us-central1 --enable-logging"); err != nil {
			log.Println("test-nat already exists")
		}

		// Setup the firewall for VPC-SC
		for _, c := range kube.GKEClusterSpecsFromContexts(settings.KubectlContexts) {
			getFirewallRuleCmd := fmt.Sprintf("bash -c \"gcloud compute firewall-rules list --filter=\"name~gke-\"%s\"-[0-9a-z]*-master\" --format=json | jq -r '.[0].name'\"", c.Name)
			firewallRuleName, err := exec.RunWithOutput(getFirewallRuleCmd)
			if err != nil {
				return fmt.Errorf("failed to get firewall rule name: %w", err)
			}
			updateFirewallRuleCmd := fmt.Sprintf("gcloud compute firewall-rules update %s --allow tcp --source-ranges 0.0.0.0/0", firewallRuleName)
			if err := exec.Run(updateFirewallRuleCmd); err != nil {
				return fmt.Errorf("error updating firewall rule %q: %w", firewallRuleName, err)
			}
		}

		if err := addIpsToAuthorizedNetworks(settings); err != nil {
			return fmt.Errorf("error adding ips to authorized networks: %w", err)
		}
	}

	if settings.FeatureToTest == resource.PrivateClusterLimitedAccess || settings.FeatureToTest == resource.PrivateClusterNoAccess {
		if err := addIpsToAuthorizedNetworks(settings); err != nil {
			return fmt.Errorf("error adding ips to authorized networks: %w", err)
		}
	}

	return nil
}

func addFirewallRules(settings *resource.Settings) error {
	networkProject := settings.GCPProjects[0]
	clusterProjects := settings.ClusterGCPProjects
	if settings.ClusterTopology != resource.MultiProject {
		clusterProjects = []string{settings.ClusterGCPProjects[0]}
	}

	var sourceRanges, targetTags []string
	for _, p := range clusterProjects {
		// Create the firewall rules to allow multi-cluster communication as per
		// https://github.com/istio/istio.io/blob/master/content/en/docs/setup/platform-setup/gke/index.md#multi-cluster-communication
		allClusterCIDRs, err := exec.RunWithOutput(
			fmt.Sprintf("bash -c 'gcloud --project %s container clusters list --format=\"value(clusterIpv4Cidr)\" | sort | uniq'", p))
		if err != nil {
			return fmt.Errorf("error getting all cluster CIDRs for project %q: %w", p, err)
		}
		sourceRanges = append(sourceRanges, strings.Split(strings.TrimSpace(allClusterCIDRs), "\n")...)

		allClusterTags, err := exec.RunWithOutput(
			fmt.Sprintf("bash -c 'gcloud --project %s compute instances list --format=\"value(tags.items.[0])\" | sort | uniq'", p))
		if err != nil {
			return fmt.Errorf("error getting all cluster tags for project %q: %w", p, err)
		}
		targetTags = append(targetTags, strings.Split(strings.TrimSpace(allClusterTags), "\n")...)
	}

	// Get test runner CIDR
	testRunnerCidr, err := getTestRunnerCidr()
	if err != nil {
		return fmt.Errorf("failed to retrieve test runner IP: %w", err)
	}

	allowableSourceRanges := strings.Join(append(sourceRanges, testRunnerCidr), ",")
	// Set the env var to allow the VM script to read and set the allowable
	// source ranges.
	os.Setenv("ALLOWABLE_SOURCE_RANGES", allowableSourceRanges)

	if err := exec.Run(fmt.Sprintf(`gcloud compute firewall-rules create multicluster-firewall-rule \
	--network=prow-test-network \
	--project=%s \
	--allow=tcp,udp,icmp,esp,ah,sctp \
	--direction=INGRESS \
	--priority=900 \
	--source-ranges=%s \
	--target-tags=%s --quiet`, networkProject, allowableSourceRanges, strings.Join(targetTags, ","))); err != nil {
		return fmt.Errorf("error creating the firewall rule to allow multi-cluster communication")
	}

	return nil
}

func addIpsToAuthorizedNetworks(settings *resource.Settings) error {
	// Get test runner IP
	testRunnerCidr, err := getTestRunnerCidr()
	if err != nil {
		return fmt.Errorf("failed to retrieve test runner IP: %w", err)
	}

	// Get clusters' details
	gkeClusterSpecs := kube.GKEClusterSpecsFromContexts(settings.KubectlContexts)

	switch len(gkeClusterSpecs) {
	case 1:
		// Enable authorized networks for each cluster
		if err := enableAuthorizedNetworks(gkeClusterSpecs[0].Name, gkeClusterSpecs[0].ProjectID, gkeClusterSpecs[0].Location, testRunnerCidr, "", ""); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[0], err)
		}
	case 2:
		// Get the Pod IP CIDR block for each cluster
		clusterCount := 2
		clusterPodIpCidrs := make([]string, clusterCount)
		subnetPrimaryIpCidrs := make([]string, clusterCount)
		for clusterIndex := 0; clusterIndex < clusterCount; clusterIndex++ {
			podIpCidr, err := getPodIpCidr(gkeClusterSpecs[clusterIndex].Name, gkeClusterSpecs[clusterIndex].ProjectID, gkeClusterSpecs[clusterIndex].Location)
			clusterPodIpCidrs[clusterIndex] = strings.TrimSpace(podIpCidr)
			if err != nil {
				return fmt.Errorf("failed to get pod ip CIDR: %w", err)
			}
			networkProject := settings.GCPProjects[0]
			subnetPrimaryIpCidr, err := getClusterSubnetPrimaryIpCidr(gkeClusterSpecs[clusterIndex].Name, gkeClusterSpecs[clusterIndex].ProjectID, networkProject, gkeClusterSpecs[clusterIndex].Location)
			subnetPrimaryIpCidrs[clusterIndex] = strings.TrimSpace(subnetPrimaryIpCidr)
			if err != nil {
				return fmt.Errorf("failed to get cluster's subnet primary ip CIDR: %w", err)
			}
		}

		// Enable authorized networks for each cluster
		if err := enableAuthorizedNetworks(gkeClusterSpecs[0].Name, gkeClusterSpecs[0].ProjectID, gkeClusterSpecs[0].Location, testRunnerCidr, clusterPodIpCidrs[1], subnetPrimaryIpCidrs[1]); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[0].Name, err)
		}
		if err := enableAuthorizedNetworks(gkeClusterSpecs[1].Name, gkeClusterSpecs[1].ProjectID, gkeClusterSpecs[1].Location, testRunnerCidr, clusterPodIpCidrs[0], subnetPrimaryIpCidrs[0]); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[1].Name, err)
		}
		// Sleep 10 seconds to wait for the cluster update command to take into effect
		time.Sleep(10 * time.Second)
	default:
		return fmt.Errorf("expected # of clusters <= 2, received %d", len(gkeClusterSpecs))
	}

	return nil
}

func getTestRunnerCidr() (string, error) {
	getIPCmd := "curl ifconfig.me"
	ip, err := exec.RunWithOutput(getIPCmd)
	if err != nil {
		return "", fmt.Errorf("error getting test runner IP: %w", err)
	}
	return ip + "/32", nil
}

func getPodIpCidr(clusterName, project, zone string) (string, error) {
	getPodIpCidrCmd := fmt.Sprintf("gcloud container clusters describe %s"+
		" --project %s --zone %s --format \"value(ipAllocationPolicy.clusterIpv4CidrBlock)\"",
		clusterName, project, zone)
	return exec.RunWithOutput(getPodIpCidrCmd)
}

func getClusterSubnetPrimaryIpCidr(clusterName, project, networkProject, zone string) (string, error) {
	getSubnetCmd := fmt.Sprintf("gcloud container clusters describe %s"+
		" --project %s --zone %s --format \"value(subnetwork)\"",
		clusterName, project, zone)
	subnetName, err := exec.RunWithOutput(getSubnetCmd)
	if err != nil {
		return "", err
	}
	getPrimaryIpCidrCmd := fmt.Sprintf("gcloud compute networks subnets describe %s"+
		" --project %s --region %s --format \"value(ipCidrRange)\"",
		strings.TrimSpace(subnetName), networkProject, zone)
	return exec.RunWithOutput(getPrimaryIpCidrCmd)
}

func enableAuthorizedNetworks(clusterName, project, zone, pod_cidr_1, pod_cidr_2, subnet_cidr string) error {
	enableAuthorizedNetCmd := fmt.Sprintf(`gcloud container clusters update %s --project %s --zone %s \
	--enable-master-authorized-networks \
	--master-authorized-networks %s,%s`, clusterName, project, zone, pod_cidr_1, pod_cidr_2)
	// TODO (tairan): https://buganizer.corp.google.com/issues/187960475
	if clusterName == "prow-test1" {
		enableAuthorizedNetCmd = fmt.Sprintf(`gcloud container clusters update %s --project %s --zone %s \
	--enable-master-authorized-networks \
	--master-authorized-networks %s,%s,%s`, clusterName, project, zone, pod_cidr_1, pod_cidr_2, subnet_cidr)
	}
	return exec.Run(enableAuthorizedNetCmd)
}

// Keeps only the user-kubeconfig.yaml entries in the KUBECONFIG for onprem
// by removing others including the admin-kubeconfig.yaml entries.
// This function will modify the KUBECONFIG env variable.
func fixOnPrem(settings *resource.Settings) error {
	return filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "user-kubeconfig.yaml")
	})
}

// Fix bare-metal cluster configs that are created by Tailorbird:
// 1. Keep only the artifacts/kubeconfig entries in the KUBECONFIG for baremetal
//    by removing any others entries.
// 2. Configure cluster proxy.
func fixBareMetal(settings *resource.Settings) error {
	err := filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "artifacts/kubeconfig")
	})
	if err != nil {
		return err
	}

	if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
		// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/tf97d94df28f4277/artifacts/kubeconfig
		clusterArtifactsPath: filepath.Dir(settings.Kubeconfig),
		scriptRelPath:        "tunnel.sh",
		regexMatcher:         `.*\-L([0-9]*):localhost.* (root@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)`,
		sshKeyRelPath:        "id_rsa",
	}); err != nil {
		return err
	}

	return nil
}

// Fix APM cluster configs that are created by Tailorbird:
// 1. Keep only the artifacts/kubeconfig entries in the KUBECONFIG for baremetal
//    by removing any others entries.
// 2. Configure cluster proxy.
func fixAPM(settings *resource.Settings) error {
	err := filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "artifacts/kubeconfig")
	})
	if err != nil {
		return err
	}

	if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
		// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/tf97d94df28f4277/artifacts/kubeconfig
		clusterArtifactsPath: filepath.Dir(settings.Kubeconfig),
		scriptRelPath:        "tunnel.sh",
		regexMatcher:         `.*\-L([0-9]*):localhost.* (nonroot@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)`,
		sshKeyRelPath:        "id_rsa",
	}); err != nil {
		return err
	}

	return nil
}

// Fix aws cluster configs that are created by Tailorbird:
// 1. Removes gke_aws_management.conf entry from the KUBECONFIG for aws
// 2. Configure cluster proxy.
func fixAWS(settings *resource.Settings) error {
	err := filterKubeconfigFiles(settings, func(name string) bool {
		return !strings.HasSuffix(name, "gke_aws_management.conf")
	})
	if err != nil {
		return err
	}

	if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
		// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/t96ea7cc97f047f5/.kube/gke_aws_default_t96ea7cc97f047f5.conf
		clusterArtifactsPath: filepath.Dir(filepath.Dir(settings.Kubeconfig)),
		scriptRelPath:        "tunnel-script.sh",
		regexMatcher:         `.*\-L([0-9]*):localhost.* (ubuntu@.*compute\.amazonaws\.com)`,
		sshKeyRelPath:        ".ssh/anthos-gke",
	}); err != nil {
		return err
	}

	return nil
}

func filterKubeconfigFiles(settings *resource.Settings, shouldKeep func(string) bool) error {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		return errors.New("KUBECONFIG env var cannot be empty")
	}

	files := filepath.SplitList(kubeconfig)
	filteredFiles := make([]string, 0)
	for _, f := range files {
		if shouldKeep(f) {
			filteredFiles = append(filteredFiles, f)
		} else {
			log.Printf("Remove %q from KUBECONFIG", f)
		}
	}
	filteredKubeconfig := strings.Join(filteredFiles, string(os.PathListSeparator))
	os.Setenv("KUBECONFIG", filteredKubeconfig)
	settings.Kubeconfig = filteredKubeconfig

	return nil
}

type multicloudClusterConfig struct {
	// the path for storing the cluster artifacts files.
	clusterArtifactsPath string
	// tunnel script relative path to the cluster artifacts path.
	scriptRelPath string
	// ssh key file relative path to the cluster artifacts path.
	sshKeyRelPath string
	// regex to find the PORT_NUMBER and BOOTSTRAP_HOST_SSH_USER from the tunnel
	// script.
	regexMatcher string
}

func configMulticloudClusterProxy(settings *resource.Settings, mcConf multicloudClusterConfig) error {
	tunnelScriptPath := filepath.Join(mcConf.clusterArtifactsPath, mcConf.scriptRelPath)
	tunnelScriptContent, err := ioutil.ReadFile(tunnelScriptPath)
	if err != nil {
		return fmt.Errorf("error reading %q under the cluster artifacts path for aws: %w", mcConf.scriptRelPath, err)
	}

	patn := regexp.MustCompile(mcConf.regexMatcher)
	matches := patn.FindStringSubmatch(string(tunnelScriptContent))
	if len(matches) != 3 {
		return fmt.Errorf("error finding PORT_NUMBER and BOOTSTRAP_HOST_SSH_USER from: %q", tunnelScriptContent)
	}
	portNum, bootstrapHostSSHUser := matches[1], matches[2]
	httpProxy := "localhost:" + portNum
	bootstrapHostSSHKey := filepath.Join(mcConf.clusterArtifactsPath, mcConf.sshKeyRelPath)
	log.Printf("----------%s Cluster env----------", settings.ClusterType)
	log.Print("MC_HTTP_PROXY: ", httpProxy)
	log.Printf("BOOTSTRAP_HOST_SSH_USER: %s, BOOTSTRAP_HOST_SSH_KEY: %s", bootstrapHostSSHUser, bootstrapHostSSHKey)

	for name, val := range map[string]string{
		// Used by ingress related tests
		"BOOTSTRAP_HOST_SSH_USER": bootstrapHostSSHUser,
		"BOOTSTRAP_HOST_SSH_KEY":  bootstrapHostSSHKey,

		"MC_HTTP_PROXY": httpProxy,
	} {
		log.Printf("Set env var: %s=%s", name, val)
		if err := os.Setenv(name, val); err != nil {
			return fmt.Errorf("error setting env var %q to %q: %w", name, val, err)
		}
	}

	//  Increase proxy's max connection setup to avoid too many connections error
	sshCmd1 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/#max-client-connections.*/max-client-connections 512/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd2 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/keep-alive-timeout 5/keep-alive-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd3 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/socket-timeout 300/socket-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd4 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/#default-server-timeout.*/default-server-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd5 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/accept-intercepted-requests 0/accept-intercepted-requests 1/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd6 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo systemctl restart privoxy.service\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	if err := exec.RunMultiple([]string{sshCmd1, sshCmd2, sshCmd3, sshCmd4, sshCmd5, sshCmd6}); err != nil {
		return fmt.Errorf("error running the commands to increase proxy's max connection setup: %w", err)
	}

	return nil
}
