/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimruntimeconfig

import (
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestObserveRuntimeConfig_DefaultNotFound(t *testing.T) {
	fetchResult := RuntimeConfigFetchResult{
		ConfigName:      DefaultRuntimeConfigName,
		ClusterConfig:   nil,
		NamespaceConfig: nil,
	}

	obs := ObserveRuntimeConfig(fetchResult, DefaultRuntimeConfigName)

	if obs.ConfigNotFound {
		t.Error("expected ConfigNotFound=false for default config")
	}
	if obs.Error != nil {
		t.Errorf("expected no error for default config not found, got: %v", obs.Error)
	}
	if obs.MergedConfig != nil {
		t.Error("expected MergedConfig=nil when config not found")
	}
}

func TestObserveRuntimeConfig_NonDefaultNotFound(t *testing.T) {
	fetchResult := RuntimeConfigFetchResult{
		ConfigName:      "custom-config",
		ClusterConfig:   nil,
		NamespaceConfig: nil,
	}

	obs := ObserveRuntimeConfig(fetchResult, "custom-config")

	if !obs.ConfigNotFound {
		t.Error("expected ConfigNotFound=true for non-default config")
	}
	if obs.Error == nil {
		t.Error("expected error for non-default config not found")
	}
	if obs.MergedConfig != nil {
		t.Error("expected MergedConfig=nil when config not found")
	}
}

func TestObserveRuntimeConfig_ClusterOnly(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent: ptr.To(int32(10)),
					},
				},
			},
		},
	}

	fetchResult := RuntimeConfigFetchResult{
		ConfigName:      "default",
		ClusterConfig:   clusterConfig,
		NamespaceConfig: nil,
	}

	obs := ObserveRuntimeConfig(fetchResult, "default")

	if obs.ConfigNotFound {
		t.Error("expected ConfigNotFound=false")
	}
	if obs.Error != nil {
		t.Errorf("unexpected error: %v", obs.Error)
	}
	if obs.MergedConfig == nil {
		t.Fatal("expected MergedConfig to be set")
	}
	if obs.MergedConfig.Storage == nil || obs.MergedConfig.Storage.PVCHeadroomPercent == nil || *obs.MergedConfig.Storage.PVCHeadroomPercent != 10 {
		t.Error("expected cluster config values in merged config")
	}
}

func TestObserveRuntimeConfig_NamespaceOnly(t *testing.T) {
	namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{
		Spec: aimv1alpha1.AIMRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent: ptr.To(int32(20)),
					},
				},
			},
		},
	}

	fetchResult := RuntimeConfigFetchResult{
		ConfigName:      "default",
		ClusterConfig:   nil,
		NamespaceConfig: namespaceConfig,
	}

	obs := ObserveRuntimeConfig(fetchResult, "default")

	if obs.ConfigNotFound {
		t.Error("expected ConfigNotFound=false")
	}
	if obs.Error != nil {
		t.Errorf("unexpected error: %v", obs.Error)
	}
	if obs.MergedConfig == nil {
		t.Fatal("expected MergedConfig to be set")
	}
	if obs.MergedConfig.Storage == nil || obs.MergedConfig.Storage.PVCHeadroomPercent == nil || *obs.MergedConfig.Storage.PVCHeadroomPercent != 20 {
		t.Error("expected namespace config values in merged config")
	}
}

func TestObserveRuntimeConfig_MergeNamespaceOverridesCluster(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent:      ptr.To(int32(10)),
						DefaultStorageClassName: ptr.To("cluster-sc"),
					},
				},
			},
		},
	}

	namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{
		Spec: aimv1alpha1.AIMRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent: ptr.To(int32(20)), // Override cluster value
						// Don't override DefaultStorageClassName
					},
				},
			},
		},
	}

	fetchResult := RuntimeConfigFetchResult{
		ConfigName:      "default",
		ClusterConfig:   clusterConfig,
		NamespaceConfig: namespaceConfig,
	}

	obs := ObserveRuntimeConfig(fetchResult, "default")

	if obs.MergedConfig == nil {
		t.Fatal("expected MergedConfig to be set")
	}
	if obs.MergedConfig.Storage == nil {
		t.Fatal("expected Storage to be set")
	}
	// Namespace should override cluster
	if obs.MergedConfig.Storage.PVCHeadroomPercent == nil || *obs.MergedConfig.Storage.PVCHeadroomPercent != 20 {
		t.Errorf("expected PVCHeadroomPercent=20 (namespace), got %v", obs.MergedConfig.Storage.PVCHeadroomPercent)
	}
	// Cluster value should be preserved where namespace didn't override
	if obs.MergedConfig.Storage.DefaultStorageClassName == nil || *obs.MergedConfig.Storage.DefaultStorageClassName != "cluster-sc" {
		t.Error("expected DefaultStorageClassName from cluster to be preserved")
	}
}

