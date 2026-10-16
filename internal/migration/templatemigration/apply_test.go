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

package templatemigration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	v1alpha1aimservice "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	testOtherValue     = "other"
	testPreservedValue = "yes"
)

func TestValidateCurrentSourceClosure_AllowsRunningTemplatePipelineWithMigrationSignal(t *testing.T) {
	templateUID := types.UID("template-uid")
	modelUID := types.UID("model-uid")
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "service-a",
			Namespace:  testNamespace,
			Generation: 1,
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusRunning,
			ReconcilerPipeline: constants.ReconcilerPipelineTemplate,
			Conditions: []metav1.Condition{{
				Type:   "MigrationRequired",
				Status: metav1.ConditionTrue,
				Reason: "TemplateAPIDeprecated",
			}},
			ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{
				Name:      "template-a",
				Namespace: testNamespace,
				Kind:      resourceKindServiceTemplate,
				UID:       templateUID,
			},
			ResolvedModel: &aimv1alpha1.AIMResolvedReference{
				Name:      "model-a",
				Namespace: testNamespace,
				Kind:      "AIMModel",
				UID:       modelUID,
			},
		},
	}
	template := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": aimv1alpha1.GroupVersion.String(),
		"kind":       resourceKindServiceTemplate,
		"metadata": map[string]any{
			"name":       "template-a",
			"namespace":  testNamespace,
			"uid":        string(templateUID),
			"generation": int64(1),
		},
		"spec": map[string]any{"modelName": "model-a"},
		"status": map[string]any{
			"observedGeneration": int64(1),
			"status":             string(constants.AIMStatusReady),
		},
	}}
	model := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": aimv1alpha1.GroupVersion.String(),
		"kind":       "AIMModel",
		"metadata": map[string]any{
			"name":       "model-a",
			"namespace":  testNamespace,
			"uid":        string(modelUID),
			"generation": int64(1),
		},
		"status": map[string]any{
			"observedGeneration": int64(1),
			"status":             string(constants.AIMStatusReady),
		},
	}}

	if err := validateCurrentSourceClosure(service, map[string]*unstructured.Unstructured{
		roleSourceTemplate: template,
		roleSourceModel:    model,
	}, false); err != nil {
		t.Fatalf("auto-selected template pipeline was rejected: %v", err)
	}
}

func TestApply_DryRunUsesDryRunAllAndPersistsNoClusterWrites(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	manual := MigrationUnit{
		ID:        testNamespace + "/manual",
		Namespace: testNamespace,
		Service: ResourceIdentity{
			Namespace: testNamespace,
			Name:      "manual",
		},
		Outcome: OutcomeManual,
		ProposedOperations: []ProposedOperation{{
			Action: "unsupported-manual-operation",
			Resource: ResourceIdentity{
				Kind:      "Secret",
				Namespace: testNamespace,
				Name:      "ignored",
			},
		}},
	}
	fixture.document.Units = append(fixture.document.Units, manual)

	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	planPath := writeApplyPlan(t, fixture.document)
	resultPath := filepath.Join(filepath.Dir(planPath), applyResultFilename)

	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		ResultPath:   resultPath,
		WaitTimeout:  time.Nanosecond,
		PollInterval: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeDryRun ||
		summary.Automatic != 1 ||
		summary.ManualSkipped != 1 ||
		summary.Operations != 3 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(recording.creates) != 2 || len(recording.patches) != 1 {
		t.Fatalf("writes = creates %#v, patches %#v", recording.creates, recording.patches)
	}
	for _, write := range append(append([]recordedApplyWrite{}, recording.creates...), recording.patches...) {
		if !write.dryRun {
			t.Fatalf("write did not use client.DryRunAll: %#v", write)
		}
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}

	assertNotFound(t, base, client.ObjectKeyFromObject(fixture.profile), &aimv1alpha2.AIMProfile{})
	assertNotFound(t, base, client.ObjectKeyFromObject(fixture.cache), &aimv1alpha2.AIMProfileCache{})
	current := &aimv1alpha2.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.Profile != nil ||
		current.Spec.Template == nil ||
		current.Annotations[constants.AnnotationReconcilerPipeline] != "" {
		t.Fatalf("dry-run persisted service cutover: %#v", current.Spec)
	}

	assertMode(t, resultPath, 0o600)
	result := readApplyResult(t, resultPath)
	if result.Mode != ApplyModeDryRun || len(result.Operations) != 3 {
		t.Fatalf("apply result = %#v", result)
	}
	for _, operation := range result.Operations {
		if operation.Outcome != "dry-run" {
			t.Fatalf("dry-run operation = %#v", operation)
		}
	}
}

func TestApply_RejectsEditedAutomaticDedicatedPlanBeforeWrites(t *testing.T) {
	for _, test := range []struct {
		name   string
		commit bool
	}{
		{name: "dry-run"},
		{name: "commit", commit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDedicatedApplyFixture(t, nil)
			unit := &fixture.document.Units[0]
			unit.Outcome = OutcomeManual
			unit.ManualOutcomes = []Finding{{
				Code:    dedicatedHandoffReasonCode,
				Message: dedicatedHandoffReason,
			}}
			// Model an administrator changing Plan's mandatory manual disposition.
			unit.Outcome = OutcomeAutomatic
			unit.ManualOutcomes = nil
			recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
			planPath := writeApplyPlan(t, fixture.document)
			summary, err := Apply(context.Background(), recording, ApplyOptions{
				PlanPath: planPath,
				Commit:   test.commit,
			})
			if err == nil || !strings.Contains(err.Error(), "automatic Dedicated migration is prohibited") {
				t.Fatalf("Apply() error = %v, want unconditional Dedicated rejection", err)
			}
			if summary.Failed != 1 || summary.Operations != 0 {
				t.Fatalf("summary = %#v", summary)
			}
			if len(recording.writes) != 0 || recording.deletes != 0 {
				t.Fatalf("Dedicated rejection wrote to Kubernetes: %#v", recording)
			}
			result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
			if result.Summary.Failed != 1 ||
				len(result.Operations) != 0 ||
				!strings.Contains(result.Units[0].Error, "automatic Dedicated migration is prohibited") {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestApply_RejectsInvalidLegacyInferenceServiceShapeBeforeWrites(t *testing.T) {
	tests := []struct {
		name string
		edit func(*servingv1beta1.InferenceService)
		want string
	}{
		{
			name: "missing template label",
			edit: func(inferenceService *servingv1beta1.InferenceService) {
				delete(inferenceService.Labels, constants.LabelTemplate)
			},
			want: "has no legacy template label",
		},
		{
			name: "profile label present",
			edit: func(inferenceService *servingv1beta1.InferenceService) {
				inferenceService.Labels[constants.LabelProfile] = "profile-a"
			},
			want: "has a profile label",
		},
	}
	for _, test := range tests {
		for _, commit := range []bool{false, true} {
			mode := "dry-run"
			if commit {
				mode = "commit"
			}
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				fixture := newApplyFixture(t, nil)
				test.edit(fixture.inferenceService)
				recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}

				summary, err := Apply(context.Background(), recording, ApplyOptions{
					PlanPath: writeApplyPlan(t, fixture.document),
					Commit:   commit,
				})
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Apply() error = %v, want %q", err, test.want)
				}
				if summary.Failed != 1 {
					t.Fatalf("summary = %#v", summary)
				}
				if len(recording.writes) != 0 || recording.deletes != 0 {
					t.Fatalf("invalid InferenceService shape wrote to Kubernetes: %#v", recording)
				}
			})
		}
	}
}

func TestApply_RejectsEditedPlanAndSourceDriftBeforeWrites(t *testing.T) {
	tests := []struct {
		name string
		edit func(*applyFixture)
		want string
	}{
		{
			name: "document version",
			edit: func(fixture *applyFixture) {
				fixture.document.DocumentVersion = "unsupported/v9"
			},
			want: "unsupported documentVersion",
		},
		{
			name: "namespace scope",
			edit: func(fixture *applyFixture) {
				fixture.document.Units[0].Namespace = testOtherValue
			},
			want: "outside selectedNamespaces",
		},
		{
			name: "operation ordering",
			edit: func(fixture *applyFixture) {
				operations := fixture.document.Units[0].ProposedOperations
				operations[0], operations[1] = operations[1], operations[0]
			},
			want: "first operation",
		},
		{
			name: "unsupported operation",
			edit: func(fixture *applyFixture) {
				fixture.document.Units[0].ProposedOperations[2].Action = "delete"
			},
			want: "last operation",
		},
		{
			name: "cached predecessor",
			edit: func(fixture *applyFixture) {
				fixture.document.Units[0].ObservedResources = append(
					fixture.document.Units[0].ObservedResources,
					resourceIdentityForTest(
						t,
						newPlanFixture("cached").artifact,
						aimv1alpha1.GroupVersion.String(),
						resourceKindArtifact,
						"source-artifact",
					),
				)
			},
			want: "cached predecessor",
		},
		{
			name: "source relevant content drift",
			edit: func(fixture *applyFixture) {
				fixture.template.Spec.ModelName = "changed-model"
			},
			want: "relevant content changed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newApplyFixture(t, nil)
			test.edit(fixture)
			recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
			_, err := Apply(context.Background(), recording, ApplyOptions{
				PlanPath: writeApplyPlan(t, fixture.document),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(recording.creates) != 0 || len(recording.patches) != 0 || recording.deletes != 0 {
				t.Fatalf("invalid plan wrote to Kubernetes: %#v", recording)
			}
		})
	}
}

func TestApply_CreateConflictsRatherThanOverwriting(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	conflict := fixture.profile.DeepCopy()
	conflict.UID = types.UID("conflicting-profile")
	conflict.ResourceVersion = "91"
	conflict.Generation = 1
	conflict.Spec.AimId = "different/model"
	fixture.objects = append(fixture.objects, conflict)

	recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
	planPath := writeApplyPlan(t, fixture.document)
	_, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err == nil || !strings.Contains(err.Error(), "conflicting complete spec") {
		t.Fatalf("error = %v", err)
	}
	if len(recording.creates) != 0 || len(recording.patches) != 0 || recording.deletes != 0 {
		t.Fatalf("conflict caused a Kubernetes write: %#v", recording)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if len(result.Operations) != 1 || result.Operations[0].Outcome != "conflict" {
		t.Fatalf("conflict result = %#v", result)
	}
}

func TestApply_CommitCreatesFreshTargetsAndWaitsForControllerReadiness(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = func(ctx context.Context, object client.Object) error {
		switch object.(type) {
		case *aimv1alpha2.AIMProfile:
			current := &aimv1alpha2.AIMProfile{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.profile), current); err != nil {
				return err
			}
			current.UID = types.UID("created-profile-uid")
			current.Generation = 1
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			runtimeName := serving.RuntimeName(current.Name)
			current.Status = aimv1alpha2.AIMProfileStatus{
				ObservedGeneration:   current.Generation,
				Status:               constants.AIMStatusReady,
				Deployable:           true,
				ProjectedRuntimeName: runtimeName,
				Conditions: []metav1.Condition{{
					Type:   aimv1alpha2.AIMProfileConditionRuntimeProjected,
					Status: metav1.ConditionTrue,
					Reason: aimv1alpha2.AIMProfileReasonRuntimeProjected,
				}},
			}
			if err := base.Status().Update(ctx, current); err != nil {
				return err
			}
			if err := base.Create(ctx, &kservev1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			}); err != nil {
				return err
			}
			return base.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			})
		case *aimv1alpha2.AIMProfileCache:
			current := &aimv1alpha2.AIMProfileCache{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), current); err != nil {
				return err
			}
			current.UID = types.UID("created-cache-uid")
			current.Generation = 1
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			current.Status = aimv1alpha2.AIMProfileCacheStatus{
				ObservedGeneration: current.Generation,
				Status:             constants.AIMStatusReady,
			}
			return base.Status().Update(ctx, current)
		default:
			return nil
		}
	}
	recording.afterPatch = readyServiceAfterPatch(t, base, fixture, types.UID("created-profile-uid"))

	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  200 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Operations != 4 || len(recording.creates) != 2 || len(recording.patches) != 1 {
		t.Fatalf("summary/writes = %#v, creates %#v, patches %#v", summary, recording.creates, recording.patches)
	}
	for _, write := range append(append([]recordedApplyWrite{}, recording.creates...), recording.patches...) {
		if write.dryRun {
			t.Fatalf("commit write unexpectedly used dry-run: %#v", write)
		}
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
}

func TestApply_UncachedCommitRejectsLateDriftBeforeCutover(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(context.Context, client.Client, *applyFixture) error
		wantError string
		wantStep  string
	}{
		{
			name: "source dependency",
			mutate: func(ctx context.Context, base client.Client, fixture *applyFixture) error {
				current := &aimv1alpha1.AIMServiceTemplate{}
				if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.template), current); err != nil {
					return err
				}
				current.Spec.AimId = testOtherValue
				return base.Update(ctx, current)
			},
			wantError: "relevant content changed before service cutover",
			wantStep:  "verify-uncached-dependencies",
		},
		{
			name: "target profile",
			mutate: func(ctx context.Context, base client.Client, fixture *applyFixture) error {
				current := &aimv1alpha2.AIMProfile{}
				if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.profile), current); err != nil {
					return err
				}
				current.Spec.AimId = testOtherValue
				return base.Update(ctx, current)
			},
			wantError: "conflicting complete spec",
			wantStep:  "verify-uncached-profile",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newApplyFixture(t, nil)
			base := newApplyFakeClient(t, fixture.objects...)
			readyTargets := makeSharedTargetsReady(t, base, fixture, false)
			recording := &recordingApplyClient{Client: base}
			recording.afterCreate = func(ctx context.Context, object client.Object) error {
				if err := readyTargets(ctx, object); err != nil {
					return err
				}
				if _, ok := object.(*aimv1alpha2.AIMProfileCache); !ok {
					return nil
				}
				return test.mutate(ctx, base, fixture)
			}
			planPath := writeApplyPlan(t, fixture.document)

			_, err := Apply(context.Background(), recording, ApplyOptions{
				PlanPath:     planPath,
				Commit:       true,
				WaitTimeout:  100 * time.Millisecond,
				PollInterval: time.Millisecond,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Apply() error = %v, want late %s drift rejection", err, test.name)
			}
			if len(recording.patches) != 0 {
				t.Fatalf("%s drift patched service: %#v", test.name, recording.patches)
			}
			currentService := &aimv1alpha2.AIMService{}
			if getErr := base.Get(
				context.Background(),
				client.ObjectKeyFromObject(fixture.service),
				currentService,
			); getErr != nil {
				t.Fatal(getErr)
			}
			if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
				t.Fatalf("%s drift crossed service cutover fence: %#v", test.name, currentService.Spec)
			}
			result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
			last := result.Operations[len(result.Operations)-1]
			if result.LastOperation != test.wantStep ||
				last.Action != test.wantStep ||
				last.Outcome != operationOutcomeFailed {
				t.Fatalf("%s drift diagnostics = %#v", test.name, result)
			}
		})
	}
}

