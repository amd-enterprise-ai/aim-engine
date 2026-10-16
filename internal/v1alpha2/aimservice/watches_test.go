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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

// TestProfileRelevantChangePredicate_StatusTransition is the baseline case:
// readiness flip must enqueue the service even if Generation is unchanged.
func TestProfileRelevantChangePredicate_StatusTransition(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status:     aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusPending},
	}
	newP := oldP.DeepCopy()
	newP.Status.Status = constants.AIMStatusReady

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected status transition Pending->Ready to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_ResourcesChange covers the regression we
// just fixed: when the profile controller recomputes Status.Resources without
// a Generation bump (e.g. node-label-driven accelerator swap), the AIMService
// must still pick up the new container resources.
func TestProfileRelevantChangePredicate_ResourcesChange(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status: constants.AIMStatusReady,
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
	}
	newP := oldP.DeepCopy()
	newP.Status.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected Status.Resources change to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_AffinityChange is the companion case:
// the resolved node affinity can change without a Generation bump when node
// labels shift. The AIMService must re-reconcile so the ISVC's affinity
// stays aligned.
func TestProfileRelevantChangePredicate_AffinityChange(t *testing.T) {
	pred := profileRelevantChangePredicate()

	aff := func(label string) *corev1.NodeAffinity {
		return &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: label, Operator: corev1.NodeSelectorOpExists,
					}},
				}},
			},
		}
	}

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:               constants.AIMStatusReady,
			ResolvedNodeAffinity: aff("feature.node.kubernetes.io/aim-accelerator.MI300X"),
		},
	}
	newP := oldP.DeepCopy()
	newP.Status.ResolvedNodeAffinity = aff("feature.node.kubernetes.io/aim-accelerator.MI325X")

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected Status.ResolvedNodeAffinity change to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_NoiseFilteredOut ensures cosmetic status
// writes don't flood the AIMService with reconciles. This is what stops the
// predicate from being the equivalent of "always fire".
func TestProfileRelevantChangePredicate_NoiseFilteredOut(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:             constants.AIMStatusReady,
			ObservedGeneration: 5,
		},
	}
	newP := oldP.DeepCopy()

	if pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected no-op status update to be filtered out")
	}
}

// TestProfileRelevantChangePredicate_ProvenanceLabelsFire covers the
// PR 3 addition: a profile whose role label flips from base to deployable
// (a base profile becoming materialised in iteration 2) must trigger a
// re-reconcile so selector-driven services pick it up.
func TestProfileRelevantChangePredicate_ProvenanceLabelsFire(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", Labels: map[string]string{
			constants.LabelKeyProfileRole: constants.LabelValueProfileRoleBase,
		}},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	newP := oldP.DeepCopy()
	newP.Labels[constants.LabelKeyProfileRole] = constants.LabelValueProfileRoleDeployable

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected role label flip to fire the predicate")
	}
}

func TestProfileRelevantChangePredicate_YAMLContractAnnotationChange(t *testing.T) {
	pred := profileRelevantChangePredicate()

	base := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status:     aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	backfilled := base.DeepCopy()
	backfilled.Annotations = map[string]string{
		profileyaml.AnnotationContract: profileyaml.DefaultContract().Encode(),
	}
	corrected := backfilled.DeepCopy()
	corrected.Annotations[profileyaml.AnnotationContract] = `{"codec":"aim-profile/v1","metadataFields":["engine","gpu","gpu_count","metric","precision","type"]}`

	for _, tc := range []struct {
		name string
		old  *aimv1alpha2.AIMProfile
		new  *aimv1alpha2.AIMProfile
	}{
		{name: "backfill", old: base, new: backfilled},
		{name: "correction", old: backfilled, new: corrected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !pred.Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new}) {
				t.Fatal("expected profile YAML contract annotation change to fire the predicate")
			}
		})
	}
}

// newWatchTestScheme builds a scheme with both AIM versions registered for
// the watch fan-out tests.
func newWatchTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha1: %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha2: %v", err)
	}
	return scheme
}

