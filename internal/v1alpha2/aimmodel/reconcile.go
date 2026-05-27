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
	"sort"
	"strings"
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

const (
	annotationModelUID       = constants.AimLabelDomain + "/model-uid"
	annotationModelName      = constants.AimLabelDomain + "/model-name"
	annotationModelNamespace = constants.AimLabelDomain + "/model-namespace"
	// annotationBaseImage is the internal wire format the AIMModel reconciler
	// uses to communicate the AIM_BASE_IMAGE_REF extracted from the source
	// image to the AIMProfile reconciler. The AIMProfile reconciler launders
	// this into AIMProfileStatus.BaseImage during status decoration; readers
	// (AIMService overlay, AIMProfileSet) prefer the status value and only
	// fall back to the annotation in the brief window before status is
	// populated.
	annotationBaseImage     = aimprofile.AnnotationBaseImage
	ManagedModelUIDIndexKey = ".metadata.annotations." + annotationModelUID

	componentDiscovery     = "Discovery"
	componentNodeInventory = "NodeInventory"
	componentProfiles      = "ManagedProfiles"
	componentProfileSet    = "ProfileSet"
)

func AnnotationModelName() string { return annotationModelName }

func AnnotationModelUID() string { return annotationModelUID }

func AnnotationModelNamespace() string { return annotationModelNamespace }

