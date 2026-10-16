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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const (
	annotationProfileSetUID       = constants.AimLabelDomain + "/profile-set-uid"
	annotationProfileSetName      = constants.AimLabelDomain + "/profile-set-name"
	annotationProfileSetNamespace = constants.AimLabelDomain + "/profile-set-namespace"
	annotationProfileSetLineage   = constants.AimLabelDomain + "/profile-set-lineage"
	ManagedProfileSetUIDIndexKey  = ".metadata.annotations." + annotationProfileSetUID

	componentDerivation      = "Derivation"
	legacyCatalogNamingValue = "true"
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
	// APIReader is the uncached reader used to load a sourceRef catalog directly
	// from the API server.
	APIReader client.Reader
}

type ClusterProfileSetReconciler struct {
	Scheme *runtime.Scheme
	// APIReader is the uncached reader used to load a sourceRef catalog directly
	// from the API server.
	APIReader client.Reader
}

// catalogReader returns the reader used to load a sourceRef catalog ConfigMap.
// Falls back to the cached client when APIReader is unset (unit tests).
func catalogReader(apiReader client.Reader, cached client.Client) client.Reader {
	if apiReader != nil {
		return apiReader
	}
	return cached
}

type managedProfile struct {
	Object client.Object
	Status aimv1alpha2.AIMProfileStatus
}

type desiredProfile struct {
	Object client.Object
}

// profileSetCandidate keeps the profile resource metadata needed to resolve
// its YAML contract after selector and version-policy filtering. Catalog
// candidates already carry an inspected contract and are marked resolved.
type profileSetCandidate struct {
	aimprofile.ProfileCopyCandidate
	annotations        map[string]string
	labels             map[string]string
	sourceKind         string
	modelOwnerUID      string
	profileSetOwnerUID string
	contractResolved   bool
}

type ProfileSetFetchResult struct {
	set                  *aimv1alpha2.AIMProfileSet
	runtimeConfig        controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	candidates           controllerutils.FetchResult[[]profileSetCandidate]
	ineligibleCandidates []profileSetCandidate
	managed              controllerutils.FetchResult[[]managedProfile]
}

type ClusterProfileSetFetchResult struct {
	set                  *aimv1alpha2.AIMClusterProfileSet
	runtimeConfig        controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	candidates           controllerutils.FetchResult[[]profileSetCandidate]
	ineligibleCandidates []profileSetCandidate
	managed              controllerutils.FetchResult[[]managedProfile]
}

type ProfileSetObservation struct {
	desiredProfiles      []desiredProfile
	existingManaged      []managedProfile
	managedProfiles      aimv1alpha1.ManagedProfileCounts
	existingDesiredCount int32
	pruneSafe            bool
	buildErr             error
	ineligibleMatchCount int
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
	ineligibleMatchCount int
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
	candidatePools, candidateErr := loadNamespaceCandidatePools(ctx, c, catalogReader(r.APIReader, c), set)
	managed, managedErr := listManagedNamespaceProfiles(ctx, c, set.Namespace, string(set.UID))
	return ProfileSetFetchResult{
		set:                  set,
		runtimeConfig:        aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, aimruntimeconfig.DefaultRuntimeConfigName, set.Namespace),
		candidates:           controllerutils.FetchResult[[]profileSetCandidate]{Value: candidatePools.eligible, Error: candidateErr},
		ineligibleCandidates: candidatePools.excludedByCopyable,
		managed:              controllerutils.FetchResult[[]managedProfile]{Value: managed, Error: managedErr},
	}
}

func (r *ClusterProfileSetReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet],
) ClusterProfileSetFetchResult {
	set := reconcileCtx.Object
	candidatePools, candidateErr := loadClusterCandidatePools(ctx, c, catalogReader(r.APIReader, c), set)
	managed, managedErr := listManagedClusterProfiles(ctx, c, string(set.UID))
	return ClusterProfileSetFetchResult{
		set:                  set,
		runtimeConfig:        aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, aimruntimeconfig.DefaultRuntimeConfigName, ""),
		candidates:           controllerutils.FetchResult[[]profileSetCandidate]{Value: candidatePools.eligible, Error: candidateErr},
		ineligibleCandidates: candidatePools.excludedByCopyable,
		managed:              controllerutils.FetchResult[[]managedProfile]{Value: managed, Error: managedErr},
	}
}

