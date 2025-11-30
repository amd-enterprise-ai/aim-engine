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

	"dario.cat/mergo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	DefaultRuntimeConfigName = "default"
)

// ============================================================================
// FETCH
// ============================================================================

type RuntimeConfigFetchResult struct {
	ClusterConfig   *aimv1alpha1.AIMClusterRuntimeConfig
	NamespaceConfig *aimv1alpha1.AIMRuntimeConfig
	// ConfigName is the name of the requested config (used for NotFound detection)
	ConfigName string
}

// FetchRuntimeConfig fetches both namespace and cluster-scoped runtime configs.
// This is the entry point for runtime config resolution.
func FetchRuntimeConfig(ctx context.Context, c client.Client, name string, namespace string) (RuntimeConfigFetchResult, error) {
	result := RuntimeConfigFetchResult{
		ConfigName: name,
	}

	// Fetch namespace-scoped config if namespace provided
	if namespace != "" {
		namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{}
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, namespaceConfig); err != nil && !apierrors.IsNotFound(err) {
			return result, err
		} else if err == nil {
			result.NamespaceConfig = namespaceConfig
		}
	}

	// Fetch cluster-scoped config
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, clusterConfig); err != nil && !apierrors.IsNotFound(err) {
		return result, err
	} else if err == nil {
		result.ClusterConfig = clusterConfig
	}

	// Don't return error for NotFound - let Observe phase handle it
	// This ensures status gets updated even when config is not found
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type RuntimeConfigObservation struct {
	// MergedConfig is the result of merging cluster + namespace configs
	// with deprecated field migration applied
	MergedConfig *aimv1alpha1.AIMRuntimeConfigCommon

	// ConfigNotFound indicates whether the requested config was not found
	// Only true if a non-default config was requested and not found
	ConfigNotFound bool

	// Error captures any fetch/merge errors
	Error error
}

// ObserveRuntimeConfig observes and merges runtime configs.
// This migrates deprecated fields and merges namespace + cluster configs.
func ObserveRuntimeConfig(fetchResult RuntimeConfigFetchResult, configName string) RuntimeConfigObservation {
	obs := RuntimeConfigObservation{}

	// Check if config was not found
	if fetchResult.ClusterConfig == nil && fetchResult.NamespaceConfig == nil {
		if configName != DefaultRuntimeConfigName {
			// Non-default config not found - this is an error
			obs.ConfigNotFound = true
			obs.Error = apierrors.NewNotFound(
				schema.GroupResource{
					Group:    aimv1alpha1.GroupVersion.Group,
					Resource: "aimruntimeconfigs",
				},
				configName,
			)
		}
		// Default config not found is fine - MergedConfig will be nil
		return obs
	}

	var clusterCommon *aimv1alpha1.AIMRuntimeConfigCommon
	var namespaceCommon *aimv1alpha1.AIMRuntimeConfigCommon

	// Extract and migrate cluster config
	if fetchResult.ClusterConfig != nil {
		clusterCommon = &fetchResult.ClusterConfig.Spec.AIMRuntimeConfigCommon
		migrateDeprecatedStorageFields(clusterCommon)
	}

	// Extract and migrate namespace config
	if fetchResult.NamespaceConfig != nil {
		namespaceCommon = &fetchResult.NamespaceConfig.Spec.AIMRuntimeConfigCommon
		migrateDeprecatedStorageFields(namespaceCommon)
	}

	// Merge configs (namespace takes precedence over cluster)
	obs.MergedConfig = MergeRuntimeConfigs(namespaceCommon, clusterCommon)

	return obs
}

// ============================================================================
// PROJECT
// ============================================================================

func ProjectRuntimeConfigObservation(
	cm *controllerutils.ConditionManager,
	sh *controllerutils.StatusHelper,
	observation RuntimeConfigObservation,
) bool {
	if err := observation.Error; err != nil {
		if observation.ConfigNotFound {
			// Non-default config not found - this is a fatal error
			cm.Set(
				aimv1alpha1.AIMModelConditionRuntimeResolved,
				metav1.ConditionFalse,
				"ConfigNotFound",
				err.Error(),
				controllerutils.LevelWarning,
			)
			sh.Failed("ConfigNotFound", err.Error())
			return true // Fatal - stop reconciliation
		}
		// Other error (e.g., API error)
		cm.Set(
			aimv1alpha1.AIMModelConditionRuntimeResolved,
			metav1.ConditionFalse,
			"Error",
			err.Error(),
			controllerutils.LevelWarning,
		)
		sh.Degraded("RuntimeConfigError", err.Error())
		return true // Stop reconciliation
	}

	// Success - differentiate between config found vs using defaults
	if observation.MergedConfig == nil {
		// Default config not found, using system defaults
		cm.Set(
			aimv1alpha1.AIMModelConditionRuntimeResolved,
			metav1.ConditionTrue,
			"UsingDefaults",
			"Runtime config 'default' not found, using system defaults",
			controllerutils.LevelNone,
		)
	} else {
		// Config found and merged
		cm.Set(
			aimv1alpha1.AIMModelConditionRuntimeResolved,
			metav1.ConditionTrue,
			"Resolved",
			"Runtime config resolved successfully",
			controllerutils.LevelNone,
		)
	}
	return false // Continue reconciliation
}

// ============================================================================
// MERGE
// ============================================================================

// MergeRuntimeConfigs merges two AIMRuntimeConfigCommon structs, with the priority config
// taking precedence over the base config. Each field is merged individually, with priority
// values overriding base values when both are present. Note that arrays are replaced entirely,
// no item-level merging or additions are performed.
//
// If only one config is non-nil, it is returned directly.
// If both are nil, nil is returned.
//
// Parameters:
//   - priority: The config with higher priority (overrides base values)
//   - base: The config with lower priority (provides defaults)
func MergeRuntimeConfigs(priority *aimv1alpha1.AIMRuntimeConfigCommon, base *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMRuntimeConfigCommon {
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
	merged := *base

	// Merge priority config into base config, with priority values overriding
	// mergo.WithOverride ensures priority values take precedence.
	// We can ignore the error as we control the input and their types.
	_ = mergo.Merge(&merged, *priority, mergo.WithOverride)

	return &merged
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
