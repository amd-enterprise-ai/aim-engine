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

package serving

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/kserve/kserve/pkg/controller/v1alpha2/llmisvc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

// jinjaChatTemplate is a realistic value containing Go template delimiters.
const jinjaChatTemplate = "{% for m in messages %}{{ m.role }}: {{ m.content }}{% endfor %}"

func llmProfileSpec() *aimv1alpha2.AIMProfileSpecCommon {
	spec := sampleSpec()
	spec.ModelSources = []aimv1alpha2.AIMModelSource{{
		ModelID:   "qwen/qwen3-32b-fp8",
		SourceURI: "hf://qwen/qwen3-32b-fp8",
	}}
	spec.ContainerEnv = []corev1.EnvVar{{Name: "CHAT_TEMPLATE", Value: jinjaChatTemplate}}
	return spec
}

func buildTestNamespaceConfig(t *testing.T, spec *aimv1alpha2.AIMProfileSpecCommon) *kservev1alpha2.LLMInferenceServiceConfig {
	t.Helper()
	config, err := BuildNamespaceLLMInferenceServiceConfig(NamespaceRuntimeInput{
		ProfileName:  "qwen3-32b-mi300x",
		Namespace:    "team-ml",
		Spec:         spec,
		YAMLContract: profileyaml.CanonicalContract(spec),
		Resources:    gpuResources(),
		NodeAffinity: mi300xAffinity(),
	})
	if err != nil {
		t.Fatalf("BuildNamespaceLLMInferenceServiceConfig: %v", err)
	}
	return config
}

// renderOverKServeBase merges a projected config under an LLMInferenceService
// that references it, on top of KServe's built-in single-node base template,
// and renders the result the way the KServe controller does before building
// the workload Deployment.
func renderOverKServeBase(t *testing.T, config *kservev1alpha2.LLMInferenceServiceConfig) kservev1alpha2.LLMInferenceServiceSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "kserve-v0.20.0-config-llm-template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var base kservev1alpha2.LLMInferenceServiceConfig
	if err := yaml.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	service := &kservev1alpha2.LLMInferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-ml"},
		Spec: kservev1alpha2.LLMInferenceServiceSpec{
			BaseRefs: []corev1.LocalObjectReference{{Name: config.Name}},
		},
	}
	merged, err := llmisvc.MergeSpecs(context.Background(), base.Spec, config.Spec, service.Spec)
	if err != nil {
		t.Fatalf("MergeSpecs: %v", err)
	}
	rendered, err := llmisvc.ReplaceVariables(
		service,
		&kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: service.ObjectMeta, Spec: merged},
		&llmisvc.Config{},
	)
	if err != nil {
		t.Fatalf("ReplaceVariables: %v", err)
	}
	return rendered.Spec
}

func mainContainer(t *testing.T, spec kservev1alpha2.LLMInferenceServiceSpec) corev1.Container {
	t.Helper()
	if spec.Template == nil || len(spec.Template.Containers) != 1 {
		t.Fatalf("expected exactly one workload container, got %#v", spec.Template)
	}
	container := spec.Template.Containers[0]
	if container.Name != LLMInferenceServiceContainerName {
		t.Fatalf("workload container name = %q, want %q", container.Name, LLMInferenceServiceContainerName)
	}
	return container
}

func envValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestBuildNamespaceLLMInferenceServiceConfig_aimImageOverKServeBase(t *testing.T) {
	spec := llmProfileSpec()
	config := buildTestNamespaceConfig(t, spec)
	rendered := renderOverKServeBase(t, config)
	container := mainContainer(t, rendered)

	assertAIMLaunchContract(t, container, spec)
	assertAIMStartupProbe(t, container)
	assertProfilePodShape(t, rendered, container, config.Name)
	assertAIMOwnsModelLoading(t, rendered)
}

