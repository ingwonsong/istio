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

# Custom test target for ASM networking CNI test.
.PHONY: test.integration.asm.networking.cni
test.integration.asm.networking.cni: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ ./tests/integration/pilot/cni/... -timeout 30m \
	${_INTEGRATION_TEST_FLAGS} ${_INTEGRATION_TEST_SELECT_FLAGS} --log_output_level=tf:debug,mcp:debug \
	2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))

# Custom test target for ASM telemetry.
# TODO: Add select tests under tests/integration/telemetry
.PHONY: test.integration.asm.telemetry
test.integration.asm.telemetry: | $(JUNIT_REPORT)
	PATH=${PATH}:${ISTIO_OUT} $(GO) test -p 1 ${T} -tags=integ $(shell go list -tags=integ ./tests/integration/multiclusterasm/... | grep -v "${DISABLED_PACKAGES}") \
	 $(shell go list -tags=integ ./tests/integration/telemetry/stats/prometheus/... | grep -v "${DISABLED_PACKAGES}") $(shell go list -tags=integ ./tests/integration/telemetry/stackdriver/vm/... | grep -v "${DISABLED_PACKAGES}") -timeout 30m \
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
