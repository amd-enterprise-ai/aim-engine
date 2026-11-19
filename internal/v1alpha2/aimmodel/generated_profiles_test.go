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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const (
	testQwenSourceURI = "hf://Qwen/Qwen3.5-0.8B"
	testH100Model     = "H100"
)

func TestEffectiveModelIdentityDefaults(t *testing.T) {
	t.Parallel()

	spec := &aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"}
	if got := effectiveAimID(spec); got != spec.ModelID {
		t.Fatalf("effectiveAimID() = %q, want %q", got, spec.ModelID)
	}
	source := effectiveModelSource(spec)
	if source.ModelID != spec.ModelID {
		t.Fatalf("source.modelId = %q, want %q", source.ModelID, spec.ModelID)
	}
	if source.SourceURI != testQwenSourceURI {
		t.Fatalf("source.sourceUri = %q", source.SourceURI)
	}
}

func TestEffectiveModelIdentityOverrides(t *testing.T) {
	t.Parallel()

	spec := &aimv1alpha1.AIMModelSpec{
		ModelID: "acme/qwen-finetune",
		AimId:   "Qwen/Qwen3.5-0.8B",
		Source: &aimv1alpha1.AIMModelSourceLocation{
			URI:       "s3://models/qwen-finetune",
			Precision: aimv1alpha1.AIMPrecisionBF16,
		},
	}
	if got := effectiveAimID(spec); got != spec.AimId {
		t.Fatalf("effectiveAimID() = %q, want %q", got, spec.AimId)
	}
	source := effectiveModelSource(spec)
	if source.SourceURI != spec.Source.URI || source.Precision != spec.Source.Precision {
		t.Fatalf("source = %#v, want explicit location", source)
	}
}

func TestBuildDesiredGeneratedProfiles_GenericNVIDIA(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	fallback := testNVIDIAFallback("nvidia-vllm", 0)
	generated, err := buildDesiredGeneratedProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{fallback}, aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}), nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.AimId != model.Spec.ModelID || profile.Spec.ModelId != model.Spec.ModelID {
		t.Fatalf("profile identity = aimId %q modelId %q", profile.Spec.AimId, profile.Spec.ModelId)
	}
	if len(profile.Spec.ModelSources) != 1 || profile.Spec.ModelSources[0].SourceURI != testQwenSourceURI {
		t.Fatalf("profile sources = %#v", profile.Spec.ModelSources)
	}
	if profile.Spec.AcceleratorVendor != aimv1alpha1.AcceleratorVendorNVIDIA ||
		profile.Spec.AcceleratorType != aimv1alpha1.AcceleratorTypeGPU ||
		profile.Spec.AcceleratorModel != "" ||
		profile.Spec.AcceleratorCount != 1 {
		t.Fatalf("profile accelerator = %#v", profile.Spec.AIMProfileSpecCommon)
	}
	if profile.Spec.Type != aimv1alpha1.AIMProfileTypeUnoptimized || !profile.Spec.Primary {
		t.Fatalf("profile type/primary = %q/%v", profile.Spec.Type, profile.Spec.Primary)
	}
	if got := profile.Labels[constants.LabelKeyProfileOrigin]; got != string(aimv1alpha1.ProfileOriginGenerated) {
		t.Fatalf("profile origin = %q", got)
	}
	contract, found, err := profileyaml.FromAnnotations(profile.Annotations)
	if err != nil {
		t.Fatalf("parse generated profile YAML contract: %v", err)
	}
	if !found || contract.Codec() != profileyaml.CodecV1 {
		t.Fatalf("generated profile contract = %q, found=%v", contract.Codec(), found)
	}
	resources := aimprofile.ResolveProfileResources(profile.Spec.AIMProfileSpecCommon)
	gpu := resources.Requests[corev1.ResourceName(constants.NVIDIAGPUResourceName)]
	if gpu.Value() != 1 {
		t.Fatalf("resolved resources = %#v", resources)
	}
}

func TestBuildDesiredGeneratedProfiles_AimIDChangeRekeysChild(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec: aimv1alpha1.AIMModelSpec{
			ModelID: "acme/qwen-finetune",
			AimId:   "Qwen/Qwen3.5-0.8B",
		},
	}
	fallback := testNVIDIAFallback("nvidia-vllm", 0)
	first, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{fallback},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("first buildDesiredGeneratedProfiles() error = %v", err)
	}
	firstProfile := first.desired[0].Object.(*aimv1alpha2.AIMProfile)

	model.Spec.AimId = "Qwen/Qwen3.5-4B"
	second, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{fallback},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		[]managedProfile{{Object: firstProfile}},
	)
	if err != nil {
		t.Fatalf("second buildDesiredGeneratedProfiles() error = %v", err)
	}
	secondProfile := second.desired[0].Object.(*aimv1alpha2.AIMProfile)
	if firstProfile.Name == secondProfile.Name {
		t.Fatalf("aimId change reused immutable child name %q", firstProfile.Name)
	}
	if secondProfile.Spec.AimId != model.Spec.AimId {
		t.Fatalf("replacement aimId = %q, want %q", secondProfile.Spec.AimId, model.Spec.AimId)
	}
}

