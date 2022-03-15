.PHONY: asm-postmerge
asm-postmerge: asm-proxy-update asm-go-tidy operator-proto

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
