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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/fatih/color"
	protobuf "github.com/gogo/protobuf/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	operator "istio.io/api/operator/v1alpha1"
	"istio.io/istio/operator/pkg/apis/istio/v1alpha1"
	"istio.io/istio/operator/pkg/component"
	"istio.io/istio/operator/pkg/helm"
	"istio.io/istio/operator/pkg/manifest"
	"istio.io/istio/operator/pkg/tpath"
	"istio.io/istio/operator/pkg/translate"
	"istio.io/istio/operator/pkg/util"
	"istio.io/istio/operator/pkg/validate"
)

// We need to lazy load or we emit some logs that make all istioctl commands spam logs
var (
	defaultProfileSpec interface{}
	profileOnce        = sync.Once{}
)

func appendWarning(warnings []string, node interface{}, path string, warning string) []string {
	profileOnce.Do(func() {
		profileYAML, err := helm.GetProfileYAML("", "default")
		if err != nil {
			panic(err.Error())
		}
		profileIOP, err := validate.UnmarshalIOP(profileYAML)
		if err != nil {
			panic(err.Error())
		}
		defaultProfileSpec = profileIOP.Spec
	})
	v, f, _ := tpath.GetFromStructPath(node, path)
	defaultVal, defaultF, _ := tpath.GetFromStructPath(defaultProfileSpec, path)
	if f {
		if defaultF && reflect.DeepEqual(defaultVal, v) {
			return warnings
		}
		if cv, ok := v.(*protobuf.BoolValue); ok {
			v = cv.GetValue()
		}
		switch reflect.TypeOf(v).Kind() {
		case reflect.Struct, reflect.Map, reflect.Ptr:
			v = ""
		}
		if v == "" {
			warnings = append(warnings, fmt.Sprintf("%v: %v", path, warning))
		} else {
			warnings = append(warnings, fmt.Sprintf("%v=%v: %v", path, v, warning))
		}
	}
	return warnings
}

