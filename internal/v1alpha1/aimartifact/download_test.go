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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestResolveDownloadFilter(t *testing.T) {
	// hfArtifact returns an empty artifact with an hf:// source so the implicit
	// subdir-exclude default applies (the default is hf://-only).
	hfArtifact := func() *aimv1alpha1.AIMArtifact {
		return &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"}}
	}
	s3Artifact := func() *aimv1alpha1.AIMArtifact {
		return &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{SourceURI: "s3://bucket/model"}}
	}

	tests := []struct {
		name          string
		artifact      *aimv1alpha1.AIMArtifact
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		wantInclude   []string
		wantExclude   []string
		wantIsDefault bool
		wantNil       bool
	}{
		{
			name:          "hf source, no filter anywhere → default excludes subdirs",
			artifact:      hfArtifact(),
			runtimeConfig: nil,
			wantExclude:   []string{"*/*"},
			wantIsDefault: true,
		},
		{
			name:          "hf source, runtime config with empty storage → default",
			artifact:      hfArtifact(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{Storage: &aimv1alpha1.AIMStorageConfig{}}},
			wantExclude:   []string{"*/*"},
			wantIsDefault: true,
		},
		{
			name:          "s3 source, no filter anywhere → nil (download whole prefix)",
			artifact:      s3Artifact(),
			runtimeConfig: nil,
			wantNil:       true,
		},
		{
			name:          "s3 source, runtime config with empty storage → nil",
			artifact:      s3Artifact(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{Storage: &aimv1alpha1.AIMStorageConfig{}}},
			wantNil:       true,
		},
		{
			name:     "runtime config sets filter → used (applies to any scheme)",
			artifact: s3Artifact(),
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
					SourceURI:      "hf://org/model",
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
					SourceURI:      "hf://org/model",
					DownloadFilter: &aimv1alpha1.AIMDownloadFilter{},
				},
			},
			runtimeConfig: nil,
		},
		{
			name:     "explicit empty filter on runtime config → no filtering",
			artifact: hfArtifact(),
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

			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil filter, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("resolveDownloadFilter returned nil unexpectedly")
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

	job := buildCheckSizeJob(mc, nil, false)
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

	job := buildDownloadJob(mc, nil, 1000, false)
	envMap := envToMap(job.Spec.Template.Spec.Containers[0].Env)

	if val, ok := envMap["AIM_HF_EXCLUDE"]; !ok {
		t.Error("download job missing AIM_HF_EXCLUDE")
	} else if val != "*/*,*.md" {
		t.Errorf("AIM_HF_EXCLUDE = %q, want %q", val, "*/*,*.md")
	}
}

func TestBuildDownloadJobHasActiveDeadline(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model"},
	}

	job := buildDownloadJob(mc, nil, 1000, false)

	if job.Spec.ActiveDeadlineSeconds == nil {
		t.Fatal("download job missing ActiveDeadlineSeconds (hard backstop for a wedged filesystem)")
	}
	if got, want := *job.Spec.ActiveDeadlineSeconds, int64(24*60*60); got != want {
		t.Errorf("ActiveDeadlineSeconds = %d, want %d (24h)", got, want)
	}
}

func TestBothJobsGetSameFilterEnvVars(t *testing.T) {
	tests := []struct {
		name   string
		filter *aimv1alpha1.AIMDownloadFilter
	}{
		{name: "default filter", filter: nil},
		{name: "declared filter", filter: &aimv1alpha1.AIMDownloadFilter{
			Include: []string{"*.safetensors", "tokenizer/*"},
			Exclude: []string{"*.bin"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &aimv1alpha1.AIMArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/model", DownloadFilter: tt.filter},
			}

			checkJob := buildCheckSizeJob(mc, nil, false)
			downloadJob := buildDownloadJob(mc, nil, 1000, false)

			checkEnv := envToMap(checkJob.Spec.Template.Spec.Containers[0].Env)
			downloadEnv := envToMap(downloadJob.Spec.Template.Spec.Containers[0].Env)

			for _, key := range []string{"AIM_HF_INCLUDE", "AIM_HF_EXCLUDE"} {
				if checkEnv[key] != downloadEnv[key] {
					t.Errorf("env %s differs: check-size=%q download=%q", key, checkEnv[key], downloadEnv[key])
				}
			}
			if tt.filter != nil && downloadEnv["AIM_HF_INCLUDE"] == "" {
				t.Error("declared include patterns did not reach the jobs")
			}
		})
	}
}

