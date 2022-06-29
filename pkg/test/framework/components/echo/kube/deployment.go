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

package kube

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/hashicorp/go-multierror"
	kubeCore "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"istio.io/api/label"
	meshconfig "istio.io/api/mesh/v1alpha1"
	istioctlcmd "istio.io/istio/istioctl/cmd"
	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/config/protocol"
	echoCommon "istio.io/istio/pkg/test/echo/common"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/environment/kube"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/istioctl"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/framework/resource/config/apply"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
	"istio.io/istio/pkg/util/protomarshal"
	"istio.io/pkg/log"
)

const (
	// for proxyless we add a special gRPC server that doesn't get configured with xDS for test-runner use
	grpcMagicPort = 17171
	// for non-Go implementations of gRPC echo, this is the port used to forward non-gRPC requests to the Go server
	grpcFallbackPort = 17777

	serviceYAML = `
{{- if .ServiceAccount }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Service }}
---
{{- end }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Service }}
  labels:
    app: {{ .Service }}
{{- if .ServiceAnnotations }}
  annotations:
{{- range $name, $value := .ServiceAnnotations }}
    {{ $name.Name }}: {{ printf "%q" $value.Value }}
{{- end }}
{{- end }}
spec:
{{- if .IPFamilies }}
  ipFamilies: [ {{ .IPFamilies }} ]
{{- end }}
{{- if .IPFamilyPolicy }}
  ipFamilyPolicy: {{ .IPFamilyPolicy }}
{{- end }}
{{- if .Headless }}
  clusterIP: None
{{- end }}
  ports:
{{- range $i, $p := .ServicePorts }}
  - name: {{ $p.Name }}
    port: {{ $p.ServicePort }}
    targetPort: {{ $p.WorkloadPort }}
{{- end }}
  selector:
    app: {{ .Service }}
`

	deploymentYAML = `
{{- $revVerMap := .Revisions }}
{{- $subsets := .Subsets }}
{{- $cluster := .Cluster }}
{{- range $i, $subset := $subsets }}
{{- range $revision, $version := $revVerMap }}
apiVersion: apps/v1
{{- if $.StatefulSet }}
kind: StatefulSet
{{- else }}
kind: Deployment
{{- end }}
metadata:
{{- if $.Compatibility }}
  name: {{ $.Service }}-{{ $subset.Version }}-{{ $revision }}
{{- else }}
  name: {{ $.Service }}-{{ $subset.Version }}
{{- end }}
spec:
  {{- if $.StatefulSet }}
  serviceName: {{ $.Service }}
  {{- end }}
  replicas: 1
  selector:
    matchLabels:
      app: {{ $.Service }}
      version: {{ $subset.Version }}
{{- if ne $.Locality "" }}
      istio-locality: {{ $.Locality }}
{{- end }}
  template:
    metadata:
      labels:
        app: {{ $.Service }}
        version: {{ $subset.Version }}
        test.istio.io/class: {{ $.WorkloadClass }}
{{- if $.Compatibility }}
        istio.io/rev: {{ $revision }}
{{- end }}
{{- if ne $.Locality "" }}
        istio-locality: {{ $.Locality }}
{{- end }}
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "15014"
{{- range $name, $value := $subset.Annotations }}
        {{ $name.Name }}: {{ printf "%q" $value.Value }}
{{- end }}
{{- if $.ProxylessGRPC }}
        proxy.istio.io/config: '{"holdApplicationUntilProxyStarts": true}'
{{- end }}
    spec:
{{- if $.ServiceAccount }}
      serviceAccountName: {{ $.Service }}
{{- end }}
{{- if ne $.ImagePullSecretName "" }}
      imagePullSecrets:
      - name: {{ $.ImagePullSecretName }}
{{- end }}
      containers:
{{- if and
  (ne ($subset.Annotations.GetByName "sidecar.istio.io/inject") "false")
  (ne ($subset.Annotations.GetByName "inject.istio.io/templates") "grpc")
  ($.OverlayIstioProxy)
}}
      - name: istio-proxy
        image: auto
        imagePullPolicy: {{ $.ImagePullPolicy }}
        securityContext: # to allow core dumps
          readOnlyRootFilesystem: false
{{- end }}
{{- if $.IncludeExtAuthz }}
      - name: ext-authz
        image: {{ $.ImageHub }}/ext-authz:{{ $.ImageTag }}
        imagePullPolicy: {{ $.ImagePullPolicy }}
        ports:
        - containerPort: 8000
        - containerPort: 9000
{{- end }}
{{- range $i, $appContainer := $.AppContainers }}
      - name: {{ $appContainer.Name }}
{{- if $appContainer.ImageFullPath }}
        image: {{ $appContainer.ImageFullPath }}
{{- else }}
        image: {{ $.ImageHub }}/app:{{ $.ImageTag }}
{{- end }}
        imagePullPolicy: {{ $.ImagePullPolicy }}
        securityContext:
          runAsUser: 1338
          runAsGroup: 1338
        args:
{{- if $appContainer.FallbackPort }}
          - --forwarding_address=0.0.0.0:{{ $appContainer.FallbackPort }}
{{- end }}
          - --metrics=15014
          - --cluster={{ $cluster }}
{{- range $i, $p := $appContainer.ContainerPorts }}
{{- if and $p.XDSServer (eq .Protocol "GRPC") }}
          - --xds-grpc-server={{ $p.Port }}
{{- else if eq .Protocol "GRPC" }}
          - --grpc={{ $p.Port }}
{{- else if eq .Protocol "TCP" }}
          - --tcp={{ $p.Port }}
{{- else }}
          - --port={{ $p.Port }}
{{- end }}
{{- if $p.TLS }}
          - --tls={{ $p.Port }}
{{- end }}
{{- if $p.ServerFirst }}
          - --server-first={{ $p.Port }}
{{- end }}
{{- if $p.InstanceIP }}
          - --bind-ip={{ $p.Port }}
{{- end }}
{{- if $p.LocalhostIP }}
          - --bind-localhost={{ $p.Port }}
{{- end }}
{{- end }}
          - --version={{ $subset.Version }}
          - --istio-version={{ $version }}
{{- if $.TLSSettings }}
          - --crt=/etc/certs/custom/cert-chain.pem
          - --key=/etc/certs/custom/key.pem
{{- if $.TLSSettings.AcceptAnyALPN}}
          - --disable-alpn
{{- end }}
{{- else }}
          - --crt=/cert.crt
          - --key=/cert.key
{{- end }}
        ports:
{{- range $i, $p := $appContainer.ContainerPorts }}
        - containerPort: {{ $p.Port }}
{{- if eq .Port 3333 }}
          name: tcp-health-port
{{- else if and ($appContainer.ImageFullPath) (eq .Port 17171) }}
          name: tcp-health-port
{{- end }}
{{- end }}
        env:
        - name: INSTANCE_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
{{- if $.ProxylessGRPC }}
        - name: EXPOSE_GRPC_ADMIN
          value: "true"
{{- end }}
        readinessProbe:
{{- if $.ReadinessTCPPort }}
          tcpSocket:
            port: {{ $.ReadinessTCPPort }}
{{- else if $.ReadinessGRPCPort }}
          grpc:
            port: {{ $.ReadinessGRPCPort }}			
{{- else if $appContainer.ImageFullPath }}
          tcpSocket:
            port: tcp-health-port
{{- else }}
          httpGet:
            path: /
            port: 8080
{{- end }}
          initialDelaySeconds: 1
          periodSeconds: 2
          failureThreshold: 10
        livenessProbe:
          tcpSocket:
            port: tcp-health-port
          initialDelaySeconds: 10
          periodSeconds: 10
          failureThreshold: 10
{{- if $.StartupProbe }}
        startupProbe:
          tcpSocket:
            port: tcp-health-port
          periodSeconds: 1
          failureThreshold: 10
{{- end }}
{{- if $.TLSSettings }}
        volumeMounts:
        - mountPath: /etc/certs/custom
          name: custom-certs
{{- end }}
{{- end }}
{{- if $.TLSSettings }}
      volumes:
{{- if $.TLSSettings.ProxyProvision }}
      - emptyDir:
          medium: Memory
{{- else }}
      - configMap:
          name: {{ $.Service }}-certs
{{- end }}
        name: custom-certs
{{- end }}
---
{{- end }}
{{- end }}
{{- if .TLSSettings}}{{if not .TLSSettings.ProxyProvision }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ $.Service }}-certs
data:
  root-cert.pem: |
{{ .TLSSettings.RootCert | indent 4 }}
  cert-chain.pem: |
{{ .TLSSettings.ClientCert | indent 4 }}
  key.pem: |
{{.TLSSettings.Key | indent 4}}
---
{{- end}}{{- end}}
`

	// vmDeploymentYaml aims to simulate a VM, but instead of managing the complex test setup of spinning up a VM,
	// connecting, etc we run it inside a pod. The pod has pretty much all Kubernetes features disabled (DNS and SA token mount)
	// such that we can adequately simulate a VM and DIY the bootstrapping.
	vmDeploymentYaml = `
{{- $subsets := .Subsets }}
{{- $cluster := .Cluster }}
{{- range $i, $subset := $subsets }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $.Service }}-{{ $subset.Version }}
spec:
  replicas: 1
  selector:
    matchLabels:
      istio.io/test-vm: {{ $.Service }}
      istio.io/test-vm-version: {{ $subset.Version }}
  template:
    metadata:
      annotations:
        # Sidecar is inside the pod to simulate VMs - do not inject
        sidecar.istio.io/inject: "false"
      labels:
        # Label should not be selected. We will create a workload entry instead
        istio.io/test-vm: {{ $.Service }}
        istio.io/test-vm-version: {{ $subset.Version }}
    spec:
      # Disable kube-dns, to mirror VM
      # we set policy to none and explicitly provide a set of invalid values
      # for nameservers, search namespaces, etc. ndots is set to 1 so that
      # the application will first try to resolve the hostname (a, a.ns, etc.) as is
      # before attempting to add the search namespaces.
      dnsPolicy: None
      dnsConfig:
        nameservers:
        - "8.8.8.8"
        searches:
        - "com"
        options:
        - name: "ndots"
          value: "1"
{{- if $.VM.IstioHost }}
      # Override the istiod host to force traffic through east-west gateway. 
      hostAliases:
      - ip: {{ $.VM.IstioIP }}
        hostnames:
        - {{ $.VM.IstioHost }}
{{- end }}
      # Disable service account mount, to mirror VM
      automountServiceAccountToken: false
      {{- if $.ImagePullSecretName }}
      imagePullSecrets:
      - name: {{ $.ImagePullSecretName }}
      {{- end }}
      containers:
      - name: istio-proxy
        image: {{ $.ImageHub }}/{{ $.VM.Image }}:{{ $.ImageTag }}
        imagePullPolicy: {{ $.ImagePullPolicy }}
        securityContext:
          capabilities:
            add:
            - NET_ADMIN
          runAsUser: 1338
          runAsGroup: 1338
        command:
        - bash
        - -c
        - |-
          # To support image builders which cannot do RUN, do the run commands at startup.
          # This exploits the fact the images remove the installer once its installed.
          # This is a horrible idea for production images, but these are just for tests.
          [[ -f /tmp/istio-sidecar-centos-7.rpm ]] && sudo rpm -vi /tmp/istio-sidecar-centos-7.rpm && sudo rm /tmp/istio-sidecar-centos-7.rpm
          [[ -f /tmp/istio-sidecar.rpm ]] && sudo rpm -vi /tmp/istio-sidecar.rpm && sudo rm /tmp/istio-sidecar.rpm
          [[ -f /tmp/istio-sidecar.deb ]] && sudo dpkg -i /tmp/istio-sidecar.deb && sudo rm /tmp/istio-sidecar.deb

          # Read root cert from and place signed certs here (can't mount directly or the dir would be unwritable)
          sudo mkdir -p /var/run/secrets/istio

          # hack: remove certs that are bundled in the image
          sudo rm /var/run/secrets/istio/cert-chain.pem
          sudo rm /var/run/secrets/istio/key.pem
          sudo chown -R istio-proxy /var/run/secrets

          # place mounted bootstrap files (token is mounted directly to the correct location)
          sudo cp /var/run/secrets/istio/bootstrap/root-cert.pem /var/run/secrets/istio/root-cert.pem
          sudo cp /var/run/secrets/istio/bootstrap/*.env /var/lib/istio/envoy/
          sudo cp /var/run/secrets/istio/bootstrap/mesh.yaml /etc/istio/config/mesh

          # don't overwrite /etc/hosts since it's managed by kubeproxy
          #sudo sh -c 'cat /var/run/secrets/istio/bootstrap/hosts >> /etc/hosts'

          # since we're not overwriting /etc/hosts on k8s, verify that istiod hostname in /etc/hosts
          # matches the value generated by istioctl
          echo "checking istio host"
          SYSTEM_HOST=$(cat /etc/hosts | grep istiod)
          ISTIOCTL_HOST=$(cat /var/run/secrets/istio/bootstrap/hosts | grep istiod)
          if [ "$(echo "$SYSTEM_HOST" | tr -d '[:space:]')" != "$(echo "$ISTIOCTL_HOST" | tr -d '[:space:]')" ]; then
            echo "istiod host in /etc/hosts does not match value generated by istioctl"
            echo "/etc/hosts: $SYSTEM_HOST"
            echo "/var/run/secrets/istio/bootstrap/hosts: $ISTIOCTL_HOST"
            exit 1
          fi
          echo "istiod host ok"

          # read certs from correct directory
          sudo sh -c 'echo PROV_CERT=/var/run/secrets/istio >> /var/lib/istio/envoy/cluster.env'
          sudo sh -c 'echo OUTPUT_CERTS=/var/run/secrets/istio >> /var/lib/istio/envoy/cluster.env'

          # su will mess with the limits set on the process we run. This may lead to quickly exhausting the file limits
          # We will get the host limit and set it in the child as well.
          # TODO(https://superuser.com/questions/1645513/why-does-executing-a-command-in-su-change-limits) can we do better?
          currentLimit=$(ulimit -n)

          # Run the pilot agent and Envoy
          # TODO: run with systemctl?
          export ISTIO_AGENT_FLAGS="--concurrency 2 --proxyLogLevel warning,misc:error,rbac:debug,jwt:debug"
          sudo -E -s /bin/bash -c "ulimit -n ${currentLimit}; exec /usr/local/bin/istio-start.sh&"

          /usr/local/bin/server --cluster "{{ $cluster }}" --version "{{ $subset.Version }}" \
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
             --crt=/var/lib/istio/cert.crt \
             --key=/var/lib/istio/cert.key
        env:
        - name: INSTANCE_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
        volumeMounts:
        - mountPath: /var/run/secrets/tokens
          name: {{ $.Service }}-istio-token
        - mountPath: /var/run/secrets/istio/bootstrap
          name: istio-vm-bootstrap
        {{- range $name, $value := $subset.Annotations }}
        {{- if eq $name.Name "sidecar.istio.io/bootstrapOverride" }}
        - mountPath: /etc/istio-custom-bootstrap
          name: custom-bootstrap-volume
        {{- end }}
        {{- end }}
{{- if $.IncludeExtAuthz }}
      - name: ext-authz
        image: {{ $.ImageHub }}/ext-authz:{{ $.ImageTag }}
        imagePullPolicy: {{ $.ImagePullPolicy }}
        ports:
        - containerPort: 8000
        - containerPort: 9000
{{- end }}
      volumes:
      - secret:
          secretName: {{ $.Service }}-istio-token
        name: {{ $.Service }}-istio-token
      - configMap:
          name: {{ $.Service }}-{{ $subset.Version }}-vm-bootstrap
        name: istio-vm-bootstrap
      {{- range $name, $value := $subset.Annotations }}
      {{- if eq $name.Name "sidecar.istio.io/bootstrapOverride" }}
      - name: custom-bootstrap-volume
        configMap:
          name: {{ $value.Value }}
      {{- end }}
      {{- end }}
{{- end}}
`
)

