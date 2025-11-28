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
func CheckJobPodImagePullStatus(ctx context.Context, k8sClient client.Client, job *batchv1.Job, getNamespace func() string) (*ImagePullError, error) {
	namespace := getNamespace()

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