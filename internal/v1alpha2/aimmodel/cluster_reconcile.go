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
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

type ClusterModelReconciler struct {
	// Client is forwarded into the legacy v1alpha1 ClusterModelReconciler we
	// wrap (see legacy()). The legacy reconciler needs it to resolve
	// fine-tuned template deployment images per-match: each copy fetches its
	// source owner's baseImageRef on demand.
	Client    client.Client
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// legacy returns a fresh v1alpha1 ClusterModelReconciler with the same
// dependencies we hold. See ModelReconciler.legacy() for rationale.
func (r *ClusterModelReconciler) legacy() *legacyaimmodel.ClusterModelReconciler {
	return &legacyaimmodel.ClusterModelReconciler{
		Client:    r.Client,
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
}

type ClusterModelFetchResult struct {
	model                   *aimv1alpha2.AIMClusterModel
	legacyModel             *aimv1alpha1.AIMClusterModel
	discovery               DiscoveryDecision
	nodes                   controllerutils.FetchResult[[]corev1.Node]
	existingClusterProfiles controllerutils.FetchResult[[]managedProfile]
	childProfileSet         controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfileSet]
	legacyFetch             *legacyaimmodel.ClusterModelFetchResult
}

type ClusterModelObservation struct {
	ClusterModelFetchResult
	Kind                 aimv1alpha1.AIMModelKind
	ResolvedAimID        string
	ResolvedBaseImage    string
	DiscoveryCacheRef    *aimv1alpha1.DiscoveryCacheReference
	DiscoveredProfiles   aimv1alpha1.DiscoveredProfileCounts
	ProfileSetRef        *aimv1alpha1.ProfileSetReference
	ManagedProfiles      aimv1alpha1.ManagedProfileCounts
	ExistingDesiredCount int32
	DesiredProfiles      []desiredProfile
	DesiredCache         *corev1.ConfigMap
	DesiredJob           *batchv1.Job
	CompletedJobToDelete *batchv1.Job
	DesiredProfileSet    *aimv1alpha2.AIMClusterProfileSet
	DiscoveryState       *aimv1alpha1.ModelDiscoveryState
	DiscoveryReason      string
	DiscoveryMessage     string
	DiscoveryProgressing bool
	RequeueAfter         time.Duration
	pruneSafe            bool
	BuildErr             error
	componentHealth      []controllerutils.ComponentHealth
	legacyObservation    *legacyaimmodel.ClusterModelObservation
}

func (obs ClusterModelObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	return obs.componentHealth
}

// GetApplyOptions delegates to the legacy reconciler because RuntimeConfig
// (the only current driver of ApplyOptions) is a v1alpha1-shaped concept and
// is fetched on the legacy side.
func (r *ClusterModelReconciler) GetApplyOptions(obs ClusterModelObservation) controllerutils.ApplyOptions {
	if obs.legacyObservation != nil {
		return r.legacy().GetApplyOptions(*obs.legacyObservation)
	}
	return controllerutils.ApplyOptions{}
}

func (r *ClusterModelReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel],
) ClusterModelFetchResult {
	model := reconcileCtx.Object

	// Run the legacy fetch in parallel with the native one only when the
	// spec carries any v1alpha1-shaped inputs. See the package-level docs
	// in legacy_mode.go for why the two pipelines co-execute when both
	// have work to do, and why the legacy side must be skipped for pure
	// v1alpha2 specs (profileCopy-only) — otherwise the legacy ImageMetadata
	// fetch tries to parse an empty image reference and surfaces spurious
	// failures.
	result := ClusterModelFetchResult{
		model: model,
	}
	if hasLegacyInputs(model.Spec) {
		result.legacyModel = asLegacyClusterModel(model)
		legacyFetch := r.legacy().FetchRemoteState(ctx, c, controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel]{Object: result.legacyModel})
		result.legacyFetch = &legacyFetch
	}
	// See ModelReconciler.FetchRemoteState for why custom / fine-tuned models
	// skip native discovery — their image is typically a base image without
	// AIM_ID / model-specific profiles, and readiness is owned by the legacy
	// AIMServiceTemplate path. Also honour spec.discovery.extractMetadata=false
	// (see shouldRunNativeDiscovery).
	if model.Spec.Image != "" && !legacyaimmodel.IsCustomModel(&model.Spec) && shouldRunNativeDiscovery(&model.Spec) {
		inputs := DiscoveryInputs{
			Kind:             DiscoveryKindAIMClusterModel,
			ModelName:        model.Name,
			ModelUID:         string(model.UID),
			Namespace:        constants.GetOperatorNamespace(),
			Image:            model.Spec.Image,
			ImagePullSecrets: model.Spec.ImagePullSecrets,
			ServiceAccount:   model.Spec.ServiceAccountName,
			OwnerRef:         ownerReferenceForClusterModel(model),
			CurrentState:     model.Status.Discovery,
			Clientset:        r.Clientset,
		}
		fetch := FetchDiscoveryState(ctx, c, inputs)
		result.discovery = DecideDiscovery(ctx, c, inputs, fetch)

		var nodes corev1.NodeList
		nodeErr := c.List(ctx, &nodes)
		result.nodes = controllerutils.FetchResult[[]corev1.Node]{Value: nodes.Items, Error: nodeErr}
	}
	clusterProfiles, err := listManagedClusterProfiles(ctx, c, string(model.UID))
	result.existingClusterProfiles = controllerutils.FetchResult[[]managedProfile]{Value: clusterProfiles, Error: err}
	if derivationSpec(&model.Spec) != nil {
		childName, childNameErr := childProfileSetName(model.Name)
		if childNameErr != nil {
			result.childProfileSet = controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfileSet]{Error: childNameErr}
		} else {
			result.childProfileSet = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: childName}, &aimv1alpha2.AIMClusterProfileSet{})
		}
	}
	return result
}

