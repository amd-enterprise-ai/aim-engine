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

// Package trust builds the pod contract for adding administrator-provided CA
// certificates to the public roots shipped in the artifact-downloader image.
package trust

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	InitContainerName = "aim-ca-bundle-init"

	outputVolumeName = "aim-ca-bundle"
	outputMountPath  = "/etc/aim/trust"
	outputFileName   = "ca-bundle.crt"
	OutputFilePath   = outputMountPath + "/" + outputFileName

	additionalVolumeName = "aim-additional-ca"
	additionalMountPath  = "/var/run/aim-ca/additional"
	additionalFilePath   = additionalMountPath + "/ca.crt"

	s3VolumeName = "aim-s3-additional-ca"
	s3MountPath  = "/var/run/aim-ca/s3"
	s3FilePath   = s3MountPath + "/ca.crt"
)

// Options selects additional trust sources and whether S3-specific trust
// variables should point at the combined output.
type Options struct {
	AdditionalCABundleRef *aimv1alpha1.AdditionalCABundleReference
	S3CABundleRef         *aimv1alpha1.S3CABundleReference
	IncludeS3Env          bool
	Image                 string
}

// Projection is the reusable trust contract added to a workload Pod.
type Projection struct {
	InitContainer corev1.Container
	Volumes       []corev1.Volume
	VolumeMounts  []corev1.VolumeMount
	Env           []corev1.EnvVar
}

// BuildProjection returns no projection when no additional CA is configured.
// Otherwise an init container combines the downloader image's public roots
// with all selected additional bundles.
func BuildProjection(options Options) *Projection {
	if options.AdditionalCABundleRef == nil && options.S3CABundleRef == nil {
		return nil
	}
	image := options.Image
	if image == "" {
		image = aimv1alpha1.DefaultDownloadImage
	}

	args := []string{OutputFilePath}
	volumes := []corev1.Volume{{
		Name: outputVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}}
	initMounts := []corev1.VolumeMount{{
		Name:      outputVolumeName,
		MountPath: outputMountPath,
	}}

	if ref := options.AdditionalCABundleRef; ref != nil {
		args = append(args, additionalFilePath)
		volumes = append(volumes, corev1.Volume{
			Name: additionalVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					// Preserve the public-roots fallback if the ConfigMap or key
					// disappears after reconciliation but before pod startup.
					Optional: ptr.To(true),
					Items: []corev1.KeyToPath{{
						Key:  ref.Key,
						Path: "ca.crt",
					}},
				},
			},
		})
		initMounts = append(initMounts, corev1.VolumeMount{
			Name:      additionalVolumeName,
			MountPath: additionalMountPath,
			ReadOnly:  true,
		})
	}

	if ref := options.S3CABundleRef; ref != nil {
		args = append(args, s3FilePath)
		volumes = append(volumes, s3CABundleVolume(ref))
		initMounts = append(initMounts, corev1.VolumeMount{
			Name:      s3VolumeName,
			MountPath: s3MountPath,
			ReadOnly:  true,
		})
	}

	env := []corev1.EnvVar{
		{Name: "SSL_CERT_FILE", Value: OutputFilePath},
		{Name: "REQUESTS_CA_BUNDLE", Value: OutputFilePath},
		{Name: "CURL_CA_BUNDLE", Value: OutputFilePath},
	}
	if options.IncludeS3Env {
		env = append(env,
			corev1.EnvVar{Name: "AWS_CA_BUNDLE", Value: OutputFilePath},
			corev1.EnvVar{Name: "AIM_S3_CA_BUNDLE", Value: OutputFilePath},
		)
	}

	return &Projection{
		InitContainer: corev1.Container{
			Name:            InitContainerName,
			Image:           image,
			ImagePullPolicy: utils.PullPolicyForImage(image),
			Command:         []string{"/combine-ca.sh"},
			Args:            args,
			VolumeMounts:    initMounts,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				ReadOnlyRootFilesystem:   ptr.To(true),
				RunAsNonRoot:             ptr.To(true),
				RunAsUser:                ptr.To(int64(1000)),
				RunAsGroup:               ptr.To(int64(1000)),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
		},
		Volumes: volumes,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      outputVolumeName,
			MountPath: outputMountPath,
			ReadOnly:  true,
		}},
		Env: env,
	}
}

// ApplyToPod adds a projection to a core PodSpec and one network client
// container. Managed trust variables take final precedence.
func ApplyToPod(pod *corev1.PodSpec, container *corev1.Container, projection *Projection) {
	if projection == nil {
		return
	}
	pod.InitContainers = append(pod.InitContainers, projection.InitContainer)
	pod.Volumes = append(pod.Volumes, projection.Volumes...)
	container.VolumeMounts = append(container.VolumeMounts, projection.VolumeMounts...)
	container.Env = UpsertEnv(container.Env, projection.Env)
}

// UpsertEnv adds managed trust variables with final precedence.
func UpsertEnv(base, managed []corev1.EnvVar) []corev1.EnvVar {
	result := make([]corev1.EnvVar, len(base))
	copy(result, base)
	index := make(map[string]int, len(result))
	for i := range result {
		index[result[i].Name] = i
	}
	for _, item := range managed {
		if i, ok := index[item.Name]; ok {
			result[i] = item
			continue
		}
		index[item.Name] = len(result)
		result = append(result, item)
	}
	return result
}

func s3CABundleVolume(ref *aimv1alpha1.S3CABundleReference) corev1.Volume {
	item := corev1.KeyToPath{Key: ref.Key, Path: "ca.crt"}
	if ref.Kind == "Secret" {
		return corev1.Volume{
			Name: s3VolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ref.Name,
					Items:      []corev1.KeyToPath{item},
				},
			},
		}
	}
	return corev1.Volume{
		Name: s3VolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
				Items:                []corev1.KeyToPath{item},
			},
		},
	}
}
