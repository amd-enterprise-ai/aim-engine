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

package controllerutils

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const testLabelValueAlpha = "alpha"

func TestApplyControllerLabelsToResult(t *testing.T) {
	tests := []struct {
		name           string
		planResult     *PlanResult
		labels         map[string]string
		expectedLabels map[string]string
		checkJobPods   bool
	}{
		{
			name: "adds labels to create-only resources",
			planResult: &PlanResult{
				toCreate: []client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-create-only-configmap",
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
		},
		{
			name: "adds labels to regular resources",
			planResult: &PlanResult{
				toApply: []client.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-service",
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
				"aim.eai.amd.com/test.name":    "test-obj",
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
				"aim.eai.amd.com/test.name":    "test-obj",
			},
		},
		{
			name: "adds labels to Jobs but excludes managed-by from pods",
			planResult: &PlanResult{
				toApply: []client.Object{
					&batchv1.Job{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-job",
						},
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
				"aim.eai.amd.com/test.name":    "test-obj",
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
				"aim.eai.amd.com/test.name":    "test-obj",
			},
			checkJobPods: true,
		},
		{
			name: "preserves existing labels",
			planResult: &PlanResult{
				toApply: []client.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-service",
							Labels: map[string]string{
								"existing-label": "existing-value",
							},
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
			expectedLabels: map[string]string{
				"existing-label":               "existing-value",
				"app.kubernetes.io/managed-by": "test-controller",
			},
		},
		{
			name: "does not overwrite existing labels",
			planResult: &PlanResult{
				toApply: []client.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-service",
							Labels: map[string]string{
								"app.kubernetes.io/managed-by": "existing-controller",
							},
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "existing-controller",
			},
		},
		{
			name: "adds labels to toApplyWithoutOwnerRef",
			planResult: &PlanResult{
				toApplyWithoutOwnerRef: []client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-configmap",
						},
					},
				},
			},
			labels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "test-controller",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ApplyControllerLabelsToResult(tt.planResult, tt.labels)

			// Check labels on toCreate resources
			for _, obj := range tt.planResult.toCreate {
				objLabels := obj.GetLabels()
				for key, expectedValue := range tt.expectedLabels {
					if gotValue, exists := objLabels[key]; !exists {
						t.Errorf("Expected label %s to exist, but it doesn't", key)
					} else if gotValue != expectedValue {
						t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, gotValue)
					}
				}
			}

			// Check labels on toApply resources
			for _, obj := range tt.planResult.toApply {
				objLabels := obj.GetLabels()
				for key, expectedValue := range tt.expectedLabels {
					if gotValue, exists := objLabels[key]; !exists {
						t.Errorf("Expected label %s to exist, but it doesn't", key)
					} else if gotValue != expectedValue {
						t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, gotValue)
					}
				}

				// Check Job pod template labels if requested
				if tt.checkJobPods {
					if job, ok := obj.(*batchv1.Job); ok {
						podLabels := job.Spec.Template.Labels
						for key, expectedValue := range tt.expectedLabels {
							// app.kubernetes.io/managed-by should NOT be on pod templates
							if key == "app.kubernetes.io/managed-by" {
								if _, exists := podLabels[key]; exists {
									t.Errorf("Pod template should NOT have %s label (pods are managed by Job controller)", key)
								}
								continue
							}
							// Other labels should be present
							if gotValue, exists := podLabels[key]; !exists {
								t.Errorf("Expected pod template label %s to exist, but it doesn't", key)
							} else if gotValue != expectedValue {
								t.Errorf("Expected pod template label %s=%s, got %s=%s", key, expectedValue, key, gotValue)
							}
						}
					}
				}
			}

			// Check labels on toApplyWithoutOwnerRef resources
			for _, obj := range tt.planResult.toApplyWithoutOwnerRef {
				objLabels := obj.GetLabels()
				for key, expectedValue := range tt.expectedLabels {
					if gotValue, exists := objLabels[key]; !exists {
						t.Errorf("Expected label %s to exist, but it doesn't", key)
					} else if gotValue != expectedValue {
						t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, gotValue)
					}
				}
			}
		})
	}
}