func (r *ClusterModelReconciler) ComposeState(
	ctx context.Context,
	rc controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel],
	fetch ClusterModelFetchResult,
) ClusterModelObservation {
	obs := ClusterModelObservation{ClusterModelFetchResult: fetch}

	if fetch.legacyFetch != nil {
		legacyObs := r.legacy().ComposeState(ctx, controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel]{Object: fetch.legacyModel}, *fetch.legacyFetch)
		obs.legacyObservation = &legacyObs
	}
	obs.Kind = classifyModelKind(&fetch.model.Spec)
	derivation := derivationSpec(&fetch.model.Spec)
	desiredProfilesKnown := derivation != nil
	if derivation != nil {
		// For role=base derivations the target aimId lives on
		// overrides.aimId (CEL forbids selector.aimId then); for
		// role=deployable remixes it can be on either side. Prefer
		// overrides — that's the field that actually stamps the
		// derived profile's spec.aimId — and fall back to the
		// selector for legacy callers/manifests that still set it.
		obs.ResolvedAimID = derivation.Selector.AimId
		if derivation.Overrides != nil && derivation.Overrides.AimId != "" {
			obs.ResolvedAimID = derivation.Overrides.AimId
		}
	}

	if fetch.model.Spec.Image != "" {
		decision := fetch.discovery
		obs.DiscoveryState = decision.State
		obs.DiscoveryReason = decision.Reason
		obs.DiscoveryMessage = decision.Message
		obs.DiscoveryProgressing = decision.Progressing
		obs.DesiredJob = decision.DesiredJob
		obs.DesiredCache = decision.DesiredCache
		obs.CompletedJobToDelete = decision.CompletedJobToDelete
		obs.RequeueAfter = decision.RequeueAfter
		if decision.BuildErr != nil {
			obs.BuildErr = decision.BuildErr
		}
		if decision.Catalog != nil {
			catalog := *decision.Catalog
			obs.ResolvedAimID = firstNonEmpty(catalog.AimID, obs.ResolvedAimID)
			obs.ResolvedBaseImage = catalog.BaseImage
			cacheName, err := discoveryCacheName(fetch.model.Name)
			if err != nil {
				obs.BuildErr = fmt.Errorf("generate discovery cache name: %w", err)
			} else {
				obs.DiscoveryCacheRef = &aimv1alpha1.DiscoveryCacheReference{
					Name:      cacheName,
					Namespace: constants.GetOperatorNamespace(),
				}
			}
			obs.DiscoveredProfiles = summarizeDiscoveredProfiles(catalog, fetch.nodes.Value, fetch.nodes.Error)
			if obs.BuildErr == nil && derivation == nil {
				if fetch.nodes.Error != nil {
					obs.BuildErr = controllerutils.NewInfrastructureError("NodeListFailed", "failed to list cluster nodes", fetch.nodes.Error)
				} else {
					var legacyMetadata *aimv1alpha1.ImageMetadata
					if fetch.legacyFetch != nil {
						legacyMetadata = fetch.legacyFetch.ImageMetadata()
					}
					obs.DesiredProfiles, obs.BuildErr = buildDesiredOfficialClusterModelProfiles(
						fetch.model, catalog, fetch.nodes.Value,
						recommendedDeploymentsFrom(legacyMetadata),
					)
					desiredProfilesKnown = obs.BuildErr == nil
				}
			}
		}
	}

	if derivation != nil {
		childName, err := childProfileSetName(fetch.model.Name)
		if err != nil {
			obs.BuildErr = fmt.Errorf("generate child profile set name: %w", err)
		} else {
			obs.ProfileSetRef = &aimv1alpha1.ProfileSetReference{Name: childName}
			obs.DesiredProfileSet = buildDesiredClusterProfileSet(fetch.model, obs.DiscoveryCacheRef, childName)
		}
		if fetch.childProfileSet.OK() {
			obs.ManagedProfiles = fetch.childProfileSet.Value.Status.ManagedProfiles
			obs.ExistingDesiredCount = 1
		}
	} else if fetch.existingClusterProfiles.OK() {
		obs.ManagedProfiles, obs.ExistingDesiredCount = summarizeManagedProfiles(obs.DesiredProfiles, fetch.existingClusterProfiles.Value)
	}
	obs.pruneSafe = desiredProfilesKnown && obs.BuildErr == nil && fetch.existingClusterProfiles.Error == nil
	obs.componentHealth = buildClusterModelComponentHealth(obs)
	if obs.legacyObservation != nil {
		obs.componentHealth = append(obs.componentHealth, obs.legacyObservation.GetComponentHealth()...)
	}
	return obs
}

