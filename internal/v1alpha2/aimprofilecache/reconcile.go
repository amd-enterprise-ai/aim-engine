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

package aimprofilecache

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
)

const (
	artifactsComponentName      = "Artifacts"
	artifactsReadyConditionType = artifactsComponentName + "Ready"
)

type ProfileCacheReconciler struct {
	Scheme *runtime.Scheme
}

type ProfileCacheFetchResult struct {
	profileCache *aimv1alpha2.AIMProfileCache

	profile        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]
	clusterProfile controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]

	artifacts controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]
}

func (r *ProfileCacheReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache],
) ProfileCacheFetchResult {
	pc := reconcileCtx.Object

	result := ProfileCacheFetchResult{profileCache: pc}

	switch pc.Spec.ProfileScope {
	case aimv1alpha1.AIMResolutionScopeCluster:
		result.clusterProfile = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Name: pc.Spec.ProfileName,
		}, &aimv1alpha2.AIMClusterProfile{})
	case aimv1alpha1.AIMResolutionScopeNamespace:
		result.profile = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: pc.Namespace,
			Name:      pc.Spec.ProfileName,
		}, &aimv1alpha2.AIMProfile{})
	default:
		// Namespace first, then cluster fallback
		result.profile = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: pc.Namespace,
			Name:      pc.Spec.ProfileName,
		}, &aimv1alpha2.AIMProfile{})
		if result.profile.IsNotFound() {
			result.clusterProfile = controllerutils.Fetch(ctx, c, client.ObjectKey{
				Name: pc.Spec.ProfileName,
			}, &aimv1alpha2.AIMClusterProfile{})
		}
	}

	result.artifacts = controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMArtifactList{}, client.InNamespace(pc.Namespace))

	return result
}

// GetComponentHealth reports fetch-level health for the framework's status computation.
func (result ProfileCacheFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	var health []controllerutils.ComponentHealth

	// Profile is an upstream dependency
	if result.clusterProfile.Value != nil || result.clusterProfile.Error != nil {
		health = append(health, result.clusterProfile.ToUpstreamComponentHealth("Profile", func(p *aimv1alpha2.AIMClusterProfile) controllerutils.ComponentHealth {
			return getProfileHealth(&p.Status)
		}))
	} else if result.profile.Value != nil || result.profile.Error != nil {
		health = append(health, result.profile.ToUpstreamComponentHealth("Profile", func(p *aimv1alpha2.AIMProfile) controllerutils.ComponentHealth {
			return getProfileHealth(&p.Status)
		}))
	}

	// Artifact list fetch errors are reported here; actual readiness is in DecorateStatus.
	if !result.artifacts.OK() {
		health = append(health, result.artifacts.ToDownstreamComponentHealth(artifactsComponentName, func(_ *aimv1alpha1.AIMArtifactList) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{}
		}))
	}

	return health
}

// profileUnavailable reports whether the profile could not be read for a reason other
// than not existing, which leaves the cache's model sources unknown.
func (result ProfileCacheFetchResult) profileUnavailable() bool {
	return (result.profile.Error != nil && !result.profile.IsNotFound()) ||
		(result.clusterProfile.Error != nil && !result.clusterProfile.IsNotFound())
}

func getProfileHealth(status *aimv1alpha2.AIMProfileStatus) controllerutils.ComponentHealth {
	for _, cond := range status.GetConditions() {
		if cond.Type == controllerutils.ConditionTypeReady {
			var state constants.AIMStatus
			switch cond.Status {
			case metav1.ConditionTrue:
				state = constants.AIMStatusReady
			case metav1.ConditionFalse:
				if status.Status != "" {
					state = status.Status
				} else {
					state = constants.AIMStatusFailed
				}
			default:
				state = constants.AIMStatusProgressing
			}
			return controllerutils.ComponentHealth{
				State:   state,
				Reason:  cond.Reason,
				Message: cond.Message,
			}
		}
	}
	return controllerutils.ComponentHealth{
		State:   status.Status,
		Reason:  "ResourceFound",
		Message: "Resource found but no Ready condition present",
	}
}

