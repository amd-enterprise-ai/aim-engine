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

package templatemigration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	testAIMProfileKind = "AIMProfile"
	testNamespace      = "team-a"
)

func TestPlan_ExactInlineCustomProfileWithMigrationSignalIsAutomatic(t *testing.T) {
	fixture := newPlanFixture("exact")
	result := runFixturePlan(t, fixture)

	if len(result.Document.Units) != 1 {
		t.Fatalf("units = %d, want 1", len(result.Document.Units))
	}
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic {
		t.Fatalf("outcome = %q, want automatic; manual outcomes: %#v", unit.Outcome, unit.ManualOutcomes)
	}
	if unit.TargetProfile == nil ||
		unit.TargetProfile.Kind != testAIMProfileKind ||
		unit.TargetProfile.Namespace != testNamespace ||
		unit.TargetProfile.Name != fixture.template.Name {
		t.Fatalf("target profile = %#v, want retained namespace/name", unit.TargetProfile)
	}
	if len(unit.ProposedOperations) != 4 {
		t.Fatalf("operations = %#v, want profile, cache, artifact protection, and service operations", unit.ProposedOperations)
	}
	if unit.ProposedOperations[0].Action != OperationCreate ||
		unit.ProposedOperations[0].Resource.Kind != testAIMProfileKind {
		t.Fatalf("first operation = %#v, want AIMProfile create", unit.ProposedOperations[0])
	}
	annotations := nestedMap(t, unit.ProposedOperations[0].Object, "metadata", "annotations")
	if annotations[annotationMigrationSourceKind] != resourceKindServiceTemplate ||
		annotations[annotationMigrationSourceNamespace] != testNamespace ||
		annotations[annotationMigrationSourceName] != fixture.template.Name ||
		annotations[annotationMigrationSourceUID] != string(fixture.template.UID) {
		t.Fatalf("profile provenance = %#v", annotations)
	}
	if annotations[profileYAMLContractAnnotation()] == "" {
		t.Fatal("profile YAML contract annotation is missing")
	}
	protectionPatch := unit.ProposedOperations[2].Patch
	protectionAnnotations := nestedMap(t, protectionPatch, "metadata", "annotations")
	if protectionAnnotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] !=
		aimv1alpha1.ArtifactEvictionProtectedValue {
		t.Fatalf("artifact protection patch = %#v", protectionAnnotations)
	}
	servicePatch := unit.ProposedOperations[3].Patch
	patchAnnotations := nestedMap(t, servicePatch, "metadata", "annotations")
	if patchAnnotations[constants.AnnotationReconcilerPipeline] != constants.ReconcilerPipelineProfile {
		t.Fatalf("service patch pipeline annotation = %#v", patchAnnotations)
	}
	if patchAnnotations[constants.AnnotationMigrationPreserveInferenceServiceGeneration] != "2" {
		t.Fatalf("service patch no-rollout generation annotation = %#v", patchAnnotations)
	}
	if !hasImputation(unit, "spec.engine", "legacy custom-profile renderer") ||
		!hasImputation(unit, "spec.acceleratorPartitioningMode", "AIMProfile API default") ||
		!hasImputation(unit, "spec.image", "resolved model spec.image") {
		t.Fatalf("imputations do not record required translations: %#v", unit.Imputations)
	}
	if summary := result.Summary(); summary != (Summary{Services: 1, Automatic: 1}) {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestPlan_ReducedRuntimeProjectionIsManual(t *testing.T) {
	fixture := newPlanFixture("reduced")
	result := runPlanWithOptions(t, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeReduced,
	}, fixture.objects()...)
	unit := result.Document.Units[0]
	if result.Document.RuntimeProjectionMode != string(aimv1alpha2.RuntimeProjectionModeReduced) ||
		unit.Outcome != OutcomeManual ||
		!hasManualCode(&unit, "ReducedRuntimeProjectionUnsupported") ||
		len(unit.ProposedOperations) != 0 {
		t.Fatalf("Reduced projection unit = %#v", unit)
	}
}

func TestPlan_RequiresExplicitRuntimeProjectionMode(t *testing.T) {
	_, err := Plan(
		context.Background(),
		fake.NewClientBuilder().WithScheme(testScheme(t)).Build(),
		PlanOptions{Namespaces: []string{testNamespace}},
	)
	if err == nil || !strings.Contains(err.Error(), "must explicitly match") {
		t.Fatalf("Plan() error = %v, want explicit projection-mode requirement", err)
	}
}

