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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
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
