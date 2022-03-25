module istio.io/istio/prow/asm/tester

go 1.16

require (
	github.com/google/go-cmp v0.5.7
	github.com/hashicorp/go-multierror v1.1.1
	github.com/hashicorp/go-version v1.4.0
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/octago/sflags v0.3.1-0.20210726012706-20f2a9c31dfc
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.4.0
	github.com/spf13/pflag v1.0.5
	go.uber.org/multierr v1.8.0
	golang.org/x/oauth2 v0.0.0-20220309155454-6242fa91716a
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
	istio.io/istio v0.0.0-00010101000000-000000000000
	k8s.io/api v0.23.5
	k8s.io/apimachinery v0.23.5
	k8s.io/client-go v0.23.5
)

replace istio.io/istio => ../../..
