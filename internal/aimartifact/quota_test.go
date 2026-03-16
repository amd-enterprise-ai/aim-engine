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

package aimartifact

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const gi = int64(1024 * 1024 * 1024)

func makeArtifact(name string, uid types.UID, opts ...func(*aimv1alpha1.AIMArtifact)) aimv1alpha1.AIMArtifact {
	a := aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       uid,
		},
	}
	for _, o := range opts {
		o(&a)
	}
	return a
}

func withAllocatedSize(bytes int64) func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Status.AllocatedSize = *resource.NewQuantity(bytes, resource.BinarySI)
	}
}

func withDiscoveredSize(bytes int64) func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Status.DiscoveredSizeBytes = &bytes
	}
}

func withSpecSize(bytes int64) func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Spec.Size = *resource.NewQuantity(bytes, resource.BinarySI)
	}
}

func withRetentionPriority(p int32) func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Spec.RetentionPriority = &p
	}
}

func withReady() func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Status.Status = constants.AIMStatusReady
		a.Status.Mode = aimv1alpha1.ArtifactModeShared
	}
}

func withDeleting() func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		now := metav1.Now()
		a.DeletionTimestamp = &now
	}
}

func withDedicated() func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.Status.Mode = aimv1alpha1.ArtifactModeDedicated
	}
}

func withCreationTime(t time.Time) func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		a.CreationTimestamp = metav1.NewTime(t)
	}
}

func withEvictionProtected() func(*aimv1alpha1.AIMArtifact) {
	return func(a *aimv1alpha1.AIMArtifact) {
		if a.Annotations == nil {
			a.Annotations = make(map[string]string)
		}
		a.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] = aimv1alpha1.ArtifactEvictionProtectedValue
	}
}

// --- effectiveAllocatedBytes ---

func TestEffectiveAllocatedBytes(t *testing.T) {
	headroom := int32(10)

	tests := []struct {
		name     string
		artifact aimv1alpha1.AIMArtifact
		want     int64
	}{
		{
			name:     "uses allocatedSize when set",
			artifact: makeArtifact("a", "1", withAllocatedSize(10*gi)),
			want:     10 * gi,
		},
		{
			name:     "uses discoveredSizeBytes with headroom when no allocatedSize",
			artifact: makeArtifact("a", "1", withDiscoveredSize(9*gi)),
			want:     10 * gi, // 9 GiB + 10% headroom rounds up to 10 GiB
		},
		{
			name:     "uses spec.size with headroom when no status sizes",
			artifact: makeArtifact("a", "1", withSpecSize(9*gi)),
			want:     10 * gi,
		},
		{
			name:     "returns 0 when no size info",
			artifact: makeArtifact("a", "1"),
			want:     0,
		},
		{
			name:     "allocatedSize takes precedence over discoveredSizeBytes",
			artifact: makeArtifact("a", "1", withAllocatedSize(20*gi), withDiscoveredSize(15*gi)),
			want:     20 * gi,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveAllocatedBytes(&tt.artifact, headroom)
			if got != tt.want {
				t.Errorf("effectiveAllocatedBytes() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- computeUsage ---

func TestComputeUsage(t *testing.T) {
	headroom := int32(0) // no headroom for simpler math

	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(10*gi)),
		makeArtifact("b", "uid-b", withAllocatedSize(20*gi)),
		makeArtifact("c", "uid-c", withAllocatedSize(5*gi)),
	}

	total := computeUsage(artifacts, "uid-none", headroom)
	if total != 35*gi {
		t.Errorf("total = %d, want %d", total, 35*gi)
	}

	// Excluding uid-b
	total = computeUsage(artifacts, "uid-b", headroom)
	if total != 15*gi {
		t.Errorf("excluding uid-b = %d, want %d", total, 15*gi)
	}
}

func TestComputeUsageExcludesDeleting(t *testing.T) {
	headroom := int32(0)
	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(10*gi)),
		makeArtifact("b", "uid-b", withAllocatedSize(20*gi), withDeleting()),
	}

	total := computeUsage(artifacts, "uid-none", headroom)
	if total != 10*gi {
		t.Errorf("total = %d, want %d (should exclude deleting)", total, 10*gi)
	}
}

// --- parseNamespaceQuota ---

func TestParseNamespaceQuota(t *testing.T) {
	tests := []struct {
		name        string
		ns          *corev1.Namespace
		expectNil   bool
		expectErr   bool
		expectValue int64
	}{
		{
			name:      "nil namespace",
			ns:        nil,
			expectNil: true,
		},
		{
			name: "no annotation",
			ns: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
			},
			expectNil: true,
		},
		{
			name: "valid annotation",
			ns: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test",
					Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "500Gi"},
				},
			},
			expectNil:   false,
			expectValue: 500 * gi,
		},
		{
			name: "invalid annotation value returns error",
			ns: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test",
					Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "not-a-quantity"},
				},
			},
			expectNil: true,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNamespaceQuota(tt.ns)
			if tt.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectNil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !tt.expectNil {
				if got == nil {
					t.Fatal("expected non-nil")
				}
				if got.Value() != tt.expectValue {
					t.Errorf("value = %d, want %d", got.Value(), tt.expectValue)
				}
			}
		})
	}
}

