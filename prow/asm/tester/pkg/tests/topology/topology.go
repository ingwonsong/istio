//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package topology

import (
	"fmt"
	"log"
	"os"
)

// AddClusterConfig adds the cluster config to the topology file, in the
// structure of []cluster.Config to inform the test framework of details about
// each cluster under test.
// Different types of cluster.Config are registered with pkg/test/framework/components/cluster/factory.go.
func AddClusterConfig(clusterConfig string) error {
	f, err := os.OpenFile(os.Getenv("INTEGRATION_TEST_TOPOLOGY_FILE"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("error opening the integration test topology file: %w", err)
	}
	defer f.Close()

	log.Printf("Writing the below cluster info into the topology file %q:\n%s", os.Getenv("INTEGRATION_TEST_TOPOLOGY_FILE"), clusterConfig)
	if _, err := f.WriteString(clusterConfig + "\n"); err != nil {
		return err
	}

	return nil
}
