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
	"fmt"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

// An LLMInferenceServiceConfig is projected next to every ServingRuntime the
// profile reconcilers maintain, under the same name and the same per-profile /
// model-slug gates, so a native LLMInferenceService references a profile by
// the same name a native InferenceService uses. Namespace configs mount the
// ServingRuntime's colocated profile ConfigMap; cluster configs are bare and
// live in the KServe namespace.

func namespaceRuntimeInput(profile *aimv1alpha2.AIMProfile, obs ProfileObservation, name string) serving.NamespaceRuntimeInput {
	spec := profile.Spec.AIMProfileSpecCommon
	return serving.NamespaceRuntimeInput{
		ProfileName:  profile.Name,
		Name:         name,
		Namespace:    profile.Namespace,
		Spec:         &spec,
		YAMLContract: obs.yamlContract,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
		Cache:        obs.profileCache,
	}
}

func clusterRuntimeInput(profile *aimv1alpha2.AIMClusterProfile, obs ClusterProfileObservation, name string) serving.ClusterRuntimeInput {
	spec := profile.Spec.AIMProfileSpecCommon
	return serving.ClusterRuntimeInput{
		ProfileName:  profile.Name,
		Name:         name,
		Spec:         &spec,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
	}
}

// namespaceLLMConfigNames returns the names of the ServingRuntimes the profile
// maintains, which are also the names of its LLMInferenceServiceConfigs. An
// empty name selects the hashed per-profile name.
func namespaceLLMConfigNames(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) []string {
	if profile == nil || profile.Spec.Image == "" {
		return nil
	}
	var names []string
	if shouldMaintainNamespaceRuntime(mode, profile, obs.projectable) {
		names = append(names, "")
	}
	if shouldMaintainNamespaceModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		names = append(names, serving.ModelSlugRuntimeName(profile.Spec.AimId))
	}
	return names
}

func clusterLLMConfigNames(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) []string {
	if profile == nil || profile.Spec.Image == "" {
		return nil
	}
	var names []string
	if shouldMaintainClusterRuntime(mode, profile, obs.projectable) {
		names = append(names, "")
	}
	if shouldMaintainClusterModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		names = append(names, serving.ModelSlugRuntimeName(profile.Spec.AimId))
	}
	return names
}

func validateNamespaceLLMConfigProjection(
	mode aimv1alpha2.RuntimeProjectionMode,
	support serving.LLMInferenceServiceSupport,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) error {
	if !support.Enabled {
		return nil
	}
	for _, name := range namespaceLLMConfigNames(mode, profile, obs) {
		if _, err := serving.BuildNamespaceLLMInferenceServiceConfig(namespaceRuntimeInput(profile, obs, name)); err != nil {
			return fmt.Errorf("build LLMInferenceServiceConfig: %w", err)
		}
	}
	return nil
}

func validateClusterLLMConfigProjection(
	mode aimv1alpha2.RuntimeProjectionMode,
	support serving.LLMInferenceServiceSupport,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) error {
	if !support.Enabled {
		return nil
	}
	for _, name := range clusterLLMConfigNames(mode, profile, obs) {
		input := clusterRuntimeInput(profile, obs, name)
		if _, err := serving.BuildClusterLLMInferenceServiceConfig(input, support.KServeNamespace); err != nil {
			return fmt.Errorf("build LLMInferenceServiceConfig: %w", err)
		}
	}
	return nil
}

// planNamespaceLLMConfigs force-applies the profile's namespaced
// LLMInferenceServiceConfigs. The colocated profile ConfigMap they mount is
// applied by the ServingRuntime projection of the same name.
func planNamespaceLLMConfigs(
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	support serving.LLMInferenceServiceSupport,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) {
	if !support.Enabled || obs.llmConfigErr != nil {
		return
	}
	for _, name := range namespaceLLMConfigNames(mode, profile, obs) {
		config, err := serving.BuildNamespaceLLMInferenceServiceConfig(namespaceRuntimeInput(profile, obs, name))
		if err != nil {
			continue
		}
		markEagerProjection(config)
		plan.ApplyWithForce(config)
	}
}

// planClusterLLMConfigs force-applies the profile's bare
// LLMInferenceServiceConfigs into the KServe namespace. The owner reference to
// the cluster-scoped profile garbage-collects them with it.
func planClusterLLMConfigs(
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	support serving.LLMInferenceServiceSupport,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) {
	if !support.Enabled || obs.llmConfigErr != nil {
		return
	}
	for _, name := range clusterLLMConfigNames(mode, profile, obs) {
		config, err := serving.BuildClusterLLMInferenceServiceConfig(clusterRuntimeInput(profile, obs, name), support.KServeNamespace)
		if err != nil {
			continue
		}
		plan.ApplyWithForce(config)
	}
}

// decorateLLMConfigProjectionCondition records whether the profile's
// LLMInferenceServiceConfigs are projected. The condition is absent when
// LLMInferenceService support is off or the profile projects nothing.
func decorateLLMConfigProjectionCondition(
	cm *controllerutils.ConditionManager,
	support serving.LLMInferenceServiceSupport,
	projectsConfigs bool,
	configErr error,
) {
	switch {
	case !support.Enabled:
		cm.Delete(aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected)
	case configErr != nil:
		cm.MarkFalse(
			aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected,
			aimv1alpha2.AIMProfileReasonLLMInferenceServiceConfigProjectionFailed,
			fmt.Sprintf("Failed to build projected LLMInferenceServiceConfig: %v", configErr),
		)
	case projectsConfigs:
		cm.MarkTrue(
			aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected,
			aimv1alpha2.AIMProfileReasonLLMInferenceServiceConfigProjected,
			"LLMInferenceServiceConfig projected for this profile",
		)
	default:
		cm.Delete(aimv1alpha2.AIMProfileConditionLLMInferenceServiceConfigProjected)
	}
}
