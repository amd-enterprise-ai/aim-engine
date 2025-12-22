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
		name     string
		slices   [][]corev1.EnvVar
		wantKeys map[string]string // name -> value
	}{
		{
			name:     "empty input",
			slices:   nil,
			wantKeys: nil,
		},
		{
			name:     "single empty slice",
			slices:   [][]corev1.EnvVar{{}},
			wantKeys: nil,
		},
		{
			name: "single slice",
			slices: [][]corev1.EnvVar{
				{
					{Name: "FOO", Value: "bar"},
					{Name: "BAZ", Value: "qux"},
				},
			},
			wantKeys: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "two slices no overlap",
			slices: [][]corev1.EnvVar{
				{{Name: "FOO", Value: "bar"}},
				{{Name: "BAZ", Value: "qux"}},
			},
			wantKeys: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "two slices with override",
			slices: [][]corev1.EnvVar{
				{{Name: "FOO", Value: "original"}},
				{{Name: "FOO", Value: "override"}},
			},
			wantKeys: map[string]string{"FOO": "override"},
		},
		{
			name: "three layer merge - cluster namespace resource",
			slices: [][]corev1.EnvVar{
				{ // cluster
					{Name: "CLUSTER_VAR", Value: "cluster"},
					{Name: "SHARED", Value: "cluster-value"},
				},
				{ // namespace
					{Name: "NS_VAR", Value: "namespace"},
					{Name: "SHARED", Value: "namespace-value"},
				},
				{ // resource
					{Name: "RESOURCE_VAR", Value: "resource"},
					{Name: "SHARED", Value: "resource-value"},
				},
			},
			wantKeys: map[string]string{
				"CLUSTER_VAR":  "cluster",
				"NS_VAR":       "namespace",
				"RESOURCE_VAR": "resource",
				"SHARED":       "resource-value", // last wins
			},
		},
		{
			name: "middle slice empty",
			slices: [][]corev1.EnvVar{
				{{Name: "FOO", Value: "first"}},
				{},
				{{Name: "BAR", Value: "third"}},
			},
			wantKeys: map[string]string{"FOO": "first", "BAR": "third"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeEnvVars(tt.slices...)

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
