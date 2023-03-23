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

package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/pkg/log"
)

var (
	cprResource     = schema.GroupVersionResource{Group: "mesh.cloud.google.com", Version: "v1beta1", Resource: "controlplanerevisions"}
	cmResource      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	channelCMMap    = map[string]string{"rapid": "istio-asm-managed-rapid", "regular": "istio-asm-managed", "stable": "istio-asm-managed-stable"}
	cprChannelMap   = map[string]string{"asm-managed-rapid": "rapid", "asm-managed": "regular", "asm-managed-stable": "stable"}
	allowedChannels = []string{"rapid", "regular", "stable"}
)

type addCACertificateRequest struct {
	RequestShared
	CaCertificates
	Channels []string `json:"channels"` // allows which all mcp release channels to add the certs to. if empty will add to all the channels.
}

type CertificateData struct {
	Pem             string   `yaml:"pem,omitempty" json:"pem"`
	SpiffeBundleURL string   `yaml:"spiffeBundleUrl,omitempty" json:"spiffe-bundle-url"`
	CertSigners     []string `yaml:"certSigners,omitempty" json:"cert-signers"`
	TrustDomains    []string `yaml:"trustDomains,omitempty" json:"trust-domains"`
}

type CaCertificates struct {
	CaCertificates []CertificateData `yaml:"caCertificates" json:"ca-certificates"`
}

func (s *server) addCACertificate(req *http.Request) (any, error) {
	var r addCACertificateRequest
	if err := decodeRequest(req, &r); err != nil {
		return false, badRequestResponse(err)
	}
	log.Infof("received addCaCertificate request: %v", r)
	err := validateAddCertificateRequest(r)
	if err != nil {
		return false, badRequestResponse(err)
	}
	client, _, err := s.createKubeClient(req.Context(), s.project, r.Location, r.Cluster)
	if err != nil {
		return false, badRequestResponse(err)
	}

	var configMaps []string
	err = retry.UntilSuccess(func() error {
		configMaps, err = getConfigMapsToAddCertificates(r.Channels, client)
		return err
	}, retry.Timeout(20*time.Second))

	if err != nil {
		return false, internalErrorResponse(err)
	}
	log.Infof("config maps to add new certificate data : %v", configMaps)

	for _, configMap := range configMaps {
		err = retry.UntilSuccess(func() error {
			return addCertToConfigMaps(client, configMap, r.CaCertificates)
		}, retry.Timeout(20*time.Second))

		if err != nil {
			return false, internalErrorResponse(err)
		}
	}
	log.Infof("updated configmap for all required channels with new certificate data.")
	return true, nil
}

func validateAddCertificateRequest(req addCACertificateRequest) error {
	for _, certData := range req.CaCertificates.CaCertificates {
		if certData.Pem == "" && certData.SpiffeBundleURL == "" {
			return fmt.Errorf("one of pem or spiffeBundleUrl should be present")
		} else if certData.Pem != "" && certData.SpiffeBundleURL != "" {
			return fmt.Errorf("both pem and spiffeBundleUrl is present. only one of them is allowed")
		}

		// validate cert in pem format
		if certData.Pem != "" {
			pemBlock, _ := pem.Decode([]byte(certData.Pem))
			if pemBlock == nil {
				return fmt.Errorf("failed to decode pem certificate")
			}
			cert, err := x509.ParseCertificate(pemBlock.Bytes)
			if err != nil {
				return fmt.Errorf("failed to parse X.509 certificate: %v", err)
			}
			if cert.BasicConstraintsValid && !cert.IsCA {
				return fmt.Errorf("certificate is not a CA certificate")
			}
		}
	}

	if len(req.Channels) != 0 {
		for _, channel := range req.Channels {
			if !isInList(allowedChannels, channel) {
				return fmt.Errorf("unknown channel %v. allowed channels are : %v", channel, allowedChannels)
			}
		}
	}
	return nil
}

