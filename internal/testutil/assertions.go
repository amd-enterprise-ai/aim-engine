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

package testutil

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetCondition finds a condition by type, returns nil if not found
func GetCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// HasCondition checks if a condition exists with the given type and status
func HasCondition(conditions []metav1.Condition, condType string, status metav1.ConditionStatus) bool {
	cond := GetCondition(conditions, condType)
	return cond != nil && cond.Status == status
}

// AssertCondition fails the test if the condition doesn't exist or doesn't match expected values
func AssertCondition(t *testing.T, conditions []metav1.Condition, condType string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	cond := GetCondition(conditions, condType)
	if cond == nil {
		t.Fatalf("condition %q not found", condType)
	}
	if cond.Status != status {
		t.Errorf("condition %q: expected status %v, got %v", condType, status, cond.Status)
	}
	if reason != "" && cond.Reason != reason {
		t.Errorf("condition %q: expected reason %q, got %q", condType, reason, cond.Reason)
	}
}

// AssertConditionExists fails the test if the condition doesn't exist
func AssertConditionExists(t *testing.T, conditions []metav1.Condition, condType string) {
	t.Helper()
	if GetCondition(conditions, condType) == nil {
		t.Fatalf("condition %q not found", condType)
	}
}

// AssertConditionNotExists fails the test if the condition exists
func AssertConditionNotExists(t *testing.T, conditions []metav1.Condition, condType string) {
	t.Helper()
	if GetCondition(conditions, condType) != nil {
		t.Fatalf("condition %q should not exist", condType)
	}
}

// AssertConditionMessage fails the test if the condition message doesn't contain the expected substring
func AssertConditionMessage(t *testing.T, conditions []metav1.Condition, condType string, messageSubstring string) {
	t.Helper()
	cond := GetCondition(conditions, condType)
	if cond == nil {
		t.Fatalf("condition %q not found", condType)
	}
	if messageSubstring != "" && !contains(cond.Message, messageSubstring) {
		t.Errorf("condition %q: expected message to contain %q, got %q", condType, messageSubstring, cond.Message)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
