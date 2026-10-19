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

// Package aimadapter implements the pipeline-agnostic LoRA adapter staging
// engine shared by the v1alpha1 (template) and v1alpha2 (profile) AIMService
// pipelines.
//
// Both pipelines resolve a single base-model AIMArtifact and its shared,
// ReadWriteMany "adapter disk" PVC; only that resolution differs between
// them. Once a pipeline has fetched the per-adapter artifacts, the parent
// model artifact, and any existing staging Jobs (see Fetch / Dependencies),
// every downstream concern — validation, per-adapter disk-side state, staging
// Job construction, the read-only subtree mount, status mirroring, and the
// aggregate component health — is identical and lives here.
package aimadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimartifact"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
)

// Adapter validation / status reasons surfaced through the Adapters component.
const (
	ReasonParentLacksDisk     = "ParentLacksAdapterDisk"
	ReasonSubtreeProvisioning = "AdapterSubtreeProvisioning"
	ReasonNotFound            = "AdapterArtifactNotFound"
	ReasonStaging             = "AdaptersStaging"
	ReasonStaged              = "AdaptersStaged"
	ReasonInvalid             = "AdapterConfigInvalid"
)

// Observation is the per-adapter state computed in Compose.
type Observation struct {
	Name        string
	Kind        string
	SourceURI   string
	AdapterPath string
	ModelID     string
	BaseModelID string
	Rank        *int32
	// RankResolved distinguishes a rank read from a fetched artifact (including
	// the omitted-rank default) from one left unknown by a lookup gap. Static
	// max-rank resolution only trusts the former.
	RankResolved bool
	State        aimv1alpha1.AIMAdapterState
	LastError    string
}

// Dependencies holds the fetched inputs the engine needs. Each pipeline
// populates these in its fetch phase: adapterArtifacts and stagingJobs are
// keyed by adapter (reference) name, parentArtifact is the resolved base-model
// artifact (nil until the pipeline can resolve it), and subtreeSyncJob is the
// per-service Job that creates/prunes the subtree and whose success signals the
// read-only subPath mount can bind.
type Dependencies struct {
	AdapterArtifacts      map[string]controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]
	AdapterRuntimeConfigs map[string]controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	StagingJobs           map[string]controllerutils.FetchResult[*batchv1.Job]
	ParentArtifact        *controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]
	SubtreeSyncJob        controllerutils.FetchResult[*batchv1.Job]
	AdapterDiskPVC        string
	AdapterDiskPVCUID     string

	// ProfileSupportsAdapters reports whether the resolved profile advertises the
	// LoRA feature. nil means unknown / not gated (e.g. the v1alpha1 pipeline has
	// no features field); Compose only rejects when this is explicitly false.
	ProfileSupportsAdapters *bool

	// ParentResolutionErr is a terminal base-model resolution error (e.g. the
	// profile has multiple model sources, ambiguous for adapters); surfaced as a
	// config error.
	ParentResolutionErr error
}

// State is the engine's interpretation of the declared adapters against the
// resolved parent, computed once in Compose and consumed by Plan, Health,
// AddVolumeMount, and DecorateStatus.
//
// Adapters are dynamic: the aim-runtime loads/unloads them from the mounted
// disk at will, so the InferenceService only ever needs the per-service subtree
// to *exist* before it starts (the runtime errors on a missing subPath). Ready
// therefore means "mountable" — it does NOT wait for downloads. Individual
// adapters stage asynchronously after the ISVC is up; the runtime picks them up
// as they land. AllStaged is informational only.
type State struct {
	Adapters              []Observation
	AdapterDiskPVC        string
	AdapterDiskPVCUID     string
	StorageBindingChanged bool
	// MaxRank is the AIM_ADAPTER_MAX_RANK value resolved for the serving
	// workload. Dynamic mode uses service/runtime configuration; static mode
	// infers the value from the declared adapter artifacts.
	MaxRank int32

	// DesiredKey is a hash of the adapter-disk PVC and sorted declared adapter
	// set. It is recorded on status once the subtree-sync Job has reconciled that
	// binding, so the controller detects both add/remove drift and storage
	// replacement without re-run loops.
	DesiredKey string
	// SyncedKey mirrors status.AdapterSubtreeSyncKey (the last set the sync Job
	// reconciled). NeedsSync is DesiredKey != SyncedKey.
	SyncedKey string
	// CurrentSyncSucceeded is true when the sync Job for the current DesiredKey
	// has completed successfully this cycle (drives both SubtreeReady and the
	// status write).
	CurrentSyncSucceeded bool

	// SubtreeReady is true once the per-service subtree directory exists on the
	// adapter disk (a subtree-sync Job has succeeded). Sticky once recorded on
	// status, so a later add/remove never tears down a running ISVC.
	SubtreeReady bool
	// AllStaged is true once every declared adapter is Downloaded. Informational
	// for dynamic mode; a gating input for static mode (see Ready).
	AllStaged bool
	// MountReady ("mountable") is true once the per-service subtree can be mounted:
	// config valid, adapter disk PVC resolved, subtree present. Does NOT require
	// downloads. This is the gate for dynamic mode.
	MountReady bool
	// Ready gates ISVC creation: MountReady for dynamic mode; MountReady &&
	// AllStaged for static mode (the runtime enumerates adapters once at launch,
	// so they must all be on disk before the predictor starts).
	Ready bool
	// ConfigErr is a terminal user-config error (surfaced as ConfigValid=False
	// via Health). Transient not-found / not-ready conditions only keep
	// adapters in a non-Downloaded state and do not set ConfigErr.
	ConfigErr error
}

// keepAdapterPaths returns the sorted on-disk directory names for the service's
// declared adapters. In the MVP an adapter's on-disk path equals the referenced
// AIMArtifact name (adapterPathTemplate is deferred; resolveAdapterPath freezes
// AdapterPath to the artifact name), so the keep-list is derived from the
// reference names alone — stable regardless of artifact-fetch progress.
func keepAdapterPaths(service *aimv1alpha1.AIMService) []string {
	paths := make([]string, 0, len(service.Spec.Adapters))
	for i := range service.Spec.Adapters {
		paths = append(paths, service.Spec.Adapters[i].Name)
	}
	sort.Strings(paths)
	return paths
}

