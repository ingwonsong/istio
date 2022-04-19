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

package caclient

import (
	"reflect"
	"testing"

	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"istio.io/istio/security/pkg/nodeagent/caclient/providers/google-cas/mock"
)

var (
	fakeCert                 = "foo"
	fakeCertChain            = []string{"baz", "bar"}
	fakeCaBundle             = [][]string{{"bar"}, {"baz", "bar"}}
	fakeExpectedRootCaBundle = []string{"bar"}
	fakePoolLocator          = "projects/test-project/locations/test-location/caPools/test-pool"
	badPoolLocator           = "bad-pool"
	// nolint: lll
	fakePoolWithTemplate = "projects/test-project/locations/test-location/caPools/test-pool:projects/test-project/locations/test-location/certificateTemplates/test-template"
	badPoolWithTemplate  = "projects/test-project/locations/test-location/caPools/test-pool:bad-template"
)

func TestGoogleCASClient(t *testing.T) {
	fakeCombinedCert := append([]string{}, fakeCert)
	fakeCombinedCert = append(fakeCombinedCert, fakeCertChain...)

	testCases := map[string]struct {
		poolLocator        string
		service            mock.CASService
		expectedCert       []string
		expectedCertBundle []string
		expectedSignErr    error
		expectedBundleErr  error
	}{
		"Valid certs with Pool": {
			// Check RootCertBundle is correctly extracted from CAS response
			// Check Certchain is correctly build from CAS response
			poolLocator:        fakePoolLocator,
			service:            mock.CASService{CertPEM: fakeCert, CertChainPEM: fakeCertChain, CaCertBundle: fakeCaBundle},
			expectedCert:       fakeCombinedCert,
			expectedCertBundle: fakeExpectedRootCaBundle,
			expectedSignErr:    nil,
			expectedBundleErr:  nil,
		},
		"Invalid Pool": {
			// Destination is invalid pool
			poolLocator:        badPoolLocator,
			service:            mock.CASService{CertPEM: fakeCert, CertChainPEM: fakeCertChain, CaCertBundle: fakeCaBundle},
			expectedCert:       fakeCombinedCert,
			expectedCertBundle: fakeExpectedRootCaBundle,
			expectedSignErr:    status.Error(codes.InvalidArgument, "malformed ca path"),
			expectedBundleErr:  status.Error(codes.InvalidArgument, "malformed ca path"),
		},
		"Valid certs with Pool and Template": {
			// Check RootCertBundle is correctly extracted from CAS response
			// Check Certchain is correctly build from CAS response
			poolLocator:        fakePoolWithTemplate,
			service:            mock.CASService{CertPEM: fakeCert, CertChainPEM: fakeCertChain, CaCertBundle: fakeCaBundle},
			expectedCert:       fakeCombinedCert,
			expectedCertBundle: fakeExpectedRootCaBundle,
			expectedSignErr:    nil,
			expectedBundleErr:  nil,
		},
		"Invalid Template": {
			// Destination constains invalid template
			poolLocator:        badPoolWithTemplate,
			service:            mock.CASService{CertPEM: fakeCert, CertChainPEM: fakeCertChain, CaCertBundle: fakeCaBundle},
			expectedCert:       fakeCombinedCert,
			expectedCertBundle: fakeExpectedRootCaBundle,
			expectedSignErr:    status.Error(codes.InvalidArgument, "malformed ca certificate template"),
			expectedBundleErr:  nil,
		},
	}

	for id, tc := range testCases {
		// create a local grpc server
		s, lis, err := mock.CreateServer(&tc.service)
		if err != nil {
			t.Fatalf("Test case [%s] Mock CAS Server Init: failed to create server: %v", id, err)
		}
		defer s.Stop()

		cli, err := NewGoogleCASClient(tc.poolLocator,
			option.WithoutAuthentication(),
			option.WithGRPCDialOption(grpc.WithContextDialer(mock.ContextDialerCreate(lis))),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		if err != nil {
			t.Errorf("Test case [%s] Client Init: failed to create ca client: %v", id, err)
		}

		resp, err := cli.CSRSign([]byte{0o1}, 1)
		if err != nil {
			if err.Error() != tc.expectedSignErr.Error() {
				t.Errorf("Test case [%s] Cert Check: error (%s) does not match expected error (%s)", id, err.Error(), tc.expectedSignErr.Error())
			}
		} else {
			if tc.expectedSignErr != nil {
				t.Errorf("Test case [%s] Cert Check: expect error: %s but got no error", id, tc.expectedSignErr.Error())
			} else if !reflect.DeepEqual(resp, tc.expectedCert) {
				t.Errorf("Test case [%s] Cert Check: resp: got %+v, expected %v", id, resp, tc.expectedCert)
			}
		}

		resp, err = cli.GetRootCertBundle()
		if err != nil {
			if err.Error() != tc.expectedBundleErr.Error() {
				t.Errorf("Test case [%s] RootCaBundle check: error (%s) does not match expected error (%s)", id, err.Error(), tc.expectedSignErr.Error())
			}
		} else {
			if tc.expectedBundleErr != nil {
				t.Errorf("Test case [%s] RootCaBundle check: expect error: %s but got no error", id, tc.expectedSignErr.Error())
			} else if !reflect.DeepEqual(resp, tc.expectedCertBundle) {
				t.Errorf("Test case [%s] RootCaBundle check: resp: got %+v, expected %v", id, resp, tc.expectedCertBundle)
			}
		}
	}
}
