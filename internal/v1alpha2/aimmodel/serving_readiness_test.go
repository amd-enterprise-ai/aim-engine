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
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestModelPipeline_ImageDerivationReadiness(t *testing.T) {
	cases := []struct {
		name       string
		child      aimv1alpha1.AIMProfileSetStatus
		wantStatus constants.AIMStatus
	}{
		{
			name: "no compatible hardware",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusNotAvailable,
				ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 1, NotAvailable: 1},
			},
			wantStatus: constants.AIMStatusNotAvailable,
		},
		{
			name: "derivation failed",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusFailed,
			},
			wantStatus: constants.AIMStatusFailed,
		},
		{
			name: "empty selection",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusNotAvailable,
			},
			wantStatus: constants.AIMStatusNotAvailable,
		},
		{
			name: "stale ready generation",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 0, Status: constants.AIMStatusReady,
				ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 1, Ready: 1},
			},
			wantStatus: constants.AIMStatusProgressing,
		},
		{
			name: "ready set without ready profiles",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusReady,
				ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 1},
			},
			wantStatus: constants.AIMStatusNotAvailable,
		},
		{
			name: "partially ready",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusDegraded,
				ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 2, Ready: 1, NotAvailable: 1},
			},
			wantStatus: constants.AIMStatusDegraded,
		},
		{
			name: "ready",
			child: aimv1alpha1.AIMProfileSetStatus{
				ObservedGeneration: 1, Status: constants.AIMStatusReady,
				ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 1, Ready: 1},
			},
			wantStatus: constants.AIMStatusReady,
		},
	}
	for _, cluster := range []bool{false, true} {
		scope := "AIMModel"
		if cluster {
			scope = "AIMClusterModel"
		}
		t.Run(scope, func(t *testing.T) {
			for _, templates := range []constants.AIMStatus{"", constants.AIMStatusReady, constants.AIMStatusFailed} {
				name := string(templates)
				if name == "" {
					name = "disabled"
				}
				t.Run("templates "+name, func(t *testing.T) {
					for _, test := range cases {
						t.Run(test.name, func(t *testing.T) {
							status := runImageDerivationReadiness(t, cluster, templates, test.child)
							if status.Status != test.wantStatus {
								t.Errorf("model status = %q, want %q", status.Status, test.wantStatus)
							}
							wantReady := metav1.ConditionFalse
							if test.wantStatus == constants.AIMStatusReady {
								wantReady = metav1.ConditionTrue
							}
							condition := meta.FindStatusCondition(status.Conditions, "Ready")
							if condition == nil || condition.Status != wantReady {
								t.Errorf("Ready = %#v, want %s", condition, wantReady)
							}
						})
					}
				})
			}
		})
	}
}

// Child statuses are Kubernetes dependency snapshots. Fetch, compose, planning,
// the readiness rollup, and the persisted parent status all use production code.
func runImageDerivationReadiness(
	t *testing.T,
	cluster bool,
	templates constants.AIMStatus,
	childStatus aimv1alpha1.AIMProfileSetStatus,
) aimv1alpha1.AIMModelStatus {
	t.Helper()
	ctx := context.Background()
	model := imageDerivationReadinessModel(templates != "")
	if cluster {
		model.Namespace = ""
	}
	cache := readinessDiscoveryCache(t, model)
	childName, err := ChildProfileSetName(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	cacheRef := &aimv1alpha1.DiscoveryCacheReference{Name: cache.Name, Namespace: cache.Namespace}
	clusterModel := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: model.ObjectMeta, Spec: model.Spec, Status: model.Status,
	}
	objects := []client.Object{cache}
	if cluster {
		cache.OwnerReferences = []metav1.OwnerReference{ownerReferenceForClusterModel(clusterModel)}
		child := buildDesiredClusterProfileSet(clusterModel, cacheRef, childName)
		child.Generation, child.Status = 1, childStatus
		objects = append(objects, clusterModel, child)
		if templates != "" {
			template := &aimv1alpha1.AIMClusterServiceTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy-template"},
			}
			template.Spec.ModelName, template.Status.Status = model.Name, templates
			objects = append(objects, template)
		}
	} else {
		child := buildDesiredProfileSet(model, cacheRef, childName)
		child.Generation, child.Status = 1, childStatus
		objects = append(objects, model, child)
		if templates != "" {
			template := &aimv1alpha1.AIMServiceTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy-template", Namespace: model.Namespace},
			}
			template.Spec.ModelName, template.Status.Status = model.Name, templates
			objects = append(objects, template)
		}
	}
	k8sClient, scheme := imageDerivationReadinessClient(t, objects...)
	recorder := record.NewFakeRecorder(50)
	clientset := kubernetesfake.NewSimpleClientset()
	var status aimv1alpha1.AIMModelStatus
	templateCondition := "ServiceTemplatesReady"
	if cluster {
		pipeline := controllerutils.Pipeline[
			*aimv1alpha2.AIMClusterModel, *aimv1alpha1.AIMModelStatus, ClusterModelFetchResult, ClusterModelObservation,
		]{
			Client: k8sClient, StatusClient: k8sClient.Status(), Scheme: scheme,
			Reconciler: &ClusterModelReconciler{Client: k8sClient, Scheme: scheme},
			Recorder:   recorder, ControllerName: "cluster-model", Clientset: clientset, ForceApply: true,
		}
		if _, err := pipeline.Run(ctx, clusterModel); err != nil {
			t.Fatalf("Pipeline.Run() error = %v; conditions = %#v", err, clusterModel.Status.Conditions)
		}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterModel), clusterModel); err != nil {
			t.Fatal(err)
		}
		status, templateCondition = clusterModel.Status, "ClusterServiceTemplatesReady"
	} else {
		pipeline := controllerutils.Pipeline[
			*aimv1alpha2.AIMModel, *aimv1alpha1.AIMModelStatus, ModelFetchResult, ModelObservation,
		]{
			Client: k8sClient, StatusClient: k8sClient.Status(), Scheme: scheme,
			Reconciler: &ModelReconciler{Client: k8sClient, Scheme: scheme},
			Recorder:   recorder, ControllerName: "model", Clientset: clientset, ForceApply: true,
		}
		if _, err := pipeline.Run(ctx, model); err != nil {
			t.Fatalf("Pipeline.Run() error = %v; conditions = %#v", err, model.Status.Conditions)
		}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
			t.Fatal(err)
		}
		status = model.Status
	}
	legacyCondition := meta.FindStatusCondition(status.Conditions, templateCondition)
	if legacyCondition == nil {
		t.Fatal("legacy template condition must remain visible")
	}
	if templates == "" && legacyCondition.Reason != aimv1alpha1.AIMModelReasonNoTemplatesExpected {
		t.Fatalf("expected disabled-template path, got %#v", legacyCondition)
	}
	if templates == constants.AIMStatusReady && legacyCondition.Status != metav1.ConditionTrue {
		t.Fatalf("fixture must expose Ready legacy templates: %#v", legacyCondition)
	}
	if templates == constants.AIMStatusFailed && legacyCondition.Status != metav1.ConditionFalse {
		t.Fatalf("fixture must expose failed legacy templates: %#v", legacyCondition)
	}
	if !meta.IsStatusConditionTrue(status.Conditions, "DiscoveryReady") {
		t.Fatalf("fixture must have a valid discovery cache: %#v", status.Conditions)
	}
	return status
}

