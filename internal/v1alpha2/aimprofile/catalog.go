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

package aimprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

// Discovery cache on-disk layout (ConfigMap data keys):
//
//   profiles.<flattened-path>.yaml    Raw profile YAML exactly as produced by the
//                                     image (or hand-authored for pre-seeded sources).
//                                     The in-image relative path "foo/bar.yaml" flattens
//                                     to "profiles.foo__bar.yaml" because ConfigMap keys
//                                     cannot contain "/".
//   metadata.json                     Optional JSON sidecar with {aimId, sourceImage,
//                                     baseImage, sourceSpecHash, discoveryCommandVersion,
//                                     profileIds, discoveredAt}.
//
// There is no persisted catalog.json. The DiscoveryCatalog struct is rebuilt in-memory
// from the raw YAMLs on every read, so parser changes apply immediately without
// migrating existing caches.

const (
	// ProfilesDataKeyPrefix is the prefix every raw-profile key in the cache starts with.
	ProfilesDataKeyPrefix = "profiles."

	// ProfilesDataKeySuffix is the trailing suffix for raw-profile keys.
	ProfilesDataKeySuffix = ".yaml"

	// DiscoveryCacheMetadataKey is the ConfigMap data key holding cache metadata.
	DiscoveryCacheMetadataKey = "metadata.json"

	// pathSeparator is the in-image path separator we replace when composing ConfigMap keys.
	pathSeparator = "/"

	// flattenedSeparator stands in for "/" inside ConfigMap keys.
	flattenedSeparator = "__"
)

// DiscoveryCatalog is the normalized discovery output rebuilt in-memory from a
// cache ConfigMap's raw-YAML data keys.
type DiscoveryCatalog struct {
	AimID       string
	SourceImage string
	BaseImage   string
	Profiles    []DiscoveryCatalogItem
}

// DiscoveryCatalogItem is one normalized discovered profile entry.
type DiscoveryCatalogItem struct {
	Name      string
	Spec      aimv1alpha2.AIMProfileSpecCommon
	Version   string
	BaseImage string
	// RelPath is the in-image relative path the profile was emitted from, e.g.
	// "Qwen/Qwen3-0.6B/vllm-cpu-bf16-tp1-latency.yaml" or
	// "general/vllm-cpu-bf16-tp1-latency.yaml". Retained on the item so the
	// catalog can disambiguate same-named profiles emitted from both the
	// model-specific and general/ directories (model-specific wins).
	RelPath string
	// PrimaryExplicit reports whether the source profile YAML set the
	// `metadata.primary` field itself (true) or left it unset (false). The
	// distinction matters at materialisation time: when the YAML didn't
	// stamp `primary`, the AIMModel reconciler falls back to the OCI
	// `recommendedDeployments` label to derive Spec.Primary so legacy
	// images (which use the OCI label as the primary signal and don't yet
	// stamp `primary` per-profile) still bubble up an auto-selectable
	// subset. Explicit values from the YAML always win.
	PrimaryExplicit bool
}

// DiscoveryCacheMetadata is the JSON sidecar stored at metadata.json.
type DiscoveryCacheMetadata struct {
	AimID                   string   `json:"aimId,omitempty"`
	SourceImage             string   `json:"sourceImage,omitempty"`
	BaseImage               string   `json:"baseImage,omitempty"`
	SourceSpecHash          string   `json:"sourceSpecHash,omitempty"`
	DiscoveryCommandVersion string   `json:"discoveryCommandVersion,omitempty"`
	DiscoveredAt            string   `json:"discoveredAt,omitempty"`
	ProfileIDs              []string `json:"profileIds,omitempty"`
}

// imageProfileFile mirrors the YAML schema the AIM image emits.
type imageProfileFile struct {
	AimID      string               `json:"aim_id,omitempty"`
	ModelID    string               `json:"model_id,omitempty"`
	Metadata   imageProfileMetadata `json:"metadata"`
	EngineArgs map[string]any       `json:"engine_args,omitempty"`
	EnvVars    map[string]string    `json:"env_vars,omitempty"`
}

type imageProfileMetadata struct {
	Engine              string                      `json:"engine"`
	Metric              aimv1alpha1.AIMMetric       `json:"metric"`
	Precision           aimv1alpha1.AIMPrecision    `json:"precision"`
	Type                aimv1alpha1.AIMProfileType  `json:"type"`
	Primary             *bool                       `json:"primary,omitempty"`
	ManualSelectionOnly bool                        `json:"manual_selection_only,omitempty"`
	AcceleratorModel    string                      `json:"accelerator_model,omitempty"`
	GPU                 string                      `json:"gpu,omitempty"`
	AcceleratorType     aimv1alpha1.AcceleratorType `json:"accelerator_type,omitempty"`
	AcceleratorCount    *int32                      `json:"accelerator_count,omitempty"`
	GPUCount            *int32                      `json:"gpu_count,omitempty"`
	// Features mirrors the runtime's metadata.features (e.g. "adapters"); discovery
	// materialises it onto AIMProfile.spec.features so gating matches the image.
	Features []string `json:"features,omitempty"`
}

