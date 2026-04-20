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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestResolveDownloadFilter(t *testing.T) {
	tests := []struct {
		name          string
		artifact      *aimv1alpha1.AIMArtifact
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		wantInclude   []string
		wantExclude   []string
		wantIsDefault bool
	}{
		{
			name:          "no filter anywhere → default excludes subdirs",
			artifact:      &aimv1alpha1.AIMArtifact{},
			runtimeConfig: nil,
			wantExclude:   []string{"*/*"},
			wantIsDefault: true,
		},
		{
			name:          "runtime config with empty storage → default",
			artifact:      &aimv1alpha1.AIMArtifact{},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{Storage: &aimv1alpha1.AIMStorageConfig{}}},
			wantExclude:   []string{"*/*"},
			wantIsDefault: true,
		},
		{
			name:     "runtime config sets filter → used",
			artifact: &aimv1alpha1.AIMArtifact{},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
					},
				},
			},
			wantInclude: []string{"*.safetensors"},
		},
		{
			name: "artifact filter overrides runtime config",
			artifact: &aimv1alpha1.AIMArtifact{
				Spec: aimv1alpha1.AIMArtifactSpec{
					DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin"}},
				},
			},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
					},
				},
			},
			wantExclude: []string{"*.bin"},
		},
		{
			name: "explicit empty filter on artifact → no filtering (overrides default)",
			artifact: &aimv1alpha1.AIMArtifact{
				Spec: aimv1alpha1.AIMArtifactSpec{
					DownloadFilter: &aimv1alpha1.AIMDownloadFilter{},
				},
			},
			runtimeConfig: nil,
		},
		{
			name:     "explicit empty filter on runtime config → no filtering",
			artifact: &aimv1alpha1.AIMArtifact{},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						DownloadFilter: &aimv1alpha1.AIMDownloadFilter{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDownloadFilter(tt.artifact, tt.runtimeConfig)

			if got == nil {
				t.Fatal("resolveDownloadFilter returned nil, should always return a filter")
			}

			if tt.wantIsDefault && got != defaultDownloadFilter {
				t.Error("expected default filter instance")
			}

			assertSliceEqual(t, "include", got.Include, tt.wantInclude)
			assertSliceEqual(t, "exclude", got.Exclude, tt.wantExclude)
		})
	}
}

func TestDownloadFilterEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		filter   *aimv1alpha1.AIMDownloadFilter
		wantEnvs map[string]string
	}{
		{
			name:     "nil filter → no env vars",
			filter:   nil,
			wantEnvs: map[string]string{},
		},
		{
			name:     "empty filter → no env vars",
			filter:   &aimv1alpha1.AIMDownloadFilter{},
			wantEnvs: map[string]string{},
		},
		{
			name:     "exclude only",
			filter:   &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
			wantEnvs: map[string]string{"AIM_HF_EXCLUDE": "*/*"},
		},
		{
			name:     "include only",
			filter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors", "config.json"}},
			wantEnvs: map[string]string{"AIM_HF_INCLUDE": "*.safetensors,config.json"},
		},
		{
			name: "both include and exclude",
			filter: &aimv1alpha1.AIMDownloadFilter{
				Include: []string{"*.safetensors"},
				Exclude: []string{"*/*"},
			},
			wantEnvs: map[string]string{
				"AIM_HF_INCLUDE": "*.safetensors",
				"AIM_HF_EXCLUDE": "*/*",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envs := downloadFilterEnvVars(tt.filter)
			gotMap := make(map[string]string)
			for _, e := range envs {
				gotMap[e.Name] = e.Value
			}

			for k, want := range tt.wantEnvs {
				if got, ok := gotMap[k]; !ok {
					t.Errorf("missing env var %s", k)
				} else if got != want {
					t.Errorf("env %s = %q, want %q", k, got, want)
				}
			}

			if len(gotMap) != len(tt.wantEnvs) {
				t.Errorf("got %d env vars, want %d", len(gotMap), len(tt.wantEnvs))
			}
		})
	}
}

func TestBuildCheckSizeJobIncludesFilter(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI:      "hf://org/model",
			DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*", "*.md"}},
		},
	}

	job := buildCheckSizeJob(mc, nil)
	envMap := envToMap(job.Spec.Template.Spec.Containers[0].Env)

	if val, ok := envMap["AIM_HF_EXCLUDE"]; !ok {
		t.Error("check-size job missing AIM_HF_EXCLUDE")
	} else if val != "*/*,*.md" {
		t.Errorf("AIM_HF_EXCLUDE = %q, want %q", val, "*/*,*.md")
	}
}

func TestBuildDownloadJobIncludesFilter(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI:      "hf://org/model",
			DownloadFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*", "*.md"}},
		},
	}

	job := buildDownloadJob(mc, nil, 1000)
	envMap := envToMap(job.Spec.Template.Spec.Containers[0].Env)

	if val, ok := envMap["AIM_HF_EXCLUDE"]; !ok {
		t.Error("download job missing AIM_HF_EXCLUDE")
	} else if val != "*/*,*.md" {
		t.Errorf("AIM_HF_EXCLUDE = %q, want %q", val, "*/*,*.md")
	}
}

func TestBothJobsGetSameFilterEnvVars(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
	}

	checkJob := buildCheckSizeJob(mc, nil)
	downloadJob := buildDownloadJob(mc, nil, 1000)

	checkEnv := envToMap(checkJob.Spec.Template.Spec.Containers[0].Env)
	downloadEnv := envToMap(downloadJob.Spec.Template.Spec.Containers[0].Env)

	for _, key := range []string{"AIM_HF_INCLUDE", "AIM_HF_EXCLUDE"} {
		if checkEnv[key] != downloadEnv[key] {
			t.Errorf("env %s differs: check-size=%q download=%q", key, checkEnv[key], downloadEnv[key])
		}
	}
}

func TestResolveDownloadImage(t *testing.T) {
	const (
		artifactImage = "ghcr.io/example/downloader:artifact"
		runtimeImage  = "ghcr.io/example/downloader:runtime"
	)

	tests := []struct {
		name          string
		artifact      *aimv1alpha1.AIMArtifact
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		want          string
	}{
		{
			name:     "falls back to build-time default when nothing set",
			artifact: &aimv1alpha1.AIMArtifact{},
			want:     aimv1alpha1.DefaultDownloadImage,
		},
		{
			name:     "uses runtime config when artifact does not override",
			artifact: &aimv1alpha1.AIMArtifact{},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				Artifact: &aimv1alpha1.AIMArtifactConfig{ModelDownloadImage: runtimeImage},
			},
			want: runtimeImage,
		},
		{
			name: "artifact spec overrides runtime config",
			artifact: &aimv1alpha1.AIMArtifact{
				Spec: aimv1alpha1.AIMArtifactSpec{ModelDownloadImage: artifactImage},
			},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				Artifact: &aimv1alpha1.AIMArtifactConfig{ModelDownloadImage: runtimeImage},
			},
			want: artifactImage,
		},
		{
			name:     "nil Artifact section falls back to default",
			artifact: &aimv1alpha1.AIMArtifact{},
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				Artifact: nil,
			},
			want: aimv1alpha1.DefaultDownloadImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDownloadImage(tt.artifact, tt.runtimeConfig); got != tt.want {
				t.Errorf("resolveDownloadImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func envToMap(envs []corev1.EnvVar) map[string]string {
	m := make(map[string]string)
	for _, e := range envs {
		m[e.Name] = e.Value
	}
	return m
}

func assertSliceEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", name, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s[%d]: got %q, want %q", name, i, got[i], want[i])
		}
	}
}
