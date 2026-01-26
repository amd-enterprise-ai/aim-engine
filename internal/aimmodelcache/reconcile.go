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

package aimmodelcache

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

type ModelCacheReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

type ModelCacheFetchResult struct {
	modelCache *aimv1alpha1.AIMModelCache

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	cachePvc            controllerutils.FetchResult[*corev1.PersistentVolumeClaim]

	// Check-size job (fetched when spec.size is empty and not yet discovered)
	checkSizeJob     *controllerutils.FetchResult[*batchv1.Job]
	checkSizeJobPods *controllerutils.FetchResult[*corev1.PodList]
	checkSizeOutput  string // Last log line from check-size container

	// Download job pods (existing)
	downloadJob     *controllerutils.FetchResult[*batchv1.Job]
	downloadJobPods *controllerutils.FetchResult[*corev1.PodList]

	// progressLogLine stores the last progress log line from the monitor container
	progressLogLine string
}

// progressLog represents the JSON structure of progress log lines from download-monitor.sh
type progressLog struct {
	Type            string `json:"type"`
	Percent         int32  `json:"percent,omitempty"`
	CurrentBytes    int64  `json:"currentBytes,omitempty"`
	ExpectedBytes   int64  `json:"expectedBytes,omitempty"`
	Message         string `json:"message,omitempty"`
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
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

	if output.SizeBytes <= 0 {
		return nil, fmt.Errorf("invalid size: %d bytes", output.SizeBytes)
	}

	return &output.SizeBytes, nil
}

// IsSizeKnown returns true if size is available (from spec or discovered)
func (result ModelCacheFetchResult) IsSizeKnown() bool {
	mc := result.modelCache
	return !mc.Spec.Size.IsZero() || mc.Status.DiscoveredSizeBytes != nil
}

// GetEffectiveSize returns the size to use (spec or discovered)
func (result ModelCacheFetchResult) GetEffectiveSize() int64 {
	mc := result.modelCache
	if !mc.Spec.Size.IsZero() {
		return mc.Spec.Size.Value()
	}
	if mc.Status.DiscoveredSizeBytes != nil {
		return *mc.Status.DiscoveredSizeBytes
	}
	return 0
}

// CheckSizeJobSucceeded returns true if the check-size job completed successfully
func (result ModelCacheFetchResult) CheckSizeJobSucceeded() bool {
	if result.checkSizeJob == nil {
		return false
	}
	return utils.IsJobSucceeded(result.checkSizeJob.Value)
}

// parseProgressLog parses a JSON progress log line and returns a DownloadProgress if valid
func parseProgressLog(logLine string) *aimv1alpha1.DownloadProgress {
	if logLine == "" {
		return nil
	}

	var log progressLog
	if err := json.Unmarshal([]byte(logLine), &log); err != nil {
		return nil
	}

	// Only return progress for "progress", "start", and "complete" type logs
	// Ignore "terminated" - that just means the container stopped, not that download succeeded
	if log.Type != "progress" && log.Type != "start" && log.Type != "complete" {
		return nil
	}

	// Calculate percentage - multiply first to avoid integer division truncation
	var percent int32
	if log.ExpectedBytes > 0 {
		percent = int32((log.CurrentBytes * 100) / log.ExpectedBytes)
	} else {
		percent = 0
	}

	// Ensure percentage is bounded between 0 and 100
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}

	// For "complete" type, ensure we show 100%
	if log.Type == "complete" {
		percent = 100
	}

	return &aimv1alpha1.DownloadProgress{
		TotalBytes:        log.ExpectedBytes,
		DownloadedBytes:   log.CurrentBytes,
		Percentage:        percent,
		DisplayPercentage: fmt.Sprintf("%d %%", percent),
	}
}