// runMcpCheck runs the mcp migration check
func runMcpCheck(w io.Writer, filenames []string, outDir string, revision string) error {
	switch revision {
	case "asm-managed", "asm-managed-stable":
		return fmt.Errorf(`currently only "asm-managed-rapid" --revision is supported (have: %q)`, revision)
	case "asm-managed-rapid":
	default:
		return fmt.Errorf("unknown revision: %v", revision)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %v: %v", outDir, err)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "gateways-istiooperator"), 0o755); err != nil {
		return fmt.Errorf("failed to create %v: %v", outDir, err)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "gateways-kubernetes"), 0o755); err != nil {
		return fmt.Errorf("failed to create %v: %v", outDir, err)
	}
	y, err := manifest.ReadLayeredYAMLs(filenames)
	if err != nil {
		return fmt.Errorf("failed to read files: %v", err)
	}
	originalIop, err := validate.UnmarshalIOP(y)
	if err != nil {
		return err
	}
	profileYAML, err := helm.GetProfileYAML("", originalIop.Spec.Profile)
	if err != nil {
		panic(err.Error())
	}
	overlay, err := util.OverlayIOP(profileYAML, y)
	if err != nil {
		return err
	}
	iop, err := validate.UnmarshalIOP(overlay)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, color.New(color.FgBlue).
		Sprint("Generating equivalent configuration for Anthos Service Mesh managed control plane..."))

	warnings, info, configDeleteList := generateWarnings(iop.Spec)

	var meshConfigFilename string
	// First we will check mesh config. Users can set this, but they need to do it by configmap.
	// A few settings doe nothing, so we warn about those.
	if len(v1alpha1.AsMap(originalIop.Spec.MeshConfig)) > 0 {
		fmt.Fprintln(w, color.New(color.FgBlue).Sprint("\nMigrating MeshConfig settings..."))
		cm, err := constructMeshConfigmap(v1alpha1.AsMap(originalIop.Spec.MeshConfig), revision, configDeleteList)
		if err != nil {
			return err
		}
		meshConfigFilename = filepath.Join(outDir, "meshconfig.yaml")
		if err := writeYAMLFile(meshConfigFilename, []byte(cm)); err != nil {
			return err
		}

		fmt.Fprintf(w, color.New(color.FgGreen).Sprint("✔")+
			" Wrote MeshConfig to %v.\n", meshConfigFilename)
	}

	profileIOP, err := validate.UnmarshalIOP(profileYAML)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, color.New(color.FgBlue).Sprint("\nMigrating gateway deployments..."))
	writeGateway := func(newIOP *v1alpha1.IstioOperator, gwType string) error {
		// We will output two forms of the gateway.
		// * IstioOperator, but stripped down and made to be one gateway per IstioOperator.
		// * Plain kubernetes YAML, by rendering the IstioOperator above.
		if newIOP == nil {
			return nil
		}
		by, err := yaml.Marshal(newIOP)
		if err != nil {
			return err
		}
		filename := filepath.Join(outDir, "gateways-istiooperator", newIOP.Name+".yaml")
		if err := writeYAMLFile(filename, by); err != nil {
			return err
		}
		fmt.Fprintf(w, color.New(color.FgGreen).Sprint("✔")+
			" Wrote Gateway to %v\n", filename)

		overlay, err := util.OverlayIOP(profileYAML, string(by))
		if err != nil {
			return err
		}
		fullIOP, err := validate.UnmarshalIOP(overlay)
		if err != nil {
			return err
		}

		gwSpec := fullIOP.Spec.GetComponents().GetIngressGateways()[0]
		opts := &component.Options{
			InstallSpec: fullIOP.Spec,
			Translator:  translate.NewTranslator(),
			Namespace:   gwSpec.Namespace,
		}
		var c component.IstioComponent
		switch gwType {
		case "istio-ingressgateway":
			c = component.NewIngressComponent(gwSpec.Name, 0, gwSpec, opts)
		case "istio-egressgateway":
			c = component.NewEgressComponent(gwSpec.Name, 0, gwSpec, opts)
		}
		if err := c.Run(); err != nil {
			return err
		}
		y, err := c.RenderManifest()
		if err != nil {
			return err
		}
		filename = filepath.Join(outDir, "gateways-kubernetes", fullIOP.Name+".yaml")
		if err := writeYAMLFile(filename, []byte(y)); err != nil {
			return err
		}
		fmt.Fprintf(w, color.New(color.FgGreen).Sprint("✔")+
			" Wrote Gateway to %v\n", filename)

		return nil
	}

	gateways := 0
	handledDefaultGateway := false
	for _, gw := range originalIop.Spec.GetComponents().GetIngressGateways() {
		if gw.Name == "istio-ingressgateway" {
			handledDefaultGateway = true
		}
		gwIOP := extractGateway(originalIop.DeepCopy(), gw, "istio-ingressgateway", revision, configDeleteList)
		if gwIOP != nil {
			gateways++
		}
		if err := writeGateway(gwIOP, "istio-ingressgateway"); err != nil {
			return fmt.Errorf("failed to extract gateway: %v", err)
		}
	}
	// The default install has a gateway, so make sure we include that if needed
	if !handledDefaultGateway && hasEnabledGateway(profileIOP.Spec.GetComponents().GetIngressGateways()) {
		gwIOP := extractGateway(originalIop.DeepCopy(), &operator.GatewaySpec{
			Name:    "istio-ingressgateway",
			Enabled: &protobuf.BoolValue{Value: true},
		}, "istio-ingressgateway", revision, configDeleteList)
		if gwIOP != nil {
			gateways++
		}
		if err := writeGateway(gwIOP, "istio-ingressgateway"); err != nil {
			return fmt.Errorf("failed to extract gateway: %v", err)
		}
	}
	for _, gw := range originalIop.Spec.GetComponents().GetEgressGateways() {
		gwIOP := extractGateway(originalIop.DeepCopy(), gw, "istio-egressgateway", revision, configDeleteList)
		if gwIOP != nil {
			gateways++
		}
		if err := writeGateway(gwIOP, "istio-egressgateway"); err != nil {
			return fmt.Errorf("failed to extract gateway: %v", err)
		}
	}

	fmt.Fprintln(w, color.New(color.FgBlue).Sprint("\nChecking configuration compatibility..."))
	if len(warnings) > 0 {
		fmt.Fprintf(w, color.New(color.FgYellow).Sprint("!")+
			" Found unsupported configurations:\n")
	} else {
		fmt.Fprintf(w, color.New(color.FgGreen).Sprint("✔")+
			" No incompatible configuration detected\n")
	}
	for _, warning := range warnings {
		fmt.Fprintln(w, "    "+warning)
	}
	if len(info) > 0 {
		fmt.Fprintf(w, color.New(color.FgYellow).Sprint("!")+
			" Found configurations that may require migration:\n")
	}
	for _, f := range info {
		fmt.Fprintln(w, "    "+f)
	}

	actionItems := len(warnings) > 0 || len(meshConfigFilename) > 0 || gateways > 0
	if !actionItems {
		fmt.Fprintln(w, color.New(color.FgBlue).Sprint("\nNo actions required to migrate!"))
		return nil
	}
	fmt.Fprintln(w, color.New(color.FgBlue).Sprint("\nActions required to migrate:"))
	if len(warnings) > 0 {
		// nolint: lll
		fmt.Fprintf(w, color.New(color.FgYellow).Sprint("!")+" Found potentially unsupported configurations; review warnings above before proceeding\n")
	}
	if len(meshConfigFilename) > 0 {
		// nolint: lll
		fmt.Fprintf(w, "- Found custom mesh configuration settings. To apply these settings to ASM managed control plane, run: `kubectl apply -f '%s'`\n", meshConfigFilename)
	}
	// nolint: lll
	if gateways > 0 {
		fmt.Fprintf(w, `- Found %d gateway deployments. There are two options available to deploy these to ASM managed control plane. Both options will provide the same end result:

(1) Simple Kubernetes Configuration (recommended)

This option contains all of the standard Kubernetes configution, such as Deployment and Services, to deploy a gateway.
If you want to modify the configuration in the future, you can modify these configurations directly.
To deploy these to the cluster, run `+"`kubectl apply -f '%s/gateways-kubernetes'`"+` after installing ASM managed control plane.

(2) Continue to use IstioOperator

If you would prefer to continue to use the same IstioOperator API, you may continue to do so.
The '%s/gateways-istiooperator' directory contains an IstioOperator per configured gateway. These have been modified to be compatible with ASM managed control plane.
To deploy these to the cluster, run, for each gateway, one of the following commands:
`+"- `istioctl install -f '%s/gateways-istiooperator/GATEWAY_NAME.yaml'`"+`
`+"- `istioctl manifest generate -f '%s/gateways-istiooperator/GATEWAY_NAME.yaml' | kubectl apply -f -`"+`
`,
			gateways, outDir, outDir, outDir, outDir)
	}

	fmt.Fprintf(w, "\nTIP: steps recommending `kubectl apply` to be run should be integrated into your CI/CD pipeline, if applicable.\n")
	if len(warnings) > 0 {
		return errorUnsupportedConfig
	}
	return nil
}