func TestPlan_SelectedNamespaceInventoryFailureReturnsWrappedError(t *testing.T) {
	const namespace = "inventory-failure"
	sentinelErr := errors.New("fixture list failure")
	reader := &listErrorReader{
		Reader: fake.NewClientBuilder().WithScheme(testScheme(t)).Build(),
		err:    sentinelErr,
	}

	result, err := Plan(context.Background(), reader, PlanOptions{
		Namespaces:            []string{namespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
	})
	if result != nil {
		t.Fatalf("Plan() result = %#v, want nil", result)
	}
	if err == nil || !strings.Contains(err.Error(), namespace) {
		t.Fatalf("Plan() error = %v, want selected namespace %q", err, namespace)
	}
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("Plan() error = %v, want wrapped sentinel error", err)
	}
}

func TestPlan_DedicatedArtifactHandoffRequiresManualQuiescence(t *testing.T) {
	fixture := newPlanFixture("dedicated")
	configureDedicatedPlanFixture(fixture)
	result := runFixturePlan(t, fixture)
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeManual ||
		!hasManualCode(&unit, "DedicatedHandoffRequiresQuiescence") ||
		len(unit.ProposedOperations) != 0 {
		t.Fatalf("Dedicated unit was not fail-closed manual: %#v", unit)
	}

	fixture.artifact.OwnerReferences = append(
		fixture.artifact.OwnerReferences,
		metav1.OwnerReference{UID: "unexpected-owner"},
	)
	result = runFixturePlan(t, fixture)
	unit = result.Document.Units[0]
	if unit.Outcome != OutcomeManual || !hasManualCode(&unit, "UnsupportedArtifactOwnership") {
		t.Fatalf("extra Dedicated artifact owner was not rejected: %#v", unit)
	}
}

func configureDedicatedPlanFixture(fixture *planFixture) {
	fixture.service.Spec.Caching.Mode = aimv1alpha1.CachingModeDedicated
	fixture.cache.Spec.Mode = aimv1alpha1.TemplateCacheModeDedicated
	fixture.cache.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         aimv1alpha2.GroupVersion.String(),
		Kind:               "AIMService",
		Name:               fixture.service.Name,
		UID:                fixture.service.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}}
	fixture.artifact.Status.Mode = aimv1alpha1.ArtifactModeDedicated
	fixture.artifact.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         aimv1alpha1.GroupVersion.String(),
		Kind:               resourceKindTemplateCache,
		Name:               fixture.cache.Name,
		UID:                fixture.cache.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}}
	fixture.claim.OwnerReferences[0].Controller = ptr.To(true)
	fixture.claim.OwnerReferences[0].BlockOwnerDeletion = ptr.To(true)
}

func TestPlan_SharedCacheAuthorityMustBeUniqueAndServiceDriven(t *testing.T) {
	t.Run("profile-managed cache is manual", func(t *testing.T) {
		fixture := newPlanFixture("profile-cache")
		fixture.template.Spec.Caching = &aimv1alpha1.AIMTemplateCachingConfig{Enabled: true}

		result := runFixturePlan(t, fixture)
		unit := result.Document.Units[0]
		if unit.Outcome != OutcomeManual ||
			!hasManualCode(&unit, "ProfileManagedSharedCache") {
			t.Fatalf("profile-managed Shared cache was not manual: %#v", unit)
		}
	})

	t.Run("second matching cache is manual", func(t *testing.T) {
		fixture := newPlanFixture("competing-cache")
		competing := &aimv1alpha2.AIMProfileCache{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-shared-cache",
				Namespace: testNamespace,
			},
			Spec: aimv1alpha2.AIMProfileCacheSpec{
				ProfileName:  fixture.template.Name,
				ProfileScope: aimv1alpha1.AIMResolutionScopeNamespace,
				Mode:         aimv1alpha2.ProfileCacheModeShared,
			},
		}

		result := runPlanWithObjects(t, append(fixture.objects(), competing)...)
		unit := result.Document.Units[0]
		if unit.Outcome != OutcomeManual ||
			!hasManualCode(&unit, "SharedCacheAuthorityAmbiguous") {
			t.Fatalf("competing Shared cache was not manual: %#v", unit)
		}
	})
}

