/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodelcache

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type jobFetchResult struct {
	job   *batchv1.Job
	error error
}

func fetchJob(ctx context.Context, c client.Client, cache *aimv1alpha1.AIMModelCache) (jobFetchResult, error) {
	result := jobFetchResult{}

	jobName := jobNameForCache(cache)
	var job batchv1.Job
	err := c.Get(ctx, types.NamespacedName{Namespace: cache.Namespace, Name: jobName}, &job)

	if err != nil && client.IgnoreNotFound(err) != nil {
		return result, err
	}

	result.job = &job
	result.error = err
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

// jobObservation contains information about the download job.
type jobObservation struct {
	found            bool
	job              *batchv1.Job
	succeeded        bool
	failed           bool
	PendingOrRunning bool
}

func observeJob(result jobFetchResult) jobObservation {
	obs := jobObservation{}

	if result.error != nil {
		obs.found = false
		return obs
	}

	obs.found = true
	obs.job = result.job

	// Check job conditions
	for _, c := range result.job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			obs.failed = true
		}
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			obs.succeeded = true
		}
	}

	// Check job status counters
	if result.job.Status.Succeeded > 0 {
		obs.succeeded = true
	}
	if result.job.Status.Active > 0 || utils.ValueOrDefault(result.job.Status.Ready) > 0 {
		obs.PendingOrRunning = true
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func planJob(cache *aimv1alpha1.AIMModelCache, obs Observation, scheme *runtime.Scheme) client.Object {
	// Plan Job when storage is ready OR when PVC is pending with waitForFirstConsumer
	if !canCreateJob(obs) || obs.job.found {
		return nil
	}

	logger := log.Log.WithName("aimmodelcache").WithValues("cache", cache.Name)
	logger.V(1).Info("Planning to create download job",
		"storageReady", obs.pvc.ready,
		"waitForFirstConsumer", obs.storageClass.waitForFirstConsumer)

	job := buildDownloadJob(cache, obs)
	if err := controllerutil.SetOwnerReference(cache, job, scheme); err != nil {
		return nil
	}
	return job
}

// canCreateJob determines if the download job can be created.
func canCreateJob(obs Observation) bool {
	// Can create job if storage is ready
	if obs.pvc.ready {
		return true
	}

	// Or if PVC is pending with waitForFirstConsumer
	// (job will trigger the binding)
	if obs.pvc.found &&
		obs.pvc.pvc.Status.Phase == corev1.ClaimPending &&
		obs.storageClass.waitForFirstConsumer {
		return true
	}

	return false
}

// buildDownloadJob creates a Job to download the model into the PVC.
func buildDownloadJob(cache *aimv1alpha1.AIMModelCache, _ Observation) *batchv1.Job {
	mountPath := "/cache"
	downloadImage := aimv1alpha1.DefaultDownloadImage
	if len(cache.Spec.ModelDownloadImage) > 0 {
		downloadImage = cache.Spec.ModelDownloadImage
	}

	pvcName := pvcNameForCache(cache)
	jobName := jobNameForCache(cache)

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: cache.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "modelcache-controller",
				"aim.eai.amd.com/modelcache":   cache.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(2)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 10)), // Cleanup after 10min to allow status observation
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)), // kserve storage-initializer user
						RunAsGroup:   ptr.To(int64(1000)),
						FSGroup:      ptr.To(int64(1000)), // Ensures volume ownership matches user
						RunAsNonRoot: ptr.To(true),
					},
					ImagePullSecrets: cache.Spec.ImagePullSecrets,
					Volumes: []corev1.Volume{
						{
							Name: "cache",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
							},
						},
						{
							Name: "tmp",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									SizeLimit: ptr.To(resource.MustParse("500Mi")), // Small temp space for system operations
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "model-download",
							Image:           downloadImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:  ptr.To(int64(1000)),
								RunAsGroup: ptr.To(int64(1000)),
							},
							Env: append(cache.Spec.Env, []corev1.EnvVar{
								{Name: "HF_HUB_DISABLE_XET", Value: "1"},
								{Name: "HF_HOME", Value: mountPath + "/.hf"},
								{Name: "UMASK", Value: "0022"}, // Create files with 644 permissions (readable by others)
							}...),
							Command: []string{"/bin/sh"},
							Args: []string{
								"-c",
								fmt.Sprintf(`
# Download the model
python /storage-initializer/scripts/initializer-entrypoint %s %s &&
(
# Clean up HF xet cache to save space (keeps only final model files)
echo "Cleaning up HF cache to save space..."
rm -rf %s/.hf/xet/*/chunk-cache 2>/dev/null || true
rm -rf %s/.hf/xet/*/staging 2>/dev/null || true

# Report final sizes
echo "Final storage usage:"
du -sh %s
du -sh %s/.hf 2>/dev/null || true
)
				`, cache.Spec.SourceURI, mountPath, mountPath, mountPath, mountPath, mountPath),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "cache", MountPath: mountPath},
								{Name: "tmp", MountPath: "/tmp"},
							},
						},
					},
				},
			},
		},
	}
}

