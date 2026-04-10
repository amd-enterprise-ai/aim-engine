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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestModelIDFromSourceURI(t *testing.T) {
	tests := []struct {
		name      string
		sourceUri string
		want      string
	}{
		{
			name:      "hf source",
			sourceUri: "hf://HuggingFaceTB/SmolLM2-135M",
			want:      "HuggingFaceTB/SmolLM2-135M",
		},
		{
			name:      "s3 source",
			sourceUri: "s3://bucket/path/to/model",
			want:      "bucket/path/to/model",
		},
		{
			name:      "no scheme",
			sourceUri: "some/model",
			want:      "some/model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ModelIDFromSourceURI(tt.sourceUri)
			if got != tt.want {
				t.Errorf("ModelIDFromSourceURI(%q) = %q, want %q", tt.sourceUri, got, tt.want)
			}
		})
	}
}

func TestComputeCacheKey(t *testing.T) {
	tests := []struct {
		name   string
		filter *aimv1alpha1.AIMDownloadFilter
	}{
		{
			name:   "nil filter",
			filter: nil,
		},
		{
			name:   "empty filter",
			filter: &aimv1alpha1.AIMDownloadFilter{},
		},
		{
			name:   "default exclude",
			filter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
		},
		{
			name: "include and exclude",
			filter: &aimv1alpha1.AIMDownloadFilter{
				Include: []string{"*.safetensors", "config.json"},
				Exclude: []string{"*/*"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := ComputeCacheKey(tt.filter)
			if len(key) != 16 {
				t.Errorf("ComputeCacheKey() returned key of length %d, want 16", len(key))
			}
		})
	}
}

func TestComputeCacheKeyDeterministic(t *testing.T) {
	filter := &aimv1alpha1.AIMDownloadFilter{
		Include: []string{"*.safetensors", "config.json"},
		Exclude: []string{"*/*"},
	}
	key1 := ComputeCacheKey(filter)
	key2 := ComputeCacheKey(filter)
	if key1 != key2 {
		t.Errorf("ComputeCacheKey is not deterministic: %q != %q", key1, key2)
	}
}

func TestComputeCacheKeySortOrder(t *testing.T) {
	filter1 := &aimv1alpha1.AIMDownloadFilter{
		Include: []string{"config.json", "*.safetensors"},
	}
	filter2 := &aimv1alpha1.AIMDownloadFilter{
		Include: []string{"*.safetensors", "config.json"},
	}
	if ComputeCacheKey(filter1) != ComputeCacheKey(filter2) {
		t.Error("ComputeCacheKey should produce same key regardless of pattern order")
	}
}

func TestComputeCacheKeyDiffers(t *testing.T) {
	noFilter := ComputeCacheKey(nil)
	withExclude := ComputeCacheKey(&aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}})
	withInclude := ComputeCacheKey(&aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}})

	if noFilter == withExclude {
		t.Error("nil filter and exclude filter should produce different keys")
	}
	if noFilter == withInclude {
		t.Error("nil filter and include filter should produce different keys")
	}
	if withExclude == withInclude {
		t.Error("exclude and include filters should produce different keys")
	}
}

func TestCacheS3Path(t *testing.T) {
	got := CacheS3Path("s3://aim-cache/artifacts", "HuggingFaceTB/SmolLM2-135M", "abc123")
	want := "s3://aim-cache/artifacts/HuggingFaceTB/SmolLM2-135M/abc123"
	if got != want {
		t.Errorf("CacheS3Path() = %q, want %q", got, want)
	}
}

func TestCacheS3PathTrailingSlash(t *testing.T) {
	got := CacheS3Path("s3://aim-cache/artifacts/", "org/model", "key123")
	want := "s3://aim-cache/artifacts/org/model/key123"
	if got != want {
		t.Errorf("CacheS3Path() with trailing slash = %q, want %q", got, want)
	}
}

func TestManifestS3Path(t *testing.T) {
	got := ManifestS3Path("s3://aim-cache/artifacts", "org/model", "abc123")
	want := "s3://aim-cache/artifacts/org/model/abc123/manifest.json"
	if got != want {
		t.Errorf("ManifestS3Path() = %q, want %q", got, want)
	}
}

