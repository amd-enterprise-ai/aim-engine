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
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
)

// QuotaDecision captures the result of a quota evaluation.
type QuotaDecision struct {
	// NamespaceExceeded is true if namespace quota would be exceeded.
	NamespaceExceeded bool
	// ClusterExceeded is true if cluster quota would be exceeded.
	ClusterExceeded bool

	NamespaceQuota *resource.Quantity
	ClusterQuota   *resource.Quantity
	NamespaceUsage int64
	ClusterUsage   int64
	ProjectedSize  int64

	// ToEvict lists artifacts that should be deleted to make room.
	// Empty if eviction is not possible or not needed.
	ToEvict []aimv1alpha1.AIMArtifact

	// Blocked is true when quota is exceeded and eviction cannot free enough space.
	Blocked bool
	// BlockReason describes why the artifact is blocked (for condition message).
	BlockReason string

	// DefaultRetentionPriority records the default priority used for eviction decisions.
	// Stored here so callers (e.g., log lines) can compute effective priority without
	// needing to re-derive it from the runtime config.
	DefaultRetentionPriority *int32

	// ConfigWarning is set when quota configuration has issues (e.g., invalid annotation).
	// Non-fatal: quota evaluation continues with fallback behavior.
	ConfigWarning string
}

// effectiveAllocatedBytes returns the storage bytes an artifact occupies or would occupy.
// Priority: allocatedSize (PVC exists) > discoveredSizeBytes + headroom > spec.size + headroom.
// Returns 0 if size is unknown.
func effectiveAllocatedBytes(artifact *aimv1alpha1.AIMArtifact, headroomPercent int32) int64 {
	if !artifact.Status.AllocatedSize.IsZero() {
		return artifact.Status.AllocatedSize.Value()
	}
	if artifact.Status.DiscoveredSizeBytes != nil {
		return v1alpha1utils.ApplyHeadroomAndRound(*artifact.Status.DiscoveredSizeBytes, headroomPercent)
	}
	if !artifact.Spec.Size.IsZero() {
		return v1alpha1utils.ApplyHeadroomAndRound(artifact.Spec.Size.Value(), headroomPercent)
	}
	return 0
}

// computeUsage sums the effective allocated bytes for all artifacts, excluding the one with excludeUID
// and any artifact that is being deleted.
func computeUsage(artifacts []aimv1alpha1.AIMArtifact, excludeUID types.UID, headroomPercent int32) int64 {
	var total int64
	for i := range artifacts {
		a := &artifacts[i]
		if a.UID == excludeUID {
			continue
		}
		if a.DeletionTimestamp != nil {
			continue
		}
		total += effectiveAllocatedBytes(a, headroomPercent)
	}
	return total
}

// parseNamespaceQuota reads the artifact storage quota from a Namespace annotation.
// Returns (nil, nil) if not set, (value, nil) on success, or (nil, error) if the
// annotation is present but cannot be parsed.
func parseNamespaceQuota(ns *corev1.Namespace) (*resource.Quantity, error) {
	if ns == nil {
		return nil, nil
	}
	val, ok := ns.Annotations[aimv1alpha1.ArtifactStorageQuotaAnnotation]
	if !ok || val == "" {
		return nil, nil
	}
	q, err := resource.ParseQuantity(val)
	if err != nil {
		return nil, fmt.Errorf("invalid %s annotation %q on namespace %s: %w",
			aimv1alpha1.ArtifactStorageQuotaAnnotation, val, ns.Name, err)
	}
	return &q, nil
}

// resolveNamespaceQuota determines the effective namespace quota: the namespace annotation
// takes precedence over the cluster config's DefaultNamespaceLimit.
// Returns (quota, parseError) where parseError is non-nil if the annotation exists but is malformed.
func resolveNamespaceQuota(ns *corev1.Namespace, clusterQuota *aimv1alpha1.AIMArtifactStorageQuota) (*resource.Quantity, error) {
	q, err := parseNamespaceQuota(ns)
	if err != nil {
		// Annotation present but malformed — fall through to cluster default but propagate warning.
		if clusterQuota != nil && clusterQuota.DefaultNamespaceLimit != nil {
			return clusterQuota.DefaultNamespaceLimit, err
		}
		return nil, err
	}
	if q != nil {
		return q, nil
	}
	if clusterQuota != nil && clusterQuota.DefaultNamespaceLimit != nil {
		return clusterQuota.DefaultNamespaceLimit, nil
	}
	return nil, nil
}

