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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
)

func TestLegacyAutoCustomModelRegeneratesTemplateWithoutPersistingHydration(t *testing.T) {
	service, model := newLegacyAutoCustomFixture(t)
	original := model.DeepCopy()
	reconciler, k8sClient := newLegacyAutoCustomReconciler(t, service)

	fetch := reconciler.FetchRemoteState(
		context.Background(),
		k8sClient,
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
	)
	if fetch.legacyModelErr != nil || !fetch.legacyAutoCustom {
		t.Fatalf("compatibility fetch = (found %t, error %v)", fetch.legacyAutoCustom, fetch.legacyModelErr)
	}
	if fetch.legacyModel == nil ||
		!equality.Semantic.DeepEqual(fetch.legacyModel.Spec.ModelSources, service.Spec.Model.Custom.ModelSources) ||
		fetch.legacyModel.Spec.Custom == nil {
		t.Fatalf("legacy model was not hydrated before template generation: %#v", fetch.legacyModel)
	}

	obs := reconciler.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		fetch,
	)
	status := model.Status.DeepCopy()
	cm := controllerutils.NewConditionManager(nil)
	reconciler.DecorateStatus(status, cm, obs)
	if obs.Kind != aimv1alpha1.AIMModelKindCustom ||
		status.Kind != aimv1alpha1.AIMModelKindCustom ||
		status.SourceType != aimv1alpha1.AIMModelSourceTypeCustom {
		t.Fatalf("hydrated kind/source = %q/%q, want custom/custom", status.Kind, status.SourceType)
	}
	plan := reconciler.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		obs,
	)
	var template *aimv1alpha1.AIMServiceTemplate
	for _, object := range plan.GetToApply() {
		if candidate, ok := object.(*aimv1alpha1.AIMServiceTemplate); ok {
			template = candidate
			break
		}
	}
	if template == nil {
		t.Fatal("valid compatibility model did not regenerate a legacy AIMServiceTemplate")
	}
	if len(plan.GetToApply()) != 1 ||
		len(plan.GetToApplyWithoutOwnerRef()) != 0 ||
		len(plan.GetToApplyWithForce()) != 0 ||
		len(plan.GetToDelete()) != 0 {
		t.Fatalf("compatibility model was reinterpreted as a native image model: %#v", plan)
	}
	if !equality.Semantic.DeepEqual(template.Spec.ModelSources, service.Spec.Model.Custom.ModelSources) {
		t.Fatalf("generated template sources = %#v, want %#v", template.Spec.ModelSources, service.Spec.Model.Custom.ModelSources)
	}
	if !equality.Semantic.DeepEqual(model, original) {
		t.Fatalf("stored v1alpha2 model mutated during reconcile:\ngot:  %#v\nwant: %#v", model, original)
	}
}

func TestGrandfatheredLegacyAutoCustomModelRegeneratesTemplateWithoutRewrite(t *testing.T) {
	service, model := newGrandfatheredLegacyAutoCustomFixture(t)
	original := model.DeepCopy()
	reconciler, k8sClient := newLegacyAutoCustomReconciler(t, service)

	fetch := reconciler.FetchRemoteState(
		context.Background(),
		k8sClient,
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
	)
	if fetch.legacyModelErr != nil || fetch.legacyAutoCustom {
		t.Fatalf("grandfathered fetch = (compatibility %t, error %v)", fetch.legacyAutoCustom, fetch.legacyModelErr)
	}
	if fetch.legacyModel == nil ||
		!equality.Semantic.DeepEqual(fetch.legacyModel.Spec.ModelSources, service.Spec.Model.Custom.ModelSources) ||
		fetch.legacyModel.Spec.Custom == nil {
		t.Fatalf("grandfathered model did not retain predecessor fields: %#v", fetch.legacyModel)
	}

	obs := reconciler.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		fetch,
	)
	plan := reconciler.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		obs,
	)
	var template *aimv1alpha1.AIMServiceTemplate
	for _, object := range plan.GetToApply() {
		if candidate, ok := object.(*aimv1alpha1.AIMServiceTemplate); ok {
			template = candidate
			break
		}
	}
	if template == nil {
		t.Fatal("grandfathered model did not regenerate its legacy AIMServiceTemplate")
	}
	if !equality.Semantic.DeepEqual(template.Spec.ModelSources, service.Spec.Model.Custom.ModelSources) {
		t.Fatalf("generated template sources = %#v, want %#v", template.Spec.ModelSources, service.Spec.Model.Custom.ModelSources)
	}
	if _, annotated := model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec]; annotated {
		t.Fatal("grandfathered model was annotated during reconciliation")
	}
	if len(model.OwnerReferences) != 0 {
		t.Fatalf("grandfathered model gained owner references: %#v", model.OwnerReferences)
	}
	if !equality.Semantic.DeepEqual(model, original) {
		t.Fatalf("stored grandfathered model mutated during reconcile:\ngot:  %#v\nwant: %#v", model, original)
	}
}

