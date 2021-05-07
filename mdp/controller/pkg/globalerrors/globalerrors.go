/*
 Copyright Istio Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package globalerrors

import "sync"

var (
	mu       sync.Mutex
	errorMap map[string]error
)

// I don't think this is a reasonable approach for propagating error status conditions to DPR.
func ErrorOnRevision(revision string, err error) {
	mu.Lock()
	defer mu.Unlock()
	if errorMap == nil {
		errorMap = make(map[string]error)
	}
	errorMap[revision] = err
}

func ClearErrorOnRevision(revision string) {
	mu.Lock()
	defer mu.Unlock()
	delete(errorMap, revision)
}

func GetErrorForRevision(revision string) error {
	mu.Lock()
	defer mu.Unlock()
	return errorMap[revision]
}