// desiredAdapterKey hashes the resolved adapter-disk PVC identity and sorted
// declared adapter set. The PVC UID makes the key a true storage generation:
// both a new UID-derived claim name and a same-name PVC recreation force a
// fresh subtree synchronization. The empty set still hashes to a stable,
// non-empty value so a synced empty subtree remains distinguishable from a
// never-synced service.
func desiredAdapterKey(service *aimv1alpha1.AIMService, adapterDiskPVC, adapterDiskPVCUID string) string {
	paths := keepAdapterPaths(service)
	// NUL separates fields unambiguously because Kubernetes names cannot contain it.
	sum := sha256.Sum256([]byte(adapterDiskPVC + "\x00" + adapterDiskPVCUID + "\x00" + strings.Join(paths, ",")))
	return hex.EncodeToString(sum[:])[:12]
}

// ServiceSubtreeID returns the per-service subtree directory name on the shared
// adapter disk. The AIMService UID guarantees isolation across delete/recreate.
func ServiceSubtreeID(service *aimv1alpha1.AIMService) string {
	return string(service.UID)
}

// IsActive reports whether the adapter engine must run this cycle. It is true
// whenever the service needs the adapter disk (spec.AdaptersEnabled(): adapters
// declared, or dynamic mode — which mounts the disk even at zero adapters), AND
// while previously-staged adapters are still being reclaimed (status.adapters
// carries Deleting entries until their subtree-sync prune completes). Gating on
// status as well as the spec is what lets a removal — including dropping the last
// adapter in dynamic mode — drive its prune to completion instead of stranding
// bytes on the shared disk.
func IsActive(service *aimv1alpha1.AIMService) bool {
	return service.Spec.AdaptersEnabled() || len(service.Status.Adapters) > 0
}

// Fetch loads the resolved parent model artifact, then each declared adapter and
// the Jobs belonging to the current storage binding. parentName is resolved by
// the caller from its pipeline-specific cache (profile cache for v1alpha2,
// template cache for v1alpha1) and may be empty when the base model is not yet
// resolvable. A transient resolution gap uses the PVC persisted on service
// status so the controller continues observing the previous binding; a newly
// resolved PVC selects a fresh generation of staging and subtree-sync Jobs.
func Fetch(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	parentName string,
) Dependencies {
	deps := Dependencies{
		AdapterArtifacts:      make(map[string]controllerutils.FetchResult[*aimv1alpha1.AIMArtifact], len(service.Spec.Adapters)),
		AdapterRuntimeConfigs: make(map[string]controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon], len(service.Spec.Adapters)),
		StagingJobs:           make(map[string]controllerutils.FetchResult[*batchv1.Job], len(service.Spec.Adapters)),
	}

	adapterDiskPVC := service.Status.AdapterDiskPersistentVolumeClaim
	adapterDiskPVCUID := service.Status.AdapterDiskPersistentVolumeClaimUID
	if parentName != "" {
		pf := controllerutils.Fetch(ctx, c,
			client.ObjectKey{Namespace: service.Namespace, Name: parentName},
			&aimv1alpha1.AIMArtifact{})
		deps.ParentArtifact = &pf
		if pf.OK() && pf.Value != nil && pf.Value.Status.AdapterPersistentVolumeClaim != "" {
			adapterDiskPVC = pf.Value.Status.AdapterPersistentVolumeClaim
			if adapterDiskPVC != service.Status.AdapterDiskPersistentVolumeClaim {
				adapterDiskPVCUID = ""
			}
		}
	}

	if adapterDiskPVC != "" {
		pvcFetch := controllerutils.Fetch(ctx, c,
			client.ObjectKey{Namespace: service.Namespace, Name: adapterDiskPVC},
			&corev1.PersistentVolumeClaim{})
		if pvcFetch.OK() && pvcFetch.Value != nil {
			adapterDiskPVCUID = string(pvcFetch.Value.UID)
		}
	}
	deps.AdapterDiskPVC = adapterDiskPVC
	deps.AdapterDiskPVCUID = adapterDiskPVCUID

	for i := range service.Spec.Adapters {
		ref := service.Spec.Adapters[i]
		artifactFetch := controllerutils.Fetch(ctx, c,
			client.ObjectKey{Namespace: service.Namespace, Name: ref.Name},
			&aimv1alpha1.AIMArtifact{})
		deps.AdapterArtifacts[ref.Name] = artifactFetch
		if artifactFetch.OK() && artifactFetch.Value != nil &&
			strings.HasPrefix(artifactFetch.Value.Spec.SourceURI, "s3://") {
			configName := artifactFetch.Value.GetRuntimeConfigRef().Name
			deps.AdapterRuntimeConfigs[ref.Name] = aimruntimeconfig.FetchMergedRuntimeConfig(
				ctx,
				c,
				configName,
				service.Namespace,
			)
		}

		jobName := StagingJobName(service, ref.Name, adapterDiskPVC, adapterDiskPVCUID)
		jf := controllerutils.Fetch(ctx, c,
			client.ObjectKey{Namespace: service.Namespace, Name: jobName},
			&batchv1.Job{})
		deps.StagingJobs[ref.Name] = jf
	}

	deps.SubtreeSyncJob = controllerutils.Fetch(ctx, c,
		client.ObjectKey{Namespace: service.Namespace, Name: SubtreeSyncJobName(service, adapterDiskPVC, adapterDiskPVCUID)},
		&batchv1.Job{})

	return deps
}

// Compose validates the declared adapters against the resolved parent and
// computes each adapter's disk-side state.
func Compose(service *aimv1alpha1.AIMService, deps Dependencies) State {
	return ComposeWithRuntimeConfig(service, deps, nil, nil)
}