func TestBuildDesiredGeneratedProfiles_AMDOnlyNodeSelectsAMDFallback(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{
			testAMDFallback("amd-vllm", 10),
			testNVIDIAFallback("nvidia-vllm", 20),
		},
		aimprofile.NodesOnly([]corev1.Node{testAMDNode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want only the AMD fallback", len(desired))
	}

	profile := desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.ProfileId != "amd-vllm" ||
		profile.Spec.AcceleratorVendor != aimv1alpha1.AcceleratorVendorAMD ||
		profile.Spec.AcceleratorType != aimv1alpha1.AcceleratorTypeGPU {
		t.Fatalf("profile identity/accelerator = %#v", profile.Spec.AIMProfileSpecCommon)
	}
	if profile.Spec.AimId != model.Spec.ModelID || profile.Spec.ModelId != model.Spec.ModelID {
		t.Fatalf("profile identity = aimId %q modelId %q", profile.Spec.AimId, profile.Spec.ModelId)
	}
	if len(profile.Spec.ModelSources) != 1 || profile.Spec.ModelSources[0].SourceURI != testQwenSourceURI {
		t.Fatalf("profile sources = %#v", profile.Spec.ModelSources)
	}

	resources := aimprofile.ResolveProfileResources(profile.Spec.AIMProfileSpecCommon)
	for _, resourceList := range []corev1.ResourceList{resources.Requests, resources.Limits} {
		if got := resourceList[corev1.ResourceName(constants.AMDGPUResourceName)]; got.Cmp(resource.MustParse("1")) != 0 {
			t.Fatalf("%s = %s, want 1", constants.AMDGPUResourceName, got.String())
		}
		if _, exists := resourceList[corev1.ResourceName(constants.NVIDIAGPUResourceName)]; exists {
			t.Fatalf("unexpected %s in %#v", constants.NVIDIAGPUResourceName, resourceList)
		}
	}
}

func TestBuildDesiredGeneratedProfiles_NoHardwareDoesNotCreate(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	generated, err := buildDesiredGeneratedProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{testNVIDIAFallback("nvidia-vllm", 0)}, aimprofile.NodeInventory{}, nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 0 {
		t.Fatalf("len(desired) = %d, want 0", len(desired))
	}
}

func TestBuildDesiredGeneratedProfiles_ExplainsHardwareRejection(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	fallback := testNVIDIAFallback("nvidia-vllm", 0)
	fallback.Runtime.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("32Gi"),
		},
	}
	node := testNVIDIANode()
	node.Name = "mainbox"
	node.Status.Allocatable[corev1.ResourceName(constants.NVIDIAGPUResourceName)] = resource.MustParse("0")
	node.Status.Allocatable[corev1.ResourceMemory] = resource.MustParse("64Gi")

	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{fallback},
		aimprofile.NodesOnly([]corev1.Node{node}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	if len(generated.desired) != 0 {
		t.Fatalf("len(desired) = %d, want 0", len(generated.desired))
	}
	want := `No configured runtime fallback matches available cluster hardware: fallback "nvidia-vllm": node "mainbox": resource nvidia.com/gpu requested 1, allocatable 0`
	if got := generated.noCompatibleRuntimeMessage(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestBuildDesiredGeneratedProfiles_ExplainsModelRequestMismatch(t *testing.T) {
	t.Parallel()

	fallback := testNVIDIAFallback("nvidia-h100", 0)
	fallback.Match.AcceleratorModel = testH100Model
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec: aimv1alpha1.AIMModelSpec{
			ModelID:     "Qwen/Qwen3.5-0.8B",
			Accelerator: &aimv1alpha1.AIMModelAcceleratorRequest{Model: "A100"},
		},
	}

	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{fallback},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	want := `No configured runtime fallback matches available cluster hardware: fallback "nvidia-h100": model requests accelerator model "A100", fallback targets "H100"`
	if got := generated.noCompatibleRuntimeMessage(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestBuildDesiredGeneratedProfiles_RetainsExistingWithoutHardware(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	existing := []managedProfile{{
		Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{
			Name:      "old",
			Namespace: "team-a",
			Annotations: map[string]string{
				annotationProfileGenerationFallback: "nvidia-vllm",
			},
		}},
	}}
	generated, err := buildDesiredGeneratedProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{testNVIDIAFallback("nvidia-vllm", 0)}, aimprofile.NodeInventory{}, existing)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want retained profile", len(desired))
	}
}

