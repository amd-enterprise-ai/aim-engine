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

package aimmodel

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

var errListNodesFailed = errors.New("list nodes failed")

const testBaseImageRef = "ghcr.io/silogen/aim-base:0.8.5"

func TestHasLegacyFields_DetectsLegacyCompatibilityFields(t *testing.T) {
	t.Parallel()

	spec := aimv1alpha1.AIMModelSpec{
		Image:                  "quay.io/amd/aim-qwen:0.10.0",
		RuntimeConfigRef:       aimv1alpha1.RuntimeConfigRef{Name: "fast"},
		DefaultServiceTemplate: "default-template",
		Discovery:              &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(true)},
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "org/model",
			SourceURI: "hf://org/model",
		}},
		ImageMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{CanonicalName: "org/model"},
		},
	}

	if !spec.HasLegacyFields() {
		t.Fatalf("HasLegacyFields() = false, want true for spec with legacy fields populated")
	}

	nativeSpec := aimv1alpha1.AIMModelSpec{
		Image: "quay.io/amd/aim-qwen:0.10.0",
		ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
			Selector: aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
		},
	}
	if nativeSpec.HasLegacyFields() {
		t.Fatalf("HasLegacyFields() = true, want false for image+profileCopy spec")
	}
}

func TestEnsureBaseImageBridge_PopulatesBothSides(t *testing.T) {
	t.Parallel()

	specWithBaseImageMetadata := aimv1alpha1.AIMModelSpec{
		ImageMetadata: &aimv1alpha1.ImageMetadata{BaseImageRef: testBaseImageRef},
	}
	status := &aimv1alpha1.AIMModelStatus{}
	EnsureBaseImageBridge(&specWithBaseImageMetadata, status)
	if status.BaseImage != testBaseImageRef {
		t.Fatalf("status.BaseImage = %q, want %q", status.BaseImage, testBaseImageRef)
	}
	if status.ImageMetadata == nil || status.ImageMetadata.BaseImageRef != testBaseImageRef {
		t.Fatalf("status.ImageMetadata.BaseImageRef = %#v, want carried through", status.ImageMetadata)
	}

	statusOnly := &aimv1alpha1.AIMModelStatus{BaseImage: testBaseImageRef}
	EnsureBaseImageBridge(&aimv1alpha1.AIMModelSpec{}, statusOnly)
	if statusOnly.ImageMetadata == nil || statusOnly.ImageMetadata.BaseImageRef != testBaseImageRef {
		t.Fatalf("status.ImageMetadata.BaseImageRef should mirror status.BaseImage, got %#v", statusOnly.ImageMetadata)
	}
}

func TestBuildDesiredProfileSet_UsesDiscoveryCacheForImageBackedCopy(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-finetuned-qwen",
			Namespace: "ml-team",
			UID:       "model-uid",
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "quay.io/amd/aim-qwen3-32b-instruct:0.10.0",
			ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
				Selector: aimv1alpha1.ProfileSelector{
					AimId:   "qwen/qwen3-32b",
					ModelId: "qwen/qwen3-32b-fp8",
				},
				VersionPolicy: aimv1alpha1.ProfileVersionPolicyPinned,
				Version:       "0.10.0",
				Image:         "amdenterpriseai/aim-base:0.10.0",
				Overrides: &aimv1alpha1.ProfileOverrides{
					ModelSources: []aimv1alpha1.AIMModelSource{{
						ModelID:   "my-org/qwen3-32b-fp8",
						SourceURI: "s3://bucket/fp8",
					}},
				},
			},
		},
	}

	cacheName, err := discoveryCacheName(model.Name)
	if err != nil {
		t.Fatalf("discoveryCacheName() error = %v", err)
	}
	ref := &aimv1alpha1.DiscoveryCacheReference{Name: cacheName, Namespace: "ml-team"}
	profileSetName, err := childProfileSetName(model.Name)
	if err != nil {
		t.Fatalf("childProfileSetName() error = %v", err)
	}
	desired := buildDesiredProfileSet(model, ref, profileSetName)
	if desired == nil {
		t.Fatal("buildDesiredProfileSet() = nil, want child AIMProfileSet")
	}
	if desired.Name != profileSetName {
		t.Fatalf("child set name = %q, want %q", desired.Name, profileSetName)
	}
	if desired.Spec.SourceRef == nil {
		t.Fatal("child set SourceRef = nil, want generated discovery cache ref")
	}
	if desired.Spec.SourceRef.Name != ref.Name {
		t.Fatalf("child set SourceRef = %#v, want %#v", desired.Spec.SourceRef, ref)
	}
	if desired.Spec.Image != "amdenterpriseai/aim-base:0.10.0" {
		t.Fatalf("child set spec.image = %q, want runtime image override", desired.Spec.Image)
	}
}