func (r *ClusterModelReconciler) PlanResources(
	ctx context.Context,
	rc controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel],
	obs ClusterModelObservation,
) controllerutils.PlanResult {
	var plan controllerutils.PlanResult
	if obs.RequeueAfter > 0 {
		plan.RequeueAfter = obs.RequeueAfter
	}
	if obs.CompletedJobToDelete != nil {
		plan.Delete(obs.CompletedJobToDelete)
	}

	// Always merge the legacy plan first; see ModelReconciler.PlanResources
	// for the full rationale. In short, the legacy plan emits independent
	// outputs (cluster-scoped AIMServiceTemplates from RecommendedDeployments
	// / customTemplates / aimId-matching) that must not be gated on native
	// discovery progress or pruneSafe.
	if obs.legacyObservation != nil {
		legacyPlan := r.legacy().PlanResources(ctx, controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel]{Object: obs.legacyModel}, *obs.legacyObservation)
		plan.Merge(legacyPlan)
	}

	if obs.BuildErr != nil {
		return plan
	}
	if obs.DesiredJob != nil {
		plan.ApplyWithoutOwnerRef(obs.DesiredJob)
	}
	if obs.DesiredCache != nil {
		plan.ApplyWithoutOwnerRef(obs.DesiredCache)
	}
	if obs.DesiredProfileSet != nil {
		plan.Apply(obs.DesiredProfileSet)
	}
	for _, desired := range obs.DesiredProfiles {
		plan.Apply(desired.Object)
	}
	if !obs.pruneSafe {
		return plan
	}
	desiredNames := desiredProfileNames(obs.DesiredProfiles)
	derivation := derivationSpec(&obs.model.Spec)
	if derivation != nil {
		for _, existing := range obs.existingClusterProfiles.Value {
			plan.Delete(existing.Object)
		}
	} else {
		for _, existing := range obs.existingClusterProfiles.Value {
			if _, keep := desiredNames[client.ObjectKeyFromObject(existing.Object).String()]; !keep {
				plan.Delete(existing.Object)
			}
		}
	}
	if derivation == nil && obs.childProfileSet.OK() && obs.childProfileSet.Value != nil {
		plan.Delete(obs.childProfileSet.Value)
	}
	return plan
}

