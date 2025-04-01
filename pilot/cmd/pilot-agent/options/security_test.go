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

package options

import (
	"os"
	"testing"

	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/istio/pkg/security"
	"istio.io/istio/security/pkg/credentialfetcher"
)

func TestCheckGkeWorkloadCertificate(t *testing.T) {
	cert, err := os.CreateTemp("", "existing-cert-file")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(cert.Name())

	tests := []struct {
		name     string
		paths    []string
		expected bool
	}{
		{
			name: "non-existing cert paths",
			paths: []string{
				"/this-is-a-nonexisting-path-1", "/this-is-a-nonexisting-path-2",
				"/this-is-a-nonexisting-path-3",
			},
			expected: false,
		},
		{
			name:     "existing cert paths",
			paths:    []string{cert.Name(), cert.Name(), cert.Name()},
			expected: true,
		},
		{
			name:     "mixed non-existing and existing cert paths",
			paths:    []string{cert.Name(), "/this-is-a-nonexisting-path-1", "/this-is-a-nonexisting-path-2"},
			expected: false,
		},
	}
	for _, tt := range tests {
		result := security.CheckWorkloadCertificate(tt.paths[0], tt.paths[1], tt.paths[2])
		if result != tt.expected {
			t.Errorf("Test %s failed, expected: %t got: %t", tt.name, tt.expected, result)
		}
	}
}

func TestSetupSecurityOptions(t *testing.T) {
	proxyConfig := &meshconfig.ProxyConfig{}
	mockCredFetcher, err := credentialfetcher.NewCredFetcher(security.Mock, "", "", "")
	if err != nil {
		t.Fatalf("failed to create mock credential fetcher: %v", err)
	}

	tests := []struct {
		name        string
		secOpt      *security.Options
		expectError bool
		expectedOut *security.Options
	}{
		{
			name: "CA endpoint is a google api",
			secOpt: &security.Options{
				CAEndpoint: "googleapis.com",
			},
			expectError: false,
			expectedOut: &security.Options{
				CAEndpoint:     "googleapis.com",
				CAProviderName: security.GoogleCAProvider,
				CredFetcher:    mockCredFetcher,
			},
		},
		{
			name: "CA provider is security.GkeWorkloadCertificateProvider",
			secOpt: &security.Options{
				CAProviderName: security.GkeWorkloadCertificateProvider,
			},
			// The error is expected because the GKE workload certificate is not present in the test.
			expectError: true,
		},
	}
	for _, tt := range tests {
		result, err := SetupSecurityOptions(proxyConfig, tt.secOpt, "", "", "")
		gotErr := err != nil
		if gotErr != tt.expectError {
			t.Errorf("expect error is %v while actual error is %v", tt.expectError, gotErr)
			continue
		}
		if !gotErr && !resultAsExpected(result, tt.expectedOut) {
			t.Errorf("Test %s failed, expected: %v got: %v", tt.name, tt.expectedOut, result)
		}
	}
}

func resultAsExpected(result *security.Options, expectedOut *security.Options) bool {
	if result.CAEndpoint == expectedOut.CAEndpoint &&
		result.CAProviderName == expectedOut.CAProviderName &&
		result.FileMountedCerts == expectedOut.FileMountedCerts &&
		result.CertChainFilePath == expectedOut.CertChainFilePath &&
		result.KeyFilePath == expectedOut.KeyFilePath &&
		result.RootCertFilePath == expectedOut.RootCertFilePath {
		return true
	}
	return false
}

func TestExtractCAHeadersFromEnv(t *testing.T) {
	tests := []struct {
		name              string
		envVars           map[string]string
		expectedCAHeaders map[string]string
	}{
		{
			name: "no CA headers",
			envVars: map[string]string{
				"RANDOM_KEY": "value",
			},
			expectedCAHeaders: map[string]string{},
		},
		{
			name: "single CA header",
			envVars: map[string]string{
				"CA_HEADER_FOO": "foo",
			},
			expectedCAHeaders: map[string]string{
				"FOO": "foo",
			},
		},
		{
			name: "multiple CA headers",
			envVars: map[string]string{
				"CA_HEADER_FOO": "foo",
				"CA_HEADER_BAR": "bar",
			},
			expectedCAHeaders: map[string]string{
				"FOO": "foo",
				"BAR": "bar",
			},
		},
		{
			name: "mixed CA and non-CA headers",
			envVars: map[string]string{
				"CA_HEADER_FOO":  "foo",
				"XDS_HEADER_BAR": "bar",
				"CA_HEADER_BAZ":  "=baz",
			},
			expectedCAHeaders: map[string]string{
				"FOO": "foo",
				"BAZ": "=baz",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}
			// Clean up environment variables after test
			defer func() {
				for k := range tt.envVars {
					os.Unsetenv(k)
				}
			}()

			o := &security.Options{
				CAHeaders: map[string]string{},
			}
			extractCAHeadersFromEnv(o)

			if len(o.CAHeaders) != len(tt.expectedCAHeaders) {
				t.Errorf("expected %d CA headers, got %d", len(tt.expectedCAHeaders), len(o.CAHeaders))
			}

			for k, v := range tt.expectedCAHeaders {
				if o.CAHeaders[k] != v {
					t.Errorf("expected CA header %s to be %s, got %s", k, v, o.CAHeaders[k])
				}
			}
		})
	}
}
