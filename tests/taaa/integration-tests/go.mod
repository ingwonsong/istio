module istio.io/istio/tests/taaa/test-artifact

go 1.16

replace istio.io/istio/prow/asm/tester => ../../../prow/asm/tester

replace istio.io/istio => ../../..

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/magefile/mage v1.11.0
	github.com/spf13/cobra v1.2.1
	gke-internal.git.corp.google.com/taaa/lib.git v0.0.0-20210923204113-d7e249b27cfd
	gke-internal.git.corp.google.com/taaa/protobufs.git v0.0.0-20210921035534-78e673220a03
	istio.io/istio/prow/asm/tester v0.0.0-00010101000000-000000000000
	knative.dev/test-infra/rundk v0.0.0-20210921180237-ac9f05820da7
)