// ProfileCacheObservation holds derived state from fetched data.
type ProfileCacheObservation struct {
	ProfileCacheFetchResult

	AllCachesAvailable bool
	MissingCaches      []aimv1alpha1.AIMModelSource
	BestArtifacts      map[string]aimv1alpha1.AIMArtifact

	// TerminatingArtifactNames lists artifacts this cache would have adopted but that
	// are being deleted. Held apart from both BestArtifacts and MissingCaches: their PVC
	// is about to be collected so they cannot back a Ready cache, and in Shared mode a
	// replacement hashes to the same name, so recreating one before the delete completes
	// would patch the dying object instead.
	TerminatingArtifactNames []string

	// Replacements holds, by model ID, the sources whose served artifact no longer
	// matches them, typically after their download filter changed. The served artifact
	// stays in BestArtifacts so services keep their volume, while the matching artifact
	// is created (the source is also in MissingCaches) and downloaded.
	Replacements map[string]artifactReplacement
}

// artifactReplacement pairs the artifact still serving a model source with the
// artifact that will replace it.
type artifactReplacement struct {
	serving aimv1alpha1.AIMArtifact
	// replacementName names the artifact being created or downloaded. Empty while
	// a previous replacement is still being deleted.
	replacementName string
	// replacement is nil until the replacement artifact reports a status.
	replacement *aimv1alpha1.AIMArtifact
	// terminatingNames lists matching artifacts still being deleted; the replacement
	// is created only once they are gone, because in Shared mode it reuses the name.
	terminatingNames []string
}