func TestApply_CommitReusesExactTargetsAndPatchesServiceInPlace(t *testing.T) {
	fixture := newApplyFixture(t, func(plan *planFixture) {
		plan.service.Annotations = map[string]string{"example.invalid/preserved": testPreservedValue}
		plan.service.Spec.Replicas = ptr.To[int32](3)
		plan.service.Spec.Resources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resourceMustParse(t, "2")},
		}
	})
	profile, cache, runtimeObject, runtimeConfigMap := fixture.makeReadyReusableTargets(t)
	fixture.objects = append(fixture.objects, profile, cache, runtimeObject, runtimeConfigMap)

	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterPatch = readyServiceAfterPatch(t, base, fixture, profile.UID)

	beforeSpec := fixture.service.Spec.DeepCopy()
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeCommit || summary.Operations != 4 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(recording.creates) != 0 || len(recording.patches) != 1 || recording.patches[0].dryRun {
		t.Fatalf("commit writes = creates %#v patches %#v", recording.creates, recording.patches)
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}

	current := &aimv1alpha2.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), current); err != nil {
		t.Fatal(err)
	}
	expectedSpec := beforeSpec.DeepCopy()
	expectedSpec.Profile = &aimv1alpha1.AIMServiceProfileConfig{Name: profile.Name}
	expectedSpec.Template = nil
	if !equality.Semantic.DeepEqual(current.Spec, *expectedSpec) {
		t.Fatalf("service spec was not preserved\ncurrent: %#v\nexpected: %#v", current.Spec, *expectedSpec)
	}
	if current.UID != fixture.service.UID ||
		current.Annotations["example.invalid/preserved"] != testPreservedValue ||
		current.Annotations[constants.AnnotationReconcilerPipeline] != constants.ReconcilerPipelineProfile ||
		current.Annotations[constants.AnnotationMigrationPreserveInferenceServiceGeneration] != "2" ||
		current.Annotations[constants.AnnotationMigrationPreserveInferenceServiceUID] !=
			string(fixture.inferenceService.UID) {
		t.Fatalf("service identity/annotations were not preserved: %#v", current.ObjectMeta)
	}
	inferenceService := &servingv1beta1.InferenceService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.inferenceService), inferenceService); err != nil {
		t.Fatal(err)
	}
	if inferenceService.UID != fixture.inferenceService.UID {
		t.Fatalf("InferenceService UID = %q, want %q", inferenceService.UID, fixture.inferenceService.UID)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.Operations[len(result.Operations)-1].Outcome != "verified" {
		t.Fatalf("commit result = %#v", result)
	}
}

func TestApply_CommitRejectsInferenceServiceRollout(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	profile, cache, runtimeObject, runtimeConfigMap := fixture.makeReadyReusableTargets(t)
	fixture.objects = append(fixture.objects, profile, cache, runtimeObject, runtimeConfigMap)

	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	markServiceReady := readyServiceAfterPatch(t, base, fixture, profile.UID)
	recording.afterPatch = func(ctx context.Context, object client.Object) error {
		if err := markServiceReady(ctx, object); err != nil {
			return err
		}
		if _, ok := object.(*aimv1alpha2.AIMService); !ok {
			return nil
		}
		current := &servingv1beta1.InferenceService{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.inferenceService), current); err != nil {
			return err
		}
		current.Generation++
		if err := base.Update(ctx, current); err != nil {
			return err
		}
		current.Status.ObservedGeneration = current.Generation
		return base.Status().Update(ctx, current)
	}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  20 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "InferenceService") {
		t.Fatalf("Apply() error = %v, want no-rollout continuity failure", err)
	}
}

func TestApply_CommitRejectsInferenceServiceDriftBeforeCutover(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeSharedTargetsReady(t, base, fixture, false)
	recording.afterPatch = func(ctx context.Context, object client.Object) error {
		if _, ok := object.(*aimv1alpha1.AIMArtifact); !ok {
			return nil
		}
		current := &servingv1beta1.InferenceService{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.inferenceService), current); err != nil {
			return err
		}
		current.Annotations = map[string]string{"example.invalid/drift": "changed"}
		return base.Update(ctx, current)
	}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "changed before cutover") {
		t.Fatalf("Apply() error = %v, want pre-cutover no-rollout drift rejection", err)
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.service),
		currentService,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("InferenceService drift crossed service cutover fence: %#v", currentService.Spec)
	}
}

func TestApply_CommitRejectsTerminatingInferenceServiceBeforeCutover(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	fixture.inferenceService.Finalizers = []string{"test.aim.eai.amd.com/preserve"}
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeSharedTargetsReady(t, base, fixture, false)
	recording.afterPatch = func(ctx context.Context, object client.Object) error {
		if _, ok := object.(*aimv1alpha1.AIMArtifact); !ok {
			return nil
		}
		current := &servingv1beta1.InferenceService{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.inferenceService), current); err != nil {
			return err
		}
		return base.Delete(ctx, current)
	}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "not current and Ready immediately before cutover") {
		t.Fatalf("Apply() error = %v, want pre-cutover terminating InferenceService rejection", err)
	}
	for _, patch := range recording.patches {
		if patch.kind == resourceKindService {
			t.Fatalf("terminating InferenceService allowed AIMService patch: %#v", patch)
		}
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.service),
		currentService,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("terminating InferenceService crossed service cutover fence: %#v", currentService.Spec)
	}
	currentInferenceService := &servingv1beta1.InferenceService{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.inferenceService),
		currentInferenceService,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentInferenceService.DeletionTimestamp == nil {
		t.Fatal("test InferenceService did not enter termination")
	}
}

func TestInferenceServiceReadyRequiresCurrentGeneration(t *testing.T) {
	inferenceService := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Status: servingv1beta1.InferenceServiceStatus{
			Status: duckv1.Status{
				ObservedGeneration: 1,
				Conditions: duckv1.Conditions{{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				}},
			},
		},
	}
	if inferenceServiceReady(inferenceService) {
		t.Fatal("inferenceServiceReady() accepted stale Ready=True")
	}
	inferenceService.Status.ObservedGeneration = inferenceService.Generation
	if !inferenceServiceReady(inferenceService) {
		t.Fatal("inferenceServiceReady() rejected current Ready=True")
	}
	deletionTimestamp := metav1.Now()
	inferenceService.DeletionTimestamp = &deletionTimestamp
	if inferenceServiceReady(inferenceService) {
		t.Fatal("inferenceServiceReady() accepted terminating current Ready=True")
	}
	inferenceService.DeletionTimestamp = nil

	before := inferenceService.DeepCopy()
	after := inferenceService.DeepCopy()
	after.Status.DeploymentMode = "changed-status-only"
	if !inferenceServiceDesiredStateUnchanged(before, after) {
		t.Fatal("status-only change broke no-rollout desired-state equality")
	}
	after.Annotations = map[string]string{"example.invalid/rollout": "changed"}
	if inferenceServiceDesiredStateUnchanged(before, after) {
		t.Fatal("metadata desired-state change passed no-rollout equality")
	}
}

func TestComparePinnedIdentity_AllowsStatusOnlyResourceVersionAdvance(t *testing.T) {
	service := &aimv1alpha2.AIMService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       resourceKindService,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "service",
			Namespace:       testNamespace,
			UID:             "service-uid",
			ResourceVersion: "10",
			Generation:      1,
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To("example.invalid/model:1")},
		},
	}
	expected := resourceIdentityForTest(
		t,
		service,
		aimv1alpha2.GroupVersion.String(),
		resourceKindService,
		"service",
	)
	current := service.DeepCopy()
	current.ResourceVersion = "11"
	current.Status.Status = constants.AIMStatusRunning
	current.Status.ObservedGeneration = 1

	if err := comparePinnedIdentity(current, expected); err != nil {
		t.Fatalf("status-only resourceVersion advance rejected: %v", err)
	}

	runtimeConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMClusterRuntimeConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "default",
			UID:             "runtime-config-uid",
			ResourceVersion: "20",
			Generation:      1,
		},
	}
	runtimeExpected := resourceIdentityForTest(
		t,
		runtimeConfig,
		aimv1alpha1.GroupVersion.String(),
		"AIMClusterRuntimeConfig",
		"runtime-config",
	)
	runtimeConfig.ResourceVersion = "21"
	if err := comparePinnedIdentity(runtimeConfig, runtimeExpected); err == nil ||
		!strings.Contains(err.Error(), "resourceVersion changed") {
		t.Fatalf("statusless RuntimeConfig resourceVersion advance error = %v", err)
	}
}

func TestValidateReusableProfileProvenance_AcceptsPinnedSourceModelOwner(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	profile := fixture.profile.DeepCopy()
	for _, name := range []string{
		annotationMigrationSourceKind,
		annotationMigrationSourceNamespace,
		annotationMigrationSourceName,
		annotationMigrationSourceUID,
	} {
		delete(profile.Annotations, name)
	}
	var sourceModel ResourceIdentity
	for _, identity := range fixture.document.Units[0].ObservedResources {
		if identity.Role == roleSourceModel {
			sourceModel = identity
		}
	}
	sourceTemplate := fixture.document.Units[0].ObservedResources[1]
	for _, identity := range fixture.document.Units[0].ObservedResources {
		if identity.Role == roleSourceTemplate {
			sourceTemplate = identity
		}
	}
	profile.Status.SourceModel = &aimv1alpha2.ProfileSourceModel{
		Name:      sourceModel.Name,
		Namespace: sourceModel.Namespace,
		Kind:      aimv1alpha2.ProfileSourceModelKind(sourceModel.Kind),
	}
	profile.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       sourceModel.Kind,
		Name:       sourceModel.Name,
		UID:        types.UID(sourceModel.UID),
	}}
	if err := validateReusableProfileProvenance(profile, sourceTemplate, sourceModel); err != nil {
		t.Fatal(err)
	}
	profile.OwnerReferences[0].UID = types.UID(testOtherValue)
	if err := validateReusableProfileProvenance(profile, sourceTemplate, sourceModel); err == nil {
		t.Fatal("mismatched source-model owner was accepted")
	}
}

func TestApply_CommitWaitIsBoundedAndDoesNotCutOverEarly(t *testing.T) {
	fixture := newApplyFixture(t, nil)

	recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
	planPath := writeApplyPlan(t, fixture.document)
	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  5 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "did not become Ready") {
		t.Fatalf("error = %v", err)
	}
	if len(recording.patches) != 0 {
		t.Fatalf("service was patched before profile readiness: %#v", recording.patches)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	last := result.Operations[len(result.Operations)-1]
	if last.Action != "wait-profile-runtime" || last.Outcome != "failed" {
		t.Fatalf("bounded-wait result = %#v", last)
	}
	first := result.Operations[0]
	if first.Action != string(OperationCreate) ||
		first.Outcome != operationOutcomeCreated ||
		!first.Attempted ||
		!first.Completed {
		t.Fatalf("accepted profile create was not durable before readiness wait: %#v", first)
	}
}

func TestApply_PersistsMutationAttemptBeforeKubernetesCall(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	planPath := writeApplyPlan(t, fixture.document)
	resultPath := filepath.Join(filepath.Dir(planPath), applyResultFilename)
	observedAttempt := false
	var recording *recordingApplyClient
	recording = &recordingApplyClient{
		Client: newApplyFakeClient(t, fixture.objects...),
		beforeWrite: func(object client.Object) {
			if observedAttempt {
				return
			}
			observedAttempt = true
			result := readApplyResult(t, resultPath)
			if len(result.Operations) != 1 {
				t.Fatalf("operations before first API call = %#v", result.Operations)
			}
			operation := result.Operations[0]
			if operation.Outcome != operationOutcomeAttempting ||
				!operation.Attempted ||
				operation.Completed {
				t.Fatalf("pre-call operation = %#v, want durable incomplete attempt", operation)
			}
			current := &aimv1alpha2.AIMProfile{}
			if err := recording.Get(
				context.Background(),
				client.ObjectKeyFromObject(fixture.profile),
				current,
			); !apierrors.IsNotFound(err) {
				t.Fatalf("target profile existed before API call: %v", err)
			}
		},
	}

	if _, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath}); err != nil {
		t.Fatal(err)
	}
	if !observedAttempt {
		t.Fatal("Kubernetes write hook did not observe the durable attempt")
	}
}

