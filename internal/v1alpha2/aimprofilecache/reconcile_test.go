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

package aimprofilecache

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

func makeProfileCache(name string, profileName string, scope aimv1alpha1.AIMResolutionScope, mode aimv1alpha2.AIMProfileCacheMode, storageClass string) *aimv1alpha2.AIMProfileCache {
	return &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("pc-uid-" + name),
		},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:      profileName,
			ProfileScope:     scope,
			Mode:             mode,
			StorageClassName: storageClass,
		},
	}
}

func makeArtifact(name, sourceURI, modelID string, status constants.AIMStatus, storageClass string, ownerUID ...types.UID) aimv1alpha1.AIMArtifact {
	a := aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("artifact-uid-" + name),
		},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI:        sourceURI,
			ModelID:          modelID,
			StorageClassName: storageClass,
		},
		Status: aimv1alpha1.AIMArtifactStatus{
			Status: status,
		},
	}
	for _, uid := range ownerUID {
		a.OwnerReferences = append(a.OwnerReferences, metav1.OwnerReference{UID: uid})
	}
	return a
}

// markTerminating stages an artifact caught mid-deletion: a deletionTimestamp, a
// finalizer still holding the object, and the PVC the cache would otherwise publish.
// A deletionTimestamp is only ever observable while some finalizer holds the object, so
// the fixture carries one.
func markTerminating(artifact aimv1alpha1.AIMArtifact) aimv1alpha1.AIMArtifact {
	deletedAt := metav1.NewTime(time.Date(2026, 7, 30, 12, 58, 18, 0, time.UTC))
	artifact.DeletionTimestamp = &deletedAt
	artifact.Finalizers = []string{"example.com/hold"}
	artifact.Status.PersistentVolumeClaim = artifact.Name + "-cache-e1a0d5dc"
	return artifact
}

func makeProfile(name string, modelSources []aimv1alpha1.AIMModelSource) *aimv1alpha2.AIMProfile {
	return &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:        "test/model",
				Image:        "test:latest",
				ModelSources: modelSources,
			},
		},
	}
}

func TestComposeState_MatchesArtifactBySourceURI(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	artifact := makeArtifact("artifact-a", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{artifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 1 {
		t.Fatalf("expected 1 best artifact, got %d", len(obs.BestArtifacts))
	}
	if obs.BestArtifacts["org/model-a"].Name != "artifact-a" {
		t.Errorf("expected artifact-a, got %s", obs.BestArtifacts["org/model-a"].Name)
	}
	if len(obs.MissingCaches) != 0 {
		t.Errorf("expected 0 missing caches, got %d", len(obs.MissingCaches))
	}
}

func TestS3ArtifactIdentityIncludesRuntimeConfig(t *testing.T) {
	source := aimv1alpha1.AIMModelSource{
		ModelID:   "org/model",
		SourceURI: "s3://bucket/model",
	}
	first := &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{Name: "first"},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			RuntimeConfigRef: aimv1alpha1.RuntimeConfigRef{Name: "first-s3"},
		},
	}
	second := first.DeepCopy()
	second.Name = "second"
	second.Spec.Name = "second-s3"

	firstName, err := generateArtifactName(first, source)
	if err != nil {
		t.Fatal(err)
	}
	secondName, err := generateArtifactName(second, source)
	if err != nil {
		t.Fatal(err)
	}
	if firstName == secondName {
		t.Fatalf("different S3 runtime configs produced the same artifact name %q", firstName)
	}
	if s3RuntimeConfigMatches(
		source.SourceURI,
		first.Spec.RuntimeConfigRef,
		second.Spec.RuntimeConfigRef,
	) {
		t.Fatal("S3 artifacts from different runtime configs must not be reused")
	}
	if !s3RuntimeConfigMatches(
		"hf://org/model",
		first.Spec.RuntimeConfigRef,
		second.Spec.RuntimeConfigRef,
	) {
		t.Fatal("Hugging Face artifact reuse must retain existing behavior")
	}
}

// TestComposeState_RequiresAdapterDiskSkipsDisklessArtifact verifies that a
// cache which must serve adapters does not adopt an otherwise-matching artifact
// that lacks an adapter disk — it falls through to MissingCaches so PlanResources
// creates a disk-bearing one.
func TestComposeState_RequiresAdapterDiskSkipsDisklessArtifact(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.RequiresAdapterDisk = true
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	disklessArtifact := makeArtifact("artifact-a", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{disklessArtifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)
	if len(obs.BestArtifacts) != 0 {
		t.Errorf("expected diskless artifact to be skipped, got %d best artifacts", len(obs.BestArtifacts))
	}
	if len(obs.MissingCaches) != 1 {
		t.Fatalf("expected 1 missing cache, got %d", len(obs.MissingCaches))
	}

	// PlanResources must stamp an adapter disk onto the created artifact.
	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, obs)
	var created *aimv1alpha1.AIMArtifact
	planned := append(plan.GetToApply(), plan.GetToApplyWithoutOwnerRef()...)
	for _, obj := range planned {
		if a, ok := obj.(*aimv1alpha1.AIMArtifact); ok {
			created = a
			break
		}
	}
	if created == nil {
		t.Fatal("expected a created AIMArtifact in the plan")
	}
	if created.Spec.AdapterDisk == nil {
		t.Error("expected created artifact to carry an adapterDisk")
	}
}

