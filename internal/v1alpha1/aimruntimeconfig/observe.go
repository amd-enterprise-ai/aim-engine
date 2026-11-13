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

package aimruntimeconfig

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// DefaultRuntimeConfigName is an alias of constants.DefaultRuntimeConfigName,
// kept so callers already importing this package do not need a second import.
// There is one definition of the value.
const DefaultRuntimeConfigName = constants.DefaultRuntimeConfigName

// FetchMergedRuntimeConfig fetches and merges namespace and cluster-scoped runtime configs.
// Returns a FetchResult containing the merged config.
//
// Behavior:
//   - If both namespace and cluster configs exist, they are merged (namespace takes precedence)
//   - If only one exists, it is returned
//   - If neither exists and name is "default", returns nil config with no error (OK)
//   - If neither exists and name is not "default", returns NotFound error
func FetchMergedRuntimeConfig(ctx context.Context, c client.Client, name, namespace string) controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon] {
	if name == "" {
		name = DefaultRuntimeConfigName
	}

	// Fetch namespace-scoped config
	var nsConfig *aimv1alpha1.AIMRuntimeConfig
	if namespace != "" {
		nsResult := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: name, Namespace: namespace}, &aimv1alpha1.AIMRuntimeConfig{})
		if nsResult.HasError() && !nsResult.IsNotFound() {
			return controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Error: nsResult.Error}
		}
		if nsResult.OK() {
			nsConfig = nsResult.Value
		}
	}

	// Fetch cluster-scoped config
	var clusterConfig *aimv1alpha1.AIMClusterRuntimeConfig
	clusterResult := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: name}, &aimv1alpha1.AIMClusterRuntimeConfig{})
	if clusterResult.HasError() && !clusterResult.IsNotFound() {
		return controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Error: clusterResult.Error}
	}
	if clusterResult.OK() {
		clusterConfig = clusterResult.Value
	}

	// Both not found
	if nsConfig == nil && clusterConfig == nil {
		if name != DefaultRuntimeConfigName {
			// Non-default config not found - this is a user configuration error
			// (they referenced a config that doesn't exist), not a transient dependency
			return controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Error: controllerutils.NewMissingUpstreamDependencyError(
					"ConfigNotFound",
					"RuntimeConfig "+name+" not found",
					apierrors.NewNotFound(
						schema.GroupResource{
							Group:    aimv1alpha1.GroupVersion.Group,
							Resource: "aimruntimeconfigs",
						},
						name,
					),
				),
			}
		}
		// Default config not found is OK - return nil config
		return controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{}
	}

	// Extract and migrate configs
	var clusterCommon, nsCommon *aimv1alpha1.AIMRuntimeConfigCommon
	if clusterConfig != nil {
		clusterCommon = &clusterConfig.Spec.AIMRuntimeConfigCommon
		migrateDeprecatedStorageFields(clusterCommon)
	}
	if nsConfig != nil {
		nsCommon = &nsConfig.Spec.AIMRuntimeConfigCommon
		migrateDeprecatedStorageFields(nsCommon)
	}

	// Merge configs (namespace takes precedence over cluster)
	merged := mergeRuntimeConfigs(nsCommon, clusterCommon)

	return controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: merged}
}

