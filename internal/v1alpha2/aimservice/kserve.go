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

package aimservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1svc "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
)

// fetchInferenceService fetches the existing InferenceService for the
// service. A name-generation failure is recorded on the FetchResult so
// ComposeState can surface it through component health instead of being
// silently dropped.
func fetchInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*servingv1beta1.InferenceService] {
	isvcName, err := v1alpha1svc.GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      isvcName,
	}, &servingv1beta1.InferenceService{})
}

// buildInferenceServiceFromProfile constructs a KServe InferenceService from profile data.
// Name and ConfigMap references come from the precomputed observation so the
// fetched and planned ISVC are guaranteed to match and the mounted ConfigMap
// name matches what the ConfigMap builder applies.
func buildInferenceServiceFromProfile(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) *servingv1beta1.InferenceService {
	profileSpec := obs.resolvedProfileSpec
	profileStatus := obs.resolvedProfileStatus
	if profileSpec == nil || profileStatus == nil {
		return nil
	}
	if obs.isvcName == "" || obs.configMapName == "" {
		return nil
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	profileLabelValue, _ := utils.SanitizeLabelValue(obs.profileName)

	labels := map[string]string{
		constants.LabelK8sComponent: constants.ComponentInference,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
		constants.LabelProfile:      profileLabelValue,
	}

	if string(profileSpec.Metric) != "" {
		metricVal, _ := utils.SanitizeLabelValue(string(profileSpec.Metric))
		labels[constants.LabelMetric] = metricVal
	}
	if string(profileSpec.Precision) != "" {
		precisionVal, _ := utils.SanitizeLabelValue(string(profileSpec.Precision))
		labels[constants.LabelPrecision] = precisionVal
	}

	// Build environment variables. Order of precedence (last wins on conflict):
	//   profileSpec.ContainerEnv (author defaults)
	//     -> framework AIM_* vars (must not be overridable from the profile)
	//     -> service.Spec.ProfileOverrides.ContainerEnv (explicit user override)
	envVars := upsertEnvVars(nil, profileSpec.ContainerEnv)
	envVars = upsertEnvVars(envVars, buildFrameworkEnvVars(profileSpec, obs.profileYAMLName))
	if service.Spec.ProfileOverrides != nil {
		envVars = upsertEnvVars(envVars, service.Spec.ProfileOverrides.ContainerEnv)
	}

	resources := resolveResourcesFromProfile(service, profileSpec, profileStatus)

	dshmSizeLimit := resource.MustParse(constants.DefaultSharedMemorySize)

	isvc := &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       "InferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        obs.isvcName,
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: make(map[string]string),
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				PodSpec: servingv1beta1.PodSpec{
					ImagePullSecrets:   buildPullSecrets(service, profileSpec),
					ServiceAccountName: resolveServiceAccountName(service, profileSpec),
					PriorityClassName:  service.Spec.PriorityClassName,
					Containers: []corev1.Container{
						{
							Name:            constants.ContainerKServe,
							Image:           profileSpec.Image,
							ImagePullPolicy: corev1.PullAlways,
							Env:             envVars,
							Resources:       resources,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: constants.DefaultHTTPPort,
									Name:          "http",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      constants.VolumeSharedMemory,
									MountPath: constants.MountPathSharedMemory,
								},
								BuildProfileVolumeMount(profileSpec.AimId),
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: constants.VolumeSharedMemory,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									Medium:    corev1.StorageMediumMemory,
									SizeLimit: &dshmSizeLimit,
								},
							},
						},
						BuildProfileVolume(obs.configMapName),
					},
				},
			},
		},
	}

	// Wire replicas, KEDA autoscaling, and autoscaler annotations. Shared with
	// the v1alpha1 template pipeline so both paths produce the same HPA shape
	// and semantics for Spec.Replicas / MinReplicas / MaxReplicas / AutoScaling.
	v1alpha1svc.ConfigureReplicasAndAutoscaling(isvc, service)

	if profileStatus.ResolvedNodeAffinity != nil {
		isvc.Spec.Predictor.Affinity = &corev1.Affinity{
			NodeAffinity: profileStatus.ResolvedNodeAffinity,
		}
	}

	addProfileCacheVolumes(isvc, obs)

	return isvc
}