// --- findEvictable ---

func TestFindEvictable(t *testing.T) {
	now := time.Now()

	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("self", "uid-self", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi)),
		makeArtifact("no-priority", "uid-np", withReady(), withAllocatedSize(10*gi)),
		makeArtifact("not-ready", "uid-nr", withRetentionPriority(0), withAllocatedSize(10*gi)),
		makeArtifact("dedicated", "uid-ded", withReady(), withDedicated(), withRetentionPriority(0), withAllocatedSize(10*gi)),
		makeArtifact("deleting", "uid-del", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi), withDeleting()),
		makeArtifact("prio-5-old", "uid-p5o", withReady(), withRetentionPriority(5), withAllocatedSize(10*gi), withCreationTime(now.Add(-2*time.Hour))),
		makeArtifact("prio-5-new", "uid-p5n", withReady(), withRetentionPriority(5), withAllocatedSize(10*gi), withCreationTime(now.Add(-1*time.Hour))),
		makeArtifact("prio-1", "uid-p1", withReady(), withRetentionPriority(1), withAllocatedSize(10*gi)),
	}

	evictable := findEvictable(artifacts, "uid-self", nil, nil)

	// Should include: prio-1, prio-5-old, prio-5-new (not: self, no-priority, not-ready, dedicated, deleting)
	if len(evictable) != 3 {
		t.Fatalf("expected 3 evictable, got %d", len(evictable))
	}

	// Order: prio-1 (1), prio-5-old (5, older), prio-5-new (5, newer)
	if evictable[0].Name != "prio-1" {
		t.Errorf("first should be prio-1, got %s", evictable[0].Name)
	}
	if evictable[1].Name != "prio-5-old" {
		t.Errorf("second should be prio-5-old, got %s", evictable[1].Name)
	}
	if evictable[2].Name != "prio-5-new" {
		t.Errorf("third should be prio-5-new, got %s", evictable[2].Name)
	}
}

func TestFindEvictable_DefaultRetentionPriority(t *testing.T) {
	defaultPrio := int32(10)
	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("explicit-prio", "uid-ep", withReady(), withRetentionPriority(5), withAllocatedSize(10*gi)),
		makeArtifact("no-prio", "uid-np", withReady(), withAllocatedSize(10*gi)),
	}

	// Without default: only explicit-prio is evictable
	evictable := findEvictable(artifacts, "uid-self", nil, nil)
	if len(evictable) != 1 || evictable[0].Name != "explicit-prio" {
		t.Errorf("without default: expected only explicit-prio, got %v", evictable)
	}

	// With default: both are evictable, explicit first (5 < 10)
	evictable = findEvictable(artifacts, "uid-self", &defaultPrio, nil)
	if len(evictable) != 2 {
		t.Fatalf("with default: expected 2 evictable, got %d", len(evictable))
	}
	if evictable[0].Name != "explicit-prio" {
		t.Errorf("explicit (prio 5) should sort before default (prio 10), got %s first", evictable[0].Name)
	}
}