func TestBuildDesiredOfficialProfiles_MarksProfilesAsImageDerived(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage: "quay.io/amd/aim-base:0.10.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/Qwen3-32B",
				ModelId:   "qwen/Qwen3-32B",
				ProfileId: "tp2",
			},
		}},
	}

	desired, err := buildDesiredOfficialProfiles(model, catalog, nil, nil)
	if err != nil {
		t.Fatalf("buildDesiredOfficialProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}

	profile, ok := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMProfile", desired[0].Object)
	}
	if profile.Namespace != "team-a" {
		t.Fatalf("profile namespace = %q, want %q", profile.Namespace, "team-a")
	}
	wantName, err := utils.GenerateDerivedName(
		[]string{"qwen", "tp2"},
		utils.WithHashSource("qwen", "tp2"),
	)
	if err != nil {
		t.Fatalf("GenerateDerivedName() error = %v", err)
	}
	if profile.Name != wantName {
		t.Fatalf("profile name = %q, want %q", profile.Name, wantName)
	}
	if profile.Annotations[aimprofile.AnnotationProfileSource] != aimprofile.ProfileSourceImage {
		t.Fatalf("profile source = %q, want %q", profile.Annotations[aimprofile.AnnotationProfileSource], aimprofile.ProfileSourceImage)
	}
	if !aimprofile.IsProfileCopyable(profile.Annotations) {
		t.Fatal("image-derived profile should be marked copyable")
	}
}

