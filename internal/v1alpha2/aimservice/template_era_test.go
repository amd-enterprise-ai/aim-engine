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
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type countingStatusWriter struct {
	client.StatusWriter
	updates int
}

func (w *countingStatusWriter) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.SubResourceUpdateOption,
) error {
	w.updates++
	return w.StatusWriter.Update(ctx, obj, opts...)
}

// A service that left the AIMServiceTemplate pipeline arrives with the status
// that pipeline wrote. The first profile-pipeline reconcile must replace it
// with profile status in its single status write.
func TestPipelineRun_ClearsTemplateEraStatusInOneStatusWrite(t *testing.T) {
	ctx := context.Background()
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ReconcilerPipeline: "template",
			Status:             constants.AIMStatusRunning,
			Conditions: []metav1.Condition{
				{Type: "ModelReady", Status: metav1.ConditionTrue, Reason: aimv1alpha1.AIMServiceReasonModelResolved},
				{Type: "TemplateReady", Status: metav1.ConditionFalse, Reason: aimv1alpha1.AIMServiceReasonTemplateNotReady},
				{Type: "CacheReady", Status: metav1.ConditionTrue, Reason: aimv1alpha1.AIMServiceReasonCacheReady},
				{
					Type:   controllerutils.ConditionTypeMigrationRequired,
					Status: metav1.ConditionTrue,
					Reason: "TemplateAPIDeprecated",
				},
			},
			ResolvedModel:    &aimv1alpha1.AIMResolvedReference{Name: "template-era-model", UID: "model-uid"},
			ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{Name: "template", UID: "template-uid"},
			Cache: &aimv1alpha1.AIMServiceCacheStatus{
				TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "template-cache"},
				RetryAttempts:    1,
			},
		},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterProfileName, UID: "profile-uid"},
		Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
		Status:     aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	c, pipeline := newProfileRuntimePipelineFixture(t, service, clusterProfile)
	pipeline.Clientset = kubernetesfake.NewClientset()
	statusWriter := &countingStatusWriter{StatusWriter: pipeline.StatusClient}
	pipeline.StatusClient = statusWriter

	serviceKey := client.ObjectKeyFromObject(service)
	runProfileServicePipelineCycle(t, ctx, pipeline, getProfileServiceForTest(t, ctx, c, serviceKey))

	if statusWriter.updates != 1 {
		t.Fatalf("status writes = %d, want 1", statusWriter.updates)
	}
	status := getProfileServiceForTest(t, ctx, c, serviceKey).Status
	for _, conditionType := range templateOwnedConditionTypes {
		if condition := meta.FindStatusCondition(status.Conditions, conditionType); condition != nil {
			t.Errorf("template-era condition %s survived: %#v", conditionType, condition)
		}
	}
	if status.ResolvedTemplate != nil || status.Cache != nil {
		t.Fatalf("template-era references survived: resolvedTemplate=%#v cache=%#v", status.ResolvedTemplate, status.Cache)
	}
	if status.ResolvedModel == nil || status.ResolvedModel.Name != testModelIDFP8 || status.ResolvedModel.UID != "" {
		t.Fatalf("resolvedModel = %#v, want the resolved profile's model ID %q", status.ResolvedModel, testModelIDFP8)
	}
	if status.ResolvedProfile == nil || status.ResolvedProfile.Name != testClusterProfileName {
		t.Fatalf("resolvedProfile = %#v, want %q", status.ResolvedProfile, testClusterProfileName)
	}
	if profileReady := meta.FindStatusCondition(status.Conditions, "ProfileReady"); profileReady == nil ||
		profileReady.Status != metav1.ConditionTrue {
		t.Fatalf("ProfileReady = %#v, want True", profileReady)
	}
	if status.ReconcilerPipeline != "" {
		t.Fatalf("reconcilerPipeline = %q, want it cleared", status.ReconcilerPipeline)
	}
}

func TestDecorateStatus_TemplateFieldsIgnoredNamesSetFields(t *testing.T) {
	custom := &aimv1alpha1.AIMServiceModelCustom{BaseImage: "docker.io/amdenterpriseai/aim-base:0.7.0"}
	profile := &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA}
	tests := []struct {
		name       string
		spec       aimv1alpha1.AIMServiceSpec
		wantFields string
	}{
		{
			name: "no template-era fields",
			spec: aimv1alpha1.AIMServiceSpec{Profile: profile},
		},
		{
			name:       "spec.template",
			spec:       aimv1alpha1.AIMServiceSpec{Profile: profile, Template: &aimv1alpha1.AIMServiceTemplateConfig{Name: "t"}},
			wantFields: "spec.template",
		},
		{
			name:       "spec.overrides",
			spec:       aimv1alpha1.AIMServiceSpec{Profile: profile, Overrides: &aimv1alpha1.AIMServiceOverrides{}},
			wantFields: "spec.overrides",
		},
		{
			name:       "spec.model.custom",
			spec:       aimv1alpha1.AIMServiceSpec{Profile: profile, Model: &aimv1alpha1.AIMServiceModel{Custom: custom}},
			wantFields: "spec.model.custom",
		},
		{
			name: "all template-era fields",
			spec: aimv1alpha1.AIMServiceSpec{
				Model:     &aimv1alpha1.AIMServiceModel{Custom: custom},
				Template:  &aimv1alpha1.AIMServiceTemplateConfig{},
				Overrides: &aimv1alpha1.AIMServiceOverrides{},
			},
			wantFields: "spec.template, spec.overrides, spec.model.custom",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Start from a stale warning so the no-fields case shows it clears.
			cm := controllerutils.NewConditionManager([]metav1.Condition{{
				Type:    controllerutils.ConditionTypeTemplateFieldsIgnored,
				Status:  metav1.ConditionTrue,
				Reason:  controllerutils.ReasonTemplateFieldsSet,
				Message: "stale",
			}})
			(&ProfileServiceReconciler{}).DecorateStatus(
				&aimv1alpha1.AIMServiceStatus{},
				cm,
				ServiceObservation{ServiceFetchResult: ServiceFetchResult{
					service: &aimv1alpha2.AIMService{Spec: test.spec},
				}},
			)

			got := cm.Get(controllerutils.ConditionTypeTemplateFieldsIgnored)
			if test.wantFields == "" {
				if got != nil {
					t.Fatalf("TemplateFieldsIgnored = %#v, want no condition", got)
				}
				return
			}
			wantMessage := "These fields have no effect on a profile-based AIMService and can be removed: " + test.wantFields
			if got == nil ||
				got.Status != metav1.ConditionTrue ||
				got.Reason != controllerutils.ReasonTemplateFieldsSet ||
				got.Message != wantMessage {
				t.Fatalf("TemplateFieldsIgnored = %#v, want True/%s with message %q",
					got, controllerutils.ReasonTemplateFieldsSet, wantMessage)
			}
		})
	}
}