// GetComponentHealth overrides the embedded FetchResult's method to include artifact match health.
func (obs ProfileCacheObservation) GetComponentHealth() []controllerutils.ComponentHealth {
	health := obs.ProfileCacheFetchResult.GetComponentHealth()

	if len(obs.BestArtifacts) > 0 {
		worstStatus := constants.AIMStatusReady
		for _, a := range obs.BestArtifacts {
			if constants.CompareAIMStatus(a.Status.Status, worstStatus) < 0 {
				worstStatus = a.Status.Status
			}
		}
		health = append(health, controllerutils.ComponentHealth{
			Component:      artifactsComponentName,
			State:          worstStatus,
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	} else if len(obs.MissingCaches) > 0 || len(obs.TerminatingArtifactNames) > 0 {
		// Artifacts are being created, or are waiting for a delete to finish first
		health = append(health, controllerutils.ComponentHealth{
			Component:      artifactsComponentName,
			State:          constants.AIMStatusProgressing,
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	}

	return health
}

func (r *ProfileCacheReconciler) ComposeState(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache],
	fetch ProfileCacheFetchResult,
) ProfileCacheObservation {
	logger := log.FromContext(ctx)
	obs := ProfileCacheObservation{
		ProfileCacheFetchResult: fetch,
	}

	pc := fetch.profileCache

	var modelSources []aimv1alpha1.AIMModelSource
	if fetch.profile.OK() && fetch.profile.Value != nil {
		modelSources = fetch.profile.Value.Spec.ModelSources
	} else if fetch.clusterProfile.OK() && fetch.clusterProfile.Value != nil {
		modelSources = fetch.clusterProfile.Value.Spec.ModelSources
	} else {
		return obs
	}

	if len(modelSources) == 0 {
		return obs
	}
	if !fetch.artifacts.OK() {
		// Unknown artifact state is not evidence that caches are missing.
		// Fetch health reports the infrastructure error and retries.
		return obs
	}

	obs.BestArtifacts = map[string]aimv1alpha1.AIMArtifact{}
	obs.Replacements = map[string]artifactReplacement{}

	logger.V(1).Info("ComposeState: checking artifacts",
		"profileCache", pc.Name,
		"modelSources", len(modelSources),
		"fetchedArtifacts", len(fetch.artifacts.Value.Items))

	for _, model := range modelSources {
		match := matchArtifactForSource(pc, fetch.artifacts.Value.Items, model)
		serving, keepServing := servedArtifactPendingReplacement(pc, fetch.artifacts.Value.Items, model, match)
		switch {
		case keepServing:
			replacement := newArtifactReplacement(pc, model, serving, match)
			logger.V(1).Info("ComposeState: keeping served artifact until its replacement is ready",
				"modelID", model.ModelID,
				"servedArtifact", serving.Name,
				"replacementArtifact", replacement.replacementName,
				"terminatingArtifacts", match.terminatingNames)
			obs.BestArtifacts[model.ModelID] = serving
			obs.Replacements[model.ModelID] = replacement
			if !match.found && len(match.terminatingNames) == 0 {
				obs.MissingCaches = append(obs.MissingCaches, model)
			}
		case match.found:
			logger.V(1).Info("ComposeState: model source matched",
				"modelID", model.ModelID,
				"bestArtifact", match.best.Name,
				"bestStatus", match.best.Status.Status)
			obs.BestArtifacts[model.ModelID] = match.best
		case len(match.terminatingNames) > 0:
			logger.V(1).Info("ComposeState: model source served only by terminating artifacts",
				"modelID", model.ModelID,
				"terminatingArtifacts", match.terminatingNames)
			obs.TerminatingArtifactNames = append(obs.TerminatingArtifactNames, match.terminatingNames...)
		default:
			logger.V(1).Info("ComposeState: model source missing artifact", "modelID", model.ModelID)
			obs.MissingCaches = append(obs.MissingCaches, model)
		}
	}

	return obs
}

// artifactMatch is the outcome of resolving one model source against the artifacts in
// the namespace.
type artifactMatch struct {
	best  aimv1alpha1.AIMArtifact
	found bool

	// terminatingNames holds artifacts that serve the model source and would be
	// adoptable were they not being deleted.
	terminatingNames []string
}

// matchArtifactForSource picks the artifact this cache should resolve modelSource to:
// the adoptable candidate with the best status. A terminating candidate is reported
// separately rather than adopted or treated as missing - see TerminatingArtifactNames.
func matchArtifactForSource(
	profileCache *aimv1alpha2.AIMProfileCache,
	artifacts []aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
) artifactMatch {
	var match artifactMatch

	for _, cached := range artifacts {
		if !ArtifactAdoptableBy(profileCache, &cached) {
			continue
		}

		if cached.Spec.SourceURI != modelSource.SourceURI {
			continue
		}

		if !s3RuntimeConfigMatches(
			modelSource.SourceURI,
			cached.Spec.RuntimeConfigRef,
			profileCache.Spec.RuntimeConfigRef,
		) {
			continue
		}

		if !ArtifactFilterServes(cached.Spec.DownloadFilter, modelSource.DownloadFilter) {
			continue
		}

		// Must precede the empty-status guard: an artifact deleted before its first
		// status still has to be waited out, and an apply over a deletionTimestamp is
		// accepted silently, so classifying it as missing writes to the dying object.
		if cached.DeletionTimestamp != nil {
			match.terminatingNames = append(match.terminatingNames, cached.Name)
			continue
		}

		if cached.Status.Status == "" {
			continue
		}

		if !match.found || constants.CompareAIMStatus(match.best.Status.Status, cached.Status.Status) < 0 {
			match.found = true
			match.best = cached
		}
	}

	return match
}

// servedArtifactPendingReplacement returns the artifact to keep serving modelSource
// when no Ready artifact matches it yet.
func servedArtifactPendingReplacement(
	profileCache *aimv1alpha2.AIMProfileCache,
	artifacts []aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
	match artifactMatch,
) (aimv1alpha1.AIMArtifact, bool) {
	if match.found && match.best.Status.Status == constants.AIMStatusReady {
		return aimv1alpha1.AIMArtifact{}, false
	}
	return servedArtifact(profileCache, artifacts, modelSource)
}

// servedArtifact returns the artifact status.artifacts lists for modelSource when it
// can keep serving the source until a matching artifact is Ready. It must still be the
// same live, Ready object and pass every matchArtifactForSource check except the
// download filter. Adoptability matters beyond the match: the artifact watch only wakes
// this cache for artifacts it can adopt, so serving one it cannot would miss its deletion.
func servedArtifact(
	profileCache *aimv1alpha2.AIMProfileCache,
	artifacts []aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
) (aimv1alpha1.AIMArtifact, bool) {
	for _, resolved := range profileCache.Status.Artifacts {
		if resolved.Model != modelSource.ModelID {
			continue
		}
		for _, artifact := range artifacts {
			if artifact.Name != resolved.Name || string(artifact.UID) != resolved.UID {
				continue
			}
			servable := artifact.DeletionTimestamp == nil &&
				artifact.Status.Status == constants.AIMStatusReady &&
				artifact.Spec.SourceURI == modelSource.SourceURI &&
				ArtifactAdoptableBy(profileCache, &artifact) &&
				s3RuntimeConfigMatches(modelSource.SourceURI, artifact.Spec.RuntimeConfigRef, profileCache.Spec.RuntimeConfigRef)
			if !servable {
				return aimv1alpha1.AIMArtifact{}, false
			}
			return artifact, true
		}
	}
	return aimv1alpha1.AIMArtifact{}, false
}

// newArtifactReplacement records what the replacement for a served source is waiting
// on: the matching artifact if it already reports a status, a previous copy that is
// still being deleted, or otherwise the name the artifact will be created under.
func newArtifactReplacement(
	profileCache *aimv1alpha2.AIMProfileCache,
	modelSource aimv1alpha1.AIMModelSource,
	serving aimv1alpha1.AIMArtifact,
	match artifactMatch,
) artifactReplacement {
	replacement := artifactReplacement{serving: serving}
	switch {
	case match.found:
		best := match.best
		replacement.replacement = &best
		replacement.replacementName = best.Name
	case len(match.terminatingNames) > 0:
		replacement.terminatingNames = match.terminatingNames
	default:
		replacement.replacementName, _ = generateArtifactName(profileCache, modelSource)
	}
	return replacement
}

func (r *ProfileCacheReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileCache],
	obs ProfileCacheObservation,
) controllerutils.PlanResult {
	pc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	for idx, model := range obs.MissingCaches {
		artifactName, _ := generateArtifactName(pc, model)
		profileCacheLabelValue, _ := utils.SanitizeLabelValue(pc.Name)

		artifact := &aimv1alpha1.AIMArtifact{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMArtifact",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      artifactName,
				Namespace: pc.Namespace,
				Labels: map[string]string{
					constants.AimLabelDomain + "/profile.name":  pc.Spec.ProfileName,
					constants.AimLabelDomain + "/profile.scope": string(pc.Spec.ProfileScope),
					constants.AimLabelDomain + "/profile.index": strconv.Itoa(idx),
					constants.LabelProfileCacheName:             profileCacheLabelValue,
				},
			},
			Spec: aimv1alpha1.AIMArtifactSpec{
				StorageClassName: pc.Spec.StorageClassName,
				SourceURI:        model.SourceURI,
				ModelID:          model.ModelID,
				Size:             getSizeOrZero(model.Size),
				Env:              utils.MergeEnvVars(pc.Spec.Env, model.Env),
				RuntimeConfigRef: pc.Spec.RuntimeConfigRef,
				DownloadFilter:   model.DownloadFilter,
			},
		}

		// Persist the canonical form so caches declaring the same file set with
		// reordered or duplicated globs land on byte-identical immutable specs.
		if artifact.Spec.DownloadFilter != nil {
			filter := canonicalDownloadFilter(artifact.Spec.DownloadFilter)
			artifact.Spec.DownloadFilter = &filter
		}

		// Provision the shared adapter disk; size/class are left empty so the
		// AIMArtifact reconciler resolves them from AIMRuntimeConfig.Storage.
		if pc.Spec.RequiresAdapterDisk {
			artifact.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}
		}

		if pc.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated {
			result.Apply(artifact)
		} else {
			result.ApplyWithoutOwnerRef(artifact)
		}
	}

	return result
}