// ComposeWithRuntimeConfig validates adapters with namespace/cluster runtime
// defaults available for resolving the dynamic max-rank ceiling. A failed
// RuntimeConfig fetch leaves a dynamic service's last resolved ceiling in place;
// the caller's RuntimeConfig component health owns reporting and retrying the
// fetch error.
func ComposeWithRuntimeConfig(
	service *aimv1alpha1.AIMService,
	deps Dependencies,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	runtimeConfigFetchErr error,
) State {
	var st State

	// Resolve the parent adapter disk.
	var parent *aimv1alpha1.AIMArtifact
	if deps.ParentArtifact != nil && deps.ParentArtifact.OK() && deps.ParentArtifact.Value != nil {
		parent = deps.ParentArtifact.Value
		if parent.Spec.Type == aimv1alpha1.ArtifactTypeAdapter {
			st.ConfigErr = fmt.Errorf("resolved parent artifact %s is not a model artifact", parent.Name)
		}
	}

	// Fetch resolves the concrete PVC identity, falling back to the last
	// successfully synchronized status binding during transient parent gaps.
	st.AdapterDiskPVC = deps.AdapterDiskPVC
	st.AdapterDiskPVCUID = deps.AdapterDiskPVCUID
	if st.AdapterDiskPVC == "" {
		st.AdapterDiskPVC = service.Status.AdapterDiskPersistentVolumeClaim
		st.AdapterDiskPVCUID = service.Status.AdapterDiskPersistentVolumeClaimUID
	} else if st.AdapterDiskPVC == service.Status.AdapterDiskPersistentVolumeClaim &&
		st.AdapterDiskPVCUID == "" {
		st.AdapterDiskPVCUID = service.Status.AdapterDiskPersistentVolumeClaimUID
	}
	st.StorageBindingChanged = st.AdapterDiskPVC != service.Status.AdapterDiskPersistentVolumeClaim ||
		st.AdapterDiskPVCUID != service.Status.AdapterDiskPersistentVolumeClaimUID

	// Gate on the runtime contract: the image only loads adapters when its
	// effective profile advertises the LoRA feature. An explicit
	// spec.profileOverrides.features entry is applied before this validation.
	if err := profileAdapterSupportError(service, deps); err != nil {
		st.ConfigErr = err
	}

	// Surface a terminal base-model resolution error as ConfigValid=False.
	if deps.ParentResolutionErr != nil {
		st.ConfigErr = deps.ParentResolutionErr
	}

	// Subtree-sync bookkeeping (computed early so deletion tracking below can tell
	// whether a removed adapter's bytes have already been pruned). Compare the
	// declared set (DesiredKey) against the last set the sync Job reconciled
	// (SyncedKey, from status). The subtree exists once any sync has succeeded —
	// sticky via the recorded SyncedKey so a later add/remove never tears down a
	// running ISVC, with the current-cycle success used so the very first sync
	// gates promptly.
	st.DesiredKey = desiredAdapterKey(service, st.AdapterDiskPVC, st.AdapterDiskPVCUID)
	st.SyncedKey = service.Status.AdapterSubtreeSyncKey
	st.CurrentSyncSucceeded = subtreeSyncSucceeded(deps)
	st.SubtreeReady = st.AdapterDiskPVC != "" && st.AdapterDiskPVCUID != "" &&
		((!st.StorageBindingChanged && st.SyncedKey != "") || st.CurrentSyncSucceeded)

	// A removed adapter's bytes are gone once the sync Job for the current
	// (reduced) set has reconciled the subtree — either it succeeded this cycle or
	// status already records the current set as synced.
	pruneConfirmed := st.CurrentSyncSucceeded || st.SyncedKey == st.DesiredKey

	seenPaths := make(map[string]string, len(service.Spec.Adapters))
	specNames := make(map[string]struct{}, len(service.Spec.Adapters))
	st.Adapters = make([]Observation, 0, len(service.Spec.Adapters))

	for i := range service.Spec.Adapters {
		ref := service.Spec.Adapters[i]
		specNames[ref.Name] = struct{}{}

		ad, cfgErr := evalAdapter(ref, deps, parent)
		if cfgErr != nil {
			st.ConfigErr = cfgErr
		}
		if ad.AdapterPath != "" {
			if existing, dup := seenPaths[ad.AdapterPath]; dup {
				st.ConfigErr = fmt.Errorf("adapters %s and %s resolve to the same adapterPath %q", existing, ref.Name, ad.AdapterPath)
			}
			seenPaths[ad.AdapterPath] = ref.Name
		}
		st.Adapters = append(st.Adapters, ad)
	}

	var rankErr error
	st.MaxRank, rankErr = resolveValidatedMaxAdapterRank(
		service,
		st.Adapters,
		runtimeConfig,
		runtimeConfigFetchErr,
	)
	if st.ConfigErr == nil {
		st.ConfigErr = rankErr
	}

	st.Adapters = append(st.Adapters, deletingAdapters(service, specNames, pruneConfirmed)...)

	// AllStaged is informational (every declared adapter Downloaded). Deleting
	// entries are ignored so a removal in progress doesn't flip it false.
	downloaded, active := specStagingProgress(st.Adapters)
	st.AllStaged = st.ConfigErr == nil && active > 0 && downloaded == active

	// MountReady ("mountable"): config valid + disk PVC resolved + subtree present.
	st.MountReady = st.ConfigErr == nil && st.AdapterDiskPVC != "" &&
		st.AdapterDiskPVCUID != "" && st.SubtreeReady

	// Static mode must additionally wait for downloads (see Ready field doc).
	if service.Spec.AdapterModeDynamic() {
		st.Ready = st.MountReady
	} else {
		st.Ready = st.MountReady && st.AllStaged
	}

	return st
}

