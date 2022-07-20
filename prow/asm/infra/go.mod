module istio.io/istio/prow/asm/infra

go 1.15

require (
	github.com/google/uuid v1.3.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/spf13/pflag v1.0.5
	gopkg.in/yaml.v2 v2.4.0
	istio.io/istio v0.0.0-20220408200757-466c02050528
	k8s.io/apimachinery v0.24.2
	sigs.k8s.io/boskos v0.0.0-20210823185622-ae371c628ac9
	sigs.k8s.io/kubetest2 v0.0.0-20220713164938-2aac35a0b4ba
)

replace istio.io/istio => ../../..
