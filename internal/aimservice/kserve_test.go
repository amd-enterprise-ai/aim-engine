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
	"strings"
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// GENERATE INFERENCE SERVICE NAME TESTS
// ============================================================================

func TestGenerateInferenceServiceName(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		namespace    string
		wantContains []string
	}{
		{
			name:         "simple service",
			serviceName:  "my-service",
			namespace:    "my-namespace",
			wantContains: []string{"my-service"},
		},
		{
			name:         "long service name is truncated",
			serviceName:  "very-long-service-name-that-might-exceed-kubernetes-limits",
			namespace:    "default",
			wantContains: []string{}, // Just verify it doesn't error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateInferenceServiceName(tt.serviceName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify k8s name constraints
			if len(result) > 63 {
				t.Errorf("name too long: %d chars", len(result))
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}
		})
	}
}

func TestGenerateInferenceServiceName_Deterministic(t *testing.T) {
	result1, _ := GenerateInferenceServiceName("svc", "ns")
	result2, _ := GenerateInferenceServiceName("svc", "ns")

	if result1 != result2 {
		t.Errorf("expected deterministic output, got %q and %q", result1, result2)
	}
}

// ============================================================================
// IS READY FOR INFERENCE SERVICE TESTS
// ============================================================================

func TestIsReadyForInferenceService(t *testing.T) {
	tests := []struct {
		name     string
		service  *aimv1alpha1.AIMService
		obs      ServiceObservation
		expected bool
	}{
		{
			name:     "not ready - no model",
			service:  NewService("svc").Build(),
			obs:      ServiceObservation{},
			expected: false,
		},
		{
			name:    "not ready - model not ready",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusPending).Build(),
						},
					},
				},
			},
			expected: false,
		},
		{
			name:    "caching mode Shared - not ready without cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeShared).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expected: false,
		},
		{
			name:    "caching mode Shared - ready with cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeShared).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "default mode Shared - ready with template cache",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "legacy Never maps to Dedicated - ready with template cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeNever).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Spec: aimv1alpha1.AIMTemplateCacheSpec{
								Mode: aimv1alpha1.TemplateCacheModeDedicated,
							},
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "cluster model ready with template cache",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						ClusterModel: controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]{
							Value: NewClusterModel("cm").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "ISVC exists - always ready (update path bypasses model/cache checks)",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							ObjectMeta: metav1.ObjectMeta{Name: "existing-isvc", Namespace: testNamespace},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "ISVC exists - ready even with unhealthy model (update path)",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							ObjectMeta: metav1.ObjectMeta{Name: "existing-isvc", Namespace: testNamespace},
						},
					},
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusFailed).Build(),
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isReadyForInferenceService(tt.service, tt.obs)

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// PRESERVE EXISTING STORAGE VOLUMES TESTS
// ============================================================================