func TestBuildDesiredOfficialProfiles_PrimaryFallbackFromRecommendedDeployments(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
	}
	specCommon := func(profileID, accel string, count int32, prec aimv1alpha1.AIMPrecision, metric aimv1alpha1.AIMMetric) aimv1alpha2.AIMProfileSpecCommon {
		return aimv1alpha2.AIMProfileSpecCommon{
			AimId:            "Qwen/Qwen3-32B",
			ModelId:          "Qwen/Qwen3-32B",
			ProfileId:        profileID,
			Engine:           "vllm",
			AcceleratorModel: accel,
			AcceleratorCount: count,
			Precision:        prec,
			Metric:           metric,
		}
	}

	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{
			{
				// Matches the recommended deployment by (gpu, count, precision, metric)
				// tuple and the YAML didn't stamp `primary` itself, so the fallback
				// should flip Primary to true.
				PrimaryExplicit: false,
				Spec:            specCommon("vllm-mi300x-fp16-tp1-latency", "MI300X", 1, "fp16", "latency"),
			},
			{
				// Same image, no matching recommended deployment for fp8 — fallback
				// leaves Primary at the catalog default (false).
				PrimaryExplicit: false,
				Spec:            specCommon("vllm-mi300x-fp8-tp1-latency", "MI300X", 1, "fp8", "latency"),
			},
			{
				// YAML explicitly set Primary=false (PrimaryExplicit=true). Even
				// though this spec matches a recommended deployment, the fallback
				// MUST NOT override an explicit value from the image author.
				PrimaryExplicit: true,
				Spec:            specCommon("vllm-mi300x-fp16-tp1-throughput", "MI300X", 1, "fp16", "throughput"),
			},
			{
				// Recommended deployment uses profileId — exact match on ProfileId
				// wins regardless of the other fields.
				PrimaryExplicit: false,
				Spec:            specCommon("vllm-mi300x-named-special", "MI300X", 1, "fp8", "latency"),
			},
		},
	}
	rds := []aimv1alpha1.RecommendedDeployment{
		{GPUModel: "MI300X", GPUCount: 1, Precision: "fp16", Metric: "latency"},
		{GPUModel: "MI300X", GPUCount: 1, Precision: "fp16", Metric: "throughput"},
		{ProfileId: "vllm-mi300x-named-special"},
	}
	// Provide a fake MI300X node so isSupportedProfile keeps every entry in
	// the desired set; otherwise the matchingNodes==0 filter trims them all
	// before the Primary fallback ever runs.
	nodes := []corev1.Node{{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n1",
			Labels: map[string]string{
				"feature.node.kubernetes.io/aim-accelerator.MI300X": "8",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"amd.com/gpu": resource.MustParse("8"),
			},
		},
	}}

	desired, err := buildDesiredOfficialProfiles(model, catalog, nodes, rds)
	if err != nil {
		t.Fatalf("buildDesiredOfficialProfiles() error = %v", err)
	}
	if len(desired) != 4 {
		t.Fatalf("len(desired) = %d, want 4", len(desired))
	}

	got := map[string]bool{}
	for _, d := range desired {
		profile := d.Object.(*aimv1alpha2.AIMProfile)
		got[profile.Spec.ProfileId] = profile.Spec.Primary
	}
	want := map[string]bool{
		"vllm-mi300x-fp16-tp1-latency":    true,
		"vllm-mi300x-fp8-tp1-latency":     false,
		"vllm-mi300x-fp16-tp1-throughput": false,
		"vllm-mi300x-named-special":       true,
	}
	for id, wantPrimary := range want {
		if got[id] != wantPrimary {
			t.Errorf("profile %q: Primary = %v, want %v", id, got[id], wantPrimary)
		}
	}
}

