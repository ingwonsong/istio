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

package asm

import (
	"bytes"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"istio.io/istio/pilot/test/util"
)

func TestMCPCheck(t *testing.T) {
	cases := []struct {
		name string
	}{
		{"full"},
		{"empty"},
		{"explicitly-empty"},
		{"default-profile"},
		{"customer-example-1"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			w := bytes.NewBuffer(nil)
			out := t.TempDir()
			goldenPath := filepath.Join("testdata", tt.name)
			if err := runMcpCheck(w, []string{filepath.Join("testdata", tt.name+".yaml")}, out, "asm-managed-rapid"); err != nil && err != errorUnsupportedConfig {
				t.Fatal(err)
			}
			if util.Refresh() {
				if err := os.RemoveAll(goldenPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(goldenPath, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			util.CompareContent(
				[]byte(strings.ReplaceAll(w.String(), out, "output-path")),
				filepath.Join("testdata", tt.name, "output"),
				t)
			err := filepath.Walk(out, func(path string, info fs.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				got, err := ioutil.ReadFile(path)
				if err != nil {
					return err
				}
				goldenFilePath := strings.Replace(path, out, "testdata/"+tt.name, 1)
				if util.Refresh() {
					if err := os.MkdirAll(filepath.Dir(goldenFilePath), 0o755); err != nil {
						return err
					}
				}
				util.CompareContent(got, goldenFilePath, t)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
