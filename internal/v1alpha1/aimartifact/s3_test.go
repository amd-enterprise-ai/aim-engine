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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestResolveS3PodSettingsPreservesLegacyPrecedenceWithoutTypedConnection(t *testing.T) {
	settings := ResolveS3PodSettings(
		nil,
		[]corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://default.example"}},
		[]corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://runtime.example"}},
		[]corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://artifact.example"}},
	)

	if got := envValueForTest(settings.Env, "AWS_ENDPOINT_URL"); got != "https://artifact.example" {
		t.Fatalf("AWS_ENDPOINT_URL = %q, want artifact env value", got)
	}
	if len(settings.Volumes) != 0 || len(settings.VolumeMounts) != 0 {
		t.Fatal("legacy env-only configuration must not add volumes")
	}
}

func TestResolveS3PodSettingsAppliesTypedStaticConnection(t *testing.T) {
	connection := &aimv1alpha1.S3ConnectionConfig{
		Endpoint:         "https://s3.internal.example",
		Region:           "eu-west-1",
		AddressingStyle:  aimv1alpha1.S3AddressingStyleVirtual,
		SignatureVersion: aimv1alpha1.S3SignatureVersionV4,
		Auth: &aimv1alpha1.S3AuthConfig{
			Mode: aimv1alpha1.S3AuthModeStatic,
			CredentialsSecretRef: &aimv1alpha1.S3CredentialsSecretReference{
				Name:            "s3-credentials",
				SessionTokenKey: "session-token",
			},
		},
		TLS: &aimv1alpha1.S3TLSConfig{
			CABundleRef: &aimv1alpha1.S3CABundleReference{
				Kind: "ConfigMap",
				Name: "s3-ca",
				Key:  "ca.pem",
			},
		},
	}
	settings := ResolveS3PodSettings(
		connection,
		nil,
		[]corev1.EnvVar{
			{Name: "AWS_ENDPOINT_URL", Value: "https://legacy.example"},
			{Name: "AWS_ACCESS_KEY_ID", Value: "legacy-access"},
			{Name: "AIM_S3_INSECURE_SKIP_VERIFY", Value: "true"},
		},
		[]corev1.EnvVar{
			{Name: "AWS_ENDPOINT_URL_S3", Value: "https://artifact.example"},
			{Name: "AWS_SECRET_ACCESS_KEY", Value: "artifact-secret"},
		},
	)

	assertEnvValue(t, settings.Env, "AWS_ENDPOINT_URL", connection.Endpoint)
	assertEnvValue(t, settings.Env, "AWS_REGION", connection.Region)
	assertEnvValue(t, settings.Env, "AWS_DEFAULT_REGION", connection.Region)
	assertEnvValue(t, settings.Env, "AIM_S3_ADDRESSING_STYLE", "virtual")
	assertEnvValue(t, settings.Env, "AIM_S3_SIGNATURE_VERSION", "s3v4")
	assertEnvValue(t, settings.Env, "AIM_S3_AUTH_MODE", "static")
	assertEnvValue(t, settings.Env, "AWS_ACCESS_KEY_ID", "")
	assertEnvValue(t, settings.Env, "AWS_SECRET_ACCESS_KEY", "")
	assertEnvValue(t, settings.Env, "AWS_ACCESS_KEY_ID_FILE", s3CredentialsMountPath+"/access-key-id")
	assertEnvValue(t, settings.Env, "AWS_SECRET_ACCESS_KEY_FILE", s3CredentialsMountPath+"/secret-access-key")
	assertEnvValue(t, settings.Env, "AWS_SESSION_TOKEN_FILE", s3CredentialsMountPath+"/session-token")
	assertEnvValue(t, settings.Env, "AWS_ENDPOINT_URL_S3", "")
	assertEnvValue(t, settings.Env, "AIM_S3_INSECURE_SKIP_VERIFY", "false")
	assertEnvValue(t, settings.Env, "AWS_CA_BUNDLE", "")

	if len(settings.Volumes) != 1 || len(settings.VolumeMounts) != 1 {
		t.Fatalf(
			"volumes/mounts = %d/%d, want 1/1",
			len(settings.Volumes),
			len(settings.VolumeMounts),
		)
	}
	if settings.Volumes[0].Secret == nil ||
		settings.Volumes[0].Secret.SecretName != "s3-credentials" {
		t.Fatalf("credential volume = %#v", settings.Volumes[0])
	}
}