// mergeRuntimeConfigs merges two AIMRuntimeConfigCommon structs, with the priority config
// taking precedence over the base config. Uses key-based merging for env vars.
//
// Parameters:
//   - priority: The config with higher priority (overrides base values)
//   - base: The config with lower priority (provides defaults)
func mergeRuntimeConfigs(priority, base *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMRuntimeConfigCommon {
	// If only priority exists, return it
	if priority != nil && base == nil {
		return priority
	}

	// If only base exists, return it
	if base != nil && priority == nil {
		return base
	}

	// If neither exists, return nil
	if base == nil {
		return nil
	}

	// Both exist - merge them with priority taking precedence
	priorityFallbacks := copyProfileGenerationFallbacks(priority)
	baseFallbacks := copyProfileGenerationFallbacks(base)
	priorityImageMappings := copyImageRegistryMappings(priority)
	baseImageMappings := copyImageRegistryMappings(base)
	merged := aimv1alpha1.AIMRuntimeConfigCommon{}
	// We can ignore the error as we control the input and their types
	_ = utils.MergeConfigs(&merged, *base, *priority)
	mergeProfileGenerationConfig(&merged, priorityFallbacks, baseFallbacks)
	mergeImageRegistryConfig(&merged, priorityImageMappings, baseImageMappings)

	// An S3 connection is a trust boundary, not a bag of independently
	// inheritable defaults. If the namespace config supplies one, use that
	// object in full instead of combining its endpoint, credentials, or CA with
	// the cluster connection.
	if priority.Artifact != nil && priority.Artifact.S3 != nil {
		if merged.Artifact == nil {
			merged.Artifact = &aimv1alpha1.AIMArtifactConfig{}
		}
		merged.Artifact.S3 = priority.Artifact.S3.DeepCopy()
	}

	return &merged
}

// mergeImageRegistryConfig applies list-map semantics to image registry
// mappings. Namespace mappings replace cluster mappings with the same From
// prefix and preserve unrelated cluster mappings.
func mergeImageRegistryConfig(
	merged *aimv1alpha1.AIMRuntimeConfigCommon,
	priorityMappings, baseMappings []aimv1alpha1.AIMImageRegistryMapping,
) {
	if len(priorityMappings) == 0 && len(baseMappings) == 0 {
		return
	}
	if merged.ImageRegistry == nil {
		merged.ImageRegistry = &aimv1alpha1.AIMImageRegistryConfig{}
	}

	byFrom := make(map[string]aimv1alpha1.AIMImageRegistryMapping, len(baseMappings)+len(priorityMappings))
	order := make([]string, 0, len(baseMappings)+len(priorityMappings))
	for _, mapping := range baseMappings {
		if _, exists := byFrom[mapping.From]; !exists {
			order = append(order, mapping.From)
		}
		byFrom[mapping.From] = mapping
	}
	for _, mapping := range priorityMappings {
		if _, exists := byFrom[mapping.From]; !exists {
			order = append(order, mapping.From)
		}
		byFrom[mapping.From] = mapping
	}
	mergedMappings := make([]aimv1alpha1.AIMImageRegistryMapping, 0, len(order))
	for _, from := range order {
		mergedMappings = append(mergedMappings, byFrom[from])
	}
	merged.ImageRegistry.Mappings = mergedMappings
}

func copyImageRegistryMappings(config *aimv1alpha1.AIMRuntimeConfigCommon) []aimv1alpha1.AIMImageRegistryMapping {
	if config == nil || config.ImageRegistry == nil {
		return nil
	}
	return append([]aimv1alpha1.AIMImageRegistryMapping(nil), config.ImageRegistry.Mappings...)
}

// mergeProfileGenerationConfig applies list-map semantics to profile
// generation fallbacks. Generic config merging replaces slices wholesale,
// while RuntimeConfig fallbacks need namespace entries to replace matching
// cluster entries by name and preserve unrelated cluster entries.
func mergeProfileGenerationConfig(
	merged *aimv1alpha1.AIMRuntimeConfigCommon,
	priorityFallbacks, baseFallbacks []aimv1alpha1.AIMProfileGenerationFallback,
) {
	if len(priorityFallbacks) == 0 && len(baseFallbacks) == 0 {
		return
	}
	if merged.Model == nil {
		merged.Model = &aimv1alpha1.AIMModelConfig{}
	}
	if merged.Model.ProfileGeneration == nil {
		merged.Model.ProfileGeneration = &aimv1alpha1.AIMProfileGenerationConfig{}
	}

	byName := make(map[string]aimv1alpha1.AIMProfileGenerationFallback, len(baseFallbacks)+len(priorityFallbacks))
	order := make([]string, 0, len(baseFallbacks)+len(priorityFallbacks))
	for _, fallback := range baseFallbacks {
		if _, exists := byName[fallback.Name]; !exists {
			order = append(order, fallback.Name)
		}
		byName[fallback.Name] = fallback
	}
	for _, fallback := range priorityFallbacks {
		if _, exists := byName[fallback.Name]; !exists {
			order = append(order, fallback.Name)
		}
		byName[fallback.Name] = fallback
	}
	mergedFallbacks := make([]aimv1alpha1.AIMProfileGenerationFallback, 0, len(order))
	for _, name := range order {
		mergedFallbacks = append(mergedFallbacks, byName[name])
	}
	merged.Model.ProfileGeneration.Fallbacks = mergedFallbacks
}

func copyProfileGenerationFallbacks(config *aimv1alpha1.AIMRuntimeConfigCommon) []aimv1alpha1.AIMProfileGenerationFallback {
	if config == nil || config.Model == nil || config.Model.ProfileGeneration == nil {
		return nil
	}
	return append([]aimv1alpha1.AIMProfileGenerationFallback(nil), config.Model.ProfileGeneration.Fallbacks...)
}

// migrateDeprecatedStorageFields migrates deprecated top-level storage fields to the new Storage struct.
// This ensures backward compatibility with existing runtimeConfig resources that use the old field names.
func migrateDeprecatedStorageFields(config *aimv1alpha1.AIMRuntimeConfigCommon) {
	// Migrate DefaultStorageClassName
	if config.DefaultStorageClassName != "" {
		if config.Storage == nil {
			config.Storage = &aimv1alpha1.AIMStorageConfig{}
		}
		if config.Storage.DefaultStorageClassName == nil {
			config.Storage.DefaultStorageClassName = ptr.To(config.DefaultStorageClassName)
		}
	}

	// Migrate PVCHeadroomPercent
	if config.PVCHeadroomPercent != nil {
		if config.Storage == nil {
			config.Storage = &aimv1alpha1.AIMStorageConfig{}
		}
		if config.Storage.PVCHeadroomPercent == nil {
			config.Storage.PVCHeadroomPercent = config.PVCHeadroomPercent
		}
	}
}

func GetRuntimeConfigHealth(config *aimv1alpha1.AIMRuntimeConfigCommon) controllerutils.ComponentHealth {
	if config != nil {
		return controllerutils.ComponentHealth{
			Reason: "ConfigFound",
		}
	}
	// No config found is OK for models - runtime config is optional
	return controllerutils.ComponentHealth{
		State:   constants.AIMStatusReady,
		Reason:  "DefaultConfigNotFound",
		Message: "Default config was not found",
	}
}

func GetLabelPropagationSettings(config *aimv1alpha1.AIMRuntimeConfigCommon) *controllerutils.LabelPropagationSettings {
	if config == nil || config.LabelPropagation == nil {
		return nil
	}

	return &controllerutils.LabelPropagationSettings{
		Enabled: config.LabelPropagation.Enabled,
		Match:   config.LabelPropagation.Match,
	}
}

func GetApplyOptions(config *aimv1alpha1.AIMRuntimeConfigCommon) controllerutils.ApplyOptions {
	return controllerutils.ApplyOptions{
		LabelPropagation: GetLabelPropagationSettings(config),
	}
}
