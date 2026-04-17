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
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestFetchRuntimeConfig_DefaultNotFound(t *testing.T) {
	c := newFakeClient()
	result := FetchMergedRuntimeConfig(context.Background(), c, DefaultRuntimeConfigName, "test-ns")

	if result.HasError() {
		t.Errorf("expected no error for default config not found, got: %v", result.Error)
	}
	if result.Value != nil {
		t.Error("expected nil config when default not found")
	}
}

func TestFetchRuntimeConfig_NonDefaultNotFound(t *testing.T) {
	c := newFakeClient()
	result := FetchMergedRuntimeConfig(context.Background(), c, "custom-config", "test-ns")

	if !result.IsNotFound() {
		t.Error("expected NotFound error for non-default config")
	}
}

func TestFetchRuntimeConfig_ClusterOnly(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
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

	c := newFakeClient(clusterConfig)
	result := FetchMergedRuntimeConfig(context.Background(), c, "default", "test-ns")

	if result.HasError() {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.Value == nil {
		t.Fatal("expected config to be set")
	}
	if result.Value.Storage == nil || result.Value.Storage.PVCHeadroomPercent == nil || *result.Value.Storage.PVCHeadroomPercent != 10 {
		t.Error("expected cluster config values in merged config")
	}
}

func TestFetchRuntimeConfig_NamespaceOnly(t *testing.T) {
	namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "test-ns"},
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

	c := newFakeClient(namespaceConfig)
	result := FetchMergedRuntimeConfig(context.Background(), c, "default", "test-ns")

	if result.HasError() {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.Value == nil {
		t.Fatal("expected config to be set")
	}
	if result.Value.Storage == nil || result.Value.Storage.PVCHeadroomPercent == nil || *result.Value.Storage.PVCHeadroomPercent != 20 {
		t.Error("expected namespace config values in merged config")
	}
}

func TestFetchRuntimeConfig_MergeNamespaceOverridesCluster(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "test-ns"},
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

	c := newFakeClient(clusterConfig, namespaceConfig)
	result := FetchMergedRuntimeConfig(context.Background(), c, "default", "test-ns")

	if result.Value == nil {
		t.Fatal("expected config to be set")
	}
	if result.Value.Storage == nil {
		t.Fatal("expected Storage to be set")
	}
	// Namespace should override cluster
	if result.Value.Storage.PVCHeadroomPercent == nil || *result.Value.Storage.PVCHeadroomPercent != 20 {
		t.Errorf("expected PVCHeadroomPercent=20 (namespace), got %v", result.Value.Storage.PVCHeadroomPercent)
	}
	// Cluster value should be preserved where namespace didn't override
	if result.Value.Storage.DefaultStorageClassName == nil || *result.Value.Storage.DefaultStorageClassName != "cluster-sc" {
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
			result := mergeRuntimeConfigs(tt.priority, tt.base)
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
		{
			name: "migrate both deprecated fields",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{
				DefaultStorageClassName: "old-sc",
				PVCHeadroomPercent:      ptr.To(int32(15)),
			},
			validate: func(t *testing.T, config *aimv1alpha1.AIMRuntimeConfigCommon) {
				if config.Storage == nil {
					t.Fatal("expected Storage to be created")
				}
				if config.Storage.DefaultStorageClassName == nil || *config.Storage.DefaultStorageClassName != "old-sc" {
					t.Error("expected DefaultStorageClassName to be migrated")
				}
				if config.Storage.PVCHeadroomPercent == nil || *config.Storage.PVCHeadroomPercent != 15 {
					t.Error("expected PVCHeadroomPercent to be migrated")
				}
			},
		},
		{
			name: "empty string does not create Storage",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{
				DefaultStorageClassName: "",
			},
			validate: func(t *testing.T, config *aimv1alpha1.AIMRuntimeConfigCommon) {
				if config.Storage != nil {
					t.Error("expected Storage to remain nil for empty DefaultStorageClassName")
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

func TestFetchRuntimeConfig_RoutingMerge(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "test-ns"},
		Spec: aimv1alpha1.AIMRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						PathTemplate: ptr.To("/namespace/{.metadata.name}"),
					},
				},
			},
		},
	}

	c := newFakeClient(clusterConfig, namespaceConfig)
	result := FetchMergedRuntimeConfig(context.Background(), c, "default", "test-ns")

	if result.Value == nil || result.Value.Routing == nil {
		t.Fatal("expected merged routing config")
	}

	// Verify namespace PathTemplate overrides cluster
	if result.Value.Routing.PathTemplate == nil || *result.Value.Routing.PathTemplate != "/namespace/{.metadata.name}" {
		t.Error("namespace PathTemplate should override cluster")
	}

	// Verify cluster Enabled is preserved
	if result.Value.Routing.Enabled == nil || *result.Value.Routing.Enabled != true {
		t.Error("cluster Enabled should be preserved")
	}
}

func TestFetchRuntimeConfig_ModelConfig(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				Model: &aimv1alpha1.AIMModelConfig{
					AutoDiscovery: ptr.To(false),
				},
			},
		},
	}

	c := newFakeClient(clusterConfig)
	result := FetchMergedRuntimeConfig(context.Background(), c, "default", "test-ns")

	if result.Value == nil || result.Value.Model == nil {
		t.Fatal("expected merged model config")
	}

	if result.Value.Model.AutoDiscovery == nil || *result.Value.Model.AutoDiscovery != false {
		t.Error("expected AutoDiscovery=false from cluster config")
	}
}

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

	result := mergeRuntimeConfigs(priority, base)

	// When priority has nil Storage, base Storage should be preserved
	if result == nil || result.Storage == nil {
		t.Fatal("expected base Storage to be preserved")
	}
	if result.Storage.PVCHeadroomPercent == nil || *result.Storage.PVCHeadroomPercent != 20 {
		t.Error("expected base Storage values to be preserved when priority Storage is nil")
	}
}

func TestFetchRuntimeConfig_EmptyName(t *testing.T) {
	clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
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

	c := newFakeClient(clusterConfig)
	// Empty name should default to "default"
	result := FetchMergedRuntimeConfig(context.Background(), c, "", "test-ns")

	if result.HasError() {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.Value == nil {
		t.Fatal("expected config to be set")
	}
	if result.Value.Storage == nil || result.Value.Storage.PVCHeadroomPercent == nil || *result.Value.Storage.PVCHeadroomPercent != 10 {
		t.Error("expected default config values")
	}
}
