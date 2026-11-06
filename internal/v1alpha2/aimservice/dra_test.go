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
	"errors"
	"strings"
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

func withAllocationMode(mode aimv1alpha1.AcceleratorAllocationMode) *aimv1alpha1.AIMAcceleratorAllocationConfig {
	return &aimv1alpha1.AIMAcceleratorAllocationConfig{Mode: mode}
}

func profileStatusWith(devicePlugin, dra int32) *aimv1alpha2.AIMProfileStatus {
	return &aimv1alpha2.AIMProfileStatus{
		Status:                    constants.AIMStatusReady,
		MatchingNodes:             devicePlugin + dra,
		MatchingNodesByAllocation: &aimv1alpha2.AIMProfileMatchingNodes{DevicePlugin: devicePlugin, DRA: dra},
	}
}

func TestDecideAllocation(t *testing.T) {
	amd := sampleProfileSpec()
	nvidia := sampleProfileSpec()
	nvidia.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA
	cpu := sampleProfileSpec()
	cpu.AcceleratorType = aimv1alpha1.AcceleratorTypeCPU
	devicePluginISVC := &servingv1beta1.InferenceService{}
	draISVC := &servingv1beta1.InferenceService{}
	draISVC.Spec.Predictor.ResourceClaims = []corev1.PodResourceClaim{{Name: draClaimName}}

	tests := []struct {
		name          string
		serviceMode   aimv1alpha1.AcceleratorAllocationMode
		runtimeMode   aimv1alpha1.AcceleratorAllocationMode
		spec          *aimv1alpha2.AIMProfileSpecCommon
		status        *aimv1alpha2.AIMProfileStatus
		existing      *servingv1beta1.InferenceService
		want          aimv1alpha1.AcceleratorAllocationMode
		wantErrSubstr string
	}{
		{name: "CPU profiles need no allocation", spec: cpu, status: profileStatusWith(1, 1), want: ""},
		{name: "Auto prefers DRA nodes", spec: amd, status: profileStatusWith(1, 1), want: aimv1alpha1.AcceleratorAllocationDRA},
		{name: "Auto without DRA nodes", spec: amd, status: profileStatusWith(1, 0), want: aimv1alpha1.AcceleratorAllocationDevicePlugin},
		{
			name: "Auto keeps a running device-plugin service on the device plugin", spec: amd,
			status: profileStatusWith(1, 2), existing: devicePluginISVC, want: aimv1alpha1.AcceleratorAllocationDevicePlugin,
		},
		{
			name: "Auto keeps a running DRA service on DRA", spec: amd,
			status: profileStatusWith(2, 0), existing: draISVC, want: aimv1alpha1.AcceleratorAllocationDRA,
		},
		{name: "Auto uses the device plugin for NVIDIA", spec: nvidia, status: profileStatusWith(1, 0), want: aimv1alpha1.AcceleratorAllocationDevicePlugin},
		{
			name: "explicit DevicePlugin", serviceMode: aimv1alpha1.AcceleratorAllocationDevicePlugin, spec: amd,
			status: profileStatusWith(0, 1), want: aimv1alpha1.AcceleratorAllocationDevicePlugin,
		},
		{
			name: "explicit DRA without DRA nodes", serviceMode: aimv1alpha1.AcceleratorAllocationDRA, spec: amd,
			status: profileStatusWith(1, 0), existing: devicePluginISVC, want: aimv1alpha1.AcceleratorAllocationDRA,
		},
		{
			name: "explicit DRA on an NVIDIA profile", serviceMode: aimv1alpha1.AcceleratorAllocationDRA, spec: nvidia,
			status: profileStatusWith(1, 0), want: aimv1alpha1.AcceleratorAllocationDRA, wantErrSubstr: "needs an AMD GPU profile",
		},
		{
			name: "runtime config mode applies", runtimeMode: aimv1alpha1.AcceleratorAllocationDevicePlugin, spec: amd,
			status: profileStatusWith(1, 1), want: aimv1alpha1.AcceleratorAllocationDevicePlugin,
		},
		{
			name: "service mode overrides the runtime config", serviceMode: aimv1alpha1.AcceleratorAllocationDRA,
			runtimeMode: aimv1alpha1.AcceleratorAllocationDevicePlugin, spec: amd,
			status: profileStatusWith(1, 1), want: aimv1alpha1.AcceleratorAllocationDRA,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &aimv1alpha1.AIMService{}
			if tt.serviceMode != "" {
				service.Spec.AcceleratorAllocation = withAllocationMode(tt.serviceMode)
			}
			var runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
			if tt.runtimeMode != "" {
				runtimeConfig = &aimv1alpha1.AIMRuntimeConfigCommon{}
				runtimeConfig.AcceleratorAllocation = withAllocationMode(tt.runtimeMode)
			}
			got := decideAllocation(service, "profile", tt.spec, tt.status, tt.existing, runtimeConfig)
			if got.mode != tt.want {
				t.Errorf("mode = %q, want %q", got.mode, tt.want)
			}
			if (got.err != nil) != (tt.wantErrSubstr != "") || (got.err != nil && !strings.Contains(got.err.Error(), tt.wantErrSubstr)) {
				t.Errorf("err = %v, want one containing %q", got.err, tt.wantErrSubstr)
			}
		})
	}
}