// DecorateStatus sets domain-specific status fields and conditions.
func (r *ProfileCacheReconciler) DecorateStatus(
	status *aimv1alpha2.AIMProfileCacheStatus,
	cm *controllerutils.ConditionManager,
	obs ProfileCacheObservation,
) {
	if obs.profileCache != nil {
		status.ObservedGeneration = obs.profileCache.Generation
	}
	if !obs.artifacts.OK() || obs.profileUnavailable() {
		// Keep the last known artifact/PVC projection while the list or the profile is
		// unavailable. Fetch health records the dependency failure. Clearing it would
		// also lose the served artifact an in-progress replacement still depends on.
		return
	}

	// Must run ahead of the early returns below: an artifact this cache advertised
	// until now has to stop being advertised, PVC included, the moment it starts
	// being deleted.
	status.Artifacts = buildResolvedArtifacts(obs.BestArtifacts)
	decorateArtifactReplacements(cm, obs)

	// Reported ahead of the missing artifacts it will turn into: "waiting for a delete"
	// is the part an operator can act on.
	if len(obs.TerminatingArtifactNames) > 0 {
		cm.MarkFalse(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonArtifactTerminating,
			"Waiting for AIM artifacts to finish deleting before recreating them: "+strings.Join(obs.TerminatingArtifactNames, ", "))
		return
	}
	unserved := slices.ContainsFunc(obs.MissingCaches, func(model aimv1alpha1.AIMModelSource) bool {
		_, served := obs.Replacements[model.ModelID]
		return !served
	})
	if unserved {
		cm.MarkFalse(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonCreatingCaches, "Waiting for AIM artifacts to be created")
		return
	}
	if len(obs.BestArtifacts) > 0 {
		var statusValues []constants.AIMStatus
		for _, a := range obs.BestArtifacts {
			statusValues = append(statusValues, a.Status.Status)
		}
		worstStatus := slices.MinFunc(statusValues, constants.CompareAIMStatus)

		if worstStatus == constants.AIMStatusReady {
			cm.MarkTrue(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonAllCachesReady, "All artifacts are ready")
		} else {
			var worstArtifact *aimv1alpha1.AIMArtifact
			for _, a := range obs.BestArtifacts {
				if a.Status.Status == worstStatus {
					copy := a
					worstArtifact = &copy
					break
				}
			}

			if worstArtifact != nil {
				for _, cond := range worstArtifact.Status.Conditions {
					if cond.Type == controllerutils.ConditionTypeReady {
						cm.MarkFalse(artifactsReadyConditionType, cond.Reason, "One or more artifacts are not ready: "+cond.Message)
						break
					}
				}
			}

			if cm.Get(artifactsReadyConditionType) == nil {
				cm.MarkFalse(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonCachesNotReady, "One or more artifacts are not ready")
			}
		}
	} else {
		cm.MarkFalse(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonNoCaches, "No artifacts to track", controllerutils.AsError())
	}
}