// ToCandidates returns the catalog entries as ProfileCopy candidates.
func (c DiscoveryCatalog) ToCandidates() []ProfileCopyCandidate {
	out := make([]ProfileCopyCandidate, 0, len(c.Profiles))
	for _, item := range c.Profiles {
		out = append(out, ProfileCopyCandidate{
			Name:      item.Name,
			Spec:      *item.Spec.DeepCopy(),
			Status:    aimv1alpha2.AIMProfileStatus{Version: item.Version},
			BaseImage: firstNonEmpty(item.BaseImage, c.BaseImage),
		})
	}
	return out
}

// FlattenProfilePath turns an in-image relative path into a ConfigMap data key.
// Example: "HuggingFaceTB/SmolLM2-135M/cpu.yaml" -> "profiles.HuggingFaceTB__SmolLM2-135M__cpu.yaml".
func FlattenProfilePath(relpath string) string {
	trimmed := strings.TrimSuffix(relpath, ProfilesDataKeySuffix)
	return ProfilesDataKeyPrefix + strings.ReplaceAll(trimmed, pathSeparator, flattenedSeparator) + ProfilesDataKeySuffix
}

// UnflattenProfileKey reverses FlattenProfilePath. Returns ("", false) if the key
// does not look like a raw-profile key.
func UnflattenProfileKey(key string) (string, bool) {
	if !strings.HasPrefix(key, ProfilesDataKeyPrefix) || !strings.HasSuffix(key, ProfilesDataKeySuffix) {
		return "", false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(key, ProfilesDataKeyPrefix), ProfilesDataKeySuffix)
	if middle == "" {
		return "", false
	}
	return strings.ReplaceAll(middle, flattenedSeparator, pathSeparator) + ProfilesDataKeySuffix, true
}

// HasRawProfileKeys reports whether the ConfigMap contains at least one
// profiles.*.yaml data key.
func HasRawProfileKeys(cm *corev1.ConfigMap) bool {
	if cm == nil {
		return false
	}
	for k := range cm.Data {
		if _, ok := UnflattenProfileKey(k); ok {
			return true
		}
	}
	return false
}

// ParseDiscoveryCatalog rebuilds the normalized DiscoveryCatalog from a ConfigMap
// containing raw profiles.*.yaml keys and an optional metadata.json sidecar.
func ParseDiscoveryCatalog(cm *corev1.ConfigMap) (*DiscoveryCatalog, error) {
	if cm == nil {
		return nil, fmt.Errorf("configmap is nil")
	}

	metadata, err := readCacheMetadata(cm)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(cm.Data))
	for k := range cm.Data {
		if _, ok := UnflattenProfileKey(k); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("ConfigMap %s/%s has no %s*%s keys", cm.Namespace, cm.Name, ProfilesDataKeyPrefix, ProfilesDataKeySuffix)
	}
	sort.Strings(keys)

	catalog := &DiscoveryCatalog{
		AimID:       metadata.AimID,
		SourceImage: metadata.SourceImage,
		BaseImage:   metadata.BaseImage,
		Profiles:    make([]DiscoveryCatalogItem, 0, len(keys)),
	}

	version := ExtractVersionFromImage(metadata.SourceImage)
	for _, key := range keys {
		rel, _ := UnflattenProfileKey(key)
		item, err := parseProfileYAMLIntoCatalogItem([]byte(cm.Data[key]), rel, metadata.AimID, metadata.SourceImage, metadata.BaseImage, version)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", key, err)
		}
		catalog.Profiles = append(catalog.Profiles, item)
	}

	catalog.Profiles = dedupCatalogItems(catalog.Profiles, metadata.AimID)
	return catalog, nil
}

// dedupCatalogItems enforces the "bound profiles win over general/" precedence.
//
// Discovery scans both `$AIM_ID/` and `general/` (the latter is the only
// inhabited directory for unbound base images). When a model-specific image
// also leaks `general/` profiles from the base layer they collide on basename
// (e.g. both directories ship `vllm-cpu-bf16-tp1-latency.yaml`) and the
// downstream pipeline cannot tell them apart — `parseProfileYAMLIntoCatalogItem`
// derives `Spec.ProfileId` from `path.Base(relpath)`, so two YAMLs at different
// directories produce items with the same `Name`/`ProfileId` but different
// (and contradictory) specs. Re-applying both in turn causes the AIMModel
// reconciler to oscillate the resulting AIMProfile spec on every pass.
//
// The semantically correct precedence is "model-specific wins": when a bound
// AimID is present and at least one catalog item is rooted under `$AIM_ID/`,
// we drop every item rooted under `general/`. For genuine base images (empty
// AimID) and for malformed images that only leak `general/`, we keep the
// general items as the only available source — graceful degradation.
//
// A safer long-term fix is to stop bundling `general/` into model-specific
// images at build time; this dedup makes the operator robust against the
// leak in the meantime.
func dedupCatalogItems(items []DiscoveryCatalogItem, aimID string) []DiscoveryCatalogItem {
	if aimID == "" || len(items) == 0 {
		return items
	}
	boundPrefix := aimID + "/"
	const generalPrefix = "general/"
	hasBound := false
	for _, item := range items {
		if strings.HasPrefix(item.RelPath, boundPrefix) {
			hasBound = true
			break
		}
	}
	if !hasBound {
		return items
	}
	out := make([]DiscoveryCatalogItem, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.RelPath, generalPrefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// LoadDiscoveryCatalog fetches a discovery cache ConfigMap by ProfileSourceRef and
// parses it into a DiscoveryCatalog.
func LoadDiscoveryCatalog(ctx context.Context, c client.Client, ref aimv1alpha1.ProfileSourceRef, namespace string) (*corev1.ConfigMap, *DiscoveryCatalog, error) {
	var configMap corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: namespace}, &configMap); err != nil {
		return nil, nil, err
	}
	catalog, err := ParseDiscoveryCatalog(&configMap)
	if err != nil {
		return &configMap, nil, err
	}
	return &configMap, catalog, nil
}

