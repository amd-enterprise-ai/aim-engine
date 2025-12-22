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

package controllerutils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// errorCategory represents the type of error found in pod logs
type errorCategory int

const (
	errorCategoryUnknown errorCategory = iota
	errorCategoryAuth
	errorCategoryStorageFull
	errorCategoryResourceNotFound
)

const (
	reasonOOMKilled = "OOMKilled"
)

// Common error patterns for categorization
var (
	// Authentication/authorization errors (S3, HuggingFace, etc.)
	authPatterns = []*regexp.Regexp{
		// S3/AWS auth errors
		regexp.MustCompile(`(?i)access.*denied.*s3`),
		regexp.MustCompile(`(?i)s3.*403`),
		regexp.MustCompile(`(?i)InvalidAccessKeyId`),
		regexp.MustCompile(`(?i)SignatureDoesNotMatch`),
		regexp.MustCompile(`(?i)s3.*unauthorized`),
		regexp.MustCompile(`(?i)aws.*credentials.*not.*found`),
		regexp.MustCompile(`(?i)NoCredentialProviders`),
		// HuggingFace auth errors (excluding "Repository not found" which is handled separately)
		regexp.MustCompile(`(?i)Access to model .* is restricted`),
		regexp.MustCompile(`(?i)Cannot access gated repo`),
		regexp.MustCompile(`(?i)Invalid.*token.*huggingface`),
		regexp.MustCompile(`(?i)huggingface.*authentication.*failed`),
		regexp.MustCompile(`(?i)401.*Unauthorized.*hf\.co`),
		regexp.MustCompile(`(?i)403.*Forbidden.*hf\.co`),
	}

	// Storage/disk full errors
	storageFullPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)no space left on device`),
		regexp.MustCompile(`(?i)disk.*full`),
		regexp.MustCompile(`(?i)ENOSPC`),
		regexp.MustCompile(`(?i)quota.*exceeded`),
		regexp.MustCompile(`(?i)storage.*full`),
	}

	// Resource not found errors (404 and HuggingFace "Repository Not Found")
	// Note: HuggingFace returns 401 for non-existent repos but includes "Repository Not Found" message
	resourceNotFoundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)Repository Not Found`),
		regexp.MustCompile(`(?i)RepositoryNotFoundError`),
		regexp.MustCompile(`(?i)404.*not.*found`),
		regexp.MustCompile(`(?i)not.*found.*404`),
		regexp.MustCompile(`(?i)NoSuchKey`),
		regexp.MustCompile(`(?i)NoSuchBucket`),
		regexp.MustCompile(`(?i)model.*not.*found`),
		regexp.MustCompile(`(?i)file.*not.*found`),
		regexp.MustCompile(`(?i)s3.*404`),
	}
)

// fetchPodLogs retrieves the last 50 lines of logs from a pod container
func fetchPodLogs(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod, containerName string) (string, error) {
	if clientset == nil {
		return "", fmt.Errorf("clientset is nil")
	}

	tailLines := int64(50)
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
	})

	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("error opening log stream: %w", err)
	}
	defer func() {
		_ = podLogs.Close()
	}()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("error reading logs: %w", err)
	}

	return buf.String(), nil
}

// categorizeErrorFromLogs analyzes pod logs to determine the error category
// Returns the error category and the matching log line (if found)
func categorizeErrorFromLogs(logs string) (errorCategory, string) {
	if logs == "" {
		return errorCategoryUnknown, ""
	}

	lines := strings.Split(logs, "\n")

	// Check for resource not found errors FIRST (404, Repository Not Found)
	// This must come before auth checks because HuggingFace returns 401 for non-existent repos
	// but includes "Repository Not Found" in the message
	for _, pattern := range resourceNotFoundPatterns {
		for _, line := range lines {
			if pattern.MatchString(line) {
				return errorCategoryResourceNotFound, strings.TrimSpace(line)
			}
		}
	}

	// Check for auth errors (S3, HuggingFace, etc.)
	for _, pattern := range authPatterns {
		for _, line := range lines {
			if pattern.MatchString(line) {
				return errorCategoryAuth, strings.TrimSpace(line)
			}
		}
	}

	// Check for storage full errors
	for _, pattern := range storageFullPatterns {
		for _, line := range lines {
			if pattern.MatchString(line) {
				return errorCategoryStorageFull, strings.TrimSpace(line)
			}
		}
	}

	return errorCategoryUnknown, ""
}

