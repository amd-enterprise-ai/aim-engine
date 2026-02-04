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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ArtifactReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
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
}

func (r *ArtifactReconciler) ComposeState(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact],
	fetch ArtifactFetchResult,
) ArtifactObservation {
	logger := log.FromContext(ctx)
	obs := ArtifactObservation{ArtifactFetchResult: fetch}

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

	return obs
}

func (r *ArtifactReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact],
	obs ArtifactObservation,
) controllerutils.PlanResult {
	mc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	// Use runtime config if available, otherwise use nil (functions should handle defaults)
	runtimeConfig := reconcileCtx.MergedRuntimeConfig.Value

	// Phase 0: Rolebinding creation - if not found
	if obs.roleBinding.IsNotFound() {
		roleBinding := buildRoleBinding(mc)
		result.ApplyWithoutOwnerRef(roleBinding)
	}

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

	// Phase 3: Download job creation - size is known and PVC, rolebinding exists
	if mc.Status.Status != constants.AIMStatusReady &&
		obs.downloadJob != nil && obs.downloadJob.IsNotFound() && obs.roleBinding.OK() {
		downloadJob := buildDownloadJob(mc, runtimeConfig, obs.GetEffectiveSize())
		result.Apply(downloadJob)
	}

	return result
}

// DecorateStatus implements StatusDecorator to update download status
func (r *ArtifactReconciler) DecorateStatus(
	status *aimv1alpha1.AIMArtifactStatus,
	cm *controllerutils.ConditionManager,
	obs ArtifactObservation,
) {
	mc := obs.artifact
	runtimeConfig := obs.mergedRuntimeConfig.Value

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
		// This ensures we show completion even if the downloader didn't have time to report it
		expectedSize := obs.GetEffectiveSize()

		status.Progress = &aimv1alpha1.DownloadProgress{
			TotalBytes:        expectedSize,
			DownloadedBytes:   expectedSize,
			Percentage:        100,
			DisplayPercentage: "100 %",
		}
		return
	}
}