func TestApply_ResultIsAtomicPrivateAndStripsClientErrorSecrets(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	recording := &recordingApplyClient{
		Client:      newApplyFakeClient(t, fixture.objects...),
		createError: errors.New("token=fixture-secret-value"),
	}
	planPath := writeApplyPlan(t, fixture.document)
	resultPath := filepath.Join(filepath.Dir(planPath), applyResultFilename)

	_, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err == nil {
		t.Fatal("Apply succeeded with injected create failure")
	}
	if strings.Contains(err.Error(), "fixture-secret-value") ||
		strings.Contains(err.Error(), "token=") {
		t.Fatalf("Apply returned an unsanitized error: %v", err)
	}
	assertMode(t, resultPath, 0o600)
	contents, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(contents), "fixture-secret-value") ||
		strings.Contains(string(contents), "token=") {
		t.Fatalf("apply result leaked client error secret:\n%s", contents)
	}
	result := readApplyResult(t, resultPath)
	if len(result.Operations) != 1 ||
		result.Operations[0].Outcome != "failed" ||
		!result.Operations[0].Attempted ||
		result.Operations[0].Completed ||
		result.Operations[0].Error == "" {
		t.Fatalf("failure result = %#v", result)
	}
	if result.PlanDigest == "" || result.RunID == "" {
		t.Fatalf("result lacks plan/run identity: %#v", result)
	}
	if len(result.Units) != 1 ||
		result.Units[0].Outcome != ApplyUnitFailed ||
		result.Units[0].LastAttemptedStep != string(OperationCreate) ||
		result.Units[0].LastCompletedStep != "" {
		t.Fatalf("failure unit progress = %#v", result.Units)
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
}

func TestApply_RefusesExistingResultAndPreservesHistory(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	planPath := writeApplyPlan(t, fixture.document)
	resultPath := filepath.Join(filepath.Dir(planPath), applyResultFilename)
	previous := []byte("{\"previous\":\"apply history\"}\n")
	if err := os.WriteFile(resultPath, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}

	_, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err == nil || !strings.Contains(err.Error(), "archive it before starting a new run") {
		t.Fatalf("Apply() error = %v, want existing-result refusal", err)
	}
	current, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(current, previous) {
		t.Fatalf("existing apply history changed: %q", current)
	}
	if len(recording.writes) != 0 {
		t.Fatalf("Kubernetes writes occurred despite existing result: %#v", recording.writes)
	}
}

func TestApply_ExclusiveLockRejectsConcurrentRun(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	planPath := writeApplyPlan(t, fixture.document)
	firstWrite := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once
	firstClient := &recordingApplyClient{
		Client: newApplyFakeClient(t, fixture.objects...),
		beforeWrite: func(client.Object) {
			once.Do(func() {
				close(firstWrite)
				<-releaseFirst
			})
		},
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := Apply(context.Background(), firstClient, ApplyOptions{PlanPath: planPath})
		firstResult <- err
	}()
	<-firstWrite

	secondClient := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
	_, err := Apply(context.Background(), secondClient, ApplyOptions{PlanPath: planPath})
	if err == nil || !strings.Contains(err.Error(), "exclusive apply lock") {
		t.Fatalf("concurrent Apply() error = %v, want exclusive-lock refusal", err)
	}
	if len(secondClient.writes) != 0 {
		t.Fatalf("concurrent loser wrote to Kubernetes: %#v", secondClient.writes)
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
}

func TestApply_RejectsSymlinkedResultDirectory(t *testing.T) {
	fixture := newApplyFixture(t, nil)
	planPath := writeApplyPlan(t, fixture.document)
	baseDirectory := t.TempDir()
	actualDirectory := filepath.Join(baseDirectory, "actual")
	if err := os.Mkdir(actualDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedDirectory := filepath.Join(baseDirectory, "linked")
	if err := os.Symlink(actualDirectory, linkedDirectory); err != nil {
		t.Fatal(err)
	}
	recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:   planPath,
		ResultPath: filepath.Join(linkedDirectory, applyResultFilename),
	})
	if err == nil || !strings.Contains(err.Error(), "not a non-symlink directory") {
		t.Fatalf("Apply() error = %v, want symlinked-directory rejection", err)
	}
	if len(recording.writes) != 0 {
		t.Fatalf("Apply wrote through symlinked publication directory: %#v", recording.writes)
	}
}

func TestApply_MixedBatchContinuesDeterministicallyAndManualWritesNothing(t *testing.T) {
	failed := newNamedApplyFixture(t, "a-failed", nil, false)
	succeeded := newNamedApplyFixture(t, "z-succeeded", nil, false)
	manual := MigrationUnit{
		ID:        testNamespace + "/manual",
		Namespace: testNamespace,
		Service: ResourceIdentity{
			Namespace: testNamespace,
			Name:      "manual",
		},
		Outcome: OutcomeManual,
		ProposedOperations: []ProposedOperation{{
			Action: OperationAction("delete"),
			Resource: ResourceIdentity{
				Kind:      "Secret",
				Namespace: testNamespace,
				Name:      "must-not-be-written",
			},
		}},
	}
	document := PlanDocument{
		DocumentVersion:       DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{testNamespace},
		Units: []MigrationUnit{
			succeeded.document.Units[0],
			manual,
			failed.document.Units[0],
		},
	}
	objects := append([]client.Object{}, failed.objects...)
	objects = append(objects, succeeded.objects...)
	recording := &recordingApplyClient{
		Client:      newApplyFakeClient(t, objects...),
		failWriteAt: 1,
	}
	planPath := writeApplyPlan(t, document)
	summary, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err == nil || !strings.Contains(err.Error(), failed.document.Units[0].ID) {
		t.Fatalf("batch error = %v", err)
	}
	if summary.Automatic != 2 ||
		summary.Succeeded != 1 ||
		summary.Failed != 1 ||
		summary.Manual != 1 ||
		summary.Skipped != 0 ||
		summary.Operations != 4 {
		t.Fatalf("batch summary = %#v", summary)
	}
	if len(recording.writes) != 4 {
		t.Fatalf("batch writes = %#v", recording.writes)
	}
	for _, write := range recording.writes {
		if write.name == "must-not-be-written" || !write.dryRun {
			t.Fatalf("manual or persistent write recorded: %#v", write)
		}
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}

	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.Summary != (ApplyResultSummary{Succeeded: 1, Failed: 1, Manual: 1}) {
		t.Fatalf("result summary = %#v", result.Summary)
	}
	wantIDs := []string{
		manual.ID,
		failed.document.Units[0].ID,
		succeeded.document.Units[0].ID,
	}
	wantOutcomes := []ApplyUnitOutcome{ApplyUnitManual, ApplyUnitFailed, ApplyUnitSucceeded}
	for index := range wantIDs {
		if result.Units[index].UnitID != wantIDs[index] ||
			result.Units[index].Outcome != wantOutcomes[index] {
			t.Fatalf("unit[%d] = %#v", index, result.Units[index])
		}
	}
	for _, operation := range result.Operations {
		if !operation.Attempted ||
			(operation.UnitID == succeeded.document.Units[0].ID && !operation.Completed) {
			t.Fatalf("attempt/completion result = %#v", operation)
		}
	}
}

func TestApply_MixedAutomaticPathsUseIndependentVerifiedExecutors(t *testing.T) {
	uncached := newNamedApplyFixture(t, "batch-a-uncached", nil, false)
	shared := newNamedApplyFixture(t, "batch-b-shared", nil, true)
	dedicated := newNamedDedicatedApplyFixture(t, "batch-c-dedicated", nil)
	manual := MigrationUnit{
		ID:        testNamespace + "/manual-mixed-paths",
		Namespace: testNamespace,
		Service: ResourceIdentity{
			Namespace: testNamespace,
			Name:      "manual-mixed-paths",
		},
		Outcome: OutcomeManual,
	}
	document := PlanDocument{
		DocumentVersion:       DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{testNamespace},
		Units: []MigrationUnit{
			dedicated.document.Units[0],
			manual,
			shared.document.Units[0],
			uncached.document.Units[0],
		},
	}
	objects := append([]client.Object{}, uncached.objects...)
	objects = append(objects, shared.objects...)
	objects = append(objects, dedicated.objects...)
	recording := &recordingApplyClient{Client: newApplyFakeClient(t, objects...)}
	planPath := writeApplyPlan(t, document)

	summary, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err == nil || !strings.Contains(err.Error(), "automatic Dedicated migration is prohibited") {
		t.Fatalf("Apply() error = %v, want independent Dedicated rejection", err)
	}
	if summary.Succeeded != 2 ||
		summary.Manual != 1 ||
		summary.Failed != 1 ||
		summary.Skipped != 0 ||
		summary.Operations != 7 {
		t.Fatalf("mixed-path summary = %#v", summary)
	}
	if len(recording.writes) != 7 || recording.deletes != 0 {
		t.Fatalf("mixed-path writes/deletes = %d/%d", len(recording.writes), recording.deletes)
	}
	for _, write := range recording.writes {
		if !write.dryRun || write.name == manual.Service.Name {
			t.Fatalf("mixed-path write = %#v", write)
		}
	}

	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	operationCounts := map[string]int{}
	for _, operation := range result.Operations {
		operationCounts[operation.UnitID]++
		if !operation.Attempted || !operation.Completed {
			t.Fatalf("mixed-path operation = %#v", operation)
		}
	}
	wantCounts := map[string]int{
		uncached.document.Units[0].ID: 3,
		shared.document.Units[0].ID:   4,
	}
	if !reflect.DeepEqual(operationCounts, wantCounts) {
		t.Fatalf("operation counts = %#v, want %#v", operationCounts, wantCounts)
	}
}

func TestApply_MixedUncachedSharedAndDedicatedUnitsUseExistingPaths(t *testing.T) {
	uncached := newNamedApplyFixture(t, "mixed-uncached", nil, false)
	shared := newNamedApplyFixture(t, "mixed-shared", nil, true)
	dedicated := newNamedDedicatedApplyFixture(t, "mixed-dedicated", nil)
	manual := MigrationUnit{
		ID:        testNamespace + "/mixed-manual",
		Namespace: testNamespace,
		Service: ResourceIdentity{
			Namespace: testNamespace,
			Name:      "mixed-manual",
		},
		Outcome: OutcomeManual,
	}
	document := PlanDocument{
		DocumentVersion:       DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{testNamespace},
		Units: []MigrationUnit{
			shared.document.Units[0],
			manual,
			uncached.document.Units[0],
			dedicated.document.Units[0],
		},
	}
	objects := append([]client.Object{}, uncached.objects...)
	objects = append(objects, shared.objects...)
	objects = append(objects, dedicated.objects...)
	recording := &recordingApplyClient{Client: newApplyFakeClient(t, objects...)}
	planPath := writeApplyPlan(t, document)
	summary, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Succeeded != 3 ||
		summary.Failed != 0 ||
		summary.Manual != 1 ||
		summary.Skipped != 0 ||
		summary.Operations != 14 {
		t.Fatalf("mixed-path summary = %#v", summary)
	}
	if len(recording.writes) != 14 || recording.deletes != 0 {
		t.Fatalf("mixed-path writes = %#v", recording)
	}
	for _, write := range recording.writes {
		if !write.dryRun {
			t.Fatalf("mixed-path write was not server-side dry-run: %#v", write)
		}
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.Summary != (ApplyResultSummary{Succeeded: 3, Manual: 1}) {
		t.Fatalf("mixed-path result = %#v", result.Summary)
	}
}

func TestPlanAndApply_RecordingClientRevalidatesRepeatedRunsAndNeverDeletes(t *testing.T) {
	fixture := newNamedApplyFixture(t, "repeat", nil, false)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	if _, err := Plan(
		context.Background(),
		recording,
		PlanOptions{
			Namespaces:            []string{testNamespace},
			RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(recording.writes) != 0 || recording.deletes != 0 {
		t.Fatalf("Plan mutated through the recording client: %#v", recording)
	}
	planPath := writeApplyPlan(t, fixture.document)
	if _, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath}); err != nil {
		t.Fatal(err)
	}
	writesAfterFirstRun := len(recording.writes)

	current := &aimv1alpha1.AIMServiceTemplate{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.template), current); err != nil {
		t.Fatal(err)
	}
	current.Spec.ModelName = "changed-between-runs"
	if err := base.Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}

	secondResultPath := filepath.Join(filepath.Dir(planPath), "apply-result-rerun.json")
	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:   planPath,
		ResultPath: secondResultPath,
	})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("second Apply error = %v", err)
	}
	if summary.Failed != 1 || summary.Succeeded != 0 {
		t.Fatalf("second Apply summary = %#v", summary)
	}
	if len(recording.writes) != writesAfterFirstRun {
		t.Fatalf("second run wrote before revalidation: %#v", recording.writes[writesAfterFirstRun:])
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
	result := readApplyResult(t, secondResultPath)
	if len(result.Operations) != 1 ||
		result.Operations[0].Action != "validate-sources" ||
		result.Operations[0].Completed {
		t.Fatalf("second-run result = %#v", result)
	}
}