var (
	serviceTemplate      *template.Template
	deploymentTemplate   *template.Template
	vmDeploymentTemplate *template.Template
)

func init() {
	serviceTemplate = template.New("echo_service")
	if _, err := serviceTemplate.Funcs(sprig.TxtFuncMap()).Parse(serviceYAML); err != nil {
		panic(fmt.Sprintf("unable to parse echo service template: %v", err))
	}

	deploymentTemplate = template.New("echo_deployment")
	if _, err := deploymentTemplate.Funcs(sprig.TxtFuncMap()).Parse(deploymentYAML); err != nil {
		panic(fmt.Sprintf("unable to parse echo deployment template: %v", err))
	}

	vmDeploymentTemplate = template.New("echo_vm_deployment")
	if _, err := vmDeploymentTemplate.Funcs(sprig.TxtFuncMap()).Funcs(template.FuncMap{"Lines": lines}).Parse(vmDeploymentYaml); err != nil {
		panic(fmt.Sprintf("unable to parse echo vm deployment template: %v", err))
	}
}

var _ workloadHandler = &deployment{}

type deployment struct {
	ctx             resource.Context
	cfg             echo.Config
	shouldCreateWLE bool
}

func newDeployment(ctx resource.Context, cfg echo.Config) (*deployment, error) {
	if !cfg.Cluster.IsConfig() && cfg.DeployAsVM {
		return nil, fmt.Errorf("cannot deploy %s/%s as VM on non-config %s",
			cfg.Namespace.Name(),
			cfg.Service,
			cfg.Cluster.Name())
	}

	if cfg.DeployAsVM {
		if err := createVMConfig(ctx, cfg); err != nil {
			return nil, fmt.Errorf("failed creating vm config for %s/%s: %v",
				cfg.Namespace.Name(),
				cfg.Service,
				err)
		}
	}

	deploymentYAML, err := GenerateDeployment(ctx, cfg, ctx.Settings())
	if err != nil {
		return nil, fmt.Errorf("failed generating echo deployment YAML for %s/%s: %v",
			cfg.Namespace.Name(),
			cfg.Service, err)
	}

	// Apply the deployment to the configured cluster.
	if err = ctx.ConfigKube(cfg.Cluster).
		YAML(cfg.Namespace.Name(), deploymentYAML).
		Apply(apply.NoCleanup); err != nil {
		return nil, fmt.Errorf("failed deploying echo %s to cluster %s: %v",
			cfg.ClusterLocalFQDN(), cfg.Cluster.Name(), err)
	}

	return &deployment{
		ctx:             ctx,
		cfg:             cfg,
		shouldCreateWLE: cfg.DeployAsVM && !cfg.AutoRegisterVM,
	}, nil
}

