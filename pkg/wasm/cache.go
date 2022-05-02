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

package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"istio.io/istio/pkg/util/sets"
	"istio.io/pkg/log"
)

var wasmLog = log.RegisterScope("wasm", "", 0)

const (
	// DefaultWasmModulePurgeInterval is the default interval for periodic stale Wasm module clean up.
	DefaultWasmModulePurgeInterval = 10 * time.Minute

	// DefaultWasmModuleExpiry is the default duration for least recently touched Wasm module to become stale.
	DefaultWasmModuleExpiry = 24 * time.Hour

	// oci URL prefix
	ociURLPrefix = "oci://"

	// sha256 scheme prefix
	sha256SchemePrefix = "sha256:"
)

// Cache models a Wasm module cache.
type Cache interface {
	Get(url, checksum string, timeout time.Duration, pullSecret []byte) (string, error)
	Cleanup()
}

// LocalFileCache for downloaded Wasm modules. Currently it stores the Wasm module as local file.
type LocalFileCache struct {
	// Map from Wasm module checksum to cache entry.
	modules map[moduleKey]*cacheEntry
	// Map from tagged URL to checksum
	checksums map[string]string

	// http fetcher fetches Wasm module with HTTP get.
	httpFetcher *HTTPFetcher

	// directory path used to store Wasm module.
	dir string

	// mux is needed because stale Wasm module files will be purged periodically.
	mux sync.Mutex

	// Duration for stale Wasm module purging.
	purgeInterval      time.Duration
	wasmModuleExpiry   time.Duration
	insecureRegistries sets.Set

	// stopChan currently is only used by test
	stopChan chan struct{}
}

var _ Cache = &LocalFileCache{}

type moduleKey struct {
	// Identifier for the module. It should be neutral for the checksum.
	// e.g.) oci://docker.io/test@sha256:0123456789 is not allowed.
	//       oci://docker.io/test:latest (tagged form) is allowed.
	name     string
	checksum string
}

type cacheKey struct {
	moduleKey
	downloadURL string
}

// cacheEntry contains information about a Wasm module cache entry.
type cacheEntry struct {
	// File path to the downloaded wasm modules.
	modulePath string
	// Last time that this local Wasm module is referenced.
	last time.Time
	// set of URLs referencing this entry
	referencingURLs sets.Set
}

// NewLocalFileCache create a new Wasm module cache which downloads and stores Wasm module files locally.
func NewLocalFileCache(dir string, purgeInterval, moduleExpiry time.Duration, insecureRegistries []string) *LocalFileCache {
	cache := &LocalFileCache{
		httpFetcher:        NewHTTPFetcher(),
		modules:            make(map[moduleKey]*cacheEntry),
		checksums:          make(map[string]string),
		dir:                dir,
		purgeInterval:      purgeInterval,
		wasmModuleExpiry:   moduleExpiry,
		stopChan:           make(chan struct{}),
		insecureRegistries: sets.New(insecureRegistries...),
	}
	go func() {
		cache.purge()
	}()
	return cache
}

func urlAsResourceName(fullURLStr string) string {
	if strings.HasPrefix(fullURLStr, ociURLPrefix) {
		if tag, err := name.ParseReference(fullURLStr[len(ociURLPrefix):]); err == nil {
			// remove tag or sha
			return ociURLPrefix + tag.Context().Name()
		}
	}
	return fullURLStr
}

// Get returns path the local Wasm module file.
func (c *LocalFileCache) Get(downloadURL, checksum string, timeout time.Duration, pullSecret []byte) (string, error) {
	// Construct Wasm cache key with downloading URL and provided checksum of the module.
	key := cacheKey{
		downloadURL: downloadURL,
		moduleKey: moduleKey{
			name:     urlAsResourceName(downloadURL),
			checksum: checksum,
		},
	}

	// First check if the cache entry is already downloaded.
	if modulePath := c.getEntry(key); modulePath != "" {
		return modulePath, nil
	}

	// If not, fetch images.
	u, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("fail to parse Wasm module fetch url: %s", downloadURL)
	}

	// Byte array of Wasm binary.
	var b []byte
	// Hex-Encoded sha256 checksum of binary.
	var dChecksum string
	var binaryFetcher func() ([]byte, error)
	switch u.Scheme {
	case "http", "https":
		// Download the Wasm module with http fetcher.
		b, err = c.httpFetcher.Fetch(downloadURL, timeout)
		if err != nil {
			wasmRemoteFetchCount.With(resultTag.Value(downloadFailure)).Increment()
			return "", err
		}

		// Get sha256 checksum and check if it is the same as provided one.
		sha := sha256.Sum256(b)
		dChecksum = hex.EncodeToString(sha[:])
	case "oci":
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		insecure := false
		if c.insecureRegistries.Contains(u.Host) {
			insecure = true
		}
		// TODO: support imagePullSecret and pass it to ImageFetcherOption.
		imgFetcherOps := ImageFetcherOption{
			Insecure: insecure,
		}
		if pullSecret != nil {
			imgFetcherOps.PullSecret = pullSecret
		}
		wasmLog.Debugf("wasm oci fetch %s with options: %v", downloadURL, imgFetcherOps)
		fetcher := NewImageFetcher(ctx, imgFetcherOps)
		binaryFetcher, dChecksum, err = fetcher.PrepareFetch(u.Host + u.Path)
		if err != nil {
			wasmRemoteFetchCount.With(resultTag.Value(downloadFailure)).Increment()
			return "", fmt.Errorf("could not fetch Wasm OCI image: %v", err)
		}
	default:
		return "", fmt.Errorf("unsupported Wasm module downloading URL scheme: %v", u.Scheme)
	}

	if key.checksum == "" {
		key.checksum = dChecksum
		// check again if the cache is having the checksum.
		if modulePath := c.getEntry(key); modulePath != "" {
			return modulePath, nil
		}
	} else if dChecksum != key.checksum {
		wasmRemoteFetchCount.With(resultTag.Value(checksumMismatch)).Increment()
		return "", fmt.Errorf("module downloaded from %v has checksum %v, which does not match: %v", downloadURL, dChecksum, key.checksum)
	}

	if binaryFetcher != nil {
		b, err = binaryFetcher()
		if err != nil {
			wasmRemoteFetchCount.With(resultTag.Value(downloadFailure)).Increment()
			return "", fmt.Errorf("could not fetch Wasm binary: %v", err)
		}
	}

	if !isValidWasmBinary(b) {
		wasmRemoteFetchCount.With(resultTag.Value(fetchFailure)).Increment()
		return "", fmt.Errorf("fetched Wasm binary from %s is invalid", downloadURL)
	}

	wasmRemoteFetchCount.With(resultTag.Value(fetchSuccess)).Increment()

	key.checksum = dChecksum
	f := filepath.Join(c.dir, fmt.Sprintf("%s.wasm", dChecksum))

	if err := c.addEntry(key, b, f); err != nil {
		return "", err
	}
	return f, nil
}