func TestEffectiveSourceURI(t *testing.T) {
	tests := []struct {
		name       string
		artifact   *aimv1alpha1.AIMArtifact
		wantSource string
	}{
		{
			name: "no resolved URI uses spec",
			artifact: &aimv1alpha1.AIMArtifact{
				Spec:   aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
				Status: aimv1alpha1.AIMArtifactStatus{},
			},
			wantSource: "hf://org/model",
		},
		{
			name: "resolved URI overrides spec",
			artifact: &aimv1alpha1.AIMArtifact{
				Spec: aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
				Status: aimv1alpha1.AIMArtifactStatus{
					ResolvedSourceURI: "s3://aim-cache/artifacts/org/model/abc123",
				},
			},
			wantSource: "s3://aim-cache/artifacts/org/model/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveSourceURI(tt.artifact)
			if got != tt.wantSource {
				t.Errorf("effectiveSourceURI() = %q, want %q", got, tt.wantSource)
			}
		})
	}
}

func TestBuildDownloadJobUsesCacheEnv(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
		Status: aimv1alpha1.AIMArtifactStatus{
			ResolvedSourceURI: "s3://aim-cache/artifacts/org/model/abc123",
		},
	}

	cacheEnv := []corev1.EnvVar{
		{Name: "AWS_ENDPOINT_URL", Value: "http://seaweedfs-s3.seaweedfs-system:8333"},
		{Name: "S3_NO_SSL", Value: "true"},
	}

	job := buildDownloadJob(mc, nil, 1000, cacheEnv...)
	container := job.Spec.Template.Spec.Containers[0]

	// Verify source URI is the resolved one
	if container.Args[0] != "s3://aim-cache/artifacts/org/model/abc123" {
		t.Errorf("download job arg = %q, want resolved s3:// URI", container.Args[0])
	}

	// Verify cache env vars are present
	envMap := envToMap(container.Env)
	if envMap["AWS_ENDPOINT_URL"] != "http://seaweedfs-s3.seaweedfs-system:8333" {
		t.Errorf("AWS_ENDPOINT_URL = %q, want seaweedfs endpoint", envMap["AWS_ENDPOINT_URL"])
	}
	if envMap["S3_NO_SSL"] != "true" {
		t.Errorf("S3_NO_SSL = %q, want true", envMap["S3_NO_SSL"])
	}
}

func TestBuildCheckSizeJobUsesCacheEnv(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
		Status: aimv1alpha1.AIMArtifactStatus{
			ResolvedSourceURI: "s3://aim-cache/artifacts/org/model/abc123",
		},
	}

	cacheEnv := []corev1.EnvVar{
		{Name: "AWS_ENDPOINT_URL", Value: "http://seaweedfs-s3.seaweedfs-system:8333"},
	}

	job := buildCheckSizeJob(mc, nil, cacheEnv...)
	container := job.Spec.Template.Spec.Containers[0]

	if container.Args[0] != "s3://aim-cache/artifacts/org/model/abc123" {
		t.Errorf("check-size job arg = %q, want resolved s3:// URI", container.Args[0])
	}

	envMap := envToMap(container.Env)
	if envMap["AWS_ENDPOINT_URL"] != "http://seaweedfs-s3.seaweedfs-system:8333" {
		t.Errorf("AWS_ENDPOINT_URL = %q, want seaweedfs endpoint", envMap["AWS_ENDPOINT_URL"])
	}
}

