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
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"k8s.io/client-go/tools/clientcmd/api"
)

var (
	membershipNameRegex = regexp.MustCompile(`^projects/([^/]+)/locations/([^/]+)/memberships/([^/]+)$`)

	cgwHostRegex = regexp.MustCompile(`^([^\.]*)-?(autopush|staging)?\-?connectgateway.(?:sandbox\.)?googleapis.com$`)
	cgwPathRegex = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/gkeMemberships/([^/]+)$`)
)

func apiConfigFromMembership(membership *gkehubpb.Membership, hubEndpoint, fleetProjectNumber string) (api.Config, error) {
	cgwEndpoint, err := connectEndpointFromMembership(membership, hubEndpoint, fleetProjectNumber)
	if err != nil {
		return api.Config{}, fmt.Errorf("failed to generate connect gateway endpoint: %w", err)
	}

	cgwURL := fmt.Sprintf("https://%s", cgwEndpoint)
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

func connectGatewayKubeConfig(hostname string) (api.Config, error) {
	// Connect Gateway URL must be in the form:
	// https://[LOCATION]-[ENVIRONMENT]-connectgateway.[sandbox.]googleapis.com/v1/projects/[PROJECT NUMBER]/locations/[LOCATION]/gkeMemberships/[MEMBERSHIP NAME]
	cgwURL, err := url.Parse(hostname)
	if err != nil {
		return api.Config{}, fmt.Errorf("failed to parse connect gateway URL from server %s", hostname)
	}

	// Parse the membership location and GKE Connect environment from the host if possible.
	hostMatches := cgwHostRegex.FindStringSubmatch(cgwURL.Host)
	if len(hostMatches) == 0 {
		return api.Config{}, fmt.Errorf("cannot parse host regex from host: %s", cgwURL.Host)
	}

	prefixMembershipLocation := hostMatches[1]
	connectEnvironment := hostMatches[2]

	// For the case where the regex captures the environment as the first group (when URL starts [ENV]-connectgateway..)
	// Can be handled in the regex, but it's simpler to fix here in the code.
	if strings.Contains(prefixMembershipLocation, "staging") ||
		strings.Contains(prefixMembershipLocation, "autopush") {
		connectEnvironment = hostMatches[1]
		prefixMembershipLocation = ""
	}

	pathMatches := cgwPathRegex.FindStringSubmatch(cgwURL.Path)
	if len(pathMatches) == 0 {
		return api.Config{}, fmt.Errorf("cannot parse path regex from path: %s", cgwURL.Path)
	}

	projectNumber := pathMatches[1]
	pathMembershipLocation := pathMatches[2]
	membershipName := pathMatches[3]

	sb := strings.Builder{}
	sb.WriteString("https://")
	if prefixMembershipLocation != "" {
		sb.WriteString(prefixMembershipLocation)
	}
	if connectEnvironment != "" {
		sb.WriteString(connectEnvironment)
	}
	sb.WriteString("connectgateway.")
	if connectEnvironment != "" {
		sb.WriteString("sandbox.")
	}
	sb.WriteString("googleapis.com")
	sb.WriteString(fmt.Sprintf("/v1/projects/%s/locations/%s/gkeMemberships/%s",
		projectNumber, pathMembershipLocation, membershipName))

	return api.Config{
		Clusters: map[string]*api.Cluster{
			"cgw": {
				Server: sb.String(),
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
