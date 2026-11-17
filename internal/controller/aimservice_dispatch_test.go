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

package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	servicepolicy "github.com/amd-enterprise-ai/aim-engine/internal/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	profileservice "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimservice"
)

const dispatchTestModelName = "model"

func TestSelectPipeline(t *testing.T) {
	const (
		profile  = constants.ReconcilerPipelineProfile
		template = constants.ReconcilerPipelineTemplate
	)
	modelName := "some-model"
	image := "registry/image:tag"

	tests := []struct {
		name        string
		annotations map[string]string
		spec        aimv1alpha1.AIMServiceSpec
		want        string
	}{
		{
			name: "spec.profile routes to profile pipeline",
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: profile,
		},
		{
			name: "spec.template routes to template pipeline",
			spec: aimv1alpha1.AIMServiceSpec{
				Template: &aimv1alpha1.AIMServiceTemplateConfig{Name: "t"},
			},
			want: template,
		},
		{
			name: "spec.model.name routes to template pipeline by default",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: template,
		},
		{
			name: "spec.model.image routes to template pipeline by default",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
		{
			name:        "annotation forces profile pipeline on a model-shaped spec",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: profile},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: profile,
		},
		{
			name:        "grandfathered template annotation on profile-only spec falls back to profile",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: profile,
		},
		{
			name:        "unknown annotation value falls through to spec shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: "bogus"},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: profile,
		},
		{
			name:        "empty annotation value falls through to spec shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: ""},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: template,
		},
		// v1alpha2 quick-start: image-shape services must be able to
		// opt into the profile pipeline via the annotation so the
		// resolver's auto-create AIMModel path applies. Without the
		// annotation they keep using the v1alpha1 template pipeline
		// (which has its own image-based auto-create flow); flipping
		// is the migration-window opt-in until v1alpha1 is removed.
		{
			name:        "annotation forces profile pipeline on a spec.model.image service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: profile},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: profile,
		},
		{
			name:        "annotation forces template pipeline on a spec.model.image service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
		{
			name:        "annotation has higher priority than profile spec on image-shape service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Model:   &aimv1alpha1.AIMServiceModel{Image: &image},
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: template,
		},
		{
			name:        "unknown annotation on image-shape falls through to template default",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: "bogus"},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &aimv1alpha1.AIMService{
				ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations},
				Spec:       tc.spec,
			}
			if got := servicepolicy.SelectPipeline(svc); string(got) != tc.want {
				t.Fatalf("SelectPipeline = %q, want %q", got, tc.want)
			}
		})
	}
}

type recordingServicePipeline struct {
	runs int
}

func (p *recordingServicePipeline) Run(
	_ context.Context,
	_ *aimv1alpha2.AIMService,
) (ctrl.Result, error) {
	p.runs++
	return ctrl.Result{}, nil
}

type recordingStatusWriter struct {
	client.StatusWriter
	updated client.Object
}

func (w *recordingStatusWriter) Update(
	_ context.Context,
	obj client.Object,
	_ ...client.SubResourceUpdateOption,
) error {
	w.updated = obj.DeepCopyObject().(client.Object)
	return nil
}

func TestCanonicalAIMServiceStatusWriterUsesV1Alpha2(t *testing.T) {
	canonical := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "service", Namespace: "default"},
	}
	legacy := servicepolicy.LegacyView(canonical)
	legacy.Status.Conditions = []metav1.Condition{{Type: "Ready", Reason: "Updated"}}

	recorder := &recordingStatusWriter{}
	writer := &canonicalAIMServiceStatusWriter{
		StatusWriter: recorder,
		service:      canonical,
	}
	if err := writer.Update(context.Background(), legacy); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, ok := recorder.updated.(*aimv1alpha2.AIMService); !ok {
		t.Fatalf("status update object = %T, want *v1alpha2.AIMService", recorder.updated)
	}
	legacy.Status.Conditions[0].Reason = "MutatedAfterWrite"
	if got := canonical.Status.Conditions[0].Reason; got != "Updated" {
		t.Fatalf("canonical status shared legacy memory: reason = %q", got)
	}
}

