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

package utils

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestMergeEnvVars(t *testing.T) {
	tests := []struct {
		name          string
		defaults      []corev1.EnvVar
		overrides     []corev1.EnvVar
		jsonMergeKeys []string
		wantKeys      map[string]string // name -> value
	}{
		{
			name:     "empty input",
			defaults: nil,
			wantKeys: nil,
		},
		{
			name:      "both empty slices",
			defaults:  []corev1.EnvVar{},
			overrides: []corev1.EnvVar{},
			wantKeys:  nil,
		},
		{
			name: "defaults only",
			defaults: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
				{Name: "BAZ", Value: "qux"},
			},
			wantKeys: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:      "overrides only",
			defaults:  nil,
			overrides: []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
			wantKeys:  map[string]string{"FOO": "bar"},
		},
		{
			name:      "two slices no overlap",
			defaults:  []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
			overrides: []corev1.EnvVar{{Name: "BAZ", Value: "qux"}},
			wantKeys:  map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:      "overrides take precedence",
			defaults:  []corev1.EnvVar{{Name: "FOO", Value: "original"}},
			overrides: []corev1.EnvVar{{Name: "FOO", Value: "override"}},
			wantKeys:  map[string]string{"FOO": "override"},
		},
		{
			name: "mixed override and new",
			defaults: []corev1.EnvVar{
				{Name: "KEEP", Value: "kept"},
				{Name: "OVERRIDE", Value: "old"},
			},
			overrides: []corev1.EnvVar{
				{Name: "OVERRIDE", Value: "new"},
				{Name: "NEW", Value: "added"},
			},
			wantKeys: map[string]string{
				"KEEP":     "kept",
				"OVERRIDE": "new",
				"NEW":      "added",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeEnvVars(tt.defaults, tt.overrides, tt.jsonMergeKeys...)

			if tt.wantKeys == nil {
				if got != nil {
					t.Errorf("MergeEnvVars() = %v, want nil", got)
				}
				return
			}

			if len(got) != len(tt.wantKeys) {
				t.Errorf("MergeEnvVars() returned %d items, want %d", len(got), len(tt.wantKeys))
			}

			gotMap := make(map[string]string)
			for _, env := range got {
				gotMap[env.Name] = env.Value
			}

			for k, want := range tt.wantKeys {
				if gotVal, ok := gotMap[k]; !ok {
					t.Errorf("MergeEnvVars() missing key %q", k)
				} else if gotVal != want {
					t.Errorf("MergeEnvVars()[%q] = %q, want %q", k, gotVal, want)
				}
			}
		})
	}
}

