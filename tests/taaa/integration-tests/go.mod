module istio.io/istio/tests/taaa/test-artifact

go 1.16

replace istio.io/istio/prow/asm/tester => ../../../prow/asm/tester

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/magefile/mage v1.11.0
	github.com/spf13/cobra v1.2.1
	gke-internal.git.corp.google.com/taaa/lib.git v0.0.0-20210805225820-a46291bf7c71
	gke-internal.git.corp.google.com/taaa/protobufs.git v0.0.0-20210909220842-1af680420a1c
	istio.io/istio/prow/asm/tester v0.0.0-00010101000000-000000000000
	knative.dev/test-infra/rundk v0.0.0-20210907164518-001dd6fcbf2a
)
