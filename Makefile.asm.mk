IN_CONTAINER := $(shell bash -c "test -f /.dockerenv && echo '1' || echo '0'")

FINDFILES_IGNORE= -path ./tests/taaa/integration-tests/vendor -o -path ./tools/asm-lifecycle-tag/vendor -o -path ./prow/asm/tester/vendor -o -path ./prow/asm/infra/vendor -o -path ./go-control-plane
export FINDFILES_IGNORE

ifeq ($(IN_CONTAINER),0)
.PHONY: asm-sync
asm-sync:
	@bin/asm-sync.sh
else
asm-sync: ; $(error sync cannot run within container -- set BUILD_WITH_CONTAINER=0)
endif

.PHONY: asm-postsync
asm-postsync: asm-proxy-update asm-go-tidy operator-proto

.PHONY: asm-proxy-update
asm-proxy-update:
	@bin/asm-proxy-update.sh

.PHONY: asm-go-tidy
asm-go-tidy:
	@bin/asm-go-tidy.sh

.PHONY: racetest
racetest: $(JUNIT_REPORT)
	CGO_ENABLED=1 go test ${GOBUILDFLAGS} ${T} -race ./... 2>&1 | tee >($(JUNIT_REPORT) > $(JUNIT_OUT))
	$(MAKE) tester-unit-tests

.PHONY: binaries-test
binaries-test:
	CGO_ENABLED=1 GOEXPERIMENT=boringcrypto go test ${GOBUILDFLAGS} ./tests/binary/... -v --base-dir ${TARGET_OUT} --binaries="$(RELEASE_SIZE_TEST_BINARIES)"

gen-third-party-notice:
	./bin/gen-third-party-notice.sh

include mdp/manifest/gen.mk

#-----------------------------------------------------------------------------
# Target: ASM specific tests
#-----------------------------------------------------------------------------
include tests/integration/tests-asm.mk
include prow/asm/tester/tester.mk

#-----------------------------------------------------------------------------
# Target: Cloudrun
#-----------------------------------------------------------------------------
include tools/packaging/knative/Makefile