func TestFindEvictable_InUseProtection(t *testing.T) {
	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("in-use", "uid-iu", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi)),
		makeArtifact("free", "uid-free", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi)),
	}

	inUse := map[types.UID]bool{"uid-iu": true}
	evictable := findEvictable(artifacts, "uid-self", nil, inUse)

	if len(evictable) != 1 || evictable[0].Name != "free" {
		t.Errorf("should only include free (not in-use), got %v", evictable)
	}
}

func TestFindEvictable_EvictionProtectedAnnotation(t *testing.T) {
	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("protected", "uid-p", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi), withEvictionProtected()),
		makeArtifact("normal", "uid-n", withReady(), withRetentionPriority(0), withAllocatedSize(10*gi)),
	}

	evictable := findEvictable(artifacts, "uid-self", nil, nil)
	if len(evictable) != 1 || evictable[0].Name != "normal" {
		t.Errorf("should exclude eviction-protected artifact, got %v", evictable)
	}
}

func TestFindEvictable_EvictionProtectedWithDefaultPriority(t *testing.T) {
	defaultPrio := int32(10)
	artifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("protected", "uid-p", withReady(), withAllocatedSize(10*gi), withEvictionProtected()),
		makeArtifact("normal", "uid-n", withReady(), withAllocatedSize(10*gi)),
	}

	evictable := findEvictable(artifacts, "uid-self", &defaultPrio, nil)
	if len(evictable) != 1 || evictable[0].Name != "normal" {
		t.Errorf("protected should be excluded even with default priority, got %v", evictable)
	}
}

func TestBuildInUseArtifactUIDs(t *testing.T) {
	caches := []aimv1alpha1.AIMTemplateCache{
		{
			Status: aimv1alpha1.AIMTemplateCacheStatus{
				Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
					"art-a": {UID: "uid-a", Name: "art-a"},
					"art-b": {UID: "uid-b", Name: "art-b"},
				},
			},
		},
		{
			Status: aimv1alpha1.AIMTemplateCacheStatus{
				Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
					"art-a": {UID: "uid-a", Name: "art-a"},
					"art-c": {UID: "uid-c", Name: "art-c"},
				},
			},
		},
	}

	inUse := BuildInUseArtifactUIDs(caches)
	if len(inUse) != 3 {
		t.Fatalf("expected 3 in-use UIDs, got %d", len(inUse))
	}
	for _, uid := range []types.UID{"uid-a", "uid-b", "uid-c"} {
		if !inUse[uid] {
			t.Errorf("expected %s to be in use", uid)
		}
	}
}

// --- computeEvictionPlan ---

func TestComputeEvictionPlan(t *testing.T) {
	headroom := int32(0)

	evictable := []aimv1alpha1.AIMArtifact{
		makeArtifact("small", "uid-s", withAllocatedSize(5*gi)),
		makeArtifact("medium", "uid-m", withAllocatedSize(10*gi)),
		makeArtifact("large", "uid-l", withAllocatedSize(20*gi)),
	}

	tests := []struct {
		name      string
		deficit   int64
		expectLen int
		expectNil bool
	}{
		{"zero deficit", 0, 0, true},
		{"small deficit freed by first", 3 * gi, 1, false},
		{"deficit needs two", 12 * gi, 2, false},
		{"deficit needs all", 30 * gi, 3, false},
		{"impossible deficit", 100 * gi, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := computeEvictionPlan(evictable, tt.deficit, headroom)
			if tt.expectNil && plan != nil {
				t.Errorf("expected nil plan, got %d items", len(plan))
			}
			if !tt.expectNil {
				if plan == nil {
					t.Fatal("expected non-nil plan")
				}
				if len(plan) != tt.expectLen {
					t.Errorf("plan length = %d, want %d", len(plan), tt.expectLen)
				}
			}
		})
	}
}