func TestMatchesRecommendedDeployment_TupleAndProfileIdMatching(t *testing.T) {
	t.Parallel()

	spec := aimv1alpha2.AIMProfileSpecCommon{
		ProfileId:        "vllm-mi300x-fp16-tp1-latency",
		AcceleratorModel: "MI300X",
		AcceleratorCount: 1,
		Precision:        "fp16",
		Metric:           "latency",
	}

	cases := []struct {
		name string
		rds  []aimv1alpha1.RecommendedDeployment
		want bool
	}{
		{"empty list", nil, false},
		{"exact tuple match", []aimv1alpha1.RecommendedDeployment{{GPUModel: "MI300X", GPUCount: 1, Precision: "fp16", Metric: "latency"}}, true},
		{"case-insensitive tuple", []aimv1alpha1.RecommendedDeployment{{GPUModel: "mi300x", GPUCount: 1, Precision: "FP16", Metric: "LATENCY"}}, true},
		{"partial wildcard (gpuModel only)", []aimv1alpha1.RecommendedDeployment{{GPUModel: "MI300X"}}, true},
		{"profileId exact match", []aimv1alpha1.RecommendedDeployment{{ProfileId: "vllm-mi300x-fp16-tp1-latency"}}, true},
		{"profileId mismatch wins over tuple", []aimv1alpha1.RecommendedDeployment{{ProfileId: "different-id", GPUModel: "MI300X", GPUCount: 1, Precision: "fp16", Metric: "latency"}}, false},
		{"precision mismatch", []aimv1alpha1.RecommendedDeployment{{GPUModel: "MI300X", GPUCount: 1, Precision: "fp8", Metric: "latency"}}, false},
		{"gpuCount mismatch", []aimv1alpha1.RecommendedDeployment{{GPUModel: "MI300X", GPUCount: 2, Precision: "fp16", Metric: "latency"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesRecommendedDeployment(spec, tc.rds); got != tc.want {
				t.Errorf("matchesRecommendedDeployment() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGeneratedChildNames_TruncateLongModelNamesWithStableHash(t *testing.T) {
	t.Parallel()

	modelName := "this-is-a-very-long-model-name-that-would-overflow-kubernetes-name-length-if-a-suffix-were-appended-directly"

	cacheName, err := discoveryCacheName(modelName)
	if err != nil {
		t.Fatalf("discoveryCacheName() error = %v", err)
	}
	if len(cacheName) > 63 {
		t.Fatalf("len(cacheName) = %d, want <= 63", len(cacheName))
	}

	profileSetName, err := childProfileSetName(modelName)
	if err != nil {
		t.Fatalf("childProfileSetName() error = %v", err)
	}
	if len(profileSetName) > 63 {
		t.Fatalf("len(profileSetName) = %d, want <= 63", len(profileSetName))
	}
	if cacheName == profileSetName {
		t.Fatalf("generated names collide: %q", cacheName)
	}
}

func TestSummarizeManagedProfiles_UsesDirectDerivativesOnly(t *testing.T) {
	t.Parallel()

	desired := []desiredProfile{
		{Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "managed-one", Namespace: "ml-team"}}},
		{Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "managed-two", Namespace: "ml-team"}}},
	}
	existing := []managedProfile{
		{
			Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "managed-one", Namespace: "ml-team"}},
			Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		},
		{
			Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "external-match", Namespace: "ml-team"}},
			Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusNotAvailable},
		},
	}

	summary, existingDesired := summarizeManagedProfiles(desired, existing)

	if summary.Total != 2 {
		t.Fatalf("Total = %d, want 2", summary.Total)
	}
	if summary.Ready != 1 {
		t.Fatalf("Ready = %d, want 1", summary.Ready)
	}
	if summary.NotAvailable != 0 {
		t.Fatalf("NotAvailable = %d, want 0 because external profiles must not be aggregated", summary.NotAvailable)
	}
	if existingDesired != 1 {
		t.Fatalf("ExistingDesiredCount = %d, want 1", existingDesired)
	}
}

func TestComposeState_NodeListFailurePreventsPruningManagedProfiles(t *testing.T) {
	t.Parallel()

	reconciler := &ModelReconciler{}
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: "quay.io/amd/aim-qwen:0.10.0"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		AimID: "qwen/qwen3-32b",
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			Name:    "tp2",
			Version: "0.10.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/qwen3-32b",
				ModelId:   "qwen/qwen3-32b-fp8",
				ProfileId: "tp2",
				Engine:    "vllm",
			},
		}},
	}
	fetch := ModelFetchResult{
		model: model,
		discovery: DiscoveryDecision{
			Catalog: &catalog,
			State:   &aimv1alpha1.ModelDiscoveryState{},
			Reason:  "DiscoveryCacheValid",
		},
		nodes: controllerutils.FetchResult[[]corev1.Node]{Error: errListNodesFailed},
		existingProfiles: controllerutils.FetchResult[[]managedProfile]{Value: []managedProfile{{
			Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "team-a"}},
		}}},
	}

	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, obs)

	if obs.BuildErr == nil {
		t.Fatal("BuildErr = nil, want infrastructure error when node listing fails")
	}
	if obs.pruneSafe {
		t.Fatal("pruneSafe = true, want false when desired profiles could not be resolved")
	}
	if len(plan.GetToDelete()) != 0 {
		t.Fatalf("deletes = %#v, want none when node inventory is unavailable", plan.GetToDelete())
	}
}

