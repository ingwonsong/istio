// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package asmvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"

	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/echo/client"
	"istio.io/istio/pkg/test/framework/components/cluster"
	"istio.io/istio/pkg/test/framework/components/cluster/asmvm"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/util/retry"
)

const cloudSDKCIRepo = "https://storage.googleapis.com/cloud-sdk-testing/ci/staging/components-2.json"

var _ echo.Instance = &instance{}

func init() {
	echo.RegisterFactory(cluster.ASMVM, newInstances)
}

func newInstances(ctx resource.Context, config []echo.Config) (echo.Instances, error) {
	if err := updateCloudSDK(cloudSDKCIRepo); err != nil {
		return nil, err
	}

	errG := multierror.Group{}
	mu := sync.Mutex{}
	var out echo.Instances
	for _, c := range config {
		c := c
		errG.Go(func() error {
			i, err := newInstance(ctx, c)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			out = append(out, i)
			return nil
		})
	}
	if err := errG.Wait().ErrorOrNil(); err != nil {
		return nil, err
	}
	return out, nil
}

func newInstance(ctx resource.Context, config echo.Config) (echo.Instance, error) {
	c, ok := config.Cluster.(asmvm.Cluster)
	if !ok {
		return nil, fmt.Errorf("failed to cast cluster of kind %s to asmvm.Cluster: %v", config.Cluster.Kind(), config.Cluster)
	}

	svcAddr, err := getClusterIP(config)
	if err != nil {
		return nil, err
	}

	i := &instance{
		ctx:           ctx,
		config:        config,
		cluster:       c,
		replicas:      1,
		address:       svcAddr,
		echoInstalled: map[uint64]bool{},
		zone:          c.Zone(),
	}
	i.id = ctx.TrackResource(i)

	if err := i.generateConfig(); err != nil {
		return nil, err
	}
	if err := i.createWorkloadGroup(ctx); err != nil {
		return nil, err
	}
	if err := i.createInstanceTemplate(); err != nil {
		return nil, err
	}
	if err := i.createManagedInstanceGroup(); err != nil {
		return nil, err
	}
	if err := i.initializeWorkloads(); err != nil {
		return nil, err
	}

	return i, nil
}

type instance struct {
	id      resource.ID
	ctx     resource.Context
	config  echo.Config
	cluster asmvm.Cluster

	// dir containing generated config for the VMs
	dir string
	// unitFile is the path to a systemd unit file that starts the echo service with approperiate ports
	unitFile string
	// workloadGroup generated yaml with appropriate port mappings
	workloadGroup string

	// address is the k8s Service address in front of the instances
	address string
	// replicas is the desired number of workloads, this number should be changed when scaling the MIG
	// so that calls to initializeWorkloads wait for the proper number of VMs to be ready.
	replicas int

	sync.Mutex
	workloads []echo.Workload
	zone      string
	// echoInstalled tracks GCP resource IDs that have already had the echo app installed.
	// This prevents initializeWorkloads calls for MIG scaling from having to re-install echo
	// on an instance that already has it.
	echoInstalled map[uint64]bool
}

func (i *instance) ID() resource.ID {
	return i.id
}

func (i *instance) Config() echo.Config {
	return i.config
}

func (i *instance) Address() string {
	return i.address
}

func (i *instance) Workloads() ([]echo.Workload, error) {
	i.Lock()
	defer i.Unlock()
	return i.workloads, nil
}

func (i *instance) WorkloadsOrFail(t test.Failer) []echo.Workload {
	i.Lock()
	defer i.Unlock()
	w, err := i.Workloads()
	if err != nil {
		t.Fatalf("failed getting workloads for %s", i.Config().Service)
	}
	return w
}

func (i *instance) defaultClient() (*client.Instance, error) {
	i.Lock()
	defer i.Unlock()
	return i.workloads[0].(*workload).Instance, nil
}

func (i *instance) Call(opts echo.CallOptions) (client.ParsedResponses, error) {
	return common.ForwardEcho(i.Config().Service, i.defaultClient, &opts, false)
}

func (i *instance) CallOrFail(t test.Failer, opts echo.CallOptions) client.ParsedResponses {
	t.Helper()
	res, err := i.Call(opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (i *instance) CallWithRetry(opts echo.CallOptions, retryOptions ...retry.Option) (client.ParsedResponses, error) {
	return common.ForwardEcho(i.Config().Service, i.defaultClient, &opts, true, retryOptions...)
}

func (i *instance) CallWithRetryOrFail(t test.Failer, opts echo.CallOptions, retryOptions ...retry.Option) client.ParsedResponses {
	t.Helper()
	res, err := i.CallWithRetry(opts, retryOptions...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const dumpScriptVM = `
rm -rf ~/asmvm-ci-dump
mkdir ~/asmvm-ci-dump
cd ~/asmvm-ci-dump
curl localhost:15000/clusters > clusters.txt
curl localhost:15000/config_dump?include_eds > config-dump.json
sudo journalctl -u echo.service > echo.log
sudo journalctl -u service-proxy-agent.service > service-proxy-agent.log
cp /var/log/envoy/* .
tar -czvf ~/dump.tar.gz .
`

func (i *instance) Dump(ctx resource.Context) {
	var err error
	defer func() {
		if err != nil {
			scopes.Framework.Errorf("failed dumping asmvm %s.%s: %v", i.config.Service, i.config.Namespace.Name(), err)
		}
	}()
	workloads, err := i.Workloads()
	if err != nil {
		return
	}

	dir, err := ctx.CreateDirectory(fmt.Sprintf("asmvm-%s.%s", i.config.Service, i.config.Namespace.Name()))
	if err != nil {
		return
	}
	errG := multierror.Group{}
	for _, w := range workloads {
		w := w
		errG.Go(func() error {
			if err := i.dumpWorkload(dir, w); err != nil {
				return fmt.Errorf("failed dumping %s: %v", w.PodName(), err)
			}
			return nil
		})
	}
	if err = errG.Wait().ErrorOrNil(); err != nil {
		return
	}
}

func (i *instance) dumpWorkload(wd string, workload echo.Workload) error {
	wd = path.Join(wd, workload.PodName())
	err := os.Mkdir(wd, 0o644)
	if err != nil {
		return fmt.Errorf("failed creating dump wd %s: %v", wd, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "gcloud", "compute", "ssh",
		"echovm@"+workload.PodName(),
		"--zone", i.zone,
		"--command", dumpScriptVM).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create dump for vm: %v: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "gcloud", "compute", "scp",
		"echovm@"+workload.PodName()+":~/dump.tar.gz", wd,
		"--zone", i.zone).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to scp dump for vm: %v: %s", err, out)
	}
	// TODO decompress dump
	return nil
}

func (i *instance) Restart() error {
	panic("TODO implement restarts for GCE VMs")
}

func (i *instance) Scale(replicas int) error {
	// TODO stubbing this method here so that OSS can add it to echo.Instance without breaking asm compilation
	panic("TODO implement Scale for GCE VMs/MIG")
}