func resolveValidatedMaxAdapterRank(
	service *aimv1alpha1.AIMService,
	adapters []Observation,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	runtimeConfigFetchErr error,
) (int32, error) {
	if service.Spec.AdapterModeDynamic() && runtimeConfigFetchErr != nil {
		// The dynamic ceiling may come from RuntimeConfig, so a failed fetch
		// cannot be interpreted as "unset" and replaced by the built-in default.
		// Preserve the last resolved value until the fetch succeeds. The service
		// reconciler surfaces the fetch error and requeues infrastructure errors.
		return service.Status.AdapterMaxRank, nil
	}

	maxRank, err := resolveMaxAdapterRank(service, adapters, runtimeConfig)
	if err != nil {
		return maxRank, err
	}
	return maxRank, dynamicAdapterRankError(service, adapters, maxRank)
}

func profileAdapterSupportError(service *aimv1alpha1.AIMService, deps Dependencies) error {
	if service.Spec.AdaptersEnabled() && deps.ProfileSupportsAdapters != nil && !*deps.ProfileSupportsAdapters {
		return fmt.Errorf(
			"resolved profile does not advertise LoRA adapter support; select an adapter-capable profile or set spec.profileOverrides.features: [\"adapters\"] on the AIMService")
	}
	return nil
}

func dynamicAdapterRankError(
	service *aimv1alpha1.AIMService,
	adapters []Observation,
	maxRank int32,
) error {
	if !service.Spec.AdapterModeDynamic() {
		return nil
	}
	for _, ad := range adapters {
		if ad.Rank != nil && *ad.Rank > maxRank {
			return fmt.Errorf(
				"adapter %s rank %d exceeds dynamic adapterRuntime.maxRank %d; increase spec.adapterRuntime.maxRank or the referenced RuntimeConfig value",
				ad.Name, *ad.Rank, maxRank)
		}
	}
	return nil
}

// resolveMaxAdapterRank computes the startup-time rank capacity exposed to the
// inference image. Dynamic services need a ceiling that also covers future
// adapters, so the service setting wins over RuntimeConfig and then the
// conservative built-in default. Static services have an immutable adapter set
// and round the largest resolved adapter rank up to a runtime-supported ceiling.
func resolveMaxAdapterRank(
	service *aimv1alpha1.AIMService,
	adapters []Observation,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (int32, error) {
	if service.Spec.AdapterModeDynamic() {
		maxRank := int32(0)
		if service.Spec.AdapterRuntime != nil && service.Spec.AdapterRuntime.MaxRank != nil {
			maxRank = *service.Spec.AdapterRuntime.MaxRank
		} else if runtimeConfig != nil && runtimeConfig.AdapterRuntime != nil &&
			runtimeConfig.AdapterRuntime.MaxRank != nil {
			maxRank = *runtimeConfig.AdapterRuntime.MaxRank
		} else {
			maxRank = constants.DefaultAIMAdapterMaxRank
		}

		// Do not lower a running workload until every declared rank has been
		// resolved. A transient artifact lookup gap must not make an unsafe
		// decrease look valid.
		if maxRank < service.Status.AdapterMaxRank && hasUnresolvedRank(adapters) {
			return service.Status.AdapterMaxRank, nil
		}
		return maxRank, nil
	}

	// Static mode provisions exactly for the declared set. Recompute from the
	// current artifacts so a downward rank correction actually takes effect —
	// seeding from status.AdapterMaxRank would make the ceiling monotonic and a
	// 64 -> 16 change would never land.
	//
	// status is consulted only across a genuine lookup gap: if any adapter's rank
	// is unresolved this cycle, the recomputed value would be an undercount, so
	// the last resolved ceiling is retained rather than rolling a running
	// workload down to the compatibility default.
	var maxRank int32
	resolved := true
	for _, ad := range adapters {
		if !ad.RankResolved {
			resolved = false
			continue
		}
		if ad.Rank != nil && *ad.Rank > maxRank {
			maxRank = *ad.Rank
		}
	}
	if !resolved && service.Status.AdapterMaxRank > maxRank {
		maxRank = service.Status.AdapterMaxRank
	}
	if maxRank == 0 {
		maxRank = constants.DefaultAIMAdapterRank
	}
	// Keep these rounding ceilings synchronized with the MaxRank CEL allow-list
	// in api/v1alpha1/aimruntimeconfig_shared.go. CEL remains the API and
	// generated-documentation source of truth; this copy drives runtime rounding.
	for _, supported := range []int32{1, 8, 16, 32, 64, 128, 256, 320, 512} {
		if maxRank <= supported {
			return supported, nil
		}
	}
	return maxRank, fmt.Errorf("adapter rank %d exceeds the maximum supported rank 512", maxRank)
}

func hasUnresolvedRank(adapters []Observation) bool {
	for _, ad := range adapters {
		if !ad.RankResolved {
			return true
		}
	}
	return false
}

// evalAdapter resolves a single declared adapter against its artifact and the
// resolved parent, returning the observation and any terminal config error.
func evalAdapter(ref aimv1alpha1.AIMServiceAdapterReference, deps Dependencies, parent *aimv1alpha1.AIMArtifact) (Observation, error) {
	ad := Observation{
		Name:  ref.Name,
		Kind:  string(ref.Kind),
		State: aimv1alpha1.AdapterStatePending,
	}

	af, ok := deps.AdapterArtifacts[ref.Name]
	switch {
	case !ok || af.IsNotFound():
		ad.LastError = ReasonNotFound
		return ad, nil
	case af.HasError():
		ad.LastError = af.Error.Error()
		return ad, nil
	}

	artifact := af.Value
	var cfgErr error
	if artifact.Spec.Type != aimv1alpha1.ArtifactTypeAdapter {
		cfgErr = fmt.Errorf("artifact %s referenced as an adapter is type %q", ref.Name, artifact.Spec.Type)
	}
	if parent != nil {
		ad.BaseModelID = parent.Spec.ModelID
		switch {
		case artifact.Spec.ParentArtifact != "" && artifact.Spec.ParentArtifact != parent.Name:
			cfgErr = fmt.Errorf("adapter %s.parentArtifact (%s) does not match the service's resolved base model %s",
				ref.Name, artifact.Spec.ParentArtifact, parent.Name)
		case len(artifact.Spec.CompatibleWith) > 0 && !slices.Contains(artifact.Spec.CompatibleWith, parent.Spec.ModelID):
			cfgErr = fmt.Errorf("adapter %s is not compatible with the service's resolved base modelId %q",
				ref.Name, parent.Spec.ModelID)
		}
	}

	ad.SourceURI = effectiveAdapterSourceURI(artifact)
	ad.ModelID = artifact.Spec.ModelID
	rank := int32(constants.DefaultAIMAdapterRank)
	if artifact.Spec.Rank != nil {
		rank = *artifact.Spec.Rank
	}
	ad.Rank = ptr.To(rank)
	ad.RankResolved = true
	ad.AdapterPath = artifact.Status.AdapterPath
	if ad.AdapterPath == "" {
		ad.AdapterPath = artifact.Name
	}
	ad.State, ad.LastError = adapterDiskState(ref.Name, artifact, deps)
	return ad, cfgErr
}

// adapterDiskState derives an adapter's disk-side state: the artifact must be
// Ready (lineage validated) before staging, then the staging Job drives
// Downloading/Downloaded/Failed. Terminal Job failure details are returned for
// status.adapters[].lastError.
func adapterDiskState(
	name string,
	artifact *aimv1alpha1.AIMArtifact,
	deps Dependencies,
) (aimv1alpha1.AIMAdapterState, string) {
	if artifact.Status.Status != constants.AIMStatusReady {
		return aimv1alpha1.AdapterStatePending, ""
	}
	jf, jok := deps.StagingJobs[name]
	if !jok || !jf.OK() || jf.Value == nil {
		return aimv1alpha1.AdapterStatePending, ""
	}
	if utils.IsJobSucceeded(jf.Value) {
		return aimv1alpha1.AdapterStateDownloaded, ""
	}
	if utils.IsJobFailed(jf.Value) {
		return aimv1alpha1.AdapterStateFailed, stagingJobFailure(jf.Value)
	}
	return aimv1alpha1.AdapterStateDownloading, ""
}

func stagingJobFailure(job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type != batchv1.JobFailed || condition.Status != corev1.ConditionTrue {
			continue
		}
		reason := strings.TrimSpace(condition.Reason)
		message := strings.TrimSpace(condition.Message)
		switch {
		case reason != "" && message != "":
			return fmt.Sprintf("%s: %s", reason, message)
		case message != "":
			return message
		case reason != "":
			return reason
		default:
			return "Adapter staging Job failed"
		}
	}
	return "Adapter staging Job failed"
}