// Restart performs a `kubectl rollout restart` on the echo deployment and waits for
// `kubectl rollout status` to complete before returning.
func (d *deployment) Restart() error {
	var errs error
	var deploymentNames []string
	for _, s := range d.cfg.Subsets {
		// TODO(Monkeyanator) move to common place so doesn't fall out of sync with templates
		deploymentNames = append(deploymentNames, fmt.Sprintf("%s-%s", d.cfg.Service, s.Version))
	}
	for _, deploymentName := range deploymentNames {
		wlType := "deployment"
		if d.cfg.IsStatefulSet() {
			wlType = "statefulset"
		}
		rolloutCmd := fmt.Sprintf("kubectl rollout restart %s/%s -n %s",
			wlType, deploymentName, d.cfg.Namespace.Name())
		if _, err := shell.Execute(true, rolloutCmd); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("failed to rollout restart %v/%v: %v",
				d.cfg.Namespace.Name(), deploymentName, err))
			continue
		}
		waitCmd := fmt.Sprintf("kubectl rollout status %s/%s -n %s",
			wlType, deploymentName, d.cfg.Namespace.Name())
		if _, err := shell.Execute(true, waitCmd); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("failed to wait rollout status for %v/%v: %v",
				d.cfg.Namespace.Name(), deploymentName, err))
		}
	}
	return errs
}