// effectiveRetentionPriority returns the retention priority for an artifact,
// falling back to the runtime config default if the spec doesn't set one.
// Returns nil if neither is set (artifact is not evictable).
func effectiveRetentionPriority(a *aimv1alpha1.AIMArtifact, defaultPriority *int32) *int32 {
	if a.Spec.RetentionPriority != nil {
		return a.Spec.RetentionPriority
	}
	return defaultPriority
}

// findEvictable returns artifacts eligible for eviction, sorted by eviction priority
// (lowest retentionPriority first, then oldest creationTimestamp).
// inUseUIDs contains UIDs of artifacts referenced by AIMTemplateCaches; these are skipped.
func findEvictable(artifacts []aimv1alpha1.AIMArtifact, selfUID types.UID, defaultPriority *int32, inUseUIDs map[types.UID]bool) []aimv1alpha1.AIMArtifact {
	var candidates []aimv1alpha1.AIMArtifact
	for i := range artifacts {
		a := &artifacts[i]
		if a.UID == selfUID {
			continue
		}
		if a.DeletionTimestamp != nil {
			continue
		}
		if a.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] == aimv1alpha1.ArtifactEvictionProtectedValue {
			continue
		}
		if effectiveRetentionPriority(a, defaultPriority) == nil {
			continue
		}
		if a.Status.Status != constants.AIMStatusReady {
			continue
		}
		if a.Status.Mode != aimv1alpha1.ArtifactModeShared {
			continue
		}
		if inUseUIDs[a.UID] {
			continue
		}
		candidates = append(candidates, *a)
	}

	sort.Slice(candidates, func(i, j int) bool {
		pi := *effectiveRetentionPriority(&candidates[i], defaultPriority)
		pj := *effectiveRetentionPriority(&candidates[j], defaultPriority)
		if pi != pj {
			return pi < pj
		}
		return candidates[i].CreationTimestamp.Before(&candidates[j].CreationTimestamp)
	})

	return candidates
}

// computeEvictionPlan selects the minimum set of evictable artifacts to free at least
// deficit bytes. Returns nil if the candidates cannot free enough.
func computeEvictionPlan(evictable []aimv1alpha1.AIMArtifact, deficit int64, headroomPercent int32) []aimv1alpha1.AIMArtifact {
	if deficit <= 0 {
		return nil
	}
	var toEvict []aimv1alpha1.AIMArtifact
	var freed int64
	for i := range evictable {
		if freed >= deficit {
			break
		}
		toEvict = append(toEvict, evictable[i])
		freed += effectiveAllocatedBytes(&evictable[i], headroomPercent)
	}
	if freed >= deficit {
		return toEvict
	}
	return nil
}

// BuildInUseArtifactUIDs returns the set of artifact UIDs that are referenced by
// any AIMTemplateCache. Artifacts in this set should not be evicted because their
// PVCs are likely mounted by running inference pods and deletion won't free storage.
func BuildInUseArtifactUIDs(caches []aimv1alpha1.AIMTemplateCache) map[types.UID]bool {
	inUse := make(map[types.UID]bool)
	for i := range caches {
		for _, resolved := range caches[i].Status.Artifacts {
			if resolved.UID != "" {
				inUse[types.UID(resolved.UID)] = true
			}
		}
	}
	return inUse
}