// TestApplyControllerLabelsToResult_ForceBucket verifies the force-apply bucket
// receives the same controller labels as the regular owned bucket, so an object
// queued via ApplyWithForce is not left without managed-by/correlator labels.
func TestApplyControllerLabelsToResult_ForceBucket(t *testing.T) {
	planResult := &PlanResult{}
	planResult.ApplyWithForce(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "forced-service"},
	})

	labels := map[string]string{"app.kubernetes.io/managed-by": "test-controller"}
	ApplyControllerLabelsToResult(planResult, labels)

	got := planResult.GetToApplyWithForce()
	if len(got) != 1 {
		t.Fatalf("force bucket length = %d, want 1", len(got))
	}
	if v := got[0].GetLabels()["app.kubernetes.io/managed-by"]; v != "test-controller" {
		t.Errorf("force-bucket object missing controller label, got %q", v)
	}
}

func TestRemoveExcludedLabelsFromResult(t *testing.T) {
	const excludedLabel = "aim.eai.amd.com/service.name"
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "shared",
			Labels: map[string]string{"existing": "preserved"},
		},
	}
	parent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "parent",
			Labels: map[string]string{excludedLabel: "service-a"},
		},
	}
	planResult := &PlanResult{}
	planResult.ApplyWithoutOwnerRef(obj)
	planResult.ExcludeLabels(obj, excludedLabel)

	PropagateLabelsForResult(parent, planResult, nil)
	ApplyControllerLabelsToResult(planResult, map[string]string{
		"app.kubernetes.io/managed-by": "test-controller",
		excludedLabel:                  "service-a",
	})
	RemoveExcludedLabelsFromResult(planResult)

	if _, exists := obj.Labels[excludedLabel]; exists {
		t.Fatalf("excluded label %q remains on object", excludedLabel)
	}
	if got := obj.Labels["existing"]; got != "preserved" {
		t.Errorf("existing label = %q, want preserved", got)
	}
	if got := obj.Labels["app.kubernetes.io/managed-by"]; got != "test-controller" {
		t.Errorf("non-excluded controller label = %q, want test-controller", got)
	}
}

func TestSkipLabelPropagationForResult(t *testing.T) {
	parent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "parent",
			Labels: map[string]string{
				"team":                            testLabelValueAlpha,
				constants.AimLabelDomain + "/env": "service-owned",
			},
		},
	}
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "shared",
			Labels: map[string]string{constants.LabelProfile: "profile-a"},
		},
	}
	planResult := &PlanResult{}
	planResult.ApplyWithoutOwnerRef(obj)
	planResult.SkipLabelPropagation(obj)

	PropagateLabelsForResult(
		parent,
		planResult,
		&LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
	)
	ApplyControllerLabelsToResult(planResult, map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
	})

	if _, exists := obj.Labels["team"]; exists {
		t.Error("shared object inherited configured parent label")
	}
	if _, exists := obj.Labels[constants.AimLabelDomain+"/env"]; exists {
		t.Error("shared object inherited AIM parent label")
	}
	if got := obj.Labels[constants.LabelProfile]; got != "profile-a" {
		t.Errorf("profile label = %q, want profile-a", got)
	}
	if got := obj.Labels[constants.LabelK8sManagedBy]; got != constants.LabelValueManagedBy {
		t.Errorf("managed-by label = %q, want %q", got, constants.LabelValueManagedBy)
	}
}

// TestPropagateLabelsForResult_ForceBucket verifies parent labels propagate to
// objects in the force-apply bucket, matching the other apply buckets.
func TestPropagateLabelsForResult_ForceBucket(t *testing.T) {
	parent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Labels: map[string]string{"team": testLabelValueAlpha}},
	}
	planResult := &PlanResult{}
	planResult.ApplyWithForce(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "forced"}})

	PropagateLabelsForResult(parent, planResult, &LabelPropagationSettings{Enabled: true, Match: []string{"team"}})

	if v := planResult.GetToApplyWithForce()[0].GetLabels()["team"]; v != testLabelValueAlpha {
		t.Errorf("force-bucket object missing propagated label, got %q", v)
	}
}

