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

package aimartifact

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ArtifactReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
	APIReader client.Reader
	Recorder  record.EventRecorder
}

type ArtifactFetchResult struct {
	artifact *aimv1alpha1.AIMArtifact

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	cachePvc            controllerutils.FetchResult[*corev1.PersistentVolumeClaim]

	// Check-size job (fetched when spec.size is empty and not yet discovered)
	checkSizeJob     *controllerutils.FetchResult[*batchv1.Job]
	checkSizeJobPods *controllerutils.FetchResult[*corev1.PodList]
	checkSizeOutput  string // Last log line from check-size container

	// Download job pods (existing)
	downloadJob     *controllerutils.FetchResult[*batchv1.Job]
	downloadJobPods *controllerutils.FetchResult[*corev1.PodList]

	// roleBinding stores the role binding for updating the artifact status
	roleBinding controllerutils.FetchResult[*rbacv1.RoleBinding]

	// Quota-related fetches (populated when PVC not yet created)
	namespace          *controllerutils.FetchResult[*corev1.Namespace]
	clusterConfig      *controllerutils.FetchResult[*aimv1alpha1.AIMClusterRuntimeConfig]
	namespaceArtifacts *controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]
	clusterArtifacts   *controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]
	templateCaches     *controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCacheList]
}