func TestAIMServiceReconcilerPersistsTransitionBeforeDispatch(t *testing.T) {
	modelName := dispatchTestModelName
	tests := []struct {
		name               string
		service            *aimv1alpha2.AIMService
		wantPipeline       servicepolicy.Pipeline
		inactiveConditions []string
	}{
		{
			name: "template to profile",
			service: &aimv1alpha2.AIMService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "template-to-profile",
					Namespace:  "default",
					Finalizers: []string{finalizerCacheCleanup},
				},
				Spec: aimv1alpha1.AIMServiceSpec{
					Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
				},
				Status: aimv1alpha1.AIMServiceStatus{
					ReconcilerPipeline: constants.ReconcilerPipelineTemplate,
					Conditions: []metav1.Condition{
						{Type: "ModelReady", Status: metav1.ConditionTrue},
						{Type: "TemplateReady", Status: metav1.ConditionTrue},
						{Type: "InferenceServiceReady", Status: metav1.ConditionTrue},
					},
					ResolvedModel:    &aimv1alpha1.AIMResolvedReference{Name: dispatchTestModelName},
					ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{Name: "template"},
					Cache: &aimv1alpha1.AIMServiceCacheStatus{
						TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "template-cache"},
					},
				},
			},
			wantPipeline:       servicepolicy.PipelineProfile,
			inactiveConditions: []string{"ModelReady", "TemplateReady"},
		},
		{
			name: "profile to template",
			service: &aimv1alpha2.AIMService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "profile-to-template",
					Namespace:  "default",
					Finalizers: []string{finalizerCacheCleanup},
					Annotations: map[string]string{
						constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate,
					},
				},
				Spec: aimv1alpha1.AIMServiceSpec{
					Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
				},
				Status: aimv1alpha1.AIMServiceStatus{
					ReconcilerPipeline: constants.ReconcilerPipelineProfile,
					Conditions: []metav1.Condition{
						{Type: "ProfileReady", Status: metav1.ConditionTrue},
						{Type: "ProfileRuntimeReady", Status: metav1.ConditionTrue},
						{Type: "HTTPRouteReady", Status: metav1.ConditionTrue},
					},
					ResolvedModel:   &aimv1alpha1.AIMResolvedReference{Name: "model-id"},
					ResolvedProfile: &aimv1alpha1.AIMResolvedReference{Name: "profile"},
					Cache: &aimv1alpha1.AIMServiceCacheStatus{
						ProfileCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "profile-cache"},
					},
				},
			},
			wantPipeline:       servicepolicy.PipelineTemplate,
			inactiveConditions: []string{"ProfileReady", "ProfileRuntimeReady"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := aimv1alpha2.AddToScheme(scheme); err != nil {
				t.Fatalf("register v1alpha2: %v", err)
			}
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&aimv1alpha2.AIMService{}).
				WithObjects(test.service).
				Build()
			templatePipeline := &recordingServicePipeline{}
			profilePipeline := &recordingServicePipeline{}
			reconciler := &AIMServiceReconciler{
				Client:           cl,
				templatePipeline: templatePipeline,
				profilePipeline:  profilePipeline,
			}
			request := ctrl.Request{NamespacedName: types.NamespacedName{
				Name:      test.service.Name,
				Namespace: test.service.Namespace,
			}}

			result, err := reconciler.Reconcile(context.Background(), request)
			if err != nil {
				t.Fatalf("cleanup reconcile: %v", err)
			}
			if result == (ctrl.Result{}) {
				t.Fatal("transition cleanup did not request requeue")
			}
			if templatePipeline.runs != 0 || profilePipeline.runs != 0 {
				t.Fatal("pipeline ran before transition cleanup was persisted")
			}

			var cleaned aimv1alpha2.AIMService
			if err := cl.Get(context.Background(), request.NamespacedName, &cleaned); err != nil {
				t.Fatalf("get cleaned service: %v", err)
			}
			if cleaned.Status.ReconcilerPipeline != string(test.wantPipeline) {
				t.Fatalf(
					"ReconcilerPipeline = %q, want %q",
					cleaned.Status.ReconcilerPipeline,
					test.wantPipeline,
				)
			}
			if cleaned.Status.Status != constants.AIMStatusPending {
				t.Fatalf("status = %q, want Pending during pipeline transition", cleaned.Status.Status)
			}
			for _, conditionType := range test.inactiveConditions {
				if findServiceCondition(cleaned.Status.Conditions, conditionType) != nil {
					t.Errorf("%s condition survived transition", conditionType)
				}
			}
			if cleaned.Status.ResolvedModel != nil || cleaned.Status.Cache != nil {
				t.Fatal("pipeline-specific model or cache lineage survived transition")
			}

			if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
				t.Fatalf("dispatch reconcile: %v", err)
			}
			switch test.wantPipeline {
			case servicepolicy.PipelineTemplate:
				if templatePipeline.runs != 1 || profilePipeline.runs != 0 {
					t.Fatalf("pipeline runs = template:%d profile:%d", templatePipeline.runs, profilePipeline.runs)
				}
			case servicepolicy.PipelineProfile:
				if templatePipeline.runs != 0 || profilePipeline.runs != 1 {
					t.Fatalf("pipeline runs = template:%d profile:%d", templatePipeline.runs, profilePipeline.runs)
				}
			}
		})
	}
}

