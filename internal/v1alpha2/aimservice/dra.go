// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// draClaimName names the GPU claim in the pod spec and its device request.
const draClaimName = "gpus"

// allocationDecision is how a service's inference pods request GPUs.
type allocationDecision struct {
	// mode is DRA or DevicePlugin; empty when the profile needs no GPUs.
	mode aimv1alpha1.AcceleratorAllocationMode
	// err is set when the requested mode cannot serve the profile at all.
	err error
}

func (d allocationDecision) dra() bool {
	return d.mode == aimv1alpha1.AcceleratorAllocationDRA
}

// requestedAllocationMode is the service's mode, else the runtime config's,
// else Auto.
func requestedAllocationMode(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) aimv1alpha1.AcceleratorAllocationMode {
	if service.Spec.AcceleratorAllocation != nil && service.Spec.AcceleratorAllocation.Mode != "" {
		return service.Spec.AcceleratorAllocation.Mode
	}
	if runtimeConfig != nil && runtimeConfig.AcceleratorAllocation != nil && runtimeConfig.AcceleratorAllocation.Mode != "" {
		return runtimeConfig.AcceleratorAllocation.Mode
	}
	return aimv1alpha1.AcceleratorAllocationAuto
}

// decideAllocation resolves the allocation mode of a service with a GPU
// profile. An explicit mode is used as is. Auto keeps the mode of an existing
// InferenceService, so adding or removing DRA nodes never moves a running
// service; a new service uses DRA when only DRA nodes have the profile's
// GPUs. nodes are the matching-node counts for the service's resources.
func decideAllocation(
	service *aimv1alpha1.AIMService,
	profileName string,
	spec *aimv1alpha2.AIMProfileSpecCommon,
	nodes *aimv1alpha2.AIMProfileMatchingNodes,
	existing *servingv1beta1.InferenceService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) allocationDecision {
	if spec == nil || spec.AcceleratorType != aimv1alpha1.AcceleratorTypeGPU {
		return allocationDecision{}
	}
	dp := allocationDecision{mode: aimv1alpha1.AcceleratorAllocationDevicePlugin}
	dra := allocationDecision{mode: aimv1alpha1.AcceleratorAllocationDRA}
	amd := spec.AcceleratorVendor == "" || spec.AcceleratorVendor == aimv1alpha1.AcceleratorVendorAMD

	switch requestedAllocationMode(service, runtimeConfig) {
	case aimv1alpha1.AcceleratorAllocationDevicePlugin:
		return dp
	case aimv1alpha1.AcceleratorAllocationDRA:
		if !amd {
			dra.err = fmt.Errorf("acceleratorAllocation.mode DRA needs an AMD GPU profile; profile %q targets %s GPUs",
				profileName, spec.AcceleratorVendor)
		}
		return dra
	}

	if !amd {
		return dp
	}
	if existing != nil {
		if inferenceServiceUsesDRA(existing) {
			return dra
		}
		return dp
	}
	// Device-plugin GPUs count against amd.com/gpu quotas and tolerate GPU
	// taints through ExtendedResourceToleration, so Auto uses DRA only when no
	// device-plugin node fits.
	if nodes != nil && nodes.DevicePlugin == 0 && nodes.DRA > 0 {
		return dra
	}
	return dp
}

func inferenceServiceUsesDRA(isvc *servingv1beta1.InferenceService) bool {
	for _, claim := range isvc.Spec.Predictor.ResourceClaims {
		if claim.Name == draClaimName {
			return true
		}
	}
	return false
}

// allocationAvailable reports whether a Ready profile has nodes for the
// decided allocation mode. A profile status written before the breakdown
// existed made a profile Ready only through device-plugin nodes.
func allocationAvailable(decision allocationDecision, status *aimv1alpha2.AIMProfileStatus) bool {
	byAllocation := status.MatchingNodesByAllocation
	if byAllocation == nil {
		return !decision.dra()
	}
	if decision.dra() {
		return byAllocation.DRA > 0
	}
	return byAllocation.DevicePlugin > 0
}

