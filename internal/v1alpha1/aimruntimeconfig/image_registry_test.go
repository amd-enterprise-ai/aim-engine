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
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestBaseImageRegistry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *aimv1alpha1.AIMRuntimeConfigCommon
		want   string
	}{
		{name: "no runtime config", want: ""},
		{name: "unset", config: &aimv1alpha1.AIMRuntimeConfigCommon{}, want: ""},
		{
			name:   "set",
			config: &aimv1alpha1.AIMRuntimeConfigCommon{BaseImageRegistry: "registry.example.com/amdenterpriseai"},
			want:   "registry.example.com/amdenterpriseai",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := BaseImageRegistry(tt.config); got != tt.want {
				t.Fatalf("BaseImageRegistry() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeRuntimeConfigs_BaseImageRegistryNamespacePrecedence(t *testing.T) {
	t.Parallel()

	cluster := &aimv1alpha1.AIMRuntimeConfigCommon{BaseImageRegistry: "registry.example.com/cluster"}
	tests := []struct {
		name      string
		namespace *aimv1alpha1.AIMRuntimeConfigCommon
		want      string
	}{
		{name: "namespace unset inherits the cluster's", namespace: &aimv1alpha1.AIMRuntimeConfigCommon{}, want: "registry.example.com/cluster"},
		{
			name:      "namespace value replaces the cluster's",
			namespace: &aimv1alpha1.AIMRuntimeConfigCommon{BaseImageRegistry: "registry.example.com/team-a"},
			want:      "registry.example.com/team-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			merged := mergeRuntimeConfigs(tt.namespace, cluster.DeepCopy())
			if got := merged.BaseImageRegistry; got != tt.want {
				t.Fatalf("merged BaseImageRegistry = %q, want %q", got, tt.want)
			}
		})
	}
}
