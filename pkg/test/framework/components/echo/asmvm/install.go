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
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"google.golang.org/api/compute/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/kube"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
)

// resourceName is used for various purposes including WorkloadGroup, instance-template and managed-instance-group.
// multipel VMs with the same service/namespace may be deployed, but connected to different clusters, so we include
// the cluster name for uniqueness.
// Some tests check the Hostname in echo responses, so it's important that it begins with the Service name.
func (i *instance) resourceName() string {
	return strings.ToLower(fmt.Sprintf("%s-%s-%s", i.config.Service, i.config.Namespace.Name(), i.config.Cluster.StableName()))
}

// serviceAccount gives the default compute service account based on the project number
func (i *instance) serviceAccount() string {
	return i.cluster.ProjectNumber() + "-compute@developer.gserviceaccount.com"
}

const echoServiceTmpl = `
[Unit]
Description=Echo app for testing Istio
After=service-proxy-agent.service
Requires=service-proxy-agent.service

[Service]
EnvironmentFile=/etc/.echoconfig
ExecStart='/usr/sbin/echo' \
  --cluster "$CLUSTER_ID" \
{{- range $i, $p := $.ContainerPorts }}
{{- if eq .Protocol "GRPC" }}
  --grpc \
{{- else if eq .Protocol "TCP" }}
  --tcp \
{{- else }}
  --port \
{{- end }}
  "{{ $p.Port }}" \
{{- if $p.ServerFirst }}
  --server-first={{ $p.Port }} \
{{- end }}
{{- if $p.TLS }}
  --tls={{ $p.Port }} \
{{- end }}
{{- if $p.InstanceIP }}
  --bind-ip={{ $p.Port }} \
{{- end }}
{{- if $p.LocalhostIP }}
  --bind-localhost={{ $p.Port }} \
{{- end }}
{{- end }}
  --crt=/etc/certs/cert-chain.pem \
  --key=/etc/certs/key.pem
[Install]
WantedBy=multi-user.target
`

const workloadGroupTmpl = `
apiVersion: networking.istio.io/v1alpha3
kind: WorkloadGroup
metadata:
  # TODO support subsets instead of hardcoding v1
  name: {{.Service}}-v1
  namespace: {{.Namespace}}
spec:
  metadata:
    annotations:
      security.cloud.google.com/IdentityProvider: google
    labels:
      app: {{.Service}}
      version: v1
  template:
    serviceAccount: {{.serviceAccount}}
    network: "{{.network}}"
    ports:
{{- range $i, $p := .Ports }}
      {{ $p.Name }}: {{ $p.InstancePort }}
{{- end }}
`

func (i *instance) generateConfig() error {
	params := kube.ServiceParams(i.config)
	params["serviceAccount"] = i.serviceAccount()
	// if the VMs are on a specific network use that
	params["network"] = i.cluster.NetworkName()
	if params["network"] == "" {
		// otherwise assume they're on their primary cluster's network
		params["network"] = i.cluster.Primary().NetworkName()
	}

	if i.dir == "" {
		var err error
		i.dir, err = i.ctx.CreateDirectory(i.resourceName())
		if err != nil {
			return err
		}
	}
	if i.unitFile == "" {
		service := tmpl.MustEvaluate(echoServiceTmpl, params)
		i.unitFile = path.Join(i.dir, "echo.service")
		if err := ioutil.WriteFile(i.unitFile, []byte(service), 0o644); err != nil {
			return err
		}
	}
	if i.workloadGroup == "" {
		i.workloadGroup = tmpl.MustEvaluate(workloadGroupTmpl, params)
	}
	return nil
}

// createWorkloadGroup creates a WorkloadGroup resource with the proper identity provider and service account.
func (i *instance) createWorkloadGroup(ctx resource.Context) error {
	scopes.Framework.Infof("Creating WorkloadGroup for echo VM %s", i.config.Service)

	revision := "default"
	// nolint: staticcheck
	if ctx.Settings().Revision != "" {
		revision = ctx.Settings().Revision
	}
	// TODO: use --istio.test.revision to label VM namespace
	if err := i.config.Namespace.SetLabel("istio.io/rev", revision); err != nil {
		return err
	}

	c := i.cluster
	if err := ctx.ConfigKube(c.Primary()).YAML(i.config.Namespace.Name(), i.workloadGroup).Apply(); err != nil {
		return fmt.Errorf("error applying workload group for %s to %s: %v", i.config.Service, c.PrimaryName(), err)
	}
	return nil
}

var (
	projects = map[echo.VMDistro]string{
		echo.Debian11:    "debian-cloud",
		echo.Centos7:     "centos-cloud",
		echo.Rockylinux8: "centos-cloud",
	}
	distros = map[echo.VMDistro]string{
		echo.Debian11:    "debian-9",
		echo.Centos7:     "centos-7",
		echo.Rockylinux8: "centos-8",
	}
)