type ModelReconciler struct {
	// Client is forwarded into the legacy v1alpha1 ModelReconciler we wrap
	// (see legacy()). The legacy reconciler needs it to resolve fine-tuned
	// template deployment images per-match: each copy fetches its source
	// owner's baseImageRef on demand.
	Client    client.Client
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// legacy returns a fresh v1alpha1 ModelReconciler with the same dependencies
// we hold. We construct it on demand (rather than caching) because it is
// stateless and lifetimes are trivial; this keeps the wiring obvious at each
// call site.
func (r *ModelReconciler) legacy() *legacyaimmodel.ModelReconciler {
	return &legacyaimmodel.ModelReconciler{
		Client:    r.Client,
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
}

type managedProfile struct {
	Object client.Object
	Status aimv1alpha2.AIMProfileStatus
}

type desiredProfile struct {
	Object client.Object
}

type ModelFetchResult struct {
	model            *aimv1alpha2.AIMModel
	legacyModel      *aimv1alpha1.AIMModel
	discovery        DiscoveryDecision
	nodes            controllerutils.FetchResult[[]corev1.Node]
	existingProfiles controllerutils.FetchResult[[]managedProfile]
	childProfileSet  controllerutils.FetchResult[*aimv1alpha2.AIMProfileSet]
	legacyFetch      *legacyaimmodel.ModelFetchResult
}

type ModelObservation struct {
	ModelFetchResult
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
	DesiredProfileSet    *aimv1alpha2.AIMProfileSet
	DiscoveryState       *aimv1alpha1.ModelDiscoveryState
	DiscoveryReason      string
	DiscoveryMessage     string
	DiscoveryProgressing bool
	RequeueAfter         time.Duration
	pruneSafe            bool
	BuildErr             error
	componentHealth      []controllerutils.ComponentHealth
	legacyObservation    *legacyaimmodel.ModelObservation
}

func (obs ModelObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	return obs.componentHealth
}

// GetApplyOptions delegates to the legacy reconciler when the legacy
// pipeline ran (RuntimeConfig is a v1alpha1-shaped concept fetched on the
// legacy side). For pure v1alpha2 specs (profileCopy-only) the legacy
// pipeline is skipped entirely and no apply-time options are needed.
func (r *ModelReconciler) GetApplyOptions(obs ModelObservation) controllerutils.ApplyOptions {
	if obs.legacyObservation != nil {
		return r.legacy().GetApplyOptions(*obs.legacyObservation)
	}
	return controllerutils.ApplyOptions{}
}

func (r *ModelReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMModel],
) ModelFetchResult {
	model := reconcileCtx.Object

	// Run the legacy fetch only when the spec carries any v1alpha1-shaped
	// inputs (image, customTemplates, modelSources, aimId, etc.). Both
	// pipelines emit disjoint outputs (legacy → AIMServiceTemplates from
	// RecommendedDeployments / customTemplates / aimId-matching; native →
	// AIMProfiles from in-cluster discovery + AIMProfileSet from
	// profileCopy) and write disjoint status fields, so it is safe to
	// compose them whenever both have work to do.
	//
	// Skipping the legacy fetch for pure v1alpha2 specs (profileCopy-only)
	// is essential: otherwise the legacy ImageMetadata fetch tries to parse
	// an empty image reference and surfaces spurious failures
	// (ImageMetadataReady=False, DependenciesReachable=False).
	result := ModelFetchResult{
		model:            model,
		existingProfiles: controllerutils.FetchResult[[]managedProfile]{Value: nil, Error: nil},
	}
	if hasLegacyInputs(model.Spec) {
		result.legacyModel = asLegacyModel(model)
		legacyFetch := r.legacy().FetchRemoteState(ctx, c, controllerutils.ReconcileContext[*aimv1alpha1.AIMModel]{Object: result.legacyModel})
		result.legacyFetch = &legacyFetch
	}
	// Skip native v1alpha2 discovery for custom and fine-tuned models. They
	// either bring their own model artifacts via spec.modelSources +
	// customTemplates, or copy templates from a base by aimId. Their image is
	// typically a base image (e.g. aim-base:X.Y) that exposes only "general"
	// fallback profiles with no AIM_ID, which would yield AIMProfiles missing
	// the required spec.aimId and fail CRD validation. Discovery wouldn't add
	// value for these cases anyway — readiness comes from the legacy
	// AIMServiceTemplate path.
	//
	// Also honour spec.discovery.extractMetadata=false: that flag is the
	// user's explicit opt-out from any image inspection, and the native
	// in-cluster discovery Job is a stronger form of inspection than the
	// legacy OCI-label fetch.
	if model.Spec.Image != "" && !legacyaimmodel.IsCustomModel(&model.Spec) && shouldRunNativeDiscovery(&model.Spec) {
		inputs := DiscoveryInputs{
			Kind:             DiscoveryKindAIMModel,
			ModelName:        model.Name,
			ModelUID:         string(model.UID),
			Namespace:        model.Namespace,
			Image:            model.Spec.Image,
			ImagePullSecrets: model.Spec.ImagePullSecrets,
			ServiceAccount:   model.Spec.ServiceAccountName,
			OwnerRef:         ownerReferenceForModel(model),
			CurrentState:     model.Status.Discovery,
			Clientset:        r.Clientset,
		}
		fetch := FetchDiscoveryState(ctx, c, inputs)
		result.discovery = DecideDiscovery(ctx, c, inputs, fetch)

		var nodes corev1.NodeList
		nodeErr := c.List(ctx, &nodes)
		result.nodes = controllerutils.FetchResult[[]corev1.Node]{Value: nodes.Items, Error: nodeErr}
	}
	profiles, err := listManagedNamespaceProfiles(ctx, c, model.Namespace, string(model.UID))
	result.existingProfiles = controllerutils.FetchResult[[]managedProfile]{Value: profiles, Error: err}
	if derivationSpec(&model.Spec) != nil {
		childName, childNameErr := childProfileSetName(model.Name)
		if childNameErr != nil {
			result.childProfileSet = controllerutils.FetchResult[*aimv1alpha2.AIMProfileSet]{Error: childNameErr}
		} else {
			result.childProfileSet = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: childName, Namespace: model.Namespace}, &aimv1alpha2.AIMProfileSet{})
		}
	}
	return result
}