func TestLegacyAutoCustomModelRetiresStaleNativeConditions(t *testing.T) {
	service, model := newLegacyAutoCustomFixture(t)
	model.Status.Conditions = []metav1.Condition{
		{
			Type:   componentDiscovery + controllerutils.ComponentConditionSuffix,
			Status: metav1.ConditionFalse,
			Reason: "StaleNativeFailure",
		},
		{
			Type:   componentNodeInventory + controllerutils.ComponentConditionSuffix,
			Status: metav1.ConditionFalse,
			Reason: "StaleNativeFailure",
		},
		{
			Type:   componentProfiles + controllerutils.ComponentConditionSuffix,
			Status: metav1.ConditionFalse,
			Reason: "StaleNativeFailure",
		},
	}
	reconciler, k8sClient := newLegacyAutoCustomReconciler(t, service)
	fetch := reconciler.FetchRemoteState(
		context.Background(),
		k8sClient,
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
	)
	obs := reconciler.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
		fetch,
	)
	status := model.Status.DeepCopy()
	cm := controllerutils.NewConditionManager(model.Status.Conditions)
	reconciler.DecorateStatus(status, cm, obs)

	for _, component := range []string{
		componentDiscovery,
		componentNodeInventory,
		componentProfiles,
		componentProfileSet,
	} {
		if condition := cm.Get(component + controllerutils.ComponentConditionSuffix); condition != nil {
			t.Errorf("stale native condition remains: %#v", condition)
		}
	}
}

