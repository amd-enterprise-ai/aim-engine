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
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// LLMInferenceServiceContainerName is the inference container name KServe's
	// LLMInferenceService controller and its built-in base template target.
	// Every container field set here is merged over that template's container
	// of the same name.
	LLMInferenceServiceContainerName = "main"

	// aimRuntimeEntrypoint and aimRuntimeWorkingDir are the AIM image launch
	// contract (ENTRYPOINT ["./entrypoint.py"], WORKDIR /workspace). KServe's
	// base template replaces the container command with a vLLM launch script,
	// and a merge cannot unset a field, so AIM images get their entrypoint
	// back explicitly.
	aimRuntimeEntrypoint = "./entrypoint.py"
	aimRuntimeWorkingDir = "/workspace"

	// aimRuntimeHFHubCache restores the Hugging Face hub cache AIM images get
	// from their HF_HOME (/workspace/model-cache). KServe's base template sets
	// HF_HUB_CACHE=/models, which would otherwise take precedence.
	aimRuntimeHFHubCache = "/workspace/model-cache/hub"

	// llmStartupProbeFailureThreshold allows one hour (at the template's 10s
	// period) for weights to load before liveness checks begin. KServe's base
	// template allows 10 minutes, which large models loading from a cache PVC
	// or downloading on first start can exceed.
	llmStartupProbeFailureThreshold = 360
	llmStartupProbePeriodSeconds    = 10
	llmHealthPath                   = "/health"
)

// BuildNamespaceLLMInferenceServiceConfig projects a resolved profile into a
// namespaced KServe LLMInferenceServiceConfig with the same name as the
// profile's namespace ServingRuntime. Both serve the profile from the same pod
// shape and mount the same colocated profile ConfigMap, which callers apply
// from BuildNamespaceServingRuntime; this builder does not return it.
//
// The config disables KServe's storage initializer: AIM Engine owns model
// loading exactly as it does for InferenceServices (the AIM runtime downloads
// on start, or loads from the mounted AIMProfileCache PVCs). spec.model.uri
// is the profile's first model source and is informational for KServe.
func BuildNamespaceLLMInferenceServiceConfig(input NamespaceRuntimeInput) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	projection, err := buildNamespaceProjection(input)
	if err != nil {
		return nil, err
	}
	return buildLLMInferenceServiceConfig(projection, input.Namespace, input.Spec)
}

// BuildClusterLLMInferenceServiceConfig projects a resolved cluster profile into
// a bare LLMInferenceServiceConfig in kserveNamespace, where KServe resolves
// spec.baseRefs that do not exist in the consuming service's namespace. Like
// the bare ClusterServingRuntime it carries no profile ConfigMap, so it is a
// shadow target: the lazy projection materializes a complete namespaced config
// of the same name in each consuming namespace, which KServe prefers.
func BuildClusterLLMInferenceServiceConfig(
	input ClusterRuntimeInput,
	kserveNamespace string,
) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	if kserveNamespace == "" {
		return nil, fmt.Errorf("KServe namespace is empty")
	}
	projection, err := buildClusterProjection(input)
	if err != nil {
		return nil, err
	}
	return buildLLMInferenceServiceConfig(projection, kserveNamespace, input.Spec)
}

func buildLLMInferenceServiceConfig(
	projection namespaceProjection,
	namespace string,
	spec *aimv1alpha2.AIMProfileSpecCommon,
) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	modelURI, err := LLMModelURI(spec)
	if err != nil {
		return nil, err
	}
	template, err := buildLLMPodTemplate(projection.runtimeSpec, spec)
	if err != nil {
		return nil, err
	}

	configSpec, err := escapeKServeTemplateActions(kservev1alpha2.LLMInferenceServiceSpec{
		Model: kservev1alpha2.LLMModelSpec{
			URI:  *modelURI,
			Name: ptr.To(resolvedServedModelID(spec)),
		},
		StorageInitializer: &kservev1alpha2.StorageInitializerSpec{Enabled: ptr.To(false)},
		WorkloadSpec:       kservev1alpha2.WorkloadSpec{Template: template},
	})
	if err != nil {
		return nil, err
	}

	config := &kservev1alpha2.LLMInferenceServiceConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha2.SchemeGroupVersion.String(),
			Kind:       "LLMInferenceServiceConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        projection.name,
			Namespace:   namespace,
			Labels:      projection.labels,
			Annotations: projection.annotations,
		},
		Spec: configSpec,
	}

	contentHash, err := LLMInferenceServiceConfigContentHash(config, projection.configMap)
	if err != nil {
		return nil, err
	}
	config.Annotations[constants.AnnotationRuntimeProjectionContentHash] = contentHash
	return config, nil
}

