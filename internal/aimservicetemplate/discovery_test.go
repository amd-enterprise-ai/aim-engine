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

package aimservicetemplate

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// ============================================================================
// EXTRACT LAST VALID JSON ARRAY TESTS
// ============================================================================

func TestExtractLastValidJSONArray(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		wantErr     bool
		wantProfile string // Expected engine value from the first result's profile
	}{
		{
			name:        "clean JSON array",
			input:       []byte(`[{"filename":"profile.yaml","profile":{"model":"meta-llama/Llama-3.1-8B-Instruct","quantized_model":"","metadata":{"engine":"vllm","gpu":"MI300X","gpu_count":1,"metric":"throughput","precision":"fp16","type":"optimized"},"engine_args":{"tensor_parallel_size":1},"env_vars":{}},"models":[{"name":"meta-llama/Llama-3.1-8B-Instruct","source":"hf://meta-llama/Llama-3.1-8B-Instruct","size_gb":16.06}]}]`),
			wantErr:     false,
			wantProfile: "vllm",
		},
		{
			name: "JSON with leading noise",
			input: []byte(`INFO 2024-01-01 Starting discovery...
WARN Something happened
[{"filename":"profile.yaml","profile":{"model":"test","quantized_model":"","metadata":{"engine":"tgi","gpu":"MI300X","gpu_count":2,"metric":"latency","precision":"fp8","type":"preview"},"engine_args":{},"env_vars":{}},"models":[]}]`),
			wantErr:     false,
			wantProfile: "tgi",
		},
		{
			name: "JSON with trailing noise",
			input: []byte(`[{"filename":"profile.yaml","profile":{"model":"test","quantized_model":"","metadata":{"engine":"vllm","gpu":"MI325X","gpu_count":4,"metric":"throughput","precision":"fp16","type":"unoptimized"},"engine_args":{},"env_vars":{}},"models":[]}]
INFO Done.
DEBUG Cleanup complete`),
			wantErr:     false,
			wantProfile: "vllm",
		},
		{
			name: "JSON with both leading and trailing noise",
			input: []byte(`===== Discovery Starting =====
DEBUG Initializing...
[{"filename":"profile.yaml","profile":{"model":"test","quantized_model":"","metadata":{"engine":"vllm","gpu":"MI300X","gpu_count":1,"metric":"throughput","precision":"fp16","type":"optimized"},"engine_args":{},"env_vars":{}},"models":[]}]
===== Discovery Complete =====`),
			wantErr:     false,
			wantProfile: "vllm",
		},
		{
			name:    "empty input",
			input:   []byte{},
			wantErr: true,
		},
		{
			name:    "no JSON array",
			input:   []byte(`Just some random text without any JSON`),
			wantErr: true,
		},
		{
			name:    "invalid JSON structure",
			input:   []byte(`[{"unclosed": "object"`),
			wantErr: true,
		},
		{
			name:    "empty array",
			input:   []byte(`[]`),
			wantErr: true,
		},
		{
			name:    "object instead of array",
			input:   []byte(`{"key": "value"}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractLastValidJSONArray(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Parse the result to verify it's valid
			ctx := context.Background()
			parsed, err := parseDiscoveryJSON(ctx, result)
			if err != nil {
				t.Errorf("extracted JSON is not valid: %v", err)
				return
			}

			if len(parsed) == 0 {
				t.Error("parsed result is empty")
				return
			}

			if parsed[0].Profile.Metadata.Engine != tt.wantProfile {
				t.Errorf("engine = %q, want %q", parsed[0].Profile.Metadata.Engine, tt.wantProfile)
			}
		})
	}
}

// ============================================================================
// PARSE DISCOVERY JSON TESTS
// ============================================================================

func TestParseDiscoveryJSON(t *testing.T) {
	tests := []struct {
		name          string
		input         []byte
		wantErr       bool
		wantResultLen int
		wantEngine    string
		wantGPU       string
		wantGPUCount  int32
		wantMetric    string
		wantPrecision string
		wantType      string
		wantModelsLen int
		wantModelName string
		wantModelSize float64
	}{
		{
			name: "valid discovery output",
			input: []byte(`[{
				"filename": "profile.yaml",
				"profile": {
					"model": "meta-llama/Llama-3.1-8B-Instruct",
					"quantized_model": "",
					"metadata": {
						"engine": "vllm",
						"gpu": "MI300X",
						"gpu_count": 2,
						"metric": "latency",
						"precision": "fp8",
						"type": "optimized"
					},
					"engine_args": {"tensor_parallel_size": 2},
					"env_vars": {"VLLM_ATTENTION_BACKEND": "ROCM_FLASH"}
				},
				"models": [
					{
						"name": "meta-llama/Llama-3.1-8B-Instruct",
						"source": "hf://meta-llama/Llama-3.1-8B-Instruct",
						"size_gb": 16.06
					}
				]
			}]`),
			wantErr:       false,
			wantResultLen: 1,
			wantEngine:    "vllm",
			wantGPU:       "MI300X",
			wantGPUCount:  2,
			wantMetric:    "latency",
			wantPrecision: "fp8",
			wantType:      "optimized",
			wantModelsLen: 1,
			wantModelName: "meta-llama/Llama-3.1-8B-Instruct",
			wantModelSize: 16.06,
		},
		{
			name: "multiple profiles",
			input: []byte(`[
				{"filename": "profile1.yaml", "profile": {"model": "test1", "quantized_model": "", "metadata": {"engine": "vllm", "gpu": "MI300X", "gpu_count": 1, "metric": "throughput", "precision": "fp16", "type": "optimized"}, "engine_args": {}, "env_vars": {}}, "models": []},
				{"filename": "profile2.yaml", "profile": {"model": "test2", "quantized_model": "", "metadata": {"engine": "tgi", "gpu": "MI325X", "gpu_count": 4, "metric": "latency", "precision": "fp8", "type": "preview"}, "engine_args": {}, "env_vars": {}}, "models": []}
			]`),
			wantErr:       false,
			wantResultLen: 2,
			wantEngine:    "vllm", // First profile
		},
		{
			name:          "empty array",
			input:         []byte(`[]`),
			wantErr:       false,
			wantResultLen: 0,
		},
		{
			name:    "invalid JSON",
			input:   []byte(`not json at all`),
			wantErr: true,
		},
		{
			name: "JSON with stderr mixed in",
			input: []byte(`WARNING: something
[{"filename": "profile.yaml", "profile": {"model": "test", "quantized_model": "", "metadata": {"engine": "vllm", "gpu": "MI300X", "gpu_count": 1, "metric": "throughput", "precision": "fp16", "type": "unoptimized"}, "engine_args": {}, "env_vars": {}}, "models": []}]
INFO: done`),
			wantErr:       false,
			wantResultLen: 1,
			wantEngine:    "vllm",
			wantType:      "unoptimized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results, err := parseDiscoveryJSON(ctx, tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(results) != tt.wantResultLen {
				t.Errorf("result count = %d, want %d", len(results), tt.wantResultLen)
				return
			}

			if tt.wantResultLen == 0 {
				return
			}

			result := results[0]

			if tt.wantEngine != "" && result.Profile.Metadata.Engine != tt.wantEngine {
				t.Errorf("engine = %q, want %q", result.Profile.Metadata.Engine, tt.wantEngine)
			}

			if tt.wantGPU != "" && result.Profile.Metadata.GPU != tt.wantGPU {
				t.Errorf("gpu = %q, want %q", result.Profile.Metadata.GPU, tt.wantGPU)
			}

			if tt.wantGPUCount != 0 && result.Profile.Metadata.GPUCount != tt.wantGPUCount {
				t.Errorf("gpu_count = %d, want %d", result.Profile.Metadata.GPUCount, tt.wantGPUCount)
			}

			if tt.wantMetric != "" && result.Profile.Metadata.Metric != tt.wantMetric {
				t.Errorf("metric = %q, want %q", result.Profile.Metadata.Metric, tt.wantMetric)
			}

			if tt.wantPrecision != "" && result.Profile.Metadata.Precision != tt.wantPrecision {
				t.Errorf("precision = %q, want %q", result.Profile.Metadata.Precision, tt.wantPrecision)
			}

			if tt.wantType != "" && result.Profile.Metadata.Type != tt.wantType {
				t.Errorf("type = %q, want %q", result.Profile.Metadata.Type, tt.wantType)
			}

			if tt.wantModelsLen != 0 && len(result.Models) != tt.wantModelsLen {
				t.Errorf("models count = %d, want %d", len(result.Models), tt.wantModelsLen)
			}

			if tt.wantModelName != "" && len(result.Models) > 0 && result.Models[0].Name != tt.wantModelName {
				t.Errorf("model name = %q, want %q", result.Models[0].Name, tt.wantModelName)
			}

			if tt.wantModelSize != 0 && len(result.Models) > 0 && result.Models[0].SizeGB != tt.wantModelSize {
				t.Errorf("model size = %f, want %f", result.Models[0].SizeGB, tt.wantModelSize)
			}
		})
	}
}

func TestParseDiscoveryJSON_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := []byte(`[{"filename": "profile.yaml", "profile": {"model": "test", "quantized_model": "", "metadata": {"engine": "vllm", "gpu": "MI300X", "gpu_count": 1, "metric": "throughput", "precision": "fp16", "type": "optimized"}, "engine_args": {}, "env_vars": {}}, "models": []}]`)

	_, err := parseDiscoveryJSON(ctx, input)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

// ============================================================================
// CONVERT TO AIM PROFILE TESTS
// ============================================================================

func TestConvertToAIMProfile(t *testing.T) {
	tests := []struct {
		name          string
		input         discoveryProfileResult
		wantErr       bool
		wantEngine    string
		wantGPU       string
		wantGPUCount  int32
		wantMetric    aimv1alpha1.AIMMetric
		wantPrecision aimv1alpha1.AIMPrecision
		wantType      aimv1alpha1.AIMProfileType
	}{
		{
			name: "full profile",
			input: discoveryProfileResult{
				Model:          "test-model",
				QuantizedModel: "test-quantized",
				Metadata: profileMetadata{
					Engine:    "vllm",
					GPU:       "MI300X",
					GPUCount:  2,
					Metric:    "latency",
					Precision: "fp8",
					Type:      "optimized",
				},
				EngineArgs: map[string]any{
					"tensor_parallel_size": float64(2),
				},
				EnvVars: map[string]string{
					"VLLM_ATTENTION_BACKEND": "ROCM_FLASH",
				},
			},
			wantErr:       false,
			wantEngine:    "vllm",
			wantGPU:       "MI300X",
			wantGPUCount:  2,
			wantMetric:    aimv1alpha1.AIMMetric("latency"),
			wantPrecision: aimv1alpha1.AIMPrecision("fp8"),
			wantType:      aimv1alpha1.AIMProfileTypeOptimized,
		},
		{
			name: "preview profile type",
			input: discoveryProfileResult{
				Model: "test-model",
				Metadata: profileMetadata{
					Engine:    "tgi",
					GPU:       "MI325X",
					GPUCount:  4,
					Metric:    "throughput",
					Precision: "fp16",
					Type:      "preview",
				},
				EngineArgs: map[string]any{},
				EnvVars:    map[string]string{},
			},
			wantErr:  false,
			wantType: aimv1alpha1.AIMProfileTypePreview,
		},
		{
			name: "unoptimized profile type",
			input: discoveryProfileResult{
				Model: "test-model",
				Metadata: profileMetadata{
					Engine:    "vllm",
					GPU:       "MI300X",
					GPUCount:  1,
					Metric:    "throughput",
					Precision: "fp16",
					Type:      "unoptimized",
				},
				EngineArgs: map[string]any{},
				EnvVars:    map[string]string{},
			},
			wantErr:  false,
			wantType: aimv1alpha1.AIMProfileTypeUnoptimized,
		},
		{
			name: "empty engine args",
			input: discoveryProfileResult{
				Model: "test-model",
				Metadata: profileMetadata{
					Engine: "vllm",
					Type:   "optimized",
				},
				EngineArgs: nil,
				EnvVars:    nil,
			},
			wantErr:    false,
			wantEngine: "vllm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := convertToAIMProfile(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if profile == nil {
				t.Error("profile is nil")
				return
			}

			if tt.wantEngine != "" && profile.Metadata.Engine != tt.wantEngine {
				t.Errorf("engine = %q, want %q", profile.Metadata.Engine, tt.wantEngine)
			}

			if tt.wantGPU != "" && profile.Metadata.GPU != tt.wantGPU {
				t.Errorf("gpu = %q, want %q", profile.Metadata.GPU, tt.wantGPU)
			}

			if tt.wantGPUCount != 0 && profile.Metadata.GPUCount != tt.wantGPUCount {
				t.Errorf("gpu_count = %d, want %d", profile.Metadata.GPUCount, tt.wantGPUCount)
			}

			if tt.wantMetric != "" && profile.Metadata.Metric != tt.wantMetric {
				t.Errorf("metric = %q, want %q", profile.Metadata.Metric, tt.wantMetric)
			}

			if tt.wantPrecision != "" && profile.Metadata.Precision != tt.wantPrecision {
				t.Errorf("precision = %q, want %q", profile.Metadata.Precision, tt.wantPrecision)
			}

			if tt.wantType != "" && profile.Metadata.Type != tt.wantType {
				t.Errorf("type = %q, want %q", profile.Metadata.Type, tt.wantType)
			}
		})
	}
}

// ============================================================================
// CONVERT TO AIM MODEL SOURCES TESTS
// ============================================================================

func TestConvertToAIMModelSources(t *testing.T) {
	tests := []struct {
		name       string
		input      []discoveryModelResult
		wantLen    int
		wantName   string
		wantSource string
		wantSizeGB float64
	}{
		{
			name: "single model",
			input: []discoveryModelResult{
				{
					Name:   "meta-llama/Llama-3.1-8B-Instruct",
					Source: "hf://meta-llama/Llama-3.1-8B-Instruct",
					SizeGB: 16.06,
				},
			},
			wantLen:    1,
			wantName:   "meta-llama/Llama-3.1-8B-Instruct",
			wantSource: "hf://meta-llama/Llama-3.1-8B-Instruct",
			wantSizeGB: 16.06,
		},
		{
			name: "multiple models",
			input: []discoveryModelResult{
				{Name: "model1", Source: "hf://model1", SizeGB: 8.0},
				{Name: "model2", Source: "hf://model2", SizeGB: 16.0},
				{Name: "model3", Source: "hf://model3", SizeGB: 32.0},
			},
			wantLen: 3,
		},
		{
			name:    "empty models",
			input:   []discoveryModelResult{},
			wantLen: 0,
		},
		{
			name:    "nil models",
			input:   nil,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToAIMModelSources(tt.input)

			if len(result) != tt.wantLen {
				t.Errorf("result length = %d, want %d", len(result), tt.wantLen)
				return
			}

			if tt.wantLen == 0 {
				return
			}

			if tt.wantName != "" && result[0].ModelID != tt.wantName {
				t.Errorf("modelID = %q, want %q", result[0].ModelID, tt.wantName)
			}

			if tt.wantSource != "" && result[0].SourceURI != tt.wantSource {
				t.Errorf("source = %q, want %q", result[0].SourceURI, tt.wantSource)
			}

			if tt.wantSizeGB != 0 && result[0].Size != nil {
				// Convert back to GB for comparison
				sizeBytes := result[0].Size.Value()
				sizeGB := float64(sizeBytes) / (1024 * 1024 * 1024)
				// Allow small floating point differences
				if sizeGB < tt.wantSizeGB*0.99 || sizeGB > tt.wantSizeGB*1.01 {
					t.Errorf("size = %f GB, want ~%f GB", sizeGB, tt.wantSizeGB)
				}
			}
		})
	}
}

// ============================================================================
// JOB STATUS HELPER TESTS
// ============================================================================

func TestIsJobComplete(t *testing.T) {
	tests := []struct {
		name string
		job  *batchv1.Job
		want bool
	}{
		{
			name: "job succeeded",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "job failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "job in progress",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			want: false,
		},
		{
			name: "nil job",
			job:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsJobComplete(tt.job); got != tt.want {
				t.Errorf("IsJobComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsJobSucceeded(t *testing.T) {
	tests := []struct {
		name string
		job  *batchv1.Job
		want bool
	}{
		{
			name: "job succeeded",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "job failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "job in progress",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			want: false,
		},
		{
			name: "nil job",
			job:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsJobSucceeded(tt.job); got != tt.want {
				t.Errorf("IsJobSucceeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsJobFailed(t *testing.T) {
	tests := []struct {
		name string
		job  *batchv1.Job
		want bool
	}{
		{
			name: "job succeeded",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "job failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "job in progress",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			want: false,
		},
		{
			name: "nil job",
			job:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsJobFailed(tt.job); got != tt.want {
				t.Errorf("IsJobFailed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// BUILD DISCOVERY JOB TESTS
// ============================================================================

func TestBuildDiscoveryJob(t *testing.T) {
	tests := []struct {
		name         string
		spec         DiscoveryJobSpec
		wantLabels   map[string]string
		wantEnvNames []string
		wantOwnerRef bool
	}{
		{
			name: "basic job",
			spec: DiscoveryJobSpec{
				TemplateName: "my-template",
				Namespace:    "default",
				ModelID:      "test-model",
				Image:        "ghcr.io/test/image:latest",
				OwnerRef: metav1.OwnerReference{
					APIVersion: "aim.eai.amd.com/v1alpha1",
					Kind:       "AIMServiceTemplate",
					Name:       "my-template",
					UID:        "test-uid",
				},
			},
			wantLabels: map[string]string{
				"app.kubernetes.io/name":       "aim-discovery",
				"app.kubernetes.io/managed-by": "aim-controller",
			},
			wantEnvNames: []string{"AIM_LOG_LEVEL_ROOT", "AIM_LOG_LEVEL"},
			wantOwnerRef: true,
		},
		{
			name: "job with metric and precision",
			spec: DiscoveryJobSpec{
				TemplateName: "my-template",
				Namespace:    "default",
				ModelID:      "test-model",
				Image:        "ghcr.io/test/image:latest",
				TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
					ModelName: "test-model",
					AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
						Metric:    ptrTo(aimv1alpha1.AIMMetric("latency")),
						Precision: ptrTo(aimv1alpha1.AIMPrecision("fp8")),
					},
				},
			},
			wantEnvNames: []string{"AIM_LOG_LEVEL_ROOT", "AIM_LOG_LEVEL", "AIM_METRIC", "AIM_PRECISION"},
		},
		{
			name: "job with GPU selector",
			spec: DiscoveryJobSpec{
				TemplateName: "my-template",
				Namespace:    "default",
				ModelID:      "test-model",
				Image:        "ghcr.io/test/image:latest",
				TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
					ModelName: "test-model",
					AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
						Gpu: &aimv1alpha1.AIMGpuRequirements{
							Model:    "MI300X",
							Requests: 2,
						},
					},
				},
			},
			wantEnvNames: []string{"AIM_LOG_LEVEL_ROOT", "AIM_LOG_LEVEL", "AIM_GPU_MODEL", "AIM_GPU_COUNT"},
		},
		{
			name: "job with profile ID",
			spec: DiscoveryJobSpec{
				TemplateName: "my-template",
				Namespace:    "default",
				ModelID:      "test-model",
				Image:        "ghcr.io/test/image:latest",
				TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
					ModelName: "test-model",
					ProfileId: "my-profile-id",
				},
			},
			wantEnvNames: []string{"AIM_LOG_LEVEL_ROOT", "AIM_LOG_LEVEL", "AIM_PROFILE_ID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := BuildDiscoveryJob(tt.spec)

			if job == nil {
				t.Fatal("job is nil")
			}

			// Check name format
			if job.Name == "" {
				t.Error("job name is empty")
			}
			if len(job.Name) > 63 {
				t.Errorf("job name too long: %d chars", len(job.Name))
			}

			// Check namespace
			if job.Namespace != tt.spec.Namespace {
				t.Errorf("namespace = %q, want %q", job.Namespace, tt.spec.Namespace)
			}

			// Check labels
			for key, want := range tt.wantLabels {
				if got := job.Labels[key]; got != want {
					t.Errorf("label %q = %q, want %q", key, got, want)
				}
			}

			// Check owner references
			if tt.wantOwnerRef {
				if len(job.OwnerReferences) == 0 {
					t.Error("expected owner references, got none")
				} else if job.OwnerReferences[0].Name != tt.spec.OwnerRef.Name {
					t.Errorf("owner ref name = %q, want %q", job.OwnerReferences[0].Name, tt.spec.OwnerRef.Name)
				}
			}

			// Check env vars
			if len(job.Spec.Template.Spec.Containers) == 0 {
				t.Fatal("no containers in job spec")
			}
			container := job.Spec.Template.Spec.Containers[0]
			envNames := make(map[string]bool)
			for _, env := range container.Env {
				envNames[env.Name] = true
			}
			for _, wantName := range tt.wantEnvNames {
				if !envNames[wantName] {
					t.Errorf("expected env var %q not found", wantName)
				}
			}

			// Check image
			if container.Image != tt.spec.Image {
				t.Errorf("image = %q, want %q", container.Image, tt.spec.Image)
			}
		})
	}
}

func TestBuildDiscoveryJob_Deterministic(t *testing.T) {
	spec := DiscoveryJobSpec{
		TemplateName: "my-template",
		Namespace:    "default",
		ModelID:      "test-model",
		Image:        "ghcr.io/test/image:latest",
		TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "test-model",
			AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
				Metric:    ptrTo(aimv1alpha1.AIMMetric("latency")),
				Precision: ptrTo(aimv1alpha1.AIMPrecision("fp8")),
			},
		},
	}

	job1 := BuildDiscoveryJob(spec)
	job2 := BuildDiscoveryJob(spec)

	if job1.Name != job2.Name {
		t.Errorf("job names not deterministic: %q != %q", job1.Name, job2.Name)
	}
}

func TestBuildDiscoveryJob_DifferentForDifferentSpecs(t *testing.T) {
	spec1 := DiscoveryJobSpec{
		TemplateName: "my-template",
		Namespace:    "default",
		ModelID:      "test-model",
		Image:        "ghcr.io/test/image:latest",
		TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "test-model",
			AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
				Precision: ptrTo(aimv1alpha1.AIMPrecision("fp8")),
			},
		},
	}

	spec2 := DiscoveryJobSpec{
		TemplateName: "my-template",
		Namespace:    "default",
		ModelID:      "test-model",
		Image:        "ghcr.io/test/image:latest",
		TemplateSpec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "test-model",
			AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
				Precision: ptrTo(aimv1alpha1.AIMPrecision("fp16")), // Different precision
			},
		},
	}

	job1 := BuildDiscoveryJob(spec1)
	job2 := BuildDiscoveryJob(spec2)

	if job1.Name == job2.Name {
		t.Error("expected different job names for different specs")
	}
}

// Helper function for creating pointers
func ptrTo[T any](v T) *T {
	return &v
}
