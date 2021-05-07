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

package main

import (
	"log"
	"os"

	"istio.io/istio/prow/asm/tester/pkg/tests"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("Expected argument count: expected 1, got %d", len(os.Args))
	}
	skipConfigPath := os.Args[1]
	_, err := tests.ParseSkipConfig(skipConfigPath)
	if err != nil {
		log.Fatalf("Failed to parse skip config: %v", err)
	}
}
