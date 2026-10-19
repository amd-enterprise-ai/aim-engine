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

package aimprofileset

import (
	"container/list"
	"encoding/json"
	"sync"

	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

type publicationKey struct {
	catalog *aimprofile.DiscoveryCatalog
	group   string
	inputs  string
}

type publication struct {
	key        publicationKey
	candidates []aimprofile.ProfileCopyCandidate
	liveNames  map[string]string
	err        error
}

// Publications share immutable catalog definitions across consuming sets.
// Model defaults and legacy source names are resolved once per relevant revision.
// The bound also releases deleted models and old catalog revisions.
var publications = struct {
	sync.Mutex
	entries map[publicationKey]*list.Element
	lru     *list.List
}{entries: make(map[publicationKey]*list.Element), lru: list.New()}

func (m sourceModel) cachedPublication(catalog *aimprofile.DiscoveryCatalog) ([]aimprofile.ProfileCopyCandidate, map[string]string, error) {
	inputs, _ := json.Marshal(struct {
		Spec     any
		Metadata any
	}{m.spec, m.spec.GetEffectiveImageMetadata(m.status)})
	key := publicationKey{catalog, m.group(), string(inputs)}
	publications.Lock()
	defer publications.Unlock()
	if elem := publications.entries[key]; elem != nil {
		publications.lru.MoveToFront(elem)
		value := elem.Value.(publication)
		return value.candidates, value.liveNames, value.err
	}
	candidates, liveNames, err := m.buildPublication(catalog)
	publications.entries[key] = publications.lru.PushFront(publication{key, candidates, liveNames, err})
	if publications.lru.Len() > 128 {
		last := publications.lru.Back()
		delete(publications.entries, last.Value.(publication).key)
		publications.lru.Remove(last)
	}
	return candidates, liveNames, err
}