// createInstanceTemplate uses gcloud to create an instance template. createWorkloadGroup must be run first.
func (i *instance) createInstanceTemplate() error {
	scopes.Framework.Infof("Creating instance template %s for echo vm", i.resourceName())

	project, distro := projects[i.config.VMDistro], distros[i.config.VMDistro]

	if project == "" && distro == "" {
		// TODO support customizing distro in echo.Config (requires https://github.com/istio/istio/issues/31427)
		project = os.Getenv("IMAGE_PROJECT")
		distro = os.Getenv("VM_DISTRO")
	}

	gkeCluster := fmt.Sprintf("%s/%s", i.cluster.GKELocation(), i.cluster.GKEClusterName())
	workload := fmt.Sprintf("%s/%s", i.config.Namespace.Name(), i.config.Service+"-v1")
	meshVal := fmt.Sprintf("gke-cluster=%s,workload=%s", gkeCluster, workload)

	// TODO(b/210923265): Remove once gcloud adds the metadata.
	md := append(i.cluster.InstanceMetadata(), "osconfig-disabled-features=tasks")

	instanceMetadata := strings.Join(md, ",")

	cmd := exec.Command("gcloud", "beta", "compute",
		"instance-templates", "create", i.resourceName(),
		"--mesh", meshVal,
		"--metadata", instanceMetadata,
		"--machine-type", "n1-standard-1", "--boot-disk-size", "20GB",
		"--image-project", project, "--image-family", distro,
		"--network", fmt.Sprintf("projects/%s/global/networks/%s", i.cluster.Project(), i.cluster.GKENetworkName()),
		"--service-account", i.serviceAccount(),
		// the test runner should have created a firewall rule to allow traffic from itself to all instances with FirewallTag
		"--tags", i.cluster.FirewallTag())
	scopes.Framework.Infof("Running command:\n%s", cmd.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		scopes.Framework.Infof("failed creating instance template:\n%s", string(out))
		return fmt.Errorf("failed to create instance template %s: %v:\n%s", i.resourceName(), err, string(out))
	}

	return nil
}

// createManagedInstanceGroup sets up a MIG but does not wait for instances to be ready. createInstanceTemplate must
// be run first to create the MIG.
func (i *instance) createManagedInstanceGroup() error {
	scopes.Framework.Infof("Creating managed instance group for echo VM %s", i.config.Service)
	name := i.resourceName()

	if _, err := i.cluster.
		Service().InstanceGroupManagers.
		Insert(i.cluster.Project(), i.cluster.Zone(), &compute.InstanceGroupManager{
			Name:             name,
			BaseInstanceName: name,
			InstanceTemplate: i.cluster.Prefix() + "/global/instanceTemplates/" + name,
			TargetSize:       int64(i.replicas),
		}).Do(); err != nil {
		return fmt.Errorf("failed creating managed intance group %s: %v", name, err)
	}

	return nil
}

// initializeWorkloads waits for the desired number of instances to be present and ready, then sets
// the instance's workloads accordingly. This should be called when initializing the instance, or when scaling the number
// of replicas. createManagedInstanceGroup must be run once before calling this method.
func (i *instance) initializeWorkloads() error {
	scopes.Framework.Infof("Waiting for managed instances in group %s to be ready", i.resourceName())
	var migInstances []*compute.Instance
	if err := retry.UntilSuccess(func() (err error) {
		migInstances, err = i.getReadyManagedInstances()
		return
	}, retry.Timeout(1*time.Minute), retry.Delay(5*time.Second)); err != nil {
		return fmt.Errorf("failed waiting for managed instances to ready: %v", err)
	}

	scopes.Framework.Infof("Installing echo app on managed instances in group %s", i.resourceName())
	if err := i.installEcho(migInstances); err != nil {
		return fmt.Errorf("failed installing echo on VMs: %v", err)
	}

	scopes.Framework.Infof("Initializing echo gRPC clients for managed instances in group %s", i.resourceName())
	grpcPort, f := i.config.Ports.ForProtocol(protocol.GRPC)
	if !f {
		return errors.New("unable fo find GRPC command port")
	}
	workloads, err := newWorkloads(migInstances, grpcPort.WorkloadPort, i.config.TLSSettings, i.cluster)
	if err != nil {
		return err
	}

	i.Lock()
	i.workloads = workloads
	i.Unlock()

	return nil
}

// echoInstallScript moves scp'd files to the proper directory, generates an env file and starts the systemd unit
const echoInstallScript = `
sudo mv ~/server /usr/sbin/echo
sudo mv ~/echo.service /etc/systemd/system/echo.service

# this should be the private IP of the instance
echo INSTANCE_IP=%s >> .echoconfig
# this is the "test" cluster name, used for response validation (not the actual primary cluster name)
echo CLUSTER_ID=%s >> .echoconfig
sudo mv .echoconfig /etc/.echoconfig

# fix permissions in centos
which restorecon && sudo restorecon /etc/.echoconfig
which restorecon && sudo restorecon /usr/sbin/echo
which restorecon && sudo restorecon /etc/systemd/system/echo.service
sudo chmod +x /usr/sbin/echo
sudo chmod +r /etc/.echoconfig

sudo systemctl daemon-reload
sudo systemctl restart echo.service
sudo systemctl enable echo.service
`

