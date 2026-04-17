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

package v1alpha1utils

import (
	"encoding/json"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func newTestSpec() *aimv1alpha1.AIMServiceTemplateSpecCommon {
	metric := aimv1alpha1.AIMMetric("latency")
	precision := aimv1alpha1.AIMPrecision("fp16")
	profileType := aimv1alpha1.AIMProfileTypeUnoptimized

	engineArgsJSON, _ := json.Marshal(map[string]any{
		"dtype":                  "float16",
		"gpu-memory-utilization": 0.95,
		"tensor-parallel-size":   1,
	})

	return &aimv1alpha1.AIMServiceTemplateSpecCommon{
		AimId:   testAimID,
		ModelId: testAimID,
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Metric:    &metric,
			Precision: &precision,
			Hardware: &aimv1alpha1.AIMHardwareRequirements{
				GPU: &aimv1alpha1.AIMGpuRequirements{
					Model:    "MI300X",
					Requests: 1,
				},
			},
		},
		Type: &profileType,
		CustomProfile: &aimv1alpha1.AIMCustomProfile{
			EngineArgs: &apiextensionsv1.JSON{Raw: engineArgsJSON},
			EnvVars: map[string]string{
				"HIP_FORCE_DEV_KERNARG":     "1",
				"PYTORCH_TUNABLEOP_ENABLED": "1",
			},
		},
	}
}

func TestHasCustomProfile(t *testing.T) {
	t.Run("nil spec", func(t *testing.T) {
		if HasCustomProfile(nil) {
			t.Error("expected false for nil spec")
		}
	})

	t.Run("no custom profile", func(t *testing.T) {
		spec := &aimv1alpha1.AIMServiceTemplateSpecCommon{}
		if HasCustomProfile(spec) {
			t.Error("expected false when customProfile is nil")
		}
	})

	t.Run("with custom profile", func(t *testing.T) {
		spec := newTestSpec()
		if !HasCustomProfile(spec) {
			t.Error("expected true when customProfile is set")
		}
	})
}

const testAimID = "meta-llama/Llama-3-8B"

