// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

package aimprofile

import (
	"context"
	"errors"
	"testing"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

func TestProfilePipelineRuntimeApplyFailurePreservesObservedGeneration(t *testing.T) {
	t.Run("namespace", func(t *testing.T) {
		profile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "updated-profile", Namespace: "team-a", UID: "profile-uid"},
			Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: projectionRecoverySpec()},
		}
		reconciler := &ProfileReconciler{ProjectionMode: aimv1alpha2.RuntimeProjectionModeExhaustive}
		testRuntimeApplyGeneration(t, profile, reconciler, &kservev1alpha1.ServingRuntime{})
	})

	t.Run("cluster", func(t *testing.T) {
		profile := &aimv1alpha2.AIMClusterProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "updated-cluster-profile", UID: "cluster-profile-uid"},
			Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: projectionRecoverySpec()},
		}
		reconciler := &ClusterProfileReconciler{ProjectionMode: aimv1alpha2.RuntimeProjectionModeExhaustive}
		testRuntimeApplyGeneration(t, profile, reconciler, &kservev1alpha1.ClusterServingRuntime{})
	})
}

// Run the production Fetch/Compose/Plan/DecorateStatus methods through the full
// pipeline: status decoration must not bypass its apply-success generation guard.
func testRuntimeApplyGeneration[T controllerutils.ObjectWithStatus[*aimv1alpha2.AIMProfileStatus], F, Obs any](
	t *testing.T,
	profile T,
	reconciler controllerutils.DomainReconciler[T, *aimv1alpha2.AIMProfileStatus, F, Obs],
	projectedRuntime client.Object,
) {
	t.Helper()
	ctx := context.Background()
	profile.SetGeneration(7)
	profile.GetStatus().ObservedGeneration = 6
	profile.GetStatus().Status = constants.AIMStatusReady

	scheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		aimv1alpha2.AddToScheme,
		kservev1alpha1.AddToScheme,
	} {
		if err := addToScheme(scheme); err != nil {
			t.Fatalf("add API to scheme: %v", err)
		}
	}

	applyErr := errors.New("injected runtime apply failure")
	runtimeApplyCalls := 0
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(profile).
		WithStatusSubresource(profile).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() != types.ApplyPatchType {
					return c.Patch(ctx, obj, patch, opts...)
				}
				switch obj.(type) {
				case *kservev1alpha1.ServingRuntime, *kservev1alpha1.ClusterServingRuntime:
					runtimeApplyCalls++
					if applyErr != nil {
						return applyErr
					}
				case *corev1.ConfigMap:
					// The namespace runtime's prerequisite must succeed so the
					// injected failure occurs at the runtime apply itself.
				default:
					return c.Patch(ctx, obj, patch, opts...)
				}

				// The fake client does not support SSA. Persist successful
				// projection writes with Create/Update; status writes stay real.
				existing := obj.DeepCopyObject().(client.Object)
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), existing); apierrors.IsNotFound(err) {
					return c.Create(ctx, obj)
				} else if err != nil {
					return err
				}
				obj.SetResourceVersion(existing.GetResourceVersion())
				return c.Update(ctx, obj)
			},
		}).
		Build()
	pipeline := controllerutils.Pipeline[T, *aimv1alpha2.AIMProfileStatus, F, Obs]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       record.NewFakeRecorder(50),
		ControllerName: "profile-apply-generation-test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	_, err := pipeline.Run(ctx, profile)
	if !errors.Is(err, applyErr) {
		t.Fatalf("pipeline error = %v, want injected runtime apply failure", err)
	}
	var infraErr controllerutils.InfrastructureError
	if !errors.As(err, &infraErr) || infraErr.Count != 1 {
		t.Fatalf("pipeline error = %v, want one infrastructure error", err)
	}
	if runtimeApplyCalls != 1 {
		t.Fatalf("runtime apply calls = %d, want 1", runtimeApplyCalls)
	}

	persisted := profile.DeepCopyObject().(T)
	if err := cl.Get(ctx, client.ObjectKeyFromObject(profile), persisted); err != nil {
		t.Fatalf("get profile after failed apply: %v", err)
	}
	if got := persisted.GetStatus().ObservedGeneration; got != 6 {
		t.Fatalf("persisted ObservedGeneration = %d, want 6 after failed apply (generation %d)", got, persisted.GetGeneration())
	}
	requireCondition(t, persisted.GetStatus().Conditions, controllerutils.ConditionTypeDependenciesReachable).
		assertStatus(t, metav1.ConditionFalse)

	runtimeKey := client.ObjectKey{Namespace: profile.GetNamespace(), Name: serving.RuntimeName(profile.GetName())}
	if err := cl.Get(ctx, runtimeKey, projectedRuntime); !apierrors.IsNotFound(err) {
		t.Fatalf("get runtime after failed apply: %v, want NotFound", err)
	}

	// A successful retry must advance the generation, not leave it stale forever.
	applyErr = nil
	if _, err := pipeline.Run(ctx, persisted); err != nil {
		t.Fatalf("retry pipeline: %v", err)
	}
	if runtimeApplyCalls != 2 {
		t.Fatalf("runtime apply calls after retry = %d, want 2", runtimeApplyCalls)
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(profile), persisted); err != nil {
		t.Fatalf("get profile after retry: %v", err)
	}
	if got := persisted.GetStatus().ObservedGeneration; got != persisted.GetGeneration() {
		t.Fatalf("persisted ObservedGeneration = %d, want %d after successful retry", got, persisted.GetGeneration())
	}
	if persisted.GetStatus().Status != constants.AIMStatusReady {
		t.Fatalf("status after retry = %q, want Ready", persisted.GetStatus().Status)
	}
	requireCondition(t, persisted.GetStatus().Conditions, controllerutils.ConditionTypeDependenciesReachable).
		assertStatus(t, metav1.ConditionTrue)
	if err := cl.Get(ctx, runtimeKey, projectedRuntime); err != nil {
		t.Fatalf("get runtime after successful retry: %v", err)
	}
}
