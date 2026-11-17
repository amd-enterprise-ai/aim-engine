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
	"strings"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
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
func userLLMBaseRefs(service *aimv1alpha1.AIMService) []corev1.LocalObjectReference {
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
	service *aimv1alpha1.AIMService,
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
// replicas, the single-node workload shape, and LoRA adapters (AIM Engine's
// adapter contract).
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
			health.State = constants.AIMStatusFailed
			health.Reason = reasonFetchError
			health.Errors = append(health.Errors, controllerutils.NewInfrastructureError(
				reasonFetchError,
				fmt.Sprintf("Failed to fetch LLMInferenceServiceConfig %q", ref.name),
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
