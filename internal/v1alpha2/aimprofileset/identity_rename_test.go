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
	"maps"
	"os"
	"path/filepath"
	"slices"
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

func identityRenameCandidate(profileID string) profileSetCandidate {
	return profileSetCandidate{
		ProfileCopyCandidate: aimprofile.ProfileCopyCandidate{
			SourceGroup:  "AIMClusterModel//qwen3-32b/model-uid",
			SourceID:     "AIMClusterModel//qwen3-32b/model-uid/" + profileID + ".yaml",
			Name:         "qwen3-32b-" + profileID,
			YAMLContract: profileyaml.DefaultContract(),
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            "qwen/qwen3-32b",
				ModelId:          "Qwen/Qwen3-32B",
				ProfileId:        profileID,
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

// identityRenameScope runs ComposeState and PlanResources for one profile set scope.
type identityRenameScope struct {
	name      string
	resource  string
	reconcile func(t *testing.T, candidates []profileSetCandidate, spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) (applied, deleted []client.Object)
}

func identityRenameScopes() []identityRenameScope {
	return []identityRenameScope{
		{
			name:     "AIMProfileSet",
			resource: "aimprofiles",
			reconcile: func(t *testing.T, candidates []profileSetCandidate, spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) ([]client.Object, []client.Object) {
				t.Helper()
				set := &aimv1alpha2.AIMProfileSet{
					ObjectMeta: metav1.ObjectMeta{Name: "mi308x-variants", Namespace: "team-a", UID: "set-uid"},
					Spec:       spec,
				}
				reconciler := &ProfileSetReconciler{}
				rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}
				obs := reconciler.ComposeState(context.Background(), rc, ProfileSetFetchResult{
					set:        set,
					candidates: controllerutils.FetchResult[[]profileSetCandidate]{Value: candidates},
					managed:    controllerutils.FetchResult[[]managedProfile]{Value: existing},
				})
				if obs.buildErr != nil {
					t.Fatalf("ComposeState() buildErr = %v", obs.buildErr)
				}
				plan := reconciler.PlanResources(context.Background(), rc, obs)
				return plan.GetToApply(), plan.GetToDelete()
			},
		},
		{
			name:     "AIMClusterProfileSet",
			resource: "aimclusterprofiles",
			reconcile: func(t *testing.T, candidates []profileSetCandidate, spec aimv1alpha1.AIMProfileSetSpec, existing []managedProfile) ([]client.Object, []client.Object) {
				t.Helper()
				set := &aimv1alpha2.AIMClusterProfileSet{
					ObjectMeta: metav1.ObjectMeta{Name: "mi308x-variants", UID: "set-uid"},
					Spec:       spec,
				}
				reconciler := &ClusterProfileSetReconciler{}
				rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet]{Object: set}
				obs := reconciler.ComposeState(context.Background(), rc, ClusterProfileSetFetchResult{
					set:        set,
					candidates: controllerutils.FetchResult[[]profileSetCandidate]{Value: candidates},
					managed:    controllerutils.FetchResult[[]managedProfile]{Value: existing},
				})
				if obs.buildErr != nil {
					t.Fatalf("ComposeState() buildErr = %v", obs.buildErr)
				}
				plan := reconciler.PlanResources(context.Background(), rc, obs)
				return plan.GetToApply(), plan.GetToDelete()
			},
		},
	}
}

// profileSetSim holds the profiles a profile set has derived and carries out
// each planned delete and apply the way the API server would: applies are
// checked against the CRD's CEL rules, and deleted profiles stay terminating
// until finishDeletion removes them.
type profileSetSim struct {
	t          *testing.T
	scope      identityRenameScope
	structural *schema.Structural
	validator  *cel.Validator
	candidates []profileSetCandidate
	profiles   map[string]managedProfile
}

func newProfileSetSim(t *testing.T, scope identityRenameScope, candidates []profileSetCandidate) *profileSetSim {
	t.Helper()
	structural, validator := generatedProfileValidator(t, scope.resource)
	return &profileSetSim{
		t:          t,
		scope:      scope,
		structural: structural,
		validator:  validator,
		candidates: candidates,
		profiles:   map[string]managedProfile{},
	}
}

// reconcile runs one pass with overrides.aimId set to aimID and returns the
// sorted names it applied and deleted.
func (s *profileSetSim) reconcile(aimID string) (applied, deleted []string) {
	s.t.Helper()
	existing := make([]managedProfile, 0, len(s.profiles))
	for _, name := range s.names() {
		existing = append(existing, s.profiles[name])
	}
	toApply, toDelete := s.scope.reconcile(s.t, s.candidates, identityRenameSpec(aimID), existing)
	for _, obj := range toDelete {
		if profile, found := s.profiles[obj.GetName()]; found && profile.Object.GetDeletionTimestamp() == nil {
			now := metav1.Now()
			profile.Object.SetDeletionTimestamp(&now)
		}
		deleted = append(deleted, obj.GetName())
	}
	for _, obj := range toApply {
		name := obj.GetName()
		if current, found := s.profiles[name]; found {
			if current.Object.GetDeletionTimestamp() != nil {
				s.t.Fatalf("applied %s while it is still being deleted", name)
			}
			if errs := transitionErrors(s.t, s.structural, s.validator, current.Object, obj); len(errs) > 0 {
				s.t.Fatalf("update of %s is rejected by admission: %v", name, errs)
			}
		}
		s.profiles[name] = asReadyManaged(obj)
		applied = append(applied, name)
	}
	slices.Sort(applied)
	slices.Sort(deleted)
	return applied, deleted
}

// converge reconciles until a pass plans no deletes, finishing each pass's
// deletions before the next one.
func (s *profileSetSim) converge(aimID string) {
	s.t.Helper()
	for range 5 {
		_, deleted := s.reconcile(aimID)
		s.finishDeletion()
		if len(deleted) == 0 {
			return
		}
	}
	s.t.Fatalf("profile set did not converge on aimId %q", aimID)
}

// finishDeletion removes the named terminating profiles, or all of them when
// no names are given.
func (s *profileSetSim) finishDeletion(names ...string) {
	for name, profile := range s.profiles {
		if profile.Object.GetDeletionTimestamp() != nil && (len(names) == 0 || slices.Contains(names, name)) {
			delete(s.profiles, name)
		}
	}
}

func (s *profileSetSim) markTerminating(name string) {
	now := metav1.Now()
	s.profiles[name].Object.SetDeletionTimestamp(&now)
}

func (s *profileSetSim) setAimID(name, aimID string) {
	switch profile := s.profiles[name].Object.(type) {
	case *aimv1alpha2.AIMProfile:
		profile.Spec.AimId = aimID
	case *aimv1alpha2.AIMClusterProfile:
		profile.Spec.AimId = aimID
	}
}

func (s *profileSetSim) names() []string {
	return slices.Sorted(maps.Keys(s.profiles))
}

// aimIDs maps each profile name to its aimId.
func (s *profileSetSim) aimIDs() map[string]string {
	out := make(map[string]string, len(s.profiles))
	for name, profile := range s.profiles {
		spec, _ := profileSpecCommon(profile.Object)
		out[name] = spec.AimId
	}
	return out
}

func sameAimID(names []string, aimID string) map[string]string {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = aimID
	}
	return out
}