// TestComposeState_RequiresAdapterDiskAdoptsDiskArtifact verifies that an
// existing artifact WITH an adapter disk is adopted normally.
func TestComposeState_RequiresAdapterDiskAdoptsDiskArtifact(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.RequiresAdapterDisk = true
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	diskArtifact := makeArtifact("artifact-a", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")
	diskArtifact.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{diskArtifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)
	if len(obs.BestArtifacts) != 1 || obs.BestArtifacts["org/model-a"].Name != "artifact-a" {
		t.Fatalf("expected disk-bearing artifact-a to be adopted, got %#v", obs.BestArtifacts)
	}
}

func TestComposeState_MissingArtifact(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 0 {
		t.Fatalf("expected 0 best artifacts, got %d", len(obs.BestArtifacts))
	}
	if len(obs.MissingCaches) != 1 {
		t.Fatalf("expected 1 missing cache, got %d", len(obs.MissingCaches))
	}
	if obs.MissingCaches[0].ModelID != "org/model-a" {
		t.Errorf("expected org/model-a, got %s", obs.MissingCaches[0].ModelID)
	}
}

func TestArtifactListErrorPreservesLastKnownCacheState(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Generation = 7
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})
	listErr := errors.New("transient artifact list failure")
	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{},
			Error: listErr,
		},
	}

	obs := r.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
		fetch,
	)
	if len(obs.MissingCaches) != 0 {
		t.Fatalf("artifact list failure produced %d missing caches, want 0", len(obs.MissingCaches))
	}
	var reported bool
	for _, health := range obs.GetComponentHealth() {
		if health.Component == artifactsComponentName &&
			len(health.Errors) == 1 &&
			errors.Is(health.Errors[0], listErr) {
			reported = true
		}
	}
	if !reported {
		t.Fatal("artifact list failure was not reported through component health")
	}
	plan := r.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
		obs,
	)
	if got := len(plan.GetToApply()) + len(plan.GetToApplyWithoutOwnerRef()); got != 0 {
		t.Fatalf("artifact list failure planned %d artifact applies, want 0", got)
	}

	status := &aimv1alpha2.AIMProfileCacheStatus{
		Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
			"artifact-a": {
				Name:                  "artifact-a",
				Status:                constants.AIMStatusReady,
				PersistentVolumeClaim: "working-cache-pvc",
			},
		},
	}
	cm := controllerutils.NewConditionManager([]metav1.Condition{{
		Type:   artifactsReadyConditionType,
		Status: metav1.ConditionTrue,
	}})
	r.DecorateStatus(status, cm, obs)

	if got := status.Artifacts["artifact-a"].PersistentVolumeClaim; got != "working-cache-pvc" {
		t.Fatalf("preserved artifact PVC = %q, want working-cache-pvc", got)
	}
	if status.ObservedGeneration != pc.Generation {
		t.Fatalf("observed generation = %d, want %d", status.ObservedGeneration, pc.Generation)
	}
	if condition := cm.Get(artifactsReadyConditionType); condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("last known %s condition was not preserved: %+v", artifactsReadyConditionType, condition)
	}
}

func TestComposeState_SharedModeSkipsOwnedArtifacts(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	// Artifact has an owner reference -> should be skipped in Shared mode
	ownedArtifact := makeArtifact("artifact-owned", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "", types.UID("some-other-uid"))

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{ownedArtifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 0 {
		t.Fatalf("expected 0 best artifacts (owned artifacts should be skipped), got %d", len(obs.BestArtifacts))
	}
	if len(obs.MissingCaches) != 1 {
		t.Fatalf("expected 1 missing cache, got %d", len(obs.MissingCaches))
	}
}

func TestComposeState_DedicatedModeOnlyUsesOwnedArtifacts(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeDedicated, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	sharedArtifact := makeArtifact("artifact-shared", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")
	ownedByUs := makeArtifact("artifact-ours", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "", pc.UID)

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{sharedArtifact, ownedByUs}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 1 {
		t.Fatalf("expected 1 best artifact, got %d", len(obs.BestArtifacts))
	}
	if obs.BestArtifacts["org/model-a"].Name != "artifact-ours" {
		t.Errorf("expected artifact-ours, got %s", obs.BestArtifacts["org/model-a"].Name)
	}
}

func TestComposeState_StorageClassFiltering(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "fast-ssd")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	wrongSC := makeArtifact("artifact-wrong-sc", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "slow-hdd")
	rightSC := makeArtifact("artifact-right-sc", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "fast-ssd")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{wrongSC, rightSC}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 1 {
		t.Fatalf("expected 1 best artifact, got %d", len(obs.BestArtifacts))
	}
	if obs.BestArtifacts["org/model-a"].Name != "artifact-right-sc" {
		t.Errorf("expected artifact-right-sc, got %s", obs.BestArtifacts["org/model-a"].Name)
	}
}

func TestComposeState_SelectsBestStatus(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	failedArtifact := makeArtifact("artifact-failed", "hf://org/model-a", "org/model-a", constants.AIMStatusFailed, "")
	readyArtifact := makeArtifact("artifact-ready", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{failedArtifact, readyArtifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 1 {
		t.Fatalf("expected 1 best artifact, got %d", len(obs.BestArtifacts))
	}
	if obs.BestArtifacts["org/model-a"].Name != "artifact-ready" {
		t.Errorf("expected artifact-ready (best status), got %s", obs.BestArtifacts["org/model-a"].Name)
	}
}

func TestComposeState_NoProfileResolved(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc-missing", "missing-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Error: nil, // not found, Value is nil
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 0 {
		t.Errorf("expected 0 best artifacts for unresolved profile, got %d", len(obs.BestArtifacts))
	}
	if len(obs.MissingCaches) != 0 {
		t.Errorf("expected 0 missing caches for unresolved profile, got %d", len(obs.MissingCaches))
	}
}

func TestComposeState_ClusterProfile(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc-cluster", "cluster-profile", aimv1alpha1.AIMResolutionScopeCluster, aimv1alpha2.ProfileCacheModeShared, "")

	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-profile"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "test/model",
				Image: "test:latest",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "org/model-b", SourceURI: "hf://org/model-b"},
				},
			},
		},
	}

	artifact := makeArtifact("artifact-b", "hf://org/model-b", "org/model-b", constants.AIMStatusProgressing, "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		clusterProfile: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{
			Value: clusterProfile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{artifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 1 {
		t.Fatalf("expected 1 best artifact from cluster profile, got %d", len(obs.BestArtifacts))
	}
	if obs.BestArtifacts["org/model-b"].Status.Status != constants.AIMStatusProgressing {
		t.Errorf("expected Progressing, got %s", obs.BestArtifacts["org/model-b"].Status.Status)
	}
}

func TestComposeState_EmptyModelSources(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc-empty", "empty-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("empty-profile", nil)

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if obs.BestArtifacts != nil {
		t.Errorf("expected nil BestArtifacts for empty model sources, got %v", obs.BestArtifacts)
	}
	if len(obs.MissingCaches) != 0 {
		t.Errorf("expected 0 missing caches for empty model sources, got %d", len(obs.MissingCaches))
	}
}

func TestComposeState_SkipsEmptyStatusArtifacts(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	noStatusArtifact := makeArtifact("artifact-no-status", "hf://org/model-a", "org/model-a", "", "")

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{
			Value: profile,
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{noStatusArtifact}},
		},
	}

	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)

	if len(obs.BestArtifacts) != 0 {
		t.Errorf("expected 0 best artifacts (empty status should be skipped), got %d", len(obs.BestArtifacts))
	}
	if len(obs.MissingCaches) != 1 {
		t.Errorf("expected 1 missing cache, got %d", len(obs.MissingCaches))
	}
}

