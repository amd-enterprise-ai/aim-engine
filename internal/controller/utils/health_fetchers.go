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
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

func GetPodsHealth(podList *corev1.PodList) ComponentHealth {
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
			// Try to determine the root cause of failure
			var failureReason, failureMessage string

			// Check container statuses for termination details
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					term := cs.State.Terminated
					failureReason = term.Reason
					failureMessage = term.Message

					// If we have a non-zero exit code, include it
					if term.ExitCode != 0 {
						failureMessage = fmt.Sprintf("Container %s failed with exit code %d: %s",
							cs.Name, term.ExitCode, failureMessage)
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
						if term.ExitCode != 0 {
							failureMessage = fmt.Sprintf("Init container %s failed with exit code %d: %s",
								cs.Name, term.ExitCode, term.Message)
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

			// Categorize the failure based on reason
			var err error
			switch failureReason {
			case "OOMKilled":
				// Out of memory - this is a resource configuration issue
				err = NewInvalidSpecError(
					"PodOOMKilled",
					failureMessage,
					nil,
				)
			case "Error", "ContainerCannotRun", "CreateContainerConfigError", "CreateContainerError":
				// Configuration or specification errors
				err = NewInvalidSpecError(
					"PodFailed",
					failureMessage,
					nil,
				)
			case "DeadlineExceeded":
				// Pod ran too long - could be config (timeout too short) or workload issue
				err = NewInvalidSpecError(
					"PodDeadlineExceeded",
					failureMessage,
					nil,
				)
			case "Evicted":
				// Eviction is usually due to resource pressure or policy
				err = NewInfrastructureError(
					"PodEvicted",
					failureMessage,
					nil,
				)
			default:
				// Generic failure - treat as invalid spec since pod ran but failed
				err = NewInvalidSpecError(
					"PodFailed",
					failureMessage,
					nil,
				)
			}

			return ComponentHealth{Errors: []error{err}}
		}
	}

	// Check for running or succeeded pods - both indicate healthy state
	// For health monitoring, we care that pods are functioning, not completion
	hasRunningOrSucceeded := false
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
			hasRunningOrSucceeded = true
			break
		}
	}

	if hasRunningOrSucceeded {
		// No errors = Ready state (derived automatically)
		return ComponentHealth{}
	}

	// Pods are pending or in unknown state - this is normal transitional state, not an error
	return ComponentHealth{
		State:   constants.AIMStatusProgressing,
		Reason:  "PodsPending",
		Message: "Pods are pending",
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