// TestPlanResources_DiscoveredProfilesCarryOwnerRefBranch pins the contract
// that namespace AIMModel image-discovery profiles flow through the
// ownerRef-bearing apply branch (PlanResult.toApply, returned by GetToApply),
// not the manual-ownerRef branch (toApplyWithoutOwnerRef). Without a
// controller ownerRef, SourceModelFromOwnerRefs returns nil and the
// AIMProfile reconciler races with this builder over the source-model
// labels — see internal/v1alpha2/aimprofile/provenance.go and the F14
// finding in .tmp/v1alpha2-validation.md. The cluster sibling
// (cluster_reconcile.go:289-291) already does this; this test prevents
// regressing the namespace half back to ApplyWithoutOwnerRef.
func TestPlanResources_DiscoveredProfilesCarryOwnerRefBranch(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: "quay.io/amd/aim-qwen:0.10.0"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		AimID: "qwen/qwen3-32b",
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			Name:    "tp2",
			Version: "0.10.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:        "qwen/qwen3-32b",
				ModelId:      "qwen/qwen3-32b-fp8",
				ProfileId:    "tp2",
				Engine:       "vllm",
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "hf://qwen/qwen3-32b-fp8"}},
			},
		}},
	}

	reconciler := &ModelReconciler{}
	fetch := ModelFetchResult{
		model: model,
		discovery: DiscoveryDecision{
			Catalog: &catalog,
			State:   &aimv1alpha1.ModelDiscoveryState{},
			Reason:  "DiscoveryCacheValid",
		},
		nodes:            controllerutils.FetchResult[[]corev1.Node]{Value: nil, Error: nil},
		existingProfiles: controllerutils.FetchResult[[]managedProfile]{Value: nil, Error: nil},
	}

	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, fetch)
	if len(obs.DesiredProfiles) != 1 {
		t.Fatalf("len(DesiredProfiles) = %d, want 1", len(obs.DesiredProfiles))
	}
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, obs)

	for _, obj := range plan.GetToApplyWithoutOwnerRef() {
		if _, ok := obj.(*aimv1alpha2.AIMProfile); ok {
			t.Fatalf("AIMProfile %s/%s landed in toApplyWithoutOwnerRef; F14 regressed: image-discovery profiles need a controller ownerRef so SourceModelFromOwnerRefs can resolve the producer", obj.GetNamespace(), obj.GetName())
		}
	}

	var profileApplyCount int
	for _, obj := range plan.GetToApply() {
		if _, ok := obj.(*aimv1alpha2.AIMProfile); ok {
			profileApplyCount++
		}
	}
	if profileApplyCount != 1 {
		t.Fatalf("AIMProfiles in toApply = %d, want 1 (the framework will stamp controller=true Kind=AIMModel during apply)", profileApplyCount)
	}
}

func TestBuildModelComponentHealth_PartialReadinessIsDegraded(t *testing.T) {
	t.Parallel()

	health := buildModelComponentHealth(ModelObservation{
		ModelFetchResult: ModelFetchResult{
			model: &aimv1alpha2.AIMModel{},
		},
		ManagedProfiles:      aimv1alpha1.ManagedProfileCounts{Total: 2, Ready: 1, NotAvailable: 1},
		ExistingDesiredCount: 2,
	})
	if len(health) != 1 {
		t.Fatalf("len(health) = %d, want 1", len(health))
	}
	if health[0].State != constants.AIMStatusDegraded {
		t.Fatalf("state = %q, want %q", health[0].State, constants.AIMStatusDegraded)
	}
}

func TestBuildModelComponentHealth_ProfileCopyZeroProfilesIsNotAvailable(t *testing.T) {
	t.Parallel()

	health := buildModelComponentHealth(ModelObservation{
		ModelFetchResult: ModelFetchResult{
			model: &aimv1alpha2.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{
					ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
						Selector: aimv1alpha1.ProfileSelector{AimId: "amd/example"},
					},
				},
			},
		},
		ManagedProfiles:      aimv1alpha1.ManagedProfileCounts{Total: 0, Ready: 0},
		ExistingDesiredCount: 1,
	})
	if len(health) != 1 {
		t.Fatalf("len(health) = %d, want 1", len(health))
	}
	if health[0].State != constants.AIMStatusNotAvailable {
		t.Fatalf("state = %q, want %q", health[0].State, constants.AIMStatusNotAvailable)
	}
}

