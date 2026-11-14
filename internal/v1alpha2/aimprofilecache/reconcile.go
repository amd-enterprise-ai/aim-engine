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
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	// AdapterDiskUpgrades lists resolved artifacts (also in BestArtifacts) that lack
	// the adapter disk this cache requires. PlanResources adds the disk to each in
	// place, so the cache keeps advertising the artifact and its model PVC meanwhile.
	AdapterDiskUpgrades []aimv1alpha1.AIMArtifact
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

	logger.V(1).Info("ComposeState: checking artifacts",
		"profileCache", pc.Name,
		"modelSources", len(modelSources),
		"fetchedArtifacts", len(fetch.artifacts.Value.Items))

	for _, model := range modelSources {
		match := matchArtifactForSource(pc, fetch.artifacts.Value.Items, model)
		switch {
		case match.found:
			logger.V(1).Info("ComposeState: model source matched",
				"modelID", model.ModelID,
				"bestArtifact", match.best.Name,
				"bestStatus", match.best.Status.Status,
				"needsAdapterDisk", match.needsAdapterDisk)
			obs.BestArtifacts[model.ModelID] = match.best
			if match.needsAdapterDisk {
				obs.AdapterDiskUpgrades = append(obs.AdapterDiskUpgrades, match.best)
			}
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

	// needsAdapterDisk is set when best is only awaiting the adapter disk this
	// cache requires; see artifactAwaitingAdapterDisk.
	needsAdapterDisk bool

	// terminatingNames holds artifacts that serve the model source and would be
	// adoptable were they not being deleted.
	terminatingNames []string
}

// matchArtifactForSource picks the artifact this cache should resolve modelSource to:
// the candidate with the best status among those adoptable as-is or only awaiting
// their adapter disk, preferring one that already has the disk when statuses tie. A
// terminating candidate is reported separately rather than adopted or treated as
// missing - see TerminatingArtifactNames.
func matchArtifactForSource(
	profileCache *aimv1alpha2.AIMProfileCache,
	artifacts []aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
) artifactMatch {
	var match artifactMatch

	for _, cached := range artifacts {
		needsAdapterDisk := false
		if !ArtifactAdoptableBy(profileCache, &cached) {
			if !artifactAwaitingAdapterDisk(profileCache, &cached) {
				continue
			}
			needsAdapterDisk = true
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

		// Best status wins; on a tie, an artifact that already has the adapter disk
		// beats one that would need it added.
		cmp := 1
		if match.found {
			cmp = constants.CompareAIMStatus(cached.Status.Status, match.best.Status.Status)
		}
		better := cmp > 0 || (cmp == 0 && match.needsAdapterDisk && !needsAdapterDisk)
		if better {
			match.found = true
			match.best = cached
			match.needsAdapterDisk = needsAdapterDisk
		}
	}

	return match
}

// artifactAwaitingAdapterDisk reports whether artifact is a model artifact that
// profileCache could adopt but for the adapter disk the cache requires - typically
// one downloaded before a LoRA service turned requiresAdapterDisk on. Such an
// artifact gets the disk added in place instead of being replaced, so nothing is
// downloaded again and services mounting its model PVC are not disturbed.
func artifactAwaitingAdapterDisk(profileCache *aimv1alpha2.AIMProfileCache, artifact *aimv1alpha1.AIMArtifact) bool {
	if !profileCache.Spec.RequiresAdapterDisk || artifact.Spec.AdapterDisk != nil {
		return false
	}
	if artifact.Spec.Type != "" && artifact.Spec.Type != aimv1alpha1.ArtifactTypeModel {
		return false
	}
	return artifactCompatibleWith(profileCache, artifact)
}

// buildAdapterDiskPatch returns a partial AIMArtifact that adds an adapter disk
// and carries no other field, so the dedicated field manager applying it owns
// only spec.adapterDisk. Size and class are left empty for the AIMArtifact
// reconciler to resolve from AIMRuntimeConfig.Storage.
func buildAdapterDiskPatch(namespace, name string) *unstructured.Unstructured {
	patch := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"adapterDisk": map[string]any{}},
	}}
	patch.SetGroupVersionKind(aimv1alpha1.GroupVersion.WithKind("AIMArtifact"))
	patch.SetNamespace(namespace)
	patch.SetName(name)
	return patch
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
			},
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

	for i := range obs.AdapterDiskUpgrades {
		artifact := &obs.AdapterDiskUpgrades[i]
		result.ApplyFields(
			buildAdapterDiskPatch(artifact.Namespace, artifact.Name),
			constants.FieldManagerSuffixAdapterDisk,
			&controllerutils.FieldApplyEvent{
				Target: artifact,
				Reason: constants.ReasonAdapterDiskRequested,
				Message: fmt.Sprintf("AIMProfileCache %s requires an adapter disk; added spec.adapterDisk in place",
					pc.Name),
			},
		)
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
	if !obs.artifacts.OK() {
		// Keep the last known artifact/PVC projection while the list is
		// unavailable. Fetch health records the dependency failure.
		return
	}

	// Must run ahead of the early returns below: an artifact this cache advertised
	// until now has to stop being advertised, PVC included, the moment it starts
	// being deleted.
	status.Artifacts = buildResolvedArtifacts(obs.BestArtifacts)

	// Reported ahead of the missing artifacts it will turn into: "waiting for a delete"
	// is the part an operator can act on.
	if len(obs.TerminatingArtifactNames) > 0 {
		cm.MarkFalse(artifactsReadyConditionType, aimv1alpha2.AIMProfileCacheReasonArtifactTerminating,
			"Waiting for AIM artifacts to finish deleting before recreating them: "+strings.Join(obs.TerminatingArtifactNames, ", "))
		return
	}
	if len(obs.MissingCaches) > 0 {
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
// ArtifactResolvableBy, which builds on this predicate, so rejecting a terminating
// artifact here would drop the very wakeups
// that tell a cache to stop advertising it. That rejection is matchArtifactForSource's
// job instead.
func ArtifactAdoptableBy(profileCache *aimv1alpha2.AIMProfileCache, artifact *aimv1alpha1.AIMArtifact) bool {
	// LoRA-serving caches can use only a disk-bearing artifact as is; a diskless
	// match would wedge the service. matchArtifactForSource still resolves such an
	// artifact and adds the disk in place (see artifactAwaitingAdapterDisk).
	if profileCache.Spec.RequiresAdapterDisk && artifact.Spec.AdapterDisk == nil {
		return false
	}
	return artifactCompatibleWith(profileCache, artifact)
}

// ArtifactResolvableBy reports whether matchArtifactForSource may resolve
// profileCache to artifact: adoptable as is, or only awaiting the adapter disk the
// cache would add in place. Like ArtifactAdoptableBy it ignores SourceURI and
// deletionTimestamp, so the artifact watch can filter on it.
func ArtifactResolvableBy(profileCache *aimv1alpha2.AIMProfileCache, artifact *aimv1alpha1.AIMArtifact) bool {
	return ArtifactAdoptableBy(profileCache, artifact) || artifactAwaitingAdapterDisk(profileCache, artifact)
}

// artifactCompatibleWith applies ArtifactAdoptableBy's mode and storage-class
// rules, without its adapter-disk requirement.
func artifactCompatibleWith(profileCache *aimv1alpha2.AIMProfileCache, artifact *aimv1alpha1.AIMArtifact) bool {
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
