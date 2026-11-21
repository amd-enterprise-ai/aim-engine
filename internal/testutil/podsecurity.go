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

package testutil

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	psaapi "k8s.io/pod-security-admission/api"
	"k8s.io/pod-security-admission/policy"
)

// RestrictedPodSecurityViolations evaluates the Pod spec with the Kubernetes
// Pod Security Admission checks at level "restricted", version latest, and
// returns the reason of each failed check. It returns nil when the Pod passes.
func RestrictedPodSecurityViolations(t testing.TB, spec corev1.PodSpec) []string {
	t.Helper()
	evaluator, err := policy.NewEvaluator(policy.DefaultChecks())
	if err != nil {
		t.Fatalf("create Pod Security evaluator: %v", err)
	}
	level := psaapi.LevelVersion{Level: psaapi.LevelRestricted, Version: psaapi.LatestVersion()}

	var violations []string
	for _, result := range evaluator.EvaluatePod(level, &metav1.ObjectMeta{}, &spec) {
		if !result.Allowed {
			violations = append(violations, result.ForbiddenReason+": "+result.ForbiddenDetail)
		}
	}
	return violations
}
