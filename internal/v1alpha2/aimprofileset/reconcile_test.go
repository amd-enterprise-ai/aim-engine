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
	"context"
	"encoding/json"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// TestManagedProfileCounts_ZeroSerializesExplicitly pins F7: zero-valued
// count fields must serialize as `0` rather than being omitted, so that
// chainsaw assertions like `managedProfiles: {total: 0, ready: 0}` can
// match against a status that genuinely observes zero managed profiles.
func TestManagedProfileCounts_ZeroSerializesExplicitly(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(aimv1alpha1.ManagedProfileCounts{})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"total":0,"ready":0,"notAvailable":0,"deployable":0,"base":0}`
	if string(out) != want {
		t.Fatalf("ManagedProfileCounts{} JSON = %s, want %s", string(out), want)
	}
}

func TestLoadNamespaceCandidates_UsesConfigMapSourceRef(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	profileYAML := `model_id: qwen/Qwen3-32B
metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: standard
  accelerator_model: MI300X
  accelerator_count: 2
`
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-discovery", Namespace: "team-a"},
		Data: map[string]string{
			aimprofile.FlattenProfilePath("qwen/Qwen3-32B/mi300x-tp2.yaml"): profileYAML,
			aimprofile.DiscoveryCacheMetadataKey:                            `{"aimId":"qwen/Qwen3-32B","sourceImage":"quay.io/amd/aim-qwen:0.9.0"}`,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-profiles", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			SourceRef: &aimv1alpha1.ProfileSourceRef{
				Name: "qwen-discovery",
			},
			Selector: aimv1alpha1.ProfileSelector{AimId: "qwen/Qwen3-32B"},
		},
	}

	candidates, err := loadNamespaceCandidates(context.Background(), fakeClient, set)
	if err != nil {
		t.Fatalf("loadNamespaceCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].Spec.ProfileId != "mi300x-tp2" {
		t.Fatalf("candidates = %#v, want one configmap-backed candidate", candidates)
	}
	if candidates[0].Spec.Image != "quay.io/amd/aim-qwen:0.9.0" {
		t.Fatalf("candidate image = %q, want source image from metadata", candidates[0].Spec.Image)
	}
}

func TestLoadNamespaceCandidates_UsesNamespaceProfilesOnly(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "shared"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "tp2",
			Image:     "quay.io/amd/aim-cluster:0.9.0",
		}},
		Status: aimv1alpha2.AIMProfileStatus{Version: "0.9.0"},
	}
	namespaceProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "shared",
			Namespace:   "team-a",
			Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "tp2",
			Image:     "quay.io/custom/aim-namespace:0.9.0",
		}},
		Status: aimv1alpha2.AIMProfileStatus{Version: "0.9.0"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(clusterProfile, namespaceProfile).Build()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-profiles", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMProfileSetSpec{},
	}

	candidates, err := loadNamespaceCandidates(context.Background(), fakeClient, set)
	if err != nil {
		t.Fatalf("loadNamespaceCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want only the namespace-scoped candidate", len(candidates))
	}
	if candidates[0].Spec.Image != "quay.io/custom/aim-namespace:0.9.0" {
		t.Fatalf("candidate image = %q, want namespace profile", candidates[0].Spec.Image)
	}
}

func TestProfileSetCandidateLoading_RequiresExplicitCopyOptIn(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	namespaceCopyable := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "namespace-copyable",
			Namespace:   "team-a",
			Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "ns-copyable",
		}},
	}
	namespaceOriginal := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-original", Namespace: "team-a"},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "ns-original",
		}},
	}
	namespaceCopied := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "namespace-copied",
			Namespace:   "team-a",
			Annotations: map[string]string{aimprofile.AnnotationProfileSource: aimprofile.ProfileSourceCopy},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "ns-copy",
		}},
	}
	clusterCopyable := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster-copyable",
			Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"},
		},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "cluster-copyable",
		}},
	}
	clusterOriginal := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-original"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "cluster-original",
		}},
	}
	clusterCopied := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster-copied",
			Annotations: map[string]string{aimprofile.AnnotationProfileSource: aimprofile.ProfileSourceCopy},
		},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "cluster-copy",
		}},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(namespaceCopyable, namespaceOriginal, namespaceCopied, clusterCopyable, clusterOriginal, clusterCopied).
		Build()

	namespaceSet := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-profiles", Namespace: "team-a"},
	}
	namespaceCandidates, err := loadNamespaceCandidates(context.Background(), fakeClient, namespaceSet)
	if err != nil {
		t.Fatalf("loadNamespaceCandidates() error = %v", err)
	}
	if len(namespaceCandidates) != 1 || namespaceCandidates[0].Spec.ProfileId != "ns-copyable" {
		t.Fatalf("namespace candidates = %#v, want only explicitly copyable namespace profile", namespaceCandidates)
	}

	clusterSet := &aimv1alpha2.AIMClusterProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cluster-profiles"},
	}
	clusterCandidates, err := loadClusterCandidates(context.Background(), fakeClient, clusterSet)
	if err != nil {
		t.Fatalf("loadClusterCandidates() error = %v", err)
	}
	if len(clusterCandidates) != 1 || clusterCandidates[0].Spec.ProfileId != "cluster-copyable" {
		t.Fatalf("cluster candidates = %#v, want only explicitly copyable cluster profile", clusterCandidates)
	}
}

func TestPlanResources_SkipsDeletesWhenCandidatesFailToLoad(t *testing.T) {
	t.Parallel()

	reconciler := &ProfileSetReconciler{}
	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "profiles", Namespace: "team-a", UID: "set-uid"},
	}
	fetch := ProfileSetFetchResult{
		set:        set,
		candidates: controllerutils.FetchResult[[]aimprofile.ProfileCopyCandidate]{Error: errors.New("catalog unavailable")},
		managed: controllerutils.FetchResult[[]managedProfile]{Value: []managedProfile{{
			Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: "team-a"}},
		}}},
	}

	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}, obs)

	if obs.pruneSafe {
		t.Fatal("pruneSafe = true, want false when desired state is unknown")
	}
	if len(plan.GetToDelete()) != 0 {
		t.Fatalf("deletes = %#v, want none when candidate load fails", plan.GetToDelete())
	}
	if len(obs.componentHealth) != 1 || len(obs.componentHealth[0].Errors) != 1 {
		t.Fatalf("componentHealth = %#v, want one actionable fetch error", obs.componentHealth)
	}
}

func TestBuildDesiredNamespaceProfiles_MarksCopiesWithProfileSource(t *testing.T) {
	t.Parallel()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-profiles", Namespace: "team-a", UID: "set-uid"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
		},
	}
	desired, err := buildDesiredNamespaceProfiles(set, []aimprofile.ProfileCopyCandidate{{
		Name: "source",
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/Qwen3-32B",
			ModelId:   "qwen/Qwen3-32B",
			ProfileId: "tp2",
		},
	}})
	if err != nil {
		t.Fatalf("buildDesiredNamespaceProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}

	profile, ok := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMProfile", desired[0].Object)
	}
	wantName, err := utils.GenerateDerivedName(
		[]string{set.Name, "tp2"},
		utils.WithHashSource(set.Name, candidateIdentity(aimprofile.ProfileCopyCandidate{
			Name: "source",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/Qwen3-32B",
				ModelId:   "qwen/Qwen3-32B",
				ProfileId: "tp2",
			},
		})),
	)
	if err != nil {
		t.Fatalf("GenerateDerivedName() error = %v", err)
	}
	if profile.Name != wantName {
		t.Fatalf("profile name = %q, want %q", profile.Name, wantName)
	}
	if profile.Annotations[aimprofile.AnnotationProfileSource] != aimprofile.ProfileSourceCopy {
		t.Fatalf("profile source = %q, want %q", profile.Annotations[aimprofile.AnnotationProfileSource], aimprofile.ProfileSourceCopy)
	}
	if aimprofile.IsProfileCopyable(profile.Annotations) {
		t.Fatal("derived copy unexpectedly marked copyable")
	}
}

// TestBuildDesiredNamespaceProfiles_BaseRoleStampsIdentityFromOverrides
// covers the custom-model derivation contract end-to-end at the
// reconciler layer: a Base-role selector against an empty-identity
// base-profile candidate must produce a derived AIMProfile that has
// overrides.aimId/modelId stamped onto spec, modelSources from
// overrides, and is marked deployable. Without this, derived profiles
// fail the deployable-role CEL invariant and AIMService resolution
// can't reach Ready.
//
// Note the API shape: selector identity fields are EMPTY (CEL rejects
// them when role=base on the spec types). Target identity travels via
// overrides.{aimId,modelId}.
func TestBuildDesiredNamespaceProfiles_BaseRoleStampsIdentityFromOverrides(t *testing.T) {
	t.Parallel()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-derived", Namespace: "team-a", UID: "set-uid"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
			Selector: aimv1alpha1.ProfileSelector{
				Role: aimv1alpha1.ProfileSelectorRoleBase,
			},
			Overrides: &aimv1alpha1.ProfileOverrides{
				AimId:   "acme/custom-transformer",
				ModelId: "acme/custom-transformer",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "acme/custom-transformer",
					SourceURI: "s3://acme-models/custom-transformer",
				}},
			},
		},
	}

	baseProfile := aimprofile.ProfileCopyCandidate{
		Name: "custom-base-vllm-mi300x",
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			Engine:           "vllm",
			AcceleratorModel: "MI300X",
			AcceleratorCount: 1,
		},
	}

	desired, err := buildDesiredNamespaceProfiles(set, []aimprofile.ProfileCopyCandidate{baseProfile})
	if err != nil {
		t.Fatalf("buildDesiredNamespaceProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1 derived profile from the base profile", len(desired))
	}
	profile, ok := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMProfile", desired[0].Object)
	}
	if profile.Spec.AimId != "acme/custom-transformer" {
		t.Fatalf("derived.spec.aimId = %q, want overrides.aimId stamped onto base-profile-derived spec", profile.Spec.AimId)
	}
	if profile.Spec.ModelId != "acme/custom-transformer" {
		t.Fatalf("derived.spec.modelId = %q, want overrides.modelId stamped onto base-profile-derived spec", profile.Spec.ModelId)
	}
	if !aimprofile.IsProfileDeployable(profile.Spec.AIMProfileSpecCommon) {
		t.Fatalf("derived profile must be deployable; spec = %#v", profile.Spec.AIMProfileSpecCommon)
	}
}

func TestMergeCandidates_KeepsDistinctTypesInDeterministicOrder(t *testing.T) {
	t.Parallel()

	cluster := aimprofile.ProfileCopyCandidate{
		Name: "shared",
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "qwen/qwen3-32b",
			ModelId:   "qwen/qwen3-32b",
			ProfileId: "tp2",
			Engine:    "vllm",
			Type:      aimv1alpha1.AIMProfileTypePreview,
		},
		Status: aimv1alpha2.AIMProfileStatus{Version: "0.9.0"},
	}
	namespace := cluster
	namespace.Spec.Type = aimv1alpha1.AIMProfileTypeOptimized

	merged := mergeCandidates([]aimprofile.ProfileCopyCandidate{cluster}, []aimprofile.ProfileCopyCandidate{namespace})

	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2 distinct candidates", len(merged))
	}
	if merged[0].Spec.Type != aimv1alpha1.AIMProfileTypeOptimized || merged[1].Spec.Type != aimv1alpha1.AIMProfileTypePreview {
		t.Fatalf("merged order = %#v, want deterministic type ordering", merged)
	}
}

func TestBuildComponentHealth_PartialReadinessIsDegraded(t *testing.T) {
	t.Parallel()

	health := buildComponentHealth(nil, nil, aimv1alpha1.ManagedProfileCounts{
		Total:        2,
		Ready:        1,
		NotAvailable: 1,
	}, 2)
	if len(health) != 1 {
		t.Fatalf("len(health) = %d, want 1", len(health))
	}
	if health[0].State != constants.AIMStatusDegraded {
		t.Fatalf("state = %q, want %q", health[0].State, constants.AIMStatusDegraded)
	}
}

func TestLoadNamespaceCandidates_HonoursRoleAndModelRefLabels(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	// match: deployable + correct source-model
	match := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "match-profile",
			Namespace: "team-a",
			Annotations: map[string]string{
				aimprofile.AnnotationProfileCopyable: "true",
			},
			Labels: map[string]string{
				constants.LabelKeyProfileRole:      constants.LabelValueProfileRoleDeployable,
				constants.LabelKeySourceModel:      "owner-model",
				constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
			},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "selector-org/selector-model",
			ModelId:   "selector-org/selector-model",
			ProfileId: "match-profile",
			Image:     "quay.io/amd/aim-base:0.9.0",
		}},
		Status: aimv1alpha2.AIMProfileStatus{Version: "0.9.0"},
	}
	// no match: same aimId but different source-model
	otherSource := match.DeepCopy()
	otherSource.Name = "other-source-profile"
	otherSource.Labels[constants.LabelKeySourceModel] = "different-owner"
	// no match: explicitly base-labelled
	baseLabel := match.DeepCopy()
	baseLabel.Name = "base-profile"
	baseLabel.Labels[constants.LabelKeyProfileRole] = constants.LabelValueProfileRoleBase

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(match, otherSource, baseLabel).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
			return []string{obj.(*aimv1alpha2.AIMProfile).Spec.AimId}
		}).
		Build()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "selector-set", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			Selector: aimv1alpha1.ProfileSelector{
				AimId: "selector-org/selector-model",
				ModelRef: &aimv1alpha1.ProfileSelectorModelRef{
					Name:  "owner-model",
					Scope: aimv1alpha1.ProfileSelectorScopeNamespace,
				},
			},
		},
	}

	candidates, err := loadNamespaceCandidates(context.Background(), fakeClient, set)
	if err != nil {
		t.Fatalf("loadNamespaceCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d (%v), want exactly the deployable+owner-matching profile", len(candidates), candidates)
	}
	if candidates[0].Name != match.Name {
		t.Fatalf("candidate = %q, want %q", candidates[0].Name, match.Name)
	}
}

func TestLoadNamespaceCandidates_BaseRoleMatchesNothingInIteration1(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	deployable := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "deployable-profile",
			Namespace:   "team-a",
			Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"},
			Labels: map[string]string{
				constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable,
			},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId:     "selector-org/selector-model",
			ModelId:   "selector-org/selector-model",
			ProfileId: "p",
			Image:     "quay.io/amd/aim-base:0.9.0",
		}},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deployable).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
			return []string{obj.(*aimv1alpha2.AIMProfile).Spec.AimId}
		}).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
			return []string{obj.(*aimv1alpha2.AIMClusterProfile).Spec.AimId}
		}).
		Build()

	set := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "base-set", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			Selector: aimv1alpha1.ProfileSelector{
				AimId: "selector-org/selector-model",
				Role:  aimv1alpha1.ProfileSelectorRoleBase,
			},
		},
	}
	candidates, err := loadNamespaceCandidates(context.Background(), fakeClient, set)
	if err != nil {
		t.Fatalf("loadNamespaceCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("len(candidates) = %d, want 0 (no profile is labelled base in iteration 1)", len(candidates))
	}
}
