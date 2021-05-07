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

package types

// ReleaseChannel is an enum for the various GKE release channels.
type ReleaseChannel string

var (
	None    = addReleaseChannel("")
	Rapid   = addReleaseChannel("rapid")
	Regular = addReleaseChannel("regular")
	Stable  = addReleaseChannel("stable")

	SupportedReleaseChannels []ReleaseChannel
)

func addReleaseChannel(v ReleaseChannel) ReleaseChannel {
	SupportedReleaseChannels = append(SupportedReleaseChannels, v)
	return v
}
