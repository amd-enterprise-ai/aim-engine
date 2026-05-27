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
	"errors"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

const (
	annotationProfileSetUID       = constants.AimLabelDomain + "/profile-set-uid"
	annotationProfileSetName      = constants.AimLabelDomain + "/profile-set-name"
	annotationProfileSetNamespace = constants.AimLabelDomain + "/profile-set-namespace"
	ManagedProfileSetUIDIndexKey  = ".metadata.annotations." + annotationProfileSetUID

	componentDerivation = "Derivation"
)

func AnnotationProfileSetName() string {
	return annotationProfileSetName
}

func AnnotationProfileSetUID() string {
	return annotationProfileSetUID
}

func AnnotationProfileSetNamespace() string {
	return annotationProfileSetNamespace
}

type ProfileSetReconciler struct {
	Scheme *runtime.Scheme
}

type ClusterProfileSetReconciler struct {
	Scheme *runtime.Scheme
}

type managedProfile struct {
	Object client.Object
	Status aimv1alpha2.AIMProfileStatus
}

type desiredProfile struct {
	Object client.Object
}

type ProfileSetFetchResult struct {
	set        *aimv1alpha2.AIMProfileSet
	candidates controllerutils.FetchResult[[]aimprofile.ProfileCopyCandidate]
	managed    controllerutils.FetchResult[[]managedProfile]
}

type ClusterProfileSetFetchResult struct {
	set        *aimv1alpha2.AIMClusterProfileSet
	candidates controllerutils.FetchResult[[]aimprofile.ProfileCopyCandidate]
	managed    controllerutils.FetchResult[[]managedProfile]
}

type ProfileSetObservation struct {
	desiredProfiles      []desiredProfile
	existingManaged      []managedProfile
	managedProfiles      aimv1alpha1.ManagedProfileCounts
	existingDesiredCount int32
	pruneSafe            bool
	buildErr             error
	componentHealth      []controllerutils.ComponentHealth
}

func (obs ProfileSetObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	return obs.componentHealth
}

type ClusterProfileSetObservation struct {
	desiredProfiles      []desiredProfile
	existingManaged      []managedProfile
	managedProfiles      aimv1alpha1.ManagedProfileCounts
	existingDesiredCount int32
	pruneSafe            bool
	buildErr             error
	componentHealth      []controllerutils.ComponentHealth
}

func (obs ClusterProfileSetObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	return obs.componentHealth
}

func (r *ProfileSetReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet],
) ProfileSetFetchResult {
	set := reconcileCtx.Object
	candidates, candidateErr := loadNamespaceCandidates(ctx, c, set)
	managed, managedErr := listManagedNamespaceProfiles(ctx, c, set.Namespace, string(set.UID))
	return ProfileSetFetchResult{
		set:        set,
		candidates: controllerutils.FetchResult[[]aimprofile.ProfileCopyCandidate]{Value: candidates, Error: candidateErr},
		managed:    controllerutils.FetchResult[[]managedProfile]{Value: managed, Error: managedErr},
	}
}

func (r *ClusterProfileSetReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet],
) ClusterProfileSetFetchResult {
	set := reconcileCtx.Object
	candidates, candidateErr := loadClusterCandidates(ctx, c, set)
	managed, managedErr := listManagedClusterProfiles(ctx, c, string(set.UID))
	return ClusterProfileSetFetchResult{
		set:        set,
		candidates: controllerutils.FetchResult[[]aimprofile.ProfileCopyCandidate]{Value: candidates, Error: candidateErr},
		managed:    controllerutils.FetchResult[[]managedProfile]{Value: managed, Error: managedErr},
	}
}

func (r *ProfileSetReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet],
	fetch ProfileSetFetchResult,
) ProfileSetObservation {
	obs := ProfileSetObservation{}
	if fetch.candidates.Error == nil {
		obs.desiredProfiles, obs.buildErr = buildDesiredNamespaceProfiles(fetch.set, fetch.candidates.Value)
	}
	if fetch.managed.Error == nil {
		obs.existingManaged = fetch.managed.Value
		obs.managedProfiles, obs.existingDesiredCount = summarizeManagedProfiles(obs.desiredProfiles, fetch.managed.Value)
	}
	obs.pruneSafe = fetch.candidates.Error == nil && fetch.managed.Error == nil && obs.buildErr == nil
	obs.componentHealth = buildComponentHealth(collectErrors(fetch.candidates.Error, fetch.managed.Error), obs.buildErr, obs.managedProfiles, obs.existingDesiredCount)
	return obs
}

