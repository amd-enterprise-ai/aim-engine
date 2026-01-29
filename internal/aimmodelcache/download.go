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
	_ "embed"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

func buildRoleBinding(mc *aimv1alpha1.AIMModelCache) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aim-modelcache-status-updater", // Fixed name per namespace
			Namespace: mc.Namespace,
			// No OwnerReferences - independent lifecycle
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "default",
			Namespace: mc.Namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "aim-modelcache-status-updater",
		},
	}
}

func getDownloadJobName(mc *aimv1alpha1.AIMModelCache) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "download"}, utils.WithHashSource(mc.UID))
	return name
}

func buildDownloadJob(mc *aimv1alpha1.AIMModelCache, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon) *batchv1.Job {
	mountPath := "/cache"
	downloadImage := aimv1alpha1.DefaultDownloadImage
	if len(mc.Spec.ModelDownloadImage) > 0 {
		downloadImage = mc.Spec.ModelDownloadImage
	}

	// Get env vars from runtime config, or empty slice if nil
	var runtimeEnv []corev1.EnvVar
	if runtimeConfigSpec != nil {
		runtimeEnv = runtimeConfigSpec.Env
	}

	// Get effective size (from spec or discovered)
	var expectedSizeBytes int64
	if !mc.Spec.Size.IsZero() {
		expectedSizeBytes = mc.Spec.Size.Value()
	} else if mc.Status.DiscoveredSizeBytes != nil {
		expectedSizeBytes = *mc.Status.DiscoveredSizeBytes
	}

	// Merge env vars with precedence: mc.Spec.Env > runtimeConfigSpec.Env > defaults
	defaultEnv := []corev1.EnvVar{
		{Name: "HF_XET_CHUNK_CACHE_SIZE_BYTES", Value: "0"},
		{Name: "HF_XET_SHARD_CACHE_SIZE_BYTES", Value: "0"},
		{Name: "HF_HOME", Value: mountPath + "/tmp/.hf"},
		{Name: "UMASK", Value: "0022"},
		{Name: "EXPECTED_SIZE_BYTES", Value: fmt.Sprintf("%d", expectedSizeBytes)},
		{Name: "MOUNT_PATH", Value: mountPath},
		{Name: "CACHE_NAME", Value: mc.Name},
		{Name: "CACHE_NAMESPACE", Value: mc.Namespace},
		{Name: "STALL_TIMEOUT", Value: "120"},
		{Name: "TARGET_DIR", Value: mountPath},
	}
	newEnv := utils.MergeEnvVars(defaultEnv, runtimeEnv)
	newEnv = utils.MergeEnvVars(newEnv, mc.Spec.Env)

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getDownloadJobName(mc),
			Namespace: mc.Namespace,
			Labels: map[string]string{
				constants.LabelKeyCacheName: mc.Name,
				constants.LabelKeyCacheType: "model-cache",
				constants.LabelKeyComponent: "download",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(2)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 10)), // Cleanup after 10min to allow status observation
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						constants.LabelKeyCacheName: mc.Name,
						constants.LabelKeyCacheType: "model-cache",
						constants.LabelKeyComponent: "cache",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)), // kserve storage-initializer user
						RunAsGroup:   ptr.To(int64(1000)),
						FSGroup:      ptr.To(int64(1000)), // Ensures volume ownership matches user
						RunAsNonRoot: ptr.To(true),
					},
					ImagePullSecrets: mc.Spec.ImagePullSecrets,
					Volumes: []corev1.Volume{
						{
							Name: "cache",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: GenerateCachePvcName(mc)},
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
							Env:  newEnv,
							Args: []string{mc.Spec.SourceURI},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "cache", MountPath: mountPath},
							},
						},
					},
				},
			},
		},
	}
}

func getCheckSizeJobName(mc *aimv1alpha1.AIMModelCache) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "check-size"}, utils.WithHashSource(mc.UID))
	return name
}

func buildCheckSizeJob(mc *aimv1alpha1.AIMModelCache, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon) *batchv1.Job {
	downloadImage := aimv1alpha1.DefaultDownloadImage
	if len(mc.Spec.ModelDownloadImage) > 0 {
		downloadImage = mc.Spec.ModelDownloadImage
	}

	// Get auth env vars from runtime config and spec
	var runtimeEnv []corev1.EnvVar
	if runtimeConfigSpec != nil {
		runtimeEnv = runtimeConfigSpec.Env
	}
	envVars := utils.MergeEnvVars(runtimeEnv, mc.Spec.Env)

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getCheckSizeJobName(mc),
			Namespace: mc.Namespace,
			Labels: map[string]string{
				constants.LabelKeyCacheName: mc.Name,
				constants.LabelKeyCacheType: "model-cache",
				constants.LabelKeyComponent: "check-size",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(2)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 5)), // Cleanup after 5min
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						constants.LabelKeyCacheName: mc.Name,
						constants.LabelKeyCacheType: "model-cache",
						constants.LabelKeyComponent: "check-size",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)),
						RunAsGroup:   ptr.To(int64(1000)),
						RunAsNonRoot: ptr.To(true),
					},
					ImagePullSecrets: mc.Spec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "check-size",
							Image:           downloadImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/check-size.sh"},
							Args:            []string{mc.Spec.SourceURI},
							Env:             envVars,
						},
					},
				},
			},
		},
	}
}
