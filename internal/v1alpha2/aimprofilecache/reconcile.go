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
	MissingCaches      []aimv1alpha2.AIMModelSource
	BestArtifacts      map[string]aimv1alpha1.AIMArtifact
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
	} else if len(obs.MissingCaches) > 0 {
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

	var modelSources []aimv1alpha2.AIMModelSource
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

	obs.BestArtifacts = map[string]aimv1alpha1.AIMArtifact{}

	logger.V(1).Info("ComposeState: checking artifacts",
		"profileCache", pc.Name,
		"modelSources", len(modelSources),
		"fetchedArtifacts", len(fetch.artifacts.Value.Items))

	for _, model := range modelSources {
		found := false
		bestArtifact := aimv1alpha1.AIMArtifact{}
		for _, cached := range fetch.artifacts.Value.Items {
			if cached.Status.Status == "" {
				continue
			}

			// Mode isolation: same logic as template cache
			if pc.Spec.Mode == aimv1alpha2.ProfileCacheModeShared && len(cached.GetOwnerReferences()) > 0 {
				continue
			}
			if pc.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated && !hasOwnerReferenceUID(cached.GetOwnerReferences(), pc.UID) {
				continue
			}

			if cached.Spec.SourceURI == model.SourceURI &&
				(pc.Spec.StorageClassName == "" || pc.Spec.StorageClassName == cached.Spec.StorageClassName) {
				if !found || constants.CompareAIMStatus(bestArtifact.Status.Status, cached.Status.Status) < 0 {
					found = true
					bestArtifact = cached
				}
			}
		}
		if found {
			logger.V(1).Info("ComposeState: model source matched",
				"modelID", model.ModelID,
				"bestArtifact", bestArtifact.Name,
				"bestStatus", bestArtifact.Status.Status)
			obs.BestArtifacts[model.ModelID] = bestArtifact
		} else {
			logger.V(1).Info("ComposeState: model source missing artifact", "modelID", model.ModelID)
			obs.MissingCaches = append(obs.MissingCaches, model)
		}
	}

	return obs
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
			},
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

	// Populate status.artifacts
	if len(obs.BestArtifacts) > 0 {
		status.Artifacts = make(map[string]aimv1alpha1.AIMResolvedArtifact, len(obs.BestArtifacts))
		for modelName, a := range obs.BestArtifacts {
			status.Artifacts[a.Name] = aimv1alpha1.AIMResolvedArtifact{
				UID:                   string(a.UID),
				Name:                  a.Name,
				Model:                 modelName,
				Status:                a.Status.Status,
				PersistentVolumeClaim: a.Status.PersistentVolumeClaim,
			}
		}
	} else {
		status.Artifacts = nil
	}
}

// generateArtifactName returns a deterministic artifact name.
// Shared caches omit the profile cache name to allow cross-cache reuse.
// Dedicated caches scope names to the profile cache.
func generateArtifactName(pc *aimv1alpha2.AIMProfileCache, modelSource aimv1alpha2.AIMModelSource) (string, error) {
	nameWithoutDots := strings.ReplaceAll(modelSource.SourceURI, ".", "-")
	hashInputs := []any{
		modelSource.SourceURI,
		utils.MergeEnvVars(pc.Spec.Env, modelSource.Env),
		pc.Spec.StorageClassName,
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
