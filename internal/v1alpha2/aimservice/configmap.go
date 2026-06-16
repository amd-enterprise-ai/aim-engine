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
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	profileVolumePrefix = "aim-profile"
	// profileMountBase is the runtime's search root for operator-injected
	// profiles. The AIM runtime looks up profiles under
	//   /workspace/aim-runtime/profiles/custom/<aimId>/<name>
	// so mounting the assembled profile ConfigMap at <base>/<aimId> makes
	// AIM_PROFILE_ID=custom/<aimId>/<name> resolve to the file we projected.
	profileMountBase = "/workspace/aim-runtime/profiles/custom"
)

// profileYAML matches the AIM runtime's ProfileData / ModelProfileData schema
// in aim-build/src/aim_common/object_model.py.
type profileYAML struct {
	AimID      string            `json:"aim_id"`
	ModelID    string            `json:"model_id"`
	Metadata   profileMetadata   `json:"metadata"`
	EngineArgs map[string]any    `json:"engine_args"`
	EnvVars    map[string]string `json:"env_vars"`
}

// profileMetadata matches the AIM runtime's ProfileMetadata schema.
//
// We emit BOTH naming conventions so the assembled YAML is valid against
// every aim-runtime version we ship today:
//
//   - Legacy `gpu` / `gpu_count` are still REQUIRED by the runtime baked into
//     amdenterpriseai/aim-base:0.11 (and earlier). The runtime's Pydantic
//     ProfileMetadata schema validates `metadata.gpu` and `metadata.gpu_count`
//     as required fields; omitting them causes the predictor pod to crash
//     before opening port 8000.
//   - New `accelerator_model` / `accelerator_type` / `accelerator_count` are
//     the v1alpha2 vocabulary. Runtimes that don't know them ignore the
//     extras (Pydantic extras are not forbidden); runtimes that do can
//     consume them directly.
type profileMetadata struct {
	Engine              string `json:"engine"`
	GPU                 string `json:"gpu"`
	GPUCount            int32  `json:"gpu_count"`
	AcceleratorModel    string `json:"accelerator_model"`
	AcceleratorType     string `json:"accelerator_type"`
	AcceleratorCount    int32  `json:"accelerator_count"`
	ManualSelectionOnly bool   `json:"manual_selection_only"`
	Metric              string `json:"metric"`
	Precision           string `json:"precision"`
	Type                string `json:"type"`
	// Features carries optional capability tokens (e.g. "adapters") into runtime
	// ProfileMetadata; the runtime computes supports_adapters from it. Omitted
	// when empty.
	Features []string `json:"features,omitempty"`
}

// profileConfigMapName returns a deterministic ConfigMap name for a profile-based AIMService.
func profileConfigMapName(serviceName string) (string, error) {
	return utils.GenerateDerivedName(
		[]string{serviceName, "profile"},
		utils.WithHashSource(serviceName, "service-profile"),
	)
}

// assembleProfileYAML builds a complete profile YAML from an AIMProfileSpecCommon.
//
// The spec is expected to already reflect the effective profile configuration:
// when the AIMService declared spec.profileOverrides, the controller has
// materialised an overlay AIMProfile and points obs.resolvedProfileSpec at it,
// so this function does not need to re-merge user overrides on top.
func assembleProfileYAML(spec *aimv1alpha2.AIMProfileSpecCommon) ([]byte, string, error) {
	if spec == nil {
		return nil, "", fmt.Errorf("profile spec is nil")
	}

	engineArgs := make(map[string]any)
	if spec.EngineArgs != nil && len(spec.EngineArgs.Raw) > 0 {
		if err := json.Unmarshal(spec.EngineArgs.Raw, &engineArgs); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal engineArgs: %w", err)
		}
	}

	envVars := make(map[string]string)
	if spec.EngineEnv != nil {
		for k, v := range spec.EngineEnv {
			envVars[k] = v
		}
	}

	accModel := spec.AcceleratorModel
	accType := string(spec.AcceleratorType)
	accCount := spec.AcceleratorCount

	profile := profileYAML{
		AimID:   spec.AimId,
		ModelID: spec.ModelId,
		Metadata: profileMetadata{
			Engine:              spec.Engine,
			GPU:                 accModel,
			GPUCount:            accCount,
			AcceleratorModel:    accModel,
			AcceleratorType:     accType,
			AcceleratorCount:    accCount,
			ManualSelectionOnly: false,
			Metric:              string(spec.Metric),
			Precision:           string(spec.Precision),
			Type:                string(spec.Type),
			Features:            append([]string(nil), spec.Features...),
		},
		EngineArgs: engineArgs,
		EnvVars:    envVars,
	}

	yamlBytes, err := yaml.Marshal(profile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal profile YAML: %w", err)
	}

	filename := profileFilename(accModel, string(spec.Precision), accCount, string(spec.Metric))
	return yamlBytes, filename, nil
}

func profileFilename(accModel, precision string, accCount int32, metric string) string {
	accSegment := strings.ToLower(accModel)
	if accSegment == "" {
		accSegment = "none"
	}
	return fmt.Sprintf("vllm-%s-%s-tp%d-%s.yaml", accSegment, precision, accCount, metric)
}

// buildProfileConfigMap creates a ConfigMap containing the pre-assembled
// profile YAML for mounting into the InferenceService. The YAML and filename
// are produced once per reconcile in ComposeState and passed through the
// observation so the ConfigMap data and the AIM_PROFILE_ID env var the ISVC
// consumes stay consistent.
func buildProfileConfigMap(service *aimv1alpha1.AIMService, cmName, filename string, yamlBytes []byte) *corev1.ConfigMap {
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelService:      serviceLabelValue,
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
				constants.LabelK8sComponent: constants.ComponentInference,
			},
		},
		Data: map[string]string{
			filename: string(yamlBytes),
		},
	}
}

// BuildProfileVolume creates a Volume that projects the profile ConfigMap.
func BuildProfileVolume(configMapName string) corev1.Volume {
	return corev1.Volume{
		Name: profileVolumePrefix,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
			},
		},
	}
}

// BuildProfileVolumeMount creates a VolumeMount at the AIM runtime custom profiles path.
func BuildProfileVolumeMount(aimId string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      profileVolumePrefix,
		MountPath: fmt.Sprintf("%s/%s", profileMountBase, aimId),
		ReadOnly:  true,
	}
}