func (d *deployment) WorkloadReady(w *workload) {
	if !d.shouldCreateWLE {
		return
	}

	// Deploy the workload entry to the primary cluster. We will read WorkloadEntry across clusters.
	wle := d.workloadEntryYAML(w)
	if err := d.ctx.ConfigKube(d.cfg.Cluster.Primary()).
		YAML(d.cfg.Namespace.Name(), wle).
		Apply(apply.NoCleanup); err != nil {
		log.Warnf("failed deploying echo WLE for %s/%s to primary cluster: %v",
			d.cfg.Namespace.Name(),
			d.cfg.Service,
			err)
	}
}

func (d *deployment) WorkloadNotReady(w *workload) {
	if !d.shouldCreateWLE {
		return
	}

	wle := d.workloadEntryYAML(w)
	if err := d.ctx.ConfigKube(d.cfg.Cluster.Primary()).YAML(d.cfg.Namespace.Name(), wle).Delete(); err != nil {
		log.Warnf("failed deleting echo WLE for %s/%s from primary cluster: %v",
			d.cfg.Namespace.Name(),
			d.cfg.Service,
			err)
	}
}

func (d *deployment) workloadEntryYAML(w *workload) string {
	name := w.pod.Name
	podIP := w.pod.Status.PodIP
	sa := serviceAccount(d.cfg)
	network := d.cfg.Cluster.NetworkName()
	service := d.cfg.Service
	version := w.pod.Labels[constants.TestVMVersionLabel]

	return fmt.Sprintf(`
apiVersion: networking.istio.io/v1alpha3
kind: WorkloadEntry
metadata:
  name: %s
spec:
  address: %s
  serviceAccount: %s
  network: %q
  labels:
    app: %s
    version: %s
`, name, podIP, sa, network, service, version)
}