func TestBuildDesiredGeneratedProfiles_DeterministicPrimary(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	fallbacks := []aimv1alpha1.AIMProfileGenerationFallback{
		testNVIDIAFallback("generic", 10),
		testNVIDIAFallback("preferred", 20),
		testNVIDIAFallback("preferred-z", 20),
	}
	generated, err := buildDesiredGeneratedProfiles(model, fallbacks, aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}), nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	var primary string
	for _, item := range desired {
		profile := item.Object.(*aimv1alpha2.AIMProfile)
		if profile.Spec.Primary {
			if primary != "" {
				t.Fatal("more than one generated profile is primary")
			}
			primary = profile.Spec.ProfileId
		}
	}
	if primary != "preferred" {
		t.Fatalf("primary fallback = %q, want preferred", primary)
	}
}

func TestBuildDesiredGeneratedProfiles_AvailableFallbackWinsPrimary(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	unavailable := testNVIDIAFallback("unavailable-high-priority", 100)
	unavailable.Match.AcceleratorModel = testH100Model
	existing := []managedProfile{{
		Object: &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{
			Name:      "retained",
			Namespace: model.Namespace,
			Annotations: map[string]string{
				annotationProfileGenerationFallback: unavailable.Name,
			},
		}},
	}}
	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{
			unavailable,
			testNVIDIAFallback("available", 1),
		},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		existing,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 2 {
		t.Fatalf("len(desired) = %d, want retained and available profiles", len(desired))
	}
	for _, item := range desired {
		profile := item.Object.(*aimv1alpha2.AIMProfile)
		if profile.Spec.Primary != (profile.Spec.ProfileId == "available") {
			t.Fatalf("profile %q primary = %v", profile.Spec.ProfileId, profile.Spec.Primary)
		}
	}
}

func TestBuildDesiredGeneratedProfiles_SpecificFallbackWinsPriorityTie(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	specific := testNVIDIAFallback("specific", 10)
	specific.Match.AcceleratorModel = "L4"
	node := testNVIDIANode()
	node.Labels[aimprofile.AcceleratorLabelPrefix+"L4"] = ""

	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{
			testNVIDIAFallback("generic", 10),
			specific,
		},
		aimprofile.NodesOnly([]corev1.Node{node}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	desired := generated.desired
	for _, item := range desired {
		profile := item.Object.(*aimv1alpha2.AIMProfile)
		if profile.Spec.Primary != (profile.Spec.ProfileId == "specific") {
			t.Fatalf("profile %q primary = %v", profile.Spec.ProfileId, profile.Spec.Primary)
		}
	}
}

func TestBuildDesiredGeneratedClusterProfiles(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	generated, err := buildDesiredGeneratedClusterProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{testNVIDIAFallback("nvidia-vllm", 0)}, aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}), nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedClusterProfiles() error = %v", err)
	}
	desired := generated.desired
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile := desired[0].Object.(*aimv1alpha2.AIMClusterProfile)
	if got := profile.Labels[constants.LabelKeySourceModelScope]; got != constants.LabelValueSourceModelScopeCluster {
		t.Fatalf("source model scope = %q", got)
	}
}

func TestPlanResources_RemovesProfileWhenFallbackIsRemoved(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	existing := &aimv1alpha2.AIMProfile{ObjectMeta: metav1.ObjectMeta{
		Name:      "generated",
		Namespace: "team-a",
		Annotations: map[string]string{
			annotationModelUID:                  string(model.UID),
			annotationProfileGenerationFallback: "removed",
		},
	}}
	obs := ModelObservation{
		ModelFetchResult: ModelFetchResult{
			model: model,
			existingProfiles: controllerutils.FetchResult[[]managedProfile]{
				Value: []managedProfile{{Object: existing}},
			},
		},
		Kind:      aimv1alpha1.AIMModelKindGenerated,
		pruneSafe: true,
	}
	plan := (&ModelReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		obs,
	)
	if len(plan.GetToDelete()) != 1 || plan.GetToDelete()[0].GetName() != existing.Name {
		t.Fatalf("toDelete = %#v, want removed generated profile", plan.GetToDelete())
	}
}

