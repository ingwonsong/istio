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

package kms

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thalescpl-io/k8s-kms-plugin/apis/istio/v1"
	"google.golang.org/grpc"
)

const (
	defaultKek = "a37807cd-6d1a-4d75-813a-e120f30176f7"
	timeout    = time.Second
)

// mockKmsService is a simple mocked Google KMS Service.
type mockKmsService struct {
	kekKid            []byte
	encryptedDekBlob  []byte
	encryptedSkeyBlob []byte
	plaintextSkey     []byte
	ciphertext        []byte
	plaintext         []byte
	err               error
}

// mockKmsServer is the mocked KMS server.
type mockKmsServer struct {
	Server  *grpc.Server
	Address string
}

// GenerateKEK returns the KID of the GeneratedKEK if allowed/successful
func (m *mockKmsService) GenerateKEK(ctx context.Context, request *istio.GenerateKEKRequest) (
	*istio.GenerateKEKResponse, error) {
	out := &istio.GenerateKEKResponse{
		KekKid: m.kekKid,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

// GenerateDEK returns a wrapped (by HSM handled KEK)
func (m *mockKmsService) GenerateDEK(ctx context.Context, request *istio.GenerateDEKRequest) (
	*istio.GenerateDEKResponse, error) {
	out := &istio.GenerateDEKResponse{
		EncryptedDekBlob: m.encryptedDekBlob,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

// GenerateSKey returns a wrapped (by provided encrypted DEK ), for later use during loading and signing key generation
func (m *mockKmsService) GenerateSKey(ctx context.Context, request *istio.GenerateSKeyRequest) (
	*istio.GenerateSKeyResponse, error) {
	out := &istio.GenerateSKeyResponse{
		EncryptedSkeyBlob: m.encryptedSkeyBlob,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

// LoadSKey returns the SKey unwrapped for the controller to use for CA work...
func (m *mockKmsService) LoadSKey(ctx context.Context, request *istio.LoadSKeyRequest) (
	*istio.LoadSKeyResponse, error) {
	out := &istio.LoadSKeyResponse{
		PlaintextSkey: m.plaintextSkey,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

func (m *mockKmsService) AuthenticatedEncrypt(ctx context.Context, request *istio.AuthenticatedEncryptRequest) (
	*istio.AuthenticatedEncryptResponse, error) {
	out := &istio.AuthenticatedEncryptResponse{
		Ciphertext: m.ciphertext,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

func (m *mockKmsService) AuthenticatedDecrypt(ctx context.Context, request *istio.AuthenticatedDecryptRequest) (
	*istio.AuthenticatedDecryptResponse, error) {
	out := &istio.AuthenticatedDecryptResponse{
		Plaintext: m.plaintext,
	}
	if m.err != nil {
		return nil, m.err
	}
	return out, nil
}

func (m *mockKmsService) ImportCACert(ctx context.Context, request *istio.ImportCACertRequest) (
	*istio.ImportCACertResponse, error) {
	return nil, nil
}

func (m *mockKmsService) VerifyCertChain(ctx context.Context, request *istio.VerifyCertChainRequest) (
	*istio.VerifyCertChainResponse, error) {
	return nil, nil
}

// CreateServer creates a mocked local Google CA server and runs it in a separate thread.
func newMockKmsServer(t *testing.T, service *mockKmsService) *mockKmsServer {
	// create a local grpc server
	s := &mockKmsServer{
		Server: grpc.NewServer(),
	}
	t.Cleanup(s.Stop)
	d := t.TempDir()
	socket := filepath.Join(d, "test.sock")

	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	s.Address = lis.Addr().String()

	go func() {
		istio.RegisterKeyManagementServiceServer(s.Server, service)
		_ = s.Server.Serve(lis)
	}()

	return s
}

// Stop stops the Mock Mesh CA server.
func (s *mockKmsServer) Stop() {
	if s.Server != nil {
		s.Server.Stop()
	}
}

func TestGenerateDEK(t *testing.T) {
	testCases := map[string]struct {
		service     mockKmsService
		expectedDek []byte
		expectedErr string
	}{
		"Valid": {
			service: mockKmsService{
				kekKid:           []byte(defaultKek),
				encryptedDekBlob: []byte("abc"),
			},
			expectedDek: []byte("abc"),
			expectedErr: "",
		},
		"Error in Response": {
			service: mockKmsService{
				err: fmt.Errorf("kms internal error"),
			},
			expectedDek: nil,
			expectedErr: "rpc error: code = Unknown desc = kms internal error",
		},
	}

	for id, tc := range testCases {
		t.Run(id, func(t *testing.T) {
			// create a local grpc server
			s := newMockKmsServer(t, &tc.service)

			var kesClient KeyEncryptionService
			kesClient.Endpoint = s.Address

			if err := kesClient.Connect(timeout); err != nil {
				t.Errorf("failed to create client: %v", err)
			}

			kesClient.ctx = context.Background()
			resp, err := kesClient.GenerateDEK(tc.service.kekKid)
			if err != nil {
				if err.Error() != tc.expectedErr {
					t.Errorf("error (%s) does not match \n expected error (%s)", err.Error(), tc.expectedErr)
				}
			} else {
				if tc.expectedErr != "" {
					t.Errorf("expect error: %s \n but got no error", tc.expectedErr)
				} else if !reflect.DeepEqual(resp, tc.expectedDek) {
					t.Errorf("resp: got %+v, \n expected %v", resp, tc.expectedDek)
				}
			}
		})
	}
}

func TestGenerateGenerateSKey(t *testing.T) {
	testCases := map[string]struct {
		service      mockKmsService
		keyType      KeyType
		keySize      int
		expectedSkey []byte
		expectedErr  string
	}{
		"Valid AES key": {
			service: mockKmsService{
				encryptedSkeyBlob: []byte("abc"),
			},
			keyType:      AES,
			keySize:      2,
			expectedSkey: []byte("abc"),
			expectedErr:  "",
		},
		"Valid RSA key": {
			service: mockKmsService{
				encryptedSkeyBlob: []byte("abc"),
			},
			keyType:      RSA,
			keySize:      2,
			expectedSkey: []byte("abc"),
			expectedErr:  "",
		},
		"Valid ECC key": {
			service: mockKmsService{
				encryptedSkeyBlob: []byte("abc"),
			},
			keyType:      ECC,
			keySize:      2,
			expectedSkey: []byte("abc"),
			expectedErr:  "",
		},
		"Valid Unknown key": {
			service: mockKmsService{
				encryptedSkeyBlob: []byte("abc"),
			},
			keyType:      0,
			keySize:      2,
			expectedSkey: []byte("abc"),
			expectedErr:  "",
		},
		"Error in Response": {
			service: mockKmsService{
				err: fmt.Errorf("kms internal error"),
			},
			keyType:      AES,
			keySize:      2,
			expectedSkey: nil,
			expectedErr:  "rpc error: code = Unknown desc = kms internal error",
		},
	}

	for id, tc := range testCases {
		t.Run(id, func(t *testing.T) {
			// create a local grpc server
			s := newMockKmsServer(t, &tc.service)

			var kesClient KeyEncryptionService
			kesClient.Endpoint = s.Address

			if err := kesClient.Connect(timeout); err != nil {
				t.Errorf("failed to create client: %v", err)
			}

			kesClient.ctx = context.Background()
			resp, err := kesClient.GenerateSKey(tc.service.kekKid, tc.service.encryptedDekBlob, tc.keySize, tc.keyType)
			if err != nil {
				if err.Error() != tc.expectedErr {
					t.Errorf("error (%s) does not match \n expected error (%s)", err.Error(), tc.expectedErr)
				}
			} else {
				if tc.expectedErr != "" {
					t.Errorf("expect error: %s \n but got no error", tc.expectedErr)
				} else if !reflect.DeepEqual(resp, tc.expectedSkey) {
					t.Errorf("resp: got %+v, \n expected %v", resp, tc.expectedSkey)
				}
			}
		})
	}
}

func TestGenerateAuthenticatedEncrypt(t *testing.T) {
	testCases := map[string]struct {
		service     mockKmsService
		aad         []byte
		expectedkey []byte
		expectedErr string
	}{
		"Valid": {
			service: mockKmsService{
				ciphertext: []byte("abc-cipher"),
				plaintext:  []byte("abc-plain"),
			},
			aad:         []byte{2},
			expectedkey: []byte("abc-cipher"),
			expectedErr: "",
		},
		"Error in Response": {
			service: mockKmsService{
				err: fmt.Errorf("kms internal error"),
			},
			aad:         []byte{2},
			expectedkey: []byte("abc"),
			expectedErr: "rpc error: code = Unknown desc = kms internal error",
		},
	}

	for id, tc := range testCases {
		t.Run(id, func(t *testing.T) {
			// create a local grpc server
			s := newMockKmsServer(t, &tc.service)

			var kesClient KeyEncryptionService
			kesClient.Endpoint = s.Address

			if err := kesClient.Connect(timeout); err != nil {
				t.Errorf("failed to create client: %v", err)
			}

			kesClient.ctx = context.Background()
			resp, err := kesClient.AuthenticatedEncrypt(tc.service.kekKid, tc.service.encryptedDekBlob, tc.aad, tc.service.plaintext)
			if err != nil {
				if err.Error() != tc.expectedErr {
					t.Errorf("error (%s) does not match \n expected error (%s)", err.Error(), tc.expectedErr)
				}
			} else {
				if tc.expectedErr != "" {
					t.Errorf("expect error: %s \n but got no error", tc.expectedErr)
				} else if !reflect.DeepEqual(resp, tc.expectedkey) {
					t.Errorf("resp: got %+v, \n expected %v", resp, tc.expectedkey)
				}
			}
		})
	}
}

func TestGenerateAuthenticatedDecrypt(t *testing.T) {
	testCases := map[string]struct {
		service     mockKmsService
		aad         []byte
		expectedkey []byte
		expectedErr string
	}{
		"Valid": {
			service: mockKmsService{
				ciphertext: []byte("abc-cipher"),
				plaintext:  []byte("abc-plain"),
			},
			aad:         []byte{2},
			expectedkey: []byte("abc-plain"),
			expectedErr: "",
		},
		"Error in Response": {
			service: mockKmsService{
				err: fmt.Errorf("kms internal error"),
			},
			aad:         []byte{2},
			expectedkey: []byte("abc"),
			expectedErr: "rpc error: code = Unknown desc = kms internal error",
		},
	}

	for id, tc := range testCases {
		t.Run(id, func(t *testing.T) {
			// create a local grpc server
			s := newMockKmsServer(t, &tc.service)

			var kesClient KeyEncryptionService
			kesClient.Endpoint = s.Address

			if err := kesClient.Connect(timeout); err != nil {
				t.Errorf("failed to create client: %v", err)
			}

			kesClient.ctx = context.Background()
			resp, err := kesClient.AuthenticatedDecrypt(tc.service.kekKid, tc.service.encryptedDekBlob, tc.aad, tc.service.ciphertext)
			if err != nil {
				if err.Error() != tc.expectedErr {
					t.Errorf("error (%s) does not match \n expected error (%s)", err.Error(), tc.expectedErr)
				}
			} else {
				if tc.expectedErr != "" {
					t.Errorf("expect error: %s \n but got no error", tc.expectedErr)
				} else if !reflect.DeepEqual(resp, tc.expectedkey) {
					t.Errorf("resp: got %+v, \n expected %v", resp, tc.expectedkey)
				}
			}
		})
	}
}

func TestConnection(t *testing.T) {
	testCases := map[string]struct {
		createFile  bool
		addr        string
		expectedErr []string
	}{
		"Non-socket": {
			createFile:  true,
			expectedErr: []string{"socket operation on non-socket", "connection refused"},
		},
		"Server not started": {
			createFile:  false,
			expectedErr: []string{"no such file or directory"},
		},
		"Fake endpoint address": {
			createFile:  false,
			expectedErr: []string{"no such file or directory"},
		},
	}

	for id, tc := range testCases {
		t.Run(id, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.sock")
			if tc.createFile {
				os.Create(path)
			}

			var kesClient KeyEncryptionService
			kesClient.Endpoint = path
			err := kesClient.Connect(timeout)
			if err != nil {
				t.Error("Problem calling Connect")
			}

			// Connect() is asynchronous, so we need to make a call to actually check the connection.
			_, err = kesClient.GenerateDEK([]byte(defaultKek))

			var matchedErr bool
			if err != nil {
				for _, tcExpectedErr := range tc.expectedErr {
					if strings.Contains(err.Error(), tcExpectedErr) {
						matchedErr = true
						break
					}
				}
				if !matchedErr {
					t.Errorf("error (%s) does not match \n expected error (%s)", err.Error(), tc.expectedErr)
				}
			} else {
				t.Errorf("got no error, \n but expected %v", tc.expectedErr)
			}
		})
	}
}