// Changing overrides.aimId on an existing set must not plan an in-place update
// of a profile whose spec.aimId is immutable. The profile is deleted and
// recreated under the same name, so the name depends only on the set's spec.
func TestProfileSetOverrideAimIDChangeReplacesImmutableProfile(t *testing.T) {
	t.Parallel()

	const (
		aimA = "acme/qwen3-32b-mi308x"
		aimB = "acme/qwen3-32b-mi308x-v2"
		aimC = "acme/qwen3-32b-mi308x-v3"
	)
	single := []profileSetCandidate{identityRenameCandidate("mi300x-fp8-latency")}
	pair := []profileSetCandidate{identityRenameCandidate("mi300x-fp8-latency"), identityRenameCandidate("mi300x-fp8-throughput")}

	for _, tc := range []struct {
		name       string
		candidates []profileSetCandidate
		run        func(t *testing.T, s *profileSetSim)
	}{
		{
			name:       "changed aimId deletes the profile without applying over it",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				original := s.names()
				applied, deleted := s.reconcile(aimB)
				if len(applied) != 0 || !slices.Equal(deleted, original) {
					t.Fatalf("planned apply %v and delete %v, want only delete %v", applied, deleted, original)
				}
			},
		},
		{
			name:       "name is kept through A to B to C to A",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				original := s.names()
				for _, aimID := range []string{aimB, aimC, aimA} {
					s.converge(aimID)
					if got, want := s.aimIDs(), sameAimID(original, aimID); !maps.Equal(got, want) {
						t.Fatalf("after changing aimId to %q, profiles = %v, want %v", aimID, got, want)
					}
				}
			},
		},
		{
			name:       "edited set gets the same name as a new set with the same spec",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				s.converge(aimB)
				fresh := newProfileSetSim(t, s.scope, s.candidates)
				fresh.converge(aimB)
				if got, want := s.aimIDs(), fresh.aimIDs(); !maps.Equal(got, want) {
					t.Fatalf("edited set has profiles %v, new set has %v", got, want)
				}
			},
		},
		{
			name:       "empty aimId is set in place",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				names := s.names()
				s.setAimID(names[0], "")
				applied, deleted := s.reconcile(aimA)
				if !slices.Equal(applied, names) || len(deleted) != 0 {
					t.Fatalf("planned apply %v and delete %v, want only apply %v", applied, deleted, names)
				}
			},
		},
		{
			name:       "terminating profile is not applied over",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				names := s.names()
				s.markTerminating(names[0])
				if applied, deleted := s.reconcile(aimA); len(applied) != 0 || len(deleted) != 0 {
					t.Fatalf("planned apply %v and delete %v while %s is terminating, want nothing", applied, deleted, names[0])
				}
				s.finishDeletion()
				if applied, _ := s.reconcile(aimA); !slices.Equal(applied, names) {
					t.Fatalf("after deletion finished, applied %v, want %v", applied, names)
				}
			},
		},
		{
			name:       "revert while the old profile is still being deleted",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				original := s.names()
				s.reconcile(aimB)
				if applied, deleted := s.reconcile(aimA); len(applied) != 0 || len(deleted) != 0 {
					t.Fatalf("planned apply %v and delete %v while the old profile is terminating, want nothing", applied, deleted)
				}
				s.finishDeletion()
				s.converge(aimA)
				if got, want := s.aimIDs(), sameAimID(original, aimA); !maps.Equal(got, want) {
					t.Fatalf("profiles = %v, want %v", got, want)
				}
			},
		},
		{
			name:       "two outputs are replaced independently",
			candidates: pair,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				names := s.names()
				if len(names) != 2 {
					t.Fatalf("derived %v, want 2 profiles", names)
				}
				s.reconcile(aimB)
				s.finishDeletion(names[0])
				if applied, deleted := s.reconcile(aimB); !slices.Equal(applied, names[:1]) || len(deleted) != 0 {
					t.Fatalf("planned apply %v and delete %v, want only apply %v while %s is terminating", applied, deleted, names[:1], names[1])
				}
				s.finishDeletion()
				s.converge(aimB)
				if got, want := s.aimIDs(), sameAimID(names, aimB); !maps.Equal(got, want) {
					t.Fatalf("profiles = %v, want %v", got, want)
				}
			},
		},
		{
			name:       "profiles that are no longer selected are pruned",
			candidates: single,
			run: func(t *testing.T, s *profileSetSim) {
				s.converge(aimA)
				kept := s.names()
				s.candidates = pair
				s.converge(aimA)
				if len(s.names()) != 2 {
					t.Fatalf("derived %v, want 2 profiles", s.names())
				}
				s.candidates = single
				s.converge(aimA)
				if got := s.names(); !slices.Equal(got, kept) {
					t.Fatalf("profiles = %v, want %v", got, kept)
				}
			},
		},
	} {
		for _, scope := range identityRenameScopes() {
			t.Run(scope.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.run(t, newProfileSetSim(t, scope, tc.candidates))
			})
		}
	}
}