// upsertEnvVars returns base with overrides applied: for each entry in overrides,
// any existing entry in base with the same Name is replaced in place; otherwise
// it is appended. Nil base is treated as empty. The returned slice is always
// distinct from base (no aliasing of the caller's slice header).
func upsertEnvVars(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	result := append([]corev1.EnvVar(nil), base...)
	for _, o := range overrides {
		replaced := false
		for i := range result {
			if result[i].Name == o.Name {
				result[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, o)
		}
	}
	return result
}

// addProfileCacheVolumes adds resolved artifact PVC volumes from the profile cache to the ISVC.
func addProfileCacheVolumes(isvc *servingv1beta1.InferenceService, obs ServiceObservation) {
	if len(isvc.Spec.Predictor.Containers) == 0 {
		return
	}

	if !obs.profileCacheReady || obs.profileCache.Value == nil {
		return
	}

	container := &isvc.Spec.Predictor.Containers[0]
	for _, resolved := range obs.profileCache.Value.Status.Artifacts {
		if resolved.Status != constants.AIMStatusReady || resolved.PersistentVolumeClaim == "" {
			continue
		}

		volumeName := utils.MakeRFC1123Compliant(resolved.Name)
		volumeName = strings.ReplaceAll(volumeName, ".", "-")

		isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: resolved.PersistentVolumeClaim,
				},
			},
		})

		mountPath := resolved.MountPoint
		if mountPath == "" {
			safeModelName := strings.ReplaceAll(resolved.Model, "..", "")
			if safeModelName == "" || safeModelName == "." {
				safeModelName = volumeName
			}
			mountPath = filepath.Join(constants.AIMCacheBasePath, safeModelName)
		}

		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		})
	}
}

// buildFrameworkEnvVars returns the operator-managed environment variables
// that must be present on the predictor container for the AIM runtime to
// locate the projected profile and, when model caching is active, to redirect
// model loading to the PVC-backed local path.
//
// These vars are treated as framework-owned: profile.ContainerEnv cannot
// override them (we layer them on top), but service.Spec.ProfileOverrides
// still can since user intent wins.
func buildFrameworkEnvVars(profileSpec *aimv1alpha2.AIMProfileSpecCommon, profileYAMLFilename string) []corev1.EnvVar {
	profileName := strings.TrimSuffix(profileYAMLFilename, ".yaml")
	aimProfileID := fmt.Sprintf("custom/%s/%s", profileSpec.AimId, profileName)

	// AIM_PROFILE_ID points the runtime at the operator-assembled profile mounted
	// under /workspace/aim-runtime/profiles/custom/<aimId>/<name>.
	vars := []corev1.EnvVar{
		{Name: constants.EnvAIMProfileID, Value: aimProfileID},
	}

	// When the profile declares modelSources, the AIMProfileCache has populated
	// a PVC that we mount at /workspace/cache/<modelId>. AIM_CACHE_PATH and
	// AIM_MODEL_ID tell the runtime to redirect --model to that local path
	// instead of pulling from HuggingFace Hub.
	//
	// The aim-runtime enforces `AIM_ID` and `AIM_MODEL_ID` as mutually exclusive
	// (one picks an image-embedded family profile, the other redirects the model
	// location). AIM_PROFILE_ID alone is sufficient to locate the custom profile,
	// so we only set AIM_ID when we are NOT redirecting the model. Per-AIM images
	// also bake `ENV AIM_ID=...` into the container so they can run standalone;
	// we explicitly clobber it to the empty string when we set AIM_MODEL_ID to
	// avoid the runtime's mutual-exclusivity check rejecting the pod.
	if len(profileSpec.ModelSources) > 0 {
		vars = append(vars,
			corev1.EnvVar{Name: constants.EnvAIMID, Value: ""},
			corev1.EnvVar{Name: constants.EnvAIMCachePath, Value: constants.AIMCacheBasePath},
			corev1.EnvVar{Name: constants.EnvAIMModelID, Value: profileSpec.ModelSources[0].ModelID},
		)
	} else {
		vars = append(vars,
			corev1.EnvVar{Name: constants.EnvAIMID, Value: profileSpec.AimId},
		)
	}

	return vars
}

func resolveResourcesFromProfile(
	service *aimv1alpha1.AIMService,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
	profileStatus *aimv1alpha2.AIMProfileStatus,
) corev1.ResourceRequirements {
	// Service-level resource overrides take precedence
	if service.Spec.Resources != nil {
		return *service.Spec.Resources
	}

	// Use profile status.resources (computed by profile controller)
	if profileStatus != nil && profileStatus.Resources != nil {
		return *profileStatus.Resources
	}

	// Fall back to spec.resources
	if profileSpec.Resources != nil {
		return *profileSpec.Resources
	}

	return corev1.ResourceRequirements{}
}

func buildPullSecrets(service *aimv1alpha1.AIMService, profileSpec *aimv1alpha2.AIMProfileSpecCommon) []corev1.LocalObjectReference {
	if len(service.Spec.ImagePullSecrets) > 0 {
		return utils.CopyPullSecrets(service.Spec.ImagePullSecrets)
	}
	return utils.CopyPullSecrets(profileSpec.ImagePullSecrets)
}

func resolveServiceAccountName(service *aimv1alpha1.AIMService, profileSpec *aimv1alpha2.AIMProfileSpecCommon) string {
	if service.Spec.ServiceAccountName != "" {
		return service.Spec.ServiceAccountName
	}
	return profileSpec.ServiceAccountName
}