func TestPlan_ManualClassifications(t *testing.T) {
	tests := []struct {
		name string
		edit func(*planFixture)
		code string
	}{
		{
			name: "service overrides",
			edit: func(fixture *planFixture) {
				fixture.service.Spec.Overrides = &aimv1alpha1.AIMServiceOverrides{}
			},
			code: "LegacyServiceOverrides",
		},
		{
			name: "static adapters",
			edit: func(fixture *planFixture) {
				fixture.service.Spec.Adapters = []aimv1alpha1.AIMServiceAdapterReference{{
					Name: "adapter",
					Kind: aimv1alpha1.AdapterKindAIMArtifact,
				}}
			},
			code: "AdapterMigrationUnsupported",
		},
		{
			name: "dynamic adapters with empty list",
			edit: func(fixture *planFixture) {
				fixture.service.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
			},
			code: "AdapterMigrationUnsupported",
		},
		{
			name: "declared GitOps management",
			edit: func(fixture *planFixture) {
				fixture.service.Annotations = map[string]string{"argocd.argoproj.io/tracking-id": "fixture"}
			},
			code: "DeclaredDesiredStateManagement",
		},
		{
			name: "lossy discovered profile",
			edit: func(fixture *planFixture) {
				fixture.template.Spec.CustomProfile = nil
			},
			code: "LossyDiscoveredStatus",
		},
		{
			name: "non Ready artifact",
			edit: func(fixture *planFixture) {
				fixture.artifact.Status.Status = constants.AIMStatusProgressing
			},
			code: "ArtifactNotReady",
		},
		{
			name: "changed template identity",
			edit: func(fixture *planFixture) {
				fixture.service.Status.ResolvedTemplate.UID = types.UID("old-template")
			},
			code: "TemplateIdentityChanged",
		},
		{
			name: "secret literal",
			edit: func(fixture *planFixture) {
				fixture.template.Spec.Env = []corev1.EnvVar{{Name: "HF_TOKEN", Value: "fixture-token"}}
			},
			code: "SecretLiteral",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlanFixture(test.name)
			test.edit(fixture)
			result := runFixturePlan(t, fixture)
			unit := result.Document.Units[0]
			if unit.Outcome != OutcomeManual || !hasManualCode(&unit, test.code) {
				t.Fatalf("unit = %#v, want manual code %s", unit, test.code)
			}
			if len(unit.ProposedOperations) != 0 {
				t.Fatalf("manual unit proposed operations: %#v", unit.ProposedOperations)
			}
		})
	}
}

func TestPlan_ExactNativeProfileReuseRequiresProvenance(t *testing.T) {
	fixture := newPlanFixture("reuse")
	first := runFixturePlan(t, fixture)
	profileOperation := findKindOperation(first.Document.Units[0], testAIMProfileKind, OperationCreate)
	if profileOperation == nil {
		t.Fatal("initial plan has no AIMProfile create")
	}

	encoded, err := json.Marshal(profileOperation.Object)
	if err != nil {
		t.Fatal(err)
	}
	existing := &aimv1alpha2.AIMProfile{}
	if err := json.Unmarshal(encoded, existing); err != nil {
		t.Fatal(err)
	}
	existing.UID = types.UID("profile-uid")
	existing.ResourceVersion = "7"
	existing.Generation = 1
	existing.Status = aimv1alpha2.AIMProfileStatus{
		ObservedGeneration: 1,
		Status:             constants.AIMStatusReady,
	}

	objects := fixture.objects()
	objects = append(objects, existing)
	result := runPlanWithObjects(t, objects...)
	unit := result.Document.Units[0]
	reuse := findKindOperation(unit, testAIMProfileKind, OperationReuse)
	if unit.Outcome != OutcomeAutomatic ||
		reuse == nil ||
		unit.TargetProfile.UID != string(existing.UID) {
		t.Fatalf("reuse unit = %#v", unit)
	}
	if unit.ProposedOperations[0].Action != OperationReuse ||
		unit.ProposedOperations[0].Resource.Kind != testAIMProfileKind {
		t.Fatalf("profile reuse is not the first operation: %#v", unit.ProposedOperations)
	}

	existing.Annotations = map[string]string{}
	result = runPlanWithObjects(t, objects...)
	unit = result.Document.Units[0]
	if unit.Outcome != OutcomeManual || !hasManualCode(&unit, "TargetProfileConflict") {
		t.Fatalf("missing provenance unit = %#v", unit)
	}
}

