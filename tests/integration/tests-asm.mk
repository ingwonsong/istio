#-----------------------------------------------------------------------------
# Target: test.integration.asm.*
#-----------------------------------------------------------------------------

ifeq ($(CLUSTER_TYPE), bare-metal)
	export HTTP_PROXY
	export HTTPS_PROXY=$(HTTP_PROXY)
endif

# Presubmit integration tests targeting Kubernetes environment.
.PHONY: test.integration.asm
test.integration.asm: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} $(shell go list -tags=integ ./tests/integration/... | grep -v /qualification | grep -v /examples) -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM networking.
.PHONY: test.integration.asm.networking
test.integration.asm.networking: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/pilot/... | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Minimal test target for ASM networking.
.PHONY: test.integration.asm.networking.minimal
test.integration.asm.networking.minimal: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/pilot/... | grep -v "${DISABLED_PACKAGES}") -run TestTraffic -timeout 60m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM cloudesf.
.PHONY: test.integration.asm.cloudesf.apikeygrpc
test.integration.asm.cloudesf.apikeygrpc: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/cloudesf/apikeygrpc | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

.PHONY: test.integration.asm.cloudesf.grpcecho
test.integration.asm.cloudesf.grpcecho: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/cloudesf/grpcecho | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM telemetry.
# TODO: Add select tests under tests/integration/telemetry
.PHONY: test.integration.asm.telemetry
test.integration.asm.telemetry: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/multiclusterasm/... | grep -v "${DISABLED_PACKAGES}") \
	 $(shell go list -tags=integ ./tests/integration/telemetry/stats/prometheus/... | grep -v "${DISABLED_PACKAGES}") \
	 $(shell go list -tags=integ ./tests/integration/telemetry/stackdriver/vm/... | grep -v "${DISABLED_PACKAGES}") \
	 $(shell go list -tags=integ ./tests/integration/telemetry/canonicalservices/... | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM security.
.PHONY: test.integration.asm.security
test.integration.asm.security: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/security/... | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM managed control plane (MCP).
.PHONY: test.integration.asm.mcp
test.integration.asm.mcp: | $(JUNIT_REPORT) check-go-tag
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/... | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM Istiod to Mesh CA migration test.
.PHONY: test.integration.asm.meshca-migration
test.integration.asm.meshca-migration: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/security/ca_migration/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for Istio on GKE to MCP with Mesh CA migration
.PHONY: test.integration.asm.addon-migration
test.integration.asm.addon-migration: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/security/addon_migration/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM managed data plane (MDP).
# TODO: re-enable debug logs when scope doesn't cause error: --log_output_level=tf:debug,mdp:debug
.PHONY: test.integration.asm.mdp
test.integration.asm.mdp: | $(JUNIT_REPORT) check-go-tag
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/mdp/... | grep -v "cni19" | grep -v "installation") -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Temporary test target for verifying cni19 with MDP for 1.10 only
# TODO: remove after 1.11
.PHONY: test.integration.asm.mdp-cni19
test.integration.asm.mdp-cni19: | $(JUNIT_REPORT) check-go-tag
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/mdp/cni19/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

.PHONY: test.integration.asm.mdp-installation
test.integration.asm.mdp-installation: | $(JUNIT_REPORT) check-go-tag
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/mdp/installation/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM longrunning test.
.PHONY: test.integration.asm.longrunning
test.integration.asm.longrunning: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/longrunning/... | grep -v "${DISABLED_PACKAGES}") -timeout 45m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for User Auth tests.
.PHONY: test.integration.asm.userauth
test.integration.asm.userauth: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/security/user_auth/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))