// ============================================================================
// PROJECT
// ============================================================================

// projectReadyCondition sets the Ready condition.
func projectReadyCondition(cm *controllerutils.ConditionManager, obs Observation, canCreate bool) {
	ready := obs.pvc.ready && obs.job.succeeded

	if ready {
		cm.Set(aimv1alpha1.AIMModelCacheConditionReady, metav1.ConditionTrue, aimv1alpha1.AIMModelCacheReasonWarm, "", controllerutils.LevelNormal)
	} else {
		if !canCreate {
			cm.Set(aimv1alpha1.AIMModelCacheConditionReady, metav1.ConditionFalse,
				aimv1alpha1.AIMModelCacheReasonWaitingForPVC, "", controllerutils.LevelNormal)
		} else {
			cm.Set(aimv1alpha1.AIMModelCacheConditionReady, metav1.ConditionFalse,
				aimv1alpha1.AIMModelCacheReasonDownloading, "", controllerutils.LevelNormal)
		}
	}
}

// projectProgressingCondition sets the Progressing condition.
func projectProgressingCondition(cm *controllerutils.ConditionManager, obs Observation, canCreate bool) {
	ready := obs.pvc.ready && obs.job.succeeded
	failure := obs.pvc.lost || obs.job.failed

	progressing := !ready && !failure && (!obs.pvc.ready || obs.job.PendingOrRunning || (!obs.job.found && canCreate))

	if progressing {
		if !obs.pvc.ready && !canCreate {
			cm.Set(aimv1alpha1.AIMModelCacheConditionProgressing, metav1.ConditionTrue,
				aimv1alpha1.AIMModelCacheReasonWaitingForPVC, "", controllerutils.LevelNormal)
		} else if obs.job.PendingOrRunning || (!obs.job.found && canCreate) {
			cm.Set(aimv1alpha1.AIMModelCacheConditionProgressing, metav1.ConditionTrue,
				aimv1alpha1.AIMModelCacheReasonDownloading, "", controllerutils.LevelNormal)
		} else {
			cm.Set(aimv1alpha1.AIMModelCacheConditionProgressing, metav1.ConditionTrue,
				aimv1alpha1.AIMModelCacheReasonRetryBackoff, "", controllerutils.LevelNormal)
		}
	} else {
		cm.Set(aimv1alpha1.AIMModelCacheConditionProgressing, metav1.ConditionFalse,
			aimv1alpha1.AIMModelCacheReasonRetryBackoff, "", controllerutils.LevelWarning)
	}
}

// projectFailureCondition sets the Failure condition.
func projectFailureCondition(cm *controllerutils.ConditionManager, obs Observation) {
	failure := obs.pvc.lost || obs.job.failed

	if !failure {
		cm.Set(aimv1alpha1.AIMModelCacheConditionFailure, metav1.ConditionFalse,
			aimv1alpha1.AIMModelCacheReasonNoFailure, "", controllerutils.LevelNormal)
		return
	}

	// Determine specific failure reason
	if obs.pvc.lost {
		cm.Set(aimv1alpha1.AIMModelCacheConditionFailure, metav1.ConditionTrue,
			aimv1alpha1.AIMModelCacheReasonPVCLost, "", controllerutils.LevelWarning)
	} else if obs.job.failed {
		cm.Set(aimv1alpha1.AIMModelCacheConditionFailure, metav1.ConditionTrue,
			aimv1alpha1.AIMModelCacheReasonDownloadFailed, "", controllerutils.LevelWarning)
	}
}

// projectOverallStatus determines the overall status enum.
func projectOverallStatus(status *aimv1alpha1.AIMModelCacheStatus, obs Observation, canCreate bool) {
	ready := obs.pvc.ready && obs.job.succeeded
	failure := obs.pvc.lost || obs.job.failed
	progressing := !ready && !failure && (!obs.pvc.ready || obs.job.PendingOrRunning || (!obs.job.found && canCreate))

	switch {
	case failure && obs.job.failed:
		status.Status = aimv1alpha1.AIMModelCacheStatusFailed
	case ready:
		status.Status = aimv1alpha1.AIMModelCacheStatusAvailable
	case progressing:
		status.Status = aimv1alpha1.AIMModelCacheStatusProgressing
	default:
		status.Status = aimv1alpha1.AIMModelCacheStatusPending
	}
}
