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
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ResolveDownloadImage exposes the download-image resolution to other packages
// (e.g. the AIMService adapter staging Jobs) so they share the same image
// precedence: artifact spec > runtime config > build-time default.
func ResolveDownloadImage(mc *aimv1alpha1.AIMArtifact, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon) string {
	return resolveDownloadImage(mc, runtimeConfigSpec)
}

// resolveDownloadImage picks the container image for download and size-check
// jobs. Precedence: artifact spec > runtime config > build-time default.
func resolveDownloadImage(mc *aimv1alpha1.AIMArtifact, runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon) string {
	if mc != nil && len(mc.Spec.ModelDownloadImage) > 0 {
		return mc.Spec.ModelDownloadImage
	}
	if runtimeConfigSpec != nil && runtimeConfigSpec.Artifact != nil && runtimeConfigSpec.Artifact.ModelDownloadImage != "" {
		return runtimeConfigSpec.Artifact.ModelDownloadImage
	}
	return aimv1alpha1.DefaultDownloadImage
}

// PullPolicyForImage exposes pullPolicyForImage to other packages building Jobs
// that use the downloader image (e.g. AIMService adapter staging).
func PullPolicyForImage(image string) corev1.PullPolicy {
	return pullPolicyForImage(image)
}

// pullPolicyForImage delegates to utils.PullPolicyForImage; see that helper for
// the tag-aware heuristic (versioned tag -> IfNotPresent, :latest/tagless ->
// Always, digest -> IfNotPresent). Kept as a thin local alias for the Job
// builders in this package.
func pullPolicyForImage(image string) corev1.PullPolicy {
	return utils.PullPolicyForImage(image)
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
// This default reflects HuggingFace repo conventions (skip nested dirs like
// original/, onnx/, ...). It is NOT applied to s3:// sources, where the natural
// expectation is to download the whole prefix verbatim (see resolveDownloadFilter).
var defaultDownloadFilter = &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}}

// downloadJobActiveDeadlineSeconds is a hard 24h wall-clock backstop on the
// whole download Job (all pod retries). It covers the one case the progress
// monitor can't: a wedged filesystem where the monitor is paused and can't
// measure progress. On expiry the Job fails and is recreated (bytes persist on
// the PVC; HF/S3 resume). Intentionally coarse and non-configurable for now.
const downloadJobActiveDeadlineSeconds = int64(24 * 60 * 60)