type statusWriterClient struct {
	client.Client
	writer client.StatusWriter
}

func (c *statusWriterClient) Status() client.StatusWriter {
	return c.writer
}

type notFoundReader struct {
	client.Reader
}

func (r *notFoundReader) Get(
	_ context.Context,
	key client.ObjectKey,
	_ client.Object,
	_ ...client.GetOption,
) error {
	return apierrors.NewNotFound(
		schema.GroupResource{Group: aimv1alpha2.GroupVersion.Group, Resource: "aimservices"},
		key.Name,
	)
}

type metadataRacingStatusWriter struct {
	client.StatusWriter
	client client.Client
	key    client.ObjectKey
	calls  int
}

func (w *metadataRacingStatusWriter) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.SubResourceUpdateOption,
) error {
	w.calls++
	if w.calls == 1 {
		var latest aimv1alpha2.AIMService
		if err := w.client.Get(ctx, w.key, &latest); err != nil {
			return err
		}
		latest.Annotations["airm.silogen.ai/auto-discovered"] = "true"
		latest.Finalizers = append(latest.Finalizers, "airm.silogen.ai/workloads-finalizer")
		if err := w.client.Update(ctx, &latest); err != nil {
			return err
		}
		return apierrors.NewConflict(
			schema.GroupResource{Group: aimv1alpha2.GroupVersion.Group, Resource: "aimservices"},
			obj.GetName(),
			errors.New("AIWB metadata update raced pipeline status initialization"),
		)
	}
	return w.StatusWriter.Update(ctx, obj, opts...)
}

func TestAIMServiceReconcilerRetriesPipelineStatusAfterMetadataRace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha2: %v", err)
	}
	modelName := dispatchTestModelName
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "metadata-race",
			Namespace:  "default",
			Finalizers: []string{finalizerCacheCleanup},
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
		},
	}
	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha2.AIMService{}).
		WithObjects(service).
		Build()
	key := client.ObjectKeyFromObject(service)
	racingWriter := &metadataRacingStatusWriter{
		StatusWriter: baseClient.Status(),
		client:       baseClient,
		key:          key,
	}
	reconciler := &AIMServiceReconciler{
		Client:           &statusWriterClient{Client: baseClient, writer: racingWriter},
		templatePipeline: &recordingServicePipeline{},
		profilePipeline:  &recordingServicePipeline{},
	}

	result, err := reconciler.Reconcile(
		context.Background(),
		ctrl.Request{NamespacedName: key},
	)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want metadata conflict retried", err)
	}
	if result == (ctrl.Result{}) {
		t.Fatal("pipeline initialization did not request follow-up reconciliation")
	}
	if racingWriter.calls != 2 {
		t.Fatalf("status update calls = %d, want conflict plus successful retry", racingWriter.calls)
	}

	var updated aimv1alpha2.AIMService
	if err := baseClient.Get(context.Background(), key, &updated); err != nil {
		t.Fatalf("get updated service: %v", err)
	}
	if updated.Status.ReconcilerPipeline != constants.ReconcilerPipelineProfile {
		t.Fatalf(
			"ReconcilerPipeline = %q, want %q",
			updated.Status.ReconcilerPipeline,
			constants.ReconcilerPipelineProfile,
		)
	}
	if updated.Annotations["airm.silogen.ai/auto-discovered"] != "true" ||
		!slices.Contains(updated.Finalizers, "airm.silogen.ai/workloads-finalizer") {
		t.Fatalf("AIWB metadata was lost: annotations=%v finalizers=%v", updated.Annotations, updated.Finalizers)
	}
}

func TestAIMServiceReconcilerIgnoresDeletionBeforePipelineStatusInitialization(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha2: %v", err)
	}
	modelName := dispatchTestModelName
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "deleted-before-status",
			Namespace:  "default",
			Finalizers: []string{finalizerCacheCleanup},
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
		},
	}
	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha2.AIMService{}).
		WithObjects(service).
		Build()
	reconciler := &AIMServiceReconciler{
		Client:           baseClient,
		APIReader:        &notFoundReader{Reader: baseClient},
		templatePipeline: &recordingServicePipeline{},
		profilePipeline:  &recordingServicePipeline{},
	}

	result, err := reconciler.Reconcile(
		context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(service)},
	)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want deleted service ignored", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %+v, want no requeue for deleted service", result)
	}
}