func TestMergeEnvVars_JSONDeepMerge(t *testing.T) {
	tests := []struct {
		name          string
		defaults      []corev1.EnvVar
		overrides     []corev1.EnvVar
		jsonMergeKeys []string
		wantValue     string // expected value for AIM_ENGINE_ARGS
	}{
		{
			name: "JSON deep merge combines keys",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"max-model-len":4096}`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"kv-transfer-config":"value"}`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"kv-transfer-config":"value","max-model-len":4096}`,
		},
		{
			name: "JSON deep merge - override takes precedence for same key",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"key":"base-value"}`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"key":"override-value"}`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"key":"override-value"}`,
		},
		{
			name: "JSON deep merge - nested objects",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"outer":{"inner1":"a"}}`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"outer":{"inner2":"b"}}`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"outer":{"inner1":"a","inner2":"b"}}`,
		},
		{
			name: "without jsonMergeKeys - simple replace",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"max-model-len":4096}`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"kv-transfer-config":"value"}`},
			},
			jsonMergeKeys: nil,                              // no JSON merge
			wantValue:     `{"kv-transfer-config":"value"}`, // simple replace
		},
		{
			name: "invalid base JSON - use override",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `not-valid-json`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"valid":"json"}`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"valid":"json"}`,
		},
		{
			name: "invalid override JSON - use override as-is",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"valid":"json"}`},
			},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `not-valid-json`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `not-valid-json`,
		},
		{
			name:     "empty base - use override",
			defaults: []corev1.EnvVar{},
			overrides: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"key":"value"}`},
			},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"key":"value"}`,
		},
		{
			name: "empty override - use base",
			defaults: []corev1.EnvVar{
				{Name: "AIM_ENGINE_ARGS", Value: `{"key":"value"}`},
			},
			overrides:     []corev1.EnvVar{},
			jsonMergeKeys: []string{"AIM_ENGINE_ARGS"},
			wantValue:     `{"key":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeEnvVars(tt.defaults, tt.overrides, tt.jsonMergeKeys...)

			// Find AIM_ENGINE_ARGS in result
			var gotValue string
			for _, env := range got {
				if env.Name == "AIM_ENGINE_ARGS" {
					gotValue = env.Value
					break
				}
			}

			if gotValue != tt.wantValue {
				t.Errorf("MergeEnvVars()[AIM_ENGINE_ARGS] = %q, want %q", gotValue, tt.wantValue)
			}
		})
	}
}

func TestMergeJSONEnvVarValues(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		higher string
		want   string
	}{
		{
			name:   "empty base",
			base:   "",
			higher: `{"key":"value"}`,
			want:   `{"key":"value"}`,
		},
		{
			name:   "empty higher",
			base:   `{"key":"value"}`,
			higher: "",
			want:   `{"key":"value"}`,
		},
		{
			name:   "both empty",
			base:   "",
			higher: "",
			want:   "",
		},
		{
			name:   "merge different keys",
			base:   `{"a":"1"}`,
			higher: `{"b":"2"}`,
			want:   `{"a":"1","b":"2"}`,
		},
		{
			name:   "higher overrides same key",
			base:   `{"key":"base"}`,
			higher: `{"key":"higher"}`,
			want:   `{"key":"higher"}`,
		},
		{
			name:   "nested merge",
			base:   `{"outer":{"a":"1"}}`,
			higher: `{"outer":{"b":"2"}}`,
			want:   `{"outer":{"a":"1","b":"2"}}`,
		},
		{
			name:   "invalid base returns higher",
			base:   `not-json`,
			higher: `{"valid":"json"}`,
			want:   `{"valid":"json"}`,
		},
		{
			name:   "invalid higher returns higher",
			base:   `{"valid":"json"}`,
			higher: `not-json`,
			want:   `not-json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeJSONEnvVarValues(tt.base, tt.higher)
			if got != tt.want {
				t.Errorf("MergeJSONEnvVarValues(%q, %q) = %q, want %q", tt.base, tt.higher, got, tt.want)
			}
		})
	}
}

