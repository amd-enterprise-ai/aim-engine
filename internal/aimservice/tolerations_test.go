// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

package aimservice

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestInferenceTolerations(t *testing.T) {
	gpuTaint := corev1.Toleration{Key: "amd.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	poolTaint := corev1.Toleration{Key: "pool", Operator: corev1.TolerationOpEqual, Value: "inference", Effect: corev1.TaintEffectNoSchedule}
	withTolerations := func(tolerations []corev1.Toleration) *aimv1alpha1.AIMService {
		service := &aimv1alpha1.AIMService{}
		service.Spec.Tolerations = tolerations
		return service
	}
	runtimeConfig := &aimv1alpha1.AIMRuntimeConfigCommon{}
	runtimeConfig.Tolerations = []corev1.Toleration{gpuTaint}

	tests := []struct {
		name          string
		service       *aimv1alpha1.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		want          []corev1.Toleration
	}{
		{name: "none", service: withTolerations(nil)},
		{name: "runtime config", service: withTolerations(nil), runtimeConfig: runtimeConfig, want: []corev1.Toleration{gpuTaint}},
		{name: "service replaces runtime config", service: withTolerations([]corev1.Toleration{poolTaint}), runtimeConfig: runtimeConfig, want: []corev1.Toleration{poolTaint}},
		{name: "empty service list removes runtime config's", service: withTolerations([]corev1.Toleration{}), runtimeConfig: runtimeConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InferenceTolerations(tt.service, tt.runtimeConfig)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InferenceTolerations() = %v, want %v", got, tt.want)
			}
		})
	}

	got := InferenceTolerations(withTolerations(nil), runtimeConfig)
	got[0].Key = "changed"
	if runtimeConfig.Tolerations[0].Key != "amd.com/gpu" {
		t.Error("InferenceTolerations() returned the runtime config's slice instead of a copy")
	}
}