// deletingAdapters returns observations for adapters still recorded on status
// but no longer declared in spec. They stay visible as Deleting (keeping the
// engine working through a drop-to-zero removal) until the subtree-sync Job for
// the reduced set has pruned their bytes (pruneConfirmed).
func deletingAdapters(service *aimv1alpha1.AIMService, specNames map[string]struct{}, pruneConfirmed bool) []Observation {
	if pruneConfirmed {
		return nil
	}
	var out []Observation
	for i := range service.Status.Adapters {
		prev := service.Status.Adapters[i]
		if _, stillDeclared := specNames[prev.Name]; stillDeclared {
			continue
		}
		out = append(out, Observation{
			Name:        prev.Name,
			AdapterPath: prev.AdapterPath,
			ModelID:     prev.ModelID,
			State:       aimv1alpha1.AdapterStateDeleting,
		})
	}
	return out
}

// subtreeSyncSucceeded reports whether the subtree-sync Job for the current
// declared set has completed successfully.
func subtreeSyncSucceeded(deps Dependencies) bool {
	j := deps.SubtreeSyncJob
	return j.OK() && j.Value != nil && utils.IsJobSucceeded(j.Value)
}

// specStagingProgress counts Downloaded vs. total active (non-Deleting) adapters.
// Deleting entries are excluded so an in-flight removal never affects staging
// progress accounting.
func specStagingProgress(adapters []Observation) (downloaded, active int) {
	for _, ad := range adapters {
		if ad.State == aimv1alpha1.AdapterStateDeleting {
			continue
		}
		active++
		if ad.State == aimv1alpha1.AdapterStateDownloaded {
			downloaded++
		}
	}
	return downloaded, active
}

func effectiveAdapterSourceURI(artifact *aimv1alpha1.AIMArtifact) string {
	return artifact.Spec.SourceURI
}

// Health returns the single aggregate Adapters component health so the
// AIMService surfaces one accurate condition rather than a per-adapter
// explosion. numAdapters is the count declared in spec.adapters.
//
// Because adapters load dynamically, this component only gates serving up to
// the point the subtree exists: a config error is Failed, a missing disk or
// subtree is Progressing, and once the subtree is ready the component is Ready
// even while downloads are still in flight. Per-adapter download progress and
// errors are reported via status.adapters[] rather than degrading this
// condition (staging is best-effort/eventual).
func Health(st State, numAdapters int) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Adapters",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	if st.ConfigErr != nil {
		health.State = constants.AIMStatusFailed
		health.DependencyType = controllerutils.DependencyTypeUpstream
		health.Errors = []error{
			controllerutils.NewInvalidSpecError(ReasonInvalid, st.ConfigErr.Error(), st.ConfigErr),
		}
		return health
	}

	if st.AdapterDiskPVC == "" || st.AdapterDiskPVCUID == "" {
		health.State = constants.AIMStatusProgressing
		health.Reason = ReasonParentLacksDisk
		health.Message = "Base model artifact has no resolvable adapter disk yet"
		return health
	}

	if !st.SubtreeReady {
		health.State = constants.AIMStatusProgressing
		health.Reason = ReasonSubtreeProvisioning
		health.Message = "Provisioning the service adapter subtree"
		return health
	}

	// Subtree is mountable. Report Ready regardless of download progress so the
	// base model can serve; surface staging progress in the message only.
	health.State = constants.AIMStatusReady
	if numAdapters == 0 {
		health.Reason = ReasonStaged
		health.Message = "Adapter subtree ready; no adapters declared"
	} else if st.AllStaged {
		health.Reason = ReasonStaged
		health.Message = fmt.Sprintf("All %d adapter(s) staged", numAdapters)
	} else {
		health.Reason = ReasonStaging
		health.Message = fmt.Sprintf("Adapter subtree ready; %d/%d adapter(s) staged (loading asynchronously)",
			countDownloaded(st.Adapters), numAdapters)
	}
	return health
}