func (r *ModelReconciler) ComposeState(
	ctx context.Context,
	rc controllerutils.ReconcileContext[*aimv1alpha2.AIMModel],
	fetch ModelFetchResult,
) ModelObservation {
	obs := ModelObservation{ModelFetchResult: fetch}

	// Always compose a legacy observation alongside native. Both contribute
	// disjoint conditions and component health entries; legacy adds entries
	// like RuntimeConfig / ImageMetadata / ServiceTemplates while native adds
	// Discovery / ManagedProfiles / ProfileSet / NodeInventory.
	if fetch.legacyFetch != nil {
		legacyObs := r.legacy().ComposeState(ctx, controllerutils.ReconcileContext[*aimv1alpha1.AIMModel]{Object: fetch.legacyModel}, *fetch.legacyFetch)
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
				obs.DiscoveryCacheRef = &aimv1alpha1.DiscoveryCacheReference{Name: cacheName, Namespace: fetch.model.Namespace}
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
					obs.DesiredProfiles, obs.BuildErr = buildDesiredOfficialProfiles(
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
			obs.ProfileSetRef = &aimv1alpha1.ProfileSetReference{Name: childName, Namespace: fetch.model.Namespace}
			obs.DesiredProfileSet = buildDesiredProfileSet(fetch.model, obs.DiscoveryCacheRef, childName)
		}
		if fetch.childProfileSet.OK() {
			obs.ManagedProfiles = fetch.childProfileSet.Value.Status.ManagedProfiles
			obs.ExistingDesiredCount = 1
		}
	} else if fetch.existingProfiles.OK() {
		obs.ManagedProfiles, obs.ExistingDesiredCount = summarizeManagedProfiles(obs.DesiredProfiles, fetch.existingProfiles.Value)
	}
	obs.pruneSafe = desiredProfilesKnown && obs.BuildErr == nil && fetch.existingProfiles.Error == nil
	obs.componentHealth = buildModelComponentHealth(obs)
	if obs.legacyObservation != nil {
		obs.componentHealth = append(obs.componentHealth, obs.legacyObservation.GetComponentHealth()...)
	}
	return obs
}