func (r *ProfileSetReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet],
	fetch ProfileSetFetchResult,
) ProfileSetObservation {
	obs := ProfileSetObservation{}
	if fetch.runtimeConfig.Error == nil && fetch.candidates.Error == nil && fetch.managed.Error == nil {
		var selected []aimprofile.ProfileCopyCandidate
		req := profileCopyRequest(fetch.set.Spec)
		selected, obs.buildErr = resolveSelectedProfileSetCandidates(fetch.candidates.Value, req)
		var matchErr error
		obs.ineligibleMatchCount, matchErr = matchingProfileSetCandidateCount(fetch.ineligibleCandidates, req)
		obs.buildErr = errors.Join(obs.buildErr, matchErr)
		var buildErr error
		obs.desiredProfiles, buildErr = buildDesiredNamespaceProfiles(
			fetch.set,
			selected,
			aimruntimeconfig.ImageRegistryMappings(fetch.runtimeConfig.Value),
		)
		obs.buildErr = errors.Join(obs.buildErr, buildErr)
	}
	// ProfileSet output is atomic across source groups. If any selected source
	// cannot resolve its runtime lineage, retain every existing managed profile
	// until the producer metadata or RuntimeConfig is fixed.
	obs.pruneSafe = fetch.runtimeConfig.Error == nil && fetch.candidates.Error == nil && fetch.managed.Error == nil && obs.buildErr == nil
	if fetch.managed.Error == nil {
		obs.existingManaged = fetch.managed.Value
		obs.managedProfiles, obs.existingDesiredCount = summarizeManagedProfiles(obs.desiredProfiles, fetch.managed.Value)
	}
	obs.componentHealth = buildComponentHealth(collectErrors(fetch.runtimeConfig.Error, fetch.candidates.Error, fetch.managed.Error), obs.buildErr, obs.managedProfiles, obs.existingDesiredCount, obs.ineligibleMatchCount)
	return obs
}

func (r *ClusterProfileSetReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet],
	fetch ClusterProfileSetFetchResult,
) ClusterProfileSetObservation {
	obs := ClusterProfileSetObservation{}
	if fetch.runtimeConfig.Error == nil && fetch.candidates.Error == nil && fetch.managed.Error == nil {
		var selected []aimprofile.ProfileCopyCandidate
		req := profileCopyRequest(fetch.set.Spec)
		selected, obs.buildErr = resolveSelectedProfileSetCandidates(fetch.candidates.Value, req)
		var matchErr error
		obs.ineligibleMatchCount, matchErr = matchingProfileSetCandidateCount(fetch.ineligibleCandidates, req)
		obs.buildErr = errors.Join(obs.buildErr, matchErr)
		var buildErr error
		obs.desiredProfiles, buildErr = buildDesiredClusterProfiles(
			fetch.set,
			selected,
			aimruntimeconfig.ImageRegistryMappings(fetch.runtimeConfig.Value),
		)
		obs.buildErr = errors.Join(obs.buildErr, buildErr)
	}
	// ProfileSet output is atomic across source groups. If any selected source
	// cannot resolve its runtime lineage, retain every existing managed profile
	// until the producer metadata or RuntimeConfig is fixed.
	obs.pruneSafe = fetch.runtimeConfig.Error == nil && fetch.candidates.Error == nil && fetch.managed.Error == nil && obs.buildErr == nil
	if fetch.managed.Error == nil {
		obs.existingManaged = fetch.managed.Value
		obs.managedProfiles, obs.existingDesiredCount = summarizeManagedProfiles(obs.desiredProfiles, fetch.managed.Value)
	}
	obs.componentHealth = buildComponentHealth(collectErrors(fetch.runtimeConfig.Error, fetch.candidates.Error, fetch.managed.Error), obs.buildErr, obs.managedProfiles, obs.existingDesiredCount, obs.ineligibleMatchCount)
	return obs
}

