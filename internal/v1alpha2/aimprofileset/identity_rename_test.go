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

package aimprofileset

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

// generatedProfileValidator evaluates the CEL rules of the generated v1alpha2
// profile CRD, so update tests see the same immutability rules as admission.
func generatedProfileValidator(t *testing.T, resource string) (*schema.Structural, *cel.Validator) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	for _, version := range crd.Spec.Versions {
		if version.Name != "v1alpha2" {
			continue
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(version.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
			t.Fatal(err)
		}
		structural, err := schema.NewStructural(&internal)
		if err != nil {
			t.Fatal(err)
		}
		return structural, cel.NewValidator(structural, true, 1000000)
	}
	t.Fatalf("%s CRD has no v1alpha2 schema", resource)
	return nil, nil
}

// transitionErrors returns the CEL errors that appear only when updating
// oldObj to newObj, ignoring rules newObj would fail on create as well.
func transitionErrors(t *testing.T, structural *schema.Structural, validator *cel.Validator, oldObj, newObj client.Object) []string {
	t.Helper()
	toSpecMap := func(obj client.Object) map[string]any {
		values, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"spec": values["spec"]}
	}
	oldValue, newValue := toSpecMap(oldObj), toSpecMap(newObj)
	createErrs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, newValue, nil, 10000000)
	updateErrs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, newValue, oldValue, 10000000)
	onCreate := make(map[string]bool, len(createErrs))
	for _, err := range createErrs {
		onCreate[err.Error()] = true
	}
	var out []string
	for _, err := range updateErrs {
		if !onCreate[err.Error()] {
			out = append(out, err.Error())
		}
	}
	return out
}

func identityRenameCandidate() profileSetCandidate {
	return profileSetCandidate{
		ProfileCopyCandidate: aimprofile.ProfileCopyCandidate{
			SourceGroup:  "AIMClusterModel//qwen3-32b/model-uid",
			SourceID:     "AIMClusterModel//qwen3-32b/model-uid/mi300x-fp8-latency.yaml",
			Name:         "qwen3-32b-mi300x-fp8-latency",
			YAMLContract: profileyaml.DefaultContract(),
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            "qwen/qwen3-32b",
				ModelId:          "Qwen/Qwen3-32B",
				ProfileId:        "mi300x-fp8-latency",
				Image:            "docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.11.0",
				Engine:           "vllm",
				AcceleratorModel: "MI300X",
				AcceleratorCount: 1,
			},
		},
		contractResolved: true,
	}
}

func identityRenameSpec(aimID string) aimv1alpha1.AIMProfileSetSpec {
	return aimv1alpha1.AIMProfileSetSpec{
		Selector:      aimv1alpha1.ProfileSelector{AcceleratorModel: "MI300X"},
		VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
		Overrides: &aimv1alpha1.ProfileOverrides{
			AimId:            aimID,
			AcceleratorModel: "MI308X",
		},
	}
}

func asReadyManaged(obj client.Object) managedProfile {
	obj.SetGeneration(1)
	return managedProfile{
		Object: obj,
		Status: aimv1alpha2.AIMProfileStatus{ObservedGeneration: 1, Status: constants.AIMStatusReady},
	}
}

// identityRenameReconciler runs ComposeState and PlanResources for one scope.
type identityRenameReconciler func(spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) (applied, deleted []client.Object)

