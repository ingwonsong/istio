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

package namespace

import (
	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/framework/resource"
)

// Config contains configuration information about the namespace instance
type Config struct {
	// Prefix to use for autogenerated namespace name
	Prefix string
	// Inject indicates whether to add sidecar injection label to this namespace
	Inject bool
	// Revision is the namespace of custom injector instance
	Revision string
	// Labels to be applied to namespace
	Labels map[string]string
}

// Instance represents an allocated namespace that can be used to create config, or deploy components in.
type Instance interface {
	Name() string
	SetLabel(key, value string) error
	RemoveLabel(key string) error
	SetAnnotation(key, value string) error
	RemoveAnnotation(key string) error
	Prefix() string
	Labels() (map[string]string, error)
}

// Claim an existing namespace in all clusters, or create a new one if doesn't exist.
func Claim(ctx resource.Context, nsConfig Config) (i Instance, err error) {
	overwriteRevisionIfEmpty(&nsConfig, ctx.Settings().Revisions.Default())
	return claimKube(ctx, &nsConfig)
}

// ClaimOrFail calls Claim and fails test if it returns error
func ClaimOrFail(t test.Failer, ctx resource.Context, name string) Instance {
	t.Helper()
	nsCfg := Config{
		Prefix: name,
		Inject: true,
	}
	i, err := Claim(ctx, nsCfg)
	if err != nil {
		t.Fatalf("namespace.ClaimOrFail:: %v", err)
	}
	return i
}

// New creates a new Namespace in all clusters.
func New(ctx resource.Context, nsConfig Config) (i Instance, err error) {
	if ctx.Settings().StableNamespaces {
		return Claim(ctx, nsConfig)
	}
	overwriteRevisionIfEmpty(&nsConfig, ctx.Settings().Revisions.Default())
	return newKube(ctx, &nsConfig)
}

// NewOrFail calls New and fails test if it returns error
func NewOrFail(t test.Failer, ctx resource.Context, nsConfig Config) Instance {
	t.Helper()
	i, err := New(ctx, nsConfig)
	if err != nil {
		t.Fatalf("namespace.NewOrFail: %v", err)
	}
	return i
}

func overwriteRevisionIfEmpty(nsConfig *Config, revision string) {
	// Overwrite the default namespace label (istio-injection=enabled)
	// with istio.io/rev=XXX. If a revision label is already provided,
	// the label will remain as is.
	if nsConfig.Revision == "" {
		nsConfig.Revision = revision
	}
	// Allow setting revision explicitly to `default` to avoid configuration overwrite
	if nsConfig.Revision == "default" {
		nsConfig.Revision = ""
	}
}