func (r *ClusterProfileSetReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet],
	fetch ClusterProfileSetFetchResult,
) ClusterProfileSetObservation {
	obs := ClusterProfileSetObservation{}
	if fetch.candidates.Error == nil {
		obs.desiredProfiles, obs.buildErr = buildDesiredClusterProfiles(fetch.set, fetch.candidates.Value)
	}
	if fetch.managed.Error == nil {
		obs.existingManaged = fetch.managed.Value
		obs.managedProfiles, obs.existingDesiredCount = summarizeManagedProfiles(obs.desiredProfiles, fetch.managed.Value)
	}
	obs.pruneSafe = fetch.candidates.Error == nil && fetch.managed.Error == nil && obs.buildErr == nil
	obs.componentHealth = buildComponentHealth(collectErrors(fetch.candidates.Error, fetch.managed.Error), obs.buildErr, obs.managedProfiles, obs.existingDesiredCount)
	return obs
}

func (r *ProfileSetReconciler) PlanResources(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet],
	obs ProfileSetObservation,
) controllerutils.PlanResult {
	var plan controllerutils.PlanResult
	if obs.buildErr != nil {
		return plan
	}
	for _, desired := range obs.desiredProfiles {
		plan.Apply(desired.Object)
	}
	if !obs.pruneSafe {
		return plan
	}
	desiredNames := desiredProfileNames(obs.desiredProfiles)
	for _, existingProfile := range obs.existingManaged {
		if _, keep := desiredNames[client.ObjectKeyFromObject(existingProfile.Object).String()]; !keep {
			plan.Delete(existingProfile.Object)
		}
	}
	return plan
}

func (r *ClusterProfileSetReconciler) PlanResources(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet],
	obs ClusterProfileSetObservation,
) controllerutils.PlanResult {
	var plan controllerutils.PlanResult
	if obs.buildErr != nil {
		return plan
	}
	for _, desired := range obs.desiredProfiles {
		plan.Apply(desired.Object)
	}
	if !obs.pruneSafe {
		return plan
	}
	desiredNames := desiredProfileNames(obs.desiredProfiles)
	for _, existingProfile := range obs.existingManaged {
		if _, keep := desiredNames[client.ObjectKeyFromObject(existingProfile.Object).String()]; !keep {
			plan.Delete(existingProfile.Object)
		}
	}
	return plan
}

func (r *ProfileSetReconciler) DecorateStatus(
	status *aimv1alpha1.AIMProfileSetStatus,
	_ *controllerutils.ConditionManager,
	obs ProfileSetObservation,
) {
	status.ManagedProfiles = obs.managedProfiles
}

func (r *ClusterProfileSetReconciler) DecorateStatus(
	status *aimv1alpha1.AIMProfileSetStatus,
	_ *controllerutils.ConditionManager,
	obs ClusterProfileSetObservation,
) {
	status.ManagedProfiles = obs.managedProfiles
}