// decorateArtifactReplacements reports replacement downloads on their own condition so
// they never gate Ready, and records each completed switch with an event naming both
// artifacts. The served artifact is not deleted on switch: in Shared mode other caches
// may still list it, so reclaiming it is left to the operator.
func decorateArtifactReplacements(cm *controllerutils.ConditionManager, obs ProfileCacheObservation) {
	conditionType := aimv1alpha2.AIMProfileCacheConditionArtifactsReplacing

	if len(obs.Replacements) > 0 {
		var failed, pending []string
		for _, modelID := range slices.Sorted(maps.Keys(obs.Replacements)) {
			replacement := obs.Replacements[modelID]
			served := replacement.serving.Name
			switch {
			case len(replacement.terminatingNames) > 0:
				pending = append(pending, fmt.Sprintf("%s: serving %s while %s finishes deleting",
					modelID, served, strings.Join(replacement.terminatingNames, ", ")))
			case replacement.replacement == nil:
				pending = append(pending, fmt.Sprintf("%s: serving %s while %s is created",
					modelID, served, replacement.replacementName))
			case replacement.replacement.Status.Status == constants.AIMStatusFailed,
				replacement.replacement.Status.Status == constants.AIMStatusDegraded:
				failed = append(failed, fmt.Sprintf("%s: serving %s because %s is %s: %s",
					modelID, served, replacement.replacementName, replacement.replacement.Status.Status,
					readyConditionMessage(replacement.replacement)))
			default:
				pending = append(pending, fmt.Sprintf("%s: serving %s while %s is %s",
					modelID, served, replacement.replacementName, replacement.replacement.Status.Status))
			}
		}
		if len(failed) > 0 {
			cm.MarkTrue(conditionType, aimv1alpha2.AIMProfileCacheReasonReplacementFailed,
				strings.Join(append(failed, pending...), "; "), controllerutils.AsWarning())
			return
		}
		cm.MarkTrue(conditionType, aimv1alpha2.AIMProfileCacheReasonReplacingArtifacts,
			strings.Join(pending, "; "), controllerutils.AsInfo())
		return
	}

	if switched := switchedArtifacts(obs); len(switched) > 0 {
		// Recurring because a second switch can arrive while the condition already holds
		// this reason, which is not a transition; the switch is detected only once.
		cm.MarkFalse(conditionType, aimv1alpha2.AIMProfileCacheReasonArtifactsReplaced,
			strings.Join(switched, "; "), controllerutils.AsInfo(), controllerutils.WithRecurring())
		return
	}

	if condition := cm.Get(conditionType); condition != nil && condition.Status == metav1.ConditionTrue {
		cm.Delete(conditionType)
	}
}