// ReadCacheMetadata extracts the metadata sidecar, if present. Returns a zero-valued
// struct (no error) when the ConfigMap has no metadata.json — metadata is optional for
// user-authored pre-seeded sources.
func ReadCacheMetadata(cm *corev1.ConfigMap) (DiscoveryCacheMetadata, error) {
	return readCacheMetadata(cm)
}

func readCacheMetadata(cm *corev1.ConfigMap) (DiscoveryCacheMetadata, error) {
	var md DiscoveryCacheMetadata
	if cm == nil {
		return md, nil
	}
	raw := cm.Data[DiscoveryCacheMetadataKey]
	if raw == "" {
		return md, nil
	}
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		return md, fmt.Errorf("parse %s: %w", DiscoveryCacheMetadataKey, err)
	}
	return md, nil
}

// parseProfileYAMLIntoCatalogItem parses a single raw profile YAML into a
// DiscoveryCatalogItem, merging defaults from the cache metadata.
func parseProfileYAMLIntoCatalogItem(raw []byte, relpath, defaultAimID, sourceImage, baseImage, version string) (DiscoveryCatalogItem, error) {
	var parsed imageProfileFile
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return DiscoveryCatalogItem{}, fmt.Errorf("unmarshal profile yaml: %w", err)
	}

	acceleratorModel := parsed.Metadata.AcceleratorModel
	legacyGPU := acceleratorModel == "" && parsed.Metadata.GPU != ""
	if acceleratorModel == "" {
		acceleratorModel = parsed.Metadata.GPU
	}

	var acceleratorCount int32
	legacyCount := false
	switch {
	case parsed.Metadata.AcceleratorCount != nil:
		acceleratorCount = *parsed.Metadata.AcceleratorCount
	case parsed.Metadata.GPUCount != nil:
		acceleratorCount = *parsed.Metadata.GPUCount
		legacyCount = true
	}

	acceleratorType := parsed.Metadata.AcceleratorType
	if acceleratorType == "" && (legacyGPU || legacyCount) {
		acceleratorType = aimv1alpha1.AcceleratorTypeGPU
	}

	engineArgsJSON, err := marshalEngineArgs(parsed.EngineArgs)
	if err != nil {
		return DiscoveryCatalogItem{}, fmt.Errorf("marshal engine args: %w", err)
	}

	aimID := parsed.AimID
	if aimID == "" {
		aimID = defaultAimID
	}

	profileID := strings.TrimSuffix(path.Base(relpath), ProfilesDataKeySuffix)

	primary := false
	if parsed.Metadata.Primary != nil {
		primary = *parsed.Metadata.Primary
	}

	return DiscoveryCatalogItem{
		Name:            firstNonEmpty(profileID, parsed.ModelID),
		Version:         version,
		BaseImage:       baseImage,
		RelPath:         relpath,
		PrimaryExplicit: parsed.Metadata.Primary != nil,
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			AimId:               aimID,
			ModelId:             parsed.ModelID,
			ProfileId:           profileID,
			Engine:              parsed.Metadata.Engine,
			Metric:              parsed.Metadata.Metric,
			Precision:           parsed.Metadata.Precision,
			Type:                parsed.Metadata.Type,
			Primary:             primary,
			ManualSelectionOnly: parsed.Metadata.ManualSelectionOnly,
			AcceleratorModel:    acceleratorModel,
			AcceleratorType:     acceleratorType,
			AcceleratorCount:    acceleratorCount,
			EngineArgs:          engineArgsJSON,
			EngineEnv:           parsed.EnvVars,
			Image:               sourceImage,
			ModelSources:        deriveModelSources(parsed.ModelID),
			Features:            append([]string(nil), parsed.Metadata.Features...),
		},
	}, nil
}

func marshalEngineArgs(value map[string]any) (*apiextensionsv1.JSON, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}

func deriveModelSources(modelID string) []aimv1alpha1.AIMModelSource {
	if modelID == "" {
		return nil
	}
	return []aimv1alpha1.AIMModelSource{{
		ModelID:   modelID,
		SourceURI: "hf://" + modelID,
	}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
