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

package csds

// call csds

import (
	"context"
	"strconv"

	config "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	csds "github.com/envoyproxy/go-control-plane/envoy/service/status/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/util/sets"
)

// ClientV3 implements the Client interface
type Client struct {
	clientConn        *grpc.ClientConn
	csdsClient        csds.ClientStatusDiscoveryServiceClient
	xdsIdentifierNode *config.Node
	metadata          metadata.MD
	opts              ClientOptions
	podsConnectedToTD map[string]struct{}
}

// ClientOptions are options that are common to use in all the version implementations of client
type ClientOptions struct {
	ProjectNumber int64
	NodeID        string
	MeshName      string
	EnvoyDump     []byte
}

type csdsNodeMatcher struct {
	NodeID        string
	ProjectNumber int64
	MeshName      string
	XdsIdentifier string
}

var uri = env.RegisterStringVar("TRAFFIC_DIRECTOR_ADDR", "trafficdirector.googleapis.com:443", "Address of Traffic Director endpoint").Get()

// connWithAuth connects to uri with authentication
func (c *Client) connWithAuth() error {
	var err error
	c.metadata = metadata.Pairs("x-goog-user-project", strconv.FormatInt(c.opts.ProjectNumber, 10))
	c.clientConn, err = ConnToGCPWithAuto(uri)
	return err
}

// New creates a new client with v3 api version
func New(option ClientOptions) *Client {
	if option.ProjectNumber == 0 || option.MeshName == "" {
		return nil
	}
	return &Client{
		opts: option,
		xdsIdentifierNode: &config.Node{Id: "projects/" + strconv.FormatInt(option.ProjectNumber, 10) +
			"/networks/" + option.MeshName + "/nodes/sidecar~placeholder.svc.cluster.local"},
		podsConnectedToTD: map[string]struct{}{},
	}
}

func buildNodeMatchers(csdsNode *csdsNodeMatcher) []*matcher.NodeMatcher {
	nm := &matcher.NodeMatcher{}
	nm.NodeMetadatas = []*matcher.StructMatcher{
		{
			Path: []*matcher.StructMatcher_PathSegment{
				{
					Segment: &matcher.StructMatcher_PathSegment_Key{
						Key: "TRAFFICDIRECTOR_GCP_PROJECT_NUMBER",
					},
				},
			},
			Value: &matcher.ValueMatcher{
				MatchPattern: &matcher.ValueMatcher_StringMatch{
					StringMatch: &matcher.StringMatcher{
						MatchPattern: &matcher.StringMatcher_Exact{
							Exact: strconv.FormatInt(csdsNode.ProjectNumber, 10),
						},
					},
				},
			},
		},
		{
			Path: []*matcher.StructMatcher_PathSegment{
				{
					Segment: &matcher.StructMatcher_PathSegment_Key{
						Key: "TRAFFICDIRECTOR_MESH_SCOPE_NAME",
					},
				},
			},
			Value: &matcher.ValueMatcher{
				MatchPattern: &matcher.ValueMatcher_StringMatch{
					StringMatch: &matcher.StringMatcher{
						MatchPattern: &matcher.StringMatcher_Exact{
							Exact: csdsNode.MeshName,
						},
					},
				},
			},
		},
	}

	if csdsNode.NodeID != "" {
		nm.NodeId = &matcher.StringMatcher{
			MatchPattern: &matcher.StringMatcher_Exact{
				Exact: csdsNode.NodeID,
			},
		}
	}
	return []*matcher.NodeMatcher{nm}
}

// Run connects the client to the uri and calls doRequest
func (c *Client) Run() (map[string]*csds.ClientStatusResponse, error) {
	if c == nil {
		return nil, nil
	}
	if err := c.connWithAuth(); err != nil {
		return nil, err
	}
	defer c.clientConn.Close()

	c.csdsClient = csds.NewClientStatusDiscoveryServiceClient(c.clientConn)
	ctx := metadata.NewOutgoingContext(context.Background(), c.metadata)
	streamClientStatus, err := c.csdsClient.StreamClientStatus(ctx)
	if err != nil {
		return nil, err
	}
	csdsResponses := map[string]*csds.ClientStatusResponse{}
	// build the first nodeMatcher
	nm := buildNodeMatchers(&csdsNodeMatcher{
		ProjectNumber: c.opts.ProjectNumber,
		MeshName:      c.opts.MeshName,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(streamClientStatus, nm)
	if err != nil {
		log.Errorf("Error calling trafficdirector.googleapis.com. Error details: {%v}", err)
		return nil, err
	}
	err = streamClientStatus.CloseSend()
	if err != nil {
		log.Infof("Error closing client stream send. Error details: {%v}", err)
	}

	clientIDs := parseAllConfigResponse(resp)

	// Need to make one more call per proxy to CSDS to fetch sync status.
	// Find the new NodeIds to update the nodeMatcher with the new NodeMacher
	if len(clientIDs) == 0 {
		return nil, nil
	}
	for id := range clientIDs {
		if c.opts.NodeID != "" && c.opts.NodeID != ClientIDToEnvoyName(id) {
			continue
		}
		nnm := buildNodeMatchers(&csdsNodeMatcher{
			ProjectNumber: c.opts.ProjectNumber,
			MeshName:      c.opts.MeshName,
			NodeID:        id,
		})

		streamClientStatus, err := c.csdsClient.StreamClientStatus(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := c.doRequest(streamClientStatus, nnm)
		if err != nil {
			log.Errorf("Error calling trafficdirector.googleapis.com. Error details: {%v}", err)
			continue
		}
		if err = streamClientStatus.CloseSend(); err != nil {
			log.Infof("Error closing client stream send. Error details: {%v}", err)
		}

		csdsResponses[ClientIDToEnvoyName(id)] = resp
	}

	return csdsResponses, nil
}

// doRequest sends request
func (c *Client) doRequest(
	streamClientStatus csds.ClientStatusDiscoveryService_StreamClientStatusClient,
	nodeMatcher []*matcher.NodeMatcher,
) (*csds.ClientStatusResponse, error) {
	req := &csds.ClientStatusRequest{
		Node:         c.xdsIdentifierNode,
		NodeMatchers: nodeMatcher,
	}
	if err := streamClientStatus.Send(req); err != nil {
		return nil, err
	}
	return streamClientStatus.Recv()
}

// printOutResponse processes response and get all the clientIDs connected to TD
func parseAllConfigResponse(response *csds.ClientStatusResponse) sets.Set[string] {
	if len(response.GetConfig()) == 0 {
		log.Warnf("No xDS clients connected to TD.")
		return nil
	}
	clientIDs := sets.Set[string]{}
	for _, config := range response.GetConfig() {
		if config.GetNode() != nil {
			clientIDs[config.GetNode().GetId()] = struct{}{}
		}
	}
	return clientIDs
}
