# Example and tests for running Istiod on CloudRun or KNative
# This is an experimental mode, to evaluate the k8s dependencies

# GOTEMPLATE_REPLACE is a small sed command to replace strings like gotemplatereplace_Revision with {{.Revision}}
# This is required because yaml like `revision: {{.Revision}}` is not valid (need quotes around the value).
# Rather than wrapping quotes around every value in the charts, we do a small amount of postprocessing.
# This additionally has a small benefit of being clear which is templated by helm and which is templated by MCP.
GOTEMPLATE_REPLACE = sed -E 's/gotemplatereplace_(\w+?)/{{.\1}}/g'

.PHONY: all-cloudrun
all-cloudrun: $(TARGET_OUT_LINUX)/knative/injection-template.yaml $(TARGET_OUT_LINUX)/knative/mutatingwebhook.yaml

$(TARGET_OUT_LINUX)/knative/injection-template.yaml: manifests/charts/istio-control/istio-discovery/files/injection-template.yaml manifests/charts/istio-control/istio-discovery/files/gateway-injection-template.yaml manifests/charts/istio-control/istio-discovery/files/kube-gateway.yaml
	mkdir -p $(TARGET_OUT_LINUX)/knative
	cp tools/packaging/knative/injection-template-extra.yaml ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	echo -e "\n  sidecar: |-" >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	cat manifests/charts/istio-control/istio-discovery/files/injection-template.yaml | ${GOTEMPLATE_REPLACE} | sed 's/^/    /g' >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	echo -e "\n  gateway: |-" >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	cat manifests/charts/istio-control/istio-discovery/files/gateway-injection-template.yaml | ${GOTEMPLATE_REPLACE} | sed 's/^/    /g' >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	echo -e "\n  kube-gateway: |-" >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml
	cat manifests/charts/istio-control/istio-discovery/files/kube-gateway.yaml | ${GOTEMPLATE_REPLACE} | sed 's/^/    /g' >> ${TARGET_OUT_LINUX}/knative/injection-template.yaml

$(TARGET_OUT_LINUX)/knative/mutatingwebhook.yaml: manifests/charts/istio-control/istio-discovery/templates/mutatingwebhook.yaml
	mkdir -p $(TARGET_OUT_LINUX)/knative-wh/templates
	cp tools/packaging/knative/istio.labels.yaml $(TARGET_OUT_LINUX)/knative-wh/templates
	cp manifests/charts/istio-control/istio-discovery/templates/mutatingwebhook.yaml ${TARGET_OUT_LINUX}/knative-wh/templates
	cp manifests/charts/istio-control/istio-discovery/Chart.yaml ${TARGET_OUT_LINUX}/knative-wh/Chart.yaml
	helm template istio $(TARGET_OUT_LINUX)/knative-wh -n istio-system -f tools/packaging/knative/values.webhook.yaml | ${GOTEMPLATE_REPLACE} | sed 's/name: istio-sidecar-injector-/name: istiod-/g' > $@
