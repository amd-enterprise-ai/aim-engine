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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// TestPlanResources_RequiresAdapterDiskCreatesArtifactWithDisk verifies that a
// cache which must serve adapters creates its missing artifact with an adapter disk.
func TestPlanResources_RequiresAdapterDiskCreatesArtifactWithDisk(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.RequiresAdapterDisk = true
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
		{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
	})

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{},
		},
	}

	ctx := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}
	obs := r.ComposeState(context.Background(), ctx, fetch)
	plan := r.PlanResources(context.Background(), ctx, obs)

	planned := plan.GetToApplyWithoutOwnerRef()
	if len(planned) != 1 {
		t.Fatalf("expected one created artifact, got %d", len(planned))
	}
	created, ok := planned[0].(*aimv1alpha1.AIMArtifact)
	if !ok {
		t.Fatalf("expected an AIMArtifact, got %T", planned[0])
	}
	if created.Spec.AdapterDisk == nil {
		t.Error("expected created artifact to carry an adapterDisk")
	}
}

// TestPlanResources_RequiresAdapterDiskUpgradesDisklessArtifactInPlace covers a cache that gains
// requiresAdapterDisk after resolving a diskless artifact - here one whose name is
// not the cache's generated name, as when it was adopted from elsewhere. The cache
// must keep resolving that artifact (status.artifacts and ArtifactsReady unchanged,
// so services mounting its model PVC are not disturbed) and add the disk to it in
// place, owning nothing but spec.adapterDisk, rather than create a second artifact.
func TestPlanResources_RequiresAdapterDiskUpgradesDisklessArtifactInPlace(t *testing.T) {
	r := &ProfileCacheReconciler{}
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.RequiresAdapterDisk = true
	source := aimv1alpha1.AIMModelSource{ModelID: "org/model-a", SourceURI: "hf://org/model-a"}
	profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{source})

	existing := makeArtifact("adopted-artifact", source.SourceURI, source.ModelID, constants.AIMStatusReady, "")
	existing.Status.PersistentVolumeClaim = "adopted-artifact-cache"

	fetch := ProfileCacheFetchResult{
		profileCache: pc,
		profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{existing}},
		},
	}
	ctx := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}
	obs := r.ComposeState(context.Background(), ctx, fetch)

	if len(obs.MissingCaches) != 0 {
		t.Fatalf("MissingCaches = %v, want none", obs.MissingCaches)
	}
	if got := obs.BestArtifacts[source.ModelID].Name; got != existing.Name {
		t.Fatalf("resolved artifact = %q, want %q", got, existing.Name)
	}
	if len(obs.AdapterDiskUpgrades) != 1 || obs.AdapterDiskUpgrades[0].Name != existing.Name {
		t.Fatalf("AdapterDiskUpgrades = %d artifact(s), want [%s]", len(obs.AdapterDiskUpgrades), existing.Name)
	}

	status := &aimv1alpha2.AIMProfileCacheStatus{}
	cm := controllerutils.NewConditionManager(nil)
	r.DecorateStatus(status, cm, obs)
	if got := status.Artifacts[existing.Name].PersistentVolumeClaim; got != existing.Status.PersistentVolumeClaim {
		t.Errorf("status.artifacts PVC = %q, want %q", got, existing.Status.PersistentVolumeClaim)
	}
	if cond := cm.Get(artifactsReadyConditionType); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("%s = %+v, want True", artifactsReadyConditionType, cond)
	}

	plan := r.PlanResources(context.Background(), ctx, obs)
	if n := len(plan.GetToApply()) + len(plan.GetToApplyWithoutOwnerRef()); n != 0 {
		t.Fatalf("planned %d full artifact applies, want none", n)
	}
	fields := plan.GetToApplyFields()
	if len(fields) != 1 {
		t.Fatalf("planned %d field applies, want 1", len(fields))
	}
	if fields[0].FieldManagerSuffix != constants.FieldManagerSuffixAdapterDisk {
		t.Errorf("field manager suffix = %q, want %q", fields[0].FieldManagerSuffix, constants.FieldManagerSuffixAdapterDisk)
	}
	patch, ok := fields[0].Object.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("field apply object = %T, want *unstructured.Unstructured", fields[0].Object)
	}
	if patch.GetName() != existing.Name || patch.GetKind() != "AIMArtifact" {
		t.Errorf("field apply targets %s %q, want AIMArtifact %q", patch.GetKind(), patch.GetName(), existing.Name)
	}
	spec, _, _ := unstructured.NestedMap(patch.Object, "spec")
	if _, hasDisk := spec["adapterDisk"]; !hasDisk || len(spec) != 1 {
		t.Errorf("field apply spec = %v, want only adapterDisk", spec)
	}
	if ev := fields[0].Event; ev == nil || ev.Target.GetName() != existing.Name ||
		ev.Reason != constants.ReasonAdapterDiskRequested {
		t.Errorf("field apply event = %+v, want %s on artifact %q", ev, constants.ReasonAdapterDiskRequested, existing.Name)
	}
}