func TestApply_CancelledBatchLeavesValidAtomicTerminalResults(t *testing.T) {
	first := newNamedApplyFixture(t, "cancel-a", nil, false)
	second := newNamedApplyFixture(t, "cancel-b", nil, false)
	document := PlanDocument{
		DocumentVersion:       DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{testNamespace},
		Units: []MigrationUnit{
			second.document.Units[0],
			first.document.Units[0],
		},
	}
	recording := &recordingApplyClient{Client: newApplyFakeClient(t)}
	planPath := writeApplyPlan(t, document)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, err := Apply(ctx, recording, ApplyOptions{PlanPath: planPath})
	if err == nil || summary.Skipped != 2 {
		t.Fatalf("cancelled summary/error = %#v / %v", summary, err)
	}
	if len(recording.writes) != 0 || recording.deletes != 0 {
		t.Fatalf("cancelled Apply wrote to Kubernetes: %#v", recording)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.Summary.Skipped != 2 || len(result.Units) != 2 {
		t.Fatalf("cancelled result = %#v", result)
	}
	for _, unit := range result.Units {
		if unit.Outcome != ApplyUnitSkipped {
			t.Fatalf("non-terminal interrupted unit = %#v", unit)
		}
	}
}

func TestApply_CancellationBeforeCutoverDoesNotPatchService(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	ctx, cancel := context.WithCancel(context.Background())
	cancelling := &cancelAfterProtectedInferenceServiceClient{
		Client: base,
		cancel: cancel,
	}
	recording := &recordingApplyClient{Client: cancelling}
	recording.afterCreate = makeSharedTargetsReady(t, base, fixture, false)

	_, err := Apply(ctx, recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled before Kubernetes mutation") {
		t.Fatalf("Apply() error = %v, want pre-mutation cancellation", err)
	}
	for _, patch := range recording.patches {
		if patch.kind == "AIMService" {
			t.Fatalf("service was patched after cancellation: %#v", recording.patches)
		}
	}
}

func TestApply_SharedDryRunValidatesProtectionAfterCacheAndPersistsNothing(t *testing.T) {
	fixture := newSharedApplyFixture(t, func(source *planFixture) {
		source.artifact.Annotations = map[string]string{"example.invalid/preserved": testPreservedValue}
	})
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeDryRun || summary.Operations != 4 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(recording.creates) != 2 || len(recording.patches) != 2 {
		t.Fatalf("writes = creates %#v patches %#v", recording.creates, recording.patches)
	}
	if recording.patches[0].name != fixture.artifact.Name ||
		recording.patches[1].name != fixture.service.Name {
		t.Fatalf("patch ordering = %#v", recording.patches)
	}
	for _, write := range append(append([]recordedApplyWrite{}, recording.creates...), recording.patches...) {
		if !write.dryRun {
			t.Fatalf("Shared dry-run write persisted: %#v", write)
		}
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); err != nil {
		t.Fatal(err)
	}
	if currentArtifact.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] != "" ||
		currentArtifact.Annotations["example.invalid/preserved"] != testPreservedValue ||
		len(currentArtifact.OwnerReferences) != 0 {
		t.Fatalf("dry-run changed Shared artifact: %#v", currentArtifact.ObjectMeta)
	}
	assertNotFound(t, base, client.ObjectKeyFromObject(fixture.profile), &aimv1alpha2.AIMProfile{})
	assertNotFound(t, base, client.ObjectKeyFromObject(fixture.cache), &aimv1alpha2.AIMProfileCache{})
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.LastOperation != string(OperationPatch) ||
		len(result.ObservedIdentities) < len(fixture.document.Units[0].ObservedResources)+1 {
		t.Fatalf("Shared dry-run diagnostics = %#v", result)
	}
}

func TestApply_SharedCommitPreservesExactArtifactPVCAndLegacyCache(t *testing.T) {
	fixture := newSharedApplyFixture(t, func(source *planFixture) {
		source.artifact.Annotations = map[string]string{"example.invalid/preserved": testPreservedValue}
		source.service.Annotations = map[string]string{"example.invalid/service": "preserved"}
	})
	legacyBefore := fixture.legacyCache.DeepCopy()
	legacyBefore.TypeMeta = metav1.TypeMeta{}
	serviceUID := fixture.service.UID
	inferenceServiceUID := fixture.inferenceService.UID
	artifactUID := fixture.artifact.UID
	claimUID := fixture.claim.UID

	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeSharedTargetsReady(t, base, fixture, false)
	recording.afterPatch = readyServiceAfterPatch(t, base, fixture, types.UID("created-profile-uid"))
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  200 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeCommit || summary.Operations != 6 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(recording.patches) != 2 ||
		recording.patches[0].name != fixture.artifact.Name ||
		recording.patches[1].name != fixture.service.Name ||
		recording.deletes != 0 {
		t.Fatalf("writes = patches %#v deletes %d", recording.patches, recording.deletes)
	}

	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); err != nil {
		t.Fatal(err)
	}
	if currentArtifact.UID != artifactUID ||
		currentArtifact.Status.PersistentVolumeClaim != fixture.claim.Name ||
		len(currentArtifact.OwnerReferences) != 0 ||
		currentArtifact.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] !=
			aimv1alpha1.ArtifactEvictionProtectedValue ||
		currentArtifact.Annotations["example.invalid/preserved"] != testPreservedValue {
		t.Fatalf("Shared artifact was not preserved exactly: %#v", currentArtifact)
	}
	currentClaim := &corev1.PersistentVolumeClaim{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.claim), currentClaim); err != nil {
		t.Fatal(err)
	}
	if currentClaim.UID != claimUID {
		t.Fatalf("PVC UID = %q, want %q", currentClaim.UID, claimUID)
	}
	currentLegacy := &aimv1alpha1.AIMTemplateCache{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.legacyCache), currentLegacy); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(currentLegacy, legacyBefore) {
		t.Fatalf("legacy Shared cache changed\ncurrent: %#v\nbefore: %#v", currentLegacy, legacyBefore)
	}
	currentService := &aimv1alpha2.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), currentService); err != nil {
		t.Fatal(err)
	}
	if currentService.UID != serviceUID ||
		currentService.Spec.Template != nil ||
		currentService.Spec.Profile == nil ||
		currentService.Annotations["example.invalid/service"] != "preserved" {
		t.Fatalf("service identity/config was not preserved: %#v", currentService)
	}
	currentInferenceService := &servingv1beta1.InferenceService{}
	if err := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.inferenceService),
		currentInferenceService,
	); err != nil {
		t.Fatal(err)
	}
	if currentInferenceService.UID != inferenceServiceUID {
		t.Fatalf("InferenceService UID = %q, want %q", currentInferenceService.UID, inferenceServiceUID)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.LastOperation != "verify-cutover" ||
		!resultObservedUID(result, string(artifactUID)) ||
		!resultObservedUID(result, string(inferenceServiceUID)) {
		t.Fatalf("Shared commit diagnostics = %#v", result)
	}
}

func TestApply_SharedCommitRejectsCacheAuthorityRace(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	makeReady := makeSharedTargetsReady(t, base, fixture, false)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = func(ctx context.Context, object client.Object) error {
		if err := makeReady(ctx, object); err != nil {
			return err
		}
		if _, ok := object.(*aimv1alpha2.AIMProfileCache); !ok {
			return nil
		}
		return base.Create(ctx, &aimv1alpha2.AIMProfileCache{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "competing-shared-cache",
				Namespace: fixture.cache.Namespace,
			},
			Spec: aimv1alpha2.AIMProfileCacheSpec{
				ProfileName:  fixture.cache.Spec.ProfileName,
				ProfileScope: fixture.cache.Spec.ProfileScope,
				Mode:         aimv1alpha2.ProfileCacheModeShared,
			},
		})
	}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "multiple Shared AIMProfileCache candidates") {
		t.Fatalf("Apply() error = %v, want cache authority race rejection", err)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.artifact),
		currentArtifact,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentArtifact.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] != "" {
		t.Fatalf("cache authority race crossed protection boundary: %#v", currentArtifact.Annotations)
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.service),
		currentService,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("cache authority race crossed service cutover fence: %#v", currentService.Spec)
	}
}

func TestApply_SharedCommitRejectsRecreatedLivePVCBeforeCutover(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	makeReady := makeSharedTargetsReady(t, base, fixture, false)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = func(ctx context.Context, object client.Object) error {
		if err := makeReady(ctx, object); err != nil {
			return err
		}
		if _, ok := object.(*aimv1alpha2.AIMProfileCache); !ok {
			return nil
		}
		current := &corev1.PersistentVolumeClaim{}
		key := client.ObjectKeyFromObject(fixture.claim)
		if err := base.Get(ctx, key, current); err != nil {
			return err
		}
		if err := base.Delete(ctx, current); err != nil {
			return err
		}
		replacement := fixture.claim.DeepCopy()
		replacement.ResourceVersion = ""
		replacement.UID = "replacement-pvc-uid"
		return base.Create(ctx, replacement)
	}

	_, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "PersistentVolumeClaim") ||
		!strings.Contains(err.Error(), "UID changed") {
		t.Fatalf("Apply() error = %v, want live PVC identity rejection", err)
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(
		context.Background(),
		client.ObjectKeyFromObject(fixture.service),
		currentService,
	); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("PVC replacement crossed service cutover fence: %#v", currentService.Spec)
	}
}

func TestApply_SharedCommitReusesPinnedReadyProfileCache(t *testing.T) {
	fixture := newSharedApplyFixture(t, nil)
	profile, cache, runtimeObject, runtimeConfigMap := fixture.makeReadyReusableTargets(t)
	cache.Status.Artifacts = map[string]aimv1alpha1.AIMResolvedArtifact{
		fixture.artifact.Spec.ModelID: {
			UID:                   string(fixture.artifact.UID),
			Name:                  fixture.artifact.Name,
			Model:                 fixture.artifact.Spec.ModelID,
			Status:                constants.AIMStatusReady,
			PersistentVolumeClaim: fixture.claim.Name,
		},
	}
	cacheIdentity := resourceIdentityForTest(
		t,
		cache,
		aimv1alpha2.GroupVersion.String(),
		resourceKindProfileCache,
		"target-profile-cache",
	)
	unit := &fixture.document.Units[0]
	unit.ProposedOperations[1] = ProposedOperation{
		Action:   OperationReuse,
		Resource: cacheIdentity,
	}
	unit.ObservedResources = append(unit.ObservedResources, cacheIdentity)
	fixture.objects = append(fixture.objects, profile, cache, runtimeObject, runtimeConfigMap)

	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterPatch = readyServiceAfterPatch(t, base, fixture, profile.UID)
	summary, err := Apply(context.Background(), recording, ApplyOptions{
		PlanPath:     writeApplyPlan(t, fixture.document),
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Operations != 6 ||
		len(recording.creates) != 0 ||
		len(recording.patches) != 2 ||
		recording.deletes != 0 {
		t.Fatalf("reuse summary/writes = %#v %#v", summary, recording)
	}
	current := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), current); err != nil {
		t.Fatal(err)
	}
	if current.UID != fixture.artifact.UID ||
		len(current.OwnerReferences) != 0 ||
		current.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] !=
			aimv1alpha1.ArtifactEvictionProtectedValue {
		t.Fatalf("reused-cache adoption changed artifact identity/ownership: %#v", current)
	}
}

func TestApply_SharedSemanticMismatchMatrixFailsBeforeWrites(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*applyFixture)
		want   string
	}{
		{
			name: "source URI",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.SourceURI = "hf://other/model"
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "source URI",
		},
		{
			name: "model identity",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.ModelID = "other/model"
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "model identity",
		},
		{
			name: "storage",
			mutate: func(f *applyFixture) {
				class := testOtherValue
				f.legacyCache.Spec.StorageClassName = class
				f.artifact.Spec.StorageClassName = class
				f.claim.Spec.StorageClassName = &class
				refreshFixturePin(t, f, f.legacyCache, "source-template-cache")
				refreshFixturePin(t, f, f.artifact, "source-artifact")
				refreshFixturePin(t, f, f.claim, "source-pvc")
			},
			want: "storage classes",
		},
		{
			name: "runtime configuration",
			mutate: func(f *applyFixture) {
				ref := aimv1alpha1.RuntimeConfigRef{Name: testOtherValue}
				f.legacyCache.Spec.RuntimeConfigRef = ref
				f.artifact.Spec.RuntimeConfigRef = ref
				refreshFixturePin(t, f, f.legacyCache, "source-template-cache")
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "runtime configuration",
		},
		{
			name: "environment",
			mutate: func(f *applyFixture) {
				env := []corev1.EnvVar{{Name: "OTHER", Value: "one"}}
				f.legacyCache.Spec.Env = env
				f.artifact.Spec.Env = env
				refreshFixturePin(t, f, f.legacyCache, "source-template-cache")
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "environments",
		},
		{
			name: "download filter",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.DownloadFilter = &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}}
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "download filters",
		},
		{
			name: "download image",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.ModelDownloadImage = "registry.example/other:v1"
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "download images",
		},
		{
			name: "adapter disk",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}
				f.artifact.Status.AdapterPersistentVolumeClaim = "adapter-pvc"
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "adapter-disk",
		},
		{
			name: "scope",
			mutate: func(f *applyFixture) {
				f.legacyCache.Spec.TemplateScope = aimv1alpha1.AIMServiceTemplateScopeCluster
				refreshFixturePin(t, f, f.legacyCache, "source-template-cache")
			},
			want: "scope",
		},
		{
			name: "image pull secret",
			mutate: func(f *applyFixture) {
				f.artifact.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "pull-secret"}}
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "pull secrets",
		},
		{
			name: "not Ready",
			mutate: func(f *applyFixture) {
				f.artifact.Status.Status = constants.AIMStatusProgressing
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "current, Ready, and ownerless",
		},
		{
			name: "not ownerless",
			mutate: func(f *applyFixture) {
				f.artifact.OwnerReferences = []metav1.OwnerReference{{UID: "unexpected-owner"}}
				refreshFixturePin(t, f, f.artifact, "source-artifact")
			},
			want: "current, Ready, and ownerless",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSharedApplyFixture(t, nil)
			test.mutate(fixture)
			recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
			planPath := writeApplyPlan(t, fixture.document)
			_, err := Apply(context.Background(), recording, ApplyOptions{PlanPath: planPath})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(recording.creates) != 0 ||
				len(recording.patches) != 0 ||
				recording.deletes != 0 {
				t.Fatalf("semantic mismatch wrote to Kubernetes: %#v", recording)
			}
			current := &aimv1alpha2.AIMService{}
			if getErr := recording.Get(
				context.Background(),
				client.ObjectKeyFromObject(fixture.service),
				current,
			); getErr != nil {
				t.Fatal(getErr)
			}
			if current.Spec.Template == nil || current.Spec.Profile != nil {
				t.Fatalf("semantic mismatch cut over service: %#v", current.Spec)
			}
			result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
			if result.LastOperation != "validate-sources" {
				t.Fatalf("mismatch diagnostics = %#v", result)
			}
		})
	}
}