func TestDerivationSpec_PreferDerivedFromOverProfileCopy(t *testing.T) {
	t.Parallel()

	derived := &aimv1alpha1.AIMProfileSetSpec{
		Selector: aimv1alpha1.ProfileSelector{AimId: "amd/derived"},
	}
	copy := &aimv1alpha1.AIMProfileSetSpec{
		Selector: aimv1alpha1.ProfileSelector{AimId: "amd/profile-copy"},
	}
	spec := &aimv1alpha1.AIMModelSpec{
		DerivedFrom: derived,
		ProfileCopy: copy,
	}
	got := derivationSpec(spec)
	if got != derived {
		t.Fatalf("derivationSpec() = %p, want spec.DerivedFrom %p (must win over ProfileCopy)", got, derived)
	}

	specCopyOnly := &aimv1alpha1.AIMModelSpec{ProfileCopy: copy}
	if got := derivationSpec(specCopyOnly); got != copy {
		t.Fatalf("derivationSpec() fallback = %p, want spec.ProfileCopy %p", got, copy)
	}

	if got := derivationSpec(&aimv1alpha1.AIMModelSpec{}); got != nil {
		t.Fatalf("derivationSpec(empty) = %p, want nil", got)
	}

	if got := derivationSpec(nil); got != nil {
		t.Fatalf("derivationSpec(nil) = %p, want nil", got)
	}
}

func TestBuildDesiredProfileSet_PrefersDerivedFromAndStampsSourceModelLabels(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-derived",
			Namespace: "team-a",
			UID:       "model-uid",
		},
		Spec: aimv1alpha1.AIMModelSpec{
			DerivedFrom: &aimv1alpha1.AIMProfileSetSpec{
				Selector:      aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
				VersionPolicy: aimv1alpha1.ProfileVersionPolicyPinned,
				Version:       "0.10.0",
			},
		},
	}

	profileSetName, err := childProfileSetName(model.Name)
	if err != nil {
		t.Fatalf("childProfileSetName() error = %v", err)
	}

	desired := buildDesiredProfileSet(model, nil, profileSetName)
	if desired == nil {
		t.Fatal("buildDesiredProfileSet() = nil, want child AIMProfileSet from DerivedFrom")
	}
	if desired.Spec.Selector.AimId != "qwen/qwen3-32b" {
		t.Fatalf("child set selector aimId = %q, want DerivedFrom selector", desired.Spec.Selector.AimId)
	}
	if got := desired.Labels[constants.LabelKeySourceModel]; got != model.Name {
		t.Fatalf("source-model label = %q, want %q", got, model.Name)
	}
	if got := desired.Labels[constants.LabelKeySourceModelScope]; got != constants.LabelValueSourceModelScopeNamespace {
		t.Fatalf("source-model-scope label = %q, want %q", got, constants.LabelValueSourceModelScopeNamespace)
	}
}

func TestBuildDesiredOfficialProfiles_StampsRoleAndOriginLabels(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage: "quay.io/amd/aim-base:0.10.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/Qwen3-32B",
				ModelId:   "qwen/Qwen3-32B",
				ProfileId: "tp2",
				Image:     "quay.io/amd/aim-qwen:0.10.0",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "qwen/Qwen3-32B",
					SourceURI: "hf://qwen/Qwen3-32B",
				}},
			},
		}},
	}

	desired, err := buildDesiredOfficialProfiles(model, catalog, nil, nil)
	if err != nil {
		t.Fatalf("buildDesiredOfficialProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile, ok := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMProfile", desired[0].Object)
	}
	if got := profile.Labels[constants.LabelKeyProfileRole]; got != constants.LabelValueProfileRoleDeployable {
		t.Fatalf("profile-role label = %q, want %q", got, constants.LabelValueProfileRoleDeployable)
	}
	if got := profile.Labels[constants.LabelKeyProfileOrigin]; got != string(aimv1alpha1.ProfileOriginDiscovered) {
		t.Fatalf("profile-origin label = %q, want %q", got, aimv1alpha1.ProfileOriginDiscovered)
	}
	if got := profile.Labels[constants.LabelKeySourceModel]; got != model.Name {
		t.Fatalf("source-model label = %q, want %q", got, model.Name)
	}
	if got := profile.Labels[constants.LabelKeySourceModelScope]; got != constants.LabelValueSourceModelScopeNamespace {
		t.Fatalf("source-model-scope label = %q, want %q", got, constants.LabelValueSourceModelScopeNamespace)
	}
}

