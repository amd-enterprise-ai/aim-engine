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
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	customProfileVolumePrefix = "custom-profile"
	customProfileMountBase    = "/workspace/aim-runtime/profiles/custom"
)

// profileYAML is the internal representation of a complete AIM profile YAML file.
// Field names use snake_case to match the AIM runtime's expected schema.
type profileYAML struct {
	AimID      string            `json:"aim_id"`
	ModelID    string            `json:"model_id"`
	Metadata   profileMetadata   `json:"metadata"`
	EngineArgs map[string]any    `json:"engine_args"`
	EnvVars    map[string]string `json:"env_vars"`
}

type profileMetadata struct {
	Engine              string `json:"engine"`
	GPU                 string `json:"gpu"`
	GPUCount            int32  `json:"gpu_count"`
	ManualSelectionOnly bool   `json:"manual_selection_only"`
	Metric              string `json:"metric"`
	Precision           string `json:"precision"`
	Type                string `json:"type"`
}

// HasCustomProfile returns true if the template spec has a custom profile defined.
func HasCustomProfile(spec *aimv1alpha1.AIMServiceTemplateSpecCommon) bool {
	return spec != nil && spec.CustomProfile != nil
}

// AssembleProfileYAML builds a complete profile YAML from template spec fields.
// Returns the YAML bytes, the generated filename, and any error.
// The profile follows the model_profile_schema.json format expected by the AIM runtime.
func AssembleProfileYAML(spec *aimv1alpha1.AIMServiceTemplateSpecCommon) ([]byte, string, error) {
	if spec == nil || spec.CustomProfile == nil {
		return nil, "", fmt.Errorf("spec or customProfile is nil")
	}

	// Parse engine args from apiextensionsv1.JSON to map[string]any
	engineArgs := make(map[string]any)
	if spec.CustomProfile.EngineArgs != nil && len(spec.CustomProfile.EngineArgs.Raw) > 0 {
		if err := json.Unmarshal(spec.CustomProfile.EngineArgs.Raw, &engineArgs); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal engineArgs: %w", err)
		}
	}

	envVars := make(map[string]string)
	if spec.CustomProfile.EnvVars != nil {
		envVars = spec.CustomProfile.EnvVars
	}

	// Resolve metadata fields
	gpuModel := ""
	gpuCount := int32(0)
	if spec.Hardware != nil && spec.Hardware.GPU != nil {
		gpuModel = spec.Hardware.GPU.Model
		gpuCount = spec.Hardware.GPU.Requests
	}

	metric := ""
	if spec.Metric != nil {
		metric = string(*spec.Metric)
	}

	precision := ""
	if spec.Precision != nil {
		precision = string(*spec.Precision)
	}

	profileType := "unoptimized"
	if spec.Type != nil {
		profileType = string(*spec.Type)
	}

	profile := profileYAML{
		AimID:   spec.AimId,
		ModelID: spec.ModelId,
		Metadata: profileMetadata{
			Engine:              "vllm",
			GPU:                 gpuModel,
			GPUCount:            gpuCount,
			ManualSelectionOnly: false,
			Metric:              metric,
			Precision:           precision,
			Type:                profileType,
		},
		EngineArgs: engineArgs,
		EnvVars:    envVars,
	}

	yamlBytes, err := yaml.Marshal(profile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal profile YAML: %w", err)
	}

	filename := ProfileFilename(gpuModel, precision, gpuCount, metric)
	return yamlBytes, filename, nil
}

// ProfileFilename generates a deterministic profile filename following the AIM convention:
// {engine}-{gpu}-{precision}-tp{gpu_count}-{metric}.yaml
func ProfileFilename(gpu, precision string, gpuCount int32, metric string) string {
	gpu = strings.ToLower(gpu)
	return fmt.Sprintf("vllm-%s-%s-tp%d-%s.yaml", gpu, precision, gpuCount, metric)
}

// ServiceCustomProfileConfigMapName returns a deterministic ConfigMap name for an AIMService.
func ServiceCustomProfileConfigMapName(serviceName string) string {
	name, _ := utils.GenerateDerivedName(
		[]string{serviceName, "custom-profile"},
		utils.WithHashSource(serviceName, "service-custom-profile"),
	)
	return name
}

// TemplateCustomProfileConfigMapName returns a deterministic ConfigMap name for a template.
func TemplateCustomProfileConfigMapName(templateName string) string {
	name, _ := utils.GenerateDerivedName(
		[]string{templateName, "custom-profile"},
		utils.WithHashSource(templateName, "template-custom-profile"),
	)
	return name
}

// BuildCustomProfileConfigMap creates a ConfigMap containing the assembled profile YAML.
// Ownership (ownerReference) should be set by the caller.
func BuildCustomProfileConfigMap(name, namespace string, profileYAML []byte, filename string, labels map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			filename: string(profileYAML),
		},
	}
}

// BuildCustomProfileVolume creates a Volume that projects the custom profile ConfigMap.
func BuildCustomProfileVolume(configMapName string) corev1.Volume {
	return corev1.Volume{
		Name: customProfileVolumePrefix,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
			},
		},
	}
}

// BuildCustomProfileVolumeMount creates a VolumeMount at the AIM runtime custom profiles path.
// The mount path is /workspace/aim-runtime/profiles/custom/{aimId}/ by convention.
func BuildCustomProfileVolumeMount(aimId string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      customProfileVolumePrefix,
		MountPath: fmt.Sprintf("%s/%s", customProfileMountBase, aimId),
		ReadOnly:  true,
	}
}

// CustomProfileID computes the AIM_PROFILE_ID value for a custom profile.
// Format: custom/{aimId}/{profileName} where profileName is the filename without extension.
func CustomProfileID(aimId, filename string) string {
	profileName := strings.TrimSuffix(filename, ".yaml")
	return fmt.Sprintf("custom/%s/%s", aimId, profileName)
}

// CustomProfileEnvVars returns the environment variables needed to select a custom profile:
// AIM_ID and AIM_PROFILE_ID.
func CustomProfileEnvVars(aimId, filename string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: constants.EnvAIMID, Value: aimId},
		{Name: constants.EnvAIMProfileID, Value: CustomProfileID(aimId, filename)},
	}
}
