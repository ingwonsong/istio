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

package deployment

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/sync/errgroup"

	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/ports"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/resource"
)

// SingleNamespaceView is a simplified view of Echos for tests that only require a single namespace.
type SingleNamespaceView struct {
	// Include the echos at the top-level, so there is no need for accessing sub-structures.
	EchoNamespace

	// External (out-of-mesh) deployments
	External External

	// All echo instances
	All echo.Services
}

// Echos is a common set of echo deployments to support integration testing.
type Echos struct {
	// NS is the list of echo namespaces.
	NS []EchoNamespace

	// External (out-of-mesh) deployments
	External External

	// All echo instances.
	All echo.Services
}

// SingleNamespaceView converts this Echos into a SingleNamespaceView for NS1.
func (d Echos) SingleNamespaceView() SingleNamespaceView {
	return SingleNamespaceView{
		EchoNamespace: d.NS1(),
		External:      d.External,
		All:           d.NS1().All.Append(d.External.All.Services()),
	}
}

// NS1 is shorthand for NS[0]
func (d Echos) NS1() EchoNamespace {
	return d.NS[0]
}

// NS2 is shorthand for NS[1]. Will panic if there are not at least 2 apps namespaces.
func (d Echos) NS2() EchoNamespace {
	return d.NS[1]
}

// NS1AndNS2 returns the combined set of services in NS1 and NS2.
func (d Echos) NS1AndNS2() echo.Services {
	return d.NS1().All.Append(d.NS2().All)
}

func (d *Echos) loadValues(t resource.Context, echos echo.Instances) error {
	d.All = echos.Services()

	g := multierror.Group{}
	for i := 0; i < len(d.NS); i++ {
		i := i
		g.Go(func() error {
			return d.NS[i].loadValues(t, echos, d)
		})
	}

	g.Go(func() error {
		return d.External.loadValues(echos)
	})

	return g.Wait().ErrorOrNil()
}

func (d Echos) namespaces(excludes ...namespace.Instance) []string {
	var out []string
	for _, n := range d.NS {
		include := true
		for _, e := range excludes {
			if n.Namespace.Name() == e.Name() {
				include = false
				break
			}
		}
		if include {
			out = append(out, n.Namespace.Name())
		}
	}

	sort.Strings(out)
	return out
}

func serviceEntryPorts() []echo.Port {
	var res []echo.Port
	for _, p := range ports.All().GetServicePorts() {
		if strings.HasPrefix(p.Name, "auto") {
			// The protocol needs to be set in common.EchoPorts to configure the echo deployment
			// But for service entry, we want to ensure we set it to "" which will use sniffing
			p.Protocol = ""
		}
		res = append(res, p)
	}
	return res
}

type Config struct {
	NamespaceCount int
}

func (c *Config) fillDefaults() {
	if c.NamespaceCount <= 1 {
		c.NamespaceCount = 1
	}
}

func SetupSingleNamespace(t resource.Context, view *SingleNamespaceView) error {
	// Perform a setup with exactly 1 namespace.
	var apps Echos
	if err := Setup(t, &apps, Config{NamespaceCount: 1}); err != nil {
		return err
	}

	// Store the single namespace view.
	*view = apps.SingleNamespaceView()
	return nil
}

func Setup(t resource.Context, apps *Echos, cfg Config) error {
	cfg.fillDefaults()

	// Create the namespaces concurrently.
	g, _ := errgroup.WithContext(context.TODO())

	// Create the echo namespaces.
	apps.NS = make([]EchoNamespace, cfg.NamespaceCount)
	if cfg.NamespaceCount == 1 {
		// If only using a single namespace, preserve the "echo" prefix.
		g.Go(func() (err error) {
			apps.NS[0].Namespace, err = namespace.New(t, namespace.Config{
				Prefix: "echo",
				Inject: true,
			})
			return
		})
	} else {
		for i := 0; i < cfg.NamespaceCount; i++ {
			i := i
			g.Go(func() (err error) {
				apps.NS[i].Namespace, err = namespace.New(t, namespace.Config{
					Prefix: fmt.Sprintf("echo%d", i),
					Inject: true,
				})
				return
			})
		}
	}

	// Create the external namespace.
	g.Go(func() (err error) {
		apps.External.Namespace, err = namespace.New(t, namespace.Config{
			Prefix: "external",
			Inject: false,
		})
		return
	})

	// Wait for the namespaces to be created.
	if err := g.Wait(); err != nil {
		return err
	}

	builder := deployment.New(t).WithClusters(t.Clusters()...)
	for _, n := range apps.NS {
		builder = n.build(t, builder)
	}
	builder = apps.External.build(builder)

	echos, err := builder.Build()
	if err != nil {
		return err
	}

	// Load values from the deployed echo instances.
	return apps.loadValues(t, echos)
}

// TODO(nmittler): should t.Settings().Skip(echo.Delta) do all of this?
func skipDeltaXDS(t resource.Context) bool {
	return t.Settings().Skip(echo.Delta) || !t.Settings().Revisions.AtLeast("1.11")
}