func TestPlan_LossyDiscoveredTemplateReusesExactNativeProfile(t *testing.T) {
	fixture := newPlanFixture("native-reuse")
	first := runFixturePlan(t, fixture)
	profileOperation := findKindOperation(first.Document.Units[0], testAIMProfileKind, OperationCreate)
	if profileOperation == nil {
		t.Fatal("initial plan has no AIMProfile create")
	}
	encoded, err := json.Marshal(profileOperation.Object)
	if err != nil {
		t.Fatal(err)
	}
	existing := &aimv1alpha2.AIMProfile{}
	if err := json.Unmarshal(encoded, existing); err != nil {
		t.Fatal(err)
	}
	existing.UID = types.UID("native-profile")
	existing.ResourceVersion = "8"
	existing.Generation = 1
	for _, key := range []string{
		annotationMigrationSourceKind,
		annotationMigrationSourceNamespace,
		annotationMigrationSourceName,
		annotationMigrationSourceUID,
	} {
		delete(existing.Annotations, key)
	}
	existing.Labels = map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
	}
	existing.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMModel",
		Name:       fixture.model.Name,
		UID:        fixture.model.UID,
	}}
	existing.Status = aimv1alpha2.AIMProfileStatus{
		ObservedGeneration: 1,
		Status:             constants.AIMStatusReady,
		Origin:             aimv1alpha1.ProfileOriginDiscovered,
		SourceModel: &aimv1alpha2.ProfileSourceModel{
			Name:      fixture.model.Name,
			Namespace: fixture.model.Namespace,
			Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
		},
	}

	fixture.template.Spec.CustomProfile = nil
	fixture.template.Spec.ProfileId = existing.Spec.ProfileId
	fixture.template.Status.Profile = &aimv1alpha1.AIMDiscoveredProfile{
		EngineArgs: existing.Spec.EngineArgs.DeepCopy(),
		EnvVars:    copyStringMap(existing.Spec.EngineEnv),
		Metadata: aimv1alpha1.AIMProfileMetadata{
			AimID:     existing.Spec.AimId,
			ModelID:   existing.Spec.ModelId,
			Engine:    existing.Spec.Engine,
			GPU:       existing.Spec.AcceleratorModel,
			GPUCount:  existing.Spec.AcceleratorCount,
			Metric:    existing.Spec.Metric,
			Precision: existing.Spec.Precision,
			Type:      existing.Spec.Type,
		},
	}

	result := runPlanWithObjects(t, append(fixture.objects(), existing)...)
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic ||
		findKindOperation(unit, testAIMProfileKind, OperationReuse) == nil {
		t.Fatalf("native reuse unit = %#v", unit)
	}

	existing.OwnerReferences = nil
	result = runPlanWithObjects(t, append(fixture.objects(), existing)...)
	unit = result.Document.Units[0]
	if unit.Outcome != OutcomeManual || !hasManualCode(&unit, "TargetProfileConflict") {
		t.Fatalf("unproven native profile unit = %#v", unit)
	}
}