func imageDerivationReadinessModel(createTemplates bool) *aimv1alpha2.AIMModel {
	return &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name: "partitioned", Namespace: "team-a", UID: "model-uid", Generation: 1,
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "registry.example/aim:1.0",
			Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
				CreateServiceTemplates: ptr.To(createTemplates),
			},
			Profiles: &aimv1alpha1.AIMModelProfilesSpec{
				DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
					Selector: aimv1alpha1.ProfileSelector{
						AcceleratorModel: "MI300X",
						AcceleratorCount: ptr.To(int32(1)),
					},
				},
				Overrides: &aimv1alpha1.ProfileOverrides{
					AcceleratorPartitioningMode: "CPX-NPS4",
				},
				VersionPolicy: aimv1alpha1.ProfileVersionPolicyPinned,
				Version:       "1.0.0",
			},
		},
		Status: aimv1alpha1.AIMModelStatus{
			ImageMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{CanonicalName: "partitioned"},
			},
		},
	}
}

func imageDerivationReadinessClient(t *testing.T, objects ...client.Object) (client.Client, *runtime.Scheme) {
	t.Helper()
	scheme := dualPathScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(
			&aimv1alpha2.AIMModel{}, &aimv1alpha2.AIMClusterModel{},
			&aimv1alpha2.AIMProfileSet{}, &aimv1alpha2.AIMClusterProfileSet{},
		).
		WithIndex(&aimv1alpha1.AIMServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
			return []string{obj.(*aimv1alpha1.AIMServiceTemplate).Spec.ModelName}
		}).
		WithIndex(&aimv1alpha2.AIMProfile{}, ManagedModelUIDIndexKey, func(obj client.Object) []string {
			return []string{obj.GetAnnotations()[annotationModelUID]}
		}).
		WithIndex(&aimv1alpha1.AIMClusterServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
			return []string{obj.(*aimv1alpha1.AIMClusterServiceTemplate).Spec.ModelName}
		}).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, ManagedModelUIDIndexKey, func(obj client.Object) []string {
			return []string{obj.GetAnnotations()[annotationModelUID]}
		}).
		Build()
	return k8sClient, scheme
}

func readinessDiscoveryCache(t *testing.T, model *aimv1alpha2.AIMModel) *corev1.ConfigMap {
	t.Helper()
	name, err := DiscoveryCacheName(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	namespace := model.Namespace
	if namespace == "" {
		namespace = constants.GetOperatorNamespace()
	}
	cache, err := BuildDiscoveryCacheConfigMap(DiscoveryCacheInput{
		Name: name, Namespace: namespace, ModelName: model.Name, ModelUID: string(model.UID),
		SourceImage: model.Spec.Image,
		AimID:       "acme/model",
		SourceSpecHash: ComputeDiscoverySpecHash(
			model.Spec.Image, model.Spec.ImagePullSecrets, model.Spec.ServiceAccountName, DiscoveryCommandVersion,
		),
		Profiles: map[string][]byte{"mi300x.yaml": []byte(`aim_id: acme/model
model_id: acme/model
metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: standard
  accelerator_model: MI300X
  accelerator_count: 1
  primary: true
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cache.OwnerReferences = []metav1.OwnerReference{ownerReferenceForModel(model)}
	return cache
}