func GenerateDeployment(ctx resource.Context, cfg echo.Config, settings *resource.Settings) (string, error) {
	if settings == nil {
		var err error
		settings, err = resource.SettingsFromCommandLine("template")
		if err != nil {
			return "", err
		}
	}

	params, err := deploymentParams(ctx, cfg, settings)
	if err != nil {
		return "", err
	}

	deploy := deploymentTemplate
	if cfg.DeployAsVM {
		deploy = vmDeploymentTemplate
	}

	return tmpl.Execute(deploy, params)
}

func GenerateService(cfg echo.Config) (string, error) {
	params := ServiceParams(cfg)
	return tmpl.Execute(serviceTemplate, params)
}

var VMImages = map[echo.VMDistro]string{
	echo.UbuntuXenial: "app_sidecar_ubuntu_xenial",
	echo.UbuntuJammy:  "app_sidecar_ubuntu_jammy",
	echo.Debian11:     "app_sidecar_debian_11",
	echo.Centos7:      "app_sidecar_centos_7",
	// echo.Rockylinux8:  "app_sidecar_rockylinux_8", TODO(https://github.com/istio/istio/issues/38224)
}

var RevVMImages = func() map[string]echo.VMDistro {
	r := map[string]echo.VMDistro{}
	for k, v := range VMImages {
		r[v] = k
	}
	return r
}()

// getVMOverrideForIstiodDNS returns the DNS alias to use for istiod on VMs. VMs always access
// istiod via the east-west gateway, even though they are installed on the same cluster as istiod.
func getVMOverrideForIstiodDNS(ctx resource.Context, cfg echo.Config) (istioHost string, istioIP string) {
	if ctx == nil {
		return
	}

	ist, err := istio.Get(ctx)
	if err != nil {
		log.Warnf("VM config failed to get Istio component for %s: %v", cfg.Cluster.Name(), err)
		return
	}

	// Generate the istiod host the same way as istioctl.
	istioNS := ist.Settings().SystemNamespace
	istioRevision := getIstioRevision(cfg.Namespace)
	istioHost = istioctlcmd.IstiodHost(istioNS, istioRevision)

	istioIP = ist.EastWestGatewayFor(cfg.Cluster).DiscoveryAddress().IP.String()
	if istioIP == "<nil>" {
		log.Warnf("VM config failed to get east-west gateway IP for %s", cfg.Cluster.Name())
		istioHost, istioIP = "", ""
	}
	return
}