// inspectPodFailure fetches logs from a failed pod and categorizes the error
func inspectPodFailure(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod, containerName string) (errorCategory, string) {
	if clientset == nil {
		return errorCategoryUnknown, ""
	}

	logs, err := fetchPodLogs(ctx, clientset, pod, containerName)
	if err != nil {
		// If we can't fetch logs, we can't categorize the error
		// Return unknown category with no log snippet
		return errorCategoryUnknown, ""
	}

	// categorizeErrorFromLogs now returns both the category and the matching line
	category, matchingLine := categorizeErrorFromLogs(logs)

	// If we found a specific matching line, use that as the snippet
	// Otherwise, fall back to the last few lines for context
	snippet := matchingLine
	if snippet == "" {
		lines := strings.Split(strings.TrimSpace(logs), "\n")
		snippetLines := 3
		if len(lines) < snippetLines {
			snippetLines = len(lines)
		}
		snippet = strings.Join(lines[len(lines)-snippetLines:], "\n")
	}

	return category, snippet
}

// processFailedPod analyzes a failed pod and returns the appropriate error
func processFailedPod(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod) error {
	// Try to determine the root cause of failure
	var failureReason, failureMessage string
	var containerName string
	var shouldInspectLogs bool

	// Check container statuses for termination details
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			term := cs.State.Terminated
			failureReason = term.Reason
			failureMessage = term.Message
			containerName = cs.Name

			// If we have a non-zero exit code, include it
			if term.ExitCode != 0 {
				failureMessage = fmt.Sprintf("Container %s failed with exit code %d: %s",
					cs.Name, term.ExitCode, failureMessage)
				// Inspect logs for non-zero exit codes (excluding OOMKilled)
				shouldInspectLogs = failureReason != reasonOOMKilled
			} else if failureMessage == "" {
				failureMessage = fmt.Sprintf("Container %s terminated with reason: %s",
					cs.Name, failureReason)
			}
			break
		}
	}

	// Check init container statuses if no regular container failure found
	if failureReason == "" {
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Terminated != nil {
				term := cs.State.Terminated
				failureReason = term.Reason
				containerName = cs.Name
				if term.ExitCode != 0 {
					failureMessage = fmt.Sprintf("Init container %s failed with exit code %d: %s",
						cs.Name, term.ExitCode, term.Message)
					// Inspect logs for non-zero exit codes (excluding OOMKilled)
					shouldInspectLogs = failureReason != reasonOOMKilled
				} else {
					failureMessage = fmt.Sprintf("Init container %s terminated with reason: %s",
						cs.Name, failureReason)
				}
				break
			}
		}
	}

	// Fallback to pod-level status
	if failureReason == "" {
		failureReason = pod.Status.Reason
		failureMessage = pod.Status.Message
	}

	if failureMessage == "" {
		failureMessage = "Pod failed"
	}

	// Inspect logs if we should and we have a clientset
	var logCategory errorCategory
	var logSnippet string
	if shouldInspectLogs && clientset != nil && containerName != "" {
		logCategory, logSnippet = inspectPodFailure(ctx, clientset, pod, containerName)

		// Append log snippet to failure message if available
		if logSnippet != "" {
			failureMessage = fmt.Sprintf("%s\n\nLog excerpt:\n%s", failureMessage, logSnippet)
		}
	}

	// Categorize the failure based on log analysis first, then reason
	return categorizeFailure(logCategory, failureReason, failureMessage)
}

