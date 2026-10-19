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
	"reflect"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

func TestLegacyChildPreservesNestedCredentials(t *testing.T) {
	nested := &aimv1alpha1.AIMProfileSetSpec{
		ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "nested-pull-secret"}},
		ServiceAccountName: "nested-account",
	}
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team", UID: "model-uid"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:            "registry/model@sha256:digest",
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "pull-secret"}}, ServiceAccountName: "model-account",
			ProfileCopy: nested,
		},
	}
	child := buildDesiredProfileSet(model, &aimv1alpha1.DiscoveryCacheReference{Name: "catalog"}, "child")
	if child == nil || child.Spec.SourceRef.Name != "catalog" || child.Spec.ServiceAccountName != nested.ServiceAccountName ||
		!reflect.DeepEqual(child.Spec.ImagePullSecrets, nested.ImagePullSecrets) {
		t.Fatalf("legacy child credentials overwritten: %+v", child)
	}
	cluster := &aimv1alpha2.AIMClusterModel{ObjectMeta: model.ObjectMeta, Spec: model.Spec}
	cluster.Namespace = ""
	clusterChild := buildDesiredClusterProfileSet(cluster, &aimv1alpha1.DiscoveryCacheReference{Name: "catalog"}, "child")
	if clusterChild == nil || !reflect.DeepEqual(clusterChild.Spec, child.Spec) {
		t.Fatal("cluster legacy child behavior differs")
	}
}

func TestModelDoesNotDeleteForeignChild(t *testing.T) {
	ctx := context.Background()
	model := &aimv1alpha2.AIMModel{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team", UID: "current"}}
	cluster := &aimv1alpha2.AIMClusterModel{ObjectMeta: metav1.ObjectMeta{Name: "model", UID: "current"}}
	for _, ownerUID := range []string{"", "other", "current"} {
		t.Run(ownerUID, func(t *testing.T) {
			child := &aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: "team"}}
			if ownerUID != "" {
				child.OwnerReferences = []metav1.OwnerReference{CacheOwnerReference(aimv1alpha2.GroupVersion.String(), "AIMModel", model.Name, ownerUID)}
			}
			clusterChild := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: *child.ObjectMeta.DeepCopy()}
			clusterChild.Namespace = ""
			r := &ModelReconciler{}
			plan := r.PlanResources(ctx, controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, ModelObservation{
				ModelFetchResult: ModelFetchResult{model: model, childProfileSet: controllerutils.FetchResult[*aimv1alpha2.AIMProfileSet]{Value: child}},
				pruneSafe:        true,
			})
			cr := &ClusterModelReconciler{}
			clusterPlan := cr.PlanResources(ctx, controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: cluster}, ClusterModelObservation{
				ClusterModelFetchResult: ClusterModelFetchResult{model: cluster, childProfileSet: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfileSet]{Value: clusterChild}},
				pruneSafe:               true,
			})
			want := 0
			if ownerUID == "current" {
				want = 1
			}
			if len(plan.GetToDelete()) != want || len(clusterPlan.GetToDelete()) != want {
				t.Fatalf("unexpected child deletion for owner %q", ownerUID)
			}
		})
	}
}

func TestCopyabilityOptOutSurvivesHardwareLoss(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		for _, owner := range []string{"model-uid", "foreign"} {
			for _, removed := range []bool{false, true} {
				model := &aimv1alpha2.AIMModel{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team", UID: "model-uid"}}
				name, err := utils.GenerateDerivedName([]string{"model", "gpu"}, utils.WithHashSource("model", "gpu"))
				if err != nil {
					t.Fatal(err)
				}
				meta := metav1.ObjectMeta{
					Name: name, Namespace: "team", Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "false"},
					OwnerReferences: []metav1.OwnerReference{CacheOwnerReference(aimv1alpha2.GroupVersion.String(), "AIMModel", model.Name, owner)},
				}
				var live client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta}
				catalog := aimprofile.DiscoveryCatalog{Profiles: []aimprofile.DiscoveryCatalogItem{{YAMLContract: profileyaml.CanonicalContract(nil), Spec: aimv1alpha2.AIMProfileSpecCommon{
					ProfileId: "gpu", AcceleratorType: aimv1alpha1.AcceleratorTypeGPU, AcceleratorModel: "MI300X", AcceleratorCount: 1,
				}}}}
				if removed {
					catalog.Profiles = nil
				}
				existing := []managedProfile{{Object: live}}
				var desired []desiredProfile
				if cluster {
					model.Namespace = ""
					meta.Namespace = ""
					live = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta}
					existing[0].Object = live
					desired, err = buildDesiredOfficialClusterModelProfiles(&aimv1alpha2.AIMClusterModel{ObjectMeta: model.ObjectMeta}, catalog, nil, nil, existing...)
				} else {
					desired, err = buildDesiredOfficialProfiles(model, catalog, nil, nil, existing...)
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if owner == "model-uid" && !removed {
					want = 1
				}
				if len(desired) != want {
					t.Fatalf("cluster=%v owner=%s removed=%v: profiles=%d", cluster, owner, removed, len(desired))
				}
				preserveCopyability(desired, existing)
				if want == 1 && desired[0].Object.GetAnnotations()[aimprofile.AnnotationProfileCopyable] != "false" {
					t.Fatal("retained profile lost opt-out")
				}
			}
		}
	}
}

func TestDiscoveryDoesNotPublishPreviousModelJob(t *testing.T) {
	for _, stale := range []string{"ownership", "inputs"} {
		t.Run(stale, func(t *testing.T) {
			in := DiscoveryInputs{
				ModelName: "model", ModelUID: "current", Kind: DiscoveryKindAIMModel, Namespace: "team", Image: "registry/model:2.0",
				OwnerRef: CacheOwnerReference(aimv1alpha2.GroupVersion.String(), "AIMModel", "model", "current"),
			}
			oldImage := in.Image
			owner := in.OwnerRef
			if stale == "ownership" {
				owner.UID = "previous"
			} else {
				oldImage = "registry/model:1.0"
			}
			job := BuildDiscoveryJob(DiscoveryJobSpec{
				ModelName: in.ModelName, ModelUID: string(owner.UID), Kind: in.Kind, Namespace: in.Namespace, Image: oldImage,
				SpecHash: ComputeDiscoverySpecHash(oldImage, nil, "", DiscoveryCommandVersion), OwnerRef: owner,
			})
			decision := DecideDiscovery(context.Background(), nil, in, DiscoveryFetch{Job: controllerutils.FetchResult[*batchv1.Job]{Value: job}})
			if decision.Catalog != nil || decision.CompletedJobToDelete != job || decision.Reason != "DiscoveryInputsChanged" {
				t.Fatalf("stale job accepted: %+v", decision)
			}
		})
	}
}