func (r *ProfileSetReconciler) PlanResources(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet],
	obs ProfileSetObservation,
) controllerutils.PlanResult {
	var plan controllerutils.PlanResult
	if !obs.pruneSafe {
		return plan
	}
	for _, desired := range obs.desiredProfiles {
		plan.Apply(desired.Object)
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
	if !obs.pruneSafe {
		return plan
	}
	for _, desired := range obs.desiredProfiles {
		plan.Apply(desired.Object)
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

func buildDesiredNamespaceProfiles(
	set *aimv1alpha2.AIMProfileSet,
	candidates []aimprofile.ProfileCopyCandidate,
	registryMappings []aimimage.RegistryMapping,
) ([]desiredProfile, error) {
	req := profileCopyRequest(set.Spec)
	matched, err := aimprofile.FilterProfileCopyCandidates(candidates, req)
	if err != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}
	desired := make([]desiredProfile, 0, len(matched))
	failures := &sourceErrors{}
	for _, source := range matched {
		name, err := derivedProfileName(set.Name, source)
		if err != nil {
			failures.add(source.SourceGroup, fmt.Errorf("generate derived profile name: %w", err))
			continue
		}
		copiedSpec, err := aimprofile.ApplyProfileCopyOverrides(source.Spec, set.Spec.Overrides, set.Spec.Image, source.BaseImage, registryMappings)
		if err != nil {
			failures.add(source.SourceGroup, profileCopyError(err))
			continue
		}
		derivedBaseImage := aimprofile.ResolveDerivedProfileBaseImage(
			source.BaseImage,
			copiedSpec.Image,
			set.Spec.Image,
			set.Spec.Overrides,
			aimprofile.IsProfileDeployable(source.Spec),
		)
		copiedSpec.BaseImageRef = derivedBaseImage
		profile := &aimv1alpha2.AIMProfile{}
		profile.Namespace = set.Namespace
		profile.Name = name
		profile.Annotations = aimprofile.MarkProfileSource(map[string]string{
			annotationProfileSetUID:          string(set.UID),
			annotationProfileSetName:         set.Name,
			annotationProfileSetNamespace:    set.Namespace,
			annotationProfileSetLineage:      appendProfileSetLineage(source.ProfileSetUIDs, string(set.UID)),
			aimprofile.AnnotationSourceID:    source.SourceID,
			aimprofile.AnnotationSourceGroup: source.SourceGroup,
			aimprofile.AnnotationBaseImage:   derivedBaseImage,
		}, aimprofile.ProfileSourceCopy)
		profile.Annotations = profileyaml.Mark(profile.Annotations, source.YAMLContract)
		if set.Spec.OutputPolicy != nil && set.Spec.OutputPolicy.Copyable {
			profile.Annotations = aimprofile.MarkProfileCopyable(profile.Annotations, true)
		}
		if inheritsLegacyCatalogNaming(source, set.Spec) {
			profile.Annotations[aimprofile.AnnotationLegacyCatalogNaming] = legacyCatalogNamingValue
		}
		profile.Spec = aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: copiedSpec}
		profile.Spec.ImagePullSecrets = inheritedPullSecrets(set.Spec, source.Spec)
		profile.Spec.ServiceAccountName = inheritedServiceAccount(set.Spec, source.Spec)
		desired = append(desired, desiredProfile{Object: profile})
	}
	return desired, errors.Join(failures.err(), validateDerivedNames(desired))
}

func buildDesiredClusterProfiles(
	set *aimv1alpha2.AIMClusterProfileSet,
	candidates []aimprofile.ProfileCopyCandidate,
	registryMappings []aimimage.RegistryMapping,
) ([]desiredProfile, error) {
	req := profileCopyRequest(set.Spec)
	matched, err := aimprofile.FilterProfileCopyCandidates(candidates, req)
	if err != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}
	desired := make([]desiredProfile, 0, len(matched))
	failures := &sourceErrors{}
	for _, source := range matched {
		name, err := derivedProfileName(set.Name, source)
		if err != nil {
			failures.add(source.SourceGroup, fmt.Errorf("generate derived profile name: %w", err))
			continue
		}
		copiedSpec, err := aimprofile.ApplyProfileCopyOverrides(source.Spec, set.Spec.Overrides, set.Spec.Image, source.BaseImage, registryMappings)
		if err != nil {
			failures.add(source.SourceGroup, profileCopyError(err))
			continue
		}
		derivedBaseImage := aimprofile.ResolveDerivedProfileBaseImage(
			source.BaseImage,
			copiedSpec.Image,
			set.Spec.Image,
			set.Spec.Overrides,
			aimprofile.IsProfileDeployable(source.Spec),
		)
		copiedSpec.BaseImageRef = derivedBaseImage
		profile := &aimv1alpha2.AIMClusterProfile{}
		profile.Name = name
		profile.Annotations = aimprofile.MarkProfileSource(map[string]string{
			annotationProfileSetUID:          string(set.UID),
			annotationProfileSetName:         set.Name,
			annotationProfileSetLineage:      appendProfileSetLineage(source.ProfileSetUIDs, string(set.UID)),
			aimprofile.AnnotationSourceID:    source.SourceID,
			aimprofile.AnnotationSourceGroup: source.SourceGroup,
			aimprofile.AnnotationBaseImage:   derivedBaseImage,
		}, aimprofile.ProfileSourceCopy)
		profile.Annotations = profileyaml.Mark(profile.Annotations, source.YAMLContract)
		if set.Spec.OutputPolicy != nil && set.Spec.OutputPolicy.Copyable {
			profile.Annotations = aimprofile.MarkProfileCopyable(profile.Annotations, true)
		}
		if inheritsLegacyCatalogNaming(source, set.Spec) {
			profile.Annotations[aimprofile.AnnotationLegacyCatalogNaming] = legacyCatalogNamingValue
		}
		profile.Spec = aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: copiedSpec}
		profile.Spec.ImagePullSecrets = inheritedPullSecrets(set.Spec, source.Spec)
		profile.Spec.ServiceAccountName = inheritedServiceAccount(set.Spec, source.Spec)
		desired = append(desired, desiredProfile{Object: profile})
	}
	return desired, errors.Join(failures.err(), validateDerivedNames(desired))
}

