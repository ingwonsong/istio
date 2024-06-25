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

package bootstrap

import (
	"fmt"
	"os"
	"strings"

	"istio.io/istio/pkg/log"
	"istio.io/istio/security/pkg/k8s/chiron"
)

// initDNSCertsCSMSigner creates CSR with 'pki.gke.io/istiod' signer using the K8s CA
// and gets CA bundle from './var/run/secrets/kubernetes.io/serviceaccount/ca.crt' path.
// This is only invoked for MeshCA and CAS for platforms other than attached platforms.
func (s *Server) initDNSCertsCSMSigner() error {
	var certChain, keyPEM, caBundle []byte
	var err error

	log.Infof("Generating K8S-signed cert for %v", s.dnsNames)
	k8sSigner := chiron.GkeAsmKubernetesSigner

	certChain, keyPEM, _, err = chiron.GenKeyCertK8sCA(s.kubeClient.Kube(),
		strings.Join(s.dnsNames, ","), defaultCACertPath, k8sSigner, false, SelfSignedCACertTTL.Get())
	if err != nil {
		return fmt.Errorf("failed generating key and cert by kubernetes: %v", err)
	}
	caBundle, err = os.ReadFile(defaultCACertPath)
	if err != nil {
		return fmt.Errorf("failed reading %s: %v", defaultCACertPath, err)
	}

	s.addStartFunc("istiod server certificate rotation", func(stop <-chan struct{}) error {
		go func() {
			// Track TTL of DNS cert and renew cert in accordance to grace period.
			s.RotateDNSCertForK8sCA(stop, defaultCACertPath, k8sSigner, false, SelfSignedCACertTTL.Get())
		}()
		return nil
	})
	s.istiodCertBundleWatcher.SetAndNotify(keyPEM, certChain, caBundle)
	return nil
}