// assertAIMLaunchContract checks the fields KServe's base template would
// otherwise impose on an AIM image: its vLLM launch script, the non-root
// requirement, and the Hugging Face hub cache location.
func assertAIMLaunchContract(t *testing.T, container corev1.Container, spec *aimv1alpha2.AIMProfileSpecCommon) {
	t.Helper()
	if container.Image != spec.Image {
		t.Errorf("image = %q, want the profile image %q", container.Image, spec.Image)
	}
	if !slices.Equal(container.Command, []string{"./entrypoint.py"}) || len(container.Args) != 0 {
		t.Errorf("command/args = %q %q, want the AIM entrypoint with no args", container.Command, container.Args)
	}
	if container.WorkingDir != "/workspace" {
		t.Errorf("workingDir = %q, want /workspace (the entrypoint is relative)", container.WorkingDir)
	}
	if container.SecurityContext == nil || container.SecurityContext.RunAsNonRoot == nil || *container.SecurityContext.RunAsNonRoot {
		t.Errorf("runAsNonRoot must be false for root AIM images, got %#v", container.SecurityContext)
	}
	if got, _ := envValue(container.Env, "HF_HUB_CACHE"); got != "/workspace/model-cache/hub" {
		t.Errorf("HF_HUB_CACHE = %q, want the AIM image's hub cache", got)
	}
	if got, _ := envValue(container.Env, "CHAT_TEMPLATE"); got != jinjaChatTemplate {
		t.Errorf("CHAT_TEMPLATE = %q, want the literal Jinja template %q", got, jinjaChatTemplate)
	}
	if _, ok := envValue(container.Env, "AIM_PROFILE_ID"); !ok {
		t.Error("framework env AIM_PROFILE_ID is missing")
	}
}

func assertAIMStartupProbe(t *testing.T, container corev1.Container) {
	t.Helper()
	probe := container.StartupProbe
	if probe == nil || probe.HTTPGet == nil {
		t.Fatalf("startup probe = %#v, want an HTTP probe", probe)
	}
	if probe.FailureThreshold != 360 || probe.HTTPGet.Path != "/health" ||
		probe.HTTPGet.Port.IntValue() != constants.DefaultHTTPPort || probe.HTTPGet.Scheme != corev1.URISchemeHTTP {
		t.Errorf("startup probe = %#v, want an HTTP /health probe with a one-hour budget", probe)
	}
}

// assertProfilePodShape checks that the ServingRuntime pod shape survives the
// merge: resources, node affinity, the AIM shared-memory size over KServe's
// 1Gi, and the profile ConfigMap shared with the ServingRuntime.
func assertProfilePodShape(
	t *testing.T,
	rendered kservev1alpha2.LLMInferenceServiceSpec,
	container corev1.Container,
	configMapName string,
) {
	t.Helper()
	if got := container.Resources.Requests["amd.com/gpu"]; got.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("amd.com/gpu request = %s, want 1", got.String())
	}
	if rendered.Template.Affinity == nil || rendered.Template.Affinity.NodeAffinity == nil {
		t.Error("profile node affinity is missing from the workload template")
	}
	shm := findVolume(rendered.Template.Volumes, constants.VolumeSharedMemory)
	if shm == nil || shm.EmptyDir == nil || shm.EmptyDir.SizeLimit == nil ||
		shm.EmptyDir.SizeLimit.String() != constants.DefaultSharedMemorySize {
		t.Errorf("shared memory volume = %#v, want %s", shm, constants.DefaultSharedMemorySize)
	}
	profileVolume := findVolume(rendered.Template.Volumes, ProfileVolumePrefix)
	if profileVolume == nil || profileVolume.ConfigMap == nil || profileVolume.ConfigMap.Name != configMapName {
		t.Errorf("profile volume = %#v, want the ConfigMap %q", profileVolume, configMapName)
	}
	if findMount(container.VolumeMounts, ProfileVolumePrefix) == nil {
		t.Error("profile ConfigMap is not mounted into the main container")
	}
}

func assertAIMOwnsModelLoading(t *testing.T, rendered kservev1alpha2.LLMInferenceServiceSpec) {
	t.Helper()
	if rendered.StorageInitializer == nil || rendered.StorageInitializer.Enabled == nil || *rendered.StorageInitializer.Enabled {
		t.Errorf("storage initializer must be disabled, got %#v", rendered.StorageInitializer)
	}
	if got := rendered.Model.URI.String(); got != "hf://qwen/qwen3-32b-fp8" {
		t.Errorf("model.uri = %q, want the profile's first model source", got)
	}
	if rendered.Model.Name == nil || *rendered.Model.Name != "qwen/qwen3-32b-fp8" {
		t.Errorf("model.name = %v, want the served model id", rendered.Model.Name)
	}
}

