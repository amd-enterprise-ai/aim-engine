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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestIsGPUKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"amd.com/gpu", true},
		{"amd.com/gpu.device-id", true},
		{"beta.amd.com/gpu.device-id", true},
		{"feature.node.kubernetes.io/aim-accelerator.MI300X", true},
		{"feature.node.kubernetes.io/aim-accelerator.EPYC_9965", true},
		{"kubernetes.io/hostname", false},
		{"nvidia.com/gpu", false},
		{"node.kubernetes.io/instance-type", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := isGPUKey(tt.key); got != tt.want {
				t.Errorf("isGPUKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestLabelsContainGPUChanges(t *testing.T) {
	tests := []struct {
		name      string
		oldLabels map[string]string
		newLabels map[string]string
		want      bool
	}{
		{
			name:      "no GPU labels, no change",
			oldLabels: map[string]string{"kubernetes.io/hostname": "node-1"},
			newLabels: map[string]string{"kubernetes.io/hostname": "node-1"},
			want:      false,
		},
		{
			name:      "amd GPU label added",
			oldLabels: map[string]string{},
			newLabels: map[string]string{"amd.com/gpu.device-id": "74a1"},
			want:      true,
		},
		{
			name:      "accelerator label added",
			oldLabels: map[string]string{},
			newLabels: map[string]string{"feature.node.kubernetes.io/aim-accelerator.MI300X": "4"},
			want:      true,
		},
		{
			name:      "accelerator label replaced",
			oldLabels: map[string]string{"feature.node.kubernetes.io/aim-accelerator.MI300X": "4"},
			newLabels: map[string]string{"feature.node.kubernetes.io/aim-accelerator.MI325X": "8"},
			want:      true,
		},
		{
			name:      "accelerator label removed",
			oldLabels: map[string]string{"feature.node.kubernetes.io/aim-accelerator.MI300X": "4"},
			newLabels: map[string]string{},
			want:      true,
		},
		{
			name: "non-GPU label changed, GPU label unchanged",
			oldLabels: map[string]string{
				"kubernetes.io/hostname":                            "node-1",
				"feature.node.kubernetes.io/aim-accelerator.MI300X": "4",
			},
			newLabels: map[string]string{
				"kubernetes.io/hostname":                            "node-2",
				"feature.node.kubernetes.io/aim-accelerator.MI300X": "4",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelsContainGPUChanges(tt.oldLabels, tt.newLabels); got != tt.want {
				t.Errorf("labelsContainGPUChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeGPUChangePredicate_UpdateAcceleratorLabels(t *testing.T) {
	pred := NodeGPUChangePredicate()

	oldNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "gpu-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("64"),
			},
		},
	}

	newNode := oldNode.DeepCopy()
	newNode.Labels["feature.node.kubernetes.io/aim-accelerator.MI300X"] = "4"

	got := pred.Update(event.UpdateEvent{
		ObjectOld: oldNode,
		ObjectNew: newNode,
	})
	if !got {
		t.Error("expected predicate to return true when accelerator label is added")
	}
}