func countDownloaded(adapters []Observation) int {
	n := 0
	for _, ad := range adapters {
		if ad.State == aimv1alpha1.AdapterStateDownloaded {
			n++
		}
	}
	return n
}

// Plan provisions adapters without gating the ISVC: it ensures the per-service
// subtree exists (a fast mkdir Job, so the ISVC can mount it before downloads
// finish) and applies a staging Job for each ready-but-not-yet-Downloaded
// adapter. Staging runs asynchronously; the aim-runtime hot-loads each adapter
// as it lands. ISVC gating is the caller's concern (gate on State.Ready).
func Plan(
	planResult *controllerutils.PlanResult,
	service *aimv1alpha1.AIMService,
	deps Dependencies,
	st State,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	additionalCABundleAvailable ...bool,
) {
	if st.ConfigErr != nil || st.AdapterDiskPVC == "" || st.AdapterDiskPVCUID == "" {
		return
	}

	// Reconcile the per-service subtree toward the declared set: create it (so the
	// read-only subPath mount binds) and prune adapter directories that are no
	// longer declared. The Job name encodes DesiredKey, so a changed set yields a
	// fresh Job; recording SyncedKey on status stops it re-running once reconciled.
	if st.DesiredKey != st.SyncedKey && !jobPresent(deps.SubtreeSyncJob) {
		var parent *aimv1alpha1.AIMArtifact
		if deps.ParentArtifact != nil && deps.ParentArtifact.OK() {
			parent = deps.ParentArtifact.Value
		}
		planResult.Apply(BuildSubtreeSyncJob(
			service,
			parent,
			st.AdapterDiskPVC,
			st.AdapterDiskPVCUID,
			runtimeConfig,
		))
	}

	includeAdditionalCABundle := len(additionalCABundleAvailable) > 0 && additionalCABundleAvailable[0]

	// Stage adapters independently of the subtree gate (the staging Job creates
	// the subtree itself if needed before promoting).
	for _, ad := range st.Adapters {
		if ad.State == aimv1alpha1.AdapterStateDownloaded ||
			ad.State == aimv1alpha1.AdapterStateFailed ||
			ad.State == aimv1alpha1.AdapterStateDeleting {
			continue
		}

		af, ok := deps.AdapterArtifacts[ad.Name]
		if !ok || !af.OK() || af.Value == nil || af.Value.Status.Status != constants.AIMStatusReady {
			// Adapter artifact not ready; nothing to stage yet.
			continue
		}

		// One active staging Job per (service, adapter). If a Job already exists
		// (running or finished), leave it; convergence happens on the next event.
		if jf, jok := deps.StagingJobs[ad.Name]; jok && jobPresent(jf) {
			continue
		}

		stagingRuntimeConfig := runtimeConfig
		if configFetch, ok := deps.AdapterRuntimeConfigs[ad.Name]; ok {
			if configFetch.HasError() {
				continue
			}
			stagingRuntimeConfig = runtimeConfigForAdapterStaging(
				runtimeConfig,
				configFetch.Value,
			)
		}

		planResult.Apply(BuildStagingJob(
			service,
			af.Value,
			st.AdapterDiskPVC,
			st.AdapterDiskPVCUID,
			ad,
			stagingRuntimeConfig,
			includeAdditionalCABundle,
		))
	}
}

func runtimeConfigForAdapterStaging(
	serviceConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	adapterConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *aimv1alpha1.AIMRuntimeConfigCommon {
	var result *aimv1alpha1.AIMRuntimeConfigCommon
	if serviceConfig != nil {
		result = serviceConfig.DeepCopy()
	} else {
		result = &aimv1alpha1.AIMRuntimeConfigCommon{}
	}

	var adapterS3 *aimv1alpha1.S3ConnectionConfig
	if adapterConfig != nil {
		result.Env = utils.MergeEnvVars(result.Env, adapterConfig.Env)
		if adapterConfig.Artifact != nil {
			adapterS3 = adapterConfig.Artifact.S3
		}
	}
	if result.Artifact == nil {
		result.Artifact = &aimv1alpha1.AIMArtifactConfig{}
	}
	result.Artifact.S3 = adapterS3
	return result
}

func jobPresent(jf controllerutils.FetchResult[*batchv1.Job]) bool {
	return jf.OK() && jf.Value != nil
}

// SubtreeSyncJobName returns the name of the per-service subtree-sync Job. The
// name encodes the declared adapter set and adapter-disk PVC so either an
// adapter edit or a storage-binding change yields a fresh Job.
func SubtreeSyncJobName(service *aimv1alpha1.AIMService, adapterDiskPVC, adapterDiskPVCUID string) string {
	name, _ := utils.GenerateDerivedName(
		[]string{service.Name, "subtree-sync"},
		utils.WithHashSource(string(service.UID), desiredAdapterKey(service, adapterDiskPVC, adapterDiskPVCUID)),
	)
	return name
}

// BuildSubtreeSyncJob constructs the fast, idempotent AIMService-owned Job that
// reconciles the service's subtree on the shared adapter disk: it creates the
// per-service directory (so the InferenceService can mount it read-only before
// any adapter has downloaded) and prunes adapter directories no longer declared
// (unload). parent (the base-model artifact) is used only to resolve the
// downloader image; it may be nil, in which case the image falls back to the
// runtime config / default.
func BuildSubtreeSyncJob(
	service *aimv1alpha1.AIMService,
	parent *aimv1alpha1.AIMArtifact,
	adapterDiskPVC string,
	adapterDiskPVCUID string,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *batchv1.Job {
	image := aimartifact.ResolveDownloadImage(parent, runtimeConfig)

	env := []corev1.EnvVar{
		{Name: "ADAPTER_PVC_ROOT", Value: constants.AIMAdapterPVCRoot},
		{Name: "SERVICE_ID", Value: ServiceSubtreeID(service)},
		{Name: "KEEP_ADAPTER_PATHS", Value: strings.Join(keepAdapterPaths(service), ",")},
	}
	if runtimeConfig != nil {
		env = utils.MergeEnvVars(env, runtimeConfig.Env)
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	labels := map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
		constants.LabelKeyComponent: "adapter-subtree-sync",
	}

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      SubtreeSyncJobName(service, adapterDiskPVC, adapterDiskPVCUID),
			Namespace: service.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(3)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 60 * 24)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: service.Spec.ImagePullSecrets,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)),
						RunAsGroup:   ptr.To(int64(1000)),
						RunAsNonRoot: ptr.To(true),
						FSGroup:      ptr.To(int64(1000)),
					},
					Containers: []corev1.Container{
						{
							Name:            "adapter-subtree-sync",
							Image:           image,
							ImagePullPolicy: aimartifact.PullPolicyForImage(image),
							Command:         []string{"/adapter-subtree-sync.sh"},
							Env:             env,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      constants.VolumeAdapterDisk,
									MountPath: constants.AIMAdapterPVCRoot,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: constants.VolumeAdapterDisk,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: adapterDiskPVC,
								},
							},
						},
					},
				},
			},
		},
	}
}

