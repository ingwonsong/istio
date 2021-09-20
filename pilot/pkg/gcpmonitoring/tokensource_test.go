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

package gcpmonitoring

import (
	"testing"
	"time"

	"istio.io/istio/pkg/security"
	"istio.io/istio/security/pkg/stsservice/tokenmanager"
	"istio.io/istio/security/pkg/stsservice/tokenmanager/google"
	"istio.io/istio/security/pkg/stsservice/tokenmanager/google/mock"
)

func TestStsTokenSource(t *testing.T) {
	tests := []struct {
		name         string
		refreshToken bool
	}{
		{"token source without refresh", false},
		{"token source with refresh", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, mockBackend := setUpTestComponents(t, testSetUp{})
			_ = mockBackend

			st := mock.FakeSubjectToken
			if tt.refreshToken {
				// if refreshing token is enabled. set dummy token initially and refresh it to desired subject token later.
				st = "initial dummy token"
			}
			ts := NewTokenSource(tm, st, "https://www.googleapis.com/auth/cloud-platform")

			ts.tm = tm

			if tt.refreshToken {
				// If refreshing token is enabled, set the correct subject token.
				ts.RefreshSubjectToken(mock.FakeSubjectToken)
			}

			// Get and verify access token
			got, err := ts.Token()
			if err != nil {
				t.Fatalf("failed to get access token: %v", err)
			}
			if got.AccessToken != mock.FakeAccessToken {
				t.Errorf("access token want %v got %v", mock.FakeAccessToken, got.AccessToken)
			}
			if got.TokenType != "Bearer" {
				t.Errorf("access token type want %v got %v", "Bearer", got.TokenType)
			}
			gotExpireIn := time.Until(got.Expiry)
			// Give wanted expire duration 10 seconds headroom.
			wantedExpireIn := time.Duration(mock.FakeExpiresInSeconds-10) * time.Second
			if gotExpireIn < wantedExpireIn {
				t.Errorf("expiry too short want at least %v, got %v", wantedExpireIn, gotExpireIn)
			}
		})
	}
}

type testSetUp struct {
	enableCache        bool
	enableDynamicToken bool
}

// setUpTest sets up components for the STS flow, including a STS server, a
// token manager, and an authorization server.
func setUpTestComponents(t *testing.T, setup testSetUp) (security.TokenManager, *mock.AuthorizationServer) {
	// Create mock authorization server
	mockServer, err := mock.StartNewServer(t, mock.Config{Port: 0})
	t.Cleanup(func() {
		mockServer.Stop()
	})
	mockServer.EnableDynamicAccessToken(setup.enableDynamicToken)
	if err != nil {
		t.Fatalf("failed to start a mock server: %v", err)
	}
	// Create token exchange Google plugin
	tokenExchangePlugin, _ := google.CreateTokenManagerPlugin(nil, mock.FakeTrustDomain, mock.FakeProjectNum,
		mock.FakeGKEClusterURL, setup.enableCache)
	federatedTokenTestingEndpoint := mockServer.URL + "/v1/token"
	accessTokenTestingEndpoint := mockServer.URL + "/v1/projects/-/serviceAccounts/service-%s@gcp-sa-meshdataplane.iam.gserviceaccount.com:generateAccessToken"
	tokenExchangePlugin.SetEndpoints(federatedTokenTestingEndpoint, accessTokenTestingEndpoint)
	// Create token manager
	tokenManager := &tokenmanager.TokenManager{}
	tokenManager.SetPlugin(tokenExchangePlugin)

	return tokenManager, mockServer
}