func TestBuildResourceClaimTemplate(t *testing.T) {
	service := &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"}}
	spec := sampleProfileSpec()
	spec.AcceleratorCount = 4
	request, ok := aimprofile.NewDRAClaimRequest(*spec, aimprofile.ResolveProfileResources(*spec))
	if !ok {
		t.Fatal("NewDRAClaimRequest() = false for an MI300X profile")
	}

	template, err := buildResourceClaimTemplate(service, "gpu.amd.com", request)
	if err != nil {
		t.Fatal(err)
	}
	if template.Namespace != "ns" || !strings.HasPrefix(template.Name, testServiceName+"-gpus-") {
		t.Errorf("template key = %s/%s", template.Namespace, template.Name)
	}
	devices := template.Spec.Spec.Devices
	if len(devices.Requests) != 1 || devices.Requests[0].Exactly == nil {
		t.Fatalf("requests = %+v, want one exact request", devices.Requests)
	}
	exact := devices.Requests[0].Exactly
	if exact.DeviceClassName != "gpu.amd.com" || exact.Count != 4 || exact.Selectors[0].CEL.Expression != request.Selector {
		t.Errorf("request = %+v, want 4 devices of class gpu.amd.com matching %q", exact, request.Selector)
	}
	if len(devices.Constraints) != 1 || string(*devices.Constraints[0].MatchAttribute) != "gpu.amd.com/deviceID" {
		t.Errorf("constraints = %+v, want all devices of one model", devices.Constraints)
	}

	single := request
	single.Count = 1
	one, err := buildResourceClaimTemplate(service, "gpu.amd.com", single)
	if err != nil {
		t.Fatal(err)
	}
	if one.Name == template.Name {
		t.Error("a different device request must use a different template name: pod claims cannot change")
	}
	if len(one.Spec.Spec.Devices.Constraints) != 0 {
		t.Errorf("a single-device request has constraints %+v", one.Spec.Spec.Devices.Constraints)
	}
}

func draObservation(t *testing.T, service *aimv1alpha1.AIMService) ServiceObservation {
	t.Helper()
	spec := sampleProfileSpec()
	obs := ServiceObservation{
		resolvedProfileSpec:   spec,
		resolvedProfileStatus: profileStatusWith(0, 1),
		profileName:           testProfileA,
		isvcName:              "service-isvc",
	}
	obs.service = service
	obs.allocation = allocationDecision{mode: aimv1alpha1.AcceleratorAllocationDRA}
	obs.profileRuntimeName = serving.DRARuntimeName(testProfileA)
	return obs
}