// Cleanup closes background Wasm module purge routine.
func (c *LocalFileCache) Cleanup() {
	close(c.stopChan)
}

func (c *LocalFileCache) addEntry(key cacheKey, wasmModule []byte, f string) error {
	// If OCI URL having a tag, we need to update checksum.
	needChecksumUpdate := strings.HasPrefix(key.downloadURL, ociURLPrefix) && !strings.Contains(key.downloadURL, "@")

	c.mux.Lock()
	defer c.mux.Unlock()

	if needChecksumUpdate {
		c.checksums[key.downloadURL] = key.checksum
	}

	// Check if the module has already been added. If so, avoid writing the file again.
	if ce, ok := c.modules[key.moduleKey]; ok {
		// Update last touched time.
		ce.last = time.Now()
		if needChecksumUpdate {
			ce.referencingURLs.Insert(key.downloadURL)
		}
		return nil
	}

	// Materialize the Wasm module into a local file. Use checksum as name of the module.
	if err := os.WriteFile(f, wasmModule, 0o644); err != nil {
		return err
	}

	ce := cacheEntry{
		modulePath:      f,
		last:            time.Now(),
		referencingURLs: sets.New(),
	}
	if needChecksumUpdate {
		ce.referencingURLs.Insert(key.downloadURL)
	}
	c.modules[key.moduleKey] = &ce
	wasmCacheEntries.Record(float64(len(c.modules)))
	return nil
}

func (c *LocalFileCache) getEntry(key cacheKey) string {
	modulePath := ""
	cacheHit := false

	// Only apply this for OCI image, not http/https because OCI image has ImagePullPolicy
	// to control the pull policy, but http/https currently rely on existence of checksum.
	// At this point, we don't need to break the current behavior for http/https.
	if len(key.checksum) == 0 && strings.HasPrefix(key.downloadURL, ociURLPrefix) {
		if d, err := name.NewDigest(key.downloadURL[len(ociURLPrefix):]); err == nil {
			// If there is no checksum and the digest is suffixed in URL, use the digest.
			dstr := d.DigestStr()
			if strings.HasPrefix(dstr, sha256SchemePrefix) {
				key.checksum = dstr[len(sha256SchemePrefix):]
			}
			// For other digest scheme, give up to use cache.
		} else {
			// If no checksum, try the checksum cache.
			// If the image was pulled before, there should be a checksum of the most recently pulled image.
			key.checksum = c.checksums[key.downloadURL]
		}
	}

	c.mux.Lock()
	defer c.mux.Unlock()
	if ce, ok := c.modules[key.moduleKey]; ok {
		// Update last touched time.
		ce.last = time.Now()
		modulePath = ce.modulePath
		cacheHit = true
	}
	wasmCacheLookupCount.With(hitTag.Value(strconv.FormatBool(cacheHit))).Increment()
	return modulePath
}

// Purge periodically clean up the stale Wasm modules local file and the cache map.
func (c *LocalFileCache) purge() {
	ticker := time.NewTicker(c.purgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mux.Lock()
			for k, m := range c.modules {
				if m.expired(c.wasmModuleExpiry) {
					// The module has not be touched for expiry duration, delete it from the map as well as the local dir.
					if err := os.Remove(m.modulePath); err != nil {
						wasmLog.Errorf("failed to purge Wasm module %v: %v", m.modulePath, err)
					} else {
						for downloadURL := range m.referencingURLs {
							delete(c.checksums, downloadURL)
						}
						delete(c.modules, k)
						wasmLog.Debugf("successfully removed stale Wasm module %v", m.modulePath)
					}
				}
			}
			wasmCacheEntries.Record(float64(len(c.modules)))
			c.mux.Unlock()
		case <-c.stopChan:
			// Currently this will only happen in test.
			return
		}
	}
}

// Expired returns true if the module has not been touched for Wasm module Expiry.
func (ce *cacheEntry) expired(expiry time.Duration) bool {
	now := time.Now()
	return now.Sub(ce.last) > expiry
}

var wasmMagicNumber = []byte{0x00, 0x61, 0x73, 0x6d}

func isValidWasmBinary(in []byte) bool {
	// Wasm file header is 8 bytes (magic number + version).
	return len(in) >= 8 && bytes.Equal(in[:4], wasmMagicNumber)
}