func profileCopyError(err error) error {
	if aimprofile.IsBaseImageUnresolved(err) {
		return controllerutils.NewMissingUpstreamDependencyError(
			aimprofile.ReasonBaseImageUnresolved,
			err.Error(),
			err,
		)
	}
	return controllerutils.NewInvalidSpecError("InvalidProfileOverrides", err.Error(), err)
}

func appendProfileSetLineage(lineage []string, uid string) string {
	seen := make(map[string]bool, len(lineage)+1)
	ordered := make([]string, 0, len(lineage)+1)
	for _, ancestor := range lineage {
		ancestor = strings.TrimSpace(ancestor)
		if ancestor == "" || seen[ancestor] {
			continue
		}
		seen[ancestor] = true
		ordered = append(ordered, ancestor)
	}
	if uid != "" && !seen[uid] {
		ordered = append(ordered, uid)
	}
	return strings.Join(ordered, ",")
}

func validateDerivedNames(profiles []desiredProfile) error {
	seen := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		key := client.ObjectKeyFromObject(p.Object).String()
		if seen[key] {
			return controllerutils.NewInvalidSpecError("AmbiguousDerivedNames",
				fmt.Sprintf("multiple source definitions produce derived profile %q; narrow the selector", key), nil)
		}
		seen[key] = true
	}
	return nil
}

func inheritsLegacyCatalogNaming(source aimprofile.ProfileCopyCandidate, spec aimv1alpha1.AIMProfileSetSpec) bool {
	// Explicit vendor overrides were already modeled before catalog selection.
	// A downstream derivation must include those in its historical fingerprint.
	return source.LegacyCatalogNaming && (spec.Overrides == nil || spec.Overrides.AcceleratorVendor == "")
}