// --- EvaluateQuota ---

func TestEvaluateQuota_NoQuotas(t *testing.T) {
	dec := EvaluateQuota("uid-self", 10*gi, nil, nil, nil, nil, 0, nil, nil)
	if dec.NamespaceExceeded || dec.ClusterExceeded || dec.Blocked {
		t.Error("should not be exceeded when no quotas configured")
	}
}

func TestEvaluateQuota_WithinLimits(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(50*gi)),
	}

	dec := EvaluateQuota("uid-self", 10*gi, existing, nil, ns, nil, 0, nil, nil)
	if dec.NamespaceExceeded || dec.ClusterExceeded || dec.Blocked {
		t.Error("should be within limits (50 + 10 <= 100)")
	}
}

func TestEvaluateQuota_NamespaceExceeded_Eviction(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(80*gi), withReady(), withRetentionPriority(0)),
		makeArtifact("b", "uid-b", withAllocatedSize(15*gi)),
	}

	dec := EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, nil)
	if !dec.NamespaceExceeded {
		t.Error("should be namespace exceeded (80 + 15 + 20 = 115 > 100)")
	}
	if dec.Blocked {
		t.Error("should not be blocked (artifact a is evictable)")
	}
	if len(dec.ToEvict) != 1 || dec.ToEvict[0].Name != "a" {
		t.Errorf("should evict artifact a, got %v", dec.ToEvict)
	}
}

func TestEvaluateQuota_NamespaceExceeded_Blocked(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(90*gi)),
		makeArtifact("b", "uid-b", withAllocatedSize(5*gi)),
	}

	dec := EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, nil)
	if !dec.NamespaceExceeded {
		t.Error("should be namespace exceeded")
	}
	if !dec.Blocked {
		t.Error("should be blocked (no evictable artifacts)")
	}
	if dec.BlockReason == "" {
		t.Error("should have block reason")
	}
}

func TestEvaluateQuota_ClusterExceeded_Eviction(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	clusterQuota := &aimv1alpha1.AIMArtifactStorageQuota{
		ClusterLimit: ptr.To(resource.MustParse("200Gi")),
	}
	nsArtifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("local", "uid-local", withAllocatedSize(50*gi)),
	}
	clusterArtifacts := []aimv1alpha1.AIMArtifact{
		makeArtifact("local", "uid-local", withAllocatedSize(50*gi)),
		makeArtifact("remote", "uid-remote", withAllocatedSize(140*gi), withReady(), withRetentionPriority(0)),
	}

	dec := EvaluateQuota("uid-self", 20*gi, nsArtifacts, clusterArtifacts, ns, clusterQuota, 0, nil, nil)
	if !dec.ClusterExceeded {
		t.Error("should be cluster exceeded (50 + 140 + 20 = 210 > 200)")
	}
	if dec.Blocked {
		t.Error("should not be blocked (remote is evictable)")
	}
	if len(dec.ToEvict) != 1 || dec.ToEvict[0].Name != "remote" {
		t.Errorf("should evict remote, got %v", dec.ToEvict)
	}
}

func TestEvaluateQuota_DefaultNamespaceLimit(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	clusterQuota := &aimv1alpha1.AIMArtifactStorageQuota{
		DefaultNamespaceLimit: ptr.To(resource.MustParse("50Gi")),
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(45*gi)),
	}

	dec := EvaluateQuota("uid-self", 10*gi, existing, nil, ns, clusterQuota, 0, nil, nil)
	if !dec.NamespaceExceeded {
		t.Error("should be namespace exceeded (45 + 10 > 50 default)")
	}
}

func TestEvaluateQuota_AnnotationOverridesDefault(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	clusterQuota := &aimv1alpha1.AIMArtifactStorageQuota{
		DefaultNamespaceLimit: ptr.To(resource.MustParse("50Gi")),
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(45*gi)),
	}

	dec := EvaluateQuota("uid-self", 10*gi, existing, nil, ns, clusterQuota, 0, nil, nil)
	if dec.NamespaceExceeded {
		t.Error("should NOT be exceeded (45 + 10 <= 100 annotation overrides 50 default)")
	}
}