// newWatchTestClient is a fake client with the AIMService and AIMArtifact field
// indexes used by the watch mappers.
func newWatchTestClient(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&aimv1alpha2.AIMService{}, aimv1alpha1.AIMServiceProfileIndexKey, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Name == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Name}
		}).
		WithIndex(&aimv1alpha2.AIMService{}, ServiceModelNameIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok || svc.Spec.Model == nil || svc.Spec.Model.Name == nil || *svc.Spec.Model.Name == "" {
				return nil
			}
			return []string{*svc.Spec.Model.Name}
		}).
		WithIndex(&aimv1alpha2.AIMService{}, ServiceSelectorModelRefIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil ||
				svc.Spec.Profile.Selector.ModelRef == nil || svc.Spec.Profile.Selector.ModelRef.Name == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Selector.ModelRef.Name}
		}).
		WithIndex(&aimv1alpha2.AIMService{}, ServiceSelectorAimIdIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil ||
				svc.Spec.Profile.Selector.AimId == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Selector.AimId}
		}).
		WithIndex(&aimv1alpha2.AIMService{}, ServiceResourceNodeMatchIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok || svc.Spec.Resources == nil || !usesProfilePipeline(svc) {
				return nil
			}
			return []string{serviceResourceNodeMatchIndexValue}
		}).
		WithIndex(&aimv1alpha2.AIMService{}, aimv1alpha1.AIMServiceAdapterArtifactIndexKey, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha2.AIMService)
			if !ok {
				return nil
			}
			names := make([]string, 0, len(svc.Spec.Adapters))
			for _, adapter := range svc.Spec.Adapters {
				if adapter.Name != "" {
					names = append(names, adapter.Name)
				}
			}
			return names
		}).
		WithIndex(&aimv1alpha2.AIMService{}, ServiceResolvedProfileIndex, indexServiceResolvedProfile).
		WithIndex(&aimv1alpha1.AIMArtifact{}, aimv1alpha1.ArtifactParentIndexKey, func(obj client.Object) []string {
			artifact, ok := obj.(*aimv1alpha1.AIMArtifact)
			if !ok || artifact.Spec.ParentArtifact == "" {
				return nil
			}
			return []string{artifact.Spec.ParentArtifact}
		}).
		WithIndex(&aimv1alpha1.AIMArtifact{}, aimv1alpha1.ArtifactCompatibleModelIDIndexKey, func(obj client.Object) []string {
			artifact, ok := obj.(*aimv1alpha1.AIMArtifact)
			if !ok {
				return nil
			}
			return artifact.Spec.CompatibleWith
		}).
		Build()
}

func TestServiceResourceNodeChangePredicate(t *testing.T) {
	pred := serviceResourceNodeChangePredicate()
	base := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node",
			Labels: map[string]string{
				"feature.node.kubernetes.io/aim-accelerator.MI300X": "1",
				"kubernetes.io/hostname":                            "node",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("24Gi"),
			},
		},
	}

	if !pred.Create(event.CreateEvent{Object: base}) {
		t.Fatal("Node creation must trigger service resource matching")
	}
	if !pred.Delete(event.DeleteEvent{Object: base}) {
		t.Fatal("Node deletion must trigger service resource matching")
	}

	t.Run("accelerator label change", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Labels["feature.node.kubernetes.io/aim-accelerator.MI300X"] = "2"
		if !pred.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}) {
			t.Fatal("accelerator label change must trigger")
		}
	})

	t.Run("memory allocatable change", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Status.Allocatable[corev1.ResourceMemory] = resource.MustParse("32Gi")
		if !pred.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}) {
			t.Fatal("allocatable resource change must trigger")
		}
	})

	t.Run("unrelated label change", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Labels["kubernetes.io/hostname"] = "renamed"
		if pred.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}) {
			t.Fatal("unrelated label change must be filtered")
		}
	})
}

func TestFindServicesForResourceNodeChange(t *testing.T) {
	scheme := newWatchTestScheme(t)
	resources := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("16Gi")},
	}

	profileService := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "profile", Namespace: "alpha"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:   &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			Resources: resources,
		},
	}
	forcedProfileService := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "forced-profile",
			Namespace: "beta",
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model:     &aimv1alpha1.AIMServiceModel{Name: ptr.To("model")},
			Resources: resources,
		},
	}
	noOverride := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "no-override", Namespace: "alpha"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
		},
	}
	templateService := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "template", Namespace: "alpha"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Template:  &aimv1alpha1.AIMServiceTemplateConfig{Name: "template"},
			Resources: resources,
		},
	}
	forcedTemplateService := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "forced-template",
			Namespace: "alpha",
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model:     &aimv1alpha1.AIMServiceModel{Name: ptr.To("model")},
			Profile:   &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			Resources: resources,
		},
	}
	grandfatheredProfileService := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "grandfathered-profile",
			Namespace: "alpha",
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: "historical",
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:   &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			Resources: resources,
		},
	}

	c := newWatchTestClient(
		t,
		scheme,
		profileService,
		forcedProfileService,
		noOverride,
		templateService,
		forcedTemplateService,
		grandfatheredProfileService,
	)
	requests := findServicesForResourceNodeChange(c)(
		context.Background(),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}},
	)

	got := map[types.NamespacedName]struct{}{}
	for _, request := range requests {
		got[request.NamespacedName] = struct{}{}
	}
	for _, want := range []types.NamespacedName{
		{Name: "profile", Namespace: "alpha"},
		{Name: "forced-profile", Namespace: "beta"},
		{Name: "grandfathered-profile", Namespace: "alpha"},
	} {
		if _, exists := got[want]; !exists {
			t.Errorf("missing Node-event enqueue for %s", want)
		}
	}
	if len(got) != 3 {
		t.Fatalf("Node event enqueued unexpected services: %#v", got)
	}
}