// categorizeFailure converts failure information into an appropriate error type
func categorizeFailure(logCategory errorCategory, failureReason, failureMessage string) error {
	// Priority 1: Log-based categorization (more specific)
	switch logCategory {
	case errorCategoryAuth:
		return NewAuthError(
			"AuthError",
			fmt.Sprintf("Authentication error detected in pod logs: %s", failureMessage),
			nil,
		)
	case errorCategoryStorageFull:
		return NewResourceExhaustionError(
			"StorageFull",
			fmt.Sprintf("Storage full error detected in pod logs: %s", failureMessage),
			nil,
		)
	case errorCategoryResourceNotFound:
		return NewInvalidSpecError(
			"SourceNotFound",
			fmt.Sprintf("Source resource not found (404): %s", failureMessage),
			nil,
		)
	default:
		// Priority 2: Reason-based categorization (fallback)
		return categorizeByReason(failureReason, failureMessage)
	}
}

// categorizeByReason categorizes failure based on the failure reason
func categorizeByReason(failureReason, failureMessage string) error {
	switch failureReason {
	case reasonOOMKilled:
		// Out of memory - resource exhaustion requiring limit increase
		return NewResourceExhaustionError(
			"PodOOMKilled",
			failureMessage,
			nil,
		)
	case "Error", "ContainerCannotRun", "CreateContainerConfigError", "CreateContainerError":
		// Configuration or specification errors
		return NewInvalidSpecError(
			"PodFailed",
			failureMessage,
			nil,
		)
	case "DeadlineExceeded":
		// Pod ran too long - could be config (timeout too short) or workload issue
		return NewInvalidSpecError(
			"PodDeadlineExceeded",
			failureMessage,
			nil,
		)
	case "Evicted":
		// Eviction is usually due to resource pressure or policy
		return NewInfrastructureError(
			"PodEvicted",
			failureMessage,
			nil,
		)
	default:
		// Generic failure - treat as invalid spec since pod ran but failed
		return NewInvalidSpecError(
			"PodFailed",
			failureMessage,
			nil,
		)
	}
}

func GetPodsHealth(ctx context.Context, clientset kubernetes.Interface, podList *corev1.PodList) ComponentHealth {
	if podList == nil || len(podList.Items) == 0 {
		return ComponentHealth{
			Errors: []error{
				NewMissingDownstreamDependencyError("NoPods", "No pods found", nil),
			},
		}
	}

	// Check for image pull errors first (highest priority)
	for i := range podList.Items {
		pod := &podList.Items[i]
		if imagePullErr := utils.CheckPodImagePullStatus(pod); imagePullErr != nil {
			var err error
			containerInfo := fmt.Sprintf("Container %s: %s", imagePullErr.Container, imagePullErr.Message)

			switch imagePullErr.Type {
			case utils.ImagePullErrorAuth:
				err = NewAuthError(
					constants.ReasonImagePullAuthFailure,
					containerInfo,
					nil,
				)
			case utils.ImagePullErrorNotFound:
				err = NewMissingUpstreamDependencyError(
					constants.ReasonImageNotFound,
					containerInfo,
					nil,
				)
			default:
				err = NewInfrastructureError(
					constants.ReasonImagePullBackOff,
					containerInfo,
					nil,
				)
			}

			return ComponentHealth{Errors: []error{err}}
		}
	}

	// Check for any failed pods - inspect failure reasons to categorize appropriately
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodFailed {
			err := processFailedPod(ctx, clientset, pod)
			return ComponentHealth{Errors: []error{err}}
		}
	}

	// Check for running or succeeded pods - both indicate healthy state
	// For health monitoring, we care that pods are functioning, not completion
	hasRunningOrSucceeded := false
	hasPending := false

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
			hasRunningOrSucceeded = true
			break
		}
		if pod.Status.Phase == corev1.PodPending {
			hasPending = true
		}
	}

	if hasRunningOrSucceeded {
		// No errors = Ready state (derived automatically)
		return ComponentHealth{}
	}

	// Distinguish between Pending (waiting for resources) and Progressing (actively working)
	if hasPending {
		// Pods are pending - waiting for scheduling, image pull, volume mount, etc.
		return ComponentHealth{
			State:   constants.AIMStatusPending,
			Reason:  "PodsPending",
			Message: "Pods are pending",
		}
	}

	// Pods in unknown or other transitional states
	return ComponentHealth{
		State:   constants.AIMStatusProgressing,
		Reason:  "PodsProgressing",
		Message: "Pods are progressing",
	}
}