// noNodesForAllocationMessage explains why a ready profile cannot serve the
// service with its allocation mode, pointing to the mode that would.
func noNodesForAllocationMessage(
	profileName string,
	decision allocationDecision,
	byAllocation *aimv1alpha2.AIMProfileMatchingNodes,
) string {
	var devicePlugin, dra int32
	if byAllocation != nil {
		devicePlugin, dra = byAllocation.DevicePlugin, byAllocation.DRA
	}
	message := fmt.Sprintf("No node can serve profile %s with %s allocation (device-plugin nodes: %d, DRA nodes: %d)",
		profileName, decision.mode, devicePlugin, dra)
	switch {
	case decision.dra() && devicePlugin > 0:
		message += "; set acceleratorAllocation.mode to DevicePlugin to use the device-plugin nodes"
	case !decision.dra() && dra > 0:
		message += "; set acceleratorAllocation.mode to DRA to use the DRA nodes"
	}
	return message
}

// draDeviceClassName is the DeviceClass GPU claims request devices from.
func draDeviceClassName(runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) string {
	if runtimeConfig != nil && runtimeConfig.DRA != nil && runtimeConfig.DRA.DeviceClassName != "" {
		return runtimeConfig.DRA.DeviceClassName
	}
	return constants.AMDGPUDRADriverName
}

// buildResourceClaimTemplate returns the service's ResourceClaimTemplate for
// a DRA device request. Its name carries a hash of the request: a pod's
// claims cannot change, so a different request needs a new template, and the
// InferenceService's pod template then rolls out.
func buildResourceClaimTemplate(
	service *aimv1alpha1.AIMService,
	deviceClassName string,
	request aimprofile.DRAClaimRequest,
) (*resourcev1.ResourceClaimTemplate, error) {
	name, err := utils.GenerateDerivedName(
		[]string{service.Name, draClaimName},
		utils.WithHashSource(deviceClassName, request.Count, request.Selector),
		utils.WithMaxLength(utils.MaxKubernetesNameLength),
	)
	if err != nil {
		return nil, fmt.Errorf("generate ResourceClaimTemplate name: %w", err)
	}

	claim := resourcev1.DeviceClaim{
		Requests: []resourcev1.DeviceRequest{{
			Name: draClaimName,
			Exactly: &resourcev1.ExactDeviceRequest{
				DeviceClassName: deviceClassName,
				AllocationMode:  resourcev1.DeviceAllocationModeExactCount,
				Count:           request.Count,
				Selectors: []resourcev1.DeviceSelector{{
					CEL: &resourcev1.CELDeviceSelector{Expression: request.Selector},
				}},
			},
		}},
	}
	if request.Count > 1 {
		// Tensor parallelism needs every GPU of the claim to be the same model.
		claim.Constraints = []resourcev1.DeviceConstraint{{
			Requests:       []string{draClaimName},
			MatchAttribute: ptr.To(resourcev1.FullyQualifiedName(constants.AMDGPUDRADriverName + "/deviceID")),
		}}
	}

	serviceLabel, _ := utils.SanitizeLabelValue(service.Name)
	return &resourcev1.ResourceClaimTemplate{
		TypeMeta: metav1.TypeMeta{APIVersion: resourcev1.SchemeGroupVersion.String(), Kind: "ResourceClaimTemplate"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelK8sComponent: constants.ComponentInference,
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
				constants.LabelService:      serviceLabel,
			},
		},
		Spec: resourcev1.ResourceClaimTemplateSpec{
			Spec: resourcev1.ResourceClaimSpec{Devices: claim},
		},
	}, nil
}

// composeAllocation validates the allocation decision and, for DRA services,
// builds the ResourceClaimTemplate their pods claim GPUs from. Problems are
// reported through configErr.
func composeAllocation(obs *ServiceObservation) {
	if obs.configErr != nil || obs.resolvedProfileSpec == nil {
		return
	}
	if obs.allocation.err != nil {
		obs.configErr = obs.allocation.err
		return
	}
	if !obs.allocation.dra() {
		return
	}
	if requestsDevicePluginGPU(obs.service.Spec.Resources) {
		obs.configErr = fmt.Errorf(
			"spec.resources requests %s, but the service allocates GPUs through DRA; "+
				"remove it or set acceleratorAllocation.mode to DevicePlugin",
			constants.AMDGPUResourceName,
		)
		return
	}

	resources := obs.effectiveResources
	if resources == nil {
		resources = aimprofile.ResolveProfileResources(*obs.resolvedProfileSpec)
	}
	request, ok := aimprofile.NewDRAClaimRequest(*obs.resolvedProfileSpec, resources)
	if !ok {
		obs.configErr = fmt.Errorf(
			"profile %q cannot be served through DRA: no AMD GPU device IDs are known for accelerator model %q",
			obs.profileName, obs.resolvedProfileSpec.AcceleratorModel,
		)
		return
	}
	template, err := buildResourceClaimTemplate(obs.service, draDeviceClassName(obs.mergedRuntimeConfig.Value), request)
	if err != nil {
		obs.configErr = err
		return
	}
	obs.desiredClaimTemplate = template
}