// switchedArtifacts describes each model source that moved to a Ready artifact other
// than the one the previous status.artifacts listed for it. A move to an artifact that
// is not Ready is not a completed replacement; ArtifactsReady reports it instead.
func switchedArtifacts(obs ProfileCacheObservation) []string {
	if obs.profileCache == nil {
		return nil
	}
	var switched []string
	for _, modelID := range slices.Sorted(maps.Keys(obs.BestArtifacts)) {
		current := obs.BestArtifacts[modelID]
		if current.Status.Status != constants.AIMStatusReady {
			continue
		}
		for _, previous := range obs.profileCache.Status.Artifacts {
			if previous.Model == modelID && previous.Name != current.Name {
				switched = append(switched, fmt.Sprintf("%s: switched from %s to %s", modelID, previous.Name, current.Name))
			}
		}
	}
	return switched
}

func readyConditionMessage(artifact *aimv1alpha1.AIMArtifact) string {
	for _, condition := range artifact.Status.Conditions {
		if condition.Type == controllerutils.ConditionTypeReady {
			return condition.Message
		}
	}
	return string(artifact.Status.Status)
}

// buildResolvedArtifacts renders the resolved artifacts for status.artifacts, keyed by
// artifact name. Returns nil when nothing is resolved, so the field is cleared rather
// than left holding a previous reconcile's artifacts.
func buildResolvedArtifacts(bestArtifacts map[string]aimv1alpha1.AIMArtifact) map[string]aimv1alpha1.AIMResolvedArtifact {
	if len(bestArtifacts) == 0 {
		return nil
	}

	resolved := make(map[string]aimv1alpha1.AIMResolvedArtifact, len(bestArtifacts))
	for modelName, artifact := range bestArtifacts {
		resolved[artifact.Name] = aimv1alpha1.AIMResolvedArtifact{
			UID:                   string(artifact.UID),
			Name:                  artifact.Name,
			Model:                 modelName,
			Status:                artifact.Status.Status,
			PersistentVolumeClaim: artifact.Status.PersistentVolumeClaim,
		}
	}
	return resolved
}

// generateArtifactName returns a deterministic artifact name.
// Shared caches omit the profile cache name to allow cross-cache reuse.
// Dedicated caches scope names to the profile cache.
func generateArtifactName(pc *aimv1alpha2.AIMProfileCache, modelSource aimv1alpha1.AIMModelSource) (string, error) {
	nameWithoutDots := strings.ReplaceAll(modelSource.SourceURI, ".", "-")
	hashInputs := []any{
		modelSource.SourceURI,
		utils.MergeEnvVars(pc.Spec.Env, modelSource.Env),
		pc.Spec.StorageClassName,
	}
	if strings.HasPrefix(modelSource.SourceURI, "s3://") {
		hashInputs = append(hashInputs, normalizedRuntimeConfigName(pc.Spec.RuntimeConfigRef))
	}
	if modelSource.DownloadFilter != nil {
		hashInputs = append(hashInputs, canonicalDownloadFilter(modelSource.DownloadFilter))
	}

	if pc.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated {
		hashInputs = append(hashInputs, "dedicated", pc.Name)
	}

	return utils.GenerateDerivedName(
		[]string{nameWithoutDots},
		utils.WithHashSource(hashInputs...),
		utils.WithHashLength(10),
	)
}

