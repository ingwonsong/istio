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

package mcpcallback

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestToPayload(t *testing.T) {
	st := status.New(codes.Internal, "test error")
	st, err := st.WithDetails(wrapperspb.String("test details1"))
	if err != nil {
		t.Fatalf("WithDetails returned unexpected error: %v", err)
	}
	payload, err := toPayload(st, 685032*time.Nanosecond, "rev1", "101010")
	if err != nil {
		t.Fatalf("toPayload returned unexpected error: %v", err)
	}

	transformJSON := cmp.FilterValues(func(x, y string) bool {
		return json.Valid([]byte(x)) && json.Valid([]byte(y))
	}, cmp.Transformer("ParseJSON", func(in string) (out interface{}) {
		if err := json.Unmarshal([]byte(in), &out); err != nil {
			panic(err) // should never occur given previous filter to ensure valid JSON
		}
		return out
	}))

	want := `{
		"startup_duration": "0.000685032s",
		"status": {
			"code":13,
			"message": "test error",
			"details": [{
				"@type": "type.googleapis.com/google.protobuf.StringValue",
				"value": "test details1"
			}]
		},
		"revision": "rev1",
		"instance_id": "101010"
	}`
	if diff := cmp.Diff(want, payload, transformJSON); diff != "" {
		t.Fatalf("toPayload returned unexpected diff (-want +got):\n%s", diff)
	}
}
