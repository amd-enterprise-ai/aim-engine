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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestDerivedModelAvailabilityUsesCurrentChild(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			for _, tt := range []struct {
				name         string
				edit         func(*aimv1alpha2.AIMModel, *aimv1alpha2.AIMProfileSet)
				inventoryErr error
				want         constants.AIMStatus
				wantReason   string
				current      bool
				prune        bool
			}{
				{name: "ready-with-unavailable-alternative", want: constants.AIMStatusReady, current: true},
				{name: "ready-with-failed-or-pending-alternative", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) { s.Status.ManagedProfiles.NotAvailable = 0 }, want: constants.AIMStatusReady, current: true},
				{name: "all-ready-permits-pruning", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.ManagedProfiles.Ready = 2
					s.Status.ManagedProfiles.NotAvailable = 0
				}, want: constants.AIMStatusReady, current: true, prune: true},
				{name: "no-ready-alternative", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusNotAvailable
					s.Status.ManagedProfiles.Ready = 0
					s.Status.ManagedProfiles.NotAvailable = 2
				}, want: constants.AIMStatusNotAvailable, current: true},
				{name: "empty-selection", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusNotAvailable
					s.Status.ManagedProfiles = aimv1alpha1.ManagedProfileCounts{}
				}, want: constants.AIMStatusNotAvailable, wantReason: "NoMatchingProfiles", current: true},
				{name: "copyability-excluded-selection", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusNotAvailable
					s.Status.ManagedProfiles = aimv1alpha1.ManagedProfileCounts{}
					s.Status.Conditions = []metav1.Condition{{
						Type:    "DerivationReady",
						Status:  metav1.ConditionFalse,
						Reason:  "NoEligibleProfiles",
						Message: "1 source profile matches the selection but is not published as copyable",
					}}
				}, want: constants.AIMStatusNotAvailable, wantReason: "NoEligibleProfiles", current: true},
				{name: "degraded-child-with-old-ready-count", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusDegraded
				}, want: constants.AIMStatusDegraded, current: true},
				{name: "failed-child-with-old-ready-count", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusFailed
				}, want: constants.AIMStatusFailed, current: true},
				{name: "progressing-child-with-old-ready-count", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Status.Status = constants.AIMStatusProgressing
				}, want: constants.AIMStatusProgressing, current: true},
				{name: "stale-child-generation", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) { s.Status.ObservedGeneration-- }, want: constants.AIMStatusProgressing},
				{name: "parent-overrides-changed", edit: func(m *aimv1alpha2.AIMModel, _ *aimv1alpha2.AIMProfileSet) {
					m.Spec.Profiles.Overrides.ModelSources[0].SourceURI = "s3://models/new-weights"
				}, want: constants.AIMStatusProgressing},
				{name: "parent-selector-changed", edit: func(m *aimv1alpha2.AIMModel, _ *aimv1alpha2.AIMProfileSet) {
					m.Spec.Profiles.DerivedFrom.Selector.AcceleratorModel = "MI325X"
				}, want: constants.AIMStatusProgressing},
				{name: "child-credentials-differ", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "new-credentials"}}
				}, want: constants.AIMStatusProgressing},
				{name: "foreign-child", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.OwnerReferences[0].UID = "different-model"
				}, want: constants.AIMStatusProgressing},
				{name: "unowned-child", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) { s.OwnerReferences = nil }, want: constants.AIMStatusProgressing},
				{name: "deleting-child", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					now := metav1.Now()
					s.DeletionTimestamp = &now
				}, want: constants.AIMStatusProgressing},
				{name: "inventory-unreadable", inventoryErr: errors.New("profile inventory unavailable"), want: constants.AIMStatusDegraded, current: true},
				{name: "admitted-selector-defaults", edit: func(_ *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					s.Spec.Selector.Role = aimv1alpha1.ProfileSelectorRoleDeployable
					s.Spec.Selector.ModelRef.Scope = aimv1alpha1.ProfileSelectorScopeAuto
					s.Spec.ImagePullSecrets = []corev1.LocalObjectReference{}
				}, want: constants.AIMStatusReady, current: true},
				{name: "admitted-version-default", edit: func(m *aimv1alpha2.AIMModel, s *aimv1alpha2.AIMProfileSet) {
					m.Spec.Profiles.VersionPolicy = ""
					m.Spec.Profiles.Version = "1.2.3"
					s.Spec.VersionPolicy = aimv1alpha1.ProfileVersionPolicyPinned
					s.Spec.Version = "1.2.3"
				}, want: constants.AIMStatusReady, current: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					model := &aimv1alpha2.AIMModel{
						ObjectMeta: metav1.ObjectMeta{Name: "fine-tuned", Namespace: namespace, UID: "model-uid", Generation: 2},
						Spec: aimv1alpha1.AIMModelSpec{Profiles: &aimv1alpha1.AIMModelProfilesSpec{
							DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{Selector: aimv1alpha1.ProfileSelector{
								ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "base-model"},
							}},
							VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
							Overrides: &aimv1alpha1.ProfileOverrides{
								AimId: "architecture", ModelId: "fine-tuned",
								ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "fine-tuned", SourceURI: "s3://models/weights"}},
							},
						}},
					}
					childName, err := childProfileSetName(model.Name)
					if err != nil {
						t.Fatal(err)
					}
					child := buildDesiredProfileSet(model, nil, childName)
					child.Generation = 2
					child.OwnerReferences = []metav1.OwnerReference{CacheOwnerReference(aimv1alpha2.GroupVersion.String(), "AIMModel", model.Name, string(model.UID))}
					child.Status = aimv1alpha1.AIMProfileSetStatus{
						Status: constants.AIMStatusReady, ObservedGeneration: 2,
						ManagedProfiles: aimv1alpha1.ManagedProfileCounts{Total: 2, Ready: 1, NotAvailable: 1, Deployable: 2},
					}
					if tt.edit != nil {
						tt.edit(model, child)
					}
					var health []controllerutils.ComponentHealth
					var current, prune bool
					if namespace == "" {
						clusterModel := &aimv1alpha2.AIMClusterModel{ObjectMeta: model.ObjectMeta, Spec: model.Spec}
						clusterChild := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: child.ObjectMeta, Spec: child.Spec, Status: child.Status}
						for i := range clusterChild.OwnerReferences {
							clusterChild.OwnerReferences[i].Kind = "AIMClusterModel"
						}
						r := &ClusterModelReconciler{}
						obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: clusterModel}, ClusterModelFetchResult{
							model:                   clusterModel,
							existingClusterProfiles: controllerutils.FetchResult[[]managedProfile]{Error: tt.inventoryErr},
							childProfileSet:         controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfileSet]{Value: clusterChild},
						})
						health, current, prune = obs.componentHealth, obs.ProfileSetCurrent, obs.pruneSafe
					} else {
						r := &ModelReconciler{}
						obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}, ModelFetchResult{
							model:            model,
							existingProfiles: controllerutils.FetchResult[[]managedProfile]{Error: tt.inventoryErr},
							childProfileSet:  controllerutils.FetchResult[*aimv1alpha2.AIMProfileSet]{Value: child},
						})
						health, current, prune = obs.componentHealth, obs.ProfileSetCurrent, obs.pruneSafe
					}
					if len(health) != 1 || health[0].State != tt.want {
						t.Errorf("health = %+v, want %s", health, tt.want)
					}
					if tt.wantReason != "" && (len(health) != 1 || health[0].Reason != tt.wantReason) {
						t.Errorf("health = %+v, want reason %s", health, tt.wantReason)
					}
					if current != tt.current {
						t.Errorf("child current = %v, want %v", current, tt.current)
					}
					if prune != tt.prune {
						t.Errorf("prune safe = %v, want %v; availability must not relax replacement safety", prune, tt.prune)
					}
				})
			}
		})
	}
}