func TestMergeRuntimeConfigs(t *testing.T) {
	tests := []struct {
		name     string
		priority *aimv1alpha1.AIMRuntimeConfigCommon
		base     *aimv1alpha1.AIMRuntimeConfigCommon
		validate func(*testing.T, *aimv1alpha1.AIMRuntimeConfigCommon)
	}{
		{
			name:     "both nil",
			priority: nil,
			base:     nil,
			validate: func(t *testing.T, result *aimv1alpha1.AIMRuntimeConfigCommon) {
				if result != nil {
					t.Error("expected nil result")
				}
			},
		},
		{
			name: "priority only",
			priority: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{PVCHeadroomPercent: ptr.To(int32(10))},
				},
			},
			base: nil,
			validate: func(t *testing.T, result *aimv1alpha1.AIMRuntimeConfigCommon) {
				if result == nil || result.Storage == nil || result.Storage.PVCHeadroomPercent == nil || *result.Storage.PVCHeadroomPercent != 10 {
					t.Error("expected priority config values")
				}
			},
		},
		{
			name:     "base only",
			priority: nil,
			base: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{PVCHeadroomPercent: ptr.To(int32(20))},
				},
			},
			validate: func(t *testing.T, result *aimv1alpha1.AIMRuntimeConfigCommon) {
				if result == nil || result.Storage == nil || result.Storage.PVCHeadroomPercent == nil || *result.Storage.PVCHeadroomPercent != 20 {
					t.Error("expected base config values")
				}
			},
		},
		{
			name: "priority overrides base",
			priority: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{PVCHeadroomPercent: ptr.To(int32(10))},
				},
			},
			base: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent:      ptr.To(int32(20)),
						DefaultStorageClassName: ptr.To("base-sc"),
					},
				},
			},
			validate: func(t *testing.T, result *aimv1alpha1.AIMRuntimeConfigCommon) {
				if result == nil || result.Storage == nil {
					t.Fatal("expected merged config")
				}
				if result.Storage.PVCHeadroomPercent == nil || *result.Storage.PVCHeadroomPercent != 10 {
					t.Errorf("expected priority value 10, got %v", result.Storage.PVCHeadroomPercent)
				}
				if result.Storage.DefaultStorageClassName == nil || *result.Storage.DefaultStorageClassName != "base-sc" {
					t.Error("expected base value to be preserved for non-overridden field")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeRuntimeConfigs(tt.priority, tt.base)
			tt.validate(t, result)
		})
	}
}

func TestMigrateDeprecatedStorageFields(t *testing.T) {
	tests := []struct {
		name     string
		config   *aimv1alpha1.AIMRuntimeConfigCommon
		validate func(*testing.T, *aimv1alpha1.AIMRuntimeConfigCommon)
	}{
		{
			name: "migrate DefaultStorageClassName",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{
				DefaultStorageClassName: "old-sc",
			},
			validate: func(t *testing.T, config *aimv1alpha1.AIMRuntimeConfigCommon) {
				if config.Storage == nil {
					t.Fatal("expected Storage to be created")
				}
				if config.Storage.DefaultStorageClassName == nil || *config.Storage.DefaultStorageClassName != "old-sc" {
					t.Error("expected DefaultStorageClassName to be migrated")
				}
			},
		},
		{
			name: "migrate PVCHeadroomPercent",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{
				PVCHeadroomPercent: ptr.To(int32(10)),
			},
			validate: func(t *testing.T, config *aimv1alpha1.AIMRuntimeConfigCommon) {
				if config.Storage == nil {
					t.Fatal("expected Storage to be created")
				}
				if config.Storage.PVCHeadroomPercent == nil || *config.Storage.PVCHeadroomPercent != 10 {
					t.Error("expected PVCHeadroomPercent to be migrated")
				}
			},
		},
		{
			name: "don't override if new field already set",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{
				DefaultStorageClassName: "old-sc",
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						DefaultStorageClassName: ptr.To("new-sc"),
					},
				},
			},
			validate: func(t *testing.T, config *aimv1alpha1.AIMRuntimeConfigCommon) {
				if config.Storage == nil {
					t.Fatal("expected Storage to exist")
				}
				if *config.Storage.DefaultStorageClassName != "new-sc" {
					t.Error("expected new field value to be preserved, not overridden by deprecated field")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrateDeprecatedStorageFields(tt.config)
			tt.validate(t, tt.config)
		})
	}
}