// The model's accelerator request supplies the count a runtime template cannot
// know, and narrows/overrides the axes the fallback leaves open.
func TestBuildDesiredGeneratedProfiles_AcceleratorRequest(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec: aimv1alpha1.AIMModelSpec{
			ModelID: "Qwen/Qwen3.5-0.8B",
			Accelerator: &aimv1alpha1.AIMModelAcceleratorRequest{
				Vendor: aimv1alpha1.AcceleratorVendorNVIDIA,
				Count:  ptr.To(int32(4)),
			},
		},
	}
	fallbacks := []aimv1alpha1.AIMProfileGenerationFallback{
		testNVIDIAFallback("nvidia-vllm", 0),
		testAMDFallback("amd-vllm", 10),
	}
	nvidiaNode := testNVIDIANode()
	nvidiaNode.Status.Allocatable[corev1.ResourceName(constants.NVIDIAGPUResourceName)] = resource.MustParse("4")
	generated, err := buildDesiredGeneratedProfiles(
		model,
		fallbacks,
		aimprofile.NodesOnly([]corev1.Node{nvidiaNode, testAMDNode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	if len(generated.desired) != 1 {
		t.Fatalf("len(desired) = %d, want only the requested vendor", len(generated.desired))
	}
	profile := generated.desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.AcceleratorVendor != aimv1alpha1.AcceleratorVendorNVIDIA {
		t.Fatalf("acceleratorVendor = %q, want nvidia", profile.Spec.AcceleratorVendor)
	}
	if profile.Spec.AcceleratorCount != 4 {
		t.Fatalf("acceleratorCount = %d, want 4", profile.Spec.AcceleratorCount)
	}
	if got := generated.matchedFallbacks; len(got) != 1 || got[0] != "nvidia-vllm" {
		t.Fatalf("matchedFallbacks = %v, want [nvidia-vllm]", got)
	}
}

func TestBuildDesiredGeneratedProfiles_AcceleratorCountDefaultsToOne(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{testNVIDIAFallback("nvidia-vllm", 0)},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	profile := generated.desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.AcceleratorCount != defaultGeneratedAcceleratorCount {
		t.Fatalf("acceleratorCount = %d, want %d", profile.Spec.AcceleratorCount, defaultGeneratedAcceleratorCount)
	}
}

// A request for a card the fallback does not serve must not silently fall back
// to that fallback's own model.
func TestBuildDesiredGeneratedProfiles_AcceleratorModelMismatchDropsFallback(t *testing.T) {
	t.Parallel()

	fallback := testNVIDIAFallback("nvidia-h100", 0)
	fallback.Match.AcceleratorModel = testH100Model

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec: aimv1alpha1.AIMModelSpec{
			ModelID:     "Qwen/Qwen3.5-0.8B",
			Accelerator: &aimv1alpha1.AIMModelAcceleratorRequest{Model: "A100"},
		},
	}
	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{fallback},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	if len(generated.desired) != 0 {
		t.Fatalf("len(desired) = %d, want 0 for mismatched accelerator model", len(generated.desired))
	}
}

// Generated profiles are unoptimized unless the platform operator raises the
// tier on the fallback — that is what keeps them below the AIMService floor.
func TestBuildDesiredGeneratedProfiles_ProfileType(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}

	unset := testNVIDIAFallback("nvidia-vllm", 0)
	generated, err := buildDesiredGeneratedProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{unset}, aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}), nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	profile := generated.desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.Type != aimv1alpha1.AIMProfileTypeUnoptimized {
		t.Fatalf("type = %q, want unoptimized by default", profile.Spec.Type)
	}

	raised := testNVIDIAFallback("nvidia-vllm", 0)
	raised.Type = aimv1alpha1.AIMProfileTypeGeneral
	generated, err = buildDesiredGeneratedProfiles(model, []aimv1alpha1.AIMProfileGenerationFallback{raised}, aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}), nil)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	profile = generated.desired[0].Object.(*aimv1alpha2.AIMProfile)
	if profile.Spec.Type != aimv1alpha1.AIMProfileTypeGeneral {
		t.Fatalf("type = %q, want general", profile.Spec.Type)
	}
}

