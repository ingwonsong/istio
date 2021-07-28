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

package common

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

const (
	UpgradePath = "upgrade-gke"
)

// IsRunningOnCI indicates whether we're running in a CI environment.
func IsRunningOnCI() bool {
	return os.Getenv("CI") == "true"
}


func NewWebServer(supportedHandlers map[string]func() (func(http.ResponseWriter, *http.Request), error)) (net.Listener, error) {
	// Create the mapping of URL paths to handlers.
	router := http.NewServeMux()
	for path, factory := range supportedHandlers {
		// Create an instance of this handler.
		h, err := factory()
		if err != nil {
			log.Printf("failed creating lifecycle handler for path %s: %v", path, err)
			return nil, err
		}
		router.HandleFunc(fmt.Sprintf("/%s", path), h)
	}

	// Automatically assign the next available port.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Printf("failed creating lifecycle server: %v", err)
		return nil, err
	}

	// Start the server.
	go func() {
		if err := http.Serve(listener, router); err != nil {
			log.Printf("webhook server exited with error: %v", err)
		}
	}()
	return listener, nil
}
