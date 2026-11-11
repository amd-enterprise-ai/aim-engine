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

package aimprofile

import (
	"context"
	"slices"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

var llmSupport = serving.LLMInferenceServiceSupport{Enabled: true, KServeNamespace: "kserve-system"}

func llmProjectableSpec() aimv1alpha2.AIMProfileSpecCommon {
	spec := primaryGPUSpec()
	spec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b", SourceURI: "hf://qwen/qwen3-32b"}}
	return spec
}

func projectableNamespaceObservation(profile *aimv1alpha2.AIMProfile, modelSlugWinner bool) ProfileObservation {
	spec := profile.Spec.AIMProfileSpecCommon
	obs := ProfileObservation{
		ProfileFetchResult: ProfileFetchResult{modelSlugWinner: modelSlugWinner},
		matchResult: NodeMatchResult{
			MatchingNodes: 1,
			NodeAffinity:  BuildNodeAffinity(aimv1alpha2.AcceleratorTypeGPU, "MI300X", "unpartitioned"),
		},
		resolvedResources: ResolveProfileResources(spec),
		deployable:        true,
		projectable:       true,
		yamlContract:      profileyaml.CanonicalContract(&spec),
	}
	obs.profile = profile
	return obs
}

func llmConfigs(objs []client.Object) []*kservev1alpha2.LLMInferenceServiceConfig {
	var configs []*kservev1alpha2.LLMInferenceServiceConfig
	for _, o := range objs {
		if config, ok := o.(*kservev1alpha2.LLMInferenceServiceConfig); ok {
			configs = append(configs, config)
		}
	}
	return configs
}

func llmConfigNames(configs []*kservev1alpha2.LLMInferenceServiceConfig) []string {
	names := make([]string, 0, len(configs))
	for _, config := range configs {
		names = append(names, config.Name)
	}
	slices.Sort(names)
	return names
}

func TestProfilePlanResources_projectsLLMConfigsNextToServingRuntimes(t *testing.T) {
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-32b-mi300x", Namespace: "team-a"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: llmProjectableSpec()},
	}
	perProfile := serving.RuntimeName(profile.Name)
	modelSlug := serving.ModelSlugRuntimeName(profile.Spec.AimId)

	for _, tc := range []struct {
		name      string
		mode      aimv1alpha2.RuntimeProjectionMode
		wantNames []string
	}{
		{"exhaustive", aimv1alpha2.RuntimeProjectionModeExhaustive, []string{perProfile}},
		{"reduced", aimv1alpha2.RuntimeProjectionModeReduced, []string{modelSlug}},
		{"both", aimv1alpha2.RuntimeProjectionModeBoth, []string{perProfile, modelSlug}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &ProfileReconciler{ProjectionMode: tc.mode, LLMInferenceService: llmSupport}
			obs := projectableNamespaceObservation(profile, true)
			obs.llmConfigErr = validateNamespaceLLMConfigProjection(tc.mode, llmSupport, profile, obs)
			if obs.llmConfigErr != nil {
				t.Fatalf("unexpected config error: %v", obs.llmConfigErr)
			}

			plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile]{Object: profile}, obs)
			force := plan.GetToApplyWithForce()
			configs := llmConfigs(force)

			slices.Sort(tc.wantNames)
			if got := llmConfigNames(configs); !slices.Equal(got, tc.wantNames) {
				t.Fatalf("LLMInferenceServiceConfig names = %v, want %v", got, tc.wantNames)
			}
			for _, config := range configs {
				if config.Namespace != profile.Namespace {
					t.Errorf("config %s namespace = %q, want the profile namespace", config.Name, config.Namespace)
				}
				if config.Labels[constants.LabelRuntimeProjection] != constants.LabelValueRuntimeProjectionEager {
					t.Errorf("config %s is missing the eager projection marker", config.Name)
				}
				// The ServingRuntime projection of the same name applies the
				// ConfigMap the config mounts.
				findConfigMap(t, force, config.Name, profile.Namespace)
			}
			if got := countServingRuntimes(force); got != len(tc.wantNames) {
				t.Errorf("ServingRuntimes = %d, want %d alongside the configs", got, len(tc.wantNames))
			}
		})
	}
}

func TestProfilePlanResources_noLLMConfigsWithoutSupport(t *testing.T) {
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-32b-mi300x", Namespace: "team-a"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: llmProjectableSpec()},
	}
	r := &ProfileReconciler{ProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth}
	plan := r.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile]{Object: profile},
		projectableNamespaceObservation(profile, true),
	)
	if configs := llmConfigs(plan.GetToApplyWithForce()); len(configs) != 0 {
		t.Fatalf("projected %d LLMInferenceServiceConfigs without LLMInferenceService support", len(configs))
	}
}