// TestBuildDesiredOfficialProfiles_BaseEntriesStampedAsBase covers the
// iteration-2 base-image (custom-model source) producer path: a catalog item
// from a base image has no aimId / modelSources, so the materialised profile
// must carry the `base` role label (and remain non-deployable per
// IsProfileDeployable). Also asserts the base/deployable counts surfaced via
// summarizeManagedProfiles so the AIMModel status reflects the mix.
func TestBuildDesiredOfficialProfiles_BaseEntriesStampedAsBase(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-base", Namespace: "team-a", UID: "model-uid"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage: "ghcr.io/silogen/aim-base-vllm:0.1.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				ProfileId: "vllm-cpu-bf16-tp1-latency",
				Engine:    "vllm",
			},
		}},
	}

	desired, err := buildDesiredOfficialProfiles(model, catalog, nil, nil)
	if err != nil {
		t.Fatalf("buildDesiredOfficialProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile, ok := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMProfile", desired[0].Object)
	}
	if got := profile.Labels[constants.LabelKeyProfileRole]; got != constants.LabelValueProfileRoleBase {
		t.Fatalf("profile-role label = %q, want %q for base-profile entry", got, constants.LabelValueProfileRoleBase)
	}
	if got := profile.Labels[constants.LabelKeyProfileOrigin]; got != string(aimv1alpha1.ProfileOriginDiscovered) {
		t.Fatalf("profile-origin label = %q, want %q", got, aimv1alpha1.ProfileOriginDiscovered)
	}
	if aimprofile.IsProfileDeployable(profile.Spec.AIMProfileSpecCommon) {
		t.Fatal("base profile spec must not satisfy IsProfileDeployable")
	}

	summary, _ := summarizeManagedProfiles(desired, nil)
	if summary.Total != 1 {
		t.Fatalf("summary.Total = %d, want 1", summary.Total)
	}
	if summary.Base != 1 {
		t.Fatalf("summary.Base = %d, want 1 for base-only catalog", summary.Base)
	}
	if summary.Deployable != 0 {
		t.Fatalf("summary.Deployable = %d, want 0 for base-only catalog", summary.Deployable)
	}
}

// TestSummarizeManagedProfiles_CountsDeployableEntries asserts the
// deployable counterpart: a fully-formed AIMProfile (aimId + modelSources)
// shows up under Deployable and not Base.
func TestSummarizeManagedProfiles_CountsDeployableEntries(t *testing.T) {
	t.Parallel()

	desired := []desiredProfile{{
		Object: &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "team-a"},
			Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "qwen/Qwen3-32B",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "qwen/Qwen3-32B",
					SourceURI: "hf://qwen/Qwen3-32B",
				}},
			}},
		},
	}}

	summary, _ := summarizeManagedProfiles(desired, nil)
	if summary.Total != 1 {
		t.Fatalf("summary.Total = %d, want 1", summary.Total)
	}
	if summary.Deployable != 1 {
		t.Fatalf("summary.Deployable = %d, want 1 for deployable-only desired set", summary.Deployable)
	}
	if summary.Base != 0 {
		t.Fatalf("summary.Base = %d, want 0 for deployable-only desired set", summary.Base)
	}
}