func TestValidateSharedArtifactSemantics_OptionalTargetSize(t *testing.T) {
	tests := []struct {
		name       string
		targetSize *resource.Quantity
		wantError  bool
	}{
		{
			name: "omitted target size accepts discovered artifact size",
		},
		{
			name:       "explicit conflicting target size is rejected",
			targetSize: ptr.To(resourceMustParse(t, "2Gi")),
			wantError:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSharedApplyFixture(t, nil)
			fixture.artifact.Spec.Size = resourceMustParse(t, "1Gi")
			modelSource := fixture.profile.Spec.ModelSources[0]
			modelSource.Size = test.targetSize
			err := validateSharedArtifactSemantics(
				context.Background(),
				newApplyFakeClient(t, fixture.objects...),
				fixture.cache,
				fixture.legacyCache,
				fixture.artifact,
				fixture.claim,
				modelSource,
			)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "size") {
					t.Fatalf("error = %v, want size mismatch", err)
				}
			} else if err != nil {
				t.Fatalf("omitted target size rejected discovered artifact size: %v", err)
			}
		})
	}
}

func TestValidateSharedArtifactSemantics_DownloadFilter(t *testing.T) {
	tests := []struct {
		name           string
		artifactFilter *aimv1alpha1.AIMDownloadFilter
		targetFilter   *aimv1alpha1.AIMDownloadFilter
		wantError      bool
	}{
		{name: "neither declares a filter"},
		{
			name:           "equal filters in different order",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin", "*.md"}},
			targetFilter:   &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.md", "*.bin"}},
		},
		{
			name:         "target declares a filter the artifact lacks",
			targetFilter: &aimv1alpha1.AIMDownloadFilter{},
			wantError:    true,
		},
		{
			name:         "target declares the default the artifact inherits",
			targetFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
			wantError:    true,
		},
		{
			name:           "different filters",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
			targetFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
			wantError:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSharedApplyFixture(t, nil)
			fixture.artifact.Spec.DownloadFilter = test.artifactFilter
			modelSource := fixture.profile.Spec.ModelSources[0]
			modelSource.DownloadFilter = test.targetFilter
			err := validateSharedArtifactSemantics(
				context.Background(),
				newApplyFakeClient(t, fixture.objects...),
				fixture.cache,
				fixture.legacyCache,
				fixture.artifact,
				fixture.claim,
				modelSource,
			)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "download filter") {
					t.Fatalf("error = %v, want download filter mismatch", err)
				}
			} else if err != nil {
				t.Fatalf("matching download filters rejected: %v", err)
			}
		})
	}
}

// The controller never adopts an artifact whose filter differs from the source's,
// so such an artifact must not count toward adoption ambiguity either.
func TestValidateSharedArtifactCandidates_IgnoresOtherDownloadFilters(t *testing.T) {
	tests := []struct {
		name          string
		otherFilter   *aimv1alpha1.AIMDownloadFilter
		wantAmbiguous bool
	}{
		{name: "same filter is ambiguous", wantAmbiguous: true},
		{name: "different filter is not a candidate", otherFilter: &aimv1alpha1.AIMDownloadFilter{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSharedApplyFixture(t, nil)
			other := fixture.artifact.DeepCopy()
			other.Name = "other-artifact"
			other.UID = "other-artifact-uid"
			other.ResourceVersion = "99"
			other.Status.PersistentVolumeClaim = "other-pvc"
			other.Spec.DownloadFilter = test.otherFilter
			objects := append(fixture.objects, other)

			err := validateSharedArtifactCandidates(
				context.Background(),
				newApplyFakeClient(t, objects...),
				fixture.cache,
				fixture.artifact,
				fixture.profile.Spec.ModelSources[0],
			)
			if test.wantAmbiguous {
				if err == nil || !strings.Contains(err.Error(), "ambiguous") {
					t.Fatalf("error = %v, want ambiguity", err)
				}
			} else if err != nil {
				t.Fatalf("artifact with a different filter counted as a candidate: %v", err)
			}
		})
	}
}

//nolint:gocyclo // Related fail-closed cases intentionally share one end-to-end fixture test.
func TestApply_SharedAmbiguityAndWrongAdoptionFailBeforeCutover(t *testing.T) {
	t.Run("ambiguous artifact candidates", func(t *testing.T) {
		fixture := newSharedApplyFixture(t, nil)
		other := fixture.artifact.DeepCopy()
		other.Name = "other-artifact"
		other.UID = "other-artifact-uid"
		other.ResourceVersion = "99"
		other.Status.PersistentVolumeClaim = "other-pvc"
		fixture.objects = append(fixture.objects, other)
		recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
		_, err := Apply(context.Background(), recording, ApplyOptions{
			PlanPath: writeApplyPlan(t, fixture.document),
		})
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("error = %v, want ambiguity", err)
		}
		if len(recording.creates) != 0 || len(recording.patches) != 0 || recording.deletes != 0 {
			t.Fatalf("ambiguity wrote to Kubernetes: %#v", recording)
		}
	})

	t.Run("ambiguous profile-cache candidates", func(t *testing.T) {
		fixture := newSharedApplyFixture(t, nil)
		other := fixture.cache.DeepCopy()
		other.Name = "other-shared-cache"
		other.UID = "other-cache-uid"
		other.ResourceVersion = "98"
		other.Generation = 1
		other.Status = aimv1alpha2.AIMProfileCacheStatus{
			ObservedGeneration: 1,
			Status:             constants.AIMStatusReady,
		}
		fixture.objects = append(fixture.objects, other)
		recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
		_, err := Apply(context.Background(), recording, ApplyOptions{
			PlanPath: writeApplyPlan(t, fixture.document),
		})
		if err == nil || !strings.Contains(err.Error(), "candidate") {
			t.Fatalf("error = %v, want profile-cache ambiguity", err)
		}
		if len(recording.creates) != 0 || len(recording.patches) != 0 || recording.deletes != 0 {
			t.Fatalf("profile-cache ambiguity wrote to Kubernetes: %#v", recording)
		}
	})

	t.Run("artifact identity changed", func(t *testing.T) {
		fixture := newSharedApplyFixture(t, nil)
		fixture.artifact.UID = "replacement-artifact-uid"
		fixture.artifact.ResourceVersion = "97"
		recording := &recordingApplyClient{Client: newApplyFakeClient(t, fixture.objects...)}
		_, err := Apply(context.Background(), recording, ApplyOptions{
			PlanPath: writeApplyPlan(t, fixture.document),
		})
		if err == nil || !strings.Contains(err.Error(), "UID changed") {
			t.Fatalf("error = %v, want artifact identity drift", err)
		}
		if len(recording.creates) != 0 || len(recording.patches) != 0 || recording.deletes != 0 {
			t.Fatalf("identity drift wrote to Kubernetes: %#v", recording)
		}
	})

	t.Run("Ready cache reports wrong predecessor", func(t *testing.T) {
		fixture := newSharedApplyFixture(t, nil)
		base := newApplyFakeClient(t, fixture.objects...)
		recording := &recordingApplyClient{Client: base}
		recording.afterCreate = makeSharedTargetsReady(t, base, fixture, true)
		planPath := writeApplyPlan(t, fixture.document)
		_, err := Apply(context.Background(), recording, ApplyOptions{
			PlanPath:     planPath,
			Commit:       true,
			WaitTimeout:  100 * time.Millisecond,
			PollInterval: time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "exact predecessor artifact UID and PVC") {
			t.Fatalf("error = %v", err)
		}
		if len(recording.patches) != 0 || recording.deletes != 0 {
			t.Fatalf("wrong adoption mutated protected resources: %#v", recording)
		}
		currentService := &aimv1alpha2.AIMService{}
		if getErr := base.Get(
			context.Background(),
			client.ObjectKeyFromObject(fixture.service),
			currentService,
		); getErr != nil {
			t.Fatal(getErr)
		}
		if currentService.Spec.Template == nil || currentService.Spec.Profile != nil {
			t.Fatalf("wrong adoption cut over service: %#v", currentService.Spec)
		}
		currentArtifact := &aimv1alpha1.AIMArtifact{}
		if getErr := base.Get(
			context.Background(),
			client.ObjectKeyFromObject(fixture.artifact),
			currentArtifact,
		); getErr != nil {
			t.Fatal(getErr)
		}
		if currentArtifact.Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] != "" ||
			len(currentArtifact.OwnerReferences) != 0 {
			t.Fatalf("wrong adoption changed artifact: %#v", currentArtifact.ObjectMeta)
		}
		result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
		if result.LastOperation != "verify-shared-adoption" ||
			!resultObservedUID(result, "created-cache-uid") {
			t.Fatalf("wrong-adoption diagnostics = %#v", result)
		}
	})

	t.Run("adoption timeout", func(t *testing.T) {
		fixture := newSharedApplyFixture(t, nil)
		base := newApplyFakeClient(t, fixture.objects...)
		recording := &recordingApplyClient{Client: base}
		readyTargets := makeSharedTargetsReady(t, base, fixture, false)
		recording.afterCreate = func(ctx context.Context, object client.Object) error {
			if err := readyTargets(ctx, object); err != nil {
				return err
			}
			if _, ok := object.(*aimv1alpha2.AIMProfileCache); !ok {
				return nil
			}
			current := &aimv1alpha2.AIMProfileCache{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), current); err != nil {
				return err
			}
			current.Status.Status = constants.AIMStatusProgressing
			current.Status.Artifacts = nil
			return base.Status().Update(ctx, current)
		}
		planPath := writeApplyPlan(t, fixture.document)
		_, err := Apply(context.Background(), recording, ApplyOptions{
			PlanPath:     planPath,
			Commit:       true,
			WaitTimeout:  5 * time.Millisecond,
			PollInterval: time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "did not become Ready") {
			t.Fatalf("error = %v, want adoption timeout", err)
		}
		if len(recording.patches) != 0 || recording.deletes != 0 {
			t.Fatalf("adoption timeout mutated cutover resources: %#v", recording)
		}
		result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
		if result.LastOperation != "wait-profile-cache" ||
			!resultObservedUID(result, "created-cache-uid") {
			t.Fatalf("adoption-timeout diagnostics = %#v", result)
		}
	})
}

func applyDedicatedForTest(
	ctx context.Context,
	kubernetesClient client.Client,
	options ApplyOptions,
) (ApplySummary, error) {
	return apply(ctx, kubernetesClient, options, true)
}

//nolint:gocyclo // The ordered-write assertion intentionally verifies every handoff boundary in one flow.
func TestApply_DedicatedDryRunValidatesOrderedWritesWithoutPersistence(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, func(source *planFixture) {
		source.cache.Annotations = map[string]string{"example.invalid/cache": testPreservedValue}
		source.artifact.Annotations = map[string]string{"example.invalid/artifact": testPreservedValue}
	})
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{PlanPath: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeDryRun || summary.Operations != 7 {
		t.Fatalf("summary = %#v", summary)
	}
	wantWrites := []struct {
		verb   string
		kind   string
		name   string
		paused bool
	}{
		{verb: "create", kind: resourceKindProfile, name: fixture.profile.Name},
		{verb: "patch", kind: resourceKindTemplateCache, name: fixture.legacyCache.Name, paused: true},
		{verb: "create", kind: resourceKindProfileCache, name: fixture.cache.Name, paused: true},
		{verb: "patch", kind: resourceKindArtifact, name: fixture.artifact.Name},
		{verb: "create", kind: resourceKindProfileCache, name: fixture.cache.Name},
		{verb: "patch", kind: resourceKindArtifact, name: fixture.artifact.Name},
		{verb: "patch", kind: "AIMService", name: fixture.service.Name},
	}
	if len(recording.writes) != len(wantWrites) {
		t.Fatalf("writes = %#v", recording.writes)
	}
	for index, want := range wantWrites {
		got := recording.writes[index]
		if got.verb != want.verb ||
			got.kind != want.kind ||
			got.name != want.name ||
			got.paused != want.paused ||
			!got.dryRun {
			t.Fatalf("write[%d] = %#v, want %#v with DryRunAll", index, got, want)
		}
	}
	stagedOwners := recording.writes[3].ownerReferences
	if len(stagedOwners) != 2 ||
		stagedOwners[0].UID != fixture.legacyCache.UID ||
		stagedOwners[0].Controller == nil ||
		!*stagedOwners[0].Controller ||
		stagedOwners[1].Controller == nil ||
		*stagedOwners[1].Controller ||
		stagedOwners[1].BlockOwnerDeletion == nil ||
		!*stagedOwners[1].BlockOwnerDeletion {
		t.Fatalf("staged owners = %#v", stagedOwners)
	}
	transferredOwners := recording.writes[5].ownerReferences
	if len(transferredOwners) != 1 ||
		transferredOwners[0].Kind != resourceKindProfileCache ||
		transferredOwners[0].Controller == nil ||
		!*transferredOwners[0].Controller ||
		transferredOwners[0].BlockOwnerDeletion == nil ||
		!*transferredOwners[0].BlockOwnerDeletion {
		t.Fatalf("transferred owners = %#v", transferredOwners)
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
	currentCache := &aimv1alpha1.AIMTemplateCache{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.legacyCache), currentCache); err != nil {
		t.Fatal(err)
	}
	if currentCache.Annotations[constants.AnnotationReconciliationPaused] != "" ||
		currentCache.Annotations["example.invalid/cache"] != testPreservedValue {
		t.Fatalf("dry-run changed old cache: %#v", currentCache.Annotations)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(currentArtifact.OwnerReferences, fixture.artifact.OwnerReferences) ||
		currentArtifact.Annotations["example.invalid/artifact"] != testPreservedValue {
		t.Fatalf("dry-run changed artifact: %#v", currentArtifact.ObjectMeta)
	}
	assertNotFound(t, base, client.ObjectKeyFromObject(fixture.cache), &aimv1alpha2.AIMProfileCache{})
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	wantActions := []string{
		"create",
		actionPauseSourceCache,
		actionCreatePausedCache,
		actionStageArtifactOwner,
		actionUnpauseTargetCache,
		actionTransferArtifactOwner,
		"patch",
	}
	for index, want := range wantActions {
		if result.Operations[index].Action != want ||
			result.Operations[index].Outcome != operationOutcomeDryRun {
			t.Fatalf("operation[%d] = %#v, want action %q dry-run", index, result.Operations[index], want)
		}
	}
}

func TestDedicatedOwnerReferencesPreserveDeletionProtectionFlags(t *testing.T) {
	tests := []struct {
		name  string
		value *bool
	}{
		{name: "unset"},
		{name: "false", value: ptr.To(false)},
		{name: "enabled", value: ptr.To(true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared := &preparedApply{
				dedicated: &preparedDedicatedApply{
					oldOwner: metav1.OwnerReference{
						APIVersion:         aimv1alpha1.GroupVersion.String(),
						Kind:               resourceKindTemplateCache,
						Name:               "old-cache",
						UID:                "old-cache-uid",
						Controller:         ptr.To(true),
						BlockOwnerDeletion: test.value,
					},
				},
			}
			target := &aimv1alpha2.AIMProfileCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: "new-cache",
					UID:  "new-cache-uid",
				},
			}
			staged := dedicatedDualOwnerReferences(prepared, target)
			if len(staged) != 2 ||
				staged[0].Controller == nil ||
				!*staged[0].Controller ||
				staged[1].Controller == nil ||
				*staged[1].Controller ||
				!equality.Semantic.DeepEqual(staged[0].BlockOwnerDeletion, test.value) ||
				!equality.Semantic.DeepEqual(staged[1].BlockOwnerDeletion, test.value) {
				t.Fatalf("staged owners = %#v", staged)
			}
			transferred := dedicatedTransferredOwnerReference(prepared, target)
			if transferred.Controller == nil ||
				!*transferred.Controller ||
				!equality.Semantic.DeepEqual(transferred.BlockOwnerDeletion, test.value) {
				t.Fatalf("transferred owner = %#v", transferred)
			}
		})
	}
}