func deploymentParams(ctx resource.Context, cfg echo.Config, settings *resource.Settings) (map[string]interface{}, error) {
	supportStartupProbe := cfg.Cluster.MinKubeVersion(0)
	imagePullSecretName, err := settings.Image.PullSecretName()
	if err != nil {
		return nil, err
	}

	containerPorts := getContainerPorts(cfg)
	appContainers := []map[string]interface{}{{
		"Name":           appContainerName,
		"ImageFullPath":  settings.EchoImage, // This overrides image hub/tag if it's not empty.
		"ContainerPorts": getContainerPorts(cfg),
	}}

	// Only use the custom image for proxyless gRPC instances. This will bind the gRPC ports on one container
	// and all other ports on another. Additionally, we bind one port for communication between the custom image
	// container, and the regular Go server.
	if cfg.IsProxylessGRPC() && settings.CustomGRPCEchoImage != "" {
		var grpcPorts, otherPorts echoCommon.PortList
		for _, port := range containerPorts {
			if port.Protocol == protocol.GRPC {
				grpcPorts = append(grpcPorts, port)
			} else {
				otherPorts = append(otherPorts, port)
			}
		}
		otherPorts = append(otherPorts, &echoCommon.Port{
			Name:     "grpc-fallback",
			Protocol: protocol.GRPC,
			Port:     grpcFallbackPort,
		})
		appContainers[0]["ContainerPorts"] = otherPorts
		appContainers = append(appContainers, map[string]interface{}{
			"Name":           "custom-grpc-" + appContainerName,
			"ImageFullPath":  settings.CustomGRPCEchoImage, // This overrides image hub/tag if it's not empty.
			"ContainerPorts": grpcPorts,
			"FallbackPort":   grpcFallbackPort,
		})
	}

	params := map[string]interface{}{
		"ImageHub":            settings.Image.Hub,
		"ImageTag":            strings.TrimSuffix(settings.Image.Tag, "-distroless"),
		"ImagePullPolicy":     settings.Image.PullPolicy,
		"ImagePullSecretName": imagePullSecretName,
		"Service":             cfg.Service,
		"StatefulSet":         cfg.StatefulSet,
		"ProxylessGRPC":       cfg.IsProxylessGRPC(),
		"GRPCMagicPort":       grpcMagicPort,
		"Locality":            cfg.Locality,
		"ServiceAccount":      cfg.ServiceAccount,
		"AppContainers":       appContainers,
		"ContainerPorts":      getContainerPorts(cfg),
		"Subsets":             cfg.Subsets,
		"TLSSettings":         cfg.TLSSettings,
		"Cluster":             cfg.Cluster.Name(),
		"ReadinessTCPPort":    cfg.ReadinessTCPPort,
		"ReadinessGRPCPort":   cfg.ReadinessGRPCPort,
		"StartupProbe":        supportStartupProbe,
		"IncludeExtAuthz":     cfg.IncludeExtAuthz,
		"Revisions":           settings.Revisions.TemplateMap(),
		"Compatibility":       settings.Compatibility,
		"WorkloadClass":       cfg.WorkloadClass(),
		"OverlayIstioProxy":   canCreateIstioProxy(settings.Revisions.Minimum()),
	}

	vmIstioHost, vmIstioIP := "", ""
	if cfg.IsVM() {
		vmImage := VMImages[cfg.VMDistro]
		_, knownImage := RevVMImages[cfg.VMDistro]
		if vmImage == "" {
			if knownImage {
				vmImage = cfg.VMDistro
			} else {
				vmImage = VMImages[echo.DefaultVMDistro]
			}
			log.Debugf("no image for distro %s, defaulting to %s", cfg.VMDistro, echo.DefaultVMDistro)
		}

		vmIstioHost, vmIstioIP = getVMOverrideForIstiodDNS(ctx, cfg)

		params["VM"] = map[string]interface{}{
			"Image":     vmImage,
			"IstioHost": vmIstioHost,
			"IstioIP":   vmIstioIP,
		}
	}

	return params, nil
}

// TODO Similar to TemplateParams ancestor make this exported in OSS rather
// than in this fork
func ServiceParams(cfg echo.Config) map[string]interface{} {
	return map[string]interface{}{
		"Service":            cfg.Service,
		"Headless":           cfg.Headless,
		"ServiceAccount":     cfg.ServiceAccount,
		"ServicePorts":       cfg.Ports.GetServicePorts(),
		"ServiceAnnotations": cfg.ServiceAnnotations,
		"IPFamilies":         cfg.IPFamilies,
		"IPFamilyPolicy":     cfg.IPFamilyPolicy,
	}
}