// TestComposeState_RequiresAdapterDiskRanksByStatusFirst verifies how a cache that
// requires an adapter disk picks between a diskless and a disk-bearing artifact:
// the better status wins, so a Ready cache never falls back to a downloading or
// failed artifact, and on a tie the one that already has the disk wins.
func TestComposeState_RequiresAdapterDiskRanksByStatusFirst(t *testing.T) {
	tests := []struct {
		name           string
		disklessStatus constants.AIMStatus
		withDiskStatus constants.AIMStatus
		want           string
		wantUpgrade    bool
	}{
		{"tie prefers disk", constants.AIMStatusReady, constants.AIMStatusReady, "with-disk", false},
		{"ready diskless beats progressing disk", constants.AIMStatusReady, constants.AIMStatusProgressing, "diskless", true},
		{"ready diskless beats failed disk", constants.AIMStatusReady, constants.AIMStatusFailed, "diskless", true},
		{"ready disk beats progressing diskless", constants.AIMStatusProgressing, constants.AIMStatusReady, "with-disk", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ProfileCacheReconciler{}
			pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
			pc.Spec.RequiresAdapterDisk = true
			profile := makeProfile("my-profile", []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-a", SourceURI: "hf://org/model-a"},
			})

			diskless := makeArtifact("diskless", "hf://org/model-a", "org/model-a", tt.disklessStatus, "")
			withDisk := makeArtifact("with-disk", "hf://org/model-a", "org/model-a", tt.withDiskStatus, "")
			withDisk.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}

			// Both orders, so the result does not depend on list order.
			for _, items := range [][]aimv1alpha1.AIMArtifact{{diskless, withDisk}, {withDisk, diskless}} {
				fetch := ProfileCacheFetchResult{
					profileCache: pc,
					profile:      controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
					artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
						Value: &aimv1alpha1.AIMArtifactList{Items: items},
					},
				}
				obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache]{Object: pc}, fetch)
				if got := obs.BestArtifacts["org/model-a"].Name; got != tt.want {
					t.Fatalf("resolved artifact = %q, want %q", got, tt.want)
				}
				if got := len(obs.AdapterDiskUpgrades) == 1; got != tt.wantUpgrade {
					t.Errorf("adapter disk upgrade planned = %v, want %v", got, tt.wantUpgrade)
				}
			}
		})
	}
}

// TestArtifactResolvableBy verifies the artifact watch filter accepts a diskless
// artifact a disk-requiring cache would upgrade in place, while still enforcing
// mode isolation.
func TestArtifactResolvableBy(t *testing.T) {
	pc := makeProfileCache("pc1", "my-profile", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, "")
	pc.Spec.RequiresAdapterDisk = true

	diskless := makeArtifact("diskless", "hf://org/model-a", "org/model-a", constants.AIMStatusReady, "")
	if ArtifactAdoptableBy(pc, &diskless) {
		t.Error("ArtifactAdoptableBy accepted a diskless artifact for a disk-requiring cache")
	}
	if !ArtifactResolvableBy(pc, &diskless) {
		t.Error("ArtifactResolvableBy rejected a diskless artifact the cache would upgrade in place")
	}

	owned := diskless.DeepCopy()
	owned.OwnerReferences = []metav1.OwnerReference{{UID: "someone-else"}}
	if ArtifactResolvableBy(pc, owned) {
		t.Error("ArtifactResolvableBy accepted an owned artifact for a Shared cache")
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