type checkSizeOutput struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"sizeBytes"`
}

func parseCheckSizeOutput(logLine string) (*int64, error) {
	if logLine == "" {
		return nil, fmt.Errorf("no output from check-size job")
	}

	var output checkSizeOutput
	if err := json.Unmarshal([]byte(logLine), &output); err != nil {
		return nil, fmt.Errorf("failed to parse check-size output: %w", err)
	}

	const maxReasonableSize = 1 << 50 // 1 PB - no model should be larger

	if output.SizeBytes <= 0 || output.SizeBytes > maxReasonableSize {
		return nil, fmt.Errorf("invalid size: %d bytes", output.SizeBytes)
	}

	return &output.SizeBytes, nil
}

func (obs ArtifactObservation) IsSizeKnown() bool {
	mc := obs.artifact
	return !mc.Spec.Size.IsZero() || obs.discoveredSizeBytes != nil || mc.Status.DiscoveredSizeBytes != nil
}

func (obs ArtifactObservation) GetEffectiveSize() int64 {
	mc := obs.artifact
	if !mc.Spec.Size.IsZero() {
		return mc.Spec.Size.Value()
	}
	if obs.discoveredSizeBytes != nil {
		return *obs.discoveredSizeBytes
	}
	if mc.Status.DiscoveredSizeBytes != nil {
		return *mc.Status.DiscoveredSizeBytes
	}
	return 0
}

// CheckSizeJobSucceeded returns true if the check-size job completed successfully
func (result ArtifactFetchResult) CheckSizeJobSucceeded() bool {
	if result.checkSizeJob == nil {
		return false
	}
	return utils.IsJobSucceeded(result.checkSizeJob.Value)
}

// getLastLogLine retrieves the last line from a container's logs
func (r *ArtifactReconciler) getLastLogLine(ctx context.Context, namespace, podName, containerName string) string {
	if r.Clientset == nil {
		return ""
	}

	// Request only the last few lines for efficiency (tail lines)
	tailLines := int64(1)
	req := r.Clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		// Silently ignore errors - pod may not be ready yet or logs not available
		return ""
	}
	defer func(stream io.ReadCloser) {
		_ = stream.Close()
	}(stream)

	// Read the last line
	scanner := bufio.NewScanner(stream)
	var lastLine string
	for scanner.Scan() {
		lastLine = scanner.Text()
	}

	return strings.TrimSpace(lastLine)
}

func (r *ArtifactReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact],
) ArtifactFetchResult {
	mc := reconcileCtx.Object
	downloadJobName := getDownloadJobName(mc)
	downloadJob := &batchv1.Job{}
	downloadJobPods := &corev1.PodList{}

	result := ArtifactFetchResult{
		artifact:            mc,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
		cachePvc: controllerutils.Fetch(
			ctx, c,
			client.ObjectKey{Name: GenerateCachePvcName(mc), Namespace: mc.Namespace},
			&corev1.PersistentVolumeClaim{},
		),
		roleBinding: controllerutils.Fetch(
			ctx, c,
			client.ObjectKey{Name: "aim-engine-artifact-status-updater", Namespace: mc.Namespace},
			&rbacv1.RoleBinding{},
		),
	}

	// Fetch check-size job if size not in spec AND not yet discovered
	if mc.Spec.Size.IsZero() && mc.Status.DiscoveredSizeBytes == nil {
		checkSizeJobName := getCheckSizeJobName(mc)
		checkSizeJob := &batchv1.Job{}

		checkSizeJobFetchResult := controllerutils.Fetch(
			ctx, c,
			client.ObjectKey{Name: checkSizeJobName, Namespace: mc.Namespace},
			checkSizeJob,
		)
		result.checkSizeJob = &checkSizeJobFetchResult

		// Fetch pods if job exists
		if !checkSizeJobFetchResult.IsNotFound() && !checkSizeJobFetchResult.HasError() {
			checkSizeJobPods := &corev1.PodList{}
			podsFetchResult := controllerutils.FetchList(
				ctx, c,
				checkSizeJobPods,
				client.InNamespace(mc.Namespace),
				client.MatchingLabels{"job-name": checkSizeJobName},
			)
			result.checkSizeJobPods = &podsFetchResult

			// Get output from completed/running pod
			if len(checkSizeJobPods.Items) > 0 {
				for i := range checkSizeJobPods.Items {
					pod := &checkSizeJobPods.Items[i]
					if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodRunning {
						logLine := r.getLastLogLine(ctx, pod.Namespace, pod.Name, "check-size")
						if logLine != "" {
							result.checkSizeOutput = logLine
						}
						break
					}
				}
			}
		}
	}

	// Always fetch the download job to determine if it succeeded
	// We need this to transition from Progressing to Ready
	downloadJobFetchResult := controllerutils.Fetch(
		ctx, c,
		client.ObjectKey{Name: downloadJobName, Namespace: reconcileCtx.Object.Namespace},
		downloadJob,
	)
	result.downloadJob = &downloadJobFetchResult

	// Only fetch pods if the job exists and hasn't succeeded yet
	// Once the job succeeds, we don't need to track pods anymore
	if !downloadJobFetchResult.IsNotFound() && !downloadJobFetchResult.HasError() {
		// Only fetch pods if the job hasn't succeeded yet
		if !utils.IsJobSucceeded(downloadJobFetchResult.Value) {
			downloadJobPodsFetchResult := controllerutils.FetchList(
				ctx, c,
				downloadJobPods,
				client.InNamespace(reconcileCtx.Object.Namespace),
				client.MatchingLabels{"job-name": downloadJobName},
			)
			result.downloadJobPods = &downloadJobPodsFetchResult
		}
	}

	// Quota data: always fetch when PVC does not yet exist.
	// This ensures quota evaluation runs on the same cycle as size discovery,
	// preventing PVC creation from bypassing quota checks. ComposeState skips
	// evaluation when size is not yet known (no projected size to check against).
	//
	// Reads use the API reader (bypasses informer cache) so that quota evaluation
	// under the distributed lock always sees the latest state. Without this, a
	// concurrent reconciler that just created a PVC might not yet be visible in
	// the cache, leading to a quota overshoot.
	if result.cachePvc.IsNotFound() {
		ar := r.APIReader

		nsFetch := controllerutils.FetchDirect(ctx, ar,
			client.ObjectKey{Name: mc.Namespace}, &corev1.Namespace{})
		result.namespace = &nsFetch

		configName := mc.GetRuntimeConfigRef().Name
		if configName == "" {
			configName = constants.DefaultRuntimeConfigName
		}
		clCfgFetch := controllerutils.FetchDirect(ctx, ar,
			client.ObjectKey{Name: configName}, &aimv1alpha1.AIMClusterRuntimeConfig{})
		result.clusterConfig = &clCfgFetch

		nsArtifacts := controllerutils.FetchListDirect(ctx, ar,
			&aimv1alpha1.AIMArtifactList{}, client.InNamespace(mc.Namespace))
		result.namespaceArtifacts = &nsArtifacts

		// Fetch cluster-wide artifact list only when a cluster quota is configured
		if !clCfgFetch.HasError() && !clCfgFetch.IsNotFound() &&
			clCfgFetch.Value.Spec.ArtifactStorageQuota != nil &&
			clCfgFetch.Value.Spec.ArtifactStorageQuota.ClusterLimit != nil {
			clArtifacts := controllerutils.FetchListDirect(ctx, ar,
				&aimv1alpha1.AIMArtifactList{})
			result.clusterArtifacts = &clArtifacts
		}

		// Fetch template caches to identify in-use artifacts (whose PVCs may be mounted).
		// When cluster quota is active, eviction can span namespaces, so we need
		// template caches from all namespaces to protect in-use artifacts everywhere.
		if result.clusterArtifacts != nil {
			tcFetch := controllerutils.FetchListDirect(ctx, ar, &aimv1alpha1.AIMTemplateCacheList{})
			result.templateCaches = &tcFetch
		} else {
			tcFetch := controllerutils.FetchListDirect(ctx, ar,
				&aimv1alpha1.AIMTemplateCacheList{}, client.InNamespace(mc.Namespace))
			result.templateCaches = &tcFetch
		}
	}

	return result
}

func (obs ArtifactObservation) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	health := []controllerutils.ComponentHealth{
		obs.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
	}

	// Phase 1: Check-size job health (when discovering size)
	if obs.checkSizeJob != nil {
		health = append(health,
			obs.checkSizeJob.ToDownstreamComponentHealth("CheckSizeJob", controllerutils.GetJobHealth))
		if obs.checkSizeJobPods != nil {
			health = append(health,
				obs.checkSizeJobPods.ToComponentHealthWithContext(ctx, clientset, "CheckSizeJobPods", controllerutils.GetPodsHealth))
		}

		if obs.CheckSizeJobSucceeded() && obs.sizeParseError != nil {
			health = append(health, controllerutils.ComponentHealth{
				Component:      "CheckSizeOutput",
				State:          constants.AIMStatusFailed,
				Reason:         "InvalidSizeOutput",
				Message:        obs.sizeParseError.Error(),
				Errors:         []error{obs.sizeParseError},
				DependencyType: controllerutils.DependencyTypeDownstream,
			})
		}
	}

	// Quota gate: always report StorageQuota health when quota was evaluated so the
	// condition transitions cleanly between blocked and allowed states.
	if obs.quotaDecision != nil {
		if obs.quotaDecision.Blocked {
			reason := aimv1alpha1.ArtifactReasonNamespaceQuotaExceeded
			if obs.quotaDecision.ClusterExceeded {
				reason = aimv1alpha1.ArtifactReasonClusterQuotaExceeded
			}
			health = append(health, controllerutils.ComponentHealth{
				Component:      "StorageQuota",
				State:          constants.AIMStatusFailed,
				Reason:         reason,
				Message:        obs.quotaDecision.BlockReason,
				DependencyType: controllerutils.DependencyTypeDownstream,
			})
			return health
		}
		health = append(health, controllerutils.ComponentHealth{
			Component:      "StorageQuota",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.ArtifactReasonWithinQuota,
			Message:        "Storage usage is within quota limits",
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	}

	// Phase 2+: PVC and download job health (only after size is known)
	if obs.IsSizeKnown() {
		health = append(health,
			obs.cachePvc.ToDownstreamComponentHealth("CachePvc", controllerutils.GetPvcHealth))

		if obs.artifact.Status.Status != constants.AIMStatusReady {
			if obs.downloadJob != nil {
				health = append(health,
					obs.downloadJob.ToDownstreamComponentHealth("DownloadJob", controllerutils.GetJobHealth))
			}
			if obs.downloadJobPods != nil {
				health = append(health,
					obs.downloadJobPods.ToComponentHealthWithContext(ctx, clientset, "DownloadJobPods", controllerutils.GetPodsHealth))
			}
		}
	}

	return health
}

func (result ArtifactFetchResult) DownloadJobSucceeded() bool {
	if result.downloadJob == nil {
		return false
	}
	// Check if the job itself succeeded (not the old status)
	// The old status check (Status == Ready) caused issues where we'd show 100% even when failing
	return utils.IsJobSucceeded(result.downloadJob.Value)
}

// Observe (thin wrapper for now, may be removed later)

type ArtifactObservation struct {
	ArtifactFetchResult

	// Discovered size bytes and parse error from check-size job
	discoveredSizeBytes *int64
	sizeParseError      error

	// Cache check result: true = cache hit, false = miss or not checked
	cacheHit bool
	// The resolved S3 path for cache hits
	resolvedCacheURI string

	quotaDataFetched bool

	// Quota evaluation result (populated when quota data is available)
	quotaDecision *QuotaDecision
}

func (r *ArtifactReconciler) ComposeState(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact],
	fetch ArtifactFetchResult,
) ArtifactObservation {
	logger := log.FromContext(ctx)
	obs := ArtifactObservation{ArtifactFetchResult: fetch}

	// Direct S3 cache check when source is hf:// and not yet resolved
	mc := fetch.artifact
	runtimeConfig := fetch.mergedRuntimeConfig.Value
	if strings.HasPrefix(mc.Spec.SourceURI, "hf://") && mc.Status.ResolvedSourceURI == "" && runtimeConfig != nil {
		filter := resolveDownloadFilter(mc, runtimeConfig)
		resolvedURI, err := CheckCacheHit(ctx, runtimeConfig.ArtifactCache, mc.Spec.SourceURI, filter)
		if err != nil {
			logger.Error(err, "S3 cache check failed, proceeding without cache",
				"namespace", mc.Namespace, "name", mc.Name)
		} else if resolvedURI != "" {
			obs.cacheHit = true
			obs.resolvedCacheURI = resolvedURI
			logger.Info("S3 cache hit for artifact",
				"namespace", mc.Namespace, "name", mc.Name,
				"resolvedUri", resolvedURI)
		}
	}

	// Parse check-size output if job succeeded
	if fetch.CheckSizeJobSucceeded() && fetch.checkSizeOutput != "" {
		size, err := parseCheckSizeOutput(fetch.checkSizeOutput)
		if err != nil {
			logger.Error(err, "Failed to parse check-size output",
				"output", fetch.checkSizeOutput,
				"namespace", fetch.artifact.Namespace,
				"name", fetch.artifact.Name)
			obs.sizeParseError = err
		} else {
			obs.discoveredSizeBytes = size
		}
	}

	// If fetch.namespace is non-nil, we are on the execution path that fetches quota-related data.
	obs.quotaDataFetched = fetch.namespace != nil

	// Evaluate quota when quota data is available
	if fetch.namespaceArtifacts != nil && !fetch.namespaceArtifacts.HasError() &&
		fetch.namespace != nil && !fetch.namespace.HasError() {

		runtimeConfig := fetch.mergedRuntimeConfig.Value
		headroomPercent := utils.GetPVCHeadroomPercent(runtimeConfig)

		mc := fetch.artifact
		effectiveSize := int64(0)
		if !mc.Spec.Size.IsZero() {
			effectiveSize = mc.Spec.Size.Value()
		} else if obs.discoveredSizeBytes != nil {
			effectiveSize = *obs.discoveredSizeBytes
		} else if mc.Status.DiscoveredSizeBytes != nil {
			effectiveSize = *mc.Status.DiscoveredSizeBytes
		}

		// Only evaluate quota when we have a concrete size to check against.
		// When size is still unknown (check-size job running), we skip evaluation
		// and PlanResources stays in Phase 1 (size discovery).
		if effectiveSize > 0 {
			projectedSize := utils.ApplyHeadroomAndRound(effectiveSize, headroomPercent)

			var clusterQuotaCfg *aimv1alpha1.AIMArtifactStorageQuota
			if fetch.clusterConfig != nil && !fetch.clusterConfig.HasError() && !fetch.clusterConfig.IsNotFound() {
				clusterQuotaCfg = fetch.clusterConfig.Value.Spec.ArtifactStorageQuota
			}

			var clusterArtifacts []aimv1alpha1.AIMArtifact
			if fetch.clusterArtifacts != nil && !fetch.clusterArtifacts.HasError() {
				clusterArtifacts = fetch.clusterArtifacts.Value.Items
			}

			var inUseUIDs map[types.UID]bool
			if fetch.templateCaches != nil && !fetch.templateCaches.HasError() {
				inUseUIDs = BuildInUseArtifactUIDs(fetch.templateCaches.Value.Items)
			}

			var defaultRetentionPriority *int32
			if runtimeConfig != nil && runtimeConfig.Artifact != nil {
				defaultRetentionPriority = runtimeConfig.Artifact.DefaultRetentionPriority
			}

			dec := EvaluateQuota(
				mc.UID,
				projectedSize,
				fetch.namespaceArtifacts.Value.Items,
				clusterArtifacts,
				fetch.namespace.Value,
				clusterQuotaCfg,
				headroomPercent,
				defaultRetentionPriority,
				inUseUIDs,
			)
			obs.quotaDecision = &dec
		}
	}

	return obs
}

// resolveCacheEnv returns cache config env vars when the source was rewritten to S3.
func resolveCacheEnv(mc *aimv1alpha1.AIMArtifact, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) []corev1.EnvVar {
	if mc.Status.ResolvedSourceURI == "" || runtimeConfig == nil {
		return nil
	}
	if cc := runtimeConfig.ArtifactCache; cc != nil {
		return cc.Env
	}
	return nil
}

func (r *ArtifactReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact],
	obs ArtifactObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx)
	mc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	// Use runtime config if available, otherwise use nil (functions should handle defaults)
	runtimeConfig := reconcileCtx.MergedRuntimeConfig.Value

	// Phase 0: Rolebinding creation - if not found
	if obs.roleBinding.IsNotFound() {
		roleBinding := buildRoleBinding(mc)
		result.ApplyWithoutOwnerRef(roleBinding)
	}

	cacheEnv := resolveCacheEnv(mc, runtimeConfig)

	// Phase 1: Size discovery (when spec.size is empty)
	if !obs.IsSizeKnown() {
		if obs.checkSizeJob != nil && obs.checkSizeJob.IsNotFound() {
			checkSizeJob := buildCheckSizeJob(mc, runtimeConfig, cacheEnv...)
			result.Apply(checkSizeJob)
		}
		// Don't proceed until size is known
		return result
	}

	// Phase 2: Quota gate + PVC creation - size is known.
	// When size was just discovered from the check-size job in this cycle,
	// defer PVC creation so discoveredSizeBytes is persisted to status first.
	// This ensures NeedsQuotaLock acquires the lock on the next reconcile,
	// serializing quota evaluation and PVC creation.
	if obs.cachePvc.IsNotFound() {
		if obs.discoveredSizeBytes != nil && mc.Status.DiscoveredSizeBytes == nil {
			logger.Info("Size just discovered, deferring PVC creation to next cycle",
				"namespace", mc.Namespace, "name", mc.Name,
				"discoveredSizeBytes", *obs.discoveredSizeBytes)
			result.RequeueAfter = 1 * time.Second
			return result
		}

		// Quota evaluation is mandatory before PVC creation. If we entered the
		// quota-evaluation path but no decision was reached, requeue rather than
		// bypassing the gate.
		if obs.quotaDataFetched && obs.quotaDecision == nil {
			logger.Info("Quota evaluation incomplete, requeueing before PVC creation",
				"namespace", mc.Namespace, "name", mc.Name)
			result.RequeueAfter = 5 * time.Second
			return result
		}

		if obs.quotaDecision != nil && (obs.quotaDecision.NamespaceExceeded || obs.quotaDecision.ClusterExceeded) {
			if len(obs.quotaDecision.ToEvict) > 0 {
				for i := range obs.quotaDecision.ToEvict {
					evicted := &obs.quotaDecision.ToEvict[i]
					logger.Info("Evicting artifact to free storage quota",
						"evicted", evicted.Name,
						"evictedNamespace", evicted.Namespace,
						"retentionPriority", effectiveRetentionPriority(evicted, obs.quotaDecision.DefaultRetentionPriority),
						"forArtifact", mc.Name)
					if r.Recorder != nil {
						r.Recorder.Eventf(evicted, corev1.EventTypeWarning, "Evicted",
							"Evicted to free storage quota for artifact %s/%s", mc.Namespace, mc.Name)
					}
					result.Delete(evicted)
				}
				result.RequeueAfter = 10 * time.Second
				return result
			}
			// Blocked: cannot evict enough to satisfy quota. PVC creation is skipped.
			// The StorageQuotaExceeded condition is set in DecorateStatus.
			// Requeue periodically so we pick up config changes (e.g., raised quota,
			// new defaultRetentionPriority, deleted artifacts) without needing
			// explicit watches for every possible config source.
			logger.Info("Artifact blocked by storage quota",
				"reason", obs.quotaDecision.BlockReason,
				"namespace", mc.Namespace,
				"name", mc.Name)
			result.RequeueAfter = 30 * time.Second
			return result
		}

		headroomPercent := utils.GetPVCHeadroomPercent(runtimeConfig)
		storageClassName := utils.ResolveStorageClass(mc.Spec.StorageClassName, runtimeConfig)
		effectiveSize := obs.GetEffectiveSize()
		pvcSize := utils.QuantityWithHeadroom(effectiveSize, headroomPercent)

		pvc := buildCachePvc(mc, pvcSize, storageClassName)
		result.Apply(pvc)
		return result
	}

	// Phase 3: Download job creation - size is known and PVC, rolebinding exists
	if mc.Status.Status != constants.AIMStatusReady &&
		obs.downloadJob != nil && obs.downloadJob.IsNotFound() && obs.roleBinding.OK() {
		downloadJob := buildDownloadJob(mc, runtimeConfig, obs.GetEffectiveSize(), cacheEnv...)
		result.Apply(downloadJob)
	}

	return result
}

// DecorateStatus implements StatusDecorator to update download status and add ArtifactMode to the status
func (r *ArtifactReconciler) DecorateStatus(
	status *aimv1alpha1.AIMArtifactStatus,
	cm *controllerutils.ConditionManager,
	obs ArtifactObservation,
) {
	// Set Mode based on owner references
	// Dedicated: has owner references, will be garbage collected with owners
	// Shared: no owner references, persists independently
	if len(obs.artifact.GetOwnerReferences()) > 0 {
		status.Mode = aimv1alpha1.ArtifactModeDedicated
	} else {
		status.Mode = aimv1alpha1.ArtifactModeShared
	}

	mc := obs.artifact
	runtimeConfig := obs.mergedRuntimeConfig.Value

	// Persist cache resolution to status
	if obs.cacheHit && obs.resolvedCacheURI != "" {
		status.ResolvedSourceURI = obs.resolvedCacheURI
	}

	if obs.discoveredSizeBytes != nil {
		status.DiscoveredSizeBytes = obs.discoveredSizeBytes
	}

	// Set display size from spec or discovered
	if !mc.Spec.Size.IsZero() {
		// Convert spec size to bytes, then format with two significant digits
		sizeBytes, ok := mc.Spec.Size.AsInt64()
		if ok {
			if formatted, err := utils.FormatBytesHumanReadable(sizeBytes); err == nil {
				status.DisplaySize = formatted
			} else {
				// Fallback on formatting error (negative or too large)
				status.DisplaySize = mc.Spec.Size.String()
			}
		} else {
			// Fallback for very large values that don't fit in int64
			status.DisplaySize = mc.Spec.Size.String()
		}
	} else if status.DiscoveredSizeBytes != nil {
		if formatted, err := utils.FormatBytesHumanReadable(*status.DiscoveredSizeBytes); err == nil {
			status.DisplaySize = formatted
		}
		// On error, leave DisplaySize empty (invalid discovered size)
	}

	// Store allocated size and headroom when PVC is created
	if obs.cachePvc.OK() && status.AllocatedSize.IsZero() {
		pvc := obs.cachePvc.Value
		if pvc.Spec.Resources.Requests != nil {
			if qty, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
				status.AllocatedSize = qty
			}
		}
		headroom := utils.GetPVCHeadroomPercent(runtimeConfig)
		status.HeadroomPercent = &headroom
	}

	// Set PVC name in status when PVC exists
	if !obs.cachePvc.IsNotFound() && obs.cachePvc.Value != nil && status.PersistentVolumeClaim == "" {
		status.PersistentVolumeClaim = obs.cachePvc.Value.Name
	}

	// Check if the pod has failed (before the job is marked as failed by k8s)
	// This handles the window between pod failure and job failure status
	podFailed := false
	if obs.downloadJobPods != nil && !obs.downloadJobPods.IsNotFound() && obs.downloadJobPods.Value != nil {
		for _, pod := range obs.downloadJobPods.Value.Items {
			if pod.Status.Phase == corev1.PodFailed {
				podFailed = true
				break
			}
		}
	}

	// --- Quota condition tracking ---
	r.decorateQuotaCondition(cm, obs)

	// --- Download phase tracking ---

	r.decorateDownloadPhase(status, cm, obs, podFailed)
}

func (r *ArtifactReconciler) decorateQuotaCondition(
	cm *controllerutils.ConditionManager,
	obs ArtifactObservation,
) {
	if obs.quotaDecision == nil {
		return
	}

	dec := obs.quotaDecision

	// Append config warning to messages so it's visible in status
	warnSuffix := ""
	if dec.ConfigWarning != "" {
		warnSuffix = " (warning: " + dec.ConfigWarning + ")"
	}

	if dec.Blocked {
		reason := aimv1alpha1.ArtifactReasonNamespaceQuotaExceeded
		if dec.ClusterExceeded {
			reason = aimv1alpha1.ArtifactReasonClusterQuotaExceeded
		}
		cm.MarkTrue(aimv1alpha1.ArtifactConditionStorageQuotaExceeded,
			reason, dec.BlockReason+warnSuffix, controllerutils.AsWarning())
		return
	}

	if len(dec.ToEvict) > 0 {
		cm.MarkTrue(aimv1alpha1.ArtifactConditionStorageQuotaExceeded,
			aimv1alpha1.ArtifactReasonEvicting,
			fmt.Sprintf("Evicting %d artifact(s) to free storage quota", len(dec.ToEvict))+warnSuffix,
			controllerutils.AsWarning())
		return
	}

	if dec.NamespaceQuota != nil || dec.ClusterQuota != nil {
		msg := "Storage usage is within quota limits"
		if warnSuffix != "" {
			msg += warnSuffix
		}
		cm.MarkFalse(aimv1alpha1.ArtifactConditionStorageQuotaExceeded,
			aimv1alpha1.ArtifactReasonWithinQuota, msg)
		return
	}

	if dec.ConfigWarning != "" {
		cm.MarkFalse(aimv1alpha1.ArtifactConditionStorageQuotaExceeded,
			"ConfigWarning", dec.ConfigWarning, controllerutils.AsWarning())
	}
}

func (r *ArtifactReconciler) decorateDownloadPhase(
	status *aimv1alpha1.AIMArtifactStatus,
	cm *controllerutils.ConditionManager,
	obs ArtifactObservation,
	podFailed bool,
) {
	// The download pod sets progress to 100% after the download finishes (before verification).
	// The controller uses this signal to derive the DownloadComplete condition.
	jobExists := obs.downloadJob != nil && !obs.downloadJob.IsNotFound() && obs.downloadJob.Value != nil
	jobSucceeded := obs.DownloadJobSucceeded()
	jobFailed := jobExists && utils.IsJobFailed(obs.downloadJob.Value)

	// Only manage DownloadComplete when the download job exists
	if jobExists {
		downloadProgressAt100 := status.Progress != nil && status.Progress.Percentage >= 100

		switch {
		case jobSucceeded:
			// Job done (download + verification) → both DownloadComplete=True, progress=100%
			cm.MarkTrue(aimv1alpha1.ArtifactConditionDownloadComplete,
				aimv1alpha1.ArtifactReasonVerified,
				"Download and verification complete",
				controllerutils.WithNormalEvent())

			expectedSize := obs.GetEffectiveSize()
			status.Progress = &aimv1alpha1.DownloadProgress{
				TotalBytes:        expectedSize,
				DownloadedBytes:   expectedSize,
				Percentage:        100,
				DisplayPercentage: "100 %",
			}
			if status.Download != nil {
				status.Download.Message = "Complete"
			}
			return

		case podFailed || jobFailed:
			// Failed → DownloadComplete=False, progress=N/A
			if downloadProgressAt100 {
				// Download succeeded but verification failed
				cm.MarkFalse(aimv1alpha1.ArtifactConditionDownloadComplete,
					"VerificationFailed",
					"Download completed but verification failed",
					controllerutils.AsWarning())
			} else {
				cm.MarkFalse(aimv1alpha1.ArtifactConditionDownloadComplete,
					aimv1alpha1.ArtifactReasonDownloading,
					"Download failed before completion",
					controllerutils.AsWarning())
			}
			if status.Progress == nil {
				status.Progress = &aimv1alpha1.DownloadProgress{}
			}
			status.Progress.DisplayPercentage = "N/A"
			return

		case downloadProgressAt100:
			// Download done, verification in progress
			cm.MarkTrue(aimv1alpha1.ArtifactConditionDownloadComplete,
				aimv1alpha1.ArtifactReasonVerifying,
				"Download complete, verifying integrity...",
				controllerutils.WithNormalEvent())

		default:
			// Still downloading
			cm.MarkFalse(aimv1alpha1.ArtifactConditionDownloadComplete,
				aimv1alpha1.ArtifactReasonDownloading,
				"Download in progress",
				controllerutils.WithNormalEvent())
		}
	}
}