func TestLegacyAutoCustomFailuresAreInvalidInputAndPlanNoChildMutations(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*aimv1alpha1.AIMService, *aimv1alpha2.AIMModel)
		includeSource bool
		wantText      string
	}{
		{
			name: "malformed payload",
			mutate: func(_ *aimv1alpha1.AIMService, model *aimv1alpha2.AIMModel) {
				model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec] = `{"baseImage":`
			},
			includeSource: true,
			wantText:      "MalformedJSON",
		},
		{
			name: "label-only provenance without predecessor fields",
			mutate: func(_ *aimv1alpha1.AIMService, model *aimv1alpha2.AIMModel) {
				delete(model.Annotations, legacyaimmodel.AnnotationLegacyAutoCustomSpec)
				model.OwnerReferences = nil
				model.Spec = aimv1alpha1.AIMModelSpec{}
			},
			includeSource: true,
			wantText:      "MissingPayload",
		},
		{
			name:          "orphaned source",
			includeSource: false,
			wantText:      "orphaned",
		},
		{
			name: "ownerless compatibility state",
			mutate: func(_ *aimv1alpha1.AIMService, model *aimv1alpha2.AIMModel) {
				model.OwnerReferences = nil
			},
			includeSource: true,
			wantText:      "ProvenanceMismatch",
		},
		{
			name: "conflicting controller provenance",
			mutate: func(_ *aimv1alpha1.AIMService, model *aimv1alpha2.AIMModel) {
				model.OwnerReferences[0].UID = types.UID("conflicting-uid")
			},
			includeSource: true,
			wantText:      "ProvenanceMismatch",
		},
		{
			name: "contradictory stored model",
			mutate: func(service *aimv1alpha1.AIMService, model *aimv1alpha2.AIMModel) {
				model.Spec.ModelSources = service.Spec.Model.Custom.ModelSources
			},
			includeSource: true,
			wantText:      "ContradictoryModel",
		},
		{
			name: "source service input changed",
			mutate: func(service *aimv1alpha1.AIMService, _ *aimv1alpha2.AIMModel) {
				service.Spec.Model.Custom.ModelSources[0].SourceURI = "s3://models/changed"
			},
			includeSource: true,
			wantText:      "SourceMismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, model := newLegacyAutoCustomFixture(t)
			if tt.mutate != nil {
				tt.mutate(service, model)
			}
			var objects []client.Object
			if tt.includeSource {
				objects = append(objects, service)
			}
			reconciler, k8sClient := newLegacyAutoCustomReconciler(t, objects...)

			fetch := reconciler.FetchRemoteState(
				context.Background(),
				k8sClient,
				controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			)
			if fetch.legacyModelErr == nil || !fetch.legacyAutoCustom {
				t.Fatalf("compatibility fetch = (found %t, error %v), want invalid state", fetch.legacyAutoCustom, fetch.legacyModelErr)
			}
			categorized := controllerutils.CategorizeError(fetch.legacyModelErr)
			if categorized.Category() != controllerutils.ErrorCategoryInvalidSpec {
				t.Fatalf("error category = %s, want InvalidSpec", categorized.Category())
			}
			if !strings.Contains(categorized.UserMessage(), tt.wantText) ||
				!strings.Contains(categorized.UserMessage(), "administrator") {
				t.Fatalf("error is not actionable or specific: %q", categorized.UserMessage())
			}

			obs := reconciler.ComposeState(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
				fetch,
			)
			plan := reconciler.PlanResources(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
				obs,
			)
			if len(plan.GetToApply()) != 0 ||
				len(plan.GetToApplyWithoutOwnerRef()) != 0 ||
				len(plan.GetToApplyWithForce()) != 0 ||
				len(plan.GetToDelete()) != 0 ||
				plan.RequeueAfter != 0 {
				t.Fatalf("invalid state planned child mutation or retry: %#v", plan)
			}

			status := model.Status.DeepCopy()
			cm := controllerutils.NewConditionManager(nil)
			reconciler.DecorateStatus(status, cm, obs)
			condition := cm.Get(legacyAutoCustomConditionType)
			if condition == nil ||
				condition.Status != metav1.ConditionFalse ||
				condition.Reason != legacyAutoCustomFailureReason ||
				!strings.Contains(condition.Message, "administrator") {
				t.Fatalf("compatibility condition = %#v", condition)
			}
			if status.Kind != aimv1alpha1.AIMModelKindCustom ||
				status.SourceType != aimv1alpha1.AIMModelSourceTypeCustom {
				t.Fatalf("status kind/source = %q/%q, want custom/custom", status.Kind, status.SourceType)
			}
		})
	}
}

func TestLegacyAutoCustomFailureEmitsWarningEvent(t *testing.T) {
	service, model := newLegacyAutoCustomFixture(t)
	model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec] = `{"baseImage":`
	reconciler, k8sClient := newLegacyAutoCustomReconciler(t, service, model)
	recorder := record.NewFakeRecorder(20)
	pipeline := &controllerutils.Pipeline[
		*aimv1alpha2.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		ModelFetchResult,
		ModelObservation,
	]{
		Client:         k8sClient,
		StatusClient:   k8sClient.Status(),
		Recorder:       recorder,
		Reconciler:     reconciler,
		Scheme:         reconciler.Scheme,
		ControllerName: "model",
	}

	result, err := pipeline.Run(context.Background(), model)
	if err != nil {
		t.Fatalf("Pipeline.Run() error = %v", err)
	}
	if !result.IsZero() {
		t.Fatalf("deterministic invalid input result = %#v, want zero", result)
	}

	foundWarning := false
