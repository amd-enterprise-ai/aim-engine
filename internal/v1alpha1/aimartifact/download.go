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
	_ "embed"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// effectiveSourceURI returns the download source URI, using the resolved
// cache URI when available (cache hit) and falling back to spec.sourceUri.
func effectiveSourceURI(mc *aimv1alpha1.AIMArtifact) string {
	if mc.Status.ResolvedSourceURI != "" {
		return mc.Status.ResolvedSourceURI
	}
	return mc.Spec.SourceURI
}

// resolveDownloadImage picks the container image for download and size-check
// jobs. Precedence: artifact spec > runtime config > build-time default.
func resolveDownloadImage(mc *aimv1alpha1.AIMArtifact, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon) string {
	if len(mc.Spec.ModelDownloadImage) > 0 {
		return mc.Spec.ModelDownloadImage
	}
	if runtimeConfigSpec != nil && runtimeConfigSpec.Artifact != nil && runtimeConfigSpec.Artifact.ModelDownloadImage != "" {
		return runtimeConfigSpec.Artifact.ModelDownloadImage
	}
	return aimv1alpha1.DefaultDownloadImage
}

// pullPolicyForImage mirrors kubelet's own default-policy heuristic: when the
// image reference has no tag or carries the rolling `:latest` tag, opt into
// `PullAlways` so we never reuse an arbitrarily stale cached layer on a node.
// Versioned tags (e.g. `:v0.2.2`) are immutable enough in practice to keep
// `IfNotPresent`, which avoids hitting the registry on every Job. Digest
// references (`@sha256:...`) are also treated as immutable. This is the same
// rule the K8s controller-manager applies when ImagePullPolicy is omitted; we
// just have to do it ourselves because the Job builder sets it explicitly.
func pullPolicyForImage(image string) corev1.PullPolicy {
	switch {
	case image == "":
		return corev1.PullAlways
	case strings.Contains(image, "@sha256:"):
		return corev1.PullIfNotPresent
	}
	tag := ""
	if idx := strings.LastIndex(image, ":"); idx >= 0 && !strings.Contains(image[idx:], "/") {
		tag = image[idx+1:]
	}
	if tag == "" || tag == "latest" {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

func buildRoleBinding(mc *aimv1alpha1.AIMArtifact) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aim-engine-artifact-status-updater", // Fixed name per namespace
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
			Name:     "aim-engine-artifact-status-updater",
		},
	}
}

// defaultDownloadFilter excludes subdirectory files when no explicit filter is configured.
var defaultDownloadFilter = &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}}

// resolveDownloadFilter returns the effective download filter.
// Precedence: artifact spec > runtime config storage > default (exclude subdirs).
// An explicit empty filter (downloadFilter: {}) on the artifact disables all filtering.
func resolveDownloadFilter(mc *aimv1alpha1.AIMArtifact, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMDownloadFilter {
	if mc.Spec.DownloadFilter != nil {
		return mc.Spec.DownloadFilter
	}
	if runtimeConfig != nil && runtimeConfig.Storage != nil && runtimeConfig.Storage.DownloadFilter != nil {
		return runtimeConfig.Storage.DownloadFilter
	}
	return defaultDownloadFilter
}

func downloadFilterEnvVars(filter *aimv1alpha1.AIMDownloadFilter) []corev1.EnvVar {
	if filter == nil {
		return nil
	}
	var envs []corev1.EnvVar
	if len(filter.Include) > 0 {
		envs = append(envs, corev1.EnvVar{Name: "AIM_HF_INCLUDE", Value: strings.Join(filter.Include, ",")})
	}
	if len(filter.Exclude) > 0 {
		envs = append(envs, corev1.EnvVar{Name: "AIM_HF_EXCLUDE", Value: strings.Join(filter.Exclude, ",")})
	}
	return envs
}

func getDownloadJobName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "download"}, utils.WithHashSource(mc.UID))
	return name
}

func buildDownloadJob(mc *aimv1alpha1.AIMArtifact, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon, expectedSizeBytes int64, cacheEnv ...corev1.EnvVar) *batchv1.Job {
	mountPath := "/cache/models"
	downloadImage := resolveDownloadImage(mc, runtimeConfigSpec)

	// Use resolved source (cache hit) or original spec source
	sourceURI := effectiveSourceURI(mc)

	// Get env vars from runtime config, or empty slice if nil
	var runtimeEnv []corev1.EnvVar
	if runtimeConfigSpec != nil {
		runtimeEnv = runtimeConfigSpec.Env
	}

	// Merge env vars with precedence: mc.Spec.Env > runtimeConfigSpec.Env > filter > defaults
	defaultEnv := []corev1.EnvVar{
		{Name: "AIM_DOWNLOADER_PROTOCOL", Value: "XET,HF_TRANSFER"},
		{Name: "TMPDIR", Value: "/tmp/"},
		{Name: "HF_HOME", Value: "/tmp/.hf"},
		{Name: "EXPECTED_SIZE_BYTES", Value: fmt.Sprintf("%d", expectedSizeBytes)},
		{Name: "MOUNT_PATH", Value: mountPath},
		{Name: "ARTIFACT_NAME", Value: mc.Name},
		{Name: "ARTIFACT_NAMESPACE", Value: mc.Namespace},
		{Name: "STALL_TIMEOUT", Value: "120"},
		{Name: "TARGET_DIR", Value: mountPath},
	}
	defaultEnv = append(defaultEnv, downloadFilterEnvVars(resolveDownloadFilter(mc, runtimeConfigSpec))...)
	newEnv := utils.MergeEnvVars(defaultEnv, cacheEnv)
	newEnv = utils.MergeEnvVars(newEnv, runtimeEnv)
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
				constants.LabelKeyCacheType: "artifact",
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
						constants.LabelKeyCacheType: "artifact",
						constants.LabelKeyComponent: "download",
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
							ImagePullPolicy: pullPolicyForImage(downloadImage),
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:  ptr.To(int64(1000)),
								RunAsGroup: ptr.To(int64(1000)),
							},
							Env:  newEnv,
							Args: []string{sourceURI},
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

func getCheckSizeJobName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "check-size"}, utils.WithHashSource(mc.UID))
	return name
}

func buildCheckSizeJob(mc *aimv1alpha1.AIMArtifact, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon, cacheEnv ...corev1.EnvVar) *batchv1.Job {
	downloadImage := resolveDownloadImage(mc, runtimeConfigSpec)

	sourceURI := effectiveSourceURI(mc)

	// Get auth env vars from runtime config and spec, plus download filter
	var runtimeEnv []corev1.EnvVar
	if runtimeConfigSpec != nil {
		runtimeEnv = runtimeConfigSpec.Env
	}
	filterEnv := downloadFilterEnvVars(resolveDownloadFilter(mc, runtimeConfigSpec))
	envVars := utils.MergeEnvVars(filterEnv, cacheEnv)
	envVars = utils.MergeEnvVars(envVars, runtimeEnv)
	envVars = utils.MergeEnvVars(envVars, mc.Spec.Env)

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
				constants.LabelKeyCacheType: "artifact",
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
						constants.LabelKeyCacheType: "artifact",
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
							ImagePullPolicy: pullPolicyForImage(downloadImage),
							Command:         []string{"/check-size.sh"},
							Args:            []string{sourceURI},
							Env:             envVars,
						},
					},
				},
			},
		},
	}
}