func TestPlan_AutoSelectedImageServiceReusesUniqueGeneratedNativeProfile(t *testing.T) {
	fixture := newPlanFixture("live-auto-selection")
	first := runFixturePlan(t, fixture)
	profileOperation := findKindOperation(first.Document.Units[0], testAIMProfileKind, OperationCreate)
	if profileOperation == nil {
		t.Fatal("initial plan has no AIMProfile create")
	}
	encoded, err := json.Marshal(profileOperation.Object)
	if err != nil {
		t.Fatal(err)
	}
	existing := &aimv1alpha2.AIMProfile{}
	if err := json.Unmarshal(encoded, existing); err != nil {
		t.Fatal(err)
	}
	existing.Name = "generated-native-profile"
	existing.UID = types.UID("generated-native-profile")
	existing.ResourceVersion = "8"
	existing.Generation = 1
	for _, key := range []string{
		annotationMigrationSourceKind,
		annotationMigrationSourceNamespace,
		annotationMigrationSourceName,
		annotationMigrationSourceUID,
	} {
		delete(existing.Annotations, key)
	}
	existing.Labels = map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
	}
	existing.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMModel",
		Name:       fixture.model.Name,
		UID:        fixture.model.UID,
	}}
	existing.Status = aimv1alpha2.AIMProfileStatus{
		ObservedGeneration: 1,
		Status:             constants.AIMStatusReady,
		Origin:             aimv1alpha1.ProfileOriginDiscovered,
		SourceModel: &aimv1alpha2.ProfileSourceModel{
			Name:      fixture.model.Name,
			Namespace: fixture.model.Namespace,
			Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
		},
	}
	existing.Spec.AcceleratorVendor = ""

	image := fixture.model.Spec.Image
	fixture.service.Spec.Model = &aimv1alpha1.AIMServiceModel{Image: &image}
	fixture.service.Spec.Template = nil
	fixture.template.Spec.CustomProfile = nil
	fixture.template.Spec.ProfileId = ""
	fixture.template.Spec.ModelSources = nil
	fixture.template.Status.Profile = &aimv1alpha1.AIMDiscoveredProfile{
		EngineArgs: existing.Spec.EngineArgs.DeepCopy(),
		EnvVars:    copyStringMap(existing.Spec.EngineEnv),
		Metadata: aimv1alpha1.AIMProfileMetadata{
			AimID:     existing.Spec.AimId,
			ModelID:   existing.Spec.ModelId,
			Engine:    existing.Spec.Engine,
			GPU:       existing.Spec.AcceleratorModel,
			GPUCount:  existing.Spec.AcceleratorCount,
			Metric:    existing.Spec.Metric,
			Precision: existing.Spec.Precision,
			Type:      existing.Spec.Type,
		},
	}
	discoveredSource := existing.Spec.ModelSources[0]
	discoveredSize := resource.MustParse("40Gi")
	discoveredSource.Size = &discoveredSize
	fixture.template.Status.ModelSources = []aimv1alpha1.AIMModelSource{discoveredSource}
	fixture.template.Status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      fixture.model.Name,
		Namespace: fixture.model.Namespace,
	}
	fixture.artifact.Spec.Size = discoveredSize

	resolvedArtifact := fixture.cache.Status.Artifacts[fixture.artifact.Name]
	fixture.cache.Status.Artifacts = map[string]aimv1alpha1.AIMResolvedArtifact{
		fixture.artifact.Name: resolvedArtifact,
	}
	for object, manager := range map[client.Object]string{
		fixture.model:    "aim-service-controller",
		fixture.template: "aim-model-controller",
		fixture.cache:    "aim-service-controller",
		fixture.artifact: "aim-template-cache-controller",
		fixture.claim:    "aim-artifact-controller",
	} {
		object.SetLabels(map[string]string{constants.LabelK8sManagedBy: manager})
	}

	base := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(append(fixture.objects(), existing)...).
		Build()
	reader := &defaultingProfileGetReader{
		Reader:      base,
		profileName: existing.Name,
	}
	result, err := Plan(context.Background(), reader, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic ||
		unit.TargetProfile == nil ||
		unit.TargetProfile.Name != existing.Name ||
		findKindOperation(unit, testAIMProfileKind, OperationReuse) == nil {
		t.Fatalf("auto-selected image service unit = %#v", unit)
	}
	defaulted := existing.DeepCopy()
	defaulted.Spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorAMD
	expectedDigest, _, err := contentDigest(defaulted, aimv1alpha2.GroupVersion.String(), testAIMProfileKind)
	if err != nil {
		t.Fatal(err)
	}
	if reader.profileGets != 1 || unit.TargetProfile.ContentDigest != expectedDigest {
		t.Fatalf("target profile was not pinned from canonical Get content: %#v", unit.TargetProfile)
	}

	duplicate := existing.DeepCopy()
	duplicate.Name = "second-generated-native-profile"
	duplicate.UID = types.UID("second-generated-native-profile")
	result = runPlanWithObjects(t, append(fixture.objects(), existing, duplicate)...)
	unit = result.Document.Units[0]
	if unit.Outcome != OutcomeManual || !hasManualCode(&unit, "AmbiguousTargetProfiles") {
		t.Fatalf("duplicate exact native profiles were not rejected: %#v", unit)
	}
}

func TestPlan_ConflictingTargetProfileIsManual(t *testing.T) {
	fixture := newPlanFixture("conflict")
	conflict := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fixture.template.Name,
			Namespace:       testNamespace,
			UID:             types.UID("conflicting-profile"),
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "different/model",
				Image: "registry.invalid/different:v1",
			},
		},
		Status: aimv1alpha2.AIMProfileStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusReady,
		},
	}
	result := runPlanWithObjects(t, append(fixture.objects(), conflict)...)
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeManual || !hasManualCode(&unit, "TargetProfileConflict") {
		t.Fatalf("unit = %#v, want target conflict", unit)
	}
}