func TestAIMServiceReconcilerGrandfatheredMarkersRemainOperational(t *testing.T) {
	modelName := dispatchTestModelName
	tests := []struct {
		name         string
		marker       string
		spec         aimv1alpha1.AIMServiceSpec
		wantPipeline servicepolicy.Pipeline
	}{
		{
			name:   "unknown marker follows profile shape",
			marker: "historical",
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			wantPipeline: servicepolicy.PipelineProfile,
		},
		{
			name:   "empty marker follows model shape",
			marker: "",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			wantPipeline: servicepolicy.PipelineTemplate,
		},
		{
			name:   "template marker without model follows profile shape",
			marker: constants.ReconcilerPipelineTemplate,
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			wantPipeline: servicepolicy.PipelineProfile,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := aimv1alpha2.AddToScheme(scheme); err != nil {
				t.Fatalf("register v1alpha2: %v", err)
			}
			service := &aimv1alpha2.AIMService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "grandfathered",
					Namespace:  "default",
					Finalizers: []string{finalizerCacheCleanup},
					Annotations: map[string]string{
						constants.AnnotationReconcilerPipeline: test.marker,
					},
				},
				Spec: test.spec,
				Status: aimv1alpha1.AIMServiceStatus{
					ReconcilerPipeline: string(test.wantPipeline),
				},
			}
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&aimv1alpha2.AIMService{}).
				WithObjects(service).
				Build()
			templatePipeline := &recordingServicePipeline{}
			profilePipeline := &recordingServicePipeline{}
			recorder := record.NewFakeRecorder(1)
			reconciler := &AIMServiceReconciler{
				Client:           cl,
				Recorder:         recorder,
				templatePipeline: templatePipeline,
				profilePipeline:  profilePipeline,
			}

			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: service.Name, Namespace: service.Namespace},
			}); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			var updated aimv1alpha2.AIMService
			if err := cl.Get(
				context.Background(),
				types.NamespacedName{Name: service.Name, Namespace: service.Namespace},
				&updated,
			); err != nil {
				t.Fatalf("get reconciled service: %v", err)
			}
			if updated.Status.Status == constants.AIMStatusFailed {
				t.Fatal("grandfathered marker failed solely because of pipeline validation")
			}
			if updated.Status.ReconcilerPipeline != string(test.wantPipeline) {
				t.Fatalf(
					"ReconcilerPipeline = %q, want %q",
					updated.Status.ReconcilerPipeline,
					test.wantPipeline,
				)
			}
			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, invalidPipelineMarkerReason) ||
					!strings.Contains(event, "using spec-shape pipeline selection") {
					t.Fatalf("malformed marker event = %q", event)
				}
			default:
				t.Fatal("malformed pipeline marker did not emit a warning event")
			}
			switch test.wantPipeline {
			case servicepolicy.PipelineTemplate:
				if templatePipeline.runs != 1 || profilePipeline.runs != 0 {
					t.Fatalf("pipeline runs = template:%d profile:%d", templatePipeline.runs, profilePipeline.runs)
				}
			case servicepolicy.PipelineProfile:
				if templatePipeline.runs != 0 || profilePipeline.runs != 1 {
					t.Fatalf("pipeline runs = template:%d profile:%d", templatePipeline.runs, profilePipeline.runs)
				}
			}
		})
	}
}