//nolint:gocyclo // The end-to-end assertion keeps all preserved identities and owner flags visible together.
func TestApply_DedicatedCommitTransfersExactArtifactThenCutsOver(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, func(source *planFixture) {
		source.cache.Annotations = map[string]string{"example.invalid/cache": testPreservedValue}
		source.cache.Finalizers = []string{"aim.eai.amd.com/artifactcleanup"}
		source.artifact.Annotations = map[string]string{"example.invalid/artifact": testPreservedValue}
		source.service.Annotations = map[string]string{"example.invalid/service": testPreservedValue}
	})
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	recording.afterPatch = makeDedicatedPatchCallback(t, base, fixture, false)
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  200 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != ApplyModeCommit || summary.Operations != 10 {
		t.Fatalf("summary = %#v", summary)
	}
	if recording.deletes != 0 {
		t.Fatalf("Delete calls = %d", recording.deletes)
	}
	wantOrder := []string{
		"AIMProfile/" + fixture.profile.Name,
		"AIMTemplateCache/" + fixture.legacyCache.Name,
		"AIMProfileCache/" + fixture.cache.Name,
		"AIMArtifact/" + fixture.artifact.Name,
		"AIMProfileCache/" + fixture.cache.Name,
		"AIMArtifact/" + fixture.artifact.Name,
		"AIMService/" + fixture.service.Name,
	}
	if len(recording.writes) != len(wantOrder) {
		t.Fatalf("writes = %#v", recording.writes)
	}
	for index, want := range wantOrder {
		got := recording.writes[index].kind + "/" + recording.writes[index].name
		if got != want || recording.writes[index].dryRun {
			t.Fatalf("write[%d] = %#v, want %q commit", index, recording.writes[index], want)
		}
	}
	if !recording.writes[1].paused ||
		!recording.writes[2].paused ||
		recording.writes[4].paused {
		t.Fatalf("pause ordering = %#v", recording.writes)
	}
	currentLegacy := &aimv1alpha1.AIMTemplateCache{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.legacyCache), currentLegacy); err != nil {
		t.Fatal(err)
	}
	if currentLegacy.UID != fixture.legacyCache.UID ||
		currentLegacy.Annotations[constants.AnnotationReconciliationPaused] != reconciliationPausedValue ||
		currentLegacy.Annotations["example.invalid/cache"] != testPreservedValue ||
		!equality.Semantic.DeepEqual(currentLegacy.Finalizers, fixture.legacyCache.Finalizers) {
		t.Fatalf("old Dedicated cache was not preserved and paused: %#v", currentLegacy)
	}
	currentCache := &aimv1alpha2.AIMProfileCache{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.cache), currentCache); err != nil {
		t.Fatal(err)
	}
	if currentCache.UID != "created-dedicated-cache-uid" ||
		currentCache.Annotations[constants.AnnotationReconciliationPaused] != "" ||
		len(currentCache.OwnerReferences) != 1 ||
		currentCache.OwnerReferences[0].UID != fixture.service.UID ||
		currentCache.OwnerReferences[0].Controller == nil ||
		!*currentCache.OwnerReferences[0].Controller {
		t.Fatalf("target Dedicated cache state = %#v", currentCache)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); err != nil {
		t.Fatal(err)
	}
	if currentArtifact.UID != fixture.artifact.UID ||
		currentArtifact.Status.PersistentVolumeClaim != fixture.claim.Name ||
		currentArtifact.Annotations["example.invalid/artifact"] != testPreservedValue ||
		len(currentArtifact.OwnerReferences) != 1 ||
		currentArtifact.OwnerReferences[0].UID != currentCache.UID ||
		currentArtifact.OwnerReferences[0].Controller == nil ||
		!*currentArtifact.OwnerReferences[0].Controller ||
		currentArtifact.OwnerReferences[0].BlockOwnerDeletion == nil ||
		!*currentArtifact.OwnerReferences[0].BlockOwnerDeletion {
		t.Fatalf("Dedicated artifact transfer = %#v", currentArtifact)
	}
	currentClaim := &corev1.PersistentVolumeClaim{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.claim), currentClaim); err != nil {
		t.Fatal(err)
	}
	if currentClaim.UID != fixture.claim.UID {
		t.Fatalf("PVC UID = %q, want %q", currentClaim.UID, fixture.claim.UID)
	}
	currentService := &aimv1alpha2.AIMService{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), currentService); err != nil {
		t.Fatal(err)
	}
	if currentService.UID != fixture.service.UID ||
		currentService.Spec.Template != nil ||
		currentService.Spec.Profile == nil ||
		currentService.Spec.Profile.Name != fixture.profile.Name ||
		currentService.Annotations["example.invalid/service"] != testPreservedValue {
		t.Fatalf("service cutover = %#v", currentService)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	wantActions := []string{
		"create",
		actionPauseSourceCache,
		actionCreatePausedCache,
		actionStageArtifactOwner,
		actionUnpauseTargetCache,
		actionVerifyDedicatedCache,
		actionTransferArtifactOwner,
		actionVerifyDedicatedHandoff,
		"patch",
		"verify-cutover",
	}
	for index, want := range wantActions {
		if result.Operations[index].Action != want {
			t.Fatalf("operation[%d] = %#v, want action %q", index, result.Operations[index], want)
		}
	}
	if !resultObservedUID(result, string(fixture.claim.UID)) ||
		!resultObservedUID(result, string(currentCache.UID)) {
		t.Fatalf("Dedicated result identities = %#v", result.ObservedIdentities)
	}
	transfer := result.Operations[6].CurrentState
	if transfer == nil ||
		len(transfer.OwnerReferences) != 1 ||
		transfer.PersistentVolumeClaim != fixture.claim.Name {
		t.Fatalf("transfer recovery state = %#v", transfer)
	}
}

func TestApply_DedicatedAlreadyExistsRequiresExactPausedCache(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, nil)
	conflict := fixture.cache.DeepCopy()
	conflict.UID = "conflicting-cache-uid"
	conflict.ResourceVersion = "77"
	conflict.Generation = 1
	conflict.Annotations = map[string]string{constants.AnnotationReconciliationPaused: reconciliationPausedValue}
	conflict.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMService",
		Name:       "other-service",
		UID:        "other-service-uid",
		Controller: ptr.To(true),
	}}
	fixture.objects = append(fixture.objects, conflict)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	planPath := writeApplyPlan(t, fixture.document)
	_, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "exact paused Dedicated") {
		t.Fatalf("error = %v", err)
	}
	currentLegacy := &aimv1alpha1.AIMTemplateCache{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.legacyCache), currentLegacy); getErr != nil {
		t.Fatal(getErr)
	}
	if currentLegacy.Annotations[constants.AnnotationReconciliationPaused] != reconciliationPausedValue {
		t.Fatalf("old cache pause was rolled back: %#v", currentLegacy.Annotations)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); getErr != nil {
		t.Fatal(getErr)
	}
	if !equality.Semantic.DeepEqual(currentArtifact.OwnerReferences, fixture.artifact.OwnerReferences) {
		t.Fatalf("AlreadyExists conflict changed artifact owners: %#v", currentArtifact.OwnerReferences)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	last := result.Operations[len(result.Operations)-1]
	if last.Action != actionCreatePausedCache ||
		last.Outcome != operationOutcomeConflict ||
		last.CurrentState == nil ||
		len(last.CurrentState.OwnerReferences) != 1 {
		t.Fatalf("AlreadyExists diagnostics = %#v", last)
	}
}

func TestApply_DedicatedReusesOnlyExactPausedAlreadyExistsForRecovery(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, nil)
	existing := fixture.cache.DeepCopy()
	existing.UID = "recovery-cache-uid"
	existing.ResourceVersion = "78"
	existing.Generation = 1
	existing.Annotations = map[string]string{constants.AnnotationReconciliationPaused: reconciliationPausedValue}
	existing.Finalizers = []string{"aim.eai.amd.com/profile-cache-artifact-cleanup"}
	existing.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMService",
		Name:       fixture.service.Name,
		UID:        fixture.service.UID,
		Controller: ptr.To(true),
	}}
	fixture.objects = append(fixture.objects, existing)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	recording.afterPatch = makeDedicatedPatchCallback(t, base, fixture, false)
	planPath := writeApplyPlan(t, fixture.document)
	summary, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  200 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Operations != 10 {
		t.Fatalf("summary = %#v", summary)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	if result.Operations[2].Action != actionCreatePausedCache ||
		result.Operations[2].Outcome != operationOutcomeReused ||
		result.Operations[2].CurrentIdentity == nil ||
		result.Operations[2].CurrentIdentity.UID != string(existing.UID) {
		t.Fatalf("recovery observation = %#v", result.Operations[2])
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); err != nil {
		t.Fatal(err)
	}
	if len(currentArtifact.OwnerReferences) != 1 ||
		currentArtifact.OwnerReferences[0].UID != existing.UID {
		t.Fatalf("recovered target cache did not receive artifact: %#v", currentArtifact.OwnerReferences)
	}
}

func TestApply_DedicatedFailureBoundariesStopWithoutCompensation(t *testing.T) {
	tests := []struct {
		name       string
		failWrite  int
		wantAction string
	}{
		{name: "pause old cache", failWrite: 2, wantAction: actionPauseSourceCache},
		{name: "create paused cache", failWrite: 3, wantAction: actionCreatePausedCache},
		{name: "stage artifact owner", failWrite: 4, wantAction: actionStageArtifactOwner},
		{name: "unpause target cache", failWrite: 5, wantAction: actionUnpauseTargetCache},
		{name: "transfer artifact owner", failWrite: 6, wantAction: actionTransferArtifactOwner},
		{name: "cut over service", failWrite: 7, wantAction: string(OperationPatch)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDedicatedApplyFixture(t, nil)
			base := newApplyFakeClient(t, fixture.objects...)
			recording := &recordingApplyClient{
				Client:      base,
				failWriteAt: test.failWrite,
			}
			recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
			recording.afterPatch = makeDedicatedPatchCallback(t, base, fixture, false)
			planPath := writeApplyPlan(t, fixture.document)
			_, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
				PlanPath:     planPath,
				Commit:       true,
				WaitTimeout:  100 * time.Millisecond,
				PollInterval: time.Millisecond,
			})
			if err == nil {
				t.Fatal("Apply succeeded with injected boundary failure")
			}
			if recording.writeCount != test.failWrite || recording.deletes != 0 {
				t.Fatalf("writes/deletes = %d/%d", recording.writeCount, recording.deletes)
			}
			result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
			last := result.Operations[len(result.Operations)-1]
			if last.Action != test.wantAction || last.Outcome != operationOutcomeFailed {
				t.Fatalf("last operation = %#v", last)
			}
			currentService := &aimv1alpha2.AIMService{}
			if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), currentService); getErr != nil {
				t.Fatal(getErr)
			}
			if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
				t.Fatalf("failure boundary cut over service: %#v", currentService.Spec)
			}
			currentLegacy := &aimv1alpha1.AIMTemplateCache{}
			if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.legacyCache), currentLegacy); getErr != nil {
				t.Fatal(getErr)
			}
			if test.failWrite > 2 &&
				currentLegacy.Annotations[constants.AnnotationReconciliationPaused] != reconciliationPausedValue {
				t.Fatalf("completed old-cache pause was compensated: %#v", currentLegacy.Annotations)
			}
		})
	}
}

