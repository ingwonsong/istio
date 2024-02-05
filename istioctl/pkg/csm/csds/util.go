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

import (
	"context"
	"crypto/x509"
	"regexp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

const scope string = "https://www.googleapis.com/auth/cloud-platform"

// ConnToGCPWithAuto connects to uri on gcp with auto authentication
func ConnToGCPWithAuto(uri string) (*grpc.ClientConn, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	creds := credentials.NewClientTLSFromCert(pool, "")
	perRPC, err := oauth.NewApplicationDefault(context.Background(), scope) // Application Default Credentials (ADC)
	if err != nil {
		return nil, err
	}

	return grpc.Dial(uri, grpc.WithTransportCredentials(creds), grpc.WithPerRPCCredentials(perRPC))
}

// Translate the csds ClientId to envoyName
// Example of clientId “projects/xxx/networks/mesh:xxxx/nodes/sidecar~10.76.1.9~ratings-v1-56bd67df9c-n2h2q.default~default.svc.cluster.local”
// Extract envoyName "ratings-v1-56bd67df9c-n2h2q.default"
func ClientIDToEnvoyName(clientID string) string {
	re := regexp.MustCompile(`projects/.+/networks/.+/nodes/.+~.+~(.+)~.+`)
	matches := re.FindStringSubmatch(clientID)
	if len(matches) == 0 {
		return clientID
	}
	return matches[1]
}