func TestDeepMergeMap(t *testing.T) {
	tests := []struct {
		name string
		dst  map[string]any
		src  map[string]any
		want map[string]any
	}{
		{
			name: "simple merge",
			dst:  map[string]any{"a": "1"},
			src:  map[string]any{"b": "2"},
			want: map[string]any{"a": "1", "b": "2"},
		},
		{
			name: "src overrides dst",
			dst:  map[string]any{"key": "dst"},
			src:  map[string]any{"key": "src"},
			want: map[string]any{"key": "src"},
		},
		{
			name: "nested maps merge",
			dst:  map[string]any{"outer": map[string]any{"a": "1"}},
			src:  map[string]any{"outer": map[string]any{"b": "2"}},
			want: map[string]any{"outer": map[string]any{"a": "1", "b": "2"}},
		},
		{
			name: "nested - src overrides nested key",
			dst:  map[string]any{"outer": map[string]any{"key": "dst"}},
			src:  map[string]any{"outer": map[string]any{"key": "src"}},
			want: map[string]any{"outer": map[string]any{"key": "src"}},
		},
		{
			name: "src replaces non-map with map",
			dst:  map[string]any{"key": "string"},
			src:  map[string]any{"key": map[string]any{"nested": "value"}},
			want: map[string]any{"key": map[string]any{"nested": "value"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DeepMergeMap(tt.dst, tt.src)
			// Compare by converting back to string
			if !mapsEqual(tt.dst, tt.want) {
				t.Errorf("DeepMergeMap() = %v, want %v", tt.dst, tt.want)
			}
		})
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		aMap, aIsMap := av.(map[string]any)
		bMap, bIsMap := bv.(map[string]any)
		if aIsMap && bIsMap {
			if !mapsEqual(aMap, bMap) {
				return false
			}
		} else if av != bv {
			return false
		}
	}
	return true
}

type testConfig struct {
	Name    string
	Count   int
	Enabled *bool
	Env     []corev1.EnvVar
}

func TestMergeConfigs(t *testing.T) {
	boolTrue := true
	boolFalse := false

	tests := []struct {
		name       string
		srcs       []testConfig
		wantName   string
		wantCount  int
		wantEnvMap map[string]string
	}{
		{
			name:       "empty sources",
			srcs:       nil,
			wantName:   "",
			wantCount:  0,
			wantEnvMap: nil,
		},
		{
			name: "single source",
			srcs: []testConfig{
				{
					Name:  "first",
					Count: 5,
					Env:   []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
				},
			},
			wantName:   "first",
			wantCount:  5,
			wantEnvMap: map[string]string{"FOO": "bar"},
		},
		{
			name: "scalar override",
			srcs: []testConfig{
				{Name: "first", Count: 5},
				{Name: "second", Count: 10},
			},
			wantName:  "second",
			wantCount: 10,
		},
		{
			name: "zero value does not override",
			srcs: []testConfig{
				{Name: "first", Count: 5},
				{Count: 10}, // Name is zero value
			},
			wantName:  "first", // zero value doesn't override with WithOverride
			wantCount: 10,
		},
		{
			name: "pointer field override",
			srcs: []testConfig{
				{Enabled: &boolTrue},
				{Enabled: &boolFalse},
			},
			wantName:  "",
			wantCount: 0,
		},
		{
			name: "env var key-based merge",
			srcs: []testConfig{
				{
					Env: []corev1.EnvVar{
						{Name: "CLUSTER", Value: "cluster-val"},
						{Name: "SHARED", Value: "cluster-shared"},
					},
				},
				{
					Env: []corev1.EnvVar{
						{Name: "NAMESPACE", Value: "ns-val"},
						{Name: "SHARED", Value: "ns-shared"},
					},
				},
			},
			wantEnvMap: map[string]string{
				"CLUSTER":   "cluster-val",
				"NAMESPACE": "ns-val",
				"SHARED":    "ns-shared", // later wins
			},
		},
		{
			name: "three layer config merge",
			srcs: []testConfig{
				{
					Name: "cluster",
					Env: []corev1.EnvVar{
						{Name: "TOKEN", Value: "cluster-token"},
					},
				},
				{
					Name: "namespace",
					Env: []corev1.EnvVar{
						{Name: "API_KEY", Value: "ns-key"},
					},
				},
				{
					Env: []corev1.EnvVar{
						{Name: "TOKEN", Value: "resource-token"},
					},
				},
			},
			wantName: "namespace",
			wantEnvMap: map[string]string{
				"TOKEN":   "resource-token", // resource overrides cluster
				"API_KEY": "ns-key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst testConfig
			err := MergeConfigs(&dst, tt.srcs...)
			if err != nil {
				t.Fatalf("MergeConfigs() error = %v", err)
			}

			if dst.Name != tt.wantName {
				t.Errorf("MergeConfigs() Name = %q, want %q", dst.Name, tt.wantName)
			}
			if dst.Count != tt.wantCount {
				t.Errorf("MergeConfigs() Count = %d, want %d", dst.Count, tt.wantCount)
			}

			if tt.wantEnvMap != nil {
				gotEnvMap := make(map[string]string)
				for _, env := range dst.Env {
					gotEnvMap[env.Name] = env.Value
				}

				if len(gotEnvMap) != len(tt.wantEnvMap) {
					t.Errorf("MergeConfigs() Env has %d items, want %d", len(gotEnvMap), len(tt.wantEnvMap))
				}

				for k, want := range tt.wantEnvMap {
					if got, ok := gotEnvMap[k]; !ok {
						t.Errorf("MergeConfigs() Env missing key %q", k)
					} else if got != want {
						t.Errorf("MergeConfigs() Env[%q] = %q, want %q", k, got, want)
					}
				}
			}
		})
	}
}