// TestFindServicesForProfile_FansOutByNameModelAndAimId exercises the three
// lookup paths a single AIMProfile event takes:
//  1. profile name (services authored as spec.profile.name=foo)
//  2. source-model label (services authored as spec.model.name=foo OR
//     spec.profile.selector.modelRef.name=foo)
//  3. spec.aimId (services authored as spec.profile.selector.aimId=foo)
//
// All three must enqueue together so a single event can't leave any
// AIMService stale.
func TestFindServicesForProfile_FansOutByNameModelAndAimId(t *testing.T) {
	scheme := newWatchTestScheme(t)

	byName := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-name", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
		},
	}
	byModelShortcut := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-by-model",
			Namespace: "ns",
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("source-model")},
		},
	}
	bySelectorModelRef := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-modelref", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "source-model"},
				},
			},
		},
	}
	bySelectorAimId := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-aimid", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}
	// Service in a different namespace must NOT be enqueued — namespace
	// AIMProfile events fan out within the namespace only.
	otherNamespace := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-other-ns", Namespace: "other"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
		},
	}
	c := newWatchTestClient(t, scheme, byName, byModelShortcut, bySelectorModelRef, bySelectorAimId, otherNamespace)

	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
			Labels: map[string]string{
				constants.LabelKeySourceModel: "source-model",
			},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId: "aim/one",
			Image: "img",
		}},
	}

	requests := findServicesForProfile(c)(context.Background(), profile)
	got := map[string]struct{}{}
	for _, r := range requests {
		got[r.String()] = struct{}{}
	}

	expected := []types.NamespacedName{
		{Name: "svc-by-name", Namespace: "ns"},
		{Name: "svc-by-model", Namespace: "ns"},
		{Name: "svc-by-modelref", Namespace: "ns"},
		{Name: "svc-by-aimid", Namespace: "ns"},
	}
	for _, want := range expected {
		if _, ok := got[want.String()]; !ok {
			t.Errorf("missing enqueue for %s", want)
		}
	}
	if _, ok := got["other/svc-other-ns"]; ok {
		t.Errorf("namespace fan-out leaked into other namespace")
	}
}

// TestFindServicesForClusterProfile_SpansAllNamespaces asserts that a
// cluster profile event reaches services in every namespace via the field
// indexes (no InNamespace clause).
func TestFindServicesForClusterProfile_SpansAllNamespaces(t *testing.T) {
	scheme := newWatchTestScheme(t)

	nsAlpha := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-alpha", Namespace: "alpha"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "cluster-p"},
		},
	}
	nsBeta := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-beta", Namespace: "beta"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "cluster-p"},
		},
	}
	c := newWatchTestClient(t, scheme, nsAlpha, nsBeta)

	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-p"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId: "aim/global",
			Image: "img",
		}},
	}

	requests := findServicesForClusterProfile(c)(context.Background(), clusterProfile)
	if len(requests) != 2 {
		t.Fatalf("expected 2 enqueues (alpha + beta), got %d", len(requests))
	}
}