// LLMModelURI returns the profile's first model source as an
// LLMInferenceService or LLMInferenceServiceConfig spec.model.uri. KServe
// requires a URI with a scheme even when its storage initializer is disabled.
func LLMModelURI(spec *aimv1alpha2.AIMProfileSpecCommon) (*apis.URL, error) {
	if len(spec.ModelSources) == 0 || spec.ModelSources[0].SourceURI == "" {
		return nil, fmt.Errorf("profile has no model source to set as LLMInferenceServiceConfig spec.model.uri")
	}
	sourceURI := spec.ModelSources[0].SourceURI
	if !strings.Contains(sourceURI, "://") {
		return nil, fmt.Errorf("model source %q has no URI scheme; LLMInferenceServiceConfig spec.model.uri requires one", sourceURI)
	}
	parsed, err := apis.ParseURL(sourceURI)
	if err != nil {
		return nil, fmt.Errorf("parse model source %q as LLMInferenceServiceConfig spec.model.uri: %w", sourceURI, err)
	}
	return parsed, nil
}

// buildLLMPodTemplate converts the ServingRuntime pod shape into the
// LLMInferenceService workload template, overriding the base-template fields
// that conflict with AIM images: the container name, the launch command, the
// non-root requirement (AIM images run as root), the Hugging Face hub cache,
// and the startup probe budget.
func buildLLMPodTemplate(
	runtimeSpec kservev1alpha1.ServingRuntimeSpec,
	spec *aimv1alpha2.AIMProfileSpecCommon,
) (*corev1.PodSpec, error) {
	if len(runtimeSpec.Containers) != 1 {
		return nil, fmt.Errorf("expected one runtime container, got %d", len(runtimeSpec.Containers))
	}
	container := runtimeSpec.Containers[0].DeepCopy()
	container.Name = LLMInferenceServiceContainerName

	if len(container.Command) == 0 {
		container.Command = []string{aimRuntimeEntrypoint}
		container.WorkingDir = aimRuntimeWorkingDir
		container.Env = upsertEnvVars(container.Env, []corev1.EnvVar{{Name: "HF_HUB_CACHE", Value: aimRuntimeHFHubCache}})
	}
	container.SecurityContext = &corev1.SecurityContext{RunAsNonRoot: ptr.To(false)}
	container.StartupProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: llmHealthPath,
				Port: intstr.FromInt32(constants.DefaultHTTPPort),
			},
		},
		PeriodSeconds:    llmStartupProbePeriodSeconds,
		FailureThreshold: llmStartupProbeFailureThreshold,
	}

	return &corev1.PodSpec{
		Containers:         []corev1.Container{*container},
		Volumes:            runtimeSpec.Volumes,
		ImagePullSecrets:   runtimeSpec.ImagePullSecrets,
		Affinity:           runtimeSpec.Affinity,
		ServiceAccountName: spec.ServiceAccountName,
	}, nil
}

// escapeKServeTemplateActions protects literal "{{" in the config. KServe
// renders every merged LLMInferenceServiceConfig as a Go text/template over its
// JSON encoding (missingkey=error), so a Jinja chat template or any other
// value containing "{{" would otherwise be evaluated or rejected. Each "{{"
// becomes a raw-string action that renders back to "{{"; backquotes survive
// the JSON encoding unescaped, unlike a double-quoted string action.
func escapeKServeTemplateActions(spec kservev1alpha2.LLMInferenceServiceSpec) (kservev1alpha2.LLMInferenceServiceSpec, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return spec, fmt.Errorf("marshal LLMInferenceServiceConfig spec: %w", err)
	}
	if !bytes.Contains(raw, []byte("{{")) {
		return spec, nil
	}
	escaped := bytes.ReplaceAll(raw, []byte("{{"), []byte("{{`{{`}}"))
	var out kservev1alpha2.LLMInferenceServiceSpec
	if err := json.Unmarshal(escaped, &out); err != nil {
		return spec, fmt.Errorf("unmarshal escaped LLMInferenceServiceConfig spec: %w", err)
	}
	return out, nil
}

// LLMInferenceServiceConfigContentHash returns a deterministic hash of the
// workload-affecting content of a projected LLMInferenceServiceConfig and the
// profile ConfigMap it mounts (nil for a bare system-namespace config).
// Consumers compare it against the live object's annotation to confirm the
// projection is current before rolling out.
func LLMInferenceServiceConfigContentHash(
	config *kservev1alpha2.LLMInferenceServiceConfig,
	configMap *corev1.ConfigMap,
) (string, error) {
	if config == nil {
		return "", fmt.Errorf("LLMInferenceServiceConfig must be non-nil")
	}
	content := struct {
		Spec          kservev1alpha2.LLMInferenceServiceSpec `json:"spec"`
		ConfigMapData map[string]string                      `json:"configMapData,omitempty"`
	}{Spec: config.Spec}
	if configMap != nil {
		content.ConfigMapData = configMap.Data
	}
	data, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("marshal LLMInferenceServiceConfig projection content: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
