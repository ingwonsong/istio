IN_CONTAINER := $(shell bash -c "test -f /.dockerenv && echo '1' || echo '0'")

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