func (r *ClusterModelReconciler) DecorateStatus(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterModelObservation,
) {
	if obs.legacyObservation != nil {
		r.legacy().DecorateStatus(status, cm, *obs.legacyObservation)
	}
	status.Kind = obs.Kind
	status.AimId = obs.ResolvedAimID
	status.BaseImage = obs.ResolvedBaseImage
	status.DiscoveryCacheRef = obs.DiscoveryCacheRef
	status.DiscoveredProfiles = obs.DiscoveredProfiles
	status.ProfileSetRef = obs.ProfileSetRef
	status.ManagedProfiles = obs.ManagedProfiles
	if obs.DiscoveryState != nil {
		status.Discovery = obs.DiscoveryState
	}
	EnsureBaseImageBridge(&obs.model.Spec, status)
}

func buildClusterModelComponentHealth(obs ClusterModelObservation) []controllerutils.ComponentHealth {
	modelObs := ModelObservation{
		Kind:                 obs.Kind,
		ResolvedAimID:        obs.ResolvedAimID,
		ResolvedBaseImage:    obs.ResolvedBaseImage,
		DiscoveryCacheRef:    obs.DiscoveryCacheRef,
		DiscoveredProfiles:   obs.DiscoveredProfiles,
		ProfileSetRef:        obs.ProfileSetRef,
		ManagedProfiles:      obs.ManagedProfiles,
		ExistingDesiredCount: obs.ExistingDesiredCount,
		BuildErr:             obs.BuildErr,
		componentHealth:      obs.componentHealth,
		DiscoveryReason:      obs.DiscoveryReason,
		DiscoveryMessage:     obs.DiscoveryMessage,
		DiscoveryProgressing: obs.DiscoveryProgressing,
	}
	modelObs.model = &aimv1alpha2.AIMModel{Spec: obs.model.Spec}
	modelObs.nodes = obs.nodes
	return buildModelComponentHealth(modelObs)
}

