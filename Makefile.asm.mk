IN_CONTAINER := $(shell bash -c "test -f /.dockerenv && echo '1' || echo '0'")
RELEASE_LDFLAGS_NOLINKMODE='-extldflags -static -s -w'

FINDFILES_IGNORE= -path ./tests/taaa/integration-tests/vendor -o -path ./tools/asm-lifecycle-tag/vendor -o -path ./prow/asm/tester/vendor -o -path ./prow/asm/infra/vendor
export FINDFILES_IGNORE

LDFLAGS:=-linkmode=external -extldflags -static -s -w
CGO_ENABLED:=1
ifeq ($(TARGET_ARCH), arm64)
  CC=aarch64-linux-gnu-gcc
  # TODO(b/372456762): switch back to stripped build
  LDFLAGS:=-linkmode=external -extldflags -static -w
endif

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

.PHONY: build
build: depend ## Builds all go binaries.
	GOOS=$(GOOS_LOCAL) GOARCH=$(GOARCH_LOCAL) common/scripts/gobuild.sh $(TARGET_OUT)/ $(STANDARD_BINARIES)
	GOOS=$(GOOS_LOCAL) GOARCH=$(GOARCH_LOCAL) common/scripts/gobuild.sh $(TARGET_OUT)/ -tags="agent,netgo,osusergo" $(AGENT_BINARIES) $(LINUX_AGENT_BINARIES)

.PHONY: build-linux
build-linux: depend
	GOOS=linux GOARCH=$(GOARCH_LOCAL) common/scripts/gobuild.sh $(TARGET_OUT_LINUX)/ $(STANDARD_BINARIES)
	GOOS=linux GOARCH=$(GOARCH_LOCAL) common/scripts/gobuild.sh $(TARGET_OUT_LINUX)/ -tags="agent,netgo,osusergo" $(AGENT_BINARIES) $(LINUX_AGENT_BINARIES)

# Non-static istioctl targets. These are typically a build artifact.
${TARGET_OUT}/release/istioctl-linux-amd64:
	GOOS=linux GOARCH=amd64 LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl
${TARGET_OUT}/release/istioctl-linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl
${TARGET_OUT}/release/istioctl-linux-arm64:
	GOOS=linux GOARCH=arm64 LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl
${TARGET_OUT}/release/istioctl-osx:
	GOOS=darwin GOARCH=amd64 LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl
${TARGET_OUT}/release/istioctl-osx-arm64:
	GOOS=darwin GOARCH=arm64 LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl
${TARGET_OUT}/release/istioctl-win.exe:
	GOOS=windows LDFLAGS=$(RELEASE_LDFLAGS_NOLINKMODE) CGO_ENABLED=0 common/scripts/gobuild.sh $@ ./istioctl/cmd/istioctl

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
