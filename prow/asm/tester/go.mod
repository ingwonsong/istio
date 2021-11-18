module istio.io/istio/prow/asm/tester

go 1.16

require (
	github.com/google/go-cmp v0.5.6
	github.com/hashicorp/go-multierror v1.1.1
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/octago/sflags v0.3.1-0.20210726012706-20f2a9c31dfc
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.2.1
	github.com/spf13/pflag v1.0.5
	go.uber.org/multierr v1.7.0
	golang.org/x/oauth2 v0.0.0-20211005180243-6b3c2da341f1
	gopkg.in/yaml.v2 v2.4.0
	istio.io/istio v0.0.0-00010101000000-000000000000
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
)

replace istio.io/istio => ../../..
