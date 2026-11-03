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

package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofilecache"
)

// newProfileCacheTestReconciler mirrors the production wiring, including the
// status.artifacts index the artifact watch resolves through. Without WithIndex a
// MatchingFields List errors instead of matching, so the index path has to be
// registered here for the tests below to mean anything.
func newProfileCacheTestReconciler(t *testing.T, objs ...client.Object) *AIMProfileCacheReconciler {
	t.Helper()
	scheme := runtimeProjectionScheme(t)
	return &AIMProfileCacheReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			WithIndex(&aimv1alpha2.AIMProfileCache{}, aimv1alpha2.ProfileCacheArtifactNameIndexKey, indexProfileCacheArtifactNames).
			Build(),
		Scheme: scheme,
	}
}

func makeSharedProfileCache(name string) *aimv1alpha2.AIMProfileCache {
	pc := makeProfileCache(name, testNamespace, name+"-profile", aimv1alpha1.AIMResolutionScopeNamespace)
	pc.UID = types.UID(name + "-uid")
	pc.Spec.Mode = aimv1alpha2.ProfileCacheModeShared
	return pc
}

func makeWatchedArtifact(name string) *aimv1alpha1.AIMArtifact {
	return &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI: "hf://meta-llama/Llama-3.1-8B-Instruct",
		},
	}
}

// resolvingArtifact records artifactName in the cache's status the way a completed
// reconcile does. This is what the artifact-name index is built from, so it is also
// what makes a cache an exact match for an artifact event.
func resolvingArtifact(pc *aimv1alpha2.AIMProfileCache, artifactName string) *aimv1alpha2.AIMProfileCache {
	if pc.Status.Artifacts == nil {
		pc.Status.Artifacts = map[string]aimv1alpha1.AIMResolvedArtifact{}
	}
	pc.Status.Artifacts[artifactName] = aimv1alpha1.AIMResolvedArtifact{
		UID:    artifactName + "-uid",
		Name:   artifactName,
		Model:  pc.Spec.ProfileName + "-model",
		Status: constants.AIMStatusReady,
	}
	return pc
}

func ownedByProfileCache(pc *aimv1alpha2.AIMProfileCache) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMProfileCache",
		Name:       pc.Name,
		UID:        pc.UID,
	}
}

func TestProfileCacheReconcileWritesObservedGenerationWhenAddingFinalizer(t *testing.T) {
	scheme := runtimeProjectionScheme(t)
	pc := makeSharedProfileCache("cache-first-pass")
	pc.Generation = 1

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha2.AIMProfileCache{}).
		WithObjects(pc).
		Build()
	domainReconciler := &aimprofilecache.ProfileCacheReconciler{Scheme: scheme}
	recorder := record.NewFakeRecorder(20)
	r := &AIMProfileCacheReconciler{
		Client:     c,
		Scheme:     scheme,
		Recorder:   recorder,
		reconciler: domainReconciler,
		pipeline: controllerutils.Pipeline[
			*aimv1alpha2.AIMProfileCache,
			*aimv1alpha2.AIMProfileCacheStatus,
			aimprofilecache.ProfileCacheFetchResult,
			aimprofilecache.ProfileCacheObservation,
		]{
			Client:         c,
			StatusClient:   c.Status(),
			Recorder:       recorder,
			ControllerName: profileCacheName,
			Reconciler:     domainReconciler,
			Scheme:         scheme,
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pc)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var got aimv1alpha2.AIMProfileCache
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(pc), &got); err != nil {
		t.Fatalf("get reconciled AIMProfileCache: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&got, finalizerProfileCacheArtifactCleanup) {
		t.Fatal("first reconcile did not add the artifact cleanup finalizer")
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf(
			"first reconcile observedGeneration = %d, want metadata generation %d",
			got.Status.ObservedGeneration,
			got.Generation,
		)
	}
}

// The candidate scan cannot compare source URIs without resolving each cache's profile,
// so it may over-select eligible caches. Indexed resolvers also appear in that scan and
// must be deduplicated in the returned union.
func TestFindProfileCachesForArtifact_DeduplicatesIndexedAndEligibleCaches(t *testing.T) {
	artifact := makeWatchedArtifact("artifact-llama")

	creator := resolvingArtifact(makeSharedProfileCache("cache-mi300x-thr"), artifact.Name)
	adopter := resolvingArtifact(makeSharedProfileCache("cache-mi300x-lat"), artifact.Name)
	// Same namespace, same mode, no storage class - so ArtifactAdoptableBy says yes -
	// but it tracks a different model source entirely.
	bystander := resolvingArtifact(makeSharedProfileCache("cache-mistral"), "artifact-mistral")

	r := newProfileCacheTestReconciler(t, creator, adopter, bystander, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{
			testNamespace + "/" + creator.Name,
			testNamespace + "/" + adopter.Name,
			testNamespace + "/" + bystander.Name,
		})
}

