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

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/v1/google"
	yaml "gopkg.in/yaml.v3"
)

var (
	liveImgPrefix       = "public-image-"
	deprecatedImgPrefix = "deprecated-public-image-"
)

// Config struct
type Config struct {
	Registry string     `yaml:"registry"`
	Repos    []string   `yaml:repos`
	Versions []Versions `yaml:"versions"`
}

type Versions struct {
	Name   string `yaml:"name"`
	Status string `yaml:"status"`
}

func main() {
	configPath := flag.String("config", filepath.Join(os.Getenv("PWD"), "config/asm_releases.yaml"), "Path to the YAML configuration file")

	flag.Parse()

	if *configPath == "" {
		fmt.Println("Error: Please provide a YAML configuration file using the -config flag.")
		return
	}

	yamlData, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Printf("Error reading YAML file: %v\n", err)
		return
	}

	var config Config
	err = yaml.Unmarshal(yamlData, &config)
	if err != nil {
		fmt.Printf("Error parsing YAML: %v\n", err)
		return
	}

	gcr_auth, err := google.NewEnvAuthenticator()
	// gcr_auth, err := google.NewGcloudAuthenticator()
	if err != nil {
		panic(err)
	}

	// Process images
	for _, r := range config.Repos {
		repo := fmt.Sprintf("%s/%s", config.Registry, r)
		tags, err := crane.ListTags(repo, crane.WithAuth(gcr_auth))
		if err != nil {
			fmt.Printf("Error listing tags for %s %s", repo, err)
		}

		for _, tag := range tags {
			image := fmt.Sprintf("%s:%s", repo, tag)

			var versionConfig *Versions
			// Find matching version configuration
			for i, vc := range config.Versions {
				if strings.Contains(image, vc.Name) {
					versionConfig = &config.Versions[i]
					break
				}
			}

			if versionConfig == nil {
				continue // No matching version config
			}

			// Handle rollbacks, i.e. "deprecated" tag needs to be removed from image.
			if versionConfig.Status == "live" && strings.HasPrefix(tag, deprecatedImgPrefix) {
				newTag := fmt.Sprint(liveImgPrefix, strings.TrimPrefix(tag, deprecatedImgPrefix)) // Remove prefix

				// Construct the target image name (same repo, new tag)
				targetImage := strings.Replace(image, ":"+tag, ":"+newTag, 1)

				err := crane.Copy(image, targetImage)
				if err != nil {
					fmt.Printf("Error re-tagging image %s: %v\n", image, err)
				} else {
					fmt.Printf("Removed %s prefix from %s, tagged as %s\n", deprecatedImgPrefix, image, newTag)
				}
				err = crane.Delete(image)
				if err != nil {
					fmt.Printf("Error removing tags for %s %s", image, err)
				}

			} else if strings.HasPrefix(tag, deprecatedImgPrefix) || strings.HasPrefix(tag, liveImgPrefix) {
				continue
			} else if versionConfig.Status != "" { // Existing tag logic
				prefix := ""
				if versionConfig.Status == "live" {
					prefix = liveImgPrefix
				} else if versionConfig.Status == "deprecated" {
					prefix = deprecatedImgPrefix
				}

				if prefix != "" {
					newTag := prefix + tag // Construct new tag

					// Construct target image name (same repo, new tag)
					targetImage := strings.Replace(image, ":"+tag, ":"+newTag, 1)

					err := crane.Copy(image, targetImage)
					if err != nil {
						fmt.Printf("Error tagging image %s: %v\n", image, err)
					} else {
						fmt.Printf("Successfully tagged %s with %s\n", image, newTag)
					}
				}
			}
		}
	}
}