func TestPlanResources_CreatesArtifactsForMissing(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "fast-ssd")

	obs := ProfileCacheObservation{
		ProfileCacheFetchResult: ProfileCacheFetchResult{profileCache: pc},
		MissingCaches: []aimv1alpha1.AIMModelSource{
			{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
			{ModelID: "org/model-b", SourceURI: "hf://org/model-b"},
		},
	}

	result := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, obs)

	if len(result.GetToApplyWithoutOwnerRef()) != 2 {
		t.Fatalf("expected 2 ApplyWithoutOwnerRef resources, got %d (apply: %d, applyWithout: %d)",
			len(result.GetToApplyWithoutOwnerRef()),
			len(result.GetToApply()),
			len(result.GetToApplyWithoutOwnerRef()))
	}
}

// TestPlanResources_PropagatesEnvAndRuntimeConfigRef verifies the cache's
// download-auth env and runtime config reference are stamped onto the
// AIMArtifact it creates, so the credential and the named runtime config both
// reach the download Job (matching the v1alpha1 template-cache behavior).
func TestPlanResources_PropagatesEnvAndRuntimeConfigRef(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeCluster, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.Env = []corev1.EnvVar{{Name: "HF_TOKEN", Value: "tok"}}
	pc.Spec.RuntimeConfigRef = aimv1alpha1.RuntimeConfigRef{Name: "fast"}

	obs := ProfileCacheObservation{
		ProfileCacheFetchResult: ProfileCacheFetchResult{profileCache: pc},
		MissingCaches: []aimv1alpha1.AIMModelSource{
			{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
		},
	}

	result := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, obs)
	planned := result.GetToApplyWithoutOwnerRef()
	if len(planned) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(planned))
	}
	art, ok := planned[0].(*aimv1alpha1.AIMArtifact)
	if !ok {
		t.Fatalf("expected an AIMArtifact, got %T", planned[0])
	}
	if art.Spec.Name != "fast" {
		t.Errorf("expected runtimeConfigRef to propagate onto artifact, got %q", art.Spec.Name)
	}
	if len(art.Spec.Env) != 1 || art.Spec.Env[0].Name != "HF_TOKEN" {
		t.Errorf("expected cache env to propagate onto artifact, got %+v", art.Spec.Env)
	}
}

func TestPlanResources_DedicatedModeUsesApply(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeDedicated, "")

	obs := ProfileCacheObservation{
		ProfileCacheFetchResult: ProfileCacheFetchResult{profileCache: pc},
		MissingCaches: []aimv1alpha1.AIMModelSource{
			{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
		},
	}

	result := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, obs)

	if len(result.GetToApply()) != 1 {
		t.Fatalf("expected 1 Apply resource for dedicated mode, got %d", len(result.GetToApply()))
	}
}

// Eligibility must stay true while the artifact is terminating: the artifact watch
// filters events through this predicate, so rejecting it here would drop both the
// deletion update and the final Delete event, and every cache that published the
// artifact would keep advertising it until the next resync. ComposeState is what
// refuses to adopt it.
func TestArtifactAdoptableBy_terminatingArtifactStaysEligible(t *testing.T) {
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	terminating := markTerminating(makeArtifact("artifact-dying", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, ""))

	if !ArtifactAdoptableBy(pc, &terminating) {
		t.Fatal("ArtifactAdoptableBy() = false for a terminating artifact, want true so the watch still wakes adopters")
	}
}

// An artifact that is being deleted must not be adopted: its PVC is about to be
// garbage collected, so publishing it would point every consumer of this cache at
// storage that is going away. It must be reported as terminating whether or not it
// ever published a status, so the cache waits instead of applying over it.
func TestComposeState_doesNotAdoptTerminatingArtifact(t *testing.T) {
	tests := []struct {
		name           string
		mode           aimv1alpha2.AIMProfileCacheMode
		artifactStatus constants.AIMStatus
	}{
		{
			name:           "shared cache, artifact was ready",
			mode:           aimv1alpha2.ProfileCacheModeShared,
			artifactStatus: constants.AIMStatusReady,
		},
		{
			name:           "dedicated cache, artifact was ready",
			mode:           aimv1alpha2.ProfileCacheModeDedicated,
			artifactStatus: constants.AIMStatusReady,
		},
		{
			// The deletion check must run before the empty-status guard. An artifact
			// deleted before its controller published a first status still has to be
			// waited out, because the replacement hashes to the same name in Shared
			// mode and the API server accepts an apply over a terminating object
			// silently. Reordering these two guards puts the model source back in
			// MissingCaches and writes to the dying object.
			name:           "shared cache, artifact never got a status",
			mode:           aimv1alpha2.ProfileCacheModeShared,
			artifactStatus: "",
		},
		{
			name:           "dedicated cache, artifact never got a status",
			mode:           aimv1alpha2.ProfileCacheModeDedicated,
			artifactStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, tt.mode, "")
			profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
			})

			terminating := markTerminating(makeArtifact("artifact-dying", "hf://org/model-a", "org/model-a", tt.artifactStatus, ""))
			if tt.mode == aimv1alpha2.ProfileCacheModeDedicated {
				terminating.OwnerReferences = []metav1.OwnerReference{{UID: pc.UID}}
			}

			fetch := ProfileCacheFetchResult{
				profileCache: pc,
				profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
				artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
					Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{terminating}},
				},
			}

			reconciler := &ProfileCacheReconciler{}
			reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}
			obs := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)

			if len(obs.BestArtifacts) != 0 {
				t.Fatalf("BestArtifacts = %v, want none", obs.BestArtifacts)
			}
			if got := obs.TerminatingArtifactNames; len(got) != 1 || got[0] != terminating.Name {
				t.Fatalf("TerminatingArtifactNames = %v, want [%s]", got, terminating.Name)
			}
			// Not MissingCaches: a replacement would hash to the same name in Shared
			// mode, so the apply would patch the object that is being deleted.
			if len(obs.MissingCaches) != 0 {
				t.Fatalf("MissingCaches = %v, want none while the artifact is terminating", obs.MissingCaches)
			}

			plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)
			if applied := len(plan.GetToApply()) + len(plan.GetToApplyWithoutOwnerRef()); applied != 0 {
				t.Fatalf("planned %d applies while the artifact was terminating, want 0", applied)
			}
		})
	}
}