// The pipeline-selection step and the profile pipeline both clear template-era
// status. Run together on a service that still carries it, they must settle on
// profile status instead of rewriting each other's status every reconcile.
func TestAIMServiceReconcilerTemplateEraStatusConverges(t *testing.T) {
	const (
		profileName = "cluster-profile"
		modelID     = "org/model"
	)
	tests := []struct {
		name             string
		recordedPipeline string
	}{
		{name: "template pipeline recorded", recordedPipeline: constants.ReconcilerPipelineTemplate},
		{name: "no pipeline recorded", recordedPipeline: ""},
		{name: "profile pipeline recorded", recordedPipeline: constants.ReconcilerPipelineProfile},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service := &aimv1alpha2.AIMService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "template-era",
					Namespace:  "default",
					UID:        "service-uid",
					Finalizers: []string{finalizerCacheCleanup},
				},
				Spec: aimv1alpha1.AIMServiceSpec{
					Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: profileName},
				},
				Status: aimv1alpha1.AIMServiceStatus{
					ReconcilerPipeline: test.recordedPipeline,
					Status:             constants.AIMStatusRunning,
					Conditions: []metav1.Condition{
						{Type: "ModelReady", Status: metav1.ConditionTrue, Reason: "ModelResolved"},
						{Type: "TemplateReady", Status: metav1.ConditionTrue, Reason: "TemplateReady"},
						{Type: "CacheReady", Status: metav1.ConditionTrue, Reason: "CacheReady"},
						{Type: "MigrationRequired", Status: metav1.ConditionTrue, Reason: "TemplateAPIDeprecated"},
					},
					ResolvedModel:    &aimv1alpha1.AIMResolvedReference{Name: "template-era-model", UID: "model-uid"},
					ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{Name: "template", UID: "template-uid"},
					Cache: &aimv1alpha1.AIMServiceCacheStatus{
						TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "template-cache"},
					},
				},
			}
			clusterProfile := &aimv1alpha2.AIMClusterProfile{
				ObjectMeta: metav1.ObjectMeta{Name: profileName, UID: "profile-uid"},
				Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
					AimId:     modelID,
					ModelId:   modelID,
					Engine:    "vllm",
					Metric:    aimv1alpha1.AIMMetric("latency"),
					Precision: aimv1alpha1.AIMPrecision("bf16"),
					Type:      aimv1alpha1.AIMProfileType("general"),
					Image:     "docker.io/amdenterpriseai/aim-dummy:0.2.0",
				}},
				Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
			}
			cl, reconciler := newProfileDispatchReconcilerForTest(t, service, clusterProfile)
			key := client.ObjectKeyFromObject(service)

			var settled aimv1alpha2.AIMService
			for reconciles := 0; ; reconciles++ {
				if reconciles == 10 {
					t.Fatal("status still changing after 10 reconciles")
				}
				if err := cl.Get(ctx, key, &settled); err != nil {
					t.Fatalf("get service: %v", err)
				}
				resourceVersion := settled.ResourceVersion
				if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatalf("Reconcile() error = %v", err)
				}
				if err := cl.Get(ctx, key, &settled); err != nil {
					t.Fatalf("get service: %v", err)
				}
				if settled.ResourceVersion == resourceVersion {
					break
				}
			}

			status := settled.Status
			for _, conditionType := range []string{"ModelReady", "TemplateReady", "CacheReady", "MigrationRequired"} {
				if condition := findServiceCondition(status.Conditions, conditionType); condition != nil {
					t.Errorf("template-era condition %s survived: %#v", conditionType, condition)
				}
			}
			if status.ResolvedTemplate != nil || status.Cache != nil {
				t.Fatalf("template-era references survived: resolvedTemplate=%#v cache=%#v", status.ResolvedTemplate, status.Cache)
			}
			if status.ResolvedModel == nil || status.ResolvedModel.Name != modelID {
				t.Fatalf("resolvedModel = %#v, want profile model ID %q", status.ResolvedModel, modelID)
			}
			if status.ReconcilerPipeline != constants.ReconcilerPipelineProfile {
				t.Fatalf("ReconcilerPipeline = %q, want %q", status.ReconcilerPipeline, constants.ReconcilerPipelineProfile)
			}
		})
	}
}

func newProfileDispatchReconcilerForTest(
	t *testing.T,
	objects ...client.Object,
) (client.Client, *AIMServiceReconciler) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		autoscalingv2.AddToScheme,
		aimv1alpha1.AddToScheme,
		aimv1alpha2.AddToScheme,
		kservev1alpha1.AddToScheme,
		servingv1beta1.AddToScheme,
		gatewayapiv1.Install,
	} {
		if err := addToScheme(scheme); err != nil {
			t.Fatalf("add test types to scheme: %v", err)
		}
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha2.AIMService{}).
		WithObjects(objects...).
		Build()
	recorder := &record.FakeRecorder{}
	profilePipeline := &controllerutils.Pipeline[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		profileservice.ServiceFetchResult,
		profileservice.ServiceObservation,
	]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: serviceName,
		Reconciler:     &profileservice.ProfileServiceReconciler{Scheme: scheme, Recorder: recorder},
		Scheme:         scheme,
		Clientset:      kubernetesfake.NewClientset(),
	}
	return cl, &AIMServiceReconciler{
		Client:           cl,
		Scheme:           scheme,
		Recorder:         recorder,
		templatePipeline: &recordingServicePipeline{},
		profilePipeline: &legacyAIMServicePipeline[
			profileservice.ServiceFetchResult,
			profileservice.ServiceObservation,
		]{pipeline: profilePipeline},
	}
}

func findServiceCondition(
	conditions []metav1.Condition,
	conditionType string,
) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