// A brand-new artifact is in nobody's status yet, so eligible namespace candidates are
// the only requests available. Without them first resolution strands.
func TestFindProfileCachesForArtifact_IncludesEligibleCachesWithoutIndex(t *testing.T) {
	fresh := makeWatchedArtifact("artifact-llama")

	waiting := makeSharedProfileCache("cache-waiting")
	// Populates the index with a different key, so an empty result for the fresh
	// artifact is a genuine miss rather than an unregistered index.
	elsewhere := resolvingArtifact(makeSharedProfileCache("cache-elsewhere"), "artifact-mistral")

	r := newProfileCacheTestReconciler(t, waiting, elsewhere, fresh)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), fresh),
		[]string{testNamespace + "/" + waiting.Name, testNamespace + "/" + elsewhere.Name})
}

// A nonempty index can still be incomplete while another adopter has not published the
// artifact in status. The indexed resolver and eligible candidate must both be woken.
func TestFindProfileCachesForArtifact_IncludesAdopterThatHasNotPublishedYet(t *testing.T) {
	artifact := makeWatchedArtifact("artifact-llama")

	published := resolvingArtifact(makeSharedProfileCache("cache-published"), artifact.Name)
	// Resolves the same source and would be returned by the candidate scan, but has not
	// completed the reconcile that would publish the artifact into its status.
	unpublished := makeSharedProfileCache("cache-unpublished")

	r := newProfileCacheTestReconciler(t, published, unpublished, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{
			testNamespace + "/" + published.Name,
			testNamespace + "/" + unpublished.Name,
		})
}

// Most caches waiting out a terminating artifact have dropped it from status.artifacts,
// but one lagging resolver can keep the exact index nonempty for the final Delete event.
// The candidate scan must still wake the unindexed adopters so one can recreate it.
//
// The artifact is deliberately absent from the client: on a Delete event the object is
// already gone, so the map function has only what it is handed.
func TestFindProfileCachesForArtifact_WakesAdoptersAfterArtifactDeleted(t *testing.T) {
	deleted := makeWatchedArtifact("artifact-llama")
	deleted.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	waiting := makeSharedProfileCache("cache-waiting")
	alsoWaiting := makeSharedProfileCache("cache-also-waiting")
	lagging := resolvingArtifact(makeSharedProfileCache("cache-lagging"), deleted.Name)
	elsewhere := resolvingArtifact(makeSharedProfileCache("cache-elsewhere"), "artifact-mistral")

	r := newProfileCacheTestReconciler(t, waiting, alsoWaiting, lagging, elsewhere)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), deleted),
		[]string{
			testNamespace + "/" + waiting.Name,
			testNamespace + "/" + alsoWaiting.Name,
			testNamespace + "/" + lagging.Name,
			testNamespace + "/" + elsewhere.Name,
		})
}

// The exact index keeps a stale resolver in the union even though it can no longer adopt
// the artifact; the candidate scan adds the current dedicated owner.
func TestFindProfileCachesForArtifact_UnionsStaleResolverWithCurrentAdopter(t *testing.T) {
	claimed := makeWatchedArtifact("artifact-llama")

	owner := makeSharedProfileCache("cache-owner")
	owner.Spec.Mode = aimv1alpha2.ProfileCacheModeDedicated
	stranded := resolvingArtifact(makeSharedProfileCache("cache-shared"), claimed.Name)
	// A Dedicated cache took ownership, which puts the artifact out of reach of every
	// Shared cache - including the one still publishing it.
	claimed.OwnerReferences = []metav1.OwnerReference{ownedByProfileCache(owner)}

	r := newProfileCacheTestReconciler(t, owner, stranded, claimed)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), claimed),
		[]string{
			testNamespace + "/" + owner.Name,
			testNamespace + "/" + stranded.Name,
		})
}

// Map functions run on the manager's watch goroutine, so an unchecked type assertion
// would take down the process rather than one reconcile. The predicate deliberately
// forwards objects it cannot inspect, which makes this the reachable end of that
// contract.
func TestFindProfileCachesForArtifact_IgnoresNonArtifactObject(t *testing.T) {
	r := newProfileCacheTestReconciler(t, makeSharedProfileCache("cache-shared"))

	got := r.findProfileCachesForArtifact(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "not-an-artifact", Namespace: testNamespace},
	})
	if got != nil {
		t.Fatalf("findProfileCachesForArtifact() = %v, want nil for a non-artifact object", got)
	}
}