func TestPlan_FollowsDirectClusterClosureWithoutClusterLists(t *testing.T) {
	fixture := newPlanFixture("cluster")
	clusterTemplate := &aimv1alpha1.AIMClusterServiceTemplate{
		ObjectMeta: fixture.template.ObjectMeta,
		Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: fixture.template.Spec.AIMServiceTemplateSpecCommon,
		},
		Status: fixture.template.Status,
	}
	clusterTemplate.Namespace = ""
	clusterTemplate.Spec.Name = "runtime-cluster"
	clusterModel := &aimv1alpha1.AIMClusterModel{
		ObjectMeta: fixture.model.ObjectMeta,
		Spec:       fixture.model.Spec,
		Status:     fixture.model.Status,
	}
	clusterModel.Namespace = ""
	clusterRuntimeConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "runtime-cluster",
			UID:             types.UID("runtime-cluster"),
			ResourceVersion: "9",
			Generation:      1,
		},
	}
	fixture.service.Status.ResolvedTemplate = &aimv1alpha1.AIMResolvedReference{
		Name:  clusterTemplate.Name,
		Scope: aimv1alpha1.AIMResolutionScopeCluster,
		Kind:  resourceKindClusterTemplate,
		UID:   clusterTemplate.UID,
	}
	fixture.service.Status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:  clusterModel.Name,
		Scope: aimv1alpha1.AIMResolutionScopeCluster,
		Kind:  "AIMClusterModel",
		UID:   clusterModel.UID,
	}
	fixture.cache.Spec.TemplateScope = aimv1alpha1.AIMServiceTemplateScopeCluster

	closureObjects := []client.Object{
		fixture.service,
		clusterTemplate,
		clusterModel,
		fixture.cache,
		fixture.artifact,
		fixture.claim,
		clusterRuntimeConfig,
		&aimv1alpha1.AIMClusterServiceTemplate{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-template"}},
		&aimv1alpha1.AIMClusterModel{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-model"}},
		&aimv1alpha1.AIMClusterRuntimeConfig{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-config"}},
	}
	initial := runPlanWithObjects(t, closureObjects...)
	if initial.Document.Units[0].Outcome != OutcomeAutomatic {
		t.Fatalf(
			"statusless runtime configuration made the unit manual: %#v",
			initial.Document.Units[0].ManualOutcomes,
		)
	}
	profileOperation := findKindOperation(initial.Document.Units[0], "AIMClusterProfile", OperationCreate)
	if profileOperation == nil {
		t.Fatal("initial cluster plan has no AIMClusterProfile create")
	}
	encoded, err := json.Marshal(profileOperation.Object)
	if err != nil {
		t.Fatal(err)
	}
	existingProfile := &aimv1alpha2.AIMClusterProfile{}
	if err := json.Unmarshal(encoded, existingProfile); err != nil {
		t.Fatal(err)
	}
	existingProfile.UID = types.UID("cluster-profile")
	existingProfile.ResourceVersion = "10"
	existingProfile.Generation = 1
	existingProfile.Status = aimv1alpha2.AIMProfileStatus{
		ObservedGeneration: 1,
		Status:             constants.AIMStatusReady,
	}
	closureObjects = append(closureObjects, existingProfile)

	scheme := testScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(closureObjects...).Build()
	reader := &recordingReader{Reader: base}
	result, err := Plan(context.Background(), reader, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic {
		t.Fatalf("cluster unit = %#v", unit)
	}
	if !hasObserved(unit, resourceKindClusterTemplate, clusterTemplate.Name) ||
		!hasObserved(unit, "AIMClusterModel", clusterModel.Name) ||
		!hasObserved(unit, "AIMClusterProfile", existingProfile.Name) ||
		!hasObserved(unit, "AIMClusterRuntimeConfig", clusterRuntimeConfig.Name) ||
		hasObserved(unit, resourceKindClusterTemplate, "unrelated-template") ||
		hasObserved(unit, "AIMClusterModel", "unrelated-model") {
		t.Fatalf("observed cluster closure = %#v", unit.ObservedResources)
	}
	for _, kind := range reader.listKinds {
		if kind != "*v1alpha1.AIMServiceList" &&
			kind != "*v1alpha2.AIMProfileCacheList" {
			t.Fatalf("unexpected cluster or dependency list: %s", kind)
		}
	}
}

func TestPlan_WarningIsolationAndNoKubernetesMutations(t *testing.T) {
	first := newPlanFixture("broken")
	second := newPlanFixture("healthy")
	second.rename("healthy")
	objects := append(first.objects(), second.objects()...)
	scheme := testScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	reader := &failingReader{
		Reader:       base,
		artifactName: first.artifact.Name,
	}
	before := &aimv1alpha1.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(second.service), before); err != nil {
		t.Fatal(err)
	}

	result, err := Plan(context.Background(), reader, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Document.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(result.Document.Units))
	}
	if result.Document.Units[0].Outcome != OutcomeManual ||
		result.Document.Units[1].Outcome != OutcomeAutomatic {
		t.Fatalf("outcomes = %q, %q", result.Document.Units[0].Outcome, result.Document.Units[1].Outcome)
	}
	if len(result.Document.Units[0].Warnings) == 0 {
		t.Fatal("failed unit has no isolated warning")
	}

	after := &aimv1alpha1.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(second.service), after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Plan mutated a Kubernetes object")
	}
}

func TestPlan_SecretReferencesRemainReferences(t *testing.T) {
	fixture := newPlanFixture("secret-ref")
	fixture.template.Spec.Env = []corev1.EnvVar{{
		Name: "HF_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "download-auth"},
				Key:                  "token",
			},
		},
	}}
	result := runFixturePlan(t, fixture)
	unit := result.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic {
		t.Fatalf("secret reference incorrectly made unit manual: %#v", unit.ManualOutcomes)
	}
	found := false
	for _, reference := range unit.SecretReferences {
		if reference.Name == "download-auth" && reference.Key == "token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("secret references = %#v", unit.SecretReferences)
	}
	encoded, err := json.Marshal(result.Document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fixture-token") {
		t.Fatal("plan persisted a secret literal")
	}
}