func TestApply_DedicatedWaitRejectsRecreatedLivePVC(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	ready := makeDedicatedPatchCallback(t, base, fixture, false)
	replaced := false
	recording.afterPatch = func(ctx context.Context, object client.Object) error {
		if err := ready(ctx, object); err != nil {
			return err
		}
		if _, ok := object.(*aimv1alpha2.AIMProfileCache); !ok || replaced {
			return nil
		}
		replaced = true
		current := &corev1.PersistentVolumeClaim{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.claim), current); err != nil {
			return err
		}
		if err := base.Delete(ctx, current); err != nil {
			return err
		}
		replacement := fixture.claim.DeepCopy()
		replacement.UID = "replacement-pvc-uid"
		replacement.ResourceVersion = ""
		return base.Create(ctx, replacement)
	}
	planPath := writeApplyPlan(t, fixture.document)
	_, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "live PVC") {
		t.Fatalf("error = %v", err)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); getErr != nil {
		t.Fatal(getErr)
	}
	if len(currentArtifact.OwnerReferences) != 2 {
		t.Fatalf("PVC identity failure crossed transfer boundary: %#v", currentArtifact.OwnerReferences)
	}
}

func TestApply_DedicatedFinalDriftFenceKeepsServiceOnTemplatePipeline(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	ready := makeDedicatedPatchCallback(t, base, fixture, false)
	recording.afterPatch = func(ctx context.Context, object client.Object) error {
		if err := ready(ctx, object); err != nil {
			return err
		}
		artifact, ok := object.(*aimv1alpha1.AIMArtifact)
		if !ok ||
			len(artifact.OwnerReferences) != 1 ||
			artifact.OwnerReferences[0].Kind != resourceKindProfileCache {
			return nil
		}
		oldCache := &aimv1alpha1.AIMTemplateCache{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.legacyCache), oldCache); err != nil {
			return err
		}
		delete(oldCache.Annotations, constants.AnnotationReconciliationPaused)
		return base.Update(ctx, oldCache)
	}
	planPath := writeApplyPlan(t, fixture.document)
	_, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "pause did not preserve") {
		t.Fatalf("error = %v", err)
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), currentService); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("final drift fence cut over service: %#v", currentService.Spec)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	last := result.Operations[len(result.Operations)-1]
	if last.Action != actionVerifyDedicatedHandoff || last.Outcome != operationOutcomeFailed {
		t.Fatalf("final-drift diagnostics = %#v", last)
	}
}

func TestApply_DedicatedWrongAdoptionStopsWithDualOwners(t *testing.T) {
	fixture := newDedicatedApplyFixture(t, nil)
	base := newApplyFakeClient(t, fixture.objects...)
	recording := &recordingApplyClient{Client: base}
	recording.afterCreate = makeDedicatedCreateCallback(t, base, fixture)
	recording.afterPatch = makeDedicatedPatchCallback(t, base, fixture, true)
	planPath := writeApplyPlan(t, fixture.document)
	_, err := applyDedicatedForTest(context.Background(), recording, ApplyOptions{
		PlanPath:     planPath,
		Commit:       true,
		WaitTimeout:  100 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "exact predecessor artifact UID and PVC") {
		t.Fatalf("error = %v", err)
	}
	currentArtifact := &aimv1alpha1.AIMArtifact{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.artifact), currentArtifact); getErr != nil {
		t.Fatal(getErr)
	}
	if len(currentArtifact.OwnerReferences) != 2 ||
		currentArtifact.OwnerReferences[0].UID != fixture.legacyCache.UID {
		t.Fatalf("wrong adoption did not stop at dual ownership: %#v", currentArtifact.OwnerReferences)
	}
	currentService := &aimv1alpha2.AIMService{}
	if getErr := base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), currentService); getErr != nil {
		t.Fatal(getErr)
	}
	if currentService.Spec.Profile != nil || currentService.Spec.Template == nil {
		t.Fatalf("wrong adoption cut over service: %#v", currentService.Spec)
	}
	result := readApplyResult(t, filepath.Join(filepath.Dir(planPath), applyResultFilename))
	last := result.Operations[len(result.Operations)-1]
	if last.Action != actionVerifyDedicatedCache ||
		last.Outcome != operationOutcomeFailed ||
		!resultObservedUID(result, string(fixture.artifact.UID)) ||
		!resultObservedUID(result, string(fixture.claim.UID)) {
		t.Fatalf("wrong-adoption diagnostics = %#v", result)
	}
}

type applyFixture struct {
	document         PlanDocument
	objects          []client.Object
	service          *aimv1alpha2.AIMService
	template         *aimv1alpha1.AIMServiceTemplate
	legacyCache      *aimv1alpha1.AIMTemplateCache
	artifact         *aimv1alpha1.AIMArtifact
	claim            *corev1.PersistentVolumeClaim
	profile          *aimv1alpha2.AIMProfile
	cache            *aimv1alpha2.AIMProfileCache
	inferenceService *servingv1beta1.InferenceService
}

func newApplyFixture(t *testing.T, edit func(*planFixture)) *applyFixture {
	return newApplyFixtureWithSharedArtifact(t, edit, false)
}

func newSharedApplyFixture(t *testing.T, edit func(*planFixture)) *applyFixture {
	return newApplyFixtureWithSharedArtifact(t, edit, true)
}

func newDedicatedApplyFixture(t *testing.T, edit func(*planFixture)) *applyFixture {
	return newNamedDedicatedApplyFixture(t, "apply", edit)
}

func newNamedDedicatedApplyFixture(
	t *testing.T,
	name string,
	edit func(*planFixture),
) *applyFixture {
	t.Helper()
	return newNamedApplyFixture(t, name, func(source *planFixture) {
		source.service.Spec.Caching.Mode = aimv1alpha1.CachingModeDedicated
		source.cache.Spec.Mode = aimv1alpha1.TemplateCacheModeDedicated
		source.cache.OwnerReferences = []metav1.OwnerReference{{
			APIVersion:         aimv1alpha2.GroupVersion.String(),
			Kind:               "AIMService",
			Name:               source.service.Name,
			UID:                source.service.UID,
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(true),
		}}
		source.artifact.Status.Mode = aimv1alpha1.ArtifactModeDedicated
		source.artifact.OwnerReferences = []metav1.OwnerReference{{
			APIVersion:         aimv1alpha1.GroupVersion.String(),
			Kind:               resourceKindTemplateCache,
			Name:               source.cache.Name,
			UID:                source.cache.UID,
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(true),
		}}
		source.claim.OwnerReferences[0].Controller = ptr.To(true)
		source.claim.OwnerReferences[0].BlockOwnerDeletion = ptr.To(true)
		if edit != nil {
			edit(source)
		}
	}, true)
}

func newApplyFixtureWithSharedArtifact(
	t *testing.T,
	edit func(*planFixture),
	preserveSharedArtifact bool,
) *applyFixture {
	return newNamedApplyFixture(t, "apply", edit, preserveSharedArtifact)
}

func newNamedApplyFixture(
	t *testing.T,
	name string,
	edit func(*planFixture),
	preserveSharedArtifact bool,
) *applyFixture {
	t.Helper()
	source := newPlanFixture(name)
	if edit != nil {
		edit(source)
	}
	planned := runPlanWithOptions(t, PlanOptions{
		Namespaces:            []string{testNamespace},
		RuntimeProjectionMode: aimv1alpha2.RuntimeProjectionModeBoth,
		allowDedicatedApplyTests: source.service.Spec.Caching != nil &&
			source.service.Spec.Caching.Mode == aimv1alpha1.CachingModeDedicated,
	}, source.objects()...)
	unit := planned.Document.Units[0]
	if unit.Outcome != OutcomeAutomatic {
		t.Fatalf("source plan is not automatic: %#v", unit.ManualOutcomes)
	}

	if !preserveSharedArtifact {
		source.cache.Status.Artifacts = map[string]aimv1alpha1.AIMResolvedArtifact{}
		filtered := unit.ObservedResources[:0]
		for _, identity := range unit.ObservedResources {
			if identity.Kind != resourceKindArtifact && identity.Kind != resourceKindPVC {
				filtered = append(filtered, identity)
			}
		}
		unit.ObservedResources = filtered
		if len(unit.ProposedOperations) == 4 &&
			unit.ProposedOperations[2].Resource.Role == roleSourceArtifactProtection {
			unit.ProposedOperations = append(
				unit.ProposedOperations[:2],
				unit.ProposedOperations[3],
			)
		}
	}

	service := &aimv1alpha2.AIMService{}
	copyThroughJSON(t, source.service, service)
	service.TypeMeta = metav1.TypeMeta{
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       "AIMService",
	}
	inferenceServiceName, err := v1alpha1aimservice.GenerateInferenceServiceName(
		service.Name,
		service.Namespace,
	)
	if err != nil {
		t.Fatal(err)
	}
	inferenceService := &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       "InferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            inferenceServiceName,
			Namespace:       service.Namespace,
			UID:             types.UID("isvc-uid"),
			ResourceVersion: "31",
			Generation:      1,
			Labels: map[string]string{
				constants.LabelTemplate: "legacy-template",
			},
		},
		Status: servingv1beta1.InferenceServiceStatus{
			Status: duckv1.Status{
				ObservedGeneration: 1,
				Conditions: duckv1.Conditions{{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				}},
			},
		},
	}
	profile := &aimv1alpha2.AIMProfile{}
	copyMapToObject(t, unit.ProposedOperations[0].Object, profile)
	cache := &aimv1alpha2.AIMProfileCache{}
	copyMapToObject(t, unit.ProposedOperations[1].Object, cache)

	objects := []client.Object{
		source.service,
		service,
		source.template,
		source.model,
		source.cache,
		inferenceService,
	}
	if preserveSharedArtifact {
		objects = append(objects, source.artifact, source.claim)
	}
	return &applyFixture{
		document: PlanDocument{
			DocumentVersion:       DocumentVersion,
			RuntimeProjectionMode: planned.Document.RuntimeProjectionMode,
			SelectedNamespaces:    []string{testNamespace},
			Units:                 []MigrationUnit{unit},
		},
		objects:          objects,
		service:          service,
		template:         source.template,
		legacyCache:      source.cache,
		artifact:         source.artifact,
		claim:            source.claim,
		profile:          profile,
		cache:            cache,
		inferenceService: inferenceService,
	}
}

func (fixture *applyFixture) makeReadyReusableTargets(
	t *testing.T,
) (*aimv1alpha2.AIMProfile, *aimv1alpha2.AIMProfileCache, *kservev1alpha1.ServingRuntime, *corev1.ConfigMap) {
	t.Helper()
	profile := fixture.profile.DeepCopy()
	profile.UID = types.UID("profile-uid")
	profile.ResourceVersion = "41"
	profile.Generation = 1
	runtimeName := serving.RuntimeName(profile.Name)
	profile.Status = aimv1alpha2.AIMProfileStatus{
		ObservedGeneration:   1,
		Status:               constants.AIMStatusReady,
		Deployable:           true,
		ProjectedRuntimeName: runtimeName,
		Conditions: []metav1.Condition{{
			Type:   aimv1alpha2.AIMProfileConditionRuntimeProjected,
			Status: metav1.ConditionTrue,
			Reason: aimv1alpha2.AIMProfileReasonRuntimeProjected,
		}},
	}
	identity := resourceIdentityForTest(
		t,
		profile,
		aimv1alpha2.GroupVersion.String(),
		resourceKindProfile,
		"target-profile",
	)
	unit := &fixture.document.Units[0]
	unit.TargetProfile = &identity
	unit.ProposedOperations[0] = ProposedOperation{
		Action:   OperationReuse,
		Resource: identity,
	}
	unit.ObservedResources = append(unit.ObservedResources, identity)

	cache := fixture.cache.DeepCopy()
	cache.UID = types.UID("profile-cache-uid")
	cache.ResourceVersion = "42"
	cache.Generation = 1
	cache.Status = aimv1alpha2.AIMProfileCacheStatus{
		ObservedGeneration: 1,
		Status:             constants.AIMStatusReady,
	}
	runtimeObject := &kservev1alpha1.ServingRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha1.SchemeGroupVersion.String(),
			Kind:       "ServingRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            runtimeName,
			Namespace:       testNamespace,
			UID:             types.UID("runtime-uid"),
			ResourceVersion: "43",
		},
	}
	runtimeConfigMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            runtimeName,
			Namespace:       testNamespace,
			UID:             types.UID("runtime-config-uid"),
			ResourceVersion: "44",
		},
	}
	return profile, cache, runtimeObject, runtimeConfigMap
}

func readyServiceAfterPatch(
	t *testing.T,
	base client.Client,
	fixture *applyFixture,
	profileUID types.UID,
) func(context.Context, client.Object) error {
	t.Helper()
	return func(ctx context.Context, object client.Object) error {
		if _, ok := object.(*aimv1alpha2.AIMService); !ok {
			return nil
		}
		current := &aimv1alpha2.AIMService{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.service), current); err != nil {
			return err
		}
		current.Generation = fixture.service.Generation + 1
		if err := base.Update(ctx, current); err != nil {
			return err
		}
		current.Status.ObservedGeneration = current.Generation
		current.Status.Status = constants.AIMStatusRunning
		current.Status.ReconcilerPipeline = constants.ReconcilerPipelineProfile
		current.Status.ResolvedProfile = &aimv1alpha1.AIMResolvedReference{
			Name:      fixture.profile.Name,
			Namespace: fixture.profile.Namespace,
			Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
			Kind:      resourceKindProfile,
			UID:       profileUID,
		}
		cache := &aimv1alpha2.AIMProfileCache{}
		if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), cache); err != nil {
			return err
		}
		current.Status.Cache = &aimv1alpha1.AIMServiceCacheStatus{
			ProfileCacheRef: &aimv1alpha1.AIMResolvedReference{
				Name:      cache.Name,
				Namespace: cache.Namespace,
				UID:       cache.UID,
			},
		}
		return base.Status().Update(ctx, current)
	}
}