func buildDesiredNamespaceProfiles(set *aimv1alpha2.AIMProfileSet, candidates []aimprofile.ProfileCopyCandidate) ([]desiredProfile, error) {
	req := aimprofile.ProfileCopyRequest{
		Selector:      set.Spec.Selector,
		VersionPolicy: normalizedVersionPolicy(set.Spec.VersionPolicy),
		Version:       set.Spec.Version,
		Overrides:     set.Spec.Overrides,
		ImageOverride: set.Spec.Image,
	}
	matched, err := aimprofile.FilterProfileCopyCandidates(candidates, req)
	if err != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}
	desired := make([]desiredProfile, 0, len(matched))
	for _, source := range matched {
		name, err := utils.GenerateDerivedName(
			[]string{set.Name, firstNonEmpty(source.Spec.ProfileId, source.Name)},
			utils.WithHashSource(set.Name, candidateIdentity(source)),
		)
		if err != nil {
			return nil, fmt.Errorf("generate derived profile name: %w", err)
		}
		copiedSpec, err := aimprofile.ApplyProfileCopyOverrides(source.Spec, set.Spec.Overrides, set.Spec.Image, source.BaseImage)
		if err != nil {
			return nil, err
		}
		profile := &aimv1alpha2.AIMProfile{}
		profile.Namespace = set.Namespace
		profile.Name = name
		profile.Annotations = aimprofile.MarkProfileSource(map[string]string{
			annotationProfileSetUID:       string(set.UID),
			annotationProfileSetName:      set.Name,
			annotationProfileSetNamespace: set.Namespace,
		}, aimprofile.ProfileSourceCopy)
		profile.Spec = aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: copiedSpec}
		profile.Spec.ImagePullSecrets = inheritedPullSecrets(set.Spec, source.Spec)
		profile.Spec.ServiceAccountName = inheritedServiceAccount(set.Spec, source.Spec)
		desired = append(desired, desiredProfile{Object: profile})
	}
	return desired, nil
}

func buildDesiredClusterProfiles(set *aimv1alpha2.AIMClusterProfileSet, candidates []aimprofile.ProfileCopyCandidate) ([]desiredProfile, error) {
	req := aimprofile.ProfileCopyRequest{
		Selector:      set.Spec.Selector,
		VersionPolicy: normalizedVersionPolicy(set.Spec.VersionPolicy),
		Version:       set.Spec.Version,
		Overrides:     set.Spec.Overrides,
		ImageOverride: set.Spec.Image,
	}
	matched, err := aimprofile.FilterProfileCopyCandidates(candidates, req)
	if err != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}
	desired := make([]desiredProfile, 0, len(matched))
	for _, source := range matched {
		name, err := utils.GenerateDerivedName(
			[]string{set.Name, firstNonEmpty(source.Spec.ProfileId, source.Name)},
			utils.WithHashSource(set.Name, candidateIdentity(source)),
		)
		if err != nil {
			return nil, fmt.Errorf("generate derived profile name: %w", err)
		}
		copiedSpec, err := aimprofile.ApplyProfileCopyOverrides(source.Spec, set.Spec.Overrides, set.Spec.Image, source.BaseImage)
		if err != nil {
			return nil, err
		}
		profile := &aimv1alpha2.AIMClusterProfile{}
		profile.Name = name
		profile.Annotations = aimprofile.MarkProfileSource(map[string]string{
			annotationProfileSetUID:  string(set.UID),
			annotationProfileSetName: set.Name,
		}, aimprofile.ProfileSourceCopy)
		profile.Spec = aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: copiedSpec}
		profile.Spec.ImagePullSecrets = inheritedPullSecrets(set.Spec, source.Spec)
		profile.Spec.ServiceAccountName = inheritedServiceAccount(set.Spec, source.Spec)
		desired = append(desired, desiredProfile{Object: profile})
	}
	return desired, nil
}

func normalizedVersionPolicy(policy aimv1alpha1.ProfileVersionPolicy) aimv1alpha1.ProfileVersionPolicy {
	if policy == "" {
		return aimv1alpha1.ProfileVersionPolicyPinned
	}
	return policy
}

func inheritedPullSecrets(spec aimv1alpha1.AIMProfileSetSpec, source aimv1alpha2.AIMProfileSpecCommon) []corev1.LocalObjectReference {
	if len(spec.ImagePullSecrets) > 0 {
		out := make([]corev1.LocalObjectReference, len(spec.ImagePullSecrets))
		copy(out, spec.ImagePullSecrets)
		return out
	}
	out := make([]corev1.LocalObjectReference, len(source.ImagePullSecrets))
	copy(out, source.ImagePullSecrets)
	return out
}

func inheritedServiceAccount(spec aimv1alpha1.AIMProfileSetSpec, source aimv1alpha2.AIMProfileSpecCommon) string {
	if spec.ServiceAccountName != "" {
		return spec.ServiceAccountName
	}
	return source.ServiceAccountName
}

