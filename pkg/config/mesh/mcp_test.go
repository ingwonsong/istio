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

package mesh

import (
	"os"
	"testing"

	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/util/assert"
	"istio.io/istio/pkg/util/protomarshal"
)

func TestASMApply(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "set access log simple",
			in:   `accessLogFile: /dev/stderr`,
			out: `
defaultProviders:
  accessLogging:
  - stackdriver
  - asm-internal-envoy-legacy
extensionProviders:
- name: stackdriver
- name: asm-internal-envoy-legacy
  envoyFileAccessLog:
    path: /dev/stderr
`,
		},
		{
			name: "set access log complex",
			in: `
accessLogFile: /dev/stderr
accessLogFormat: "my custom format"
`,
			out: `
defaultProviders:
  accessLogging:
  - stackdriver
  - asm-internal-envoy-legacy
extensionProviders:
- name: stackdriver
- name: asm-internal-envoy-legacy
  envoyFileAccessLog:
    path: /dev/stderr
    logFormat:
      text: my custom format
`,
		},
		{
			name: "set access log json",
			in: `
accessLogFile: /dev/stderr
accessLogFormat: '{"my custom format":"something"}'
accessLogEncoding: JSON
`,
			out: `
defaultProviders:
  accessLogging:
  - stackdriver
  - asm-internal-envoy-legacy
extensionProviders:
- name: stackdriver
- name: asm-internal-envoy-legacy
  envoyFileAccessLog:
    path: /dev/stderr
    logFormat:
      labels:
        "my custom format": "something"
`,
		},
		{
			name: "set default provider tracing",
			in: `
defaultProviders:
  tracing: [something]
accessLogFile: /dev/stderr
`,
			out: `
defaultProviders:
  tracing: [something]
  accessLogging:
  - stackdriver
  - asm-internal-envoy-legacy
extensionProviders:
- name: stackdriver
- name: asm-internal-envoy-legacy
  envoyFileAccessLog:
    path: /dev/stderr
`,
		},
		{
			name: "set default provider logging",
			in: `
defaultProviders:
  accessLogging: [something]
accessLogFile: /dev/stderr
`,
			out: `
defaultProviders:
  accessLogging:
  - something
extensionProviders:
- name: stackdriver
`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			SetEnvForTest(t, "K_SERVICE", "test")
			mc := DefaultMeshConfig()
			// Cleanup the default config so we don't have too messy of tests
			mc.ExtensionProviders = []*meshconfig.MeshConfig_ExtensionProvider{{
				Name: "stackdriver",
			}}
			mc.DefaultProviders.Metrics = nil
			res, err := ApplyMeshConfig(tt.in, mc)
			if err != nil {
				t.Fatal(err)
			}
			// Just extract fields we are testing
			minimal := &meshconfig.MeshConfig{}
			minimal.DefaultProviders = res.DefaultProviders
			minimal.ExtensionProviders = res.ExtensionProviders

			want := &meshconfig.MeshConfig{}
			if err := protomarshal.ApplyYAML(tt.out, want); err != nil {
				t.Fatal(err)
			}
			assert.Equal(t, want, minimal)
			original, err := protomarshal.ToYAML(mc)
			if err != nil {
				t.Fatal(err)
			}
			res, err = ApplyMeshConfig(tt.in, res)
			if err != nil {
				t.Fatal(err)
			}
			idempotencyCheck, err := protomarshal.ToYAML(res)
			if err != nil {
				t.Fatal(err)
			}
			assert.Equal(t, original, idempotencyCheck, "idempotency check")
			// TODO idempotent as well
		})
	}
}

// SetEnvForTest sets an environment variable for the duration of a test, then resets it once the test is complete.
func SetEnvForTest(t test.Failer, k, v string) {
	old := os.Getenv(k)
	if err := os.Setenv(k, v); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Setenv(k, old); err != nil {
			t.Fatal(err)
		}
	})
}