func TestAssembleProfileYAML(t *testing.T) {
	t.Run("full profile", func(t *testing.T) {
		spec := newTestSpec()
		yamlBytes, filename, err := AssembleProfileYAML(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if filename != "vllm-mi300x-fp16-tp1-latency.yaml" {
			t.Errorf("unexpected filename: %s", filename)
		}

		// Parse the YAML back and verify fields
		var profile profileYAML
		if err := yaml.Unmarshal(yamlBytes, &profile); err != nil {
			t.Fatalf("failed to unmarshal generated YAML: %v", err)
		}

		if profile.AimID != testAimID {
			t.Errorf("unexpected aim_id: %s", profile.AimID)
		}
		if profile.ModelID != testAimID {
			t.Errorf("unexpected model_id: %s", profile.ModelID)
		}
		if profile.Metadata.Engine != "vllm" {
			t.Errorf("unexpected engine: %s", profile.Metadata.Engine)
		}
		if profile.Metadata.GPU != "MI300X" {
			t.Errorf("unexpected gpu: %s", profile.Metadata.GPU)
		}
		if profile.Metadata.GPUCount != 1 {
			t.Errorf("unexpected gpu_count: %d", profile.Metadata.GPUCount)
		}
		if profile.Metadata.Metric != "latency" {
			t.Errorf("unexpected metric: %s", profile.Metadata.Metric)
		}
		if profile.Metadata.Precision != "fp16" {
			t.Errorf("unexpected precision: %s", profile.Metadata.Precision)
		}
		if profile.Metadata.Type != "unoptimized" {
			t.Errorf("unexpected type: %s", profile.Metadata.Type)
		}
		if profile.Metadata.ManualSelectionOnly {
			t.Error("manual_selection_only should be false")
		}

		// Verify engine args
		if v, ok := profile.EngineArgs["dtype"]; !ok || v != "float16" {
			t.Errorf("unexpected dtype in engine_args: %v", profile.EngineArgs)
		}

		// Verify env vars
		if profile.EnvVars["HIP_FORCE_DEV_KERNARG"] != "1" {
			t.Errorf("unexpected env_vars: %v", profile.EnvVars)
		}
	})

	t.Run("empty engine args and env vars produce empty maps", func(t *testing.T) {
		spec := newTestSpec()
		spec.CustomProfile.EngineArgs = nil
		spec.CustomProfile.EnvVars = nil

		yamlBytes, _, err := AssembleProfileYAML(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var profile profileYAML
		if err := yaml.Unmarshal(yamlBytes, &profile); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if profile.EngineArgs == nil {
			t.Error("engine_args should be empty map, not nil")
		}
		if profile.EnvVars == nil {
			t.Error("env_vars should be empty map, not nil")
		}
	})

	t.Run("nil spec returns error", func(t *testing.T) {
		_, _, err := AssembleProfileYAML(nil)
		if err == nil {
			t.Error("expected error for nil spec")
		}
	})

	t.Run("nil customProfile returns error", func(t *testing.T) {
		spec := &aimv1alpha1.AIMServiceTemplateSpecCommon{}
		_, _, err := AssembleProfileYAML(spec)
		if err == nil {
			t.Error("expected error for nil customProfile")
		}
	})
}

func TestProfileFilename(t *testing.T) {
	tests := []struct {
		gpu, precision string
		gpuCount       int32
		metric         string
		expected       string
	}{
		{"MI300X", "fp16", 1, "latency", "vllm-mi300x-fp16-tp1-latency.yaml"},
		{"MI325X", "fp8", 4, "throughput", "vllm-mi325x-fp8-tp4-throughput.yaml"},
		{"MI300X", "bf16", 2, "latency", "vllm-mi300x-bf16-tp2-latency.yaml"},
	}
	for _, tt := range tests {
		got := ProfileFilename(tt.gpu, tt.precision, tt.gpuCount, tt.metric)
		if got != tt.expected {
			t.Errorf("ProfileFilename(%s, %s, %d, %s) = %s, want %s",
				tt.gpu, tt.precision, tt.gpuCount, tt.metric, got, tt.expected)
		}
	}
}

func TestServiceCustomProfileConfigMapName(t *testing.T) {
	t.Run("contains custom-profile part", func(t *testing.T) {
		name := ServiceCustomProfileConfigMapName("my-service")
		if !strings.Contains(name, "custom-profile") {
			t.Errorf("expected name to contain 'custom-profile', got: %s", name)
		}
		if !strings.Contains(name, "my-service") {
			t.Errorf("expected name to contain 'my-service', got: %s", name)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		a := ServiceCustomProfileConfigMapName("my-service")
		b := ServiceCustomProfileConfigMapName("my-service")
		if a != b {
			t.Errorf("expected deterministic names, got %q and %q", a, b)
		}
	})

	t.Run("respects max length", func(t *testing.T) {
		longName := strings.Repeat("a", 63)
		name := ServiceCustomProfileConfigMapName(longName)
		if len(name) > 63 {
			t.Errorf("name exceeds 63 chars: %d", len(name))
		}
	})
}

func TestTemplateCustomProfileConfigMapName(t *testing.T) {
	t.Run("contains custom-profile part", func(t *testing.T) {
		name := TemplateCustomProfileConfigMapName("my-template")
		if !strings.Contains(name, "custom-profile") {
			t.Errorf("expected name to contain 'custom-profile', got: %s", name)
		}
		if !strings.Contains(name, "my-template") {
			t.Errorf("expected name to contain 'my-template', got: %s", name)
		}
	})

	t.Run("service and template names do not collide", func(t *testing.T) {
		owner := "shared-owner-name"
		serviceName := ServiceCustomProfileConfigMapName(owner)
		templateName := TemplateCustomProfileConfigMapName(owner)
		if serviceName == templateName {
			t.Fatalf("expected unique names, got service=%q template=%q", serviceName, templateName)
		}
	})
}

func TestBuildCustomProfileConfigMap(t *testing.T) {
	yamlData := []byte("aim_id: test")
	filename := "vllm-mi300x-fp16-tp1-latency.yaml"
	labels := map[string]string{"app": "test"}

	cm := BuildCustomProfileConfigMap("my-cm", "default", yamlData, filename, labels)

	if cm.Name != "my-cm" {
		t.Errorf("unexpected name: %s", cm.Name)
	}
	if cm.Namespace != "default" {
		t.Errorf("unexpected namespace: %s", cm.Namespace)
	}
	if cm.Labels["app"] != "test" {
		t.Errorf("unexpected labels: %v", cm.Labels)
	}
	if cm.Data[filename] != "aim_id: test" {
		t.Errorf("unexpected data: %v", cm.Data)
	}
}

func TestBuildCustomProfileVolume(t *testing.T) {
	vol := BuildCustomProfileVolume("my-configmap")

	if vol.Name != customProfileVolumePrefix {
		t.Errorf("unexpected volume name: %s", vol.Name)
	}
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source")
	}
	if vol.ConfigMap.Name != "my-configmap" {
		t.Errorf("unexpected ConfigMap name: %s", vol.ConfigMap.Name)
	}
}

func TestBuildCustomProfileVolumeMount(t *testing.T) {
	mount := BuildCustomProfileVolumeMount(testAimID)

	if mount.Name != customProfileVolumePrefix {
		t.Errorf("unexpected mount name: %s", mount.Name)
	}
	expectedPath := "/workspace/aim-runtime/profiles/custom/meta-llama/Llama-3-8B"
	if mount.MountPath != expectedPath {
		t.Errorf("unexpected mount path: %s, want %s", mount.MountPath, expectedPath)
	}
	if !mount.ReadOnly {
		t.Error("mount should be read-only")
	}

	// Path must contain /custom/ and not /general/
	if !strings.Contains(mount.MountPath, "/custom/") {
		t.Error("mount path must contain /custom/")
	}
	if strings.Contains(mount.MountPath, "/general/") {
		t.Error("mount path must not contain /general/")
	}
}

func TestCustomProfileID(t *testing.T) {
	id := CustomProfileID(testAimID, "vllm-mi300x-fp16-tp1-latency.yaml")
	expected := "custom/meta-llama/Llama-3-8B/vllm-mi300x-fp16-tp1-latency"
	if id != expected {
		t.Errorf("CustomProfileID = %s, want %s", id, expected)
	}
}

func TestCustomProfileEnvVars(t *testing.T) {
	envVars := CustomProfileEnvVars(testAimID, "vllm-mi300x-fp16-tp1-latency.yaml")

	if len(envVars) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(envVars))
	}

	aimIDFound := false
	profileIDFound := false
	for _, ev := range envVars {
		switch ev.Name {
		case constants.EnvAIMID:
			aimIDFound = true
			if ev.Value != testAimID {
				t.Errorf("unexpected AIM_ID value: %s", ev.Value)
			}
		case constants.EnvAIMProfileID:
			profileIDFound = true
			expected := "custom/meta-llama/Llama-3-8B/vllm-mi300x-fp16-tp1-latency"
			if ev.Value != expected {
				t.Errorf("unexpected AIM_PROFILE_ID value: %s, want %s", ev.Value, expected)
			}
		}
	}

	if !aimIDFound {
		t.Error("AIM_ID env var not found")
	}
	if !profileIDFound {
		t.Error("AIM_PROFILE_ID env var not found")
	}
}