func (r *ModelReconciler) PlanResources(
	ctx context.Context,
	rc controllerutils.ReconcileContext[*aimv1alpha2.AIMModel],
	obs ModelObservation,
) controllerutils.PlanResult {
	var plan controllerutils.PlanResult
	if obs.RequeueAfter > 0 {
		plan.RequeueAfter = obs.RequeueAfter
	}
	if obs.CompletedJobToDelete != nil {
		plan.Delete(obs.CompletedJobToDelete)
	}

	// Always merge the legacy plan first. Its outputs (AIMServiceTemplates
	// from RecommendedDeployments / customTemplates / aimId-matching) are
	// independent of native discovery, so they must be applied regardless of
	// whether native discovery has produced a catalog yet or whether the
	// pruneSafe gate below is satisfied. Without this, an image whose
	// discovery hasn't completed (or that has no v2 catalog at all, e.g. an
	// older AIM image) would never get its legacy templates applied.
	if obs.legacyObservation != nil {
		legacyPlan := r.legacy().PlanResources(ctx, controllerutils.ReconcileContext[*aimv1alpha1.AIMModel]{Object: obs.legacyModel}, *obs.legacyObservation)
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
	// Image-discovery profiles get a controller ownerReference back to the
	// AIMModel that produced them. Without it SourceModelFromOwnerRefs
	// (in internal/v1alpha2/aimprofile/provenance.go) returns nil and
	// EnsureProfileProvenanceLabels strips the source-model labels every
	// reconcile, racing this builder which restamps them — a sustained
	// label flap that breaks AIMService selector resolution on
	// `selector.modelRef.name`. The cluster sibling
	// (cluster_reconcile.go) already does this; the namespace half
	// previously used ApplyWithoutOwnerRef and silently violated the
	// SourceModelFromOwnerRefs contract.
	for _, desired := range obs.DesiredProfiles {
		plan.Apply(desired.Object)
	}
	if !obs.pruneSafe {
		return plan
	}
	desiredNames := desiredProfileNames(obs.DesiredProfiles)
	derivation := derivationSpec(&obs.model.Spec)
	if derivation != nil {
		for _, existing := range obs.existingProfiles.Value {
			plan.Delete(existing.Object)
		}
	} else {
		for _, existing := range obs.existingProfiles.Value {
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

func (r *ModelReconciler) DecorateStatus(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	obs ModelObservation,
) {
	// Apply legacy decorations first (SourceType, ImageMetadata, finetune
	// resolution) and then native (AimId, BaseImage, DiscoveryCacheRef,
	// DiscoveredProfiles, ProfileSetRef, ManagedProfiles). Both write
	// disjoint fields, so order only matters for EnsureBaseImageBridge at
	// the end.
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

func buildModelComponentHealth(obs ModelObservation) []controllerutils.ComponentHealth {
	health := make([]controllerutils.ComponentHealth, 0, 4)
	// Custom / fine-tuned models skip native discovery entirely (see
	// FetchRemoteState). They get their AIMServiceTemplates from the legacy
	// pipeline and don't need v1alpha2 AIMProfiles. Suppress the Discovery,
	// NodeInventory, and Profiles component contributions so they don't block
	// readiness with stale "DiscoveryPending"/"NoSupportedProfiles" entries.
	// ProfileSet still contributes when spec.profileCopy is explicitly set.
	//
	// Likewise, when spec.discovery.extractMetadata=false the user has
	// explicitly opted out of any image inspection, so the native discovery
	// pipeline contributes no components — the model is "Ready" when its
	// other components are.
	runNativeDiscovery := !legacyaimmodel.IsCustomModel(&obs.model.Spec) && shouldRunNativeDiscovery(&obs.model.Spec)
	if runNativeDiscovery && obs.model.Spec.Image != "" {
		health = append(health, discoveryComponentHealth(obs))
	}
	if runNativeDiscovery && obs.BuildErr == nil && obs.model.Spec.ProfileCopy == nil && obs.model.Spec.Image != "" && obs.nodes.HasError() {
		health = append(health, obs.nodes.ToComponentHealth(componentNodeInventory, func(_ []corev1.Node) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{State: constants.AIMStatusReady, Reason: "NodeInventoryAvailable"}
		}))
	}
	if obs.BuildErr != nil {
		health = append(health, controllerutils.ComponentHealth{
			Component:      componentProfiles,
			State:          constants.AIMStatusDegraded,
			Reason:         "BuildFailed",
			Message:        obs.BuildErr.Error(),
			Errors:         []error{obs.BuildErr},
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
		return health
	}
	if runNativeDiscovery && obs.model.Spec.Image != "" && obs.DiscoveryCacheRef == nil {
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfiles,
			State:     constants.AIMStatusProgressing,
			Reason:    constants.ReasonCreating,
			Message:   "Waiting for discovery to produce a profile cache",
		})
		return health
	}
	if derivationSpec(&obs.model.Spec) != nil {
		if obs.ExistingDesiredCount == 0 {
			health = append(health, controllerutils.ComponentHealth{
				Component: componentProfileSet,
				State:     constants.AIMStatusProgressing,
				Reason:    constants.ReasonCreating,
				Message:   "Managed profile set is still being created",
			})
			return health
		}
		if obs.ManagedProfiles.Total > 0 && obs.ManagedProfiles.Ready == obs.ManagedProfiles.Total {
			health = append(health, controllerutils.ComponentHealth{
				Component: componentProfileSet,
				State:     constants.AIMStatusReady,
				Reason:    "ManagedProfilesReady",
			})
			return health
		}
		if obs.ManagedProfiles.Ready > 0 {
			health = append(health, controllerutils.ComponentHealth{
				Component: componentProfileSet,
				State:     constants.AIMStatusDegraded,
				Reason:    "ManagedProfilesPartiallyReady",
				Message:   "Some derived profiles are ready, but others are not available",
			})
			return health
		}
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfileSet,
			State:     constants.AIMStatusNotAvailable,
			Reason:    "ManagedProfilesNotAvailable",
			Message:   "No ready derived profiles available for this model",
		})
		return health
	}
	if !runNativeDiscovery {
		// Custom / fine-tuned models do not produce v1alpha2 AIMProfiles.
		// Readiness is gated entirely by the legacy AIMServiceTemplate path,
		// so emit no further native components here.
		return health
	}
	if obs.ManagedProfiles.Total == 0 {
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfiles,
			State:     constants.AIMStatusNotAvailable,
			Reason:    "NoSupportedProfiles",
			Message:   "No discovered profiles are currently supported by the cluster",
		})
		return health
	}
	if obs.ExistingDesiredCount < obs.ManagedProfiles.Total {
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfiles,
			State:     constants.AIMStatusProgressing,
			Reason:    constants.ReasonCreating,
			Message:   "Managed profiles are still being created",
		})
		return health
	}
	if obs.ManagedProfiles.Ready == obs.ManagedProfiles.Total {
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfiles,
			State:     constants.AIMStatusReady,
			Reason:    "ManagedProfilesReady",
		})
		return health
	}
	if obs.ManagedProfiles.Ready > 0 {
		health = append(health, controllerutils.ComponentHealth{
			Component: componentProfiles,
			State:     constants.AIMStatusDegraded,
			Reason:    "ManagedProfilesPartiallyReady",
			Message:   "Some managed profiles are ready, but others are not available",
		})
		return health
	}
	health = append(health, controllerutils.ComponentHealth{
		Component: componentProfiles,
		State:     constants.AIMStatusNotAvailable,
		Reason:    "ManagedProfilesNotAvailable",
		Message:   "Managed profiles exist but none are ready",
	})
	return health
}