// TestFindServicesForModel_FansOutByShortcutAndSelector covers the new
// AIMModel watch: a model change enqueues services authored via
// spec.model.name=foo AND services authored via
// spec.profile.selector.modelRef.name=foo, since both styles need the
// downstream profile set to be re-evaluated.
func TestFindServicesForModel_FansOutByShortcutAndSelector(t *testing.T) {
	scheme := newWatchTestScheme(t)

	byShortcut := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-shortcut",
			Namespace: "ns",
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("my-model")},
		},
	}
	byExplicit := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-explicit", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "my-model"},
				},
			},
		},
	}
	unrelated := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-unrelated", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("other-model")},
		},
	}
	inactiveTemplate := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-template", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("my-model")},
		},
	}
	c := newWatchTestClient(t, scheme, byShortcut, byExplicit, unrelated, inactiveTemplate)

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "ns"},
	}
	requests := findServicesForModel(c)(context.Background(), model)
	got := map[string]struct{}{}
	for _, r := range requests {
		got[r.Name] = struct{}{}
	}

	if _, ok := got["svc-shortcut"]; !ok {
		t.Errorf("missing enqueue for spec.model.name shortcut service")
	}
	if _, ok := got["svc-explicit"]; !ok {
		t.Errorf("missing enqueue for selector.modelRef.name service")
	}
	if _, ok := got["svc-unrelated"]; ok {
		t.Errorf("unrelated service must not be enqueued")
	}
	if _, ok := got["svc-template"]; ok {
		t.Errorf("profile watch must not enqueue a template-pipeline service")
	}
}

func TestFindServicesForAdapterArtifact_FansOutByExactAndLogicalCompatibility(t *testing.T) {
	scheme := newWatchTestScheme(t)

	serviceFor := func(name, adapterName, namespace string) *aimv1alpha2.AIMService {
		return &aimv1alpha2.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: aimv1alpha1.AIMServiceSpec{
				Adapters: []aimv1alpha1.AIMServiceAdapterReference{{
					Name: adapterName,
					Kind: aimv1alpha1.AdapterKindAIMArtifact,
				}},
			},
		}
	}

	exact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "exact-adapter", Namespace: "ns"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			ParentArtifact: "base-artifact",
		},
	}
	logical := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "logical-adapter", Namespace: "ns"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			CompatibleWith: []string{"org/other", "org/base"},
		},
	}
	unrelated := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "unrelated-adapter", Namespace: "ns"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			CompatibleWith: []string{"org/unrelated"},
		},
	}

	c := newWatchTestClient(
		t,
		scheme,
		exact,
		logical,
		unrelated,
		serviceFor("svc-exact", exact.Name, "ns"),
		serviceFor("svc-logical", logical.Name, "ns"),
		serviceFor("svc-unrelated", unrelated.Name, "ns"),
		serviceFor("svc-other-namespace", logical.Name, "other"),
	)

	model := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base-artifact", Namespace: "ns"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:    aimv1alpha1.ArtifactTypeModel,
			ModelID: "org/base",
		},
	}
	requests := findServicesForAdapterArtifact(c)(context.Background(), model)
	got := map[types.NamespacedName]struct{}{}
	for _, request := range requests {
		got[request.NamespacedName] = struct{}{}
	}

	for _, want := range []types.NamespacedName{
		{Namespace: "ns", Name: "svc-exact"},
		{Namespace: "ns", Name: "svc-logical"},
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing enqueue for %s", want)
		}
	}
	for _, notWanted := range []types.NamespacedName{
		{Namespace: "ns", Name: "svc-unrelated"},
		{Namespace: "other", Name: "svc-other-namespace"},
	} {
		if _, ok := got[notWanted]; ok {
			t.Errorf("unexpected enqueue for %s", notWanted)
		}
	}
}

// TestAdapterArtifactRelevantChangePredicate_RankChange covers the gap that let
// a mutable spec.rank edit go unnoticed: rank feeds the resolved
// AIM_ADAPTER_MAX_RANK for static services, so a change must enqueue consumers
// rather than waiting for an unrelated event to requeue them.
func TestAdapterArtifactRelevantChangePredicate_RankChange(t *testing.T) {
	pred := adapterArtifactRelevantChangePredicate()

	oldA := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "lora-a", Namespace: "ns"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type: aimv1alpha1.ArtifactTypeAdapter,
			Rank: ptr.To[int32](16),
		},
	}

	t.Run("rank raised", func(t *testing.T) {
		newA := oldA.DeepCopy()
		newA.Spec.Rank = ptr.To[int32](64)
		if !pred.Update(event.UpdateEvent{ObjectOld: oldA, ObjectNew: newA}) {
			t.Error("expected a rank change to fire the predicate")
		}
	})

	t.Run("rank cleared", func(t *testing.T) {
		newA := oldA.DeepCopy()
		newA.Spec.Rank = nil
		if !pred.Update(event.UpdateEvent{ObjectOld: oldA, ObjectNew: newA}) {
			t.Error("expected clearing rank to fire the predicate")
		}
	})

	t.Run("rank unchanged is still filtered", func(t *testing.T) {
		newA := oldA.DeepCopy()
		newA.Spec.Rank = ptr.To[int32](16)
		newA.ResourceVersion = "2"
		if pred.Update(event.UpdateEvent{ObjectOld: oldA, ObjectNew: newA}) {
			t.Error("an equal rank must not fire the predicate")
		}
	})
}