func summarizeManagedProfiles(desired []desiredProfile, existing []managedProfile) (aimv1alpha1.ManagedProfileCounts, int32) {
	result := aimv1alpha1.ManagedProfileCounts{Total: int32(len(desired))}
	for _, d := range desired {
		if spec, ok := profileSpecCommon(d.Object); ok && aimprofile.IsProfileDeployable(spec) {
			result.Deployable++
		} else {
			result.Base++
		}
	}
	if len(desired) == 0 || len(existing) == 0 {
		return result, 0
	}
	existingByKey := make(map[string]managedProfile, len(existing))
	for _, profile := range existing {
		existingByKey[client.ObjectKeyFromObject(profile.Object).String()] = profile
	}
	var existingDesiredCount int32
	for _, desired := range desired {
		key := client.ObjectKeyFromObject(desired.Object).String()
		profile, found := existingByKey[key]
		if !found {
			continue
		}
		existingDesiredCount++
		switch profile.Status.Status {
		case constants.AIMStatusReady:
			result.Ready++
		case constants.AIMStatusNotAvailable:
			result.NotAvailable++
		}
	}
	return result, existingDesiredCount
}

// profileSpecCommon extracts the AIMProfileSpecCommon embedded in either an
// AIMProfile or AIMClusterProfile. The boolean is false for unrecognised
// kinds; counting code treats those as base profiles.
func profileSpecCommon(obj client.Object) (aimv1alpha2.AIMProfileSpecCommon, bool) {
	switch p := obj.(type) {
	case *aimv1alpha2.AIMProfile:
		return p.Spec.AIMProfileSpecCommon, true
	case *aimv1alpha2.AIMClusterProfile:
		return p.Spec.AIMProfileSpecCommon, true
	default:
		return aimv1alpha2.AIMProfileSpecCommon{}, false
	}
}

func buildComponentHealth(fetchErrs []error, buildErr error, managed aimv1alpha1.ManagedProfileCounts, existingDesiredCount int32) []controllerutils.ComponentHealth {
	if len(fetchErrs) > 0 {
		return []controllerutils.ComponentHealth{{
			Component:      componentDerivation,
			State:          constants.AIMStatusDegraded,
			Reason:         "SourceLoadFailed",
			Message:        errors.Join(fetchErrs...).Error(),
			Errors:         fetchErrs,
			DependencyType: controllerutils.DependencyTypeUpstream,
		}}
	}
	if buildErr != nil {
		return []controllerutils.ComponentHealth{{
			Component:      componentDerivation,
			State:          constants.AIMStatusDegraded,
			Reason:         "DerivationFailed",
			Message:        buildErr.Error(),
			Errors:         []error{buildErr},
			DependencyType: controllerutils.DependencyTypeUpstream,
		}}
	}
	if managed.Total == 0 {
		return []controllerutils.ComponentHealth{{
			Component: componentDerivation,
			State:     constants.AIMStatusNotAvailable,
			Reason:    "NoMatchingProfiles",
			Message:   "No matching source profiles found for derivation",
		}}
	}
	if existingDesiredCount < managed.Total {
		return []controllerutils.ComponentHealth{{
			Component:      componentDerivation,
			State:          constants.AIMStatusProgressing,
			Reason:         constants.ReasonCreating,
			Message:        "Derived profiles are still being created",
			DependencyType: controllerutils.DependencyTypeDownstream,
		}}
	}
	if managed.Ready == managed.Total {
		return []controllerutils.ComponentHealth{{
			Component: componentDerivation,
			State:     constants.AIMStatusReady,
			Reason:    "ManagedProfilesReady",
		}}
	}
	if managed.Ready > 0 {
		return []controllerutils.ComponentHealth{{
			Component: componentDerivation,
			State:     constants.AIMStatusDegraded,
			Reason:    "ManagedProfilesPartiallyReady",
			Message:   "Some derived profiles are ready, but others are not available",
		}}
	}
	return []controllerutils.ComponentHealth{{
		Component: componentDerivation,
		State:     constants.AIMStatusNotAvailable,
		Reason:    "ManagedProfilesNotAvailable",
		Message:   "Derived profiles exist but none are ready",
	}}
}

