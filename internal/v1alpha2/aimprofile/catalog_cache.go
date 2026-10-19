// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package aimprofile

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"sync"

	corev1 "k8s.io/api/core/v1"
)

// CatalogCache retains a bounded number of immutable parsed revisions, including
// parse failures. A mutex coalesces concurrent requests for the same revision.
// Callers must not mutate returned catalogs or their nested specs/contracts.
type CatalogCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	lru      *list.List
}

type catalogCacheEntry struct {
	key     string
	catalog *DiscoveryCatalog
	err     error
}

func NewCatalogCache(capacity int) *CatalogCache {
	if capacity < 1 {
		capacity = 1
	}
	return &CatalogCache{capacity: capacity, entries: make(map[string]*list.Element), lru: list.New()}
}

var discoveryCatalogCache = NewCatalogCache(128)

func CachedDiscoveryCatalog(cm *corev1.ConfigMap) (*DiscoveryCatalog, error) {
	return discoveryCatalogCache.Load(cm)
}

func (cache *CatalogCache) Load(cm *corev1.ConfigMap) (*DiscoveryCatalog, error) {
	if cm == nil {
		return ParseDiscoveryCatalog(cm)
	}
	key := string(cm.UID) + "/" + cm.ResourceVersion
	if cm.UID == "" || cm.ResourceVersion == "" {
		// Pre-persistence catalogs and test clients may have no revision.
		raw, _ := json.Marshal(cm.Data)
		digest := sha256.Sum256(raw)
		key = string(digest[:])
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if elem := cache.entries[key]; elem != nil {
		cache.lru.MoveToFront(elem)
		entry := elem.Value.(catalogCacheEntry)
		return entry.catalog, entry.err
	}
	catalog, err := ParseDiscoveryCatalog(cm)
	cache.entries[key] = cache.lru.PushFront(catalogCacheEntry{key, catalog, err})
	if cache.lru.Len() > cache.capacity {
		last := cache.lru.Back()
		delete(cache.entries, last.Value.(catalogCacheEntry).key)
		cache.lru.Remove(last)
	}
	return catalog, err
}
