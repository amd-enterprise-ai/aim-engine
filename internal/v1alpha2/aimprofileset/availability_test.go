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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

func TestProfileSetAvailabilityUsesCurrentAlternatives(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			for _, tt := range []struct {
				name      string
				ready     bool
				edit      func(*aimv1alpha2.AIMProfile)
				sibling   constants.AIMStatus
				missing   bool
				want      constants.AIMStatus
				wantReady int32
			}{
				{name: "all-ready", ready: true, sibling: constants.AIMStatusReady, want: constants.AIMStatusReady, wantReady: 2},
				{name: "hardware-unavailable-alternative", ready: true, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusReady, wantReady: 1},
				{name: "failed-alternative", ready: true, sibling: constants.AIMStatusFailed, want: constants.AIMStatusReady, wantReady: 1},
				{name: "degraded-alternative", ready: true, sibling: constants.AIMStatusDegraded, want: constants.AIMStatusReady, wantReady: 1},
				{name: "pending-alternative", ready: true, sibling: constants.AIMStatusPending, want: constants.AIMStatusReady, wantReady: 1},
				{name: "progressing-alternative", ready: true, sibling: constants.AIMStatusProgressing, want: constants.AIMStatusReady, wantReady: 1},
				{name: "missing-alternative", ready: true, missing: true, want: constants.AIMStatusReady, wantReady: 1},
				{name: "stale-ready-with-ready-alternative", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { p.Status.ObservedGeneration-- }, sibling: constants.AIMStatusReady, want: constants.AIMStatusReady, wantReady: 1},
				{name: "old-spec-with-ready-alternative", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { p.Spec.Image = "registry.example/old:1" }, sibling: constants.AIMStatusReady, want: constants.AIMStatusReady, wantReady: 1},
				{name: "none-ready", sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusNotAvailable},
				{name: "last-ready-profile-failed", edit: func(p *aimv1alpha2.AIMProfile) { p.Status.Status = constants.AIMStatusFailed }, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusNotAvailable},
				{name: "stale-ready-generation", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { p.Status.ObservedGeneration-- }, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusProgressing},
				{name: "old-ready-spec", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { p.Spec.Image = "registry.example/old:1" }, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusProgressing},
				{name: "old-ready-contract", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { p.Annotations[profileyaml.AnnotationContract] = "old-contract" }, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusProgressing},
				{name: "deleting-ready-profile", ready: true, edit: func(p *aimv1alpha2.AIMProfile) { now := metav1.Now(); p.DeletionTimestamp = &now }, sibling: constants.AIMStatusNotAvailable, want: constants.AIMStatusProgressing},
			} {
				t.Run(tt.name, func(t *testing.T) {
					profile := &aimv1alpha2.AIMProfile{
						ObjectMeta: metav1.ObjectMeta{Name: "mi300x", Namespace: namespace, Generation: 2, Annotations: map[string]string{}},
						Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
							AimId: "architecture", ModelId: "fine-tuned", ProfileId: "mi300x",
							Image: "registry.example/runtime:1", AcceleratorModel: "MI300X",
							ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "fine-tuned", SourceURI: "s3://models/fine-tuned"}},
						}},
						Status: aimv1alpha2.AIMProfileStatus{ObservedGeneration: 2, Status: constants.AIMStatusNotAvailable},
					}
					if tt.ready {
						profile.Status.Status = constants.AIMStatusReady
					}
					sibling := profile.DeepCopy()
					sibling.Name = "mi325x"
					sibling.Spec.ProfileId = "mi325x"
					sibling.Spec.AcceleratorModel = "MI325X"
					sibling.Status.Status = tt.sibling
					asManaged := func(p *aimv1alpha2.AIMProfile) managedProfile {
						if namespace == "" {
							return managedProfile{
								Object: &aimv1alpha2.AIMClusterProfile{ObjectMeta: p.ObjectMeta, Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: p.Spec.AIMProfileSpecCommon}},
								Status: p.Status,
							}
						}
						return managedProfile{Object: p, Status: p.Status}
					}
					desired := []desiredProfile{{Object: asManaged(profile.DeepCopy()).Object}, {Object: asManaged(sibling.DeepCopy()).Object}}
					if tt.edit != nil {
						tt.edit(profile)
					}
					existing := []managedProfile{asManaged(profile)}
					if !tt.missing {
						existing = append(existing, asManaged(sibling))
					}
					counts, current := summarizeManagedProfiles(desired, existing)
					if counts.Ready != tt.wantReady || counts.Total != 2 || counts.Deployable != 2 {
						t.Fatalf("counts = %+v, want %d ready and 2 deployable", counts, tt.wantReady)
					}
					health := buildComponentHealth(nil, nil, counts, current, 0)
					if len(health) != 1 || health[0].State != tt.want {
						t.Fatalf("health = %+v, want %s", health, tt.want)
					}
					if tt.wantReady == 1 && (health[0].Reason != "ManagedProfilesPartiallyReady" || health[0].Message == "") {
						t.Fatalf("partial availability must remain visible: %+v", health)
					}
					if !tt.missing && tt.sibling == constants.AIMStatusNotAvailable && counts.NotAvailable != 1 && tt.ready {
						t.Fatalf("unavailable alternative was hidden: %+v", counts)
					}
				})
			}
		})
	}
}

func TestProfileSetAvailabilityDoesNotMaskSetErrors(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fetchErrs []error
		buildErr  error
		reason    string
	}{
		{name: "source-unreadable", fetchErrs: []error{errors.New("catalog unavailable")}, reason: "SourceLoadFailed"},
		{name: "invalid-derivation", buildErr: errors.New("invalid overrides"), reason: "DerivationFailed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			health := buildComponentHealth(tt.fetchErrs, tt.buildErr, aimv1alpha1.ManagedProfileCounts{Total: 2, Ready: 1}, 2, 0)
			if len(health) != 1 || health[0].State != constants.AIMStatusDegraded || health[0].Reason != tt.reason {
				t.Fatalf("set-wide error hidden by a ready profile: %+v", health)
			}
		})
	}
}

func TestProfileSetAvailabilityAllowsAdmittedProfileDefaults(t *testing.T) {
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "output", Generation: 1},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			ImagePullSecrets: []corev1.LocalObjectReference{},
		}},
	}
	desired := profile.DeepCopy()
	profile.Spec.AutoSelectionPolicy = aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized
	profile.Spec.AcceleratorPartitioningMode = aimprofile.PartitioningModeUnpartitioned
	counts, current := summarizeManagedProfiles(
		[]desiredProfile{{Object: desired}},
		[]managedProfile{{Object: profile, Status: aimv1alpha2.AIMProfileStatus{ObservedGeneration: 1, Status: constants.AIMStatusReady}}},
	)
	if counts.Ready != 1 || current != 1 {
		t.Fatalf("admitted defaults invalidate current ready profile: %+v, current=%d", counts, current)
	}
}
