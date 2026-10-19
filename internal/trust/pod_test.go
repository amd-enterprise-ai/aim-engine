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

package trust

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestBuildProjectionCombinesGenericAndS3Trust(t *testing.T) {
	projection := BuildProjection(Options{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "company.pem",
		},
		S3CABundleRef: &aimv1alpha1.S3CABundleReference{
			Kind: "Secret",
			Name: "s3-ca",
			Key:  "ca.crt",
		},
		IncludeS3Env: true,
		Image:        "registry.example.test/downloader:v1",
	})
	if projection == nil {
		t.Fatal("projection is nil")
	}
	if len(projection.Volumes) != 3 {
		t.Fatalf("volumes = %d, want output and two CA inputs", len(projection.Volumes))
	}
	if got := projection.InitContainer.Args; len(got) != 3 ||
		got[0] != OutputFilePath ||
		got[1] != additionalFilePath ||
		got[2] != s3FilePath {
		t.Fatalf("combiner args = %#v", got)
	}
	if projection.InitContainer.Image != "registry.example.test/downloader:v1" {
		t.Errorf("init image = %q", projection.InitContainer.Image)
	}
	if len(projection.InitContainer.Env) != 0 {
		t.Errorf("trust init env = %#v, want no rollout metadata", projection.InitContainer.Env)
	}
	for _, name := range []string{
		"SSL_CERT_FILE",
		"REQUESTS_CA_BUNDLE",
		"CURL_CA_BUNDLE",
		"AWS_CA_BUNDLE",
		"AIM_S3_CA_BUNDLE",
	} {
		if got := envValue(projection.Env, name); got != OutputFilePath {
			t.Errorf("%s = %q, want combined path", name, got)
		}
	}
	for _, env := range append(projection.Env, projection.InitContainer.Env...) {
		if env.ValueFrom != nil {
			t.Errorf("%s transports CA data through ValueFrom", env.Name)
		}
	}

	pod := &corev1.PodSpec{Containers: []corev1.Container{{Name: "client"}}}
	ApplyToPod(pod, &pod.Containers[0], projection)
	if pod.SecurityContext != nil {
		t.Fatalf("trust projection changed pod security context: %#v", pod.SecurityContext)
	}
}

func TestBuildProjectionReturnsNilWithoutAdditionalTrust(t *testing.T) {
	if got := BuildProjection(Options{IncludeS3Env: true}); got != nil {
		t.Fatalf("projection = %#v, want nil", got)
	}
}

func TestBuildProjectionMakesOnlyAdditionalConfigMapOptional(t *testing.T) {
	projection := BuildProjection(Options{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "company.pem",
		},
		S3CABundleRef: &aimv1alpha1.S3CABundleReference{
			Kind: "ConfigMap",
			Name: "s3-ca",
			Key:  "ca.crt",
		},
	})
	if projection == nil {
		t.Fatal("projection is nil")
	}
	if len(projection.Volumes) != 3 {
		t.Fatalf("volumes = %d, want output and two CA inputs", len(projection.Volumes))
	}

	additional := projection.Volumes[1].ConfigMap
	if additional == nil || additional.Optional == nil || !*additional.Optional {
		t.Fatalf("additional CA ConfigMap optional = %#v, want true", additional)
	}
	s3 := projection.Volumes[2].ConfigMap
	if s3 == nil || s3.Optional != nil {
		t.Fatalf("S3 CA ConfigMap optional = %#v, want required", s3)
	}
}

func TestFetchAdditionalCABundleChecksSelectedKeyWithoutParsing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "company-ca", Namespace: "team-a"},
		Data:       map[string]string{"ca.pem": "deliberately not parsed"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: configMap.Name,
			Key:  "ca.pem",
		},
	}

	result := FetchAdditionalCABundle(context.Background(), client, "team-a", config)
	if !result.OK() {
		t.Fatalf("configured key fetch failed: %v", result.Error)
	}
	if result.Value == nil || result.Value.Name != configMap.Name {
		t.Fatalf("fetched ConfigMap = %#v, want %s/%s", result.Value, configMap.Namespace, configMap.Name)
	}

	config.AdditionalCABundleRef.Key = "missing"
	result = FetchAdditionalCABundle(context.Background(), client, "team-a", config)
	if !result.OK() || result.Value != nil {
		t.Fatalf("missing selected key result = %#v, want optional bundle skipped", result)
	}

	config.AdditionalCABundleRef.Name = "missing"
	result = FetchAdditionalCABundle(context.Background(), client, "team-a", config)
	if !result.OK() || result.Value != nil {
		t.Fatalf("missing ConfigMap result = %#v, want optional bundle skipped", result)
	}
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}