func hasEnabledGateway(gateways []*operator.GatewaySpec) bool {
	for _, g := range gateways {
		if g.Enabled.GetValue() {
			return true
		}
	}
	return false
}

func extractGateway(iop *v1alpha1.IstioOperator, gw *operator.GatewaySpec, gwName string, revision string, configDeleteList []string) *v1alpha1.IstioOperator {
	if gw.Enabled != nil && !gw.Enabled.Value {
		return nil
	}
	spec := iop.DeepCopy().Spec
	values := v1alpha1.AsMap(spec.Values)

	// Remove values we do not want the user to attempt to configure
	for _, v := range configDeleteList {
		if !strings.HasPrefix(v, "Values.") {
			continue
		}
		_, _ = tpath.Delete(values, util.ToYAMLPath(strings.TrimPrefix(v, "Values.")))
	}

	if values == nil {
		values = map[string]interface{}{}
	}
	if _, f := values["gateways"]; !f {
		values["gateways"] = map[string]interface{}{}
	}
	valuesGateways := values["gateways"].(map[string]interface{})
	if _, f := valuesGateways[gwName]; !f {
		valuesGateways[gwName] = map[string]interface{}{}
	}
	valuesIngressGateways := valuesGateways[gwName].(map[string]interface{})
	valuesIngressGateways["injectionTemplate"] = "gateway"

	namespace := gw.Namespace
	if namespace == "" {
		namespace = iop.Namespace
	}
	if namespace == "" {
		namespace = "istio-system"
	}

	newIOP := &v1alpha1.IstioOperator{
		Kind:       iop.Kind,
		ApiVersion: iop.ApiVersion,
		Spec: &operator.IstioOperatorSpec{
			Profile:  "empty",
			Revision: revision,
			Components: &operator.IstioComponentSetSpec{
				IngressGateways: []*operator.GatewaySpec{{
					Enabled:   gw.Enabled,
					Namespace: namespace,
					Name:      gw.Name,
					Label:     gw.Label,
					K8S:       gw.K8S,
				}},
			},
			Values: v1alpha1.MustNewStruct(values),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      gw.Name,
			Namespace: iop.Namespace,
		},
		TypeMeta: iop.TypeMeta,
	}
	return newIOP
}

func constructMeshConfigmap(config map[string]interface{}, revision string, deleteList []string) (string, error) {
	for _, v := range deleteList {
		if !strings.HasPrefix(v, "MeshConfig.") {
			continue
		}
		_, _ = tpath.Delete(config, util.ToYAMLPath(strings.TrimPrefix(v, "MeshConfig.")))
	}
	mcs, err := yaml.Marshal(config)
	if err != nil {
		return "", err
	}
	mc := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-" + revision,
			Namespace: "istio-system",
		},
		Data: map[string]string{"mesh": string(mcs)},
	}
	cms, err := yaml.Marshal(mc)
	if err != nil {
		return "", err
	}
	return string(cms), nil
}