// TestPlanResult_MergeCarriesForceBucket guards that composite reconcilers that
// merge sub-plans do not silently drop force-apply objects.
func TestPlanResult_MergeCarriesForceBucket(t *testing.T) {
	base := PlanResult{}
	base.ApplyWithForce(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "a"}})

	other := PlanResult{}
	other.ApplyWithForce(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "b"}})

	base.Merge(other)

	if got := len(base.GetToApplyWithForce()); got != 2 {
		t.Fatalf("merged force bucket length = %d, want 2", got)
	}
}

func TestPlanResult_MergeCarriesCreateBucket(t *testing.T) {
	base := PlanResult{}
	base.Create(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "a"}})

	other := PlanResult{}
	other.Create(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b"}})

	base.Merge(other)

	if got := len(base.GetToCreate()); got != 2 {
		t.Fatalf("merged create bucket length = %d, want 2", got)
	}
}

func TestPlanResultRequestRequeueAfterKeepsSmallestNonZero(t *testing.T) {
	plan := PlanResult{}
	plan.RequestRequeueAfter(3 * time.Minute)
	plan.RequestRequeueAfter(0)
	plan.RequestRequeueAfter(5 * time.Second)
	plan.RequestRequeueAfter(time.Minute)

	if plan.RequeueAfter != 5*time.Second {
		t.Errorf("RequeueAfter=%s, want %s", plan.RequeueAfter, 5*time.Second)
	}
}

func TestApplyControllerLabels_EmptyLabels(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-service",
		},
	}

	// Should not panic or modify the object
	applyControllerLabels(svc, map[string]string{})

	if svc.GetLabels() != nil {
		t.Errorf("Expected labels to remain nil when applying empty labels")
	}
}