// StagingJobName returns the deterministic staging Job name for a
// (service, adapter, adapter-disk PVC) binding.
func StagingJobName(service *aimv1alpha1.AIMService, adapterName, adapterDiskPVC, adapterDiskPVCUID string) string {
	name, _ := utils.GenerateDerivedName(
		[]string{service.Name, adapterName, "stage"},
		utils.WithHashSource(string(service.UID), adapterDiskPVC, adapterDiskPVCUID),
	)
	return name
}

// BuildStagingJob constructs the Job that downloads and atomically promotes a
// single adapter into the service's subtree on the shared adapter disk.
func BuildStagingJob(
	service *aimv1alpha1.AIMService,
	artifact *aimv1alpha1.AIMArtifact,
	adapterDiskPVC string,
	adapterDiskPVCUID string,
	ad Observation,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	additionalCABundleAvailable ...bool,
) *batchv1.Job {
	includeAdditionalCABundle := len(additionalCABundleAvailable) > 0 && additionalCABundleAvailable[0]
	image := aimartifact.ResolveDownloadImage(artifact, runtimeConfig)
	serviceID := ServiceSubtreeID(service)
	jobID, _ := utils.GenerateDerivedName([]string{ad.AdapterPath}, utils.WithHashSource(serviceID, ad.AdapterPath))

	rank := int32(constants.DefaultAIMAdapterRank)
	if ad.Rank != nil {
		rank = *ad.Rank
	}

	env := []corev1.EnvVar{
		{Name: "ADAPTER_PVC_ROOT", Value: constants.AIMAdapterPVCRoot},
		{Name: "SERVICE_ID", Value: serviceID},
		{Name: "ADAPTER_PATH", Value: ad.AdapterPath},
		{Name: "JOB_ID", Value: jobID},
		{Name: "ADAPTER_BASE_MODEL_ID", Value: ad.BaseModelID},
		{Name: "ADAPTER_RANK", Value: fmt.Sprintf("%d", rank)},
		// Non-root uid: HF/XET need a writable cache dir or XET fails with
		// "Permission denied" on $HOME/.cache. Mirrors the model download Job.
		{Name: "TMPDIR", Value: "/tmp/"},
		{Name: "HF_HOME", Value: "/tmp/.hf"},
	}
	var runtimeEnv []corev1.EnvVar
	if runtimeConfig != nil {
		runtimeEnv = runtimeConfig.Env
	}
	var s3Volumes []corev1.Volume
	var s3VolumeMounts []corev1.VolumeMount
	if strings.HasPrefix(ad.SourceURI, "s3://") {
		connection := aimartifact.DirectS3Connection(runtimeConfig)
		if connection != nil {
			image = aimv1alpha1.DefaultDownloadImage
		}
		settings := aimartifact.ResolveS3PodSettings(
			connection,
			env,
			runtimeEnv,
			artifact.Spec.Env,
		)
		env = settings.Env
		s3Volumes = settings.Volumes
		s3VolumeMounts = settings.VolumeMounts
	} else {
		env = utils.MergeEnvVars(env, runtimeEnv)
		// Adapter artifact env (auth tokens, AIM_DEBUG_* simulation switches) wins.
		env = utils.MergeEnvVars(env, artifact.Spec.Env)
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	labels := map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
		constants.LabelKeyComponent: "adapter-stage",
	}

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      StagingJobName(service, artifact.Name, adapterDiskPVC, adapterDiskPVCUID),
			Namespace: service.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(3)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 30)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: service.Spec.ImagePullSecrets,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)),
						RunAsGroup:   ptr.To(int64(1000)),
						RunAsNonRoot: ptr.To(true),
						FSGroup:      ptr.To(int64(1000)),
					},
					Containers: []corev1.Container{
						{
							Name:            "adapter-stage",
							Image:           image,
							ImagePullPolicy: aimartifact.PullPolicyForImage(image),
							Command:         []string{"/adapter-stage.sh"},
							Args:            []string{ad.SourceURI},
							Env:             env,
							VolumeMounts: append([]corev1.VolumeMount{
								{
									Name:      constants.VolumeAdapterDisk,
									MountPath: constants.AIMAdapterPVCRoot,
								},
							}, s3VolumeMounts...),
						},
					},
					Volumes: append([]corev1.Volume{
						{
							Name: constants.VolumeAdapterDisk,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: adapterDiskPVC,
								},
							},
						},
					}, s3Volumes...),
				},
			},
		},
	}
	trust.ApplyToPod(
		&job.Spec.Template.Spec,
		&job.Spec.Template.Spec.Containers[0],
		aimartifact.ResolveTrustProjection(runtimeConfig, ad.SourceURI, includeAdditionalCABundle, image),
	)
	return job
}