func discoveryComponentHealth(obs ModelObservation) controllerutils.ComponentHealth {
	switch {
	case obs.DiscoveryReason == "DiscoveryCacheValid" || (obs.DiscoveryCacheRef != nil && !obs.DiscoveryProgressing):
		return controllerutils.ComponentHealth{
			Component: componentDiscovery,
			State:     constants.AIMStatusReady,
			Reason:    "DiscoveryCacheValid",
		}
	case obs.DiscoveryProgressing:
		return controllerutils.ComponentHealth{
			Component: componentDiscovery,
			State:     constants.AIMStatusProgressing,
			Reason:    firstNonEmpty(obs.DiscoveryReason, constants.ReasonCreating),
			Message:   obs.DiscoveryMessage,
		}
	default:
		return controllerutils.ComponentHealth{
			Component: componentDiscovery,
			State:     constants.AIMStatusNotAvailable,
			Reason:    firstNonEmpty(obs.DiscoveryReason, "DiscoveryPending"),
			Message:   obs.DiscoveryMessage,
		}
	}
}

func summarizeDiscoveredProfiles(catalog aimprofile.DiscoveryCatalog, nodes []corev1.Node, nodeErr error) aimv1alpha1.DiscoveredProfileCounts {
	result := aimv1alpha1.DiscoveredProfileCounts{Total: int32(len(catalog.Profiles))}
	if nodeErr != nil {
		return result
	}
	type hwKey struct {
		acceleratorType  aimv1alpha1.AcceleratorType
		acceleratorModel string
		acceleratorCount int32
		supported        bool
	}
	groups := make(map[hwKey]*aimv1alpha1.ProfileHardwareGroup)
	order := make([]hwKey, 0)
	for _, entry := range catalog.Profiles {
		supported := isSupportedProfile(entry.Spec, nodes)
		if supported {
			result.Supported++
		} else {
			result.Unsupported++
		}
		key := hwKey{
			acceleratorType:  entry.Spec.AcceleratorType,
			acceleratorModel: entry.Spec.AcceleratorModel,
			acceleratorCount: entry.Spec.AcceleratorCount,
			supported:        supported,
		}
		group, ok := groups[key]
		if !ok {
			group = &aimv1alpha1.ProfileHardwareGroup{
				AcceleratorType:  key.acceleratorType,
				AcceleratorModel: key.acceleratorModel,
				AcceleratorCount: key.acceleratorCount,
				Supported:        key.supported,
			}
			groups[key] = group
			order = append(order, key)
		}
		group.Profiles = append(group.Profiles, aimv1alpha1.ProfileHardwareGroupEntry{
			Metric:    entry.Spec.Metric,
			Precision: entry.Spec.Precision,
		})
	}
	if len(order) == 0 {
		return result
	}
	// Stable order: type, model, count, supported. Independent of the
	// (sorted) ConfigMap key iteration order so the rendered status doesn't
	// flap between reconciles.
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.acceleratorType != b.acceleratorType {
			return a.acceleratorType < b.acceleratorType
		}
		if a.acceleratorModel != b.acceleratorModel {
			return a.acceleratorModel < b.acceleratorModel
		}
		if a.acceleratorCount != b.acceleratorCount {
			return a.acceleratorCount < b.acceleratorCount
		}
		// Group "supported=true" before "supported=false" for a given
		// hardware footprint so kubectl renders the actionable group first.
		return a.supported && !b.supported
	})
	result.ByHardware = make([]aimv1alpha1.ProfileHardwareGroup, 0, len(order))
	for _, key := range order {
		group := groups[key]
		sort.Slice(group.Profiles, func(i, j int) bool {
			if group.Profiles[i].Metric != group.Profiles[j].Metric {
				return group.Profiles[i].Metric < group.Profiles[j].Metric
			}
			return group.Profiles[i].Precision < group.Profiles[j].Precision
		})
		result.ByHardware = append(result.ByHardware, *group)
	}
	return result
}

