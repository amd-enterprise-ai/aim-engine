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

package aimservice

import (
	"context"
	"fmt"
	"slices"
	"strings"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	llmBaseRefsComponent = "LLMInferenceServiceBaseRefs"

	reasonLLMBaseRefNotFound = "LLMInferenceServiceConfigNotFound"
	reasonLLMBaseRefConflict = "LLMInferenceServiceConfigConflict"
	reasonLLMBaseRefsReady   = "LLMInferenceServiceConfigsResolved"
)

// llmBaseRef is one user-supplied spec.llmInferenceService.baseRefs entry as
// observed. config is nil when the name resolves in neither the service
// namespace nor the KServe namespace.
type llmBaseRef struct {
	name   string
	config *kservev1alpha2.LLMInferenceServiceConfig
	err    error
}

// userLLMBaseRefs returns the service's user-supplied baseRefs, in order.
func userLLMBaseRefs(service *aimv1alpha2.AIMService) []corev1.LocalObjectReference {
	if service.Spec.LLMInferenceService == nil {
		return nil
	}
	refs := make([]corev1.LocalObjectReference, 0, len(service.Spec.LLMInferenceService.BaseRefs))
	for _, ref := range service.Spec.LLMInferenceService.BaseRefs {
		refs = append(refs, corev1.LocalObjectReference{Name: ref.Name})
	}
	return refs
}

// fetchLLMBaseRefs looks each user-supplied config up the way KServe resolves
// baseRefs: the service namespace first, then the KServe namespace.
func fetchLLMBaseRefs(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha2.AIMService,
	kserveNamespace string,
) []llmBaseRef {
	refs := userLLMBaseRefs(service)
	observed := make([]llmBaseRef, 0, len(refs))
	for _, ref := range refs {
		entry := llmBaseRef{name: ref.Name}
		for _, namespace := range []string{service.Namespace, kserveNamespace} {
			var config kservev1alpha2.LLMInferenceServiceConfig
			err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &config)
			if err == nil {
				entry.config = &config
				break
			}
			if !apierrors.IsNotFound(err) {
				entry.err = err
				break
			}
		}
		observed = append(observed, entry)
	}
	return observed
}

// llmConfigConflicts lists the fields of a user-supplied config that conflict
// with what AIM Engine owns for an LLMInferenceService-served AIMService:
// routing (the AIMService HTTPRoute), autoscaling (the KEDA ScaledObject) and
// replicas, the single-node workload shape, LoRA adapters (AIM Engine's
// adapter contract), and tracing, for which KServe adds vLLM arguments that the
// AIM entrypoint rejects. Every other field is deliberately left to the user.
func llmConfigConflicts(config *kservev1alpha2.LLMInferenceServiceConfig) []string {
	spec := config.Spec
	var fields []string
	if spec.Router != nil {
		fields = append(fields, "spec.router")
	}
	if spec.Replicas != nil {
		fields = append(fields, "spec.replicas")
	}
	if spec.Scaling != nil {
		fields = append(fields, "spec.scaling")
	}
	if spec.Prefill != nil {
		fields = append(fields, "spec.prefill")
	}
	if spec.Worker != nil {
		fields = append(fields, "spec.worker")
	}
	if spec.Model.LoRA != nil {
		fields = append(fields, "spec.model.lora")
	}
	if spec.Tracing != nil {
		fields = append(fields, "spec.tracing")
	}
	return fields
}

