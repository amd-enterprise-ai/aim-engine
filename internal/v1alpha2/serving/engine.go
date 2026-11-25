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
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const engineVLLM = "vllm"

// engineInvocation is the image-independent container process projection
// derived from a profile. Legacy AIM-runtime images retain their baked
// entrypoint; the initial direct-engine contract is upstream vLLM on NVIDIA.
type engineInvocation struct {
	Command []string
	Args    []string
}

type vllmModelReference struct {
	value string
	local bool
}

// usesDirectVLLM identifies the v0.1 upstream NVIDIA vLLM contract. Existing
// AMD AIM images also declare engine=vllm, but launch through aim-runtime and
// must retain their baked entrypoint. acceleratorVendor=nvidia is therefore
// the explicit opt-in for direct vLLM projection in this iteration.
func usesDirectVLLM(spec *aimv1alpha2.AIMProfileSpecCommon) bool {
	return spec != nil &&
		spec.AcceleratorVendor == aimv1alpha1.AcceleratorVendorNVIDIA &&
		strings.EqualFold(spec.Engine, engineVLLM)
}

// buildEngineInvocation returns the command and arguments for the profile's
// runtime image. engineArgs are optional: AIM Engine generates the complete
// baseline vLLM launch and treats engineArgs as user tuning/override input.
func buildEngineInvocation(
	spec *aimv1alpha2.AIMProfileSpecCommon,
) (engineInvocation, error) {
	if !usesDirectVLLM(spec) {
		return engineInvocation{}, nil
	}

	servedModel := resolvedServedModelID(spec)
	if servedModel == "" {
		return engineInvocation{}, fmt.Errorf("direct vLLM profile has no model identifier")
	}

	userArgs, userKeys, err := decodeVLLMEngineArgs(spec)
	if err != nil {
		return engineInvocation{}, err
	}

	args := []string{"$(" + constants.EnvAIMVLLMModel + ")"}
	if _, overridden := userKeys["served-model-name"]; !overridden {
		args = append(args, "--served-model-name="+servedModel)
	}
	args = append(args,
		"--host=0.0.0.0",
		fmt.Sprintf("--port=%d", constants.DefaultHTTPPort),
	)
	if spec.AcceleratorCount > 0 {
		if _, overridden := userKeys["tensor-parallel-size"]; !overridden {
			args = append(args, fmt.Sprintf("--tensor-parallel-size=%d", spec.AcceleratorCount))
		}
	}
	args = append(args, userArgs...)

	return engineInvocation{
		Command: []string{"vllm", "serve"},
		Args:    args,
	}, nil
}

// decodeVLLMEngineArgs converts the profile's free-form object into stable CLI
// arguments. Keys accept either snake_case or kebab-case and are normalized to
// vLLM's long-option form. Null and true emit a flag without a value, false
// emits vLLM's --no-<flag> form, arrays repeat the option, and structured values
// are encoded as compact JSON.
func decodeVLLMEngineArgs(
	spec *aimv1alpha2.AIMProfileSpecCommon,
) ([]string, map[string]struct{}, error) {
	keys := map[string]struct{}{}
	if spec.EngineArgs == nil || len(spec.EngineArgs.Raw) == 0 {
		return nil, keys, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(spec.EngineArgs.Raw))
	decoder.UseNumber()
	raw := map[string]any{}
	if err := decoder.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("decode vLLM engineArgs: %w", err)
	}

	normalized := make(map[string]any, len(raw))
	for rawKey, value := range raw {
		key := normalizeVLLMArgKey(rawKey)
		if key == "" {
			return nil, nil, fmt.Errorf("vLLM engineArgs contains an empty key")
		}
		if _, duplicate := normalized[key]; duplicate {
			return nil, nil, fmt.Errorf("vLLM engineArgs contains duplicate normalized key %q", key)
		}
		switch key {
		case "model", "host", "port":
			return nil, nil, fmt.Errorf("vLLM engineArgs.%s is managed by AIM Engine", key)
		}
		normalized[key] = value
		keys[key] = struct{}{}
	}

	sortedKeys := make([]string, 0, len(normalized))
	for key := range normalized {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	var args []string
	for _, key := range sortedKeys {
		rendered, err := renderVLLMArg(key, normalized[key])
		if err != nil {
			return nil, nil, err
		}
		args = append(args, rendered...)
	}
	return args, keys, nil
}

