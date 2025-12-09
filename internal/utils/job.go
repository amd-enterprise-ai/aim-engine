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

package utils

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckJobPodImagePullStatus checks if a job's pod is stuck in ImagePullBackOff or ErrImagePull state.
// Returns the image pull error details if found, or nil otherwise.
func CheckJobPodImagePullStatus(ctx context.Context, k8sClient client.Client, job *batchv1.Job, namespace string) (*ImagePullError, error) {
	// List pods owned by this job
	var pods corev1.PodList
	if err := k8sClient.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabels{
			"job-name": job.Name,
		}); err != nil {
		return nil, err
	}

	// Check each pod for ImagePullBackOff or ErrImagePull status
	for i := range pods.Items {
		pod := &pods.Items[i]
		if imagePullErr := CheckPodImagePullStatus(pod); imagePullErr != nil {
			return imagePullErr, nil
		}
	}

	return nil, nil
}

// IsJobComplete returns true if the job has completed (successfully or failed)
func IsJobComplete(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// IsJobSucceeded returns true if the job completed successfully
func IsJobSucceeded(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// IsJobFailed returns true if the job failed
func IsJobFailed(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// FindSuccessfulPodForJob locates a successfully completed pod for the given job.
func FindSuccessfulPodForJob(ctx context.Context, k8sClient client.Client, job *batchv1.Job) (*corev1.Pod, error) {
	var podList corev1.PodList
	if err := k8sClient.List(ctx, &podList, client.InNamespace(job.Namespace), client.MatchingLabels{
		"job-name": job.Name,
	}); err != nil {
		return nil, fmt.Errorf("failed to list pods for job: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no pods found for job %s", job.Name)
	}

	// Find a successful pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded {
			return pod, nil
		}
	}

	return nil, fmt.Errorf("no successful pod found for job %s", job.Name)
}