func TestPreserveExistingStorageVolumes(t *testing.T) {
	dshmSize := resource.MustParse("1Gi")

	tests := []struct {
		name              string
		existingVolumes   []corev1.Volume
		existingMounts    []corev1.VolumeMount
		expectVolumeCount int
		expectMountCount  int
		expectVolumeNames []string
		expectMountNames  []string
	}{
		{
			name: "preserves cache PVC volumes from existing ISVC",
			existingVolumes: []corev1.Volume{
				{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &dshmSize}}},
				{Name: "cache-vol-1", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-1"}}},
				{Name: "cache-vol-2", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-2"}}},
			},
			existingMounts: []corev1.VolumeMount{
				{Name: "dshm", MountPath: "/dev/shm"},
				{Name: "cache-vol-1", MountPath: "/aim/cache/model-a"},
				{Name: "cache-vol-2", MountPath: "/aim/cache/model-b"},
			},
			expectVolumeCount: 3, // dshm (base) + 2 cache volumes
			expectMountCount:  3, // dshm (base) + 2 cache mounts
			expectVolumeNames: []string{"dshm", "cache-vol-1", "cache-vol-2"},
			expectMountNames:  []string{"dshm", "cache-vol-1", "cache-vol-2"},
		},
		{
			name: "no extra volumes on existing ISVC",
			existingVolumes: []corev1.Volume{
				{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &dshmSize}}},
			},
			existingMounts: []corev1.VolumeMount{
				{Name: "dshm", MountPath: "/dev/shm"},
			},
			expectVolumeCount: 1,
			expectMountCount:  1,
			expectVolumeNames: []string{"dshm"},
			expectMountNames:  []string{"dshm"},
		},
		{
			name:              "existing ISVC has no containers",
			existingVolumes:   nil,
			existingMounts:    nil,
			expectVolumeCount: 1, // Only base dshm
			expectMountCount:  1, // Only base dshm
			expectVolumeNames: []string{"dshm"},
			expectMountNames:  []string{"dshm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build new ISVC with just the base shared memory volume
			newISVC := &servingv1beta1.InferenceService{
				Spec: servingv1beta1.InferenceServiceSpec{
					Predictor: servingv1beta1.PredictorSpec{
						PodSpec: servingv1beta1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "kserve-container",
									VolumeMounts: []corev1.VolumeMount{
										{Name: "dshm", MountPath: "/dev/shm"},
									},
								},
							},
							Volumes: []corev1.Volume{
								{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &dshmSize}}},
							},
						},
					},
				},
			}

			// Build existing ISVC
			existingISVC := &servingv1beta1.InferenceService{
				Spec: servingv1beta1.InferenceServiceSpec{
					Predictor: servingv1beta1.PredictorSpec{
						PodSpec: servingv1beta1.PodSpec{
							Volumes: tt.existingVolumes,
						},
					},
				},
			}
			if tt.existingMounts != nil {
				existingISVC.Spec.Predictor.Containers = []corev1.Container{
					{Name: "kserve-container", VolumeMounts: tt.existingMounts},
				}
			}

			preserveExistingStorageVolumes(newISVC, existingISVC)

			if len(newISVC.Spec.Predictor.Volumes) != tt.expectVolumeCount {
				t.Errorf("expected %d volumes, got %d", tt.expectVolumeCount, len(newISVC.Spec.Predictor.Volumes))
			}
			if len(newISVC.Spec.Predictor.Containers[0].VolumeMounts) != tt.expectMountCount {
				t.Errorf("expected %d mounts, got %d", tt.expectMountCount, len(newISVC.Spec.Predictor.Containers[0].VolumeMounts))
			}

			// Verify expected volume names
			volNames := make(map[string]bool)
			for _, v := range newISVC.Spec.Predictor.Volumes {
				volNames[v.Name] = true
			}
			for _, name := range tt.expectVolumeNames {
				if !volNames[name] {
					t.Errorf("expected volume %q not found", name)
				}
			}

			// Verify expected mount names
			mountNames := make(map[string]bool)
			for _, vm := range newISVC.Spec.Predictor.Containers[0].VolumeMounts {
				mountNames[vm.Name] = true
			}
			for _, name := range tt.expectMountNames {
				if !mountNames[name] {
					t.Errorf("expected mount %q not found", name)
				}
			}
		})
	}
}

// ============================================================================
// PLAN INFERENCE SERVICE TESTS - MUTABLE FIELD PROPAGATION
// ============================================================================

func TestPlanInferenceService_UpdatesReplicasWhenISVCExists(t *testing.T) {
	ctx := testContext()

	tests := []struct {
		name        string
		minReplicas *int32
		maxReplicas *int32
		expectMin   int32
		expectMax   int32
	}{
		{
			name:        "updated min=2, max=5",
			minReplicas: ptr.To(int32(2)),
			maxReplicas: ptr.To(int32(5)),
			expectMin:   2,
			expectMax:   5,
		},
		{
			name:        "updated min=1, max=10",
			minReplicas: ptr.To(int32(1)),
			maxReplicas: ptr.To(int32(10)),
			expectMin:   1,
			expectMax:   10,
		},
		{
			name:        "only min set, max defaults to min",
			minReplicas: ptr.To(int32(3)),
			maxReplicas: nil,
			expectMin:   3,
			expectMax:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService("svc").WithModelImage("test-image:v1").Build()
			service.Spec.MinReplicas = tt.minReplicas
			service.Spec.MaxReplicas = tt.maxReplicas

			templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: testModelName,
			}
			templateStatus := &aimv1alpha1.AIMServiceTemplateStatus{
				Status: constants.AIMStatusReady,
			}

			obs := ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: service,
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "existing-isvc",
								Namespace: testNamespace,
							},
						},
					},
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithImage("test-image:v1").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			}

			result := planInferenceService(ctx, service, "test-template", templateSpec, templateStatus, obs)
			if result == nil {
				t.Fatal("expected InferenceService to be planned, got nil")
			}

			isvc, ok := result.(*servingv1beta1.InferenceService)
			if !ok {
				t.Fatalf("expected *InferenceService, got %T", result)
			}

			if isvc.Spec.Predictor.MinReplicas == nil {
				t.Fatal("expected MinReplicas to be set")
			}
			if *isvc.Spec.Predictor.MinReplicas != tt.expectMin {
				t.Errorf("expected MinReplicas=%d, got %d", tt.expectMin, *isvc.Spec.Predictor.MinReplicas)
			}
			if isvc.Spec.Predictor.MaxReplicas != tt.expectMax {
				t.Errorf("expected MaxReplicas=%d, got %d", tt.expectMax, isvc.Spec.Predictor.MaxReplicas)
			}
		})
	}
}

// ============================================================================
// RESOLVE RESOURCES TESTS
// ============================================================================

func TestResolveResources(t *testing.T) {
	tests := []struct {
		name           string
		service        *aimv1alpha1.AIMService
		templateSpec   *aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuCount       int64 // Template profile GPU count
		expectGPU      bool
		expectGPUCount int64 // Expected final GPU count (defaults to gpuCount if 0)
		expectMemory   string
	}{
		{
			name:         "no GPU",
			service:      NewService("svc").Build(),
			templateSpec: nil,
			gpuCount:     0,
			expectGPU:    false,
		},
		{
			name:         "4 GPUs - default resources",
			service:      NewService("svc").Build(),
			templateSpec: nil,
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "128Gi", // 4 * 32Gi
		},
		{
			name: "service overrides memory resources",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("64Gi"),
					},
				}
				return svc
			}(),
			templateSpec: nil,
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "64Gi", // Override
		},
		{
			name: "service overrides GPU count",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(constants.DefaultGPUResourceName): resource.MustParse("8"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(constants.DefaultGPUResourceName): resource.MustParse("8"),
					},
				}
				return svc
			}(),
			templateSpec:   nil,
			gpuCount:       1, // Template says 1 GPU
			expectGPU:      true,
			expectGPUCount: 8, // Service overrides to 8 GPUs
		},
		{
			name:    "template spec resources",
			service: NewService("svc").Build(),
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
			},
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "256Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveResources(tt.service, tt.templateSpec, tt.gpuCount, corev1.ResourceName(constants.DefaultGPUResourceName))

			if tt.expectGPU {
				gpuQty := result.Requests[corev1.ResourceName(constants.DefaultGPUResourceName)]
				expectedGPU := tt.expectGPUCount
				if expectedGPU == 0 {
					expectedGPU = tt.gpuCount // Default to template GPU count
				}
				if gpuQty.Value() != expectedGPU {
					t.Errorf("expected GPU count %d, got %d", expectedGPU, gpuQty.Value())
				}
			}

			if tt.expectMemory != "" {
				memQty := result.Requests[corev1.ResourceMemory]
				expected := resource.MustParse(tt.expectMemory)
				if memQty.Cmp(expected) != 0 {
					t.Errorf("expected memory %s, got %s", tt.expectMemory, memQty.String())
				}
			}
		})
	}
}

// ============================================================================
// DEFAULT RESOURCE REQUIREMENTS TESTS
// ============================================================================

func TestDefaultResourceRequirementsForGPU(t *testing.T) {
	tests := []struct {
		name       string
		gpuCount   int64
		expectCPU  int64
		expectMem  string
		expectZero bool
	}{
		{
			name:       "0 GPUs",
			gpuCount:   0,
			expectZero: true,
		},
		{
			name:      "1 GPU",
			gpuCount:  1,
			expectCPU: 4,
			expectMem: "32Gi",
		},
		{
			name:      "4 GPUs",
			gpuCount:  4,
			expectCPU: 16,
			expectMem: "128Gi",
		},
		{
			name:      "8 GPUs",
			gpuCount:  8,
			expectCPU: 32,
			expectMem: "256Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultResourceRequirementsForGPU(tt.gpuCount)

			if tt.expectZero {
				if len(result.Requests) != 0 || len(result.Limits) != 0 {
					t.Errorf("expected zero resources, got %+v", result)
				}
				return
			}

			cpuQty := result.Requests[corev1.ResourceCPU]
			if cpuQty.Value() != tt.expectCPU {
				t.Errorf("expected CPU %d, got %d", tt.expectCPU, cpuQty.Value())
			}

			memQty := result.Requests[corev1.ResourceMemory]
			expectedMem := resource.MustParse(tt.expectMem)
			if memQty.Cmp(expectedMem) != 0 {
				t.Errorf("expected memory %s, got %s", tt.expectMem, memQty.String())
			}
		})
	}
}

// ============================================================================
// MERGE RESOURCE REQUIREMENTS TESTS
// ============================================================================

func TestMergeResourceRequirements(t *testing.T) {
	tests := []struct {
		name     string
		base     corev1.ResourceRequirements
		override *corev1.ResourceRequirements
		expected corev1.ResourceRequirements
	}{
		{
			name: "nil override returns base",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
			override: nil,
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
		},
		{
			name: "override replaces matching keys",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
					corev1.ResourceCPU:    resource.MustParse("4"),
				},
			},
			override: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
					corev1.ResourceCPU:    resource.MustParse("4"),
				},
			},
		},
		{
			name: "override adds new keys",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
			override: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeResourceRequirements(tt.base, tt.override)

			// Compare requests
			for key, expectedQty := range tt.expected.Requests {
				resultQty := result.Requests[key]
				if resultQty.Cmp(expectedQty) != 0 {
					t.Errorf("Requests[%s]: expected %s, got %s", key, expectedQty.String(), resultQty.String())
				}
			}

			// Compare limits
			for key, expectedQty := range tt.expected.Limits {
				resultQty := result.Limits[key]
				if resultQty.Cmp(expectedQty) != 0 {
					t.Errorf("Limits[%s]: expected %s, got %s", key, expectedQty.String(), resultQty.String())
				}
			}
		})
	}
}

// ============================================================================
// BUILD MERGED ENV VARS TESTS
// ============================================================================

func TestBuildMergedEnvVars(t *testing.T) {
	tests := []struct {
		name             string
		service          *aimv1alpha1.AIMService
		templateSpec     *aimv1alpha1.AIMServiceTemplateSpecCommon
		templateStatus   *aimv1alpha1.AIMServiceTemplateStatus
		obs              ServiceObservation
		expectContains   []string
		expectNotContain []string
	}{
		{
			name:           "system defaults always present",
			service:        &aimv1alpha1.AIMService{},
			templateSpec:   nil,
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMCachePath, constants.EnvVLLMEnableMetrics},
		},
		{
			name:    "template spec env vars",
			service: &aimv1alpha1.AIMService{},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Env: []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "custom-value"},
				},
			},
			obs:            ServiceObservation{},
			expectContains: []string{"CUSTOM_VAR"},
		},
		{
			name:    "template spec metric and precision",
			service: &aimv1alpha1.AIMService{},
			templateSpec: func() *aimv1alpha1.AIMServiceTemplateSpecCommon {
				latency := aimv1alpha1.AIMMetricLatency
				fp16 := aimv1alpha1.AIMPrecisionFP16
				return &aimv1alpha1.AIMServiceTemplateSpecCommon{
					AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
						Metric:    &latency,
						Precision: &fp16,
					},
				}
			}(),
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMMetric, constants.EnvAIMPrecision},
		},
		{
			name:    "template spec profile id",
			service: &aimv1alpha1.AIMService{},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				ProfileId: "my-profile-123",
			},
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMProfileID},
		},
		{
			name: "service env vars have highest precedence",
			service: &aimv1alpha1.AIMService{
				Spec: aimv1alpha1.AIMServiceSpec{
					AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
						Env: []corev1.EnvVar{
							{Name: "SERVICE_VAR", Value: "service-value"},
							{Name: "SHARED_VAR", Value: "from-service"},
						},
					},
				},
			},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Env: []corev1.EnvVar{
					{Name: "SHARED_VAR", Value: "from-template"},
				},
			},
			obs:            ServiceObservation{},
			expectContains: []string{"SERVICE_VAR", "SHARED_VAR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildMergedEnvVars(tt.service, tt.templateSpec, tt.obs)

			envMap := make(map[string]string)
			for _, env := range result {
				envMap[env.Name] = env.Value
			}

			for _, expected := range tt.expectContains {
				if _, ok := envMap[expected]; !ok {
					t.Errorf("expected env var %s not found", expected)
				}
			}

			for _, notExpected := range tt.expectNotContain {
				if _, ok := envMap[notExpected]; ok {
					t.Errorf("unexpected env var %s found", notExpected)
				}
			}
		})
	}
}

func TestBuildMergedEnvVars_IsSorted(t *testing.T) {
	service := &aimv1alpha1.AIMService{}
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "ZEBRA", Value: "z"},
			{Name: "APPLE", Value: "a"},
			{Name: "MANGO", Value: "m"},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, ServiceObservation{})

	for i := 1; i < len(result); i++ {
		if result[i-1].Name > result[i].Name {
			t.Errorf("env vars not sorted: %s > %s", result[i-1].Name, result[i].Name)
		}
	}
}

func TestBuildMergedEnvVars_ServiceOverridesAll(t *testing.T) {
	// Test that service env vars override template and runtime config
	service := &aimv1alpha1.AIMService{
		Spec: aimv1alpha1.AIMServiceSpec{
			AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
				Env: []corev1.EnvVar{
					{Name: "SHARED_VAR", Value: "from-service"},
				},
			},
		},
	}
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "SHARED_VAR", Value: "from-template"},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Value: &aimv1alpha1.AIMRuntimeConfigCommon{
					AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
						Env: []corev1.EnvVar{
							{Name: "SHARED_VAR", Value: "from-runtime-config"},
						},
					},
				},
			},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, obs)

	envMap := make(map[string]string)
	for _, env := range result {
		envMap[env.Name] = env.Value
	}

	// Service should win
	if envMap["SHARED_VAR"] != "from-service" {
		t.Errorf("expected SHARED_VAR='from-service', got '%s'", envMap["SHARED_VAR"])
	}
}

func TestBuildMergedEnvVars_ClusterTemplateEnv(t *testing.T) {
	// Test that env vars from cluster template spec (via common spec) propagate to inference service
	service := &aimv1alpha1.AIMService{}

	// Simulate a cluster template spec (same as namespace, just common spec)
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "CLUSTER_TOKEN", Value: "my-cluster-token"},
			{Name: "SHARED_VAR", Value: "from-cluster-template"},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, ServiceObservation{})

	envMap := make(map[string]string)
	for _, env := range result {
		envMap[env.Name] = env.Value
	}

	// Check cluster template env vars are present
	if val, ok := envMap["CLUSTER_TOKEN"]; !ok {
		t.Error("missing env var CLUSTER_TOKEN")
	} else if val != "my-cluster-token" {
		t.Errorf("expected CLUSTER_TOKEN='my-cluster-token', got '%s'", val)
	}

	if val, ok := envMap["SHARED_VAR"]; !ok {
		t.Error("missing env var SHARED_VAR")
	} else if val != "from-cluster-template" {
		t.Errorf("expected SHARED_VAR='from-cluster-template', got '%s'", val)
	}
}
