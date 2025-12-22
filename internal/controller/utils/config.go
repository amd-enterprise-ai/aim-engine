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

package controllerutils

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	DefaultRuntimeConfigName = "default"
)

// FetchMergedRuntimeConfig fetches and merges namespace and cluster-scoped runtime configs.
// Returns a FetchResult containing the merged config.
//
// Behavior:
//   - If both namespace and cluster configs exist, they are merged (namespace takes precedence)
//   - If only one exists, it is returned
//   - If neither exists and name is "default", returns nil config with no error (OK)
//   - If neither exists and name is not "default", returns NotFound error
func FetchMergedRuntimeConfig(ctx context.Context, c client.Client, name, namespace string) FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon] {
	if name == "" {
		name = DefaultRuntimeConfigName
	}

	// Fetch namespace-scoped config
	var nsConfig *aimv1alpha1.AIMRuntimeConfig
	if namespace != "" {
		nsResult := Fetch(ctx, c, client.ObjectKey{Name: name, Namespace: namespace}, &aimv1alpha1.AIMRuntimeConfig{})
		if nsResult.HasError() && !nsResult.IsNotFound() {
			return FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Error: nsResult.Error}
		}
		if nsResult.OK() {
			nsConfig = nsResult.Value
		}
	}

	// Fetch cluster-scoped config
	var clusterConfig *aimv1alpha1.AIMClusterRuntimeConfig
	clusterResult := Fetch(ctx, c, client.ObjectKey{Name: name}, &aimv1alpha1.AIMClusterRuntimeConfig{})
	if clusterResult.HasError() && !clusterResult.IsNotFound() {
		return FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Error: clusterResult.Error}
	}
	if clusterResult.OK() {
		clusterConfig = clusterResult.Value
	}

	// Both not found
	if nsConfig == nil && clusterConfig == nil {
		if name != DefaultRuntimeConfigName {
			// Non-default config not found - this is a user configuration error
			// (they referenced a config that doesn't exist), not a transient dependency
			return FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Error: NewMissingUpstreamDependencyError(
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
		return FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{}
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
	merged := MergeRuntimeConfigs(nsCommon, clusterCommon)

	return FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: merged}
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

// MergeRuntimeConfigs merges two AIMRuntimeConfigCommon structs, with the priority config
// taking precedence over the base config. Uses key-based merging for env vars.
//
// Parameters:
//   - priority: The config with higher priority (overrides base values)
//   - base: The config with lower priority (provides defaults)
func MergeRuntimeConfigs(priority, base *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMRuntimeConfigCommon {
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
	merged := aimv1alpha1.AIMRuntimeConfigCommon{}
	// We can ignore the error as we control the input and their types
	_ = utils.MergeConfigs(&merged, *base, *priority)

	return &merged
}

type RuntimeConfigRefProvider interface {
	GetRuntimeConfigRef() aimv1alpha1.RuntimeConfigRef
}