func TestBothS3JobsUseTypedConnectionAndTrustedImage(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI:          "s3://bucket/model",
			ModelDownloadImage: "example.invalid/custom-downloader:latest",
			Env: []corev1.EnvVar{
				{Name: "AWS_ENDPOINT_URL", Value: "https://artifact.example"},
				{Name: "AIM_S3_INSECURE_SKIP_VERIFY", Value: "true"},
			},
		},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "ca.crt",
		},
		Artifact: &aimv1alpha1.AIMArtifactConfig{
			S3: &aimv1alpha1.S3ConnectionConfig{
				Endpoint: "https://s3.internal.example",
				Auth: &aimv1alpha1.S3AuthConfig{
					Mode: aimv1alpha1.S3AuthModeStatic,
					CredentialsSecretRef: &aimv1alpha1.S3CredentialsSecretReference{
						Name: "s3-credentials",
					},
				},
				TLS: &aimv1alpha1.S3TLSConfig{
					CABundleRef: &aimv1alpha1.S3CABundleReference{
						Kind: "ConfigMap",
						Name: "s3-ca",
						Key:  "ca.pem",
					},
				},
			},
		},
	}

	jobs := []*batchv1.Job{
		buildCheckSizeJob(mc, config, true),
		buildDownloadJob(mc, config, 1000, true),
	}
	for _, job := range jobs {
		container := job.Spec.Template.Spec.Containers[0]
		env := envToMap(container.Env)
		if container.Image != aimv1alpha1.DefaultDownloadImage {
			t.Errorf(
				"%s image = %q, want %q",
				job.Name,
				container.Image,
				aimv1alpha1.DefaultDownloadImage,
			)
		}
		if env["AWS_ENDPOINT_URL"] != "https://s3.internal.example" {
			t.Errorf("%s endpoint = %q, want typed endpoint", job.Name, env["AWS_ENDPOINT_URL"])
		}
		if env["AIM_S3_INSECURE_SKIP_VERIFY"] != "false" {
			t.Errorf(
				"%s insecure flag = %q, want typed secure value",
				job.Name,
				env["AIM_S3_INSECURE_SKIP_VERIFY"],
			)
		}
		if env["AWS_ACCESS_KEY_ID_FILE"] == "" ||
			env["AWS_SECRET_ACCESS_KEY_FILE"] == "" {
			t.Errorf("%s does not use mounted static credentials", job.Name)
		}
		if env["AWS_CA_BUNDLE"] != "/etc/aim/trust/ca-bundle.crt" {
			t.Errorf("%s CA path = %q", job.Name, env["AWS_CA_BUNDLE"])
		}

		var sawCredentials, sawCA, sawAdditionalCA bool
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.Secret != nil && volume.Secret.SecretName == "s3-credentials" {
				sawCredentials = true
			}
			if volume.ConfigMap != nil && volume.ConfigMap.Name == "s3-ca" {
				sawCA = true
			}
			if volume.ConfigMap != nil && volume.ConfigMap.Name == "company-ca" {
				sawAdditionalCA = true
			}
		}
		if !sawCredentials || !sawCA || !sawAdditionalCA {
			t.Errorf(
				"%s volumes credentials=%v s3CA=%v additionalCA=%v, want all",
				job.Name,
				sawCredentials,
				sawCA,
				sawAdditionalCA,
			)
		}
		if len(job.Spec.Template.Spec.InitContainers) != 1 ||
			len(job.Spec.Template.Spec.InitContainers[0].Args) != 3 {
			t.Errorf("%s combined trust init = %#v", job.Name, job.Spec.Template.Spec.InitContainers)
		}
		if got := job.Spec.Template.Spec.InitContainers[0].Env; len(got) != 0 {
			t.Errorf("%s trust init env = %#v, want no rollout metadata", job.Name, got)
		}
	}
}

func TestTrustInitUsesResolvedDownloadImage(t *testing.T) {
	const image = "registry.example.test/downloader:v2"
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI:          "hf://org/model",
			ModelDownloadImage: image,
		},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "ca.crt",
		},
	}

	jobs := []*batchv1.Job{
		buildCheckSizeJob(mc, config, true),
		buildDownloadJob(mc, config, 1000, true),
	}
	for _, job := range jobs {
		if got := job.Spec.Template.Spec.Containers[0].Image; got != image {
			t.Errorf("%s workload image = %q, want %q", job.Name, got, image)
		}
		if len(job.Spec.Template.Spec.InitContainers) != 1 {
			t.Fatalf("%s init containers = %d, want 1", job.Name, len(job.Spec.Template.Spec.InitContainers))
		}
		if got := job.Spec.Template.Spec.InitContainers[0].Image; got != image {
			t.Errorf("%s trust init image = %q, want %q", job.Name, got, image)
		}
	}
}

func TestDownloadJobsSkipUnavailableAdditionalCABundle(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI: "hf://org/model",
		},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "missing-ca",
			Key:  "ca.crt",
		},
	}

	for _, job := range []*batchv1.Job{
		buildCheckSizeJob(mc, config, false),
		buildDownloadJob(mc, config, 1000, false),
	} {
		if len(job.Spec.Template.Spec.InitContainers) != 0 {
			t.Errorf("%s init containers = %#v, want no trust projection", job.Name, job.Spec.Template.Spec.InitContainers)
		}
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.ConfigMap != nil && volume.ConfigMap.Name == "missing-ca" {
				t.Errorf("%s projected unavailable ConfigMap", job.Name)
			}
		}
	}
}

func TestPullPolicyForImage(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  corev1.PullPolicy
	}{
		{name: "empty defaults to always", image: "", want: corev1.PullAlways},
		{name: "tagless reference defaults to always", image: "ghcr.io/example/downloader", want: corev1.PullAlways},
		{name: "explicit :latest is always", image: "ghcr.io/example/downloader:latest", want: corev1.PullAlways},
		{name: "semver tag is if-not-present", image: "ghcr.io/example/downloader:v0.2.2", want: corev1.PullIfNotPresent},
		{name: "non-latest moving tag is if-not-present", image: "ghcr.io/example/downloader:main", want: corev1.PullIfNotPresent},
		{name: "digest reference is if-not-present", image: "ghcr.io/example/downloader@sha256:c0ffee", want: corev1.PullIfNotPresent},
		{name: "port-qualified registry with semver", image: "registry.local:5000/example/downloader:v1.2.3", want: corev1.PullIfNotPresent},
		{name: "port-qualified registry without tag is always", image: "registry.local:5000/example/downloader", want: corev1.PullAlways},
		{name: "port-qualified registry with :latest is always", image: "registry.local:5000/example/downloader:latest", want: corev1.PullAlways},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pullPolicyForImage(tt.image); got != tt.want {
				t.Errorf("pullPolicyForImage(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
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