type planFixture struct {
	service  *aimv1alpha1.AIMService
	template *aimv1alpha1.AIMServiceTemplate
	model    *aimv1alpha1.AIMModel
	cache    *aimv1alpha1.AIMTemplateCache
	artifact *aimv1alpha1.AIMArtifact
	claim    *corev1.PersistentVolumeClaim
}

func newPlanFixture(suffix string) *planFixture {
	name := sanitizeTestName(suffix)
	templateName := "template-" + name
	modelName := "model-" + name
	cacheName := "cache-" + name
	artifactName := "artifact-" + name
	claimName := "pvc-" + name
	serviceUID := types.UID("service-" + name)
	templateUID := types.UID("template-" + name)
	modelUID := types.UID("model-" + name)
	cacheUID := types.UID("cache-" + name)
	artifactUID := types.UID("artifact-" + name)

	metric := aimv1alpha1.AIMMetricLatency
	precision := aimv1alpha1.AIMPrecisionFP16
	profileType := aimv1alpha1.AIMProfileTypeOptimized
	source := aimv1alpha1.AIMModelSource{
		ModelID:   "org/model",
		SourceURI: "hf://org/model",
	}
	service := &aimv1alpha1.AIMService{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: "AIMService"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "service-" + name,
			Namespace:       testNamespace,
			UID:             serviceUID,
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model:    &aimv1alpha1.AIMServiceModel{Name: ptr.To(modelName)},
			Template: &aimv1alpha1.AIMServiceTemplateConfig{Name: templateName},
			Caching:  &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeShared},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ObservedGeneration: 1,
			ReconcilerPipeline: "template",
			Status:             constants.AIMStatusRunning,
			Conditions: []metav1.Condition{{
				Type:   "MigrationRequired",
				Status: metav1.ConditionTrue,
				Reason: "TemplateAPIDeprecated",
			}},
			ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{
				Name:      templateName,
				Namespace: testNamespace,
				Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
				Kind:      resourceKindServiceTemplate,
				UID:       templateUID,
			},
			ResolvedModel: &aimv1alpha1.AIMResolvedReference{
				Name:      modelName,
				Namespace: testNamespace,
				Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
				Kind:      "AIMModel",
				UID:       modelUID,
			},
			Cache: &aimv1alpha1.AIMServiceCacheStatus{
				TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{
					Name:      cacheName,
					Namespace: testNamespace,
					Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
					Kind:      resourceKindTemplateCache,
					UID:       cacheUID,
				},
			},
		},
	}
	template := &aimv1alpha1.AIMServiceTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       resourceKindServiceTemplate,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            templateName,
			Namespace:       testNamespace,
			UID:             templateUID,
			ResourceVersion: "2",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: modelName,
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Metric:    &metric,
					Precision: &precision,
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							Requests:     1,
							Model:        "MI300X",
							ResourceName: constants.AMDGPUResourceName,
						},
					},
				},
				AimId:        "org/model",
				ModelId:      "org/model",
				Type:         &profileType,
				ModelSources: []aimv1alpha1.AIMModelSource{source},
				CustomProfile: &aimv1alpha1.AIMCustomProfile{
					EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
					EnvVars:    map[string]string{"ROCR_VISIBLE_DEVICES": "0"},
				},
			},
		},
		Status: aimv1alpha1.AIMServiceTemplateStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusReady,
			ModelSources:       []aimv1alpha1.AIMModelSource{source},
		},
	}
	model := &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: "AIMModel"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            modelName,
			Namespace:       testNamespace,
			UID:             modelUID,
			ResourceVersion: "3",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMModelSpec{Image: "registry.example/aim-model:v1"},
		Status: aimv1alpha1.AIMModelStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusReady,
		},
	}
	cache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: resourceKindTemplateCache},
		ObjectMeta: metav1.ObjectMeta{
			Name:            cacheName,
			Namespace:       testNamespace,
			UID:             cacheUID,
			ResourceVersion: "4",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:  templateName,
			TemplateScope: aimv1alpha1.AIMServiceTemplateScopeNamespace,
			Mode:          aimv1alpha1.TemplateCacheModeShared,
		},
		Status: aimv1alpha1.AIMTemplateCacheStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusReady,
			Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
				artifactName: {
					UID:                   string(artifactUID),
					Name:                  artifactName,
					Model:                 "org/model",
					Status:                constants.AIMStatusReady,
					PersistentVolumeClaim: claimName,
				},
			},
		},
	}
	artifact := &aimv1alpha1.AIMArtifact{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: resourceKindArtifact},
		ObjectMeta: metav1.ObjectMeta{
			Name:            artifactName,
			Namespace:       testNamespace,
			UID:             artifactUID,
			ResourceVersion: "5",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:      aimv1alpha1.ArtifactTypeModel,
			ModelID:   source.ModelID,
			SourceURI: source.SourceURI,
		},
		Status: aimv1alpha1.AIMArtifactStatus{
			ObservedGeneration:    1,
			Status:                constants.AIMStatusReady,
			PersistentVolumeClaim: claimName,
			Mode:                  aimv1alpha1.ArtifactModeShared,
		},
	}
	claim := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            claimName,
			Namespace:       testNamespace,
			UID:             types.UID("pvc-" + name),
			ResourceVersion: "6",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       resourceKindArtifact,
				Name:       artifactName,
				UID:        artifactUID,
			}},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	return &planFixture{
		service:  service,
		template: template,
		model:    model,
		cache:    cache,
		artifact: artifact,
		claim:    claim,
	}
}