// ArtifactAdoptableBy reports whether profileCache is allowed to use artifact, judged
// only on the cache's mode, adapter-disk requirement and storage class. Callers that
// resolve a specific model source must also compare SourceURI; the artifact watch
// cannot, because the model sources live on the (separately fetched) profile.
//
// Deliberately ignores deletionTimestamp. The artifact watch filters events through
// this predicate, so rejecting a terminating artifact here would drop the very wakeups
// that tell a cache to stop advertising it. That rejection is matchArtifactForSource's
// job instead.
func ArtifactAdoptableBy(profileCache *aimv1alpha2.AIMProfileCache, artifact *aimv1alpha1.AIMArtifact) bool {
	// Enforce mode isolation:
	// - Shared profile caches can use only shared artifacts (no owner refs)
	// - Dedicated profile caches can use only artifacts owned by this profile cache
	switch profileCache.Spec.Mode {
	case aimv1alpha2.ProfileCacheModeShared:
		if len(artifact.GetOwnerReferences()) > 0 {
			return false
		}
	case aimv1alpha2.ProfileCacheModeDedicated:
		if !hasOwnerReferenceUID(artifact.GetOwnerReferences(), profileCache.UID) {
			return false
		}
	}

	// LoRA-serving caches must only adopt a disk-bearing artifact; a diskless match
	// would wedge the service. Falling through to MissingCaches makes PlanResources
	// create one with a disk.
	if profileCache.Spec.RequiresAdapterDisk && artifact.Spec.AdapterDisk == nil {
		return false
	}

	return profileCache.Spec.StorageClassName == "" ||
		profileCache.Spec.StorageClassName == artifact.Spec.StorageClassName
}

func s3RuntimeConfigMatches(
	sourceURI string,
	left aimv1alpha1.RuntimeConfigRef,
	right aimv1alpha1.RuntimeConfigRef,
) bool {
	return !strings.HasPrefix(sourceURI, "s3://") ||
		normalizedRuntimeConfigName(left) == normalizedRuntimeConfigName(right)
}

// ArtifactFilterServes reports whether an artifact downloaded with artifactFilter
// holds the files a source declaring sourceFilter needs. An artifact holding the
// whole repository serves any source, which keeps a pre-created complete copy in
// use. Otherwise the filters must select the same file set: a nil filter (runtime
// config or default) and an empty one are deliberately distinct.
func ArtifactFilterServes(artifactFilter, sourceFilter *aimv1alpha1.AIMDownloadFilter) bool {
	if artifactFilter == nil {
		return sourceFilter == nil
	}
	if len(artifactFilter.Include) == 0 && len(artifactFilter.Exclude) == 0 {
		return true
	}
	if sourceFilter == nil {
		return false
	}
	a, s := canonicalDownloadFilter(artifactFilter), canonicalDownloadFilter(sourceFilter)
	return slices.Equal(a.Include, s.Include) && slices.Equal(a.Exclude, s.Exclude)
}

// canonicalDownloadFilter sorts and deduplicates the globs so that filters selecting
// the same file set hash to the same artifact name regardless of declaration order.
func canonicalDownloadFilter(filter *aimv1alpha1.AIMDownloadFilter) aimv1alpha1.AIMDownloadFilter {
	return aimv1alpha1.AIMDownloadFilter{
		Include: canonicalGlobs(filter.Include),
		Exclude: canonicalGlobs(filter.Exclude),
	}
}

func canonicalGlobs(globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	sorted := slices.Clone(globs)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

func normalizedRuntimeConfigName(ref aimv1alpha1.RuntimeConfigRef) string {
	if ref.Name == "" {
		return aimruntimeconfig.DefaultRuntimeConfigName
	}
	return ref.Name
}

func hasOwnerReferenceUID(ownerRefs []metav1.OwnerReference, ownerUID types.UID) bool {
	for _, ref := range ownerRefs {
		if ref.UID == ownerUID {
			return true
		}
	}
	return false
}

func getSizeOrZero(size *resource.Quantity) resource.Quantity {
	if size == nil {
		return resource.Quantity{}
	}
	return *size
}