func TestMatchesAnyPattern(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		patterns []string
		want     bool
	}{
		{
			name:     "exact match",
			key:      "team-alpha",
			patterns: []string{"team-alpha"},
			want:     true,
		},
		{
			name:     "wildcard prefix match",
			key:      "team-alpha",
			patterns: []string{"team-*"},
			want:     true,
		},
		{
			name:     "wildcard suffix match",
			key:      "alpha-team",
			patterns: []string{"*-team"},
			want:     true,
		},
		{
			name:     "wildcard middle match",
			key:      "my-team-label",
			patterns: []string{"my-*-label"},
			want:     true,
		},
		{
			name:     "question mark wildcard",
			key:      "team-1",
			patterns: []string{"team-?"},
			want:     true,
		},
		{
			name:     "no match",
			key:      "other-label",
			patterns: []string{"team-*"},
			want:     false,
		},
		{
			name:     "empty patterns",
			key:      "any-label",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "multiple patterns first matches",
			key:      "team-alpha",
			patterns: []string{"team-*", "org-*"},
			want:     true,
		},
		{
			name:     "multiple patterns second matches",
			key:      "org-beta",
			patterns: []string{"team-*", "org-*"},
			want:     true,
		},
		{
			name:     "domain-style pattern",
			key:      "org.mycompany/cost-center",
			patterns: []string{"org.mycompany/*"},
			want:     true,
		},
		{
			name:     "invalid pattern is skipped",
			key:      "test-label",
			patterns: []string{"[invalid", "test-*"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesAnyPattern(tt.key, tt.patterns)
			if got != tt.want {
				t.Errorf("matchesAnyPattern(%q, %v) = %v, want %v", tt.key, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestPropagateLabels(t *testing.T) {
	tests := []struct {
		name           string
		parentLabels   map[string]string
		childLabels    map[string]string
		config         *LabelPropagationSettings
		expectedLabels map[string]string
	}{
		{
			name:           "nil config does nothing",
			parentLabels:   map[string]string{"team": testLabelValueAlpha},
			childLabels:    nil,
			config:         nil,
			expectedLabels: nil,
		},
		{
			name:           "disabled propagation does nothing",
			parentLabels:   map[string]string{"team": testLabelValueAlpha},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: false, Match: []string{"team"}},
			expectedLabels: nil,
		},
		{
			name:           "empty match patterns does nothing",
			parentLabels:   map[string]string{"team": testLabelValueAlpha},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{}},
			expectedLabels: nil,
		},
		{
			name:           "propagates matching labels",
			parentLabels:   map[string]string{"team": testLabelValueAlpha, "env": "prod"},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
			expectedLabels: map[string]string{"team": testLabelValueAlpha},
		},
		{
			name:           "propagates with wildcard patterns",
			parentLabels:   map[string]string{"team-alpha": "1", "team-beta": "2", "env": "prod"},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team-*"}},
			expectedLabels: map[string]string{"team-alpha": "1", "team-beta": "2"},
		},
		{
			name:           "preserves existing child labels",
			parentLabels:   map[string]string{"team": testLabelValueAlpha, "env": "prod"},
			childLabels:    map[string]string{"existing": "value"},
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
			expectedLabels: map[string]string{"existing": "value", "team": testLabelValueAlpha},
		},
		{
			name:           "does not overwrite existing child labels",
			parentLabels:   map[string]string{"team": testLabelValueAlpha},
			childLabels:    map[string]string{"team": "beta"},
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
			expectedLabels: map[string]string{"team": "beta"},
		},
		{
			name:           "multiple match patterns",
			parentLabels:   map[string]string{"team": testLabelValueAlpha, "org": "mycompany", "env": "prod"},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team", "org"}},
			expectedLabels: map[string]string{"team": testLabelValueAlpha, "org": "mycompany"},
		},
		{
			name:           "empty parent labels does nothing",
			parentLabels:   map[string]string{},
			childLabels:    nil,
			config:         &LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
			expectedLabels: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "parent",
					Labels: tt.parentLabels,
				},
			}
			child := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "child",
					Labels: tt.childLabels,
				},
			}

			PropagateLabels(parent, child, tt.config)

			gotLabels := child.GetLabels()
			if tt.expectedLabels == nil {
				if len(gotLabels) > 0 {
					t.Errorf("Expected no labels, got %v", gotLabels)
				}
				return
			}

			if len(gotLabels) != len(tt.expectedLabels) {
				t.Errorf("Expected %d labels, got %d: %v", len(tt.expectedLabels), len(gotLabels), gotLabels)
			}

			for key, expectedValue := range tt.expectedLabels {
				if gotValue, exists := gotLabels[key]; !exists {
					t.Errorf("Expected label %s to exist, but it doesn't", key)
				} else if gotValue != expectedValue {
					t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, gotValue)
				}
			}
		})
	}
}

func TestPropagateLabels_Job(t *testing.T) {
	parent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "parent",
			Labels: map[string]string{"team": testLabelValueAlpha, "env": "prod"},
		},
	}
	child := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-job",
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{},
			},
		},
	}

	config := &LabelPropagationSettings{Enabled: true, Match: []string{"team"}}

	PropagateLabels(parent, child, config)

	// Check job labels
	jobLabels := child.GetLabels()
	if jobLabels["team"] != testLabelValueAlpha {
		t.Errorf("Expected job label team=alpha, got %v", jobLabels)
	}

	// Check pod template labels
	podLabels := child.Spec.Template.Labels
	if podLabels["team"] != testLabelValueAlpha {
		t.Errorf("Expected pod template label team=alpha, got %v", podLabels)
	}
}

func TestPropagateLabelsFoResult(t *testing.T) {
	parent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "parent",
			Labels: map[string]string{"team": testLabelValueAlpha, "env": "prod"},
		},
	}

	config := &LabelPropagationSettings{Enabled: true, Match: []string{"team"}}

	planResult := &PlanResult{
		toCreate: []client.Object{
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "create-only",
				},
			},
		},
		toApply: []client.Object{
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "svc1",
				},
			},
		},
		toApplyWithoutOwnerRef: []client.Object{
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cm1",
				},
			},
		},
	}

	PropagateLabelsForResult(parent, planResult, config)

	// Check toCreate
	for _, obj := range planResult.toCreate {
		labels := obj.GetLabels()
		if labels["team"] != testLabelValueAlpha {
			t.Errorf("Expected toCreate object to have team=alpha, got %v", labels)
		}
	}

	// Check toApply
	for _, obj := range planResult.toApply {
		labels := obj.GetLabels()
		if labels["team"] != testLabelValueAlpha {
			t.Errorf("Expected toApply object to have team=alpha, got %v", labels)
		}
	}

	// Check toApplyWithoutOwnerRef
	for _, obj := range planResult.toApplyWithoutOwnerRef {
		labels := obj.GetLabels()
		if labels["team"] != testLabelValueAlpha {
			t.Errorf("Expected toApplyWithoutOwnerRef object to have team=alpha, got %v", labels)
		}
	}
}

