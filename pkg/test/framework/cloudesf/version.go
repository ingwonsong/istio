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

package cloudesf

import (
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	"istio.io/istio/pkg/test/env"
)

var versionFilePath = filepath.Join(env.IstioSrc, "cloudesf/Makefile.cloudesf.version.mk")

func Version() string {
	file, err := ioutil.ReadFile(versionFilePath)
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(file))
	re := regexp.MustCompile("CLOUDESF_VERSION = \"(.*?)\"")

	match := re.FindStringSubmatch(version)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