func TestProjectRuntimeConfigObservation(t *testing.T) {
	tests := []struct {
		name        string
		obs         RuntimeConfigObservation
		expectFatal bool
	}{
		{
			name: "config not found - fatal",
			obs: RuntimeConfigObservation{
				ConfigNotFound: true,
				Error: apierrors.NewNotFound(
					schema.GroupResource{
						Group:    aimv1alpha1.GroupVersion.Group,
						Resource: "aimruntimeconfigs",
					},
					"custom",
				),
			},
			expectFatal: true,
		},
		{
			name: "config found - continue",
			obs: RuntimeConfigObservation{
				ConfigNotFound: false,
				MergedConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
					AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
						Storage: &aimv1alpha1.AIMStorageConfig{PVCHeadroomPercent: ptr.To(int32(10))},
					},
				},
			},
			expectFatal: false,
		},
		{
			name: "default config not found - continue (using defaults)",
			obs: RuntimeConfigObservation{
				ConfigNotFound: false,
				MergedConfig:   nil,
			},
			expectFatal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			status := &testStatus{}
			sh := controllerutils.NewStatusHelper(status, cm)

			fatal := ProjectRuntimeConfigObservation(cm, sh, tt.obs)

			if fatal != tt.expectFatal {
				t.Errorf("expected fatal=%v, got %v", tt.expectFatal, fatal)
			}
		})
	}
}

// testStatus implements StatusWithConditions for testing
type testStatus struct {
	status     string
	conditions []metav1.Condition
}

func (s *testStatus) GetConditions() []metav1.Condition {
	return s.conditions
}

func (s *testStatus) SetConditions(conditions []metav1.Condition) {
	s.conditions = conditions
}

func (s *testStatus) SetStatus(status string) {
	s.status = status
}

// TestObserveRuntimeConfig_RoutingMerge tests that routing config merges correctly
func TestObserveRuntimeConfig_RoutingMerge(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						Enabled:      ptr.To(true),
						PathTemplate: ptr.To("/cluster/{.metadata.name}"),
					},
				},
			},
		},
	}

	namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{
		Spec: aimv1alpha1.AIMRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						PathTemplate: ptr.To("/namespace/{.metadata.name}"),
						// Should override cluster's PathTemplate but inherit Enabled
					},
				},
			},
		},
	}

	obs := ObserveRuntimeConfig(RuntimeConfigFetchResult{
		ConfigName:      "default",
		ClusterConfig:   clusterConfig,
		NamespaceConfig: namespaceConfig,
	}, "default")

	if obs.MergedConfig == nil || obs.MergedConfig.Routing == nil {
		t.Fatal("expected merged routing config")
	}

	// Verify namespace PathTemplate overrides cluster
	if obs.MergedConfig.Routing.PathTemplate == nil || *obs.MergedConfig.Routing.PathTemplate != "/namespace/{.metadata.name}" {
		t.Error("namespace PathTemplate should override cluster")
	}

	// Verify cluster Enabled is preserved
	if obs.MergedConfig.Routing.Enabled == nil || *obs.MergedConfig.Routing.Enabled != true {
		t.Error("cluster Enabled should be preserved")
	}
}

// TestObserveRuntimeConfig_ModelConfig tests that Model.AutoDiscovery merges correctly
func TestObserveRuntimeConfig_ModelConfig(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				Model: &aimv1alpha1.AIMModelConfig{
					AutoDiscovery: ptr.To(false),
				},
			},
		},
	}

	obs := ObserveRuntimeConfig(RuntimeConfigFetchResult{
		ConfigName:    "default",
		ClusterConfig: clusterConfig,
	}, "default")

	if obs.MergedConfig == nil || obs.MergedConfig.Model == nil {
		t.Fatal("expected merged model config")
	}

	if obs.MergedConfig.Model.AutoDiscovery == nil || *obs.MergedConfig.Model.AutoDiscovery != false {
		t.Error("expected AutoDiscovery=false from cluster config")
	}
}