func TestS3PathToHTTPURL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		s3URI    string
		want     string
	}{
		{
			name:     "basic",
			endpoint: "http://seaweedfs:8333",
			s3URI:    "s3://aim-cache/artifacts/org/model/abc/manifest.json",
			want:     "http://seaweedfs:8333/aim-cache/artifacts/org/model/abc/manifest.json",
		},
		{
			name:     "trailing slash on endpoint",
			endpoint: "http://minio:9000/",
			s3URI:    "s3://bucket/key",
			want:     "http://minio:9000/bucket/key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s3PathToHTTPURL(tt.endpoint, tt.s3URI)
			if got != tt.want {
				t.Errorf("s3PathToHTTPURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnvValue(t *testing.T) {
	envs := []corev1.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "BAZ", Value: "qux"},
	}
	if got := envValue(envs, "FOO"); got != "bar" {
		t.Errorf("envValue(FOO) = %q, want bar", got)
	}
	if got := envValue(envs, "MISSING"); got != "" {
		t.Errorf("envValue(MISSING) = %q, want empty", got)
	}
	if got := envValue(nil, "FOO"); got != "" {
		t.Errorf("envValue(nil, FOO) = %q, want empty", got)
	}
}

func TestCheckCacheHit(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantHit    bool
		wantErr    bool
		config     *aimv1alpha1.ArtifactCacheConfig
		sourceURI  string
		filter     *aimv1alpha1.AIMDownloadFilter
		verifyPath string
	}{
		{
			name:    "nil config returns empty",
			config:  nil,
			wantHit: false,
		},
		{
			name:    "disabled config returns empty",
			config:  &aimv1alpha1.ArtifactCacheConfig{Enabled: false},
			wantHit: false,
		},
		{
			name:    "missing S3URI returns empty",
			config:  &aimv1alpha1.ArtifactCacheConfig{Enabled: true, S3URI: ""},
			wantHit: false,
		},
		{
			name:      "200 means cache hit",
			status:    http.StatusOK,
			wantHit:   true,
			sourceURI: "hf://org/model",
			filter:    &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
		},
		{
			name:      "404 means cache miss",
			status:    http.StatusNotFound,
			wantHit:   false,
			sourceURI: "hf://org/model",
			filter:    &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
		},
		{
			name:      "500 means cache miss",
			status:    http.StatusInternalServerError,
			wantHit:   false,
			sourceURI: "hf://org/model",
			filter:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			if config == nil && tt.status != 0 {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodHead {
						t.Errorf("expected HEAD, got %s", r.Method)
					}
					w.WriteHeader(tt.status)
				}))
				defer srv.Close()

				config = &aimv1alpha1.ArtifactCacheConfig{
					Enabled: true,
					S3URI:   "s3://aim-cache/artifacts",
					Env: []corev1.EnvVar{
						{Name: "AWS_ENDPOINT_URL", Value: srv.URL},
					},
				}
			}

			got, err := CheckCacheHit(context.Background(), config, tt.sourceURI, tt.filter)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckCacheHit() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantHit && got == "" {
				t.Error("CheckCacheHit() returned empty, want cache hit path")
			}
			if !tt.wantHit && got != "" {
				t.Errorf("CheckCacheHit() = %q, want empty (cache miss)", got)
			}
		})
	}
}

func TestCheckCacheHitNoEndpoint(t *testing.T) {
	config := &aimv1alpha1.ArtifactCacheConfig{
		Enabled: true,
		S3URI:   "s3://aim-cache/artifacts",
		Env:     []corev1.EnvVar{},
	}
	_, err := CheckCacheHit(context.Background(), config, "hf://org/model", nil)
	if err == nil {
		t.Error("CheckCacheHit() expected error when AWS_ENDPOINT_URL is missing")
	}
}

func TestResolveCacheEnv(t *testing.T) {
	cacheEnvVars := []corev1.EnvVar{
		{Name: "AWS_ENDPOINT_URL", Value: "http://seaweedfs:8333"},
	}

	tests := []struct {
		name          string
		resolvedURI   string
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		wantNil       bool
	}{
		{
			name:        "no resolved URI",
			resolvedURI: "",
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				ArtifactCache: &aimv1alpha1.ArtifactCacheConfig{Env: cacheEnvVars},
			},
			wantNil: true,
		},
		{
			name:          "nil runtime config",
			resolvedURI:   "s3://cache/model",
			runtimeConfig: nil,
			wantNil:       true,
		},
		{
			name:        "nil artifact cache",
			resolvedURI: "s3://cache/model",
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				ArtifactCache: nil,
			},
			wantNil: true,
		},
		{
			name:        "returns cache env",
			resolvedURI: "s3://cache/model",
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				ArtifactCache: &aimv1alpha1.ArtifactCacheConfig{Env: cacheEnvVars},
			},
			wantNil: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &aimv1alpha1.AIMArtifact{
				Status: aimv1alpha1.AIMArtifactStatus{
					ResolvedSourceURI: tt.resolvedURI,
				},
			}
			got := resolveCacheEnv(mc, tt.runtimeConfig)
			if tt.wantNil && got != nil {
				t.Errorf("resolveCacheEnv() = %v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Error("resolveCacheEnv() = nil, want env vars")
			}
		})
	}
}