// A terminating artifact must not shadow a live one serving the same model source.
func TestComposeState_adoptsLiveArtifactAlongsideTerminatingOne(t *testing.T) {
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	terminating := markTerminating(makeArtifact("artifact-dying", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, ""))
	live := makeArtifact("artifact-live", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")

	obs := (&ProfileCacheReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
		ProfileCacheFetchResult{
			profileCache: pc,
			profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
				Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{terminating, live}},
			},
		},
	)

	if got := obs.BestArtifacts["org/model-a"].Name; got != live.Name {
		t.Fatalf("BestArtifacts[org/model-a] = %q, want %q", got, live.Name)
	}
	if len(obs.TerminatingArtifactNames) != 0 {
		t.Fatalf("TerminatingArtifactNames = %v, want none once a live artifact resolves the source", obs.TerminatingArtifactNames)
	}
}

// Once the delete completes the artifact leaves the List, so the source becomes missing
// and the next plan recreates it. This is the other half of "no permanent wedge": the
// cache holds off while the artifact terminates, then creates a replacement cleanly.
// The wakeup that ends the wait is the artifact's own Delete event.
func TestPlanResources_recreatesArtifactAfterDeletionCompletes(t *testing.T) {
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}
	reconciler := &ProfileCacheReconciler{}

	fetchWith := func(artifacts ...aimv1alpha1.AIMArtifact) ProfileCacheFetchResult {
		return ProfileCacheFetchResult{
			profileCache: pc,
			profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
				Value: &aimv1alpha1.AIMArtifactList{Items: artifacts},
			},
		}
	}

	terminatingObs := reconciler.ComposeState(context.Background(), reconcileCtx,
		fetchWith(markTerminating(makeArtifact("artifact-dying", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, ""))))
	terminatingPlan := reconciler.PlanResources(context.Background(), reconcileCtx, terminatingObs)

	if applied := len(terminatingPlan.GetToApply()) + len(terminatingPlan.GetToApplyWithoutOwnerRef()); applied != 0 {
		t.Fatalf("planned %d applies while the artifact was terminating, want 0", applied)
	}

	goneObs := reconciler.ComposeState(context.Background(), reconcileCtx, fetchWith())
	gonePlan := reconciler.PlanResources(context.Background(), reconcileCtx, goneObs)

	if got := len(gonePlan.GetToApplyWithoutOwnerRef()); got != 1 {
		t.Fatalf("planned %d shared artifacts after the delete completed, want 1", got)
	}
}

// The cache must drop out of Ready while its only candidate is terminating, and it must
// stop advertising the doomed artifact and its PVC.
func TestDecorateStatus_terminatingArtifactIsNotReady(t *testing.T) {
	terminating := markTerminating(makeArtifact("artifact-dying", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, ""))
	status := &aimv1alpha2.AIMProfileCacheStatus{
		Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
			terminating.Name: {
				Name:                  terminating.Name,
				Model:                 "org/model-a",
				Status:                constants.AIMStatusReady,
				PersistentVolumeClaim: terminating.Status.PersistentVolumeClaim,
			},
		},
	}
	cm := controllerutils.NewConditionManager(nil)

	(&ProfileCacheReconciler{}).DecorateStatus(status, cm, ProfileCacheObservation{
		TerminatingArtifactNames: []string{terminating.Name},
	})

	condition := cm.Get(artifactsReadyConditionType)
	if condition == nil {
		t.Fatalf("no %s condition was set", artifactsReadyConditionType)
	}
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("%s = %v, want False", artifactsReadyConditionType, condition.Status)
	}
	if condition.Reason != aimv1alpha2.AIMProfileCacheReasonArtifactTerminating {
		t.Fatalf("reason = %q, want %q", condition.Reason, aimv1alpha2.AIMProfileCacheReasonArtifactTerminating)
	}
	if !strings.Contains(condition.Message, terminating.Name) {
		t.Fatalf("message %q does not name the terminating artifact %q", condition.Message, terminating.Name)
	}
	if status.Artifacts != nil {
		t.Fatalf("status.Artifacts = %v, want the doomed artifact and its PVC withdrawn", status.Artifacts)
	}
}

func TestPlanResources_CopiesDeclaredDownloadFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter *aimv1alpha1.AIMDownloadFilter
		// want is the spec the planner must persist. The filter is canonicalized
		// (sorted, deduplicated) before apply so that caches declaring the same file
		// set with reordered or duplicated globs submit byte-identical immutable
		// specs; nil stays nil so "no declaration" never collides with empty {}.
		want *aimv1alpha1.AIMDownloadFilter
	}{
		{name: "no filter", filter: nil, want: nil},
		{
			name:   "declared filter",
			filter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin"}},
			want:   &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin"}},
		},
		{
			name:   "reordered and duplicated globs canonicalized",
			filter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"b/*", "a/*", "a/*"}, Exclude: []string{"*.md", "*.bin"}},
			want:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"a/*", "b/*"}, Exclude: []string{"*.bin", "*.md"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
			obs := ProfileCacheObservation{
				ProfileCacheFetchResult: ProfileCacheFetchResult{profileCache: pc},
				MissingCaches: []aimv1alpha1.AIMModelSource{
					{ModelID: "org/model-a", SourceURI: "hf://org/model-a", DownloadFilter: tt.filter},
				},
			}

			result := (&ProfileCacheReconciler{}).PlanResources(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
				obs,
			)
			planned := result.GetToApplyWithoutOwnerRef()
			if len(planned) != 1 {
				t.Fatalf("expected 1 artifact, got %d", len(planned))
			}
			art := planned[0].(*aimv1alpha1.AIMArtifact)
			if !reflect.DeepEqual(art.Spec.DownloadFilter, tt.want) {
				t.Errorf("artifact downloadFilter = %+v, want %+v", art.Spec.DownloadFilter, tt.want)
			}
		})
	}
}

