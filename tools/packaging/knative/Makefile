# Example and tests for running Istiod on CloudRun or KNative
# This is an experimental mode, to evaluate the k8s dependencies

# Project used for testing the integration.
ISTIO_REGION ?= us-central1
ISTIO_PROJECT ?= asm-staging-images
ISTIO_SA ?= 601426346923-compute@developer.gserviceaccount.com
ZONE ?= us-central1-c
ISTIO_SUFFIX?=-icq63pqnqq-uc.a.run.app
# NOTE: The XDS_ADDR won't be correct if the service name (istiod-<project
# id>-<cluster>-<rev>) is too long and gets truncated.
XDS_ADDR=istiod-${PROJECT_ID}-${CLUSTER}-${REV}${ISTIO_SUFFIX}:443
CLOUDRUN_ADDR=${XDS_ADDR}

# Test project/cluster where workloads run.
PROJECT_ID ?= costin-asm1
CLUSTER ?= big1

CPU ?= 1
MEM ?= 2G

# Istiod revision to deploy
REV ?= oss

# Image tag
TAG ?= asm-cr

# Hub for the proxy
PROXY_HUB ?= grc.io/asm-staging-images

# GOTEMPLATE_REPLACE is a small sed command to replace strings like gotemplatereplace_Revision with {{.Revision}}
# This is required because yaml like `revision: {{.Revision}}` is not valid (need quotes around the value).
# Rather than wrapping quotes around every value in the charts, we do a small amount of postprocessing.
# This additionally has a small benefit of being clear which is templated by helm and which is templated by MCP.
GOTEMPLATE_REPLACE = sed -E 's/gotemplatereplace_(\w+?)/{{.\1}}/g'

.PHONY: all-cloudrun
all-cloudrun: $(ISTIO_OUT)/knative/telemetry-sd.yaml $(ISTIO_OUT)/knative/telemetry.yaml $(ISTIO_OUT)/knative/injection-template.yaml $(ISTIO_OUT)/knative/mutatingwebhook.yaml

$(ISTIO_OUT)/knative/telemetry-sd.yaml: manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.11.yaml manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.12.yaml manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.13.yaml tools/packaging/knative/values.telemetry-sd.yaml
	mkdir -p $(ISTIO_OUT)/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.11.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.12.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.13.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/Chart.yaml ${ISTIO_OUT}/knative/Chart.yaml
	helm template $(ISTIO_OUT)/knative -f tools/packaging/knative/values.telemetry-sd.yaml | ${GOTEMPLATE_REPLACE} > $@

$(ISTIO_OUT)/knative/telemetry.yaml: manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.11.yaml manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.12.yaml manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.13.yaml
	mkdir -p $(ISTIO_OUT)/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.11.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.12.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/templates/telemetryv2_1.13.yaml ${ISTIO_OUT}/knative/templates
	cp manifests/charts/istio-control/istio-discovery/Chart.yaml ${ISTIO_OUT}/knative/Chart.yaml
	helm template $(ISTIO_OUT)/knative -f tools/packaging/knative/values.telemetry.yaml | ${GOTEMPLATE_REPLACE} > $@

$(ISTIO_OUT)/knative/injection-template.yaml: manifests/charts/istio-control/istio-discovery/files/injection-template.yaml
	mkdir -p $(ISTIO_OUT)/knative
	cp tools/packaging/knative/injection-template-extra.yaml ${ISTIO_OUT}/knative/injection-template.yaml
	echo -e "\n  sidecar: |-" >> ${ISTIO_OUT}/knative/injection-template.yaml
	cat manifests/charts/istio-control/istio-discovery/files/injection-template.yaml | ${GOTEMPLATE_REPLACE} | sed 's/^/    /g' >> ${ISTIO_OUT}/knative/injection-template.yaml
	echo -e "\n  gateway: |-" >> ${ISTIO_OUT}/knative/injection-template.yaml
	cat manifests/charts/istio-control/istio-discovery/files/gateway-injection-template.yaml | ${GOTEMPLATE_REPLACE} | sed 's/^/    /g' >> ${ISTIO_OUT}/knative/injection-template.yaml

$(ISTIO_OUT)/knative/mutatingwebhook.yaml: manifests/charts/istio-control/istio-discovery/templates/mutatingwebhook.yaml
	mkdir -p $(ISTIO_OUT)/knative-wh/templates
	cp manifests/charts/istio-control/istio-discovery/templates/mutatingwebhook.yaml ${ISTIO_OUT}/knative-wh/templates
	cp manifests/charts/istio-control/istio-discovery/Chart.yaml ${ISTIO_OUT}/knative-wh/Chart.yaml
	helm template istio $(ISTIO_OUT)/knative-wh -n istio-system -f tools/packaging/knative/values.webhook.yaml | ${GOTEMPLATE_REPLACE} | sed 's/name: istio-sidecar-injector-/name: istiod-/g' > $@

# Setup the CloudRun Istiod revision
knative/setup:
	#gcloud config unset api_endpoint_overrides/run
	# Slow start - but pinned
	# Alternative: --cpu 4 --memory 4G
	gcloud alpha run deploy istiod-${PROJECT_ID}-${CLUSTER}-${REV} --allow-unauthenticated \
	 --project ${ISTIO_PROJECT} --region ${ISTIO_REGION} --platform managed \
     --image gcr.io/${ISTIO_PROJECT}/cloudrun:${TAG} \
     --set-env-vars=${ENVEXTRA}REV=${REV},TAG=${TAG},CLUSTER=${CLUSTER},ZONE=${ZONE},PROJECT=${PROJECT_ID},XDS_ADDR=${XDS_ADDR} \
	 --port 8080 --concurrency 1000 --timeout 900 --cpu ${CPU} --memory ${MEM}

# Same, but with stackdriver and CA integrated
knative/setup_asm:
	REV=asm ENVEXTRA="ASM=1," $(MAKE) knative/setup
