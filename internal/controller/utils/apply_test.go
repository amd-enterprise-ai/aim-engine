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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplyControllerLabelsToResult(t *testing.T) {
	tests := []struct {
		name           string
		planResult     *PlanResult
		labels         map[string]string
		expectedLabels map[string]string
		checkJobPods   bool
	}{
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