// The golden names pin today's identity for sources without a declared filter:
// any drift renames every existing shared artifact and orphans its cache.
func TestArtifactName_StableWithoutDeclaredFilter(t *testing.T) {
	source := aimv1alpha1.AIMModelSource{ModelID: "org/model", SourceURI: "hf://org/model"}
	tests := []struct {
		mode aimv1alpha2.AIMProfileCacheMode
		want string
	}{
		{mode: aimv1alpha2.ProfileCacheModeShared, want: "hf---org-model-04cea1e1c0"},
		{mode: aimv1alpha2.ProfileCacheModeDedicated, want: "hf---org-model-8047cb6b0e"},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			pc := &aimv1alpha2.AIMProfileCache{
				ObjectMeta: metav1.ObjectMeta{Name: "pc"},
				Spec:       aimv1alpha2.AIMProfileCacheSpec{Mode: tt.mode},
			}
			got, err := generateArtifactName(pc, source)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("artifact name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArtifactName_DivergesByDeclaredFilter(t *testing.T) {
	pc := &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{Name: "pc"},
		Spec:       aimv1alpha2.AIMProfileCacheSpec{Mode: aimv1alpha2.ProfileCacheModeShared},
	}
	nameFor := func(filter *aimv1alpha1.AIMDownloadFilter) string {
		t.Helper()
		name, err := generateArtifactName(pc, aimv1alpha1.AIMModelSource{
			ModelID: "org/model", SourceURI: "hf://org/model", DownloadFilter: filter,
		})
		if err != nil {
			t.Fatal(err)
		}
		return name
	}

	filters := map[string]*aimv1alpha1.AIMDownloadFilter{
		"nil":            nil,
		"empty":          {},
		"exclude subdir": {Exclude: []string{"*/*"}},
		"include only":   {Include: []string{"*.safetensors"}},
	}
	seen := map[string]string{}
	for label, filter := range filters {
		name := nameFor(filter)
		if other, ok := seen[name]; ok {
			t.Errorf("filters %q and %q share artifact name %q", label, other, name)
		}
		seen[name] = label
	}

	ordered := nameFor(&aimv1alpha1.AIMDownloadFilter{Include: []string{"a/*", "b/*"}, Exclude: []string{"*.bin", "*.md"}})
	reordered := nameFor(&aimv1alpha1.AIMDownloadFilter{Include: []string{"b/*", "a/*"}, Exclude: []string{"*.md", "*.bin"}})
	if ordered != reordered {
		t.Errorf("glob order changed the artifact name: %q vs %q", ordered, reordered)
	}
}

// Drives a declared download_filter from profile YAML, through the discovery catalog
// parser, into the artifact the cache plans, so a filter dropped anywhere in between
// fails here rather than only in the parser or planner tests.
func TestPlanResources_ArtifactFromDeclaredProfileYAML(t *testing.T) {
	const noFilterName = "hf---org-model-04cea1e1c0"

	plan := func(t *testing.T, declaration string) *aimv1alpha1.AIMArtifact {
		t.Helper()
		raw := `aim_id: org/model
model_id: org/model
metadata:
  engine: vllm
  metric: latency
  precision: fp16
  type: optimized
` + declaration
		catalog, err := aimprofile.ParseDiscoveryCatalog(&corev1.ConfigMap{
			Data: map[string]string{
				aimprofile.FlattenProfilePath("org/model/vllm-mi300x-fp16-tp1-latency.yaml"): raw,
			},
		})
		if err != nil {
			t.Fatalf("ParseDiscoveryCatalog: %v", err)
		}
		if len(catalog.Profiles) != 1 || len(catalog.Profiles[0].Spec.ModelSources) != 1 {
			t.Fatalf("catalog = %+v, want one profile with one model source", catalog.Profiles)
		}

		pc := &aimv1alpha2.AIMProfileCache{
			ObjectMeta: metav1.ObjectMeta{Name: "pc"},
			Spec:       aimv1alpha2.AIMProfileCacheSpec{Mode: aimv1alpha2.ProfileCacheModeShared},
		}
		result := (&ProfileCacheReconciler{}).PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
			ProfileCacheObservation{
				ProfileCacheFetchResult: ProfileCacheFetchResult{profileCache: pc},
				MissingCaches:           catalog.Profiles[0].Spec.ModelSources,
			},
		)
		planned := result.GetToApplyWithoutOwnerRef()
		if len(planned) != 1 {
			t.Fatalf("planned %d objects, want 1 artifact", len(planned))
		}
		return planned[0].(*aimv1alpha1.AIMArtifact)
	}

	if got := plan(t, "").Name; got != noFilterName {
		t.Fatalf("artifact name without a declaration = %q, want the pinned %q", got, noFilterName)
	}

	whole := plan(t, "download_filter: {}\n")
	if !reflect.DeepEqual(whole.Spec.DownloadFilter, &aimv1alpha1.AIMDownloadFilter{}) {
		t.Errorf("download_filter: {} planned spec.downloadFilter = %#v, want an empty filter", whole.Spec.DownloadFilter)
	}

	globs := plan(t, `download_filter:
  include: ["vae/*", "*.json", "transformer/*"]
  exclude: ["*.bin", "*.md"]
`)
	wantGlobs := &aimv1alpha1.AIMDownloadFilter{
		Include: []string{"*.json", "transformer/*", "vae/*"},
		Exclude: []string{"*.bin", "*.md"},
	}
	if !reflect.DeepEqual(globs.Spec.DownloadFilter, wantGlobs) {
		t.Errorf("include/exclude planned spec.downloadFilter = %#v, want %#v", globs.Spec.DownloadFilter, wantGlobs)
	}

	reordered := plan(t, `download_filter:
  include: ["transformer/*", "vae/*", "*.json"]
  exclude: ["*.md", "*.bin"]
`)
	if reordered.Name != globs.Name {
		t.Errorf("reordered globs renamed the artifact: %q vs %q", reordered.Name, globs.Name)
	}

	names := map[string]string{
		"no declaration":  noFilterName,
		"empty filter":    whole.Name,
		"include/exclude": globs.Name,
	}
	seen := map[string]string{}
	for label, name := range names {
		if other, ok := seen[name]; ok {
			t.Errorf("%s and %s share artifact name %q", label, other, name)
		}
		seen[name] = label
	}
}

func TestComposeState_MatchesArtifactByDownloadFilter(t *testing.T) {
	tests := []struct {
		name           string
		artifactFilter *aimv1alpha1.AIMDownloadFilter
		sourceFilter   *aimv1alpha1.AIMDownloadFilter
		wantAdopted    bool
	}{
		{name: "both absent", wantAdopted: true},
		{name: "source declares, artifact has none", sourceFilter: &aimv1alpha1.AIMDownloadFilter{}},
		{
			name:           "whole-repository artifact serves a source without a filter",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{},
			wantAdopted:    true,
		},
		{
			name:           "whole-repository artifact serves a narrower declared filter",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{},
			sourceFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
			wantAdopted:    true,
		},
		{
			name:           "filtered artifact does not serve a source without a filter",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
		},
		{
			name:           "different filters",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
			sourceFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
		},
		{
			name:           "equal filters in different order",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin", "*.md"}},
			sourceFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{}, Exclude: []string{"*.md", "*.bin"}},
			wantAdopted:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
			profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-a", SourceURI: "hf://org/model-a", DownloadFilter: tt.sourceFilter},
			})
			artifact := makeArtifact("artifact-a", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")
			artifact.Spec.DownloadFilter = tt.artifactFilter

			obs := (&ProfileCacheReconciler{}).ComposeState(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc},
				ProfileCacheFetchResult{
					profileCache: pc,
					profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
					artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
						Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{artifact}},
					},
				},
			)

			_, adopted := obs.BestArtifacts["org/model-a"]
			if adopted != tt.wantAdopted {
				t.Errorf("adopted = %v, want %v", adopted, tt.wantAdopted)
			}
			if wantMissing := !tt.wantAdopted; (len(obs.MissingCaches) == 1) != wantMissing {
				t.Errorf("missing caches = %d, want missing=%v", len(obs.MissingCaches), wantMissing)
			}
		})
	}
}