func buildDesiredOfficialClusterModelProfiles(
	model *aimv1alpha2.AIMClusterModel,
	catalog aimprofile.DiscoveryCatalog,
	nodes []corev1.Node,
	recommendedDeployments []aimv1alpha1.RecommendedDeployment,
) ([]desiredProfile, error) {
	desired := make([]desiredProfile, 0, len(catalog.Profiles))
	for _, entry := range catalog.Profiles {
		if !isSupportedProfile(entry.Spec, nodes) {
			continue
		}
		name, err := utils.GenerateDerivedName(
			[]string{model.Name, entry.Spec.ProfileId},
			utils.WithHashSource(model.Name, entry.Spec.ProfileId),
		)
		if err != nil {
			return nil, fmt.Errorf("generate discovered profile name: %w", err)
		}
		clusterProfile := &aimv1alpha2.AIMClusterProfile{}
		clusterProfile.Name = name
		clusterProfile.Labels = map[string]string{constants.LabelK8sManagedBy: constants.LabelValueManagedBy}
		clusterProfile.Annotations = aimprofile.MarkProfileCopyable(aimprofile.MarkProfileSource(map[string]string{
			annotationModelUID:  string(model.UID),
			annotationModelName: model.Name,
			annotationBaseImage: entry.BaseImage,
		}, aimprofile.ProfileSourceImage), true)
		// See buildDesiredOfficialProfiles for why the role label is
		// derived from the spec rather than hardcoded — a base-image
		// AIMClusterModel emits entries that classify as `base`.
		aimprofile.StampProfileProvenance(
			clusterProfile,
			aimprofile.ProfileRoleLabelValue(entry.Spec),
			aimv1alpha1.ProfileOriginDiscovered,
			&aimv1alpha2.ProfileSourceModel{
				Name: model.Name,
				Kind: aimv1alpha2.ProfileSourceModelKindAIMClusterModel,
			},
		)
		spec := *entry.Spec.DeepCopy()
		spec.ImagePullSecrets = copyPullSecrets(model.Spec.ImagePullSecrets)
		spec.ServiceAccountName = model.Spec.ServiceAccountName
		// See buildDesiredOfficialProfiles for the rationale on the
		// recommended-deployment fallback.
		if !entry.PrimaryExplicit {
			spec.Primary = matchesRecommendedDeployment(spec, recommendedDeployments)
		}
		clusterProfile.Spec = aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: spec}
		desired = append(desired, desiredProfile{Object: clusterProfile})
	}
	return desired, nil
}

func buildDesiredClusterProfileSet(model *aimv1alpha2.AIMClusterModel, cacheRef *aimv1alpha1.DiscoveryCacheReference, name string) *aimv1alpha2.AIMClusterProfileSet {
	derivation := derivationSpec(&model.Spec)
	if derivation == nil {
		return nil
	}
	spec := *derivation.DeepCopy()
	if model.Spec.Image != "" && cacheRef != nil {
		spec.SourceRef = &aimv1alpha1.ProfileSourceRef{
			Name: cacheRef.Name,
		}
	}
	profileSet := &aimv1alpha2.AIMClusterProfileSet{}
	profileSet.Name = name
	profileSet.Labels = map[string]string{
		constants.LabelK8sManagedBy:        constants.LabelValueManagedBy,
		constants.LabelKeySourceModel:      model.Name,
		constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeCluster,
	}
	profileSet.Annotations = map[string]string{
		annotationModelUID:  string(model.UID),
		annotationModelName: model.Name,
	}
	profileSet.Spec = spec
	return profileSet
}

// ownerReferenceForClusterModel returns the controller OwnerReference
// stamped on resources the cluster-model reconciler synthesises during
// discovery (the discovery Job, the catalog ConfigMap).
//
// Cross-namespace ownership note: AIMClusterModel is cluster-scoped, but
// the discovery Job and catalog ConfigMap live in the operator namespace
// (see constants.GetOperatorNamespace usage in FetchRemoteState). Kubernetes
// garbage collection requires owner and owned objects to share a namespace
// for ownerReference-driven deletion; a cluster-scoped owner referencing
// a namespaced child is supported, but cluster→namespace ownership has
// historically been the source of subtle GC bugs (kubelet logs warnings
// about cross-namespace owners that get ignored).
//
// We rely on this anyway because:
//   - The discovery Job lifecycle is managed explicitly by the
//     reconciler (DecideDiscovery returns CompletedJobToDelete after the
//     Job finishes; we delete it via the normal Plan flow).
//   - The catalog ConfigMap is intentionally retained across AIMClusterModel
//     lifetimes (it's a cache) and is GC'd via the discovery rotator when a
//     new spec hash invalidates the cache.
//
// So OwnerReference here is purely a kubectl-friendly "who owns this" hint;
// real cleanup is owned by the reconciler. The BlockOwnerDeletion flag
// ensures user-initiated `kubectl delete aimclustermodel` does not silently
// orphan an in-flight Job.
func ownerReferenceForClusterModel(model *aimv1alpha2.AIMClusterModel) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         aimv1alpha2.GroupVersion.String(),
		Kind:               "AIMClusterModel",
		Name:               model.Name,
		UID:                model.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}