func buildDesiredOfficialProfiles(
	model *aimv1alpha2.AIMModel,
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
		profile := &aimv1alpha2.AIMProfile{}
		profile.Namespace = model.Namespace
		profile.Name = name
		profile.Labels = map[string]string{constants.LabelK8sManagedBy: constants.LabelValueManagedBy}
		profile.Annotations = aimprofile.MarkProfileCopyable(aimprofile.MarkProfileSource(map[string]string{
			annotationModelUID:       string(model.UID),
			annotationModelName:      model.Name,
			annotationModelNamespace: model.Namespace,
			annotationBaseImage:      entry.BaseImage,
		}, aimprofile.ProfileSourceImage), true)
		// Image-discovery profiles are always `discovered`; the role label
		// reflects structural classification of the catalog entry:
		// deployable when the spec carries identity (aimId + modelSources),
		// base when those are absent — which is what a base image (the
		// source of custom-model derivation) emits. Stamping at
		// materialisation time means consumers don't have to wait for the
		// AIMProfile reconciler to backfill.
		aimprofile.StampProfileProvenance(
			profile,
			aimprofile.ProfileRoleLabelValue(entry.Spec),
			aimv1alpha1.ProfileOriginDiscovered,
			&aimv1alpha2.ProfileSourceModel{
				Name:      model.Name,
				Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
				Namespace: model.Namespace,
			},
		)
		spec := *entry.Spec.DeepCopy()
		spec.ImagePullSecrets = copyPullSecrets(model.Spec.ImagePullSecrets)
		spec.ServiceAccountName = model.Spec.ServiceAccountName
		// Recommended-deployment fallback for the v1alpha2 → in-image
		// `primary` migration. Newer images stamp `metadata.primary`
		// directly in the per-profile YAML, in which case the catalog
		// parser sets PrimaryExplicit=true and we trust the value from the
		// YAML unchanged. Older images don't carry the per-profile flag
		// and instead encode the "auto-selectable" set via the OCI label
		// `com.amd.aim.model.recommendedDeployments`; for those we derive
		// Spec.Primary from a match against that list so the
		// `spec.model.name` shortcut still resolves to a sensible
		// candidate without forcing every operator to upgrade their image
		// inventory in lockstep with the operator.
		if !entry.PrimaryExplicit {
			spec.Primary = matchesRecommendedDeployment(spec, recommendedDeployments)
		}
		profile.Spec = aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: spec}
		desired = append(desired, desiredProfile{Object: profile})
	}
	return desired, nil
}

// matchesRecommendedDeployment reports whether the spec matches any entry in
// the OCI `recommendedDeployments` list. A match wins on profileId when set
// (an exact name reference) or on the (gpuModel, gpuCount, precision, metric)
// tuple otherwise. Empty rd fields are wildcards — an rd with only gpuModel
// matches every spec with that accelerator, regardless of precision/metric.
// Returns false on an empty list so newer images that omit the OCI label
// behave as if every profile defaults to Primary=false (and rely on the
// per-profile `primary` field instead).
func matchesRecommendedDeployment(spec aimv1alpha2.AIMProfileSpecCommon, rds []aimv1alpha1.RecommendedDeployment) bool {
	for i := range rds {
		rd := &rds[i]
		if rd.ProfileId != "" {
			if rd.ProfileId == spec.ProfileId {
				return true
			}
			continue
		}
		if rd.GPUModel != "" && !strings.EqualFold(rd.GPUModel, spec.AcceleratorModel) {
			continue
		}
		if rd.GPUCount != 0 && rd.GPUCount != spec.AcceleratorCount {
			continue
		}
		if rd.Precision != "" && !strings.EqualFold(rd.Precision, string(spec.Precision)) {
			continue
		}
		if rd.Metric != "" && !strings.EqualFold(rd.Metric, string(spec.Metric)) {
			continue
		}
		return true
	}
	return false
}

