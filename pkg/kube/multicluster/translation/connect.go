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
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"k8s.io/client-go/tools/clientcmd/api"
)

var (
	membershipNameRegex = regexp.MustCompile(`^projects/([^/]+)/locations/([^/]+)/memberships/([^/]+)$`)
	validateTimeout     = time.Second * 5
)

func apiConfigFromMembership(membership *gkehubpb.Membership, hubEndpoint, projectNumber string, validateEndpoint bool) (api.Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()

	cgwEndpoint, err := connectEndpointFromMembership(membership, hubEndpoint, projectNumber)
	if err != nil {
		return api.Config{}, fmt.Errorf("failed to generate connect gateway endpoint: %v", err)
	}

	cgwURL := fmt.Sprintf("https://%s", cgwEndpoint)
	if validateEndpoint {
		err = validateCGWAccess(ctx, cgwURL)
		if err != nil {
			return api.Config{}, fmt.Errorf("failed to validate config gateway endpoint %s: %v",
				cgwURL, err)
		}
	}

	return api.Config{
		Clusters: map[string]*api.Cluster{
			"cgw": {
				Server: cgwURL,
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
	}, nil
}

func connectEndpointFromMembership(membership *gkehubpb.Membership, hubEndpoint, projectNumber string) (string, error) {
	matches := membershipNameRegex.FindStringSubmatch(membership.GetName())
	if len(matches) == 0 {
		return "", fmt.Errorf("cannot parse membership regex from name %s", membership.GetName())
	}

	membershipLocation := matches[2]
	membershipName := matches[3]

	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/gkeMemberships/%s",
		connectGatewayEndpointFromHubEndpoint(hubEndpoint), projectNumber, membershipLocation, membershipName), nil
}

func connectGatewayEndpointFromHubEndpoint(hubEndpoint string) string {
	switch {
	case strings.HasPrefix(hubEndpoint, "autopush-"):
		return "autopush-connectgateway.sandbox.googleapis.com"
	case strings.HasPrefix(hubEndpoint, "staging-"):
		return "staging-connectgateway.sandbox.googleapis.com"
	default:
		return "connectgateway.googleapis.com"
	}
}

// validateCGWAccess tests the following:
// 1) If the CGW API is enabled.
// 2) Whether the caller has permission to call CGW and permission to access the API server resource.
// 3) Whether the resource exists.
func validateCGWAccess(ctx context.Context, url string) error {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return err
	}
	// ConnectGateway doesn't support gRPC client.
	resp, err := oauth2.NewClient(ctx, creds.TokenSource).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return googleapi.CheckResponse(resp)
}
