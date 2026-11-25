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

package aimruntimeconfig

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestTolerations(t *testing.T) {
	t.Parallel()

	gpu := corev1.Toleration{Key: "amd.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{}
	config.Tolerations = []corev1.Toleration{gpu}

	if got := Tolerations(nil); got != nil {
		t.Fatalf("Tolerations(nil) = %v, want nil", got)
	}
	if got := Tolerations(&aimv1alpha1.AIMRuntimeConfigCommon{}); got != nil {
		t.Fatalf("Tolerations(unset) = %v, want nil", got)
	}

	got := Tolerations(config)
	if len(got) != 1 || got[0] != gpu {
		t.Fatalf("Tolerations() = %v, want [%v]", got, gpu)
	}
	got[0].Key = "changed"
	if config.Tolerations[0].Key != "amd.com/gpu" {
		t.Fatal("Tolerations() returned the config's own slice, want a copy")
	}
}