func GetJobHealth(job *batchv1.Job) ComponentHealth {
	if job == nil {
		return ComponentHealth{
			Errors: []error{
				NewMissingDownstreamDependencyError("JobNotFound", "Job not found", nil),
			},
		}
	}

	// Check job conditions
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			// Job succeeded - no errors, returns Ready
			return ComponentHealth{}
		}

		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			// Analyze the failure reason to categorize appropriately
			failureReason := condition.Reason
			failureMessage := condition.Message

			if failureMessage == "" {
				failureMessage = "Job failed"
			}

			var err error
			switch failureReason {
			case "BackoffLimitExceeded":
				// Job retried too many times - likely a persistent workload issue
				err = NewInvalidSpecError(
					"JobBackoffLimitExceeded",
					failureMessage,
					nil,
				)
			case "DeadlineExceeded":
				// Job took too long - could be timeout too short or workload issue
				err = NewInvalidSpecError(
					"JobDeadlineExceeded",
					failureMessage,
					nil,
				)
			case "Evicted":
				// Job was evicted - infrastructure or resource pressure
				err = NewInfrastructureError(
					"JobEvicted",
					failureMessage,
					nil,
				)
			default:
				// Generic job failure - treat as invalid spec
				err = NewInvalidSpecError(
					"JobFailed",
					failureMessage,
					nil,
				)
			}

			return ComponentHealth{Errors: []error{err}}
		}
	}

	// Job is still running - this is normal transitional state, not an error
	return ComponentHealth{
		State:   constants.AIMStatusProgressing,
		Reason:  "JobRunning",
		Message: "Job is in progress",
	}
}

func GetPvcHealth(pvc *corev1.PersistentVolumeClaim) ComponentHealth {
	if pvc == nil {
		return ComponentHealth{
			Errors: []error{
				NewMissingDownstreamDependencyError("PvcNotFound", "PVC not found", nil),
			},
		}
	}

	// Check PVC status
	if pvc.Status.Phase == corev1.ClaimBound {
		// PVC is bound and ready - no errors
		return ComponentHealth{}
	}

	if pvc.Status.Phase == corev1.ClaimLost {
		// PVC is lost - this is an infrastructure issue (volume disappeared)
		return ComponentHealth{
			Errors: []error{
				NewInfrastructureError(
					"PvcLost",
					"PVC lost its underlying volume",
					nil,
				),
			},
		}
	}

	// PVC is pending - check for specific issues that would indicate errors
	// Otherwise it's just a normal transitional state
	for _, condition := range pvc.Status.Conditions {
		if condition.Type == corev1.PersistentVolumeClaimResizing &&
			condition.Status == corev1.ConditionFalse &&
			condition.Reason == "ProvisioningFailed" {
			// Provisioning failed - could be config or infrastructure
			return ComponentHealth{
				Errors: []error{
					NewInfrastructureError(
						"PvcProvisioningFailed",
						fmt.Sprintf("PVC provisioning failed: %s", condition.Message),
						nil,
					),
				},
			}
		}
	}

	// PVC is pending but no error conditions - normal transitional state
	return ComponentHealth{
		State:   constants.AIMStatusProgressing,
		Reason:  "PvcPending",
		Message: "PVC is pending",
	}
}