func TestBuildDesiredGeneratedProfiles_AutoSelectionPolicy(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "uid-1"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}

	optimized := testNVIDIAFallback("optimized", 0)
	any := testNVIDIAFallback("any", 0)
	any.AutoSelectionPolicy = aimv1alpha1.AIMProfileAutoSelectionPolicyAny
	generated, err := buildDesiredGeneratedProfiles(
		model,
		[]aimv1alpha1.AIMProfileGenerationFallback{optimized, any},
		aimprofile.NodesOnly([]corev1.Node{testNVIDIANode()}),
		nil,
	)
	if err != nil {
		t.Fatalf("buildDesiredGeneratedProfiles() error = %v", err)
	}
	if len(generated.desired) != 2 {
		t.Fatalf("len(desired) = %d, want 2", len(generated.desired))
	}

	policies := make(map[string]aimv1alpha1.AIMProfileAutoSelectionPolicy, len(generated.desired))
	for _, desired := range generated.desired {
		profile := desired.Object.(*aimv1alpha2.AIMProfile)
		policies[profile.Spec.ProfileId] = profile.Spec.AutoSelectionPolicy
	}
	if got := policies["optimized"]; got != aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized {
		t.Fatalf("optimized autoSelectionPolicy = %q, want optimized", got)
	}
	if got := policies["any"]; got != aimv1alpha1.AIMProfileAutoSelectionPolicyAny {
		t.Fatalf("any autoSelectionPolicy = %q, want any", got)
	}
}

func TestGeneratedProfilesStatus_ReportsRuntimeFallbackStrategy(t *testing.T) {
	t.Parallel()

	status := generatedProfilesStatus(generatedProfiles{matchedFallbacks: []string{"nvidia-vllm"}})
	if status.Strategy != aimv1alpha1.ProfileGenerationStrategyRuntimeFallback {
		t.Fatalf("strategy = %q, want RuntimeFallback", status.Strategy)
	}
	if len(status.MatchedFallbacks) != 1 || status.MatchedFallbacks[0] != "nvidia-vllm" {
		t.Fatalf("matchedFallbacks = %v", status.MatchedFallbacks)
	}
}

func TestGeneratedProfiles_NoFallbacksConfiguredMessage(t *testing.T) {
	t.Parallel()

	want := "No configured runtime fallback matches available cluster hardware: the resolved RuntimeConfig defines no profile-generation fallbacks"
	if got := (generatedProfiles{}).noCompatibleRuntimeMessage(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func testNVIDIAFallback(name string, priority int32) aimv1alpha1.AIMProfileGenerationFallback {
	return aimv1alpha1.AIMProfileGenerationFallback{
		Name:     name,
		Priority: priority,
		Match: aimv1alpha1.AIMProfileGenerationMatch{
			AcceleratorVendor: aimv1alpha1.AcceleratorVendorNVIDIA,
			AcceleratorType:   aimv1alpha1.AcceleratorTypeGPU,
		},
		Runtime: aimv1alpha1.AIMProfileGenerationRuntime{
			Image:  "vllm/vllm-openai:test",
			Engine: "vllm",
		},
	}
}

func testAMDFallback(name string, priority int32) aimv1alpha1.AIMProfileGenerationFallback {
	return aimv1alpha1.AIMProfileGenerationFallback{
		Name:     name,
		Priority: priority,
		Match: aimv1alpha1.AIMProfileGenerationMatch{
			AcceleratorVendor: aimv1alpha1.AcceleratorVendorAMD,
			AcceleratorType:   aimv1alpha1.AcceleratorTypeGPU,
		},
		Runtime: aimv1alpha1.AIMProfileGenerationRuntime{
			Image:  "rocm/vllm:test",
			Engine: "vllm",
		},
	}
}

func testNVIDIANode() corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			aimprofile.AcceleratorVendorLabelPrefix + "GPU.NVIDIA":                          "1",
			aimprofile.PartitioningSchemeLabelPrefix + aimprofile.PartitioningSchemeDefault: "1",
		}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(constants.NVIDIAGPUResourceName): resource.MustParse("1"),
				// Keep the generic fixture large enough for tests that raise
				// acceleratorCount; focused rejection tests lower the relevant
				// resource explicitly.
				corev1.ResourceCPU:    resource.MustParse("64"),
				corev1.ResourceMemory: resource.MustParse("512Gi"),
			},
		},
	}
}

func testAMDNode() corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			aimprofile.AcceleratorVendorLabelPrefix + "GPU.AMD":                             "1",
			aimprofile.PartitioningSchemeLabelPrefix + aimprofile.PartitioningSchemeDefault: "1",
		}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("1"),
				// Keep the generic fixture large enough for tests that raise
				// acceleratorCount; focused rejection tests lower the relevant
				// resource explicitly.
				corev1.ResourceCPU:    resource.MustParse("64"),
				corev1.ResourceMemory: resource.MustParse("512Gi"),
			},
		},
	}
}