// replacementFixture stages a cache that serves served-artifact for a source whose
// download filter has since changed, so served-artifact no longer matches it.
type replacementFixture struct {
	pc              *aimv1alpha2.AIMProfileCache
	profile         *aimv1alpha2.AIMProfile
	served          aimv1alpha1.AIMArtifact
	replacementName string
}

func newReplacementFixture(t *testing.T, mode aimv1alpha2.AIMProfileCacheMode) replacementFixture {
	t.Helper()
	source := aimv1alpha1.AIMModelSource{
		ModelID:        "org/model-a",
		SourceURI:      "hf://org/model-a",
		DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors", "transformer/*"}},
	}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, mode, "")
	served := makeArtifact("served-artifact", source.SourceURI, source.ModelID, constants.AIMStatusReady, "", ownerUIDsFor(pc)...)
	served.Status.PersistentVolumeClaim = "served-artifact-pvc"
	pc.Status.Artifacts = buildResolvedArtifacts(map[string]aimv1alpha1.AIMArtifact{source.ModelID: served})

	replacementName, err := generateArtifactName(pc, source)
	if err != nil {
		t.Fatal(err)
	}
	return replacementFixture{
		pc:              pc,
		profile:         makeProfile("my-profile", []aimv1alpha1.AIMModelSource{source}),
		served:          served,
		replacementName: replacementName,
	}
}

func ownerUIDsFor(pc *aimv1alpha2.AIMProfileCache) []types.UID {
	if pc.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated {
		return []types.UID{pc.UID}
	}
	return nil
}

func (f replacementFixture) replacement(status constants.AIMStatus) aimv1alpha1.AIMArtifact {
	artifact := makeArtifact(f.replacementName, "hf://org/model-a", "org/model-a", status, "", ownerUIDsFor(f.pc)...)
	filter := canonicalDownloadFilter(f.profile.Spec.ModelSources[0].DownloadFilter)
	artifact.Spec.DownloadFilter = &filter
	artifact.Status.PersistentVolumeClaim = f.replacementName + "-pvc"
	return artifact
}

func (f replacementFixture) fetch(artifacts ...aimv1alpha1.AIMArtifact) ProfileCacheFetchResult {
	return ProfileCacheFetchResult{
		profileCache: f.pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: f.profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: artifacts},
		},
	}
}

func (f replacementFixture) reconcile(t *testing.T, artifacts ...aimv1alpha1.AIMArtifact) (ProfileCacheObservation, *aimv1alpha2.AIMProfileCacheStatus, *controllerutils.ConditionManager) {
	t.Helper()
	return f.reconcileFetch(t, f.fetch(artifacts...))
}

func (f replacementFixture) reconcileFetch(t *testing.T, fetch ProfileCacheFetchResult) (ProfileCacheObservation, *aimv1alpha2.AIMProfileCacheStatus, *controllerutils.ConditionManager) {
	t.Helper()
	r := &ProfileCacheReconciler{}
	obs := r.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: f.pc},
		fetch,
	)
	// The pipeline decorates the reconciled object's own status, which is also the
	// object the observation holds; a copy would hide reads of overwritten fields.
	status := &f.pc.Status
	cm := controllerutils.NewConditionManager(status.Conditions)
	r.DecorateStatus(status, cm, obs)
	status.Conditions = cm.Conditions()
	return obs, status, cm
}

func (f replacementFixture) plannedApplies(t *testing.T, obs ProfileCacheObservation) controllerutils.PlanResult {
	t.Helper()
	return (&ProfileCacheReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: f.pc},
		obs,
	)
}

// emittedEvents returns the events the pipeline would record for the conditions cm
// now holds, given the conditions the cache had before the reconcile.
func emittedEvents(pc *aimv1alpha2.AIMProfileCache, previous []metav1.Condition, cm *controllerutils.ConditionManager) []string {
	recorder := record.NewFakeRecorder(20)
	controllerutils.EmitConditionTransitions(recorder, pc, controllerutils.DiffConditionTransitions(previous, cm.Conditions()), cm)
	controllerutils.EmitRecurringEvents(recorder, pc, cm)
	close(recorder.Events)
	var events []string
	for event := range recorder.Events {
		events = append(events, event)
	}
	return events
}

func eventsWithReason(events []string, reason string) []string {
	var matching []string
	for _, event := range events {
		if strings.Contains(event, " "+reason+" ") {
			matching = append(matching, event)
		}
	}
	return matching
}

func failedArtifactWithMessage(artifact aimv1alpha1.AIMArtifact, message string) aimv1alpha1.AIMArtifact {
	artifact.Status.Conditions = []metav1.Condition{{
		Type:    controllerutils.ConditionTypeReady,
		Status:  metav1.ConditionFalse,
		Message: message,
	}}
	return artifact
}

func TestReplacement_KeepsServedArtifactUntilReplacementIsReady(t *testing.T) {
	tests := []struct {
		name                string
		mode                aimv1alpha2.AIMProfileCacheMode
		replacement         func(replacementFixture) []aimv1alpha1.AIMArtifact
		wantCreate          bool
		wantReason          string
		wantMessageContains string
	}{
		{
			name:                "replacement not created yet",
			replacement:         func(replacementFixture) []aimv1alpha1.AIMArtifact { return nil },
			wantCreate:          true,
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
			wantMessageContains: "is created",
		},
		{
			name:                "dedicated cache creates its own replacement",
			mode:                aimv1alpha2.ProfileCacheModeDedicated,
			replacement:         func(replacementFixture) []aimv1alpha1.AIMArtifact { return nil },
			wantCreate:          true,
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
			wantMessageContains: "is created",
		},
		{
			name: "replacement downloading",
			replacement: func(f replacementFixture) []aimv1alpha1.AIMArtifact {
				return []aimv1alpha1.AIMArtifact{f.replacement(constants.AIMStatusProgressing)}
			},
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
			wantMessageContains: "is Progressing",
		},
		{
			name: "replacement failed",
			replacement: func(f replacementFixture) []aimv1alpha1.AIMArtifact {
				return []aimv1alpha1.AIMArtifact{failedArtifactWithMessage(f.replacement(constants.AIMStatusFailed), "download returned 403")}
			},
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacementFailed,
			wantMessageContains: "download returned 403",
		},
		{
			name: "replacement download stalled",
			replacement: func(f replacementFixture) []aimv1alpha1.AIMArtifact {
				return []aimv1alpha1.AIMArtifact{failedArtifactWithMessage(f.replacement(constants.AIMStatusDegraded), "download made no progress")}
			},
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacementFailed,
			wantMessageContains: "download made no progress",
		},
		{
			name: "previous replacement still deleting",
			replacement: func(f replacementFixture) []aimv1alpha1.AIMArtifact {
				return []aimv1alpha1.AIMArtifact{markTerminating(f.replacement(constants.AIMStatusProgressing))}
			},
			wantReason:          aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
			wantMessageContains: "finishes deleting",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.mode
			if mode == "" {
				mode = aimv1alpha2.ProfileCacheModeShared
			}
			f := newReplacementFixture(t, mode)
			obs, status, cm := f.reconcile(t, append([]aimv1alpha1.AIMArtifact{f.served}, tt.replacement(f)...)...)

			resolved, ok := status.Artifacts[f.served.Name]
			if !ok || len(status.Artifacts) != 1 || resolved.PersistentVolumeClaim != "served-artifact-pvc" {
				t.Fatalf("status.artifacts = %+v, want only %s with its PVC", status.Artifacts, f.served.Name)
			}
			if ready := cm.Get(artifactsReadyConditionType); ready == nil || ready.Status != metav1.ConditionTrue {
				t.Fatalf("%s = %+v, want True while the served artifact is Ready", artifactsReadyConditionType, ready)
			}
			replacing := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing)
			if replacing == nil || replacing.Status != metav1.ConditionTrue || replacing.Reason != tt.wantReason {
				t.Fatalf("ArtifactsReplacing = %+v, want True/%s", replacing, tt.wantReason)
			}
			if !strings.Contains(replacing.Message, f.served.Name) || !strings.Contains(replacing.Message, tt.wantMessageContains) {
				t.Fatalf("ArtifactsReplacing message %q, want it to name %s and contain %q", replacing.Message, f.served.Name, tt.wantMessageContains)
			}
			if len(obs.TerminatingArtifactNames) != 0 {
				t.Fatalf("terminating artifacts %v reported against the cache's readiness", obs.TerminatingArtifactNames)
			}

			plan := f.plannedApplies(t, obs)
			applies := plan.GetToApplyWithoutOwnerRef()
			if mode == aimv1alpha2.ProfileCacheModeDedicated {
				if len(applies) != 0 {
					t.Fatalf("dedicated cache planned %d applies without an owner", len(applies))
				}
				applies = plan.GetToApply()
			}
			if created := len(applies) == 1 && applies[0].GetName() == f.replacementName; created != tt.wantCreate || len(applies) > 1 {
				t.Fatalf("planned applies %d (replacement created = %v), want replacement created = %v", len(applies), created, tt.wantCreate)
			}
		})
	}
}

func TestReplacement_FailureEmitsWarning(t *testing.T) {
	f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
	previous := slices.Clone(f.pc.Status.Conditions)
	_, _, cm := f.reconcile(t, f.served, failedArtifactWithMessage(f.replacement(constants.AIMStatusFailed), "download returned 403"))

	failures := eventsWithReason(emittedEvents(f.pc, previous, cm), aimv1alpha2.AIMProfileCacheReasonReplacementFailed)
	if len(failures) != 1 || !strings.HasPrefix(failures[0], corev1.EventTypeWarning+" ") {
		t.Fatalf("replacement failure events = %v, want one Warning", failures)
	}
}

func TestReplacement_SwitchesOnceReplacementIsReady(t *testing.T) {
	f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
	f.pc.Status.Conditions = []metav1.Condition{{
		Type:   aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing,
		Status: metav1.ConditionTrue,
		Reason: aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
	}}
	artifacts := []aimv1alpha1.AIMArtifact{f.served, f.replacement(constants.AIMStatusReady)}

	previous := slices.Clone(f.pc.Status.Conditions)
	obs, status, cm := f.reconcile(t, artifacts...)

	if len(obs.Replacements) != 0 || len(obs.MissingCaches) != 0 {
		t.Fatalf("replacements = %v, missing = %v, want none once the replacement is Ready", obs.Replacements, obs.MissingCaches)
	}
	if _, ok := status.Artifacts[f.replacementName]; !ok || len(status.Artifacts) != 1 {
		t.Fatalf("status.artifacts = %+v, want only %s", status.Artifacts, f.replacementName)
	}
	replacing := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing)
	if replacing == nil || replacing.Status != metav1.ConditionFalse || replacing.Reason != aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced {
		t.Fatalf("ArtifactsReplacing = %+v, want False/%s", replacing, aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced)
	}
	switchEvents := eventsWithReason(emittedEvents(f.pc, previous, cm), aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced)
	if len(switchEvents) != 1 || !strings.HasPrefix(switchEvents[0], corev1.EventTypeNormal+" ") ||
		!strings.Contains(switchEvents[0], f.served.Name) || !strings.Contains(switchEvents[0], f.replacementName) {
		t.Fatalf("switch events = %v, want one Normal event naming %s and %s", switchEvents, f.served.Name, f.replacementName)
	}

	// The next reconcile starts from the status the switch wrote.
	previous = slices.Clone(f.pc.Status.Conditions)
	_, _, cm = f.reconcile(t, artifacts...)
	if again := eventsWithReason(emittedEvents(f.pc, previous, cm), aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced); len(again) != 0 {
		t.Fatalf("reconcile after the switch emitted %v, want no further switch events", again)
	}
	if kept := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing); kept == nil || kept.Reason != aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced {
		t.Fatalf("ArtifactsReplacing after the switch = %+v, want the switch kept on record", kept)
	}
}

func TestReplacement_MoveToUnreadyArtifactIsNotReportedAsSwitch(t *testing.T) {
	f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
	f.pc.Status.Conditions = []metav1.Condition{{
		Type:   aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing,
		Status: metav1.ConditionTrue,
		Reason: aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
	}}

	// The served artifact was deleted while its replacement was still downloading.
	previous := slices.Clone(f.pc.Status.Conditions)
	_, status, cm := f.reconcile(t, markTerminating(f.served), f.replacement(constants.AIMStatusProgressing))

	if _, ok := status.Artifacts[f.replacementName]; !ok || len(status.Artifacts) != 1 {
		t.Fatalf("status.artifacts = %+v, want only the downloading %s", status.Artifacts, f.replacementName)
	}
	if ready := cm.Get(artifactsReadyConditionType); ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("%s = %+v, want False while nothing Ready serves the model", artifactsReadyConditionType, ready)
	}
	if replacing := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing); replacing != nil {
		t.Fatalf("ArtifactsReplacing = %+v, want the stale replacement condition removed", replacing)
	}
	if events := eventsWithReason(emittedEvents(f.pc, previous, cm), aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced); len(events) != 0 {
		t.Fatalf("switch events = %v, want none for a move to an artifact that is not Ready", events)
	}
}

func TestReplacement_UnservedSourceStillGatesReadiness(t *testing.T) {
	f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
	f.profile.Spec.ModelSources = append(f.profile.Spec.ModelSources,
		aimv1alpha1.AIMModelSource{ModelID: "org/model-b", SourceURI: "hf://org/model-b"})

	obs, status, cm := f.reconcile(t, f.served)

	if len(obs.MissingCaches) != 2 {
		t.Fatalf("missing caches = %d, want the replacement and the unserved source", len(obs.MissingCaches))
	}
	if _, ok := status.Artifacts[f.served.Name]; !ok || len(status.Artifacts) != 1 {
		t.Fatalf("status.artifacts = %+v, want only %s", status.Artifacts, f.served.Name)
	}
	ready := cm.Get(artifactsReadyConditionType)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != aimv1alpha2.AIMProfileCacheReasonCreatingCaches {
		t.Fatalf("%s = %+v, want False/%s for the unserved source", artifactsReadyConditionType, ready, aimv1alpha2.AIMProfileCacheReasonCreatingCaches)
	}
	if replacing := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing); replacing == nil || replacing.Status != metav1.ConditionTrue {
		t.Fatalf("ArtifactsReplacing = %+v, want True for the replaced source", replacing)
	}
}

func TestReplacement_ProfileFetchErrorKeepsServedArtifact(t *testing.T) {
	f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
	f.pc.Status.Conditions = []metav1.Condition{
		{Type: artifactsReadyConditionType, Status: metav1.ConditionTrue, Reason: aimv1alpha2.AIMProfileCacheReasonAllCachesReady},
		{Type: aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing, Status: metav1.ConditionTrue, Reason: aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts},
	}
	fetch := f.fetch(f.served, f.replacement(constants.AIMStatusProgressing))
	fetch.profile = controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Error: errors.New("transient profile read failure")}

	_, status, cm := f.reconcileFetch(t, fetch)

	if !reflect.DeepEqual(status.Artifacts, f.pc.Status.Artifacts) {
		t.Fatalf("status.artifacts = %+v, want the served artifact kept: %+v", status.Artifacts, f.pc.Status.Artifacts)
	}
	for _, conditionType := range []string{artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing} {
		if condition := cm.Get(conditionType); condition == nil || condition.Status != metav1.ConditionTrue {
			t.Fatalf("%s = %+v, want the last known True kept", conditionType, condition)
		}
	}
}

func TestReplacement_NotUsedWhenServedArtifactCannotServe(t *testing.T) {
	tests := []struct {
		name       string
		adjust     func(*replacementFixture)
		wantServed bool
	}{
		{
			name: "whole-repository artifact already serves the new filter",
			adjust: func(f *replacementFixture) {
				f.served.Spec.DownloadFilter = &aimv1alpha1.AIMDownloadFilter{}
			},
			wantServed: true,
		},
		{
			name: "served artifact is being deleted",
			adjust: func(f *replacementFixture) {
				f.served = markTerminating(f.served)
			},
		},
		{
			name: "served artifact is not Ready",
			adjust: func(f *replacementFixture) {
				f.served.Status.Status = constants.AIMStatusDegraded
			},
		},
		{
			name: "source points at a different repository",
			adjust: func(f *replacementFixture) {
				f.profile.Spec.ModelSources[0].SourceURI = "hf://org/model-b"
			},
		},
		{
			name: "served artifact was recreated under the same name",
			adjust: func(f *replacementFixture) {
				f.served.UID = "recreated-uid"
			},
		},
		{
			name: "cache now requires a different storage class",
			adjust: func(f *replacementFixture) {
				f.pc.Spec.StorageClassName = "fast"
				f.served.Spec.StorageClassName = "slow"
			},
		},
		{
			name: "s3 source now resolves through a different runtime config",
			adjust: func(f *replacementFixture) {
				f.profile.Spec.ModelSources[0].SourceURI = "s3://bucket/model-a"
				f.served.Spec.SourceURI = "s3://bucket/model-a"
				f.served.Spec.RuntimeConfigRef = aimv1alpha1.RuntimeConfigRef{Name: "previous-s3"}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReplacementFixture(t, aimv1alpha2.ProfileCacheModeShared)
			tt.adjust(&f)
			obs, _, cm := f.reconcile(t, f.served)

			_, served := obs.BestArtifacts["org/model-a"]
			if served != tt.wantServed {
				t.Fatalf("model served = %v, want %v", served, tt.wantServed)
			}
			if len(obs.Replacements) != 0 {
				t.Fatalf("replacements = %v, want none", obs.Replacements)
			}
			if replacing := cm.Get(aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing); replacing != nil {
				t.Fatalf("ArtifactsReplacing = %+v, want unset", replacing)
			}
			if wantMissing := !tt.wantServed; (len(obs.MissingCaches) == 1) != wantMissing {
				t.Fatalf("missing caches = %d, want an artifact created = %v", len(obs.MissingCaches), wantMissing)
			}
			wantReady := metav1.ConditionFalse
			if tt.wantServed {
				wantReady = metav1.ConditionTrue
			}
			if ready := cm.Get(artifactsReadyConditionType); ready == nil || ready.Status != wantReady {
				t.Fatalf("%s = %+v, want %s", artifactsReadyConditionType, ready, wantReady)
			}
		})
	}
}