func makeSharedTargetsReady(
	t *testing.T,
	base client.Client,
	fixture *applyFixture,
	wrongAdoption bool,
) func(context.Context, client.Object) error {
	t.Helper()
	return func(ctx context.Context, object client.Object) error {
		switch object.(type) {
		case *aimv1alpha2.AIMProfile:
			current := &aimv1alpha2.AIMProfile{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.profile), current); err != nil {
				return err
			}
			current.UID = types.UID("created-profile-uid")
			current.Generation = 1
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			runtimeName := serving.RuntimeName(current.Name)
			current.Status = aimv1alpha2.AIMProfileStatus{
				ObservedGeneration:   current.Generation,
				Status:               constants.AIMStatusReady,
				Deployable:           true,
				ProjectedRuntimeName: runtimeName,
				Conditions: []metav1.Condition{{
					Type:   aimv1alpha2.AIMProfileConditionRuntimeProjected,
					Status: metav1.ConditionTrue,
					Reason: aimv1alpha2.AIMProfileReasonRuntimeProjected,
				}},
			}
			if err := base.Status().Update(ctx, current); err != nil {
				return err
			}
			if err := base.Create(ctx, &kservev1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			}); err != nil {
				return err
			}
			return base.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			})
		case *aimv1alpha2.AIMProfileCache:
			current := &aimv1alpha2.AIMProfileCache{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), current); err != nil {
				return err
			}
			current.UID = types.UID("created-cache-uid")
			current.Generation = 1
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			artifactUID := string(fixture.artifact.UID)
			artifactName := fixture.artifact.Name
			claimName := fixture.claim.Name
			if wrongAdoption {
				artifactUID = "wrong-artifact-uid"
				artifactName = "wrong-artifact"
				claimName = "wrong-pvc"
			}
			current.Status = aimv1alpha2.AIMProfileCacheStatus{
				ObservedGeneration: current.Generation,
				Status:             constants.AIMStatusReady,
				Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
					fixture.artifact.Spec.ModelID: {
						UID:                   artifactUID,
						Name:                  artifactName,
						Model:                 fixture.artifact.Spec.ModelID,
						Status:                constants.AIMStatusReady,
						PersistentVolumeClaim: claimName,
					},
				},
			}
			return base.Status().Update(ctx, current)
		default:
			return nil
		}
	}
}

func makeDedicatedCreateCallback(
	t *testing.T,
	base client.Client,
	fixture *applyFixture,
) func(context.Context, client.Object) error {
	t.Helper()
	return func(ctx context.Context, object client.Object) error {
		switch object.(type) {
		case *aimv1alpha2.AIMProfile:
			current := &aimv1alpha2.AIMProfile{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.profile), current); err != nil {
				return err
			}
			current.UID = types.UID("created-profile-uid")
			current.Generation = 1
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			runtimeName := serving.RuntimeName(current.Name)
			current.Status = aimv1alpha2.AIMProfileStatus{
				ObservedGeneration:   current.Generation,
				Status:               constants.AIMStatusReady,
				Deployable:           true,
				ProjectedRuntimeName: runtimeName,
				Conditions: []metav1.Condition{{
					Type:   aimv1alpha2.AIMProfileConditionRuntimeProjected,
					Status: metav1.ConditionTrue,
					Reason: aimv1alpha2.AIMProfileReasonRuntimeProjected,
				}},
			}
			if err := base.Status().Update(ctx, current); err != nil {
				return err
			}
			if err := base.Create(ctx, &kservev1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			}); err != nil {
				return err
			}
			return base.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: runtimeName, Namespace: current.Namespace},
			})
		case *aimv1alpha2.AIMProfileCache:
			current := &aimv1alpha2.AIMProfileCache{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), current); err != nil {
				return err
			}
			current.UID = types.UID("created-dedicated-cache-uid")
			current.Generation = 1
			return base.Update(ctx, current)
		default:
			return nil
		}
	}
}

func makeDedicatedPatchCallback(
	t *testing.T,
	base client.Client,
	fixture *applyFixture,
	wrongAdoption bool,
) func(context.Context, client.Object) error {
	t.Helper()
	serviceReady := readyServiceAfterPatch(t, base, fixture, types.UID("created-profile-uid"))
	return func(ctx context.Context, object client.Object) error {
		switch typed := object.(type) {
		case *aimv1alpha2.AIMProfileCache:
			if typed.Annotations[constants.AnnotationReconciliationPaused] == reconciliationPausedValue {
				return nil
			}
			artifact := &aimv1alpha1.AIMArtifact{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.artifact), artifact); err != nil {
				return err
			}
			if len(artifact.OwnerReferences) != 2 {
				return errors.New("target cache was unpaused before dual ownership persisted")
			}
			current := &aimv1alpha2.AIMProfileCache{}
			if err := base.Get(ctx, client.ObjectKeyFromObject(fixture.cache), current); err != nil {
				return err
			}
			current.Finalizers = []string{"aim.eai.amd.com/profile-cache-artifact-cleanup"}
			if err := base.Update(ctx, current); err != nil {
				return err
			}
			artifactUID := string(fixture.artifact.UID)
			artifactName := fixture.artifact.Name
			claimName := fixture.claim.Name
			if wrongAdoption {
				artifactUID = "wrong-artifact-uid"
				artifactName = "wrong-artifact"
				claimName = "wrong-pvc"
			}
			current.Status = aimv1alpha2.AIMProfileCacheStatus{
				ObservedGeneration: current.Generation,
				Status:             constants.AIMStatusReady,
				Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
					artifactName: {
						UID:                   artifactUID,
						Name:                  artifactName,
						Model:                 fixture.artifact.Spec.ModelID,
						Status:                constants.AIMStatusReady,
						PersistentVolumeClaim: claimName,
					},
				},
			}
			return base.Status().Update(ctx, current)
		case *aimv1alpha2.AIMService:
			return serviceReady(ctx, object)
		default:
			return nil
		}
	}
}

func refreshFixturePin(
	t *testing.T,
	fixture *applyFixture,
	object client.Object,
	role string,
) {
	t.Helper()
	unit := &fixture.document.Units[0]
	for index := range unit.ObservedResources {
		identity := unit.ObservedResources[index]
		if identity.Role != role {
			continue
		}
		unit.ObservedResources[index] = resourceIdentityForTest(
			t,
			object,
			identity.APIVersion,
			identity.Kind,
			identity.Role,
		)
	}
	for index := range unit.ProposedOperations {
		operation := &unit.ProposedOperations[index]
		if operation.Resource.Role != roleSourceArtifactProtection ||
			role != "source-artifact" {
			continue
		}
		updated := resourceIdentityForTest(
			t,
			object,
			operation.Resource.APIVersion,
			operation.Resource.Kind,
			operation.Resource.Role,
		)
		operation.Resource = updated
	}
}

func resultObservedUID(result ApplyResultDocument, uid string) bool {
	for _, identity := range result.ObservedIdentities {
		if identity.UID == uid {
			return true
		}
	}
	return false
}

func newApplyFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := testScheme(t)
	for _, add := range []func(*runtime.Scheme) error{
		kservev1alpha1.AddToScheme,
		servingv1beta1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(
			&aimv1alpha2.AIMService{},
			&aimv1alpha2.AIMProfile{},
			&aimv1alpha2.AIMProfileCache{},
			&servingv1beta1.InferenceService{},
		).
		WithObjects(objects...).
		Build()
}

func writeApplyPlan(t *testing.T, document PlanDocument) string {
	t.Helper()
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), planFilename)
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readApplyResult(t *testing.T, path string) ApplyResultDocument {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result ApplyResultDocument
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func resourceIdentityForTest(
	t *testing.T,
	object client.Object,
	apiVersion, kind, role string,
) ResourceIdentity {
	t.Helper()
	digest, secretLiteral, err := contentDigest(object, apiVersion, kind)
	if err != nil {
		t.Fatal(err)
	}
	if secretLiteral {
		t.Fatalf("%s contains a secret literal", kind)
	}
	return ResourceIdentity{
		Role:            role,
		APIVersion:      apiVersion,
		Kind:            kind,
		Namespace:       object.GetNamespace(),
		Name:            object.GetName(),
		UID:             string(object.GetUID()),
		ResourceVersion: object.GetResourceVersion(),
		Generation:      object.GetGeneration(),
		ContentDigest:   digest,
	}
}

func copyThroughJSON(t *testing.T, source, target any) {
	t.Helper()
	contents, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, target); err != nil {
		t.Fatal(err)
	}
}

func copyMapToObject(t *testing.T, source map[string]any, target client.Object) {
	t.Helper()
	contents, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, target); err != nil {
		t.Fatal(err)
	}
}

func assertNotFound(t *testing.T, kubernetesClient client.Client, key client.ObjectKey, object client.Object) {
	t.Helper()
	if err := kubernetesClient.Get(context.Background(), key, object); !apierrors.IsNotFound(err) {
		t.Fatalf("Get(%s) error = %v, want NotFound", key, err)
	}
}

func resourceMustParse(t *testing.T, value string) resource.Quantity {
	t.Helper()
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		t.Fatal(err)
	}
	return quantity
}

type recordedApplyWrite struct {
	verb            string
	kind            string
	name            string
	dryRun          bool
	ownerUID        string
	ownerReferences []metav1.OwnerReference
	paused          bool
}

type recordingApplyClient struct {
	client.Client
	creates     []recordedApplyWrite
	patches     []recordedApplyWrite
	writes      []recordedApplyWrite
	deletes     int
	createError error
	beforeWrite func(client.Object)
	afterCreate func(context.Context, client.Object) error
	afterPatch  func(context.Context, client.Object) error
	failWriteAt int
	writeCount  int
}

type cancelAfterProtectedInferenceServiceClient struct {
	client.Client
	cancel context.CancelFunc
}

func (cancelling *cancelAfterProtectedInferenceServiceClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	options ...client.GetOption,
) error {
	if err := cancelling.Client.Get(ctx, key, object, options...); err != nil {
		return err
	}
	if _, ok := object.(*servingv1beta1.InferenceService); !ok {
		return nil
	}
	artifacts := &aimv1alpha1.AIMArtifactList{}
	if err := cancelling.List(
		context.Background(),
		artifacts,
		client.InNamespace(key.Namespace),
	); err != nil {
		return err
	}
	for index := range artifacts.Items {
		if artifacts.Items[index].Annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] ==
			aimv1alpha1.ArtifactEvictionProtectedValue {
			cancelling.cancel()
			break
		}
	}
	return nil
}

func (recording *recordingApplyClient) Create(
	ctx context.Context,
	object client.Object,
	options ...client.CreateOption,
) error {
	if recording.beforeWrite != nil {
		recording.beforeWrite(object)
	}
	applied := (&client.CreateOptions{}).ApplyOptions(options)
	dryRun := slices.Contains(applied.DryRun, metav1.DryRunAll)
	ownerUID := ""
	if owner := metav1.GetControllerOf(object); owner != nil {
		ownerUID = string(owner.UID)
	}
	write := recordedApplyWrite{
		verb:            "create",
		kind:            recordedObjectKind(object),
		name:            object.GetName(),
		dryRun:          dryRun,
		ownerUID:        ownerUID,
		ownerReferences: append([]metav1.OwnerReference(nil), object.GetOwnerReferences()...),
		paused:          object.GetAnnotations()[constants.AnnotationReconciliationPaused] == reconciliationPausedValue,
	}
	recording.creates = append(recording.creates, write)
	recording.writes = append(recording.writes, write)
	recording.writeCount++
	if recording.failWriteAt == recording.writeCount {
		return errors.New("injected write failure")
	}
	if recording.createError != nil {
		return recording.createError
	}
	if dryRun {
		return nil
	}
	if err := recording.Client.Create(ctx, object, options...); err != nil {
		return err
	}
	if recording.afterCreate != nil {
		return recording.afterCreate(ctx, object)
	}
	return nil
}

func (recording *recordingApplyClient) Patch(
	ctx context.Context,
	object client.Object,
	patch client.Patch,
	options ...client.PatchOption,
) error {
	if recording.beforeWrite != nil {
		recording.beforeWrite(object)
	}
	applied := (&client.PatchOptions{}).ApplyOptions(options)
	dryRun := slices.Contains(applied.DryRun, metav1.DryRunAll)
	write := recordedApplyWrite{
		verb:            "patch",
		kind:            recordedObjectKind(object),
		name:            object.GetName(),
		dryRun:          dryRun,
		ownerReferences: append([]metav1.OwnerReference(nil), object.GetOwnerReferences()...),
		paused:          object.GetAnnotations()[constants.AnnotationReconciliationPaused] == reconciliationPausedValue,
	}
	recording.patches = append(recording.patches, write)
	recording.writes = append(recording.writes, write)
	recording.writeCount++
	if recording.failWriteAt == recording.writeCount {
		return errors.New("injected write failure")
	}
	if dryRun {
		return nil
	}
	if err := recording.Client.Patch(ctx, object, patch, options...); err != nil {
		return err
	}
	if recording.afterPatch != nil {
		return recording.afterPatch(ctx, object)
	}
	return nil
}

func (recording *recordingApplyClient) Delete(
	ctx context.Context,
	object client.Object,
	options ...client.DeleteOption,
) error {
	recording.deletes++
	return errors.New("Delete must not be called")
}

func recordedObjectKind(object client.Object) string {
	if kind := object.GetObjectKind().GroupVersionKind().Kind; kind != "" {
		return kind
	}
	switch object.(type) {
	case *aimv1alpha1.AIMTemplateCache:
		return resourceKindTemplateCache
	case *aimv1alpha1.AIMArtifact:
		return resourceKindArtifact
	case *aimv1alpha2.AIMProfileCache:
		return resourceKindProfileCache
	case *aimv1alpha2.AIMProfile:
		return resourceKindProfile
	case *aimv1alpha2.AIMService:
		return "AIMService"
	default:
		return ""
	}
}
