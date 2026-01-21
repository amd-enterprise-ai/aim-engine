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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

//go:embed download.sh
var downloadScript string

//go:embed download-monitor.sh
var progressMonitorScript string

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

	// Merge env vars with precedence: mc.Spec.Env > runtimeConfigSpec.Env > defaults
	defaultEnv := []corev1.EnvVar{
		{Name: "HF_XET_CHUNK_CACHE_SIZE_BYTES", Value: "0"},
		{Name: "HF_XET_SHARD_CACHE_SIZE_BYTES", Value: "0"},
		{Name: "HF_XET_HIGH_PERFORMANCE", Value: "1"},
		{Name: "HF_HOME", Value: mountPath + "/.hf"},
		{Name: "UMASK", Value: "0022"},
	}
	newEnv := utils.MergeEnvVars(defaultEnv, runtimeEnv)
	newEnv = utils.MergeEnvVars(newEnv, mc.Spec.Env)

	// Expected size in bytes for progress calculation
	expectedSizeBytes := mc.Spec.Size.Value()

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getDownloadJobName(mc),
			Namespace: mc.Namespace,
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
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: getCachePvcName(mc)},
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
					// Native sidecar (Kubernetes 1.28+): init container with restartPolicy=Always
					// runs alongside main containers and is automatically terminated by kubelet
					// when all regular containers complete (success or failure)
					InitContainers: []corev1.Container{
						{
							Name:            "progress-monitor",
							Image:           "busybox:1.36",
							ImagePullPolicy: corev1.PullIfNotPresent,
							// restartPolicy: Always makes this a native sidecar that runs alongside main containers
							// Kubernetes automatically sends SIGTERM when all regular containers terminate
							RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:  ptr.To(int64(1000)),
								RunAsGroup: ptr.To(int64(1000)),
							},
							Env: []corev1.EnvVar{
								{Name: "EXPECTED_SIZE_BYTES", Value: fmt.Sprintf("%d", expectedSizeBytes)},
								{Name: "MOUNT_PATH", Value: mountPath},
							},
							Command: []string{"/bin/sh"},
							Args: []string{
								"-c",
								progressMonitorScript,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "cache", MountPath: mountPath, ReadOnly: true},
							},
							// Minimal resources for the monitor
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("16Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
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
							Env: append(newEnv, []corev1.EnvVar{
								{Name: "MOUNT_PATH", Value: mountPath},
								{Name: "SOURCE_URI", Value: mc.Spec.SourceURI},
							}...),
							Command: []string{"/bin/sh"},
							Args: []string{
								"-c",
								downloadScript,
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