func lines(input string) []string {
	out := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

// createVMConfig sets up a Service account,
func createVMConfig(ctx resource.Context, cfg echo.Config) error {
	istioCtl, err := istioctl.New(ctx, istioctl.Config{Cluster: cfg.Cluster})
	if err != nil {
		return err
	}
	// generate config files for VM bootstrap
	dirname := fmt.Sprintf("%s-vm-config-", cfg.Service)
	dir, err := ctx.CreateDirectory(dirname)
	if err != nil {
		return err
	}

	wg := tmpl.MustEvaluate(`
apiVersion: networking.istio.io/v1alpha3
kind: WorkloadGroup
metadata:
  name: {{.name}}
  namespace: {{.namespace}}
spec:
  metadata:
    labels:
      app: {{.name}}
      test.istio.io/class: {{ .workloadClass }}
  template:
    serviceAccount: {{.serviceAccount}}
    network: "{{.network}}"
  probe:
    failureThreshold: 5
    httpGet:
      path: /
      port: 8080
    periodSeconds: 2
    successThreshold: 1
    timeoutSeconds: 2

`, map[string]string{
		"name":           cfg.Service,
		"namespace":      cfg.Namespace.Name(),
		"serviceAccount": serviceAccount(cfg),
		"network":        cfg.Cluster.NetworkName(),
		"workloadClass":  cfg.WorkloadClass(),
	})

	// Push the WorkloadGroup for auto-registration
	if cfg.AutoRegisterVM {
		if err := ctx.ConfigKube(cfg.Cluster).
			YAML(cfg.Namespace.Name(), wg).
			Apply(apply.NoCleanup); err != nil {
			return err
		}
	}

	if cfg.ServiceAccount {
		// create service account, the next workload command will use it to generate a token
		err = createServiceAccount(cfg.Cluster.Kube(), cfg.Namespace.Name(), serviceAccount(cfg))
		if err != nil && !kerrors.IsAlreadyExists(err) {
			return err
		}
	}

	if err := os.WriteFile(path.Join(dir, "workloadgroup.yaml"), []byte(wg), 0o600); err != nil {
		return err
	}

	ist, err := istio.Get(ctx)
	if err != nil {
		return err
	}
	// this will wait until the eastwest gateway has an IP before running the next command
	istiodAddr, err := ist.RemoteDiscoveryAddressFor(cfg.Cluster)
	if err != nil {
		return err
	}

	var subsetDir string
	for _, subset := range cfg.Subsets {
		subsetDir, err = os.MkdirTemp(dir, subset.Version+"-")
		if err != nil {
			return err
		}
		cmd := []string{
			"x", "workload", "entry", "configure",
			"-f", path.Join(dir, "workloadgroup.yaml"),
			"-o", subsetDir,
		}
		if ctx.Clusters().IsMulticluster() {
			// When VMs talk about "cluster", they refer to the cluster they connect to for discovery
			cmd = append(cmd, "--clusterID", cfg.Cluster.Name())
		}
		if cfg.AutoRegisterVM {
			cmd = append(cmd, "--autoregister")
		}
		if !ctx.Environment().(*kube.Environment).Settings().LoadBalancerSupported {
			// LoadBalancer may not be supported and the command doesn't have NodePort fallback logic that the tests do
			cmd = append(cmd, "--ingressIP", istiodAddr.IP.String())
		}
		if rev := getIstioRevision(cfg.Namespace); len(rev) > 0 {
			cmd = append(cmd, "--revision", rev)
		}
		// make sure namespace controller has time to create root-cert ConfigMap
		if err := retry.UntilSuccess(func() error {
			stdout, stderr, err := istioCtl.Invoke(cmd)
			if err != nil {
				return fmt.Errorf("%v:\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			return nil
		}, retry.Timeout(20*time.Second)); err != nil {
			return err
		}

		// support proxyConfig customizations on VMs via annotation in the echo API.
		for k, v := range subset.Annotations {
			if k.Name == "proxy.istio.io/config" {
				if err := patchProxyConfigFile(path.Join(subsetDir, "mesh.yaml"), v.Value); err != nil {
					return fmt.Errorf("failed patching proxyconfig: %v", err)
				}
			}
		}

		if err := customizeVMEnvironment(ctx, cfg, path.Join(subsetDir, "cluster.env"), istiodAddr); err != nil {
			return fmt.Errorf("failed customizing cluster.env: %v", err)
		}

		// push boostrap config as a ConfigMap so we can mount it on our "vm" pods
		cmData := map[string][]byte{}
		generatedFiles, err := os.ReadDir(subsetDir)
		if err != nil {
			return err
		}
		for _, file := range generatedFiles {
			if file.IsDir() {
				continue
			}
			cmData[file.Name()], err = os.ReadFile(path.Join(subsetDir, file.Name()))
			if err != nil {
				return err
			}
		}
		cmName := fmt.Sprintf("%s-%s-vm-bootstrap", cfg.Service, subset.Version)
		cm := &kubeCore.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName}, BinaryData: cmData}
		_, err = cfg.Cluster.Kube().CoreV1().ConfigMaps(cfg.Namespace.Name()).Create(context.TODO(), cm, metav1.CreateOptions{})
		if err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed creating configmap %s: %v", cm.Name, err)
		}
	}

	// push the generated token as a Secret (only need one, they should be identical)
	token, err := os.ReadFile(path.Join(subsetDir, "istio-token"))
	if err != nil {
		return err
	}
	secret := &kubeCore.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Service + "-istio-token",
			Namespace: cfg.Namespace.Name(),
		},
		Data: map[string][]byte{
			"istio-token": token,
		},
	}
	if _, err := cfg.Cluster.Kube().CoreV1().Secrets(cfg.Namespace.Name()).Create(context.TODO(), secret, metav1.CreateOptions{}); err != nil {
		if kerrors.IsAlreadyExists(err) {
			if _, err := cfg.Cluster.Kube().CoreV1().Secrets(cfg.Namespace.Name()).Update(context.TODO(), secret, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("failed updating secret %s: %v", secret.Name, err)
			}
		} else {
			return fmt.Errorf("failed creating secret %s: %v", secret.Name, err)
		}
	}

	return nil
}