// getLastLogLine retrieves the last line from a container's logs
func (r *ModelCacheReconciler) getLastLogLine(ctx context.Context, namespace, podName, containerName string) string {
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

func (r *ModelCacheReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMModelCache],
) ModelCacheFetchResult {
	mc := reconcileCtx.Object
	downloadJobName := getDownloadJobName(mc)
	downloadJob := &batchv1.Job{}
	downloadJobPods := &corev1.PodList{}

	result := ModelCacheFetchResult{
		modelCache:          mc,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
		cachePvc: controllerutils.Fetch(
			ctx, c,
			client.ObjectKey{Name: getCachePvcName(mc), Namespace: mc.Namespace},
			&corev1.PersistentVolumeClaim{},
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
		job := downloadJobFetchResult.Value
		jobSucceeded := false
		if job != nil {
			for _, condition := range job.Status.Conditions {
				if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
					jobSucceeded = true
					break
				}
			}
		}

		// Fetch pods if the job hasn't succeeded yet
		if !jobSucceeded {
			downloadJobPodsFetchResult := controllerutils.FetchList(
				ctx, c,
				downloadJobPods,
				client.InNamespace(reconcileCtx.Object.Namespace),
				client.MatchingLabels{"job-name": downloadJobName},
			)
			result.downloadJobPods = &downloadJobPodsFetchResult

			// If the download job is active (running), fetch the last log line from the progress monitor
			if !downloadJobPodsFetchResult.IsNotFound() && downloadJobPods != nil && len(downloadJobPods.Items) > 0 {
				// Find a running pod
				for i := range downloadJobPods.Items {
					pod := &downloadJobPods.Items[i]
					if pod.Status.Phase == corev1.PodRunning {
						// Get logs from the progress-monitor init container
						logLine := r.getLastLogLine(ctx, pod.Namespace, pod.Name, "model-downloader")
						if logLine != "" {
							result.progressLogLine = logLine
						}
						break // Only need one pod's logs
					}
				}
			}
		}
	}

	return result
}

func (result ModelCacheFetchResult) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	health := []controllerutils.ComponentHealth{
		result.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
	}

	// Phase 1: Check-size job health (when discovering size)
	if result.checkSizeJob != nil {
		health = append(health,
			result.checkSizeJob.ToDownstreamComponentHealth("CheckSizeJob", controllerutils.GetJobHealth))
		if result.checkSizeJobPods != nil {
			health = append(health,
				result.checkSizeJobPods.ToComponentHealthWithContext(ctx, clientset, "CheckSizeJobPods", controllerutils.GetPodsHealth))
		}
	}

	// Phase 2+: PVC and download job health (only after size is known)
	if result.IsSizeKnown() {
		health = append(health,
			result.cachePvc.ToDownstreamComponentHealth("CachePvc", controllerutils.GetPvcHealth))

		if result.modelCache.Status.Status != constants.AIMStatusReady {
			if result.downloadJob != nil {
				health = append(health,
					result.downloadJob.ToDownstreamComponentHealth("DownloadJob", controllerutils.GetJobHealth))
			}
			if result.downloadJobPods != nil {
				health = append(health,
					result.downloadJobPods.ToComponentHealthWithContext(ctx, clientset, "DownloadJobPods", controllerutils.GetPodsHealth))
			}
		}
	}

	return health
}

func (result ModelCacheFetchResult) DownloadJobSucceeded() bool {
	// Check if the job itself succeeded (not the old status)
	// The old status check (Status == Ready) caused issues where we'd show 100% even when failing
	return utils.IsJobSucceeded(result.downloadJob.Value)
}

// GetProgress parses the progress log line and returns download progress information
func (result ModelCacheFetchResult) GetProgress() *aimv1alpha1.DownloadProgress {
	return parseProgressLog(result.progressLogLine)
}

// Observe (thin wrapper for now, may be removed later)

type ModelCacheObservation struct {
	ModelCacheFetchResult
}

func (r *ModelCacheReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMModelCache],
	fetch ModelCacheFetchResult,
) ModelCacheObservation {
	return ModelCacheObservation{ModelCacheFetchResult: fetch}
}

