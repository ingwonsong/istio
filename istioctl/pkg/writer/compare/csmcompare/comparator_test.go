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

package csmcompare

import (
	"bytes"
	"cmp"
	"os"
	"testing"

	csds "github.com/envoyproxy/go-control-plane/envoy/service/status/v3"

	"istio.io/istio/pkg/util/protomarshal"
)

func TestComparator(t *testing.T) {
	testcases := []struct {
		name              string
		envoyDumpInput    string
		csdsResponseInput string
		want              string
	}{
		{
			name:              "Test comparator with no config diff",
			envoyDumpInput:    "testdata/envoy-response.txt",
			csdsResponseInput: "testdata/csds-response-no-diff.txt",
			want:              "testdata/print-no-diff.txt",
		},
		{
			name:              "Test comparator with config diff",
			envoyDumpInput:    "testdata/envoy-response.txt",
			csdsResponseInput: "testdata/csds-response-with-diff.txt",
			want:              "testdata/print-diff.txt",
		},
	}
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			ci, err := os.ReadFile(tt.csdsResponseInput)
			if err != nil {
				panic(err)
			}
			cr := &csds.ClientStatusResponse{}
			err = protomarshal.Unmarshal(ci, cr)
			if err != nil {
				panic(err)
			}
			csdsInput := map[string]*csds.ClientStatusResponse{}
			csdsInput["placeholder"] = cr

			var output bytes.Buffer
			envoyInput, _ := os.ReadFile(tt.envoyDumpInput)
			c, err := NewComparator(&output, csdsInput, envoyInput)
			if err != nil {
				panic(err)
			}
			c.Diff()
			want, _ := os.ReadFile(tt.want)
			if diff := cmp.Compare(string(want), output.String()); diff != 0 {
				t.Errorf("Unexpected output")
			}
		})
	}
}