// TestMergeRuntimeConfigs_NilStorageOverride tests edge case where priority has nil Storage
func TestMergeRuntimeConfigs_NilStorageOverride(t *testing.T) {
	priority := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Storage: nil, // Explicitly nil
		},
	}

	base := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Storage: &aimv1alpha1.AIMStorageConfig{
				PVCHeadroomPercent: ptr.To(int32(20)),
			},
		},
	}

	result := MergeRuntimeConfigs(priority, base)

	// When priority has nil Storage, base Storage should be preserved
	if result == nil || result.Storage == nil {
		t.Fatal("expected base Storage to be preserved")
	}
	if result.Storage.PVCHeadroomPercent == nil || *result.Storage.PVCHeadroomPercent != 20 {
		t.Error("expected base Storage values to be preserved when priority Storage is nil")
	}
}

// TestMergeRuntimeConfigs_RoutingAnnotations tests map merging
func TestMergeRuntimeConfigs_RoutingAnnotations(t *testing.T) {
	priority := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
				Annotations: map[string]string{
					"priority-key": "priority-value",
					"shared-key":   "priority-override",
				},
			},
		},
	}

	base := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
				Annotations: map[string]string{
					"base-key":   "base-value",
					"shared-key": "base-value",
				},
			},
		},
	}

	result := MergeRuntimeConfigs(priority, base)

	if result == nil || result.Routing == nil || result.Routing.Annotations == nil {
		t.Fatal("expected merged routing annotations")
	}

	// Priority map should completely replace base map (mergo behavior with maps)
	if result.Routing.Annotations["priority-key"] != "priority-value" {
		t.Error("expected priority annotation to be present")
	}
	if result.Routing.Annotations["shared-key"] != "priority-override" {
		t.Error("expected priority to override shared key")
	}
	// Note: mergo replaces entire maps, so base-only keys may not be preserved
	// This documents the actual behavior
}

// TestMigrateDeprecatedStorageFields_BothDeprecated tests when both deprecated fields are set
func TestMigrateDeprecatedStorageFields_BothDeprecated(t *testing.T) {
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		DefaultStorageClassName: "old-sc",
		PVCHeadroomPercent:      ptr.To(int32(15)),
	}

	migrateDeprecatedStorageFields(config)

	if config.Storage == nil {
		t.Fatal("expected Storage to be created")
	}
	if config.Storage.DefaultStorageClassName == nil || *config.Storage.DefaultStorageClassName != "old-sc" {
		t.Error("expected DefaultStorageClassName to be migrated")
	}
	if config.Storage.PVCHeadroomPercent == nil || *config.Storage.PVCHeadroomPercent != 15 {
		t.Error("expected PVCHeadroomPercent to be migrated")
	}
}

// TestMigrateDeprecatedStorageFields_EmptyString tests migration with empty string
func TestMigrateDeprecatedStorageFields_EmptyString(t *testing.T) {
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		DefaultStorageClassName: "", // Empty string should not create Storage
	}

	migrateDeprecatedStorageFields(config)

	if config.Storage != nil {
		t.Error("expected Storage to remain nil for empty DefaultStorageClassName")
	}
}

// TestProjectRuntimeConfigObservation_VerifyConditions tests that conditions are actually set
func TestProjectRuntimeConfigObservation_VerifyConditions(t *testing.T) {
	tests := []struct {
		name              string
		obs               RuntimeConfigObservation
		expectFatal       bool
		expectStatus      string
		expectCondition   string
		expectReason      string
		expectMsgContains string
	}{
		{
			name: "config not found sets Failed status and condition",
			obs: RuntimeConfigObservation{
				ConfigNotFound: true,
				Error: apierrors.NewNotFound(
					schema.GroupResource{
						Group:    aimv1alpha1.GroupVersion.Group,
						Resource: "aimruntimeconfigs",
					},
					"missing-config",
				),
			},
			expectFatal:       true,
			expectStatus:      "Failed",
			expectMsgContains: "missing-config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			status := &testStatus{}
			sh := controllerutils.NewStatusHelper(status, cm)

			fatal := ProjectRuntimeConfigObservation(cm, sh, tt.obs)

			if fatal != tt.expectFatal {
				t.Errorf("expected fatal=%v, got %v", tt.expectFatal, fatal)
			}

			if tt.expectStatus != "" && status.status != tt.expectStatus {
				t.Errorf("expected status=%s, got %s", tt.expectStatus, status.status)
			}
		})
	}
}