func (r *ModelCacheReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMModelCache],
	obs ModelCacheObservation,
) controllerutils.PlanResult {
	mc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	// Use runtime config if available, otherwise use nil (functions should handle defaults)
	runtimeConfig := reconcileCtx.MergedRuntimeConfig.Value

	// Phase 1: Size discovery (when spec.size is empty)
	if !obs.IsSizeKnown() {
		if obs.checkSizeJob != nil && obs.checkSizeJob.IsNotFound() {
			// Create check-size job
			checkSizeJob := buildCheckSizeJob(mc, runtimeConfig)
			result.Apply(checkSizeJob)
		}
		// Don't proceed until size is known
		return result
	}

	// Phase 2: PVC creation - size is known
	if obs.cachePvc.IsNotFound() {
		// Include PVC only if it doesn't exist yet
		// Once created, PVCs are immutable - we never modify them to avoid:
		// 1. StorageClassName mutation errors (forbidden by Kubernetes)
		// 2. Storage size shrinkage errors (forbidden by Kubernetes)
		// 3. Unexpected PVC expansion from runtime config changes

		headroomPercent := utils.GetPVCHeadroomPercent(runtimeConfig)
		storageClassName := utils.ResolveStorageClass(mc.Spec.StorageClassName, runtimeConfig)
		effectiveSize := obs.GetEffectiveSize()
		pvcSize := utils.QuantityWithHeadroom(effectiveSize, headroomPercent)

		pvc := buildCachePvc(mc, pvcSize, storageClassName)
		result.Apply(pvc)
		return result
	}

	// Phase 3: Download job creation - size is known and PVC exists
	if mc.Status.Status != constants.AIMStatusReady &&
		obs.downloadJob != nil && obs.downloadJob.IsNotFound() {
		downloadJob := buildDownloadJob(mc, runtimeConfig)
		result.Apply(downloadJob)
	}

	return result
}

// DecorateStatus implements StatusDecorator to add download progress to the status
func (r *ModelCacheReconciler) DecorateStatus(
	status *aimv1alpha1.AIMModelCacheStatus,
	_ *controllerutils.ConditionManager,
	obs ModelCacheObservation,
) {
	mc := obs.modelCache
	runtimeConfig := obs.mergedRuntimeConfig.Value

	// Store discovered size when check-size job succeeds
	if obs.CheckSizeJobSucceeded() && status.DiscoveredSizeBytes == nil {
		if size, err := parseCheckSizeOutput(obs.checkSizeOutput); err == nil {
			status.DiscoveredSizeBytes = size
		}
	}

	// Set display size from spec or discovered
	if !mc.Spec.Size.IsZero() {
		status.DisplaySize = mc.Spec.Size.String()
	} else if status.DiscoveredSizeBytes != nil {
		// Convert bytes to human-readable
		status.DisplaySize = resource.NewQuantity(*status.DiscoveredSizeBytes, resource.BinarySI).String()
	}

	// Store allocated size and headroom when PVC is created
	if !obs.cachePvc.IsNotFound() && status.AllocatedSizeBytes == nil {
		pvc := obs.cachePvc.Value
		if pvc != nil && pvc.Spec.Resources.Requests != nil {
			if qty, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
				allocatedSize := qty.Value()
				status.AllocatedSizeBytes = &allocatedSize
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

	// Check if the job has failed
	jobFailed := obs.downloadJob != nil && !obs.downloadJob.IsNotFound() &&
		obs.downloadJob.Value != nil && utils.IsJobFailed(obs.downloadJob.Value)

	jobSucceeded := obs.DownloadJobSucceeded()

	if podFailed || jobFailed {
		// When failed, don't use stale progress from logs
		// Show N/A since the download didn't complete successfully
		// Preserve existing progress data if available
		if status.Progress == nil {
			status.Progress = &aimv1alpha1.DownloadProgress{
				TotalBytes:      0,
				DownloadedBytes: 0,
				Percentage:      0,
			}
		}
		status.Progress.DisplayPercentage = "N/A"
		return
	}

	// Check if download job succeeded
	if jobSucceeded {
		// When the job succeeds, set progress to 100%
		// This ensures we show completion even if the sidecar didn't have time to report it
		expectedSize := obs.GetEffectiveSize()

		status.Progress = &aimv1alpha1.DownloadProgress{
			TotalBytes:        expectedSize,
			DownloadedBytes:   expectedSize,
			Percentage:        100,
			DisplayPercentage: "100 %",
		}
		return
	}

	// Otherwise, we're in progress - show progress from logs if available
	progress := obs.GetProgress()
	if progress != nil {
		status.Progress = progress
	}
}