func derivedProfileName(setName string, source aimprofile.ProfileCopyCandidate) (string, error) {
	// The old catalog parser did not decode vendor. Keep that historical
	// fingerprint for catalog lineage while retaining vendor in actual specs.
	// Direct CRD sources were already vendor-aware and keep that behavior.
	if source.LegacyCatalogNaming {
		source.Spec.AcceleratorVendor = ""
	}
	return utils.GenerateDerivedName(
		[]string{setName, firstNonEmpty(source.Spec.ProfileId, source.Name)},
		utils.WithHashSource(setName, legacyNamingIdentity(source)),
	)
}

func profileCopyRequest(spec aimv1alpha1.AIMProfileSetSpec) aimprofile.ProfileCopyRequest {
	return aimprofile.ProfileCopyRequest{
		Selector:      spec.Selector,
		VersionPolicy: normalizedVersionPolicy(spec.VersionPolicy),
		Version:       spec.Version,
		Overrides:     spec.Overrides,
		ImageOverride: spec.Image,
	}
}

func resolveSelectedProfileSetCandidates(
	candidates []profileSetCandidate,
	req aimprofile.ProfileCopyRequest,
) ([]aimprofile.ProfileCopyCandidate, error) {
	plain := make([]aimprofile.ProfileCopyCandidate, len(candidates))
	for i := range candidates {
		plain[i] = candidates[i].ProfileCopyCandidate
	}

	selectedIndexes, err := aimprofile.FilterProfileCopyCandidateIndexes(plain, req)
	if err != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}

	resolved := make([]aimprofile.ProfileCopyCandidate, 0, len(selectedIndexes))
	failures := &sourceErrors{}
	for _, index := range selectedIndexes {
		candidate, err := candidates[index].resolved()
		if err != nil {
			failures.add(candidates[index].SourceGroup, err)
			continue
		}
		resolved = append(resolved, candidate)
	}
	return resolved, failures.err()
}

func matchingProfileSetCandidateCount(
	candidates []profileSetCandidate,
	req aimprofile.ProfileCopyRequest,
) (int, error) {
	plain := make([]aimprofile.ProfileCopyCandidate, len(candidates))
	for i := range candidates {
		plain[i] = candidates[i].ProfileCopyCandidate
	}
	matches, err := aimprofile.FilterProfileCopyCandidateIndexes(plain, req)
	if err != nil {
		return 0, controllerutils.NewInvalidSpecError("InvalidProfileSelection", err.Error(), err)
	}
	return len(matches), nil
}