// recommendedDeploymentsFrom returns the OCI recommendedDeployments list from
// a v1alpha1 ImageMetadata, or nil if any layer is absent. Centralised so the
// AIMModel and AIMClusterModel reconcilers extract the field the same way.
func recommendedDeploymentsFrom(metadata *aimv1alpha1.ImageMetadata) []aimv1alpha1.RecommendedDeployment {
	if metadata == nil || metadata.Model == nil {
		return nil
	}
	return metadata.Model.RecommendedDeployments
}

func buildDesiredProfileSet(model *aimv1alpha2.AIMModel, cacheRef *aimv1alpha1.DiscoveryCacheReference, name string) *aimv1alpha2.AIMProfileSet {
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
	profileSet := &aimv1alpha2.AIMProfileSet{}
	profileSet.Namespace = model.Namespace
	profileSet.Name = name
	profileSet.Labels = map[string]string{
		constants.LabelK8sManagedBy:        constants.LabelValueManagedBy,
		constants.LabelKeySourceModel:      model.Name,
		constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
	}
	profileSet.Annotations = map[string]string{
		annotationModelUID:       string(model.UID),
		annotationModelName:      model.Name,
		annotationModelNamespace: model.Namespace,
	}
	profileSet.Spec = spec
	return profileSet
}

func isSupportedProfile(spec aimv1alpha2.AIMProfileSpecCommon, nodes []corev1.Node) bool {
	if !aimprofile.HasAcceleratorRequirement(spec.AcceleratorModel, spec.AcceleratorCount, spec.Resources) {
		return true
	}
	resolvedResources := aimprofile.ResolveResources(spec.AcceleratorType, spec.AcceleratorCount, spec.Resources)
	return aimprofile.MatchNodes(nodes, spec.AcceleratorModel, resolvedResources).MatchingNodes > 0
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
// AIMProfile or AIMClusterProfile. The boolean is false when the object
// isn't one of those kinds — used so callers can keep treating unknown
// objects as base profiles in counting code without blowing up.
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

func desiredProfileNames(desired []desiredProfile) map[string]struct{} {
	names := make(map[string]struct{}, len(desired))
	for _, profile := range desired {
		names[client.ObjectKeyFromObject(profile.Object).String()] = struct{}{}
	}
	return names
}

func listManagedNamespaceProfiles(ctx context.Context, c client.Client, namespace, modelUID string) ([]managedProfile, error) {
	var list aimv1alpha2.AIMProfileList
	if err := c.List(ctx, &list, client.InNamespace(namespace), client.MatchingFields{ManagedModelUIDIndexKey: modelUID}); err != nil {
		return nil, err
	}
	profiles := make([]managedProfile, 0, len(list.Items))
	for i := range list.Items {
		profiles = append(profiles, managedProfile{
			Object: list.Items[i].DeepCopy(),
			Status: list.Items[i].Status,
		})
	}
	return profiles, nil
}

func listManagedClusterProfiles(ctx context.Context, c client.Client, modelUID string) ([]managedProfile, error) {
	var list aimv1alpha2.AIMClusterProfileList
	if err := c.List(ctx, &list, client.MatchingFields{ManagedModelUIDIndexKey: modelUID}); err != nil {
		return nil, err
	}
	profiles := make([]managedProfile, 0, len(list.Items))
	for i := range list.Items {
		profiles = append(profiles, managedProfile{
			Object: list.Items[i].DeepCopy(),
			Status: list.Items[i].Status,
		})
	}
	return profiles, nil
}

func copyPullSecrets(in []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.LocalObjectReference, len(in))
	copy(out, in)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func ownerReferenceForModel(model *aimv1alpha2.AIMModel) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         aimv1alpha2.GroupVersion.String(),
		Kind:               "AIMModel",
		Name:               model.Name,
		UID:                model.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}
