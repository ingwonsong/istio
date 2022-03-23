export IMG ?= gcr.io/asm-staging-images/asm/build-tools:master-latest

# Set up authentication if using custom Envoy URL
# This is needed to fetch Envoy binary for ASM, especially in prow
ifdef GOOGLE_APPLICATION_CREDENTIALS
$(shell gcloud auth activate-service-account --key-file="${GOOGLE_APPLICATION_CREDENTIALS}")
endif
export AUTH_HEADER ?= Authorization: Bearer $(shell gcloud auth print-access-token)
ISTIO_ENVOY_BASE_URL ?= https://storage.googleapis.com/asm-testing/istio/dev
GOBUILDFLAGS := --tags="netgo,osusergo"

# cloudesf specific overrides
-include cloudesf/Makefile.cloudesf.version.mk