func (candidate profileSetCandidate) resolved() (aimprofile.ProfileCopyCandidate, error) {
	if candidate.contractResolved {
		return candidate.ProfileCopyCandidate, nil
	}

	spec := candidate.Spec
	contract, err := profileyaml.ForProfile(
		candidate.annotations,
		profileyaml.EffectiveOrigin(candidate.labels, candidate.Status.Origin),
		&spec,
	)
	if err != nil {
		return aimprofile.ProfileCopyCandidate{}, fmt.Errorf(
			"parse profile YAML contract on %s %q: %w",
			candidate.sourceKind,
			candidate.Name,
			err,
		)
	}
	candidate.YAMLContract = contract
	return candidate.ProfileCopyCandidate, nil
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
		if !found || profile.Object.GetDeletionTimestamp() != nil {
			continue
		}
		desiredSpec, _ := profileSpecCommon(desired.Object)
		currentSpec, _ := profileSpecCommon(profile.Object)
		if !profileSpecsEqual(desiredSpec, currentSpec) ||
			profile.Status.ObservedGeneration != profile.Object.GetGeneration() ||
			desired.Object.GetAnnotations()[profileyaml.AnnotationContract] != profile.Object.GetAnnotations()[profileyaml.AnnotationContract] {
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

func profileSpecsEqual(left, right aimv1alpha2.AIMProfileSpecCommon) bool {
	// Compare the admitted wire representation: empty optional collections
	// disappear on serialization, and CRD defaults need not be explicit in
	// desired objects. Type has no default: unset and optimized are distinct.
	normalize := func(spec aimv1alpha2.AIMProfileSpecCommon) string {
		if spec.AutoSelectionPolicy == "" {
			spec.AutoSelectionPolicy = aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized
		}
		if spec.AcceleratorPartitioningMode == "" {
			spec.AcceleratorPartitioningMode = "unpartitioned"
		}
		raw, _ := json.Marshal(spec)
		return string(raw)
	}
	return normalize(left) == normalize(right)
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

func buildComponentHealth(fetchErrs []error, buildErr error, managed aimv1alpha1.ManagedProfileCounts, existingDesiredCount int32, ineligibleMatchCount int) []controllerutils.ComponentHealth {
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
		if ineligibleMatchCount > 0 {
			message := fmt.Sprintf("%d source profiles match the selection but are not published as copyable", ineligibleMatchCount)
			if ineligibleMatchCount == 1 {
				message = "1 source profile matches the selection but is not published as copyable"
			}
			message += "; annotate a hand-authored source with aim.eai.amd.com/profile-copyable=true, or set outputPolicy.copyable=true on its producing derivation"
			return []controllerutils.ComponentHealth{{
				Component: componentDerivation,
				State:     constants.AIMStatusNotAvailable,
				Reason:    "NoEligibleProfiles",
				Message:   message,
			}}
		}
		return []controllerutils.ComponentHealth{{
			Component: componentDerivation,
			State:     constants.AIMStatusNotAvailable,
			Reason:    "NoMatchingProfiles",
			Message:   "No matching source profiles found for derivation",
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
		// Profiles are alternatives: a current ready profile makes the set
		// available even while other hardware targets are unavailable or pending.
		return []controllerutils.ComponentHealth{{
			Component: componentDerivation,
			State:     constants.AIMStatusReady,
			Reason:    "ManagedProfilesPartiallyReady",
			Message:   fmt.Sprintf("%d of %d derived profiles are ready; inspect individual profiles for remaining alternatives", managed.Ready, managed.Total),
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
	return []controllerutils.ComponentHealth{{
		Component: componentDerivation,
		State:     constants.AIMStatusNotAvailable,
		Reason:    "ManagedProfilesNotAvailable",
		Message:   "Derived profiles exist but none are ready",
	}}
}

func loadNamespaceCandidates(ctx context.Context, c client.Client, reader client.Reader, set *aimv1alpha2.AIMProfileSet) ([]profileSetCandidate, error) {
	pools, err := loadNamespaceCandidatePools(ctx, c, reader, set)
	return pools.eligible, err
}

func loadNamespaceCandidatePools(ctx context.Context, c client.Client, reader client.Reader, set *aimv1alpha2.AIMProfileSet) (candidatePools, error) {
	if set.Spec.SourceRef != nil {
		candidates, err := loadExplicitCatalog(ctx, reader, set.Spec, set.Namespace)
		return candidatePools{eligible: candidates}, err
	}
	_, scope, err := aimprofile.ProvenanceLabelSelector(set.Spec.Selector)
	if err != nil {
		return candidatePools{}, err
	}
	// selector.aimId is always a strict source-side filter now. CEL on
	// AIMProfileSetSpec / AIMModelProfilesSpec rejects selector.aimId
	// when selector.role=base, so base-role selections never reach this
	// path with a non-empty aimId — target identity for derived profiles
	// is supplied via overrides.aimId (see ApplyProfileCopyOverrides).
	// Selector.modelRef.scope==Cluster skips namespace AIMProfiles entirely.
	if scope == aimprofile.SelectorScopeCluster {
		return loadDefaultScopePools(ctx, c, "", true, set.Spec, string(set.UID))
	}
	namespacePools, err := loadDefaultScopePools(ctx, c, set.Namespace, false, set.Spec, string(set.UID))
	if err != nil {
		return namespacePools, err
	}
	// Selector.modelRef.scope==Namespace already restricts to namespace
	// profiles via the source-model-scope label, so no cluster fallback is
	// needed there. SelectorScopeAny (the default) falls back to cluster
	// profiles when no namespace match was found.
	plain := make([]aimprofile.ProfileCopyCandidate, len(namespacePools.eligible))
	for i := range namespacePools.eligible {
		plain[i] = namespacePools.eligible[i].ProfileCopyCandidate
	}
	// Empty version policy is tolerated by direct unit callers; API admission
	// defaults and validates it before reconciliation.
	matches, err := aimprofile.FilterProfileCopyCandidateIndexes(plain, aimprofile.ProfileCopyRequest{Selector: set.Spec.Selector, VersionPolicy: set.Spec.VersionPolicy, Version: set.Spec.Version})
	if err != nil {
		return candidatePools{}, err
	}
	if scope == aimprofile.SelectorScopeNamespace || len(matches) > 0 {
		return namespacePools, nil
	}
	clusterPools, err := loadDefaultScopePools(ctx, c, "", true, set.Spec, string(set.UID))
	if err != nil {
		return clusterPools, err
	}
	clusterPools.excludedByCopyable = append(namespacePools.excludedByCopyable, clusterPools.excludedByCopyable...)
	return clusterPools, nil
}

func loadClusterCandidates(ctx context.Context, c client.Client, reader client.Reader, set *aimv1alpha2.AIMClusterProfileSet) ([]profileSetCandidate, error) {
	pools, err := loadClusterCandidatePools(ctx, c, reader, set)
	return pools.eligible, err
}

func loadClusterCandidatePools(ctx context.Context, c client.Client, reader client.Reader, set *aimv1alpha2.AIMClusterProfileSet) (candidatePools, error) {
	if set.Spec.SourceRef != nil {
		namespace := constants.GetOperatorNamespace()
		candidates, err := loadExplicitCatalog(ctx, reader, set.Spec, namespace)
		return candidatePools{eligible: candidates}, err
	}
	_, scope, err := aimprofile.ProvenanceLabelSelector(set.Spec.Selector)
	if err != nil {
		return candidatePools{}, err
	}
	// See loadNamespaceCandidates: Base-role selectors target aimId-less
	// base profiles; the index filter on selector.AimId would exclude them.
	if scope == aimprofile.SelectorScopeNamespace {
		return candidatePools{}, nil
	}
	return loadDefaultScopePools(ctx, c, "", true, set.Spec, string(set.UID))
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
	if candidate.SourceID != "" {
		return candidate.SourceID
	}
	return legacyNamingIdentity(candidate)
}

// legacyNamingIdentity is a compatibility contract, independent of the logical
// source ID used for selection. Keep its field list and encoding stable.
func legacyNamingIdentity(candidate aimprofile.ProfileCopyCandidate) string {
	// manualSelectionOnly is intentionally omitted: it is deprecated and no
	// longer affects selection, so two candidates differing only by it are
	// equivalent for dedup purposes.
	identity := fmt.Sprintf(
		"%s|%s|%s|%s|%s|%s|%s|%t|%s|%s|%s|%d|%s|%s",
		candidate.Name,
		candidate.Spec.AimId,
		candidate.Spec.ModelId,
		candidate.Spec.ProfileId,
		candidate.Spec.Engine,
		candidate.Spec.Metric,
		candidate.Spec.Type,
		candidate.Spec.Primary,
		candidate.Spec.AcceleratorModel,
		candidate.Spec.AcceleratorType,
		candidate.Spec.Precision,
		candidate.Spec.AcceleratorCount,
		candidate.Status.Version,
		engineArgsIdentity(candidate.Spec.EngineArgs),
	)
	// Preserve the exact pre-acceleratorVendor identity for legacy profiles so
	// existing derived profile names remain stable. Explicit vendor contracts
	// extend the identity and therefore cannot collide with each other.
	if candidate.Spec.AcceleratorVendor != "" {
		identity += "|vendor=" + string(candidate.Spec.AcceleratorVendor)
	}
	return identity
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
