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
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCatalogCacheRevisionAndConcurrentReuse(t *testing.T) {
	cache := NewCatalogCache(2)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{UID: "cache", ResourceVersion: "1"}, Data: map[string]string{
		FlattenProfilePath("model/profile.yaml"): "model_id: model\nmetadata:\n  engine: vllm\n  metric: latency\n  precision: bf16\n  type: optimized\n  accelerator_vendor: amd\n  accelerator_partitioning_mode: CPX-NPS4\n",
	}}
	first, err := cache.Load(cm)
	if err != nil {
		t.Fatal(err)
	}
	if first.Profiles[0].Spec.AcceleratorPartitioningMode != testFamilyPartitionMode || first.Profiles[0].Spec.AcceleratorVendor != "amd" {
		t.Fatal("hardware metadata lost")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.Load(cm)
			if err != nil || got != first {
				t.Errorf("revision was reparsed: %p != %p, %v", got, first, err)
			}
		}()
	}
	wg.Wait()
	next := cm.DeepCopy()
	next.ResourceVersion = "2"
	next.Data[FlattenProfilePath("model/profile.yaml")] = "metadata: ["
	if _, err := cache.Load(next); err == nil {
		t.Fatal("malformed revision accepted")
	}
	next.ResourceVersion = "3"
	next.Data = cm.Data
	if _, err := cache.Load(next); err != nil {
		t.Fatal(err)
	}
	reloaded, err := cache.Load(cm)
	if err != nil || reloaded == first {
		t.Fatal("bounded cache did not evict least recent revision")
	}
}

func BenchmarkCachedDiscoveryCatalog(b *testing.B) {
	cache := NewCatalogCache(2)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{UID: "cache", ResourceVersion: "1"}, Data: map[string]string{
		FlattenProfilePath("model/profile.yaml"): "model_id: model\nmetadata:\n  engine: vllm\n  metric: latency\n  precision: bf16\n  type: optimized\n",
	}}
	if _, err := cache.Load(cm); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := cache.Load(cm); err != nil {
			b.Fatal(err)
		}
	}
}
