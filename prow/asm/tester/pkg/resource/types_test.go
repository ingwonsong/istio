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

package resource

import "testing"

func TestCAType_Set(t *testing.T) {
	type args struct {
		value string
	}
	tests := []struct {
		name    string
		ca      CAType
		args    args
		wantErr bool
	}{
		{
			name: "Testing lowercase CA string",
			ca:   "",
			args: args{
				value: "citadel",
			},
			wantErr: false,
		},
		{
			name: "Testing uppercase CA string",
			ca:   "",
			args: args{
				value: "CITADEL",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.ca.Set(tt.args.value); (err != nil) != tt.wantErr {
				t.Errorf("Set() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