func TestBuildInferenceServiceFromProfile_DRA(t *testing.T) {
	service := &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"}}
	service.Spec.Resources = &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("16")}}
	obs := draObservation(t, service)
	obs.effectiveResources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("16"),
			corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("1"),
		},
		Limits: corev1.ResourceList{corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("1")},
	}

	if isvc := buildInferenceServiceFromProfile(service, obs); isvc != nil {
		t.Fatal("a DRA InferenceService was built without its ResourceClaimTemplate")
	}

	composeAllocation(&obs)
	if obs.configErr != nil || obs.desiredClaimTemplate == nil {
		t.Fatalf("composeAllocation() configErr = %v, template = %v", obs.configErr, obs.desiredClaimTemplate)
	}
	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatal("buildInferenceServiceFromProfile() = nil")
	}
	if got := *isvc.Spec.Predictor.Model.Runtime; got != serving.DRARuntimeName(testProfileA) {
		t.Errorf("runtime = %s, want the DRA variant", got)
	}
	claims := isvc.Spec.Predictor.ResourceClaims
	if len(claims) != 1 || claims[0].Name != draClaimName || *claims[0].ResourceClaimTemplateName != obs.desiredClaimTemplate.Name {
		t.Errorf("pod claims = %+v, want one from %s", claims, obs.desiredClaimTemplate.Name)
	}
	resources := isvc.Spec.Predictor.Model.Resources
	if len(resources.Claims) != 1 || resources.Claims[0].Name != draClaimName {
		t.Errorf("container claims = %+v", resources.Claims)
	}
	if _, ok := resources.Requests[corev1.ResourceName(constants.AMDGPUResourceName)]; ok {
		t.Errorf("DRA container requests %s: %v", constants.AMDGPUResourceName, resources.Requests)
	}
	if cpu := resources.Requests[corev1.ResourceCPU]; cpu.String() != "16" {
		t.Errorf("CPU request = %s, want the service override", cpu.String())
	}
}

func TestComposeAllocationRejectsInvalidDRAServices(t *testing.T) {
	gpuOverride := &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"}}
	gpuOverride.Spec.Resources = &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("2")},
	}
	obs := draObservation(t, gpuOverride)
	composeAllocation(&obs)
	if obs.configErr == nil || !strings.Contains(obs.configErr.Error(), "spec.resources requests amd.com/gpu") {
		t.Errorf("configErr = %v, want the device-plugin GPU override rejected", obs.configErr)
	}

	unknownModel := draObservation(t, &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"}})
	unknownModel.resolvedProfileSpec.AcceleratorModel = "MI999"
	composeAllocation(&unknownModel)
	if unknownModel.configErr == nil || !strings.Contains(unknownModel.configErr.Error(), "no AMD GPU device IDs are known") {
		t.Errorf("configErr = %v, want the unknown model rejected", unknownModel.configErr)
	}

	nvidia := draObservation(t, &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"}})
	nvidia.allocation.err = errTestAllocation
	composeAllocation(&nvidia)
	if nvidia.configErr != errTestAllocation {
		t.Errorf("configErr = %v, want the allocation decision's error", nvidia.configErr)
	}
}

var errTestAllocation = errors.New("allocation")

func TestAllocationAvailable(t *testing.T) {
	dra := allocationDecision{mode: aimv1alpha1.AcceleratorAllocationDRA}
	devicePlugin := allocationDecision{mode: aimv1alpha1.AcceleratorAllocationDevicePlugin}
	legacy := &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady}

	if !allocationAvailable(devicePlugin, legacy) || allocationAvailable(dra, legacy) {
		t.Error("a status without the breakdown made profiles Ready through device-plugin nodes only")
	}
	if allocationAvailable(devicePlugin, profileStatusWith(0, 2)) || !allocationAvailable(dra, profileStatusWith(0, 2)) {
		t.Error("allocationAvailable does not follow the breakdown")
	}
	message := noNodesForAllocationMessage("p", devicePlugin, profileStatusWith(0, 2).MatchingNodesByAllocation)
	if !strings.Contains(message, "set acceleratorAllocation.mode to DRA") {
		t.Errorf("message = %q, want a pointer to DRA", message)
	}
}

func TestStaleClaimTemplateCleanup(t *testing.T) {
	obs := ServiceObservation{}
	named := func(name string) resourcev1.ResourceClaimTemplate {
		return resourcev1.ResourceClaimTemplate{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"}}
	}
	current := named("svc-gpus-current")
	obs.desiredClaimTemplate = &current
	obs.claimTemplates = controllerutils.FetchResult[[]resourcev1.ResourceClaimTemplate]{
		Value: []resourcev1.ResourceClaimTemplate{current, named("svc-gpus-old")},
	}
	plan := controllerutils.PlanResult{}
	planStaleClaimTemplateCleanup(&plan, obs)
	deleted := plan.GetToDelete()
	if len(deleted) != 1 || deleted[0].GetName() != "svc-gpus-old" {
		t.Errorf("deleted = %v, want only svc-gpus-old", deleted)
	}

	obs.desiredClaimTemplate = nil
	plan = controllerutils.PlanResult{}
	planStaleClaimTemplateCleanup(&plan, obs)
	if len(plan.GetToDelete()) != 2 {
		t.Errorf("a service that stopped using DRA keeps templates: %v", plan.GetToDelete())
	}
}