// AddVolumeMount mounts the shared adapter disk into the inference container,
// scoped read-only to this service's subtree via subPath at /adapters, and sets
// the adapter container-contract env vars on the container.
func AddVolumeMount(
	isvc *servingv1beta1.InferenceService,
	service *aimv1alpha1.AIMService,
	adapterDiskPVC string,
	maxRank int32,
) {
	container := predictorInferenceContainer(isvc)
	if adapterDiskPVC == "" || container == nil {
		return
	}

	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: constants.VolumeAdapterDisk,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: adapterDiskPVC,
				ReadOnly:  true,
			},
		},
	})

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      constants.VolumeAdapterDisk,
		MountPath: constants.AIMAdapterMountPath,
		SubPath:   ServiceSubtreeID(service),
		ReadOnly:  true,
	})

	// Set the adapter container contract on the inference container. AIM_ADAPTER_SOURCE
	// points the image at the mounted subtree; AIM_ADAPTER_MODE mirrors the service's
	// (lowercase) adapterMode; the MAX_* caps and the dynamic-only refresh
	// interval are emitted explicitly so they are observable in the pod spec.
	mode := service.Spec.AdapterMode
	if mode == "" {
		mode = aimv1alpha1.AdapterModeStatic
	}
	if maxRank < 1 {
		maxRank = constants.DefaultAIMAdapterMaxRank
	}
	env := []corev1.EnvVar{
		{Name: constants.EnvAIMAdapterSource, Value: constants.AIMAdapterMountPath},
		{Name: constants.EnvAIMAdapterMode, Value: string(mode)},
		{Name: constants.EnvAIMAdapterMaxCount, Value: strconv.Itoa(constants.DefaultAIMAdapterMaxCount)},
		{Name: constants.EnvAIMAdapterMaxCPUCount, Value: strconv.Itoa(constants.DefaultAIMAdapterMaxCPUCount)},
		{Name: constants.EnvAIMAdapterMaxRank, Value: strconv.Itoa(int(maxRank))},
	}
	if mode == aimv1alpha1.AdapterModeDynamic {
		env = append(env, corev1.EnvVar{
			Name:  constants.EnvAIMAdapterRefreshInterval,
			Value: strconv.Itoa(constants.DefaultAIMAdapterRefreshIntervalSeconds),
		})
	}
	container.Env = utils.MergeEnvVars(container.Env, env)
}

// predictorInferenceContainer returns the inference container the adapter
// wiring should target. The v1alpha1 template pipeline inlines an explicit
// kserve-container in Predictor.Containers; the v1alpha2 profile pipeline
// references a runtime and overlays the container via Predictor.Model, so the
// container fields live on the embedded ModelSpec container. Returns nil when
// neither is present.
func predictorInferenceContainer(isvc *servingv1beta1.InferenceService) *corev1.Container {
	if len(isvc.Spec.Predictor.Containers) > 0 {
		return &isvc.Spec.Predictor.Containers[0]
	}
	if isvc.Spec.Predictor.Model != nil {
		return &isvc.Spec.Predictor.Model.Container
	}
	return nil
}

// DynamicModeAllowed reports whether dynamic adapter mode is permitted for a
// namespace. Allowed by default; an explicit
// constants.LabelAdapterDynamicAllowed="false" label opts the namespace out.
func DynamicModeAllowed(nsLabels map[string]string) bool {
	return nsLabels[constants.LabelAdapterDynamicAllowed] != "false"
}

// PreserveExistingMount reports whether an existing InferenceService must keep
// its current adapter mount this cycle. This is true while the PVC identity is
// unresolved and while a replacement storage generation has not yet completed
// its subtree synchronization. Callers skip re-applying the ISVC so it never
// switches to a PVC before its subPath exists.
func PreserveExistingMount(service *aimv1alpha1.AIMService, st State) bool {
	return service.Spec.AdaptersEnabled() &&
		(st.AdapterDiskPVC == "" ||
			st.AdapterDiskPVCUID == "" ||
			(st.StorageBindingChanged && !st.CurrentSyncSucceeded))
}

// DecorateStatus mirrors the disk-side adapter state onto the AIMService.
func DecorateStatus(status *aimv1alpha1.AIMServiceStatus, st State) {
	now := metav1.Now()
	adapters := make([]aimv1alpha1.AIMServiceAdapterStatus, 0, len(st.Adapters))
	for _, ad := range st.Adapters {
		adapters = append(adapters, aimv1alpha1.AIMServiceAdapterStatus{
			Name:         ad.Name,
			AdapterPath:  ad.AdapterPath,
			ModelID:      ad.ModelID,
			State:        ad.State,
			LastObserved: &now,
			LastError:    ad.LastError,
		})
	}
	status.Adapters = adapters
	// Report the valid startup ceiling resolved for this service. Configuration
	// errors preserve the last valid value.
	if st.MaxRank > 0 && st.ConfigErr == nil {
		status.AdapterMaxRank = st.MaxRank
	}

	// Record the declared set and concrete PVC generation once its subtree-sync
	// Job succeeds. Until then status continues to describe the mountable
	// generation currently used by an existing InferenceService.
	if st.CurrentSyncSucceeded {
		status.AdapterSubtreeSyncKey = st.DesiredKey
		status.AdapterDiskPersistentVolumeClaim = st.AdapterDiskPVC
		status.AdapterDiskPersistentVolumeClaimUID = st.AdapterDiskPVCUID
	}
}
