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

package translation

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"github.com/google/go-cmp/cmp"
	"github.com/googleapis/gax-go/v2"
	containerpb "google.golang.org/genproto/googleapis/container/v1"
	"k8s.io/client-go/tools/clientcmd/api"
)

type mockMembershipLister struct {
	memberships []*gkehubpb.Membership
	called      bool
}

func (m *mockMembershipLister) ListMemberships(ctx context.Context,
	req *gkehubpb.ListMembershipsRequest, opts ...gax.CallOption,
) ([]*gkehubpb.Membership, error) {
	m.called = true
	return m.memberships, nil
}

type mockClusterFetcher struct {
	clusters []*containerpb.Cluster
	called   bool
}

func (m *mockClusterFetcher) GetCluster(ctx context.Context,
	req *containerpb.GetClusterRequest, opts ...gax.CallOption,
) (*containerpb.Cluster, error) {
	m.called = true
	for _, cluster := range m.clusters {
		if cluster.Name == req.GetName() {
			return cluster, nil
		}
	}

	return nil, fmt.Errorf("failed to fetch cluster")
}

func TestCache(t *testing.T) {
	cases := []struct {
		name                       string
		ip                         string
		projectNumber              string
		hubEndpoint                string
		existingMemberships        []*gkehubpb.Membership
		existingClusters           []*containerpb.Cluster
		existingCacheState         map[string]*gkehubpb.Membership
		existingKnownPublicIPCache map[string]bool
		wantFound                  bool
		wantAPICalls               bool
		wantAPIConfig              api.Config
	}{
		{
			name: "config exists in cache",
			ip:   "1.2.3.4",
			existingCacheState: map[string]*gkehubpb.Membership{
				"1.2.3.4": {
					Name: "projects/example/locations/global/memberships/random",
				},
			},
			wantFound:     true,
			wantAPICalls:  false,
			wantAPIConfig: createAPIConfig("https://connectgateway.googleapis.com/v1/projects/1234/locations/global/gkeMemberships/random"),
		},
		{
			name:        "config exists in cache with autopush hub endpoint",
			hubEndpoint: "autopush-gkehub.sandbox.googleapis.com",
			ip:          "1.2.3.4",
			existingCacheState: map[string]*gkehubpb.Membership{
				"1.2.3.4": {
					Name: "projects/example/locations/global/memberships/random",
				},
			},
			wantFound:     true,
			wantAPICalls:  false,
			wantAPIConfig: createAPIConfig("https://autopush-connectgateway.sandbox.googleapis.com/v1/projects/1234/locations/global/gkeMemberships/random"),
		},
		{
			name: "config exists in cache with multiple entries",
			ip:   "1.2.3.4",
			existingCacheState: map[string]*gkehubpb.Membership{
				"1.2.3.4": {
					Name: "projects/example/locations/global/memberships/random",
				},
				"5.6.7.8": {
					Name: "projects/example/locations/global/memberships/random2",
				},
			},
			wantFound:     true,
			wantAPICalls:  false,
			wantAPIConfig: createAPIConfig("https://connectgateway.googleapis.com/v1/projects/1234/locations/global/gkeMemberships/random"),
		},
		{
			name: "config does not exist in cache or in existing memberships",
			ip:   "5.6.7.8",
			existingCacheState: map[string]*gkehubpb.Membership{
				"1.2.3.4": {
					Name: "projects/example/locations/global/memberships/random",
				},
			},
			wantFound:    false,
			wantAPICalls: true,
		},
		{
			name:               "config does not exist in cache but fleet membership exists with corresponding IP",
			ip:                 "5.6.7.8",
			existingCacheState: map[string]*gkehubpb.Membership{},
			existingMemberships: []*gkehubpb.Membership{
				createMembership("projects/example/locations/global/memberships/random",
					"//container.googleapis.com/projects/example/locations/us-west1-a/clusters/cluster"),
			},
			existingClusters: []*containerpb.Cluster{
				createPrivateCluster("projects/example/locations/us-west1-a/clusters/cluster", "5.6.7.8", ""),
			},
			wantFound:     true,
			wantAPICalls:  true,
			wantAPIConfig: createAPIConfig("https://connectgateway.googleapis.com/v1/projects/1234/locations/global/gkeMemberships/random"),
		},
		{
			name:                       "cache contains IP in known public cluster cache",
			ip:                         "1.2.3.4",
			existingKnownPublicIPCache: map[string]bool{"1.2.3.4": true},
			wantAPICalls:               false,
			wantFound:                  false,
		},
		{
			name:                       "IP matches with public cluster but does not get translated",
			ip:                         "1.2.3.4",
			existingKnownPublicIPCache: map[string]bool{},
			existingCacheState:         map[string]*gkehubpb.Membership{},
			existingMemberships: []*gkehubpb.Membership{
				createMembership("projects/example/locations/global/memberships/random",
					"//container.googleapis.com/projects/example/locations/us-west1-a/clusters/cluster"),
			},
			existingClusters: []*containerpb.Cluster{
				createPublicCluster("projects/example/locations/us-west1-a/clusters/cluster", "1.2.3.4"),
			},
			wantAPICalls: true,
			wantFound:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockFetcher := &mockClusterFetcher{clusters: tc.existingClusters}
			mockLister := &mockMembershipLister{memberships: tc.existingMemberships}
			c := &membershipCache{
				opts: environmentOpts{
					projectNumber: "1234",
					hubEndpoint:   tc.hubEndpoint,
				},
				clusterFetcher:        mockFetcher,
				membershipLister:      mockLister,
				validateEndpoint:      false,
				publicIPToMembership:  tc.existingCacheState,
				privateIPToMembership: tc.existingCacheState,
				knownPublicIPs:        tc.existingKnownPublicIPCache,
			}

			config, found := c.Get(tc.ip)
			if found != tc.wantFound {
				t.Errorf("expected translation found: %t, got: %t", tc.wantFound, found)
			}
			if (mockLister.called || mockFetcher.called) != tc.wantAPICalls {
				t.Errorf("expected API call %t, got membership list called: %t, cluster fetch called: %t",
					tc.wantAPICalls, mockLister.called, mockFetcher.called)
			}

			if !found {
				return
			}
			if diff := cmp.Diff(config, tc.wantAPIConfig); diff != "" {
				t.Fatalf("expected API config does not match result, diff (-want +got)\n:%s", diff)
			}
		})
	}
}

