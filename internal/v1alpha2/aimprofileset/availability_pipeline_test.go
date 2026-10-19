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

package aimprofileset

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// Exercise production Fetch/Compose/Plan and the status engine, but capture
// resource plans instead of applying SSA (unsupported by the fake client).
// The test explicitly creates the planned objects with controller ownership.
type availabilityStatusReconciler[T controllerutils.ObjectWithStatus[S], S controllerutils.StatusWithConditions, F, Obs any] struct {
	controllerutils.DomainReconciler[T, S, F, Obs]
	controllerutils.StatusDecorator[T, S, Obs]
	plan controllerutils.PlanResult
}

func (r *availabilityStatusReconciler[T, S, F, Obs]) PlanResources(ctx context.Context, rc controllerutils.ReconcileContext[T], obs Obs) controllerutils.PlanResult {
	r.plan = r.DomainReconciler.PlanResources(ctx, rc, obs)
	return controllerutils.PlanResult{}
}

func runAvailabilityStatus[T controllerutils.ObjectWithStatus[S], S controllerutils.StatusWithConditions, F, Obs any](
	t *testing.T, c client.Client, obj T,
	delegate interface {
		controllerutils.DomainReconciler[T, S, F, Obs]
		controllerutils.StatusDecorator[T, S, Obs]
	},
) controllerutils.PlanResult {
	t.Helper()
	r := &availabilityStatusReconciler[T, S, F, Obs]{DomainReconciler: delegate, StatusDecorator: delegate}
	p := controllerutils.Pipeline[T, S, F, Obs]{
		Client: c, StatusClient: c.Status(), Scheme: c.Scheme(), Clientset: kubernetesfake.NewClientset(),
		Recorder: record.NewFakeRecorder(100), ControllerName: "availability-test", Reconciler: r,
	}
	if _, err := p.Run(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	return r.plan
}

func createAvailabilityOutput(t *testing.T, c client.Client, owner, obj client.Object) {
	t.Helper()
	obj.SetGeneration(1)
	obj.SetUID(types.UID(obj.GetName() + "-uid"))
	if err := controllerutil.SetControllerReference(owner, obj, c.Scheme()); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

func assertAvailabilityStatus(t *testing.T, status constants.AIMStatus, counts aimv1alpha1.ManagedProfileCounts, conditions []metav1.Condition, want constants.AIMStatus) {
	t.Helper()
	ready := int32(0)
	if want == constants.AIMStatusReady {
		ready = 1
	}
	if status != want || counts.Total != 2 || counts.Ready != ready || counts.NotAvailable != 2-ready || counts.Deployable != 2 {
		t.Fatalf("status=%s counts=%+v, want %s with %d/2 ready", status, counts, want, ready)
	}
	condition := meta.FindStatusCondition(conditions, controllerutils.ConditionTypeReady)
	if condition == nil || (condition.Status == metav1.ConditionTrue) != (want == constants.AIMStatusReady) {
		t.Fatalf("Ready condition disagrees with availability: %+v", conditions)
	}
}

func reconcileAvailabilityProfile(t *testing.T, c client.Client, obj client.Object, hardwarePresent bool) {
	t.Helper()
	var status aimv1alpha2.AIMProfileStatus
	var hardware string
	switch p := obj.(type) {
	case *aimv1alpha2.AIMProfile:
		runAvailabilityStatus(t, c, p, &aimprofile.ProfileReconciler{Client: c, Scheme: c.Scheme()})
		status, hardware = p.Status, p.Spec.AcceleratorModel
	case *aimv1alpha2.AIMClusterProfile:
		runAvailabilityStatus(t, c, p, &aimprofile.ClusterProfileReconciler{Client: c, Scheme: c.Scheme()})
		status, hardware = p.Status, p.Spec.AcceleratorModel
	default:
		t.Fatalf("unexpected profile type %T", obj)
	}
	want := constants.AIMStatusNotAvailable
	if hardwarePresent && hardware == testSourceAccelerator {
		want = constants.AIMStatusReady
	}
	if status.Status != want {
		t.Fatalf("%s profile status=%+v, want %s", hardware, status, want)
	}
	if want == constants.AIMStatusNotAvailable {
		condition := meta.FindStatusCondition(status.Conditions, aimv1alpha2.AIMProfileConditionHardwareAvailable)
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != aimv1alpha2.AIMProfileReasonHardwareNotAvailable {
			t.Fatalf("missing hardware diagnostic: %+v", status)
		}
	}
}

func TestFineTunedCatalogAvailabilityPipeline(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			source, catalog := sourceFixture(t, namespace, "base-model")
			catalog.Data[aimprofile.FlattenProfilePath("architecture/mi325x.yaml")] = strings.ReplaceAll(definitionYAML, testSourceAccelerator, "MI325X")
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mi300x-node", Labels: map[string]string{
					aimprofile.AcceleratorLabelPrefix + testSourceAccelerator:                       "8",
					aimprofile.AcceleratorVendorLabelPrefix + "GPU.AMD":                             "8",
					aimprofile.PartitioningSchemeLabelPrefix + aimprofile.PartitioningSchemeDefault: "8",
				}},
				Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
					"amd.com/gpu":         resource.MustParse("8"),
					corev1.ResourceCPU:    resource.MustParse("128"),
					corev1.ResourceMemory: resource.MustParse("1Ti"),
				}},
			}
			c := sourceClient(t, source.object, catalog, node)
			modelMeta := metav1.ObjectMeta{Name: "fine-tuned", Namespace: namespace, UID: "fine-tuned-uid", Generation: 1}
			modelSpec := aimv1alpha1.AIMModelSpec{Profiles: &aimv1alpha1.AIMModelProfilesSpec{
				DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{Selector: aimv1alpha1.ProfileSelector{
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: source.object.GetName()},
				}},
				VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
				Overrides: &aimv1alpha1.ProfileOverrides{
					ModelId:      "weights/fine-tuned",
					ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "weights/fine-tuned", SourceURI: "s3://models/fine-tuned"}},
				},
			}}
			var model client.Object
			var modelStatus *aimv1alpha1.AIMModelStatus
			var reconcileModel func() controllerutils.PlanResult
			if namespace == "" {
				m := &aimv1alpha2.AIMClusterModel{ObjectMeta: modelMeta, Spec: modelSpec}
				model, modelStatus = m, &m.Status
				reconcileModel = func() controllerutils.PlanResult {
					return runAvailabilityStatus(t, c, m, &aimmodel.ClusterModelReconciler{Client: c, Scheme: c.Scheme()})
				}
			} else {
				m := &aimv1alpha2.AIMModel{ObjectMeta: modelMeta, Spec: modelSpec}
				model, modelStatus = m, &m.Status
				reconcileModel = func() controllerutils.PlanResult {
					return runAvailabilityStatus(t, c, m, &aimmodel.ModelReconciler{Client: c, Scheme: c.Scheme()})
				}
			}
			if err := c.Create(context.Background(), model); err != nil {
				t.Fatal(err)
			}
			modelPlan := reconcileModel()
			children := modelPlan.GetToApply()
			if len(children) != 1 {
				t.Fatalf("model planned %d children, want one profile set", len(children))
			}
			set := children[0]
			createAvailabilityOutput(t, c, model, set)
			var setStatus *aimv1alpha1.AIMProfileSetStatus
			var reconcileSet func() controllerutils.PlanResult
			switch s := set.(type) {
			case *aimv1alpha2.AIMProfileSet:
				setStatus = &s.Status
				reconcileSet = func() controllerutils.PlanResult {
					return runAvailabilityStatus(t, c, s, &ProfileSetReconciler{Scheme: c.Scheme()})
				}
			case *aimv1alpha2.AIMClusterProfileSet:
				setStatus = &s.Status
				reconcileSet = func() controllerutils.PlanResult {
					return runAvailabilityStatus(t, c, s, &ClusterProfileSetReconciler{Scheme: c.Scheme()})
				}
			default:
				t.Fatalf("unexpected child type %T", set)
			}
			setPlan := reconcileSet()
			profiles := setPlan.GetToApply()
			if len(profiles) != 2 {
				t.Fatalf("catalog planned %d profiles, want both hardware alternatives", len(profiles))
			}
			for _, p := range profiles {
				spec, _ := profileSpecCommon(p)
				if spec.ModelId != "weights/fine-tuned" || len(spec.ModelSources) != 1 || spec.ModelSources[0].SourceURI != "s3://models/fine-tuned" {
					t.Fatalf("fine-tuned identity lost: %+v", spec)
				}
				createAvailabilityOutput(t, c, set, p)
			}
			// Ready -> NotAvailable -> Ready: removing the last compatible node
			// must revoke availability; restoring it must recover both parents.
			for _, hardwarePresent := range []bool{true, false, true} {
				for _, p := range profiles {
					reconcileAvailabilityProfile(t, c, p, hardwarePresent)
				}
				want := constants.AIMStatusNotAvailable
				if hardwarePresent {
					want = constants.AIMStatusReady
				}
				reconcileSet()
				reconcileModel()
				assertAvailabilityStatus(t, setStatus.Status, setStatus.ManagedProfiles, setStatus.Conditions, want)
				assertAvailabilityStatus(t, modelStatus.Status, modelStatus.ManagedProfiles, modelStatus.Conditions, want)
				if hardwarePresent {
					if err := c.Delete(context.Background(), node); err != nil {
						t.Fatal(err)
					}
				} else {
					node.ResourceVersion = ""
					if err := c.Create(context.Background(), node); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