func TestResolveS3PodSettingsSupportsInsecureDiagnosticMode(t *testing.T) {
	settings := ResolveS3PodSettings(
		&aimv1alpha1.S3ConnectionConfig{
			TLS: &aimv1alpha1.S3TLSConfig{InsecureSkipVerify: true},
		},
		nil,
		nil,
		nil,
	)

	assertEnvValue(t, settings.Env, "AIM_S3_INSECURE_SKIP_VERIFY", "true")
	assertEnvValue(t, settings.Env, "AWS_CA_BUNDLE", "")
	if len(settings.Volumes) != 0 {
		t.Fatalf("insecure mode added unexpected volumes: %#v", settings.Volumes)
	}
}

func TestTypedS3ArtifactEnvironmentIsSourceAware(t *testing.T) {
	settings := ResolveS3PodSettings(
		&aimv1alpha1.S3ConnectionConfig{
			Endpoint: "https://s3.internal.example",
		},
		nil,
		[]corev1.EnvVar{
			{Name: "HTTPS_PROXY", Value: "http://admin-proxy:8080"},
			{Name: "AWS_PROFILE", Value: "admin-selected-profile"},
		},
		[]corev1.EnvVar{
			{Name: "HTTPS_PROXY", Value: "http://artifact-proxy:8080"},
			{Name: "AWS_PROFILE", Value: "artifact-profile"},
			{Name: "PYTHONPATH", Value: "/artifact/code"},
			{Name: "AIM_S3_LOG_LEVEL", Value: "DEBUG"},
			{Name: "AIM_S3_MAX_WORKERS", Value: "4"},
		},
	)

	for name, want := range map[string]string{
		"HTTPS_PROXY":        "http://admin-proxy:8080",
		"AWS_PROFILE":        "admin-selected-profile",
		"AIM_S3_LOG_LEVEL":   "DEBUG",
		"AIM_S3_MAX_WORKERS": "4",
		"AWS_ENDPOINT_URL":   "https://s3.internal.example",
	} {
		item := findEnvVarForTest(settings.Env, name)
		if item == nil || item.Value != want {
			t.Errorf("%s = %#v, want %q", name, item, want)
		}
	}
	if item := findEnvVarForTest(settings.Env, "PYTHONPATH"); item != nil {
		t.Errorf("artifact PYTHONPATH survived typed S3 filtering: %#v", item)
	}
}

func TestValidateTypedS3ArtifactEnvironment(t *testing.T) {
	allowed := []corev1.EnvVar{
		{Name: "AIM_S3_LOG_LEVEL", Value: "DEBUG"},
		{Name: "AIM_S3_MAX_WORKERS", Value: "64"},
		{Name: "AIM_S3_MAX_CONCURRENCY", Value: "1"},
		{Name: "AIM_S3_MULTIPART_CHUNKSIZE_MB", Value: "5"},
	}
	if err := ValidateTypedS3ArtifactEnv(allowed); err != nil {
		t.Fatalf("allowed typed S3 artifact env rejected: %v", err)
	}

	tests := []struct {
		name string
		env  corev1.EnvVar
		want string
	}{
		{
			name: "proxy",
			env:  corev1.EnvVar{Name: "HTTP_PROXY", Value: "http://proxy"},
			want: "HTTP_PROXY is not permitted",
		},
		{
			name: "credential provider",
			env:  corev1.EnvVar{Name: "AWS_PROFILE", Value: "evil"},
			want: "AWS_PROFILE is not permitted",
		},
		{
			name: "process control",
			env:  corev1.EnvVar{Name: "PYTHONPATH", Value: "/tmp/evil"},
			want: "PYTHONPATH is not permitted",
		},
		{
			name: "dynamic tuning value",
			env: corev1.EnvVar{
				Name: "AIM_S3_MAX_WORKERS",
				ValueFrom: &corev1.EnvVarSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{},
				},
			},
			want: "must use a literal value",
		},
		{
			name: "out of range workers",
			env:  corev1.EnvVar{Name: "AIM_S3_MAX_WORKERS", Value: "65"},
			want: "integer from 1 through 64",
		},
		{
			name: "bad log level",
			env:  corev1.EnvVar{Name: "AIM_S3_LOG_LEVEL", Value: "TRACE"},
			want: "must be one of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTypedS3ArtifactEnv([]corev1.EnvVar{tt.env})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateTypedS3ArtifactEnv() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func findEnvVarForTest(env []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			return &env[i]
		}
	}
	return nil
}

func envValueForTest(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}

func assertEnvValue(t *testing.T, env []corev1.EnvVar, name, want string) {
	t.Helper()
	for _, item := range env {
		if item.Name == name {
			if item.Value != want {
				t.Fatalf("%s = %q, want %q", name, item.Value, want)
			}
			if item.ValueFrom != nil {
				t.Fatalf("%s unexpectedly uses valueFrom", name)
			}
			return
		}
	}
	t.Fatalf("%s not found", name)
}