func (i *instance) installEcho(instances []*compute.Instance) error {
	errG := multierror.Group{}
	for _, mi := range instances {
		mi := mi
		errG.Go(func() error {
			i.Lock()
			if i.echoInstalled[mi.Id] {
				return nil
			}
			i.Unlock()

			if len(mi.NetworkInterfaces) < 1 {
				return fmt.Errorf("%s has no networkInterfaces, cannot get internal IP", mi.Name)
			}
			internalIP := mi.NetworkInterfaces[0].NetworkIP
			// find echo server executable
			outFromSrc := path.Join(env.IstioSrc, "out", "linux_amd64")
			serverExec := path.Join(env.TARGET_OUT.ValueOrDefault(outFromSrc), "server")
			files := []string{i.unitFile, serverExec}
			scopes.Framework.Infof("Copying %s to %s", strings.Join(files, ", "), mi.Name)
			if err := retry.UntilSuccess(func() error {
				// TODO scp/ssh without exec and gcloud - just look at the public IP, but what user would we use?
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				// copy files
				for _, f := range files {
					if out, err := exec.CommandContext(ctx, "gcloud", "compute", "scp",
						f, "echovm@"+mi.Name+":~",
						"--zone", mi.Zone,
					).CombinedOutput(); err != nil {
						return fmt.Errorf("failed to scp %s: %v:\n%s", f, err, string(out))
					}
				}
				// run the install script
				if out, err := exec.CommandContext(ctx, "gcloud", "compute", "ssh",
					"echovm@"+mi.Name,
					"--zone", mi.Zone,
					"--command", fmt.Sprintf(echoInstallScript, internalIP, i.cluster.Name()),
				).CombinedOutput(); err != nil {
					return fmt.Errorf("failed running install script on %s: %v:\n%s", mi.Name, err, string(out))
				}
				return nil
			}, retry.Timeout(90*time.Second), retry.Delay(1*time.Second)); err != nil {
				return fmt.Errorf("failed to install echo on %s: %v", mi.Name, err)
			}

			i.Lock()
			i.echoInstalled[mi.Id] = true
			i.Unlock()

			return nil
		})
	}
	return errG.Wait().ErrorOrNil()
}

func (i *instance) getReadyManagedInstances() ([]*compute.Instance, error) {
	// fetch the mig and all compute instances
	miRes, err := i.cluster.Service().InstanceGroupManagers.
		ListManagedInstances(i.cluster.Project(), i.cluster.Zone(), i.resourceName()).Do()
	if err != nil {
		return nil, err
	}
	if len(miRes.ManagedInstances) != i.replicas {
		return nil, fmt.Errorf("expected %d managed instances but got %d", i.replicas, len(miRes.ManagedInstances))
	}
	// fetch all compute instances - Get would still end up calling List under the hood
	instances, err := i.fetchInstances()
	if err != nil {
		return nil, err
	}

	// collect the instances that are a part of the mig
	var (
		migInstances []*compute.Instance
		errs         error
	)

	for _, mi := range miRes.ManagedInstances {
		instance, ok := instances[mi.Instance]
		if !ok {
			errs = multierror.Append(err, fmt.Errorf("did not find %s (referenced in MIG) in the list of instances", mi.Instance))
			continue
		}
		if !strings.EqualFold(mi.InstanceStatus, "running") {
			errs = multierror.Append(err, fmt.Errorf("%s in group %s: %s", instance.Name, i.resourceName(), mi.InstanceStatus))
			continue
		}
		migInstances = append(migInstances, instance)
	}
	if len(migInstances) != len(miRes.ManagedInstances) {
		errs = multierror.Append(fmt.Errorf("found %d/%d instnaces of the MIG %s", len(migInstances), len(miRes.ManagedInstances), i.resourceName()))
	}
	if errs != nil {
		return nil, errs
	}

	return migInstances, nil
}

func (i *instance) fetchInstances() (map[string]*compute.Instance, error) {
	iRes, err := i.cluster.Service().Instances.List(i.cluster.Project(), i.cluster.Zone()).Do()
	if err != nil {
		return nil, err
	}
	instances := make(map[string]*compute.Instance, len(iRes.Items))
	for _, item := range iRes.Items {
		instances[item.SelfLink] = item
	}
	return instances, nil
}

func getClusterIP(config echo.Config) (string, error) {
	svc, err := config.Cluster.Primary().Kube().CoreV1().
		Services(config.Namespace.Name()).Get(context.TODO(), config.Service, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return svc.Spec.ClusterIP, nil
}
