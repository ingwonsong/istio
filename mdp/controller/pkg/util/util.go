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

package util

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mitchellh/hashstructure/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	lbls "k8s.io/apimachinery/pkg/labels"

	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/pkg/cache"
	"istio.io/pkg/log"
)

// dont' allow more than 1000 entries in the cache
const maxCacheEntries = 1000 // TODO: tune max entries
// expire unused cache entries after an hour
const cacheDefaultExpiration = time.Hour

// check for expirations every minute
const cacheEvictionInterval = time.Minute

// LabelSelectorCache caches the results of selector.Matches against label signatures, providing a performant way
// to check lots of labels against lots of selectors.  The cache uses LRU to limit the total used memory to 1000 entries.
// used to filter proxy pods
type LabelSelectorCache struct {
	cache         cache.ExpiringCache
	selectorInits int32
	matchCalls    int32
}

type cacheEntry struct {
	mu       sync.RWMutex
	subcache map[uint64]bool
	selector lbls.Selector
}

func newCacheEntry(selector lbls.Selector) *cacheEntry {
	return &cacheEntry{subcache: make(map[uint64]bool), selector: selector}
}

func NewLSCache() *LabelSelectorCache {
	return &LabelSelectorCache{
		cache: cache.NewLRU(cacheDefaultExpiration, cacheEvictionInterval, maxCacheEntries),
	}
}

// Matches returns true if the provided labels match the provided selector.  When possible, Matches will use the cache
// to return results, but will fall back to v1.LabelSelectorAsSelector(sel).Matches(labels) in the event of cache miss
// or hashing errors.
func (l *LabelSelectorCache) Matches(sel *metav1.LabelSelector, labels map[string]string) (bool, error) {
	if sel == nil {
		return true, nil
	}
	var x lbls.Selector
	opts := &hashstructure.HashOptions{SlicesAsSets: true}
	selhash, err := hashstructure.Hash(sel, hashstructure.FormatV2, opts)
	if err != nil {
		log.Errorf("Failed to hash selector: %s", err)
		atomic.AddInt32(&l.selectorInits, 1)
		x, err = metav1.LabelSelectorAsSelector(sel)
		if err != nil {
			return false, err
		}
		atomic.AddInt32(&l.matchCalls, 1)
		return x.Matches(lbls.Set(labels)), nil
	}
	// hash the subset of labels interesting to the selector
	relevantLabels := getRelevantLabels(sel, labels)
	labelhash, err := hashstructure.Hash(relevantLabels, hashstructure.FormatV2, opts)
	if err != nil {
		log.Errorf("Failed to hash labels: %s", err)
		entry, ok := l.cache.Get(selhash)
		if !ok {
			atomic.AddInt32(&l.selectorInits, 1)
			x, err = metav1.LabelSelectorAsSelector(sel)
			if err != nil {
				return false, err
			}
			l.cache.Set(selhash, newCacheEntry(x))
		} else {
			x = entry.(*cacheEntry).selector
		}
		atomic.AddInt32(&l.matchCalls, 1)
		return x.Matches(lbls.Set(labels)), nil
	}
	var entry *cacheEntry
	if selections, ok := l.cache.Get(selhash); ok {
		entry = selections.(*cacheEntry)
		entry.mu.RLock()
		selectorResult, cacheMatch := entry.subcache[labelhash]
		entry.mu.RUnlock()
		if cacheMatch {
			return selectorResult, nil
		}
		x = entry.selector
	} else {
		// this selector has never been seen before, we need to build it
		atomic.AddInt32(&l.selectorInits, 1)
		x, err = metav1.LabelSelectorAsSelector(sel)
		if err != nil {
			return false, err
		}
		entry = newCacheEntry(x)
		l.cache.Set(selhash, entry)
	}
	atomic.AddInt32(&l.matchCalls, 1)
	result := x.Matches(lbls.Set(labels))
	entry.mu.Lock()
	entry.subcache[labelhash] = result
	entry.mu.Unlock()
	return result, nil
}

func getRelevantLabels(sel *metav1.LabelSelector, labels map[string]string) map[string]string {
	result := map[string]string{}
	for k := range sel.MatchLabels {
		if v, ok := labels[k]; ok {
			result[k] = v
		}
	}
	for _, ex := range sel.MatchExpressions {
		if v, ok := labels[ex.Key]; ok {
			result[ex.Key] = v
		}
	}
	return result
}

// ProxyVersion return true if the pod contains an istio proxy and the version of the proxy, or false and an empty
// string otherwise.
func ProxyVersion(pod *v1.Pod) (string, bool) {
	for _, container := range pod.Spec.Containers {
		vv := strings.Split(container.Image, ":")
		if !strings.Contains(vv[0], name.IstioProxyImangeName) {
			continue
		}
		if len(vv) == 1 {
			continue
		}
		return vv[1], true
	}
	return "", false
}