func patchProxyConfigFile(file string, overrides string) error {
	config, err := readMeshConfig(file)
	if err != nil {
		return err
	}
	overrideYAML := "defaultConfig:\n"
	overrideYAML += istio.Indent(overrides, "  ")
	if err := protomarshal.ApplyYAML(overrideYAML, config.DefaultConfig); err != nil {
		return err
	}
	outYAML, err := protomarshal.ToYAML(config)
	if err != nil {
		return err
	}
	return os.WriteFile(file, []byte(outYAML), 0o744)
}

func readMeshConfig(file string) (*meshconfig.MeshConfig, error) {
	baseYAML, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	config := &meshconfig.MeshConfig{}
	if err := protomarshal.ApplyYAML(string(baseYAML), config); err != nil {
		return nil, err
	}
	return config, nil
}

func createServiceAccount(client kubernetes.Interface, ns string, serviceAccount string) error {
	scopes.Framework.Debugf("Creating service account for: %s/%s", ns, serviceAccount)
	_, err := client.CoreV1().ServiceAccounts(ns).Create(context.TODO(), &kubeCore.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccount},
	}, metav1.CreateOptions{})
	return err
}

// getContainerPorts converts the ports to a port list of container ports.
// Adds ports for health/readiness if necessary.
func getContainerPorts(cfg echo.Config) echoCommon.PortList {
	ports := cfg.Ports
	containerPorts := make(echoCommon.PortList, 0, len(ports))
	var healthPort *echoCommon.Port
	var readyPort *echoCommon.Port
	for _, p := range ports {
		// Add the port to the set of application ports.
		cport := &echoCommon.Port{
			Name:        p.Name,
			Protocol:    p.Protocol,
			Port:        p.WorkloadPort,
			TLS:         p.TLS,
			ServerFirst: p.ServerFirst,
			InstanceIP:  p.InstanceIP,
			LocalhostIP: p.LocalhostIP,
		}
		containerPorts = append(containerPorts, cport)

		switch p.Protocol {
		case protocol.GRPC:
			if cfg.IsProxylessGRPC() {
				cport.XDSServer = true
			}
			continue
		case protocol.HTTP:
			if p.WorkloadPort == httpReadinessPort {
				readyPort = cport
			}
		default:
			if p.WorkloadPort == tcpHealthPort {
				healthPort = cport
			}
		}
	}

	// If we haven't added the readiness/health ports, do so now.
	if readyPort == nil {
		containerPorts = append(containerPorts, &echoCommon.Port{
			Name:     "http-readiness-port",
			Protocol: protocol.HTTP,
			Port:     httpReadinessPort,
		})
	}
	if healthPort == nil {
		containerPorts = append(containerPorts, &echoCommon.Port{
			Name:     "tcp-health-port",
			Protocol: protocol.HTTP,
			Port:     tcpHealthPort,
		})
	}

	// gives something the test runner to connect to without being in the mesh
	if cfg.IsProxylessGRPC() {
		containerPorts = append(containerPorts, &echoCommon.Port{
			Name:        "grpc-magic-port",
			Protocol:    protocol.GRPC,
			Port:        grpcMagicPort,
			LocalhostIP: true,
		})
	}
	return containerPorts
}

func customizeVMEnvironment(ctx resource.Context, cfg echo.Config, clusterEnv string, istiodAddr net.TCPAddr) error {
	f, err := os.OpenFile(clusterEnv, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return fmt.Errorf("failed opening %s: %v", clusterEnv, err)
	}
	defer f.Close()

	if cfg.VMEnvironment != nil {
		for k, v := range cfg.VMEnvironment {
			addition := fmt.Sprintf("%s=%s\n", k, v)
			_, err = f.Write([]byte(addition))
			if err != nil {
				return fmt.Errorf("failed writing %q to %s: %v", addition, clusterEnv, err)
			}
		}
	}
	if !ctx.Environment().(*kube.Environment).Settings().LoadBalancerSupported {
		// customize cluster.env with NodePort mapping
		_, err = f.Write([]byte(fmt.Sprintf("ISTIO_PILOT_PORT=%d\n", istiodAddr.Port)))
		if err != nil {
			return err
		}
	}
	return err
}

func canCreateIstioProxy(version resource.IstioVersion) bool {
	// if no revision specified create the istio-proxy
	if string(version) == "" {
		return true
	}
	if minor := strings.Split(string(version), ".")[1]; minor > "8" || len(minor) > 1 {
		return true
	}
	return false
}

func getIstioRevision(n namespace.Instance) string {
	nsLabels, err := n.Labels()
	if err != nil {
		log.Warnf("failed fetching labels for %s; assuming no-revision (can cause failures): %v", n.Name(), err)
		return ""
	}
	return nsLabels[label.IoIstioRev.Name]
}