func loadNamespaceCandidates(ctx context.Context, c client.Client, set *aimv1alpha2.AIMProfileSet) ([]aimprofile.ProfileCopyCandidate, error) {
	if set.Spec.SourceRef != nil {
		_, catalog, err := aimprofile.LoadDiscoveryCatalog(ctx, c, *set.Spec.SourceRef, set.Namespace)
		if err != nil {
			return nil, err
		}
		return catalog.ToCandidates(), nil
	}
	provenance, scope, err := aimprofile.ProvenanceLabelSelector(set.Spec.Selector)
	if err != nil {
		return nil, err
	}
	// selector.aimId is always a strict source-side filter now. CEL on
	// AIMProfileSetSpec / AIMModelProfilesSpec rejects selector.aimId
	// when selector.role=base, so base-role selections never reach this
	// path with a non-empty aimId — target identity for derived profiles
	// is supplied via overrides.aimId (see ApplyProfileCopyOverrides).
	aimIDFilter := set.Spec.Selector.AimId
	// Selector.modelRef.scope==Cluster skips namespace AIMProfiles entirely.
	if scope == aimprofile.SelectorScopeCluster {
		return listClusterProfileCandidatesWithLabels(ctx, c, aimIDFilter, provenance)
	}
	namespaceCandidates, err := listNamespaceProfileCandidatesWithLabels(ctx, c, set.Namespace, aimIDFilter, provenance)
	if err != nil {
		return nil, err
	}
	// Selector.modelRef.scope==Namespace already restricts to namespace
	// profiles via the source-model-scope label, so no cluster fallback is
	// needed there. SelectorScopeAny (the default) falls back to cluster
	// profiles when no namespace match was found.
	if scope == aimprofile.SelectorScopeNamespace || len(namespaceCandidates) > 0 {
		return namespaceCandidates, nil
	}
	return listClusterProfileCandidatesWithLabels(ctx, c, aimIDFilter, provenance)
}

func loadClusterCandidates(ctx context.Context, c client.Client, set *aimv1alpha2.AIMClusterProfileSet) ([]aimprofile.ProfileCopyCandidate, error) {
	if set.Spec.SourceRef != nil {
		namespace := constants.GetOperatorNamespace()
		_, catalog, err := aimprofile.LoadDiscoveryCatalog(ctx, c, *set.Spec.SourceRef, namespace)
		if err != nil {
			return nil, fmt.Errorf("load sourceRef %q from operator namespace %q: %w", set.Spec.SourceRef.Name, namespace, err)
		}
		return catalog.ToCandidates(), nil
	}
	provenance, _, err := aimprofile.ProvenanceLabelSelector(set.Spec.Selector)
	if err != nil {
		return nil, err
	}
	// See loadNamespaceCandidates: Base-role selectors target aimId-less
	// base profiles; the index filter on selector.AimId would exclude them.
	aimIDFilter := set.Spec.Selector.AimId
	if set.Spec.Selector.Role == aimv1alpha1.ProfileSelectorRoleBase {
		aimIDFilter = ""
	}
	return listClusterProfileCandidatesWithLabels(ctx, c, aimIDFilter, provenance)
}

func listClusterProfileCandidatesWithLabels(ctx context.Context, c client.Client, aimID string, provenance labels.Selector) ([]aimprofile.ProfileCopyCandidate, error) {
	var list aimv1alpha2.AIMClusterProfileList
	opts := []client.ListOption{}
	if aimID != "" {
		opts = append(opts, client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: aimID})
	}
	if provenance != nil && !provenance.Empty() {
		opts = append(opts, client.MatchingLabelsSelector{Selector: provenance})
	}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	out := make([]aimprofile.ProfileCopyCandidate, 0, len(list.Items))
	for i := range list.Items {
		if !aimprofile.IsProfileCopyable(list.Items[i].Annotations) {
			continue
		}
		out = append(out, aimprofile.ProfileCopyCandidate{
			Name:      list.Items[i].Name,
			Spec:      list.Items[i].Spec.AIMProfileSpecCommon,
			Status:    list.Items[i].Status,
			BaseImage: aimprofile.BaseImageFromProfile(&list.Items[i], list.Items[i].Status.BaseImage),
		})
	}
	return out, nil
}