func (fixture *planFixture) objects() []client.Object {
	return []client.Object{
		fixture.service,
		fixture.template,
		fixture.model,
		fixture.cache,
		fixture.artifact,
		fixture.claim,
	}
}

func (fixture *planFixture) rename(name string) {
	replacement := newPlanFixture(name)
	*fixture = *replacement
}

func sanitizeTestName(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func runFixturePlan(t *testing.T, fixture *planFixture) *PlanResult {
	t.Helper()
	return runPlanWithObjects(t, fixture.objects()...)
}

func runPlanWithObjects(t *testing.T, objects ...client.Object) *PlanResult {
	t.Helper()
	return runPlanWithOptions(t, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
	}, objects...)
}

func runPlanWithOptions(
	t *testing.T,
	options PlanOptions,
	objects ...client.Object,
) *PlanResult {
	t.Helper()
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objects...).
		Build()
	result, err := Plan(
		context.Background(),
		kubernetesClient,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		aimv1alpha1.AddToScheme,
		aimv1alpha2.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func hasImputation(unit MigrationUnit, field, source string) bool {
	for _, imputation := range unit.Imputations {
		if imputation.Field == field && imputation.Source == source {
			return true
		}
	}
	return false
}

func hasObserved(unit MigrationUnit, kind, name string) bool {
	for _, identity := range unit.ObservedResources {
		if identity.Kind == kind && identity.Name == name {
			return true
		}
	}
	return false
}

func findKindOperation(unit MigrationUnit, kind string, action OperationAction) *ProposedOperation {
	for index := range unit.ProposedOperations {
		operation := &unit.ProposedOperations[index]
		if operation.Resource.Kind == kind && operation.Action == action {
			return operation
		}
	}
	return nil
}

func nestedMap(t *testing.T, value map[string]any, keys ...string) map[string]any {
	t.Helper()
	current := value
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("%s is %T, want map", key, current[key])
		}
		current = next
	}
	return current
}

func profileYAMLContractAnnotation() string {
	return constants.AimLabelDomain + "/profile-yaml-contract"
}

type recordingReader struct {
	client.Reader
	listKinds []string
}

func (reader *recordingReader) List(ctx context.Context, list client.ObjectList, options ...client.ListOption) error {
	reader.listKinds = append(reader.listKinds, reflect.TypeOf(list).String())
	return reader.Reader.List(ctx, list, options...)
}

type failingReader struct {
	client.Reader
	artifactName string
}

type listErrorReader struct {
	client.Reader
	err error
}

type defaultingProfileGetReader struct {
	client.Reader
	profileName string
	profileGets int
}

func (reader *listErrorReader) List(
	context.Context,
	client.ObjectList,
	...client.ListOption,
) error {
	return reader.err
}

func (reader *defaultingProfileGetReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	options ...client.GetOption,
) error {
	if err := reader.Reader.Get(ctx, key, object, options...); err != nil {
		return err
	}
	if key.Name == reader.profileName {
		if profile, ok := object.(*aimv1alpha2.AIMProfile); ok {
			reader.profileGets++
			profile.Spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorAMD
		}
	}
	return nil
}

func (reader *failingReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	options ...client.GetOption,
) error {
	if key.Name == reader.artifactName {
		if _, ok := object.(*aimv1alpha1.AIMArtifact); ok {
			return errors.New("fixture read failure")
		}
	}
	return reader.Reader.Get(ctx, key, object, options...)
}