// Shared profile caches deliberately share one artifact: generateArtifactName omits the
// cache name in Shared mode, so two caches whose profiles resolve the same source URI
// both track it. Only the creating cache carries the profile-cache.name label, so a
// label-keyed mapping woke that one and left every other adopter waiting on a Ready
// transition it never saw.
func TestFindProfileCachesForArtifact_WakesEveryAdopterOfSharedArtifact(t *testing.T) {
	creator := makeSharedProfileCache("cache-mi300x-thr")
	adopter := makeSharedProfileCache("cache-mi300x-lat")

	artifact := makeWatchedArtifact("artifact-llama")
	artifact.Labels = map[string]string{constants.LabelProfileCacheName: creator.Name}

	r := newProfileCacheTestReconciler(t, creator, adopter, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{testNamespace + "/" + creator.Name, testNamespace + "/" + adopter.Name})
}

func TestFindProfileCachesForArtifact_RespectsModeIsolation(t *testing.T) {
	shared := makeSharedProfileCache("cache-shared")
	owner := makeSharedProfileCache("cache-owner")
	owner.Spec.Mode = aimv1alpha2.ProfileCacheModeDedicated
	other := makeSharedProfileCache("cache-other")
	other.Spec.Mode = aimv1alpha2.ProfileCacheModeDedicated

	artifact := makeWatchedArtifact("artifact-owned")
	artifact.OwnerReferences = []metav1.OwnerReference{ownedByProfileCache(owner)}

	r := newProfileCacheTestReconciler(t, shared, owner, other, artifact)

	// An owned artifact is off-limits to Shared caches and to other Dedicated caches.
	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{testNamespace + "/" + owner.Name})
}

func TestFindProfileCachesForArtifact_SkipsStorageClassMismatch(t *testing.T) {
	matching := makeSharedProfileCache("cache-fast")
	matching.Spec.StorageClassName = "fast-ssd"
	mismatched := makeSharedProfileCache("cache-slow")
	mismatched.Spec.StorageClassName = "spinning-rust"
	unset := makeSharedProfileCache("cache-any")

	artifact := makeWatchedArtifact("artifact-fast")
	artifact.Spec.StorageClassName = "fast-ssd"

	r := newProfileCacheTestReconciler(t, matching, mismatched, unset, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{testNamespace + "/" + matching.Name, testNamespace + "/" + unset.Name})
}

// A cache that needs an adapter disk adds one in place to a diskless model artifact it
// can otherwise use, so it is woken for one; a diskless adapter artifact is never a
// candidate.
func TestFindProfileCachesForArtifact_AdapterCacheWakesForDisklessModelArtifact(t *testing.T) {
	adapterCache := makeSharedProfileCache("cache-lora")
	adapterCache.Spec.RequiresAdapterDisk = true
	plainCache := makeSharedProfileCache("cache-plain")

	diskless := makeWatchedArtifact("artifact-diskless")

	r := newProfileCacheTestReconciler(t, adapterCache, plainCache, diskless)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), diskless),
		[]string{testNamespace + "/" + adapterCache.Name, testNamespace + "/" + plainCache.Name})

	adapterArtifact := makeWatchedArtifact("artifact-adapter")
	adapterArtifact.Spec.Type = aimv1alpha1.ArtifactTypeAdapter

	r = newProfileCacheTestReconciler(t, adapterCache, plainCache, adapterArtifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), adapterArtifact),
		[]string{testNamespace + "/" + plainCache.Name})

	withDisk := makeWatchedArtifact("artifact-with-disk")
	withDisk.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}

	r = newProfileCacheTestReconciler(t, adapterCache, plainCache, withDisk)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), withDisk),
		[]string{testNamespace + "/" + adapterCache.Name, testNamespace + "/" + plainCache.Name})
}

// An artifact created before the label existed, or one whose label was stripped, still
// has adopters waiting on it.
func TestFindProfileCachesForArtifact_MapsUnlabelledArtifact(t *testing.T) {
	cache := makeSharedProfileCache("cache-shared")
	artifact := makeWatchedArtifact("artifact-unlabelled")

	r := newProfileCacheTestReconciler(t, cache, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{testNamespace + "/" + cache.Name})
}

func TestFindProfileCachesForArtifact_IgnoresOtherNamespaces(t *testing.T) {
	local := makeSharedProfileCache("cache-local")
	remote := makeSharedProfileCache("cache-remote")
	remote.Namespace = "other-namespace"

	artifact := makeWatchedArtifact("artifact-local")

	r := newProfileCacheTestReconciler(t, local, remote, artifact)

	assertRequestKeys(t, r.findProfileCachesForArtifact(context.Background(), artifact),
		[]string{testNamespace + "/" + local.Name})
}
