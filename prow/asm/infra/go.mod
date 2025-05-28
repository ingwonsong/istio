module istio.io/istio/prow/asm/infra

go 1.24.0

require (
	github.com/Masterminds/sprig/v3 v3.3.0
	github.com/google/uuid v1.6.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/spf13/pflag v1.0.6
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v2 v2.4.0
	istio.io/istio v0.0.0-20220408200757-466c02050528
	k8s.io/apimachinery v0.33.0
	sigs.k8s.io/boskos v0.0.0-20210823185622-ae371c628ac9
	sigs.k8s.io/kubetest2 v0.0.0-20220713164938-2aac35a0b4ba
)

require (
	dario.cat/mergo v1.0.2 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.3.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.22.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.63.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/cast v1.8.0 // indirect
	github.com/spf13/cobra v1.9.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go4.org v0.0.0-20201209231011-d4a079459e60 // indirect
	golang.org/x/crypto v0.38.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	google.golang.org/grpc v1.72.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/test-infra v0.0.0-20210730160938-8ad9b8c53bd8 // indirect
	k8s.io/utils v0.0.0-20250502105355-0f33e8f1c979 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

replace istio.io/istio => ../../..