func TestProfileLLMConfigFailure_keepsServingRuntimeAndReportsCondition(t *testing.T) {
	spec := llmProjectableSpec()
	spec.ModelSources = nil
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-32b-mi300x", Namespace: "team-a"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: spec},
	}
	mode := aimv1alpha2.RuntimeProjectionModeExhaustive
	r := &ProfileReconciler{ProjectionMode: mode, LLMInferenceService: llmSupport}
	obs := projectableNamespaceObservation(profile, false)
	obs.llmConfigErr = validateNamespaceLLMConfigProjection(mode, llmSupport, profile, obs)
	if obs.llmConfigErr == nil {
		t.Fatal("expected a profile without model sources to fail LLMInferenceServiceConfig construction")
	}

	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile]{Object: profile}, obs)
	force := plan.GetToApplyWithForce()
	if len(llmConfigs(force)) != 0 {
		t.Error("an unrenderable LLMInferenceServiceConfig must not be applied")
	}
	if countServingRuntimes(force) != 1 {
		t.Error("the ServingRuntime projection must not depend on the LLMInferenceServiceConfig")
	}

	cm := controllerutils.NewConditionManager(nil)
	r.DecorateStatus(&aimv1alpha2.AIMProfileStatus{}, cm, obs)
	cond := cm.Get(aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected)
	if cond == nil || cond.Status != metav1.ConditionFalse ||
		cond.Reason != aimv1alpha2.AIMProfileReasonLLMInferenceServiceConfigProjectionFailed {
		t.Fatalf("condition = %#v, want False/%s", cond, aimv1alpha2.AIMProfileReasonLLMInferenceServiceConfigProjectionFailed)
	}
	if runtimeCond := cm.Get(aimv1alpha2.AIMProfileConditionRuntimeProjected); runtimeCond == nil || runtimeCond.Status != metav1.ConditionTrue {
		t.Errorf("RuntimeProjected = %#v, want True", runtimeCond)
	}
}

func TestProfileLLMConfigCondition(t *testing.T) {
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-32b-mi300x", Namespace: "team-a"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: llmProjectableSpec()},
	}
	mode := aimv1alpha2.RuntimeProjectionModeExhaustive

	for _, tc := range []struct {
		name       string
		support    serving.LLMInferenceServiceSupport
		wantStatus metav1.ConditionStatus
	}{
		{"projected", llmSupport, metav1.ConditionTrue},
		{"support disabled", serving.LLMInferenceServiceSupport{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &ProfileReconciler{ProjectionMode: mode, LLMInferenceService: tc.support}
			cm := controllerutils.NewConditionManager([]metav1.Condition{{
				Type:   aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected,
				Status: metav1.ConditionTrue,
				Reason: aimv1alpha2.AIMProfileReasonLLMInferenceServiceConfigProjected,
			}})
			r.DecorateStatus(&aimv1alpha2.AIMProfileStatus{}, cm, projectableNamespaceObservation(profile, false))

			cond := cm.Get(aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected)
			if tc.wantStatus == "" {
				if cond != nil {
					t.Fatalf("condition = %#v, want it removed", cond)
				}
				return
			}
			if cond == nil || cond.Status != tc.wantStatus {
				t.Fatalf("condition = %#v, want status %s", cond, tc.wantStatus)
			}
		})
	}
}

func TestClusterProfilePlanResources_projectsBareLLMConfigsIntoKServeNamespace(t *testing.T) {
	profile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-32b-mi300x"},
		Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: llmProjectableSpec()},
	}
	spec := profile.Spec.AIMProfileSpecCommon
	mode := aimv1alpha2.RuntimeProjectionModeBoth
	obs := ClusterProfileObservation{
		ClusterProfileFetchResult: ClusterProfileFetchResult{profile: profile, modelSlugWinner: true},
		matchResult:               NodeMatchResult{MatchingNodes: 1},
		resolvedResources:         ResolveProfileResources(spec),
		deployable:                true,
		projectable:               true,
	}
	obs.llmConfigErr = validateClusterLLMConfigProjection(mode, llmSupport, profile, obs)
	if obs.llmConfigErr != nil {
		t.Fatalf("unexpected config error: %v", obs.llmConfigErr)
	}

	r := &ClusterProfileReconciler{ProjectionMode: mode, LLMInferenceService: llmSupport}
	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile]{Object: profile}, obs)
	configs := llmConfigs(plan.GetToApplyWithForce())

	want := []string{serving.RuntimeName(profile.Name), serving.ModelSlugRuntimeName(spec.AimId)}
	slices.Sort(want)
	if got := llmConfigNames(configs); !slices.Equal(got, want) {
		t.Fatalf("LLMInferenceServiceConfig names = %v, want %v", got, want)
	}
	for _, config := range configs {
		if config.Namespace != llmSupport.KServeNamespace {
			t.Errorf("config %s namespace = %q, want the KServe namespace", config.Name, config.Namespace)
		}
	}
}