func createPublicCluster(name, ip string) *containerpb.Cluster {
	return &containerpb.Cluster{
		Name:     name,
		Endpoint: ip,
	}
}

func createPrivateCluster(name, publicIP, privateIP string) *containerpb.Cluster {
	return &containerpb.Cluster{
		Name:     name,
		Endpoint: publicIP,
		PrivateClusterConfig: &containerpb.PrivateClusterConfig{
			PrivateEndpoint: privateIP,
			PublicEndpoint:  publicIP,
		},
	}
}

func createMembership(name, resourceLink string) *gkehubpb.Membership {
	return &gkehubpb.Membership{
		Name: name,
		Type: &gkehubpb.Membership_Endpoint{
			Endpoint: &gkehubpb.MembershipEndpoint{
				Type: &gkehubpb.MembershipEndpoint_GkeCluster{
					GkeCluster: &gkehubpb.GkeCluster{
						ResourceLink:   resourceLink,
						ClusterMissing: false,
					},
				},
			},
		},
	}
}

func createAPIConfig(endpoint string) api.Config {
	return api.Config{
		Clusters: map[string]*api.Cluster{
			"cgw": {
				Server: endpoint,
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			"gcp": {
				AuthProvider: &api.AuthProviderConfig{
					Name:   "gcp",
					Config: map[string]string{},
				},
			},
		},
		Contexts: map[string]*api.Context{
			"cgw": {
				Cluster:  "cgw",
				AuthInfo: "gcp",
			},
		},
		CurrentContext: "cgw",
	}
}