func addCertToConfigMaps(client kube.Client, configMap string, caCerts CaCertificates) error {
	// get the configmap from istio-system namespace
	cm, err := client.Dynamic().Resource(cmResource).Namespace("istio-system").Get(context.TODO(), configMap, metav1.GetOptions{})
	if err != nil {
		return err
	}
	caStr, found, err := unstructured.NestedString(cm.Object, "data", "mesh") // the structure is data.mesh.caCertificates
	if !found {
		return fmt.Errorf("cannot find the configmap %v in istio-system namespace", configMap)
	}
	if err != nil {
		return err
	}

	newCAStr, updated, err := addCertToCAStr(caStr, caCerts)
	if err != nil {
		return err
	}
	if !updated {
		return nil // TODO: do we need to return back a message to the caller if the cert is already present?
	}
	err = unstructured.SetNestedField(cm.Object, newCAStr, "data", "mesh")
	if err != nil {
		return err
	}
	_, err = client.Dynamic().Resource(cmResource).Namespace("istio-system").Update(context.TODO(), cm, metav1.UpdateOptions{})
	return err
}

func addCertToCAStr(caStr string, caCerts CaCertificates) (string, bool, error) {
	cayaml := CaCertificates{}
	err := yaml.Unmarshal([]byte(caStr), &cayaml)
	if err != nil {
		return "", false, fmt.Errorf("unable to unmarshal current caCertificates: %v", err)
	}

	for _, certData := range caCerts.CaCertificates {
		if certData.Pem != "" {
			if exists(cayaml.CaCertificates, certData.Pem, func(cData CertificateData) string { return cData.Pem }) {
				return caStr, false, nil // the new pem is already present
			}
			cayaml.CaCertificates = append(cayaml.CaCertificates, certData)
		}
		if certData.SpiffeBundleURL != "" {
			if exists(cayaml.CaCertificates, certData.SpiffeBundleURL, func(cData CertificateData) string { return cData.SpiffeBundleURL }) {
				return caStr, false, nil // the new spiffeurl is already present
			}
			cayaml.CaCertificates = append(cayaml.CaCertificates, certData)
		}
	}

	newCAStr, err := yaml.Marshal(&cayaml)
	if err != nil {
		return "", false, fmt.Errorf("unable to marshal after adding new certificate data : %v", err)
	}
	return string(newCAStr), true, nil
}

func exists(existingCerts []CertificateData, newData string, getOldData func(certData CertificateData) string) bool {
	for _, certData := range existingCerts {
		if getOldData(certData) == newData {
			return true
		}
	}
	return false
}

func getConfigMapsToAddCertificates(requestedChannels []string, client kube.Client) ([]string, error) {
	// read the list of mcp release channels from the controlplanerevisions CR in istio-system namespace.
	cprList, err := client.Dynamic().Resource(cprResource).Namespace("istio-system").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, internalErrorResponse(err)
	}

	// get all installed channels
	var installedChannels []string
	for _, cpr := range cprList.Items {
		installedChannels = append(installedChannels, cprChannelMap[cpr.GetName()])
	}
	var addCertChannels []string
	//  if requestedChannels is empty, then add the certs to all the installed channels
	if len(requestedChannels) == 0 {
		addCertChannels = installedChannels
	} else {
		// filter requested channels to include only that are installed
		for _, channel := range requestedChannels {
			if isInList(installedChannels, channel) {
				addCertChannels = append(addCertChannels, channel)
			}
		}
	}
	// add configmaps
	var configMaps []string
	for _, channel := range addCertChannels {
		configMaps = append(configMaps, channelCMMap[channel])
	}
	return configMaps, nil
}

func isInList(l []string, str string) bool {
	for _, s := range l {
		if s == str {
			return true
		}
	}
	return false
}

func badRequestResponse(err error) error {
	return &httpError{
		code:    http.StatusBadRequest,
		message: err.Error(),
	}
}

func internalErrorResponse(err error) error {
	return &httpError{
		code:    http.StatusInternalServerError,
		message: err.Error(),
	}
}