// planStaleClaimTemplateCleanup deletes the service's ResourceClaimTemplates
// other than the one its pods now claim from: those of earlier device
// requests, and all of them once the service no longer uses DRA. Claims that
// running pods already hold are not affected.
func planStaleClaimTemplateCleanup(plan *controllerutils.PlanResult, obs ServiceObservation) {
	if !obs.claimTemplates.OK() {
		return
	}
	for i := range obs.claimTemplates.Value {
		template := &obs.claimTemplates.Value[i]
		if obs.desiredClaimTemplate != nil && template.Name == obs.desiredClaimTemplate.Name {
			continue
		}
		plan.Delete(template)
	}
}

// fetchResourceClaimTemplates lists the ResourceClaimTemplates the service
// owns, so templates of earlier device requests can be removed.
func fetchResourceClaimTemplates(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[[]resourcev1.ResourceClaimTemplate] {
	serviceLabel, _ := utils.SanitizeLabelValue(service.Name)
	var list resourcev1.ResourceClaimTemplateList
	if err := c.List(ctx, &list,
		client.InNamespace(service.Namespace),
		client.MatchingLabels{constants.LabelService: serviceLabel},
	); err != nil {
		// A cluster without resource.k8s.io/v1 has no templates to clean up.
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return controllerutils.FetchResult[[]resourcev1.ResourceClaimTemplate]{}
		}
		return controllerutils.FetchResult[[]resourcev1.ResourceClaimTemplate]{Error: err}
	}
	owned := make([]resourcev1.ResourceClaimTemplate, 0, len(list.Items))
	for i := range list.Items {
		if metav1.IsControlledBy(&list.Items[i], service) {
			owned = append(owned, list.Items[i])
		}
	}
	return controllerutils.FetchResult[[]resourcev1.ResourceClaimTemplate]{Value: owned}
}

// applyDRAClaim makes the predictor request its GPUs through the service's
// ResourceClaimTemplate: a pod-level claim, consumed by the model container.
func applyDRAClaim(isvc *servingv1beta1.InferenceService, template *resourcev1.ResourceClaimTemplate) {
	isvc.Spec.Predictor.ResourceClaims = []corev1.PodResourceClaim{{
		Name:                      draClaimName,
		ResourceClaimTemplateName: ptr.To(template.Name),
	}}
	if isvc.Spec.Predictor.Model != nil {
		isvc.Spec.Predictor.Model.Resources.Claims = []corev1.ResourceClaim{{Name: draClaimName}}
	}
	// The ExtendedResourceToleration admission plugin adds this toleration to
	// pods that request amd.com/gpu. DRA pods don't request it, and the claim
	// already limits them to GPU nodes.
	gpuToleration := corev1.Toleration{
		Key:      constants.AMDGPUResourceName,
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	for i := range isvc.Spec.Predictor.Tolerations {
		if isvc.Spec.Predictor.Tolerations[i].MatchToleration(&gpuToleration) {
			return
		}
	}
	isvc.Spec.Predictor.Tolerations = append(isvc.Spec.Predictor.Tolerations, gpuToleration)
}

// withoutDevicePluginGPU drops the AMD device-plugin GPU resource from a
// resource block, for services that request GPUs through DRA.
func withoutDevicePluginGPU(resources corev1.ResourceRequirements) corev1.ResourceRequirements {
	out := *resources.DeepCopy()
	delete(out.Requests, corev1.ResourceName(constants.AMDGPUResourceName))
	delete(out.Limits, corev1.ResourceName(constants.AMDGPUResourceName))
	return out
}

// requestsDevicePluginGPU reports whether a service's own resources name the
// AMD device-plugin GPU resource, which DRA services must not request.
func requestsDevicePluginGPU(resources *corev1.ResourceRequirements) bool {
	if resources == nil {
		return false
	}
	name := corev1.ResourceName(constants.AMDGPUResourceName)
	_, inRequests := resources.Requests[name]
	_, inLimits := resources.Limits[name]
	return inRequests || inLimits
}