func listNamespaceProfileCandidatesWithLabels(ctx context.Context, c client.Client, namespace, aimID string, provenance labels.Selector) ([]aimprofile.ProfileCopyCandidate, error) {
	var list aimv1alpha2.AIMProfileList
	opts := []client.ListOption{client.InNamespace(namespace)}
	if aimID != "" {
		opts = append(opts, client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: aimID})
	}
	if provenance != nil && !provenance.Empty() {
		opts = append(opts, client.MatchingLabelsSelector{Selector: provenance})
	}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	out := make([]aimprofile.ProfileCopyCandidate, 0, len(list.Items))
	for i := range list.Items {
		if !aimprofile.IsProfileCopyable(list.Items[i].Annotations) {
			continue
		}
		out = append(out, aimprofile.ProfileCopyCandidate{
			Name:      list.Items[i].Name,
			Spec:      list.Items[i].Spec.AIMProfileSpecCommon,
			Status:    list.Items[i].Status,
			BaseImage: aimprofile.BaseImageFromProfile(&list.Items[i], list.Items[i].Status.BaseImage),
		})
	}
	return out, nil
}

func mergeCandidates(clusterCandidates, namespaceCandidates []aimprofile.ProfileCopyCandidate) []aimprofile.ProfileCopyCandidate {
	merged := make(map[string]aimprofile.ProfileCopyCandidate, len(clusterCandidates)+len(namespaceCandidates))
	for _, candidate := range clusterCandidates {
		merged[candidateIdentity(candidate)] = candidate
	}
	for _, candidate := range namespaceCandidates {
		merged[candidateIdentity(candidate)] = candidate
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]aimprofile.ProfileCopyCandidate, 0, len(keys))
	for _, key := range keys {
		out = append(out, merged[key])
	}
	return out
}

func candidateIdentity(candidate aimprofile.ProfileCopyCandidate) string {
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%s|%s|%t|%t|%s|%s|%s|%d|%s|%s",
		candidate.Name,
		candidate.Spec.AimId,
		candidate.Spec.ModelId,
		candidate.Spec.ProfileId,
		candidate.Spec.Engine,
		candidate.Spec.Metric,
		candidate.Spec.Type,
		candidate.Spec.Primary,
		candidate.Spec.ManualSelectionOnly,
		candidate.Spec.AcceleratorModel,
		candidate.Spec.AcceleratorType,
		candidate.Spec.Precision,
		candidate.Spec.AcceleratorCount,
		candidate.Status.Version,
		engineArgsIdentity(candidate.Spec.EngineArgs),
	)
}

func engineArgsIdentity(value *apiextensionsv1.JSON) string {
	if value == nil {
		return ""
	}
	return string(value.Raw)
}

func listManagedNamespaceProfiles(ctx context.Context, c client.Client, namespace, profileSetUID string) ([]managedProfile, error) {
	var list aimv1alpha2.AIMProfileList
	if err := c.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingFields{ManagedProfileSetUIDIndexKey: profileSetUID},
	); err != nil {
		return nil, err
	}
	out := make([]managedProfile, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, managedProfile{
			Object: list.Items[i].DeepCopy(),
			Status: list.Items[i].Status,
		})
	}
	return out, nil
}

func listManagedClusterProfiles(ctx context.Context, c client.Client, profileSetUID string) ([]managedProfile, error) {
	var list aimv1alpha2.AIMClusterProfileList
	if err := c.List(ctx, &list, client.MatchingFields{ManagedProfileSetUIDIndexKey: profileSetUID}); err != nil {
		return nil, err
	}
	out := make([]managedProfile, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, managedProfile{
			Object: list.Items[i].DeepCopy(),
			Status: list.Items[i].Status,
		})
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func desiredProfileNames(desired []desiredProfile) map[string]struct{} {
	names := make(map[string]struct{}, len(desired))
	for _, profile := range desired {
		names[client.ObjectKeyFromObject(profile.Object).String()] = struct{}{}
	}
	return names
}

func collectErrors(values ...error) []error {
	result := make([]error, 0, len(values))
	for _, err := range values {
		if err != nil {
			result = append(result, err)
		}
	}
	return result
}