// Changing overrides.aimId on an existing set must not plan an in-place update
// of a profile whose spec.aimId is immutable. The replacement takes a new
// name, the old profile is pruned, and the next reconcile keeps that name.
func TestProfileSetOverrideAimIDChangeReplacesImmutableProfile(t *testing.T) {
	t.Parallel()

	candidates := controllerutils.FetchResult[[]profileSetCandidate]{Value: []profileSetCandidate{identityRenameCandidate()}}

	namespaced := func(spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) ([]client.Object, []client.Object) {
		set := &aimv1alpha2.AIMProfileSet{
			ObjectMeta: metav1.ObjectMeta{Name: "mi308x-variants", Namespace: "team-a", UID: "set-uid"},
			Spec:       spec,
		}
		reconciler := &ProfileSetReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}
		obs := reconciler.ComposeState(context.Background(), rc, ProfileSetFetchResult{
			set:        set,
			candidates: candidates,
			managed:    controllerutils.FetchResult[[]managedProfile]{Value: existing},
		})
		if obs.buildErr != nil {
			t.Fatalf("ComposeState() buildErr = %v", obs.buildErr)
		}
		plan := reconciler.PlanResources(context.Background(), rc, obs)
		return plan.GetToApply(), plan.GetToDelete()
	}
	cluster := func(spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) ([]client.Object, []client.Object) {
		set := &aimv1alpha2.AIMClusterProfileSet{
			ObjectMeta: metav1.ObjectMeta{Name: "mi308x-variants", UID: "set-uid"},
			Spec:       spec,
		}
		reconciler := &ClusterProfileSetReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet]{Object: set}
		obs := reconciler.ComposeState(context.Background(), rc, ClusterProfileSetFetchResult{
			set:        set,
			candidates: candidates,
			managed:    controllerutils.FetchResult[[]managedProfile]{Value: existing},
		})
		if obs.buildErr != nil {
			t.Fatalf("ComposeState() buildErr = %v", obs.buildErr)
		}
		plan := reconciler.PlanResources(context.Background(), rc, obs)
		return plan.GetToApply(), plan.GetToDelete()
	}

	for _, tc := range []struct {
		name      string
		resource  string
		reconcile identityRenameReconciler
	}{
		{name: "AIMProfileSet", resource: "aimprofiles", reconcile: namespaced},
		{name: "AIMClusterProfileSet", resource: "aimclusterprofiles", reconcile: cluster},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			structural, validator := generatedProfileValidator(t, tc.resource)

			firstApplied, _ := tc.reconcile(identityRenameSpec("acme/qwen3-32b-mi308x"), nil)
			if len(firstApplied) != 1 {
				t.Fatalf("first reconcile applied %d profiles, want 1", len(firstApplied))
			}
			original := firstApplied[0]
			existing := []managedProfile{asReadyManaged(original)}

			applied, deleted := tc.reconcile(identityRenameSpec("acme/qwen3-32b-mi308x-v2"), existing)
			if len(applied) != 1 {
				t.Fatalf("second reconcile applied %d profiles, want 1", len(applied))
			}
			replacement := applied[0]
			wantAimID := "acme/qwen3-32b-mi308x-v2"
			if spec, _ := profileSpecCommon(replacement); spec.AimId != wantAimID {
				t.Fatalf("replacement aimId = %q, want %q", spec.AimId, wantAimID)
			}
			if replacement.GetName() == original.GetName() {
				if errs := transitionErrors(t, structural, validator, original, replacement); len(errs) > 0 {
					t.Fatalf("planned in-place update of %s is rejected by admission: %v", original.GetName(), errs)
				}
			}
			if len(deleted) != 1 || deleted[0].GetName() != original.GetName() {
				t.Fatalf("deleted = %v, want the original profile %s", objectNames(deleted), original.GetName())
			}

			afterPrune := []managedProfile{asReadyManaged(replacement)}
			stable, _ := tc.reconcile(identityRenameSpec(wantAimID), afterPrune)
			if len(stable) != 1 || stable[0].GetName() != replacement.GetName() {
				t.Fatalf("third reconcile applied %v, want stable name %s", objectNames(stable), replacement.GetName())
			}

			reverted, revertDeleted := tc.reconcile(identityRenameSpec("acme/qwen3-32b-mi308x"), afterPrune)
			if len(reverted) != 1 || reverted[0].GetName() != original.GetName() {
				t.Fatalf("reverted override applied %v, want original name %s", objectNames(reverted), original.GetName())
			}
			if len(revertDeleted) != 1 || revertDeleted[0].GetName() != replacement.GetName() {
				t.Fatalf("reverted override deleted %v, want %s", objectNames(revertDeleted), replacement.GetName())
			}
		})
	}
}

func objectNames(objects []client.Object) []string {
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		names = append(names, client.ObjectKeyFromObject(obj).String())
	}
	return names
}
