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

package binary

import (
	"bytes"
	"os/exec"
	"path"
	"strings"
	"testing"

	"istio.io/istio/pkg/asm/bcheck/version"
)

var dynamicallyLinked = map[string]bool{
	// For unknown reasons, pilot-agent is not linked dynamically. While we should figure out why, this
	// is actually perfect fine (arguably better) since we run it alongside Envoy which is dynamically linked anyway.
	"pilot-agent": true,
}

// Test that binary sizes do not bloat
func TestBoringssl(t *testing.T) {
	runBinariesTest(t, func(t *testing.T, name string) {
		cmd := path.Join(*releasedir, name)
		v, err := version.ReadExe(cmd)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}
		if !v.BoringCrypto {
			t.Fatalf("binary not using boringssl")
		}

		static, err := isStaticallyLinked(cmd)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}

		if dynamicallyLinked[name] {
			if static {
				t.Fatalf("expected dynamic linking")
			}
		} else {
			if !static {
				t.Fatalf("binary not statically compiled")
			}
		}
	})
}

// isStaticallyLinked checks if a binary is statically linked or not
func isStaticallyLinked(path string) (bool, error) {
	out := &bytes.Buffer{}
	c := exec.Command("ldd", path)
	c.Stdout = out
	c.Stderr = out
	if err := c.Run(); err != nil {
		_, standardExit := err.(*exec.ExitError)
		if !standardExit {
			return false, err
		}
	}
	return strings.Contains(out.String(), "not a dynamic executable"), nil
}