// EvaluateQuota performs the full quota check and eviction planning.
// defaultRetentionPriority is the fallback from the runtime config; nil means
// artifacts without an explicit spec.retentionPriority are never evictable.
// inUseUIDs marks artifacts that should not be evicted because they are
// referenced by template caches (their PVCs may be mounted).
//
// Concurrency: The artifact controller serializes pipeline runs through a
// Lease-based lock (WithQuotaLock) for artifacts that don't yet have a PVC.
// PVC creation is always deferred to a cycle where size is already persisted
// in status, so NeedsQuotaLock returns true and the lock is held. Quota data
// is read directly from the API server (bypassing the informer cache), so the
// lock holder always sees PVCs created by the previous holder.
func EvaluateQuota(
	selfUID types.UID,
	projectedSize int64,
	namespaceArtifacts []aimv1alpha1.AIMArtifact,
	clusterArtifacts []aimv1alpha1.AIMArtifact,
	ns *corev1.Namespace,
	clusterQuotaCfg *aimv1alpha1.AIMArtifactStorageQuota,
	headroomPercent int32,
	defaultRetentionPriority *int32,
	inUseUIDs map[types.UID]bool,
) QuotaDecision {
	dec := QuotaDecision{
		ProjectedSize:            projectedSize,
		DefaultRetentionPriority: defaultRetentionPriority,
	}

	nsQuota, parseErr := resolveNamespaceQuota(ns, clusterQuotaCfg)
	if parseErr != nil {
		dec.ConfigWarning = parseErr.Error()
	}
	dec.NamespaceQuota = nsQuota

	var clusterQuota *resource.Quantity
	if clusterQuotaCfg != nil && clusterQuotaCfg.ClusterLimit != nil {
		clusterQuota = clusterQuotaCfg.ClusterLimit
	}
	dec.ClusterQuota = clusterQuota

	// No quotas configured — always within limits.
	if nsQuota == nil && clusterQuota == nil {
		return dec
	}

	nsUsage := computeUsage(namespaceArtifacts, selfUID, headroomPercent)
	dec.NamespaceUsage = nsUsage

	clusterUsage := nsUsage
	if clusterArtifacts != nil {
		clusterUsage = computeUsage(clusterArtifacts, selfUID, headroomPercent)
	}
	dec.ClusterUsage = clusterUsage

	// Check namespace quota
	if nsQuota != nil && nsUsage+projectedSize > nsQuota.Value() {
		dec.NamespaceExceeded = true
	}

	// Check cluster quota
	if clusterQuota != nil && clusterUsage+projectedSize > clusterQuota.Value() {
		dec.ClusterExceeded = true
	}

	if !dec.NamespaceExceeded && !dec.ClusterExceeded {
		return dec
	}

	// Attempt eviction planning.
	// For namespace quota, evict only from the same namespace.
	// For cluster quota, evict from any namespace.
	if dec.NamespaceExceeded {
		nsDeficit := (nsUsage + projectedSize) - nsQuota.Value()
		nsEvictable := findEvictable(namespaceArtifacts, selfUID, defaultRetentionPriority, inUseUIDs)
		if plan := computeEvictionPlan(nsEvictable, nsDeficit, headroomPercent); plan != nil {
			dec.ToEvict = plan
			return dec
		}
		dec.Blocked = true
		dec.BlockReason = formatQuotaMessage("Namespace", nsUsage, projectedSize, nsQuota.Value())
		return dec
	}

	if dec.ClusterExceeded {
		clDeficit := (clusterUsage + projectedSize) - clusterQuota.Value()
		var evictablePool []aimv1alpha1.AIMArtifact
		if clusterArtifacts != nil {
			evictablePool = findEvictable(clusterArtifacts, selfUID, defaultRetentionPriority, inUseUIDs)
		} else {
			evictablePool = findEvictable(namespaceArtifacts, selfUID, defaultRetentionPriority, inUseUIDs)
		}
		if plan := computeEvictionPlan(evictablePool, clDeficit, headroomPercent); plan != nil {
			dec.ToEvict = plan
			return dec
		}
		dec.Blocked = true
		dec.BlockReason = formatQuotaMessage("Cluster", clusterUsage, projectedSize, clusterQuota.Value())
		return dec
	}

	return dec
}

func formatQuotaMessage(scope string, currentUsage, projectedSize, quota int64) string {
	fmtBytes := func(b int64) string {
		q := resource.NewQuantity(b, resource.BinarySI)
		return q.String()
	}
	return scope + " quota exceeded: " + fmtBytes(currentUsage) + " used + " + fmtBytes(projectedSize) + " needed > " + fmtBytes(quota) + " limit"
}