// writeYAMLFile writes a file, bypassing annoying marshaling quirks
func writeYAMLFile(fname string, data []byte) error {
	data = bytes.Replace(data, []byte(`metadata:
  creationTimestamp: null`), []byte(`metadata:`), -1)
	data = bytes.Replace(data, []byte(`metadata:
  annotations: null`), []byte(`metadata:`), -1)
	return ioutil.WriteFile(fname, data, 0o644)
}

func generateWarnings(spec *operator.IstioOperatorSpec) ([]string, []string, []string) {
	// Next we generate a bunch of warnings and suggestions based on their configs
	warnings := []string{} // For unsupported configs
	info := []string{}     // For supported configs that need user action
	configDeleteList := []string{
		"Revision",
		"Values.revision",
	}
	for _, w := range []string{
		"MeshConfig.trustDomain",
		"MeshConfig.configSources",
		"MeshConfig.defaultConfig.proxyMetadata.OUTPUT_CERTS",
		"MeshConfig.defaultConfig.proxyMetadata.XDS_ROOT_CA",
		"MeshConfig.defaultConfig.proxyMetadata.CA_ROOT_CA",
		"MeshConfig.defaultConfig.proxyMetadata.XDS_AUTH_PROVIDER",
		"MeshConfig.defaultConfig.proxyMetadata.PROXY_CONFIG_XDS_AGENT",
		"MeshConfig.defaultConfig.meshId",
		"MeshConfig.defaultConfig.discoveryAddress",
	} {
		warnings = appendWarning(warnings, spec, w, "not configurable in managed control plane; this option will be ignored")
		configDeleteList = append(configDeleteList, w)
	}
	for _, w := range []string{
		"MeshConfig.defaultConfig.configPath",
		"MeshConfig.defaultConfig.binaryPath",
		"MeshConfig.defaultConfig.customConfigFile",
		"MeshConfig.defaultConfig.caCertificatesPem",
	} {
		warnings = appendWarning(warnings, spec, w, "is not supported in managed control plane and may not work")
		configDeleteList = append(configDeleteList, w)
	}

	for _, w := range []string{
		"Values.cni",
	} {
		warnings = appendWarning(warnings, spec, w, "CNI is supported, but customizing the CNI installation is not")
		configDeleteList = append(configDeleteList, w)
	}
	for _, w := range []string{
		"Values.global.logAsJson",
		"Values.global.proxy",
		"Values.global.proxy_init",
	} {
		// nolint: lll
		warnings = appendWarning(warnings, spec, w, "the injection template cannot be modified globally; per-pod settings can be customized (https://istio.io/latest/docs/setup/additional-setup/sidecar-injection/#customizing-injection)")
		configDeleteList = append(configDeleteList, w)
	}
	for _, w := range []string{
		"UnvalidatedValues",
		"AddonComponents",
		"Components.Base",
		"Components.IstiodRemote",
		"Values.global.istioNamespace",
		"Values.global.telemetryNamespace",
		"Values.global.prometheusNamespace",
		"Values.global.policyNamespace",
		"Components.Pilot",
		"Hub",
		"Values.global.hub",
		"Tag",
		"Values.global.tag",
		"Revision",
		"Values.revision",
		"Values.global.istiod",
		"Values.global.multiCluster",
		"Values.global.sds",
		"Values.global.caAddress",
		"Values.global.imagePullSecrets",
		"Values.global.meshID",
		"Values.global.mountMtlsCerts",
		"Values.global.network",
		"Values.global.sts",
		"Values.global.useMCP",
		"Values.global.imagePullPolicy",
		"Values.global.jwtPolicy",
		"Values.global.pilotCertProvider",
		"Values.global.trustDomain",
		"Values.global.remotePilotAddress",
		"Values.global.configValidation",
		"Values.global.omitSidecarInjectorConfigMap",
		"Values.global.network",
		"Values.global.operatorManageWebhooks",
		"Values.global.createRemoteSvcEndpoints",
		"Values.global.oneNamespace",
		"Values.global.logging",
		"Values.pilot",
		"Values.telemetry",
		"Values.sidecarInjectorWebhook",
		"Values.base",
		"Values.istiodRemote",
		"Values.revisionTags",
	} {
		warnings = appendWarning(warnings, spec, w, "not configurable in managed control plane")
		configDeleteList = append(configDeleteList, w)
	}
	for _, w := range []string{
		"Components.Cni",
		"Values.istio_cni",
	} {
		info = appendWarning(info, spec, w, "install with `--option cni-managed` option to enable CNI")
		configDeleteList = append(configDeleteList, w)
	}
	for _, w := range []string{
		"Values.global.proxy.tracer",
		"Values.global.tracer",
		"Values.pilot.traceSampling",
		"Values.global.meshNetworks",
	} {
		info = appendWarning(info, spec, w, "setting can be configured in MeshConfig")
		configDeleteList = append(configDeleteList, w)
	}

	return warnings, info, configDeleteList
}