func normalizeVLLMArgKey(key string) string {
	key = strings.TrimSpace(strings.TrimLeft(key, "-"))
	return strings.ReplaceAll(key, "_", "-")
}

func renderVLLMArg(key string, value any) ([]string, error) {
	flag := "--" + key
	if value == nil {
		return []string{flag}, nil
	}
	if values, ok := value.([]any); ok {
		args := make([]string, 0, len(values))
		for _, item := range values {
			rendered, err := renderVLLMArg(key, item)
			if err != nil {
				return nil, err
			}
			args = append(args, rendered...)
		}
		return args, nil
	}

	var rendered string
	switch typed := value.(type) {
	case string:
		rendered = typed
	case json.Number:
		rendered = typed.String()
	case bool:
		if typed {
			return []string{flag}, nil
		}
		return []string{"--no-" + key}, nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("encode vLLM engineArgs.%s: %w", key, err)
		}
		rendered = string(encoded)
	}
	return []string{flag + "=" + rendered}, nil
}

// buildRuntimeEnv preserves the legacy AIM-runtime environment contract unless
// the profile explicitly selects direct NVIDIA vLLM. With a direct engine the
// subprocess boundary disappears, so engineEnv is promoted to container env.
func buildRuntimeEnv(
	spec *aimv1alpha2.AIMProfileSpecCommon,
	profileYAMLFilename string,
	modelReference vllmModelReference,
) []corev1.EnvVar {
	if !usesDirectVLLM(spec) {
		return upsertEnvVars(spec.ContainerEnv, BuildFrameworkEnvVars(spec, profileYAMLFilename))
	}

	env := append([]corev1.EnvVar(nil), spec.ContainerEnv...)
	engineEnvNames := make([]string, 0, len(spec.EngineEnv))
	for name := range spec.EngineEnv {
		engineEnvNames = append(engineEnvNames, name)
	}
	sort.Strings(engineEnvNames)
	for _, name := range engineEnvNames {
		env = upsertEnvVars(env, []corev1.EnvVar{{Name: name, Value: spec.EngineEnv[name]}})
	}

	offline := "0"
	if modelReference.local {
		offline = "1"
	}
	return upsertEnvVars(env, []corev1.EnvVar{
		{Name: constants.EnvAIMVLLMModel, Value: modelReference.value},
		{Name: "HF_HUB_OFFLINE", Value: offline},
		{Name: "TRANSFORMERS_OFFLINE", Value: offline},
	})
}

// OperatorOwnedEnvNames returns the env names that neither an AIMService
// spec.env nor a runtime config spec.env sets: the AIM framework names (AIM_PROFILE_ID, AIM_ENGINE,
// AIM_ID, AIM_MODEL_ID, AIM_CACHE_PATH) for every profile, even where
// BuildFrameworkEnvVars leaves one unset, and for direct-vLLM profiles also
// AIM_VLLM_MODEL, the offline guards and the engineEnv names. Every name that
// buildRuntimeEnv sets over the profile containerEnv is in the set.
func OperatorOwnedEnvNames(spec *aimv1alpha2.AIMProfileSpecCommon) map[string]bool {
	names := map[string]bool{
		constants.EnvAIMProfileID: true,
		constants.EnvAIMEngine:    true,
		constants.EnvAIMID:        true,
		constants.EnvAIMModelID:   true,
		constants.EnvAIMCachePath: true,
	}
	if !usesDirectVLLM(spec) {
		return names
	}
	for name := range spec.EngineEnv {
		names[name] = true
	}
	for _, name := range []string{constants.EnvAIMVLLMModel, "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE"} {
		names[name] = true
	}
	return names
}