// resolveDownloadFilter returns the effective download filter.
// Precedence: artifact spec > runtime config storage > default (hf:// only).
// An explicit empty filter (downloadFilter: {}) on the artifact disables all filtering.
//
// The implicit subdir-exclude default applies only to hf:// sources. For s3://
// sources the default is no filter, so the entire prefix is downloaded. Both
// the size-check and download jobs call this, so the file set they select stays
// identical and the cache PVC is never undersized.
func resolveDownloadFilter(mc *aimv1alpha1.AIMArtifact, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMDownloadFilter {
	if mc.Spec.DownloadFilter != nil {
		return mc.Spec.DownloadFilter
	}
	if runtimeConfig != nil && runtimeConfig.Storage != nil && runtimeConfig.Storage.DownloadFilter != nil {
		return runtimeConfig.Storage.DownloadFilter
	}
	if strings.HasPrefix(mc.Spec.SourceURI, "hf://") {
		return defaultDownloadFilter
	}
	return nil
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

func buildDownloadJob(
	mc *aimv1alpha1.AIMArtifact,
	runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon,
	expectedSizeBytes int64,
	includeAdditionalCABundle bool,
) *batchv1.Job {
	mountPath := "/cache/models"
	downloadImage := resolveDownloadImage(mc, runtimeConfigSpec)
	var s3Connection *aimv1alpha1.S3ConnectionConfig
	if strings.HasPrefix(mc.Spec.SourceURI, "s3://") {
		s3Connection = DirectS3Connection(runtimeConfigSpec)
		if s3Connection != nil {
			// Typed credentials and trust settings are administrator-managed.
			// Keep them inside the release-coupled downloader contract instead
			// of exposing them to an arbitrary artifact-selected image.
			downloadImage = aimv1alpha1.DefaultDownloadImage
		}
	}

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
		// Upper bound (seconds) for a single progress-monitor size measurement.
		// Generous so slow-but-healthy storage isn't mistaken for a stuck fs.
		{Name: "DU_TIMEOUT", Value: "300"},
		{Name: "TARGET_DIR", Value: mountPath},
	}
	defaultEnv = append(defaultEnv, downloadFilterEnvVars(resolveDownloadFilter(mc, runtimeConfigSpec))...)
	s3Settings := ResolveS3PodSettings(
		s3Connection,
		defaultEnv,
		runtimeEnv,
		mc.Spec.Env,
	)

	volumes := []corev1.Volume{
		{
			Name: "cache",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: GenerateCachePvcName(mc),
				},
			},
		},
	}
	volumes = append(volumes, s3Settings.Volumes...)
	volumeMounts := []corev1.VolumeMount{{Name: "cache", MountPath: mountPath}}
	volumeMounts = append(volumeMounts, s3Settings.VolumeMounts...)

	job := &batchv1.Job{
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
			ActiveDeadlineSeconds:   ptr.To(downloadJobActiveDeadlineSeconds), // see const doc
			TTLSecondsAfterFinished: ptr.To(int32(60 * 10)),                   // Cleanup after 10min to allow status observation
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
					Volumes:          volumes,
					Containers: []corev1.Container{
						{
							Name:            "model-download",
							Image:           downloadImage,
							ImagePullPolicy: pullPolicyForImage(downloadImage),
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:  ptr.To(int64(1000)),
								RunAsGroup: ptr.To(int64(1000)),
							},
							Env:          s3Settings.Env,
							Args:         []string{mc.Spec.SourceURI},
							VolumeMounts: volumeMounts,
						},
					},
				},
			},
		},
	}
	trust.ApplyToPod(
		&job.Spec.Template.Spec,
		&job.Spec.Template.Spec.Containers[0],
		ResolveTrustProjection(runtimeConfigSpec, mc.Spec.SourceURI, includeAdditionalCABundle, downloadImage),
	)
	return job
}

func getCheckSizeJobName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "check-size"}, utils.WithHashSource(mc.UID))
	return name
}

func buildCheckSizeJob(
	mc *aimv1alpha1.AIMArtifact,
	runtimeConfigSpec *aimv1alpha1.AIMRuntimeConfigCommon,
	includeAdditionalCABundle bool,
) *batchv1.Job {
	downloadImage := resolveDownloadImage(mc, runtimeConfigSpec)
	var s3Connection *aimv1alpha1.S3ConnectionConfig
	if strings.HasPrefix(mc.Spec.SourceURI, "s3://") {
		s3Connection = DirectS3Connection(runtimeConfigSpec)
		if s3Connection != nil {
			downloadImage = aimv1alpha1.DefaultDownloadImage
		}
	}

	// Get auth env vars from runtime config and spec, plus download filter
	var runtimeEnv []corev1.EnvVar
	if runtimeConfigSpec != nil {
		runtimeEnv = runtimeConfigSpec.Env
	}
	filterEnv := downloadFilterEnvVars(resolveDownloadFilter(mc, runtimeConfigSpec))
	s3Settings := ResolveS3PodSettings(
		s3Connection,
		filterEnv,
		runtimeEnv,
		mc.Spec.Env,
	)

	job := &batchv1.Job{
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
					Volumes:          s3Settings.Volumes,
					Containers: []corev1.Container{
						{
							Name:            "check-size",
							Image:           downloadImage,
							ImagePullPolicy: pullPolicyForImage(downloadImage),
							Command:         []string{"/check-size.sh"},
							Args:            []string{mc.Spec.SourceURI},
							Env:             s3Settings.Env,
							VolumeMounts:    s3Settings.VolumeMounts,
						},
					},
				},
			},
		},
	}
	trust.ApplyToPod(
		&job.Spec.Template.Spec,
		&job.Spec.Template.Spec.Containers[0],
		ResolveTrustProjection(runtimeConfigSpec, mc.Spec.SourceURI, includeAdditionalCABundle, downloadImage),
	)
	return job
}
