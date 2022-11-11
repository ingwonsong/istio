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
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"google.golang.org/api/option"
	"k8s.io/client-go/tools/clientcmd/api"

	"istio.io/pkg/log"
)

const (
	initTimeout = 10 * time.Second
)

// Cache returns an API config cached for the given IP.
type Cache interface {
	Get(ip string) (api.Config, bool)
	Run(stop <-chan struct{})
}

type membershipCache struct {
	opts environmentOpts

	clusterFetcher   clusterFetcher
	membershipLister membershipLister

	knownPublicIPs map[string]bool

	publicIPToMembership  map[string]*gkehubpb.Membership
	privateIPToMembership map[string]*gkehubpb.Membership

	// Determines whether to attempt connection to the generated CGW endpoint
	// before adding to the cache.
	validateEndpoint bool

	shutdown func()
}

type environmentOpts struct {
	projectNumber     string
	hubEndpoint       string
	hubMembershipName string
	fleetProjectID    string
}

// NewIPMembershipCache returns a cache that correlates remote secret IPs to their connect gateway endpoints.
func NewIPMembershipCache() (Cache, error) {
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()

	opts, err := optsFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to read required opts from environment: %v", err)
	}

	cc, err := container.NewClusterManagerClient(ctx, option.WithQuotaProject(opts.projectNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster manager client: %v", err)
	}
	hc, err := gkehub.NewGkeHubMembershipRESTClient(ctx,
		option.WithQuotaProject(opts.projectNumber), option.WithEndpoint(opts.hubEndpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to create hub membership client: %v", err)
	}

	mc := &membershipCache{
		opts:                  opts,
		clusterFetcher:        cc,
		membershipLister:      &flattenedMembershipLister{hubClient: hc},
		publicIPToMembership:  map[string]*gkehubpb.Membership{},
		privateIPToMembership: map[string]*gkehubpb.Membership{},
		knownPublicIPs:        map[string]bool{},
		validateEndpoint:      true,
		shutdown: func() {
			cc.Close()
			hc.Close()
		},
	}
	if err := mc.refreshCache(); err != nil {
		log.Errorf("Failed to seed translation cache: %v", err)
	}

	return mc, nil
}

func (m *membershipCache) Get(ip string) (api.Config, bool) {
	if _, ok := m.knownPublicIPs[ip]; ok {
		log.Infof("Skipping cache refresh for public cluster with endpoint %s", ip)
		return api.Config{}, false
	}

	apiConfig, ok := m.apiConfig(ip)
	if ok {
		return apiConfig, true
	}

	if err := m.refreshCache(); err != nil {
		log.Errorf("Failed to refresh translation cache: %v", err)
		return api.Config{}, false
	}

	apiConfig, ok = m.apiConfig(ip)
	if ok {
		return apiConfig, true
	}

	return api.Config{}, false
}

func (m *membershipCache) Run(stop <-chan struct{}) {
	go func(stop <-chan struct{}) {
		<-stop
		log.Infof("Shutting down membership cache")
		m.shutdown()
	}(stop)
}

func (m *membershipCache) refreshCache() error {
	log.Infof("Refreshing membership cache")
	memberships, err := m.membershipLister.ListMemberships(context.Background(), &gkehubpb.ListMembershipsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", m.opts.fleetProjectID),
	})
	if err != nil {
		return fmt.Errorf("failed to list memberships for project %s: %v", m.opts.fleetProjectID, err)
	}

	for _, membership := range memberships {
		cluster, err := m.clusterFromMembership(membership)
		if err != nil {
			log.Errorf("Failed to retrieve cluster for membership %s: %v", membership.GetName(), err)
			continue
		}

		if privateConfig := cluster.GetPrivateClusterConfig(); privateConfig != nil {
			m.publicIPToMembership[privateConfig.PublicEndpoint] = membership
			m.privateIPToMembership[privateConfig.PrivateEndpoint] = membership
		} else {
			m.knownPublicIPs[cluster.GetEndpoint()] = true
		}
	}

	return nil
}

func (m *membershipCache) clusterFromMembership(membership *gkehubpb.Membership) (*containerpb.Cluster, error) {
	if membership == nil {
		return nil, fmt.Errorf("empty membership")
	}

	endpoint := membership.GetEndpoint()
	if endpoint == nil {
		return nil, fmt.Errorf("failed to get endpoint from membership: %s", membership.GetName())
	}

	if endpoint.GetGkeCluster() == nil {
		return nil, fmt.Errorf("failed to retrieve cluster resource link from membership %s",
			membership.GetName())
	}

	// In the form //`<CLUSTER TYPE>.googleapis.com/projects/my-project/locations/us-west1-a/clusters/my-cluster`.
	resourceLink := endpoint.GetGkeCluster().GetResourceLink()

	// Cluster name required in the form `projects/*/locations/*/clusters/*`.
	parts := strings.Split(resourceLink, "/")
	if len(parts) <= 3 {
		return nil, fmt.Errorf("invalid cluster resource link format %s", resourceLink)
	}

	clusterName := strings.Join(parts[3:], "/")

	c, err := m.clusterFetcher.GetCluster(context.Background(), &containerpb.GetClusterRequest{
		Name: clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster %s: %v", clusterName, err)
	}

	return c, nil
}

func (m *membershipCache) apiConfig(ip string) (api.Config, bool) {
	cachedMembership, ok := m.cachedMembership(ip)
	if ok {
		log.Infof("Found cached membership %s for IP %s", cachedMembership.GetName(), ip)
		config, err := apiConfigFromMembership(
			cachedMembership, m.opts.hubEndpoint, m.opts.projectNumber, m.validateEndpoint)
		if err != nil {
			log.Errorf("Failed to get apiConfig from membership: %v", err)
			return api.Config{}, false
		}

		return config, true
	}

	return api.Config{}, false
}

func (m *membershipCache) cachedMembership(ip string) (*gkehubpb.Membership, bool) {
	if _, ok := m.publicIPToMembership[ip]; ok {
		return m.publicIPToMembership[ip], true
	}
	if _, ok := m.privateIPToMembership[ip]; ok {
		return m.privateIPToMembership[ip], true
	}
	return nil, false
}

func optsFromEnvironment() (environmentOpts, error) {
	projectNumber := os.Getenv("PROJECT_NUMBER")
	if projectNumber == "" {
		return environmentOpts{}, fmt.Errorf("could not read tenant project number from environment")
	}

	// GKE Hub membership full resource name (https://google.aip.dev/122) with owning API prepended.
	// e.g. gkehub.googleapis.com/project/foo/locations/global/memberships/bar
	hubMembership := os.Getenv("GKE_HUB_MEMBERSHIP")
	if hubMembership == "" {
		return environmentOpts{}, fmt.Errorf("could not read hub membership from environment")
	}

	u, err := url.Parse(hubMembership)
	if err != nil {
		return environmentOpts{}, fmt.Errorf("failed to parse GKE Hub membership %v: %v", hubMembership, err)
	}

	cgwPathRegex := regexp.MustCompile(`^/projects/([^/]+)/locations/([^/]+)/memberships/([^/]+)$`)
	pathMatches := cgwPathRegex.FindStringSubmatch(u.Path)
	if len(pathMatches) == 0 {
		return environmentOpts{}, fmt.Errorf("cannot parse path regex from path: %s", u.Path)
	}

	return environmentOpts{
		projectNumber:     projectNumber,
		fleetProjectID:    pathMatches[1],
		hubMembershipName: pathMatches[3],
		hubEndpoint:       u.Host,
	}, nil
}