func TestEvaluateQuota_InUseProtection_Blocks(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("in-use", "uid-iu", withAllocatedSize(80*gi), withReady(), withRetentionPriority(0)),
		makeArtifact("b", "uid-b", withAllocatedSize(15*gi)),
	}

	// Without in-use protection, eviction would succeed
	dec := EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, nil)
	if dec.Blocked {
		t.Error("without in-use protection, should evict")
	}

	// With in-use protection, the only evictable is in-use → blocked
	inUse := map[types.UID]bool{"uid-iu": true}
	dec = EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, inUse)
	if !dec.Blocked {
		t.Error("with in-use protection, should be blocked")
	}
}

func TestEvaluateQuota_InvalidAnnotation_ConfigWarning(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "not-a-quantity"},
		},
	}

	dec := EvaluateQuota("uid-self", 10*gi, nil, nil, ns, nil, 0, nil, nil)
	if dec.ConfigWarning == "" {
		t.Error("expected ConfigWarning to be set for invalid annotation")
	}
	if dec.NamespaceExceeded || dec.ClusterExceeded {
		t.Error("should not be exceeded when annotation is invalid and no other quota")
	}
}

func TestEvaluateQuota_InvalidAnnotation_FallsBackToDefault(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "garbage"},
		},
	}
	clusterQuota := &aimv1alpha1.AIMArtifactStorageQuota{
		DefaultNamespaceLimit: ptr.To(resource.MustParse("50Gi")),
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(45*gi)),
	}

	dec := EvaluateQuota("uid-self", 10*gi, existing, nil, ns, clusterQuota, 0, nil, nil)
	if dec.ConfigWarning == "" {
		t.Error("expected ConfigWarning")
	}
	if !dec.NamespaceExceeded {
		t.Error("should fall back to default namespace limit (45 + 10 > 50)")
	}
}

func TestEvaluateQuota_StoresDefaultRetentionPriority(t *testing.T) {
	defaultPrio := int32(5)
	dec := EvaluateQuota("uid-self", 10*gi, nil, nil, nil, nil, 0, &defaultPrio, nil)
	if dec.DefaultRetentionPriority == nil || *dec.DefaultRetentionPriority != 5 {
		t.Error("expected DefaultRetentionPriority=5 to be stored in decision")
	}
}

func TestEvaluateQuota_EvictionProtected_Blocks(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("protected", "uid-p", withAllocatedSize(80*gi), withReady(), withRetentionPriority(0), withEvictionProtected()),
		makeArtifact("b", "uid-b", withAllocatedSize(15*gi)),
	}

	dec := EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, nil)
	if !dec.Blocked {
		t.Error("should be blocked (only evictable artifact is eviction-protected)")
	}
}

func TestEvaluateQuota_DefaultRetentionPriority_EnablesEviction(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{aimv1alpha1.ArtifactStorageQuotaAnnotation: "100Gi"},
		},
	}
	existing := []aimv1alpha1.AIMArtifact{
		makeArtifact("a", "uid-a", withAllocatedSize(80*gi), withReady()),
		makeArtifact("b", "uid-b", withAllocatedSize(15*gi)),
	}

	// Without default: "a" has no retentionPriority → blocked
	dec := EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, nil, nil)
	if !dec.Blocked {
		t.Error("without default priority, should be blocked")
	}

	// With default priority: "a" becomes evictable at priority 5
	defaultPrio := int32(5)
	dec = EvaluateQuota("uid-self", 20*gi, existing, nil, ns, nil, 0, &defaultPrio, nil)
	if dec.Blocked {
		t.Error("with default priority, should evict a")
	}
	if len(dec.ToEvict) != 1 || dec.ToEvict[0].Name != "a" {
		t.Errorf("should evict a, got %v", dec.ToEvict)
	}
}