// llmBaseRefsComponentHealth reports whether every user-supplied config exists
// and leaves AIM Engine's fields alone. A missing config is a missing
// reference; a conflicting one is an invalid spec. Both block the
// LLMInferenceService from being planned.
func llmBaseRefsComponentHealth(refs []llmBaseRef, kserveNamespace, serviceNamespace string) controllerutils.ComponentHealth {
	if len(refs) == 0 {
		return controllerutils.ComponentHealth{}
	}
	health := controllerutils.ComponentHealth{
		Component:      llmBaseRefsComponent,
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	var missing, conflicts []string
	for _, ref := range refs {
		switch {
		case ref.err != nil:
			message := fmt.Sprintf("Failed to fetch LLMInferenceServiceConfig %q: %v", ref.name, ref.err)
			health.State = constants.AIMStatusFailed
			health.Reason = reasonFetchError
			health.Message = message
			health.Errors = append(health.Errors, controllerutils.NewInfrastructureError(
				reasonFetchError,
				message,
				ref.err,
			))
		case ref.config == nil:
			missing = append(missing, ref.name)
		default:
			if fields := llmConfigConflicts(ref.config); len(fields) > 0 {
				conflicts = append(conflicts, fmt.Sprintf("%s sets %s", ref.name, strings.Join(fields, ", ")))
			}
		}
	}
	if len(health.Errors) > 0 {
		return health
	}

	if len(missing) > 0 {
		message := fmt.Sprintf("LLMInferenceServiceConfig %s not found in namespace %s or %s",
			strings.Join(missing, ", "), serviceNamespace, kserveNamespace)
		health.State = constants.AIMStatusFailed
		health.Reason = reasonLLMBaseRefNotFound
		health.Message = message
		health.Errors = []error{controllerutils.NewMissingUpstreamDependencyError(reasonLLMBaseRefNotFound, message, nil)}
		return health
	}
	if len(conflicts) > 0 {
		message := "spec.llmInferenceService.baseRefs conflict with fields AIM Engine owns (routing, autoscaling, " +
			"replicas, workload topology, LoRA adapters): " + strings.Join(conflicts, "; ")
		health.State = constants.AIMStatusFailed
		health.Reason = reasonLLMBaseRefConflict
		health.Message = message
		health.Errors = []error{controllerutils.NewInvalidSpecError(reasonLLMBaseRefConflict, message, nil)}
		return health
	}

	health.State = constants.AIMStatusReady
	health.Reason = reasonLLMBaseRefsReady
	health.Message = "All spec.llmInferenceService.baseRefs resolved"
	return health
}

// llmAcceleratorResources are the resources whose count the profile's engine
// arguments (tensor parallelism) assume.
var llmAcceleratorResources = []corev1.ResourceName{constants.AMDGPUResourceName, constants.NVIDIAGPUResourceName}

// llmProfileContractOverrides lists, per user-supplied config, the fields that
// override the resolved profile's contract: KServe fields the profile's engine
// launch makes ineffective, the main container's image and launch, its
// accelerator count, and the node affinity that pins the accelerator model.
// Accelerator counts are skipped when spec.resources is set, since the
// AIMService's resources then take precedence over every config.
func llmProfileContractOverrides(
	service *aimv1alpha2.AIMService,
	refs []llmBaseRef,
	profileConfig *kservev1alpha2.LLMInferenceServiceConfig,
) []string {
	profileSpec := profileConfig.Spec
	profileContainer := llmMainContainer(profileSpec.Template)
	var overrides []string
	for _, ref := range refs {
		if ref.config == nil {
			continue
		}
		spec := ref.config.Spec
		var fields []string
		if spec.Parallelism != nil {
			fields = append(fields, "spec.parallelism")
		}
		if spec.KVCacheOffloading != nil {
			fields = append(fields, "spec.kvCacheOffloading")
		}
		if container := llmMainContainer(spec.Template); container != nil {
			fields = append(fields, llmContainerOverrides(service, container, profileContainer)...)
		}
		if spec.Template != nil && spec.Template.Affinity != nil && spec.Template.Affinity.NodeAffinity != nil {
			var profileAffinity *corev1.NodeAffinity
			if profileSpec.Template != nil && profileSpec.Template.Affinity != nil {
				profileAffinity = profileSpec.Template.Affinity.NodeAffinity
			}
			if !equality.Semantic.DeepEqual(spec.Template.Affinity.NodeAffinity, profileAffinity) {
				fields = append(fields, "spec.template.affinity.nodeAffinity")
			}
		}
		if len(fields) > 0 {
			overrides = append(overrides, fmt.Sprintf("%s sets %s", ref.name, strings.Join(fields, ", ")))
		}
	}
	return overrides
}

// llmContainerOverrides lists the main-container fields of a user-supplied
// config that differ from the profile's.
func llmContainerOverrides(service *aimv1alpha2.AIMService, container, profileContainer *corev1.Container) []string {
	profile := corev1.Container{}
	if profileContainer != nil {
		profile = *profileContainer
	}
	prefix := "spec.template.containers[" + serving.LLMInferenceServiceContainerName + "]."
	var fields []string
	if container.Image != "" && container.Image != profile.Image {
		fields = append(fields, prefix+"image")
	}
	if len(container.Command) > 0 && !slices.Equal(container.Command, profile.Command) {
		fields = append(fields, prefix+"command")
	}
	if len(container.Args) > 0 && !slices.Equal(container.Args, profile.Args) {
		fields = append(fields, prefix+"args")
	}
	if service.Spec.Resources != nil {
		return fields
	}
	for _, list := range []struct {
		name            string
		values, profile corev1.ResourceList
	}{
		{"requests", container.Resources.Requests, profile.Resources.Requests},
		{"limits", container.Resources.Limits, profile.Resources.Limits},
	} {
		for _, name := range llmAcceleratorResources {
			value, ok := list.values[name]
			if !ok {
				continue
			}
			if profileValue, ok := list.profile[name]; !ok || !value.Equal(profileValue) {
				fields = append(fields, fmt.Sprintf("%sresources.%s[%s]", prefix, list.name, name))
			}
		}
	}
	return fields
}

func llmMainContainer(template *corev1.PodSpec) *corev1.Container {
	if template == nil {
		return nil
	}
	for i := range template.Containers {
		if template.Containers[i].Name == serving.LLMInferenceServiceContainerName {
			return &template.Containers[i]
		}
	}
	return nil
}

// setProfileContractCondition publishes the informational ProfileContractIntact
// condition for an LLMInferenceService-served service with baseRefs. It is
// removed while a config or the profile projection is unresolved; the baseRefs
// and profile components report those states.
func setProfileContractCondition(cm *controllerutils.ConditionManager, obs ServiceObservation) {
	if cm == nil {
		return
	}
	condType := aimv1alpha1.AIMServiceConditionProfileContractIntact
	if !obs.servesLLM || len(obs.llmBaseRefs) == 0 || obs.desiredProfileLLMConfig == nil {
		cm.Delete(condType)
		return
	}
	for _, ref := range obs.llmBaseRefs {
		if ref.err != nil || ref.config == nil {
			cm.Delete(condType)
			return
		}
	}

	overrides := llmProfileContractOverrides(obs.service, obs.llmBaseRefs, obs.desiredProfileLLMConfig)
	if len(overrides) == 0 {
		cm.Set(condType, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonNoProfileOverrides,
			"spec.llmInferenceService.baseRefs leave the profile's engine launch, accelerator count, and node affinity in place",
			controllerutils.Silent())
		return
	}
	cm.Set(condType, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonProfileContractOverridden,
		"spec.llmInferenceService.baseRefs override the profile's contract, so the profile's engine settings may "+
			"not apply (spec.parallelism and spec.kvCacheOffloading have no effect with the profile's engine "+
			"launch): "+strings.Join(overrides, "; "),
		controllerutils.WithWarningEvent())
}

// llmBaseRefsUsable reports whether the LLMInferenceService may be planned
// with the user-supplied configs.
func llmBaseRefsUsable(refs []llmBaseRef) bool {
	for _, ref := range refs {
		if ref.err != nil || ref.config == nil || len(llmConfigConflicts(ref.config)) > 0 {
			return false
		}
	}
	return true
}