// imageShapeService builds a service that resolves its model by image:
// spec.model.image plus a selector without modelRef or aimId. None of the
// spec-based indexes match it, so only the resolved-profile index can route
// profile events to it.
func imageShapeService(name, namespace, profile string, scope aimv1alpha1.AIMResolutionScope) *aimv1alpha2.AIMService {
	return &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To("docker.io/amdenterpriseai/aim-dummy:0.1")},
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AcceleratorCount: ptr.To[int32](1)},
			},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ResolvedProfile: &aimv1alpha1.AIMResolvedReference{Name: profile, Scope: scope},
		},
	}
}

func requestSet(requests []reconcile.Request) map[string]struct{} {
	got := map[string]struct{}{}
	for _, r := range requests {
		got[r.String()] = struct{}{}
	}
	return got
}

func assertRequests(t *testing.T, requests []reconcile.Request, want ...string) {
	t.Helper()
	got := requestSet(requests)
	if len(got) != len(want) {
		t.Errorf("expected requests %v, got %v", want, got)
		return
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("expected requests %v, got %v", want, got)
			return
		}
	}
}

func TestFindServicesForProfileCache_FansOutByResolvedProfile(t *testing.T) {
	scheme := newWatchTestScheme(t)
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
			Labels:    map[string]string{constants.LabelKeySourceModel: "svc-a-auto-1234"},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{Image: "img"}},
	}
	cache := &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{Name: "p-cache", Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:  "p",
			ProfileScope: aimv1alpha1.AIMResolutionScopeNamespace,
		},
	}

	t.Run("dedicated cache reaches image-shape service", func(t *testing.T) {
		c := newWatchTestClient(t, scheme, profile,
			imageShapeService("svc-a", "ns", "p", aimv1alpha1.AIMResolutionScopeNamespace))
		assertRequests(t, findServicesForProfileCache(c)(context.Background(), cache), "ns/svc-a")
	})

	t.Run("same name in other scope does not match", func(t *testing.T) {
		c := newWatchTestClient(t, scheme, profile,
			imageShapeService("svc-a", "ns", "p", aimv1alpha1.AIMResolutionScopeCluster))
		assertRequests(t, findServicesForProfileCache(c)(context.Background(), cache))
	})

	t.Run("shared cache reaches every resolved service", func(t *testing.T) {
		c := newWatchTestClient(t, scheme, profile,
			imageShapeService("svc-a", "ns", "p", aimv1alpha1.AIMResolutionScopeNamespace),
			imageShapeService("svc-b", "ns", "p", aimv1alpha1.AIMResolutionScopeNamespace))
		assertRequests(t, findServicesForProfileCache(c)(context.Background(), cache), "ns/svc-a", "ns/svc-b")
	})
}

func TestFindServicesForProfile_FansOutByResolvedProfile(t *testing.T) {
	scheme := newWatchTestScheme(t)
	c := newWatchTestClient(t, scheme,
		imageShapeService("svc-a", "ns", "p", aimv1alpha1.AIMResolutionScopeNamespace))
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{Image: "img"}},
	}
	assertRequests(t, findServicesForProfile(c)(context.Background(), profile), "ns/svc-a")
}

func TestFindServicesForClusterProfile_FansOutByResolvedProfile(t *testing.T) {
	scheme := newWatchTestScheme(t)
	c := newWatchTestClient(t, scheme,
		imageShapeService("svc-alpha", "alpha", "cluster-p", aimv1alpha1.AIMResolutionScopeCluster),
		imageShapeService("svc-beta", "beta", "cluster-p", aimv1alpha1.AIMResolutionScopeCluster),
		imageShapeService("svc-ns-scoped", "alpha", "cluster-p", aimv1alpha1.AIMResolutionScopeNamespace))
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-p"},
		Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{Image: "img"}},
	}

	assertRequests(t, findServicesForClusterProfile(c)(context.Background(), clusterProfile),
		"alpha/svc-alpha", "beta/svc-beta")

	cache := &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-p-cache", Namespace: "alpha"},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:  "cluster-p",
			ProfileScope: aimv1alpha1.AIMResolutionScopeCluster,
		},
	}
	assertRequests(t, findServicesForProfileCache(c)(context.Background(), cache), "alpha/svc-alpha")
}