drainEvents:
	for {
		select {
		case event := <-recorder.Events:
			if strings.Contains(event, "Warning") &&
				strings.Contains(event, legacyAutoCustomFailureReason) &&
				strings.Contains(event, "administrator") {
				foundWarning = true
			}
		default:
			break drainEvents
		}
	}
	if !foundWarning {
		t.Fatal("expected actionable warning event for invalid compatibility state")
	}
}

func newLegacyAutoCustomFixture(t *testing.T) (*aimv1alpha1.AIMService, *aimv1alpha2.AIMModel) {
	t.Helper()
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amd/aim-base:v1",
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "acme/model",
			SourceURI: "s3://models/model",
		}},
		Hardware: aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 1, Model: "MI300X"},
		},
	}
	service := &aimv1alpha1.AIMService{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: "AIMService"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-service",
			Namespace: "models",
			UID:       types.UID("source-uid"),
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model:              &aimv1alpha1.AIMServiceModel{Custom: custom},
			RuntimeConfigRef:   aimv1alpha1.RuntimeConfigRef{Name: "runtime"},
			ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "registry"}},
			ServiceAccountName: "runner",
		},
	}
	service.Spec.Env = []corev1.EnvVar{{Name: "SERVICE_ENV", Value: "value"}}
	model := &aimv1alpha2.AIMModel{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha2.GroupVersion.String(), Kind: "AIMModel"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "compatibility-model",
			Namespace: service.Namespace,
			UID:       types.UID("model-uid"),
			Labels: map[string]string{
				constants.LabelKeyOrigin:      constants.LabelValueOriginAutoGenerated,
				constants.LabelKeyCustomModel: "true",
				constants.LabelKeyService:     service.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(service, aimv1alpha1.GroupVersion.WithKind("AIMService")),
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              custom.BaseImage,
			RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
			ImagePullSecrets:   service.Spec.ImagePullSecrets,
			ServiceAccountName: service.Spec.ServiceAccountName,
			Env:                service.Spec.Env,
		},
		Status: aimv1alpha1.AIMModelStatus{
			ImageMetadata: &aimv1alpha1.ImageMetadata{},
		},
	}
	if err := legacyaimmodel.SetLegacyAutoCustomSpec(model, custom); err != nil {
		t.Fatalf("SetLegacyAutoCustomSpec() error = %v", err)
	}
	return service, model
}

func newGrandfatheredLegacyAutoCustomFixture(t *testing.T) (*aimv1alpha1.AIMService, *aimv1alpha2.AIMModel) {
	t.Helper()
	service, compatibilityModel := newLegacyAutoCustomFixture(t)
	custom := service.Spec.Model.Custom
	model := &aimv1alpha2.AIMModel{
		TypeMeta: compatibilityModel.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:      compatibilityModel.Name,
			Namespace: compatibilityModel.Namespace,
			UID:       compatibilityModel.UID,
			Labels: map[string]string{
				constants.LabelKeyOrigin:      constants.LabelValueOriginAutoGenerated,
				constants.LabelKeyCustomModel: "true",
				constants.LabelKeyService:     service.Name,
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:        custom.BaseImage,
			ModelSources: custom.ModelSources,
			Custom: &aimv1alpha1.AIMCustomModelSpec{
				Hardware:      custom.Hardware.DeepCopy(),
				VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
			},
			RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
			ImagePullSecrets:   service.Spec.ImagePullSecrets,
			ServiceAccountName: service.Spec.ServiceAccountName,
			Env:                service.Spec.Env,
		},
		Status: compatibilityModel.Status,
	}
	return service, model
}

func newLegacyAutoCustomReconciler(
	t *testing.T,
	objects ...client.Object,
) (*ModelReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha2 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&aimv1alpha2.AIMModel{}).
		WithIndex(
			&aimv1alpha1.AIMServiceTemplate{},
			aimv1alpha1.ServiceTemplateModelNameIndexKey,
			func(object client.Object) []string {
				return []string{object.(*aimv1alpha1.AIMServiceTemplate).Spec.ModelName}
			},
		).
		Build()
	return &ModelReconciler{Client: k8sClient, Scheme: scheme}, k8sClient
}