func TestBuildNamespaceLLMInferenceServiceConfig_directVLLMKeepsEngineLaunch(t *testing.T) {
	spec := llmProfileSpec()
	spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA
	spec.AcceleratorModel = "H100"
	spec.Image = "docker.io/vllm/vllm-openai:v0.11.0"

	rendered := renderOverKServeBase(t, buildTestNamespaceConfig(t, spec))
	container := mainContainer(t, rendered)

	if !slices.Equal(container.Command, []string{"vllm", "serve"}) {
		t.Errorf("command = %q, want the direct vLLM launch", container.Command)
	}
	if len(container.Args) == 0 || container.Args[0] != "$("+constants.EnvAIMVLLMModel+")" {
		t.Errorf("args = %q, want the AIM-generated vLLM arguments", container.Args)
	}
	if container.WorkingDir != "" {
		t.Errorf("workingDir = %q, want the image default for non-AIM images", container.WorkingDir)
	}
	if got, _ := envValue(container.Env, "HF_HUB_CACHE"); got == "/workspace/model-cache/hub" {
		t.Error("the AIM hub cache must not be forced onto non-AIM images")
	}
}

func TestBuildNamespaceLLMInferenceServiceConfig_metadata(t *testing.T) {
	config := buildTestNamespaceConfig(t, llmProfileSpec())

	if config.Name != RuntimeName("qwen3-32b-mi300x") || config.Namespace != "team-ml" {
		t.Errorf("config %s/%s, want the ServingRuntime name in the profile namespace", config.Namespace, config.Name)
	}
	if config.Labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
		t.Errorf("managed-by label = %q", config.Labels[constants.LabelK8sManagedBy])
	}
	if config.Annotations[constants.AnnotationProjectedProfile] != "qwen3-32b-mi300x" {
		t.Errorf("projected-profile annotation = %q", config.Annotations[constants.AnnotationProjectedProfile])
	}
	if config.Annotations[constants.AnnotationRuntimeProjectionContentHash] == "" {
		t.Error("content hash annotation is missing")
	}
}

func TestBuildClusterLLMInferenceServiceConfig_bareInKServeNamespace(t *testing.T) {
	spec := llmProfileSpec()
	config, err := BuildClusterLLMInferenceServiceConfig(ClusterRuntimeInput{
		ProfileName:  "qwen3-32b-mi300x",
		Spec:         spec,
		Resources:    gpuResources(),
		NodeAffinity: mi300xAffinity(),
	}, "kserve-system")
	if err != nil {
		t.Fatalf("BuildClusterLLMInferenceServiceConfig: %v", err)
	}

	if config.Namespace != "kserve-system" || config.Name != RuntimeName("qwen3-32b-mi300x") {
		t.Errorf("config %s/%s, want the ClusterServingRuntime name in the KServe namespace", config.Namespace, config.Name)
	}
	container := mainContainer(t, renderOverKServeBase(t, config))
	if findMount(container.VolumeMounts, ProfileVolumePrefix) != nil {
		t.Error("a bare system-namespace config must not mount a namespaced profile ConfigMap")
	}
}

func TestBuildLLMInferenceServiceConfig_invalidModelSource(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []aimv1alpha2.AIMModelSource
	}{
		{"no model sources", nil},
		{"source without scheme", []aimv1alpha2.AIMModelSource{{ModelID: "qwen/qwen3-32b", SourceURI: "qwen/qwen3-32b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := llmProfileSpec()
			spec.ModelSources = tc.sources
			_, err := BuildNamespaceLLMInferenceServiceConfig(NamespaceRuntimeInput{
				ProfileName:  "qwen3-32b-mi300x",
				Namespace:    "team-ml",
				Spec:         spec,
				YAMLContract: profileyaml.CanonicalContract(spec),
			})
			if err == nil {
				t.Fatal("expected an error for a model source KServe cannot reference")
			}
		})
	}
}

func TestLLMInferenceServiceConfigContentHash_tracksWorkloadContent(t *testing.T) {
	first := buildTestNamespaceConfig(t, llmProfileSpec())
	again := buildTestNamespaceConfig(t, llmProfileSpec())
	if first.Annotations[constants.AnnotationRuntimeProjectionContentHash] !=
		again.Annotations[constants.AnnotationRuntimeProjectionContentHash] {
		t.Error("equivalent inputs produced different content hashes")
	}

	changed := llmProfileSpec()
	changed.Image = "ghcr.io/aim/qwen3-32b:1.0.1"
	if buildTestNamespaceConfig(t, changed).Annotations[constants.AnnotationRuntimeProjectionContentHash] ==
		first.Annotations[constants.AnnotationRuntimeProjectionContentHash] {
		t.Error("an image change did not change the content hash")
	}
}