func resolvedServedModelID(spec *aimv1alpha2.AIMProfileSpecCommon) string {
	if spec.ModelId != "" {
		return spec.ModelId
	}
	if len(spec.ModelSources) > 0 && spec.ModelSources[0].ModelID != "" {
		return spec.ModelSources[0].ModelID
	}
	return spec.AimId
}

func cachedModelID(spec *aimv1alpha2.AIMProfileSpecCommon) string {
	if len(spec.ModelSources) > 0 && spec.ModelSources[0].ModelID != "" {
		return spec.ModelSources[0].ModelID
	}
	return resolvedServedModelID(spec)
}

// resolveVLLMModelReference returns the exact mounted path for a matching ready
// artifact. Without one it returns the model ID so upstream vLLM can download
// from Hugging Face instead of receiving a nonexistent local path in offline
// mode. The local bit controls the Hugging Face/Transformers offline guards.
func resolveVLLMModelReference(
	spec *aimv1alpha2.AIMProfileSpecCommon,
	cache *aimv1alpha2.AIMProfileCache,
) vllmModelReference {
	modelID := cachedModelID(spec)
	if modelID == "" {
		return vllmModelReference{}
	}

	if path := readyVLLMModelPath(modelID, cache); path != "" {
		return vllmModelReference{value: path, local: true}
	}

	return vllmModelReference{value: resolvedServedModelID(spec)}
}

func readyVLLMModelPath(modelID string, cache *aimv1alpha2.AIMProfileCache) string {
	if modelID == "" || cache == nil || cache.Status.Status != constants.AIMStatusReady {
		return ""
	}
	names := make([]string, 0, len(cache.Status.Artifacts))
	for name := range cache.Status.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		resolved := cache.Status.Artifacts[name]
		if resolved.Status != constants.AIMStatusReady ||
			resolved.PersistentVolumeClaim == "" ||
			resolved.Model != modelID {
			continue
		}
		return resolvedArtifactMountPath(resolved, artifactVolumeName(resolved))
	}
	return ""
}

// BuildDirectVLLMCacheEnv returns the model-reference override for a ready cache
// mounted by an AIMService overlay. Namespace runtimes already receive this env
// when they mount a Shared cache; this helper gives Dedicated caches the same
// exact local-path/offline behavior without duplicating path resolution.
func BuildDirectVLLMCacheEnv(
	spec *aimv1alpha2.AIMProfileSpecCommon,
	cache *aimv1alpha2.AIMProfileCache,
) []corev1.EnvVar {
	if !usesDirectVLLM(spec) {
		return nil
	}
	path := readyVLLMModelPath(cachedModelID(spec), cache)
	if path == "" {
		return nil
	}
	return []corev1.EnvVar{
		{Name: constants.EnvAIMVLLMModel, Value: path},
		{Name: "HF_HUB_OFFLINE", Value: "1"},
		{Name: "TRANSFORMERS_OFFLINE", Value: "1"},
	}
}

func artifactVolumeName(resolved aimv1alpha1.AIMResolvedArtifact) string {
	return strings.ReplaceAll(utils.MakeRFC1123Compliant(resolved.Name), ".", "-")
}

func resolvedArtifactMountPath(
	resolved aimv1alpha1.AIMResolvedArtifact,
	volumeName string,
) string {
	if resolved.MountPoint != "" {
		return resolved.MountPoint
	}
	safeModelName := strings.ReplaceAll(resolved.Model, "..", "")
	if safeModelName == "" || safeModelName == "." {
		safeModelName = volumeName
	}
	return filepath.Join(constants.AIMCacheBasePath, safeModelName)
}
