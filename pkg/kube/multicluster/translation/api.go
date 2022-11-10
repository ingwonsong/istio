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

	"cloud.google.com/go/container/apiv1/containerpb"
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"

	"istio.io/pkg/log"
)

type membershipLister interface {
	ListMemberships(ctx context.Context, req *gkehubpb.ListMembershipsRequest, opts ...gax.CallOption) ([]*gkehubpb.Membership, error)
}

type clusterFetcher interface {
	GetCluster(ctx context.Context, req *containerpb.GetClusterRequest, opts ...gax.CallOption) (*containerpb.Cluster, error)
}

type flattenedMembershipLister struct {
	hubClient *gkehub.GkeHubMembershipClient
}

func (l *flattenedMembershipLister) ListMemberships(ctx context.Context,
	req *gkehubpb.ListMembershipsRequest, opts ...gax.CallOption,
) ([]*gkehubpb.Membership, error) {
	var memberships []*gkehubpb.Membership

	membershipIter := l.hubClient.ListMemberships(ctx, req)
	for {
		membership, err := membershipIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Errorf("failed to retrieve membership: %v", err)
			return nil, err
		}
		memberships = append(memberships, membership)
	}

	return memberships, nil
}