func TestSetOwnerReferenceConvergesBridgeGVKWithoutChangingDeletionSemantics(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name               string
		kind               string
		wantVersion        string
		existing           bool
		controller         *bool
		blockOwnerDeletion *bool
	}{
		{name: "new bridge reference uses production version", kind: "AIMService", wantVersion: "v1alpha2"},
		{name: "service preserves true", kind: "AIMService", wantVersion: "v1alpha2", existing: true, controller: &trueValue, blockOwnerDeletion: &trueValue},
		{name: "model preserves nil", kind: "AIMModel", wantVersion: "v1alpha2", existing: true},
		{name: "cluster model preserves false", kind: "AIMClusterModel", wantVersion: "v1alpha2", existing: true, controller: &falseValue, blockOwnerDeletion: &falseValue},
		{name: "legacy-only owner remains v1alpha1", kind: "AIMTemplateCache", wantVersion: "v1alpha1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner := &unstructured.Unstructured{}
			owner.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "aim.eai.amd.com",
				Version: "v1alpha1",
				Kind:    tt.kind,
			})
			owner.SetName("owner")
			owner.SetUID(types.UID("owner-uid"))

			child := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "ConfigMap",
				},
			}
			if tt.existing {
				child.OwnerReferences = []metav1.OwnerReference{{
					APIVersion:         "aim.eai.amd.com/v1alpha1",
					Kind:               tt.kind,
					Name:               owner.GetName(),
					UID:                owner.GetUID(),
					Controller:         tt.controller,
					BlockOwnerDeletion: tt.blockOwnerDeletion,
				}}
			}

			if err := setOwnerReference(owner, child, nil); err != nil {
				t.Fatalf("set owner reference: %v", err)
			}

			refs := child.GetOwnerReferences()
			if len(refs) != 1 {
				t.Fatalf("owner references = %d, want 1", len(refs))
			}
			ref := refs[0]
			if ref.APIVersion != "aim.eai.amd.com/"+tt.wantVersion ||
				ref.Kind != tt.kind ||
				ref.Name != owner.GetName() ||
				ref.UID != owner.GetUID() {
				t.Errorf("owner reference = %#v, want converged %s identity", ref, tt.kind)
			}
			switch {
			case !tt.existing && (ref.Controller == nil || !*ref.Controller):
				t.Error("new owner reference must control garbage collection")
			case tt.existing && tt.controller == nil && ref.Controller != nil:
				t.Errorf("controller = %v, want nil", *ref.Controller)
			case tt.existing && tt.controller != nil &&
				(ref.Controller == nil || *ref.Controller != *tt.controller):
				t.Errorf("controller = %v, want %v", ref.Controller, *tt.controller)
			}
			switch {
			case !tt.existing &&
				(ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion):
				t.Error("new owner reference must block owner deletion")
			case tt.existing && tt.blockOwnerDeletion == nil && ref.BlockOwnerDeletion != nil:
				t.Errorf("blockOwnerDeletion = %v, want nil", *ref.BlockOwnerDeletion)
			case tt.existing && tt.blockOwnerDeletion != nil &&
				(ref.BlockOwnerDeletion == nil || *ref.BlockOwnerDeletion != *tt.blockOwnerDeletion):
				t.Errorf("blockOwnerDeletion = %v, want %v", ref.BlockOwnerDeletion, *tt.blockOwnerDeletion)
			}
		})
	}
}
