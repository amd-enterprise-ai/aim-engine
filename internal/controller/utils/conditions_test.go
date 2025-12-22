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

package controllerutils

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewConditionManager(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Success", Message: "Ready"},
		{Type: "Available", Status: metav1.ConditionFalse, Reason: "Pending", Message: "Waiting"},
	}

	cm := NewConditionManager(conditions)

	if len(cm.conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(cm.conditions))
	}

	// Verify conditions were copied correctly
	for i, cond := range conditions {
		if cm.conditions[i].Type != cond.Type {
			t.Errorf("expected type %s, got %s", cond.Type, cm.conditions[i].Type)
		}
		if cm.conditions[i].Status != cond.Status {
			t.Errorf("expected status %s, got %s", cond.Status, cm.conditions[i].Status)
		}
	}
}

func TestConditionManager_MarkTrue(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkTrue("Ready", "Success", "Everything is ready")

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", cond.Status)
	}
	if cond.Reason != "Success" {
		t.Errorf("expected reason 'Success', got %s", cond.Reason)
	}
	if cond.Message != "Everything is ready" {
		t.Errorf("unexpected message: %s", cond.Message)
	}
}

func TestConditionManager_MarkFalse(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkFalse("Ready", "Failed", "Something went wrong")

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %s", cond.Status)
	}
	if cond.Reason != "Failed" {
		t.Errorf("expected reason 'Failed', got %s", cond.Reason)
	}
}

func TestConditionManager_MarkUnknown(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkUnknown("Ready", "Progressing", "Working on it")

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	if cond.Status != metav1.ConditionUnknown {
		t.Errorf("expected status Unknown, got %s", cond.Status)
	}
}

func TestConditionManager_UpdatePreservesTransitionTime(t *testing.T) {
	oldTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	cm := NewConditionManager([]metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Success",
			Message:            "Old message",
			LastTransitionTime: oldTime,
		},
	})

	// Update only the message (same status and reason)
	cm.MarkTrue("Ready", "Success", "New message")

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	// LastTransitionTime should be preserved
	if !cond.LastTransitionTime.Equal(&oldTime) {
		t.Errorf("expected LastTransitionTime to be preserved, got %v, want %v",
			cond.LastTransitionTime, oldTime)
	}

	if cond.Message != "New message" {
		t.Errorf("expected message to be updated to 'New message', got %s", cond.Message)
	}
}

func TestConditionManager_UpdateChangesTransitionTime(t *testing.T) {
	oldTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	cm := NewConditionManager([]metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Success",
			Message:            "Old message",
			LastTransitionTime: oldTime,
		},
	})

	// Update status (transition)
	cm.MarkFalse("Ready", "Failed", "Something broke")

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	// LastTransitionTime should be updated
	if cond.LastTransitionTime.Equal(&oldTime) {
		t.Error("expected LastTransitionTime to be updated on status change")
	}

	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %s", cond.Status)
	}
}

func TestConditionManager_Delete(t *testing.T) {
	cm := NewConditionManager([]metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Success", Message: "Ready"},
		{Type: "Available", Status: metav1.ConditionTrue, Reason: "Success", Message: "Available"},
	})

	cm.Delete("Ready")

	if cm.Get("Ready") != nil {
		t.Error("expected Ready condition to be deleted")
	}

	if cm.Get("Available") == nil {
		t.Error("expected Available condition to still exist")
	}

	if len(cm.Conditions()) != 1 {
		t.Errorf("expected 1 condition, got %d", len(cm.Conditions()))
	}
}

func TestConditionManager_Get_NotFound(t *testing.T) {
	cm := NewConditionManager(nil)

	cond := cm.Get("NonExistent")
	if cond != nil {
		t.Error("expected nil for non-existent condition")
	}
}

func TestConditionManager_AllConditionsTrue(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		types      []string
		want       bool
	}{
		{
			name: "all true",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionTrue},
			},
			types: []string{"Ready", "Available"},
			want:  true,
		},
		{
			name: "one false",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionFalse},
			},
			types: []string{"Ready", "Available"},
			want:  false,
		},
		{
			name: "condition not found",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			types: []string{"Ready", "NonExistent"},
			want:  false,
		},
		{
			name:       "empty list",
			conditions: nil,
			types:      []string{},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConditionManager(tt.conditions)
			got := cm.AllConditionsTrue(tt.types...)
			if got != tt.want {
				t.Errorf("AllConditionsTrue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConditionManager_AnyConditionTrue(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		types      []string
		want       bool
	}{
		{
			name: "one true",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionFalse},
			},
			types: []string{"Ready", "Available"},
			want:  true,
		},
		{
			name: "all false",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse},
				{Type: "Available", Status: metav1.ConditionFalse},
			},
			types: []string{"Ready", "Available"},
			want:  false,
		},
		{
			name:       "empty list",
			conditions: nil,
			types:      []string{},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConditionManager(tt.conditions)
			got := cm.AnyConditionTrue(tt.types...)
			if got != tt.want {
				t.Errorf("AnyConditionTrue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConditionManager_AnyConditionFalse(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		types      []string
		want       bool
	}{
		{
			name: "one false",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionFalse},
			},
			types: []string{"Ready", "Available"},
			want:  true,
		},
		{
			name: "all true",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionTrue},
			},
			types: []string{"Ready", "Available"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConditionManager(tt.conditions)
			got := cm.AnyConditionFalse(tt.types...)
			if got != tt.want {
				t.Errorf("AnyConditionFalse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffConditionTransitions(t *testing.T) {
	oldConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady", Message: "Not ready"},
		{Type: "Available", Status: metav1.ConditionTrue, Reason: "Available", Message: "Available"},
		{Type: "ToBeDeleted", Status: metav1.ConditionTrue, Reason: "Exists", Message: "Exists"},
	}

	newConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", Message: "Ready now"},               // Status changed
		{Type: "Available", Status: metav1.ConditionTrue, Reason: "Available", Message: "Still available"}, // Message only
		{Type: "New", Status: metav1.ConditionTrue, Reason: "Created", Message: "New condition"},           // Added
		// ToBeDeleted removed
	}

	transitions := DiffConditionTransitions(oldConditions, newConditions)

	// Should have 2 transitions:
	// 1. Ready: status changed from False to True
	// 2. New: newly added
	// Available should not be included (message-only change)
	// ToBeDeleted should not be included (deletion doesn't count as transition)

	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d: %+v", len(transitions), transitions)
	}

	// Find Ready transition
	var readyTransition *ConditionTransition
	for i := range transitions {
		if transitions[i].New != nil && transitions[i].New.Type == ConditionTypeReady {
			readyTransition = &transitions[i]
			break
		}
	}
	if readyTransition == nil {
		t.Fatal("expected Ready transition")
	}
	if readyTransition.Old == nil {
		t.Error("expected Ready to have old value")
	}
	if readyTransition.Old.Status != metav1.ConditionFalse {
		t.Errorf("expected old Ready status to be False, got %s", readyTransition.Old.Status)
	}
	if readyTransition.New.Status != metav1.ConditionTrue {
		t.Errorf("expected new Ready status to be True, got %s", readyTransition.New.Status)
	}

	// Find New transition
	var newTransition *ConditionTransition
	for i := range transitions {
		if transitions[i].New != nil && transitions[i].New.Type == "New" {
			newTransition = &transitions[i]
			break
		}
	}
	if newTransition == nil {
		t.Fatal("expected New transition")
	}
	if newTransition.Old != nil {
		t.Error("expected New to have nil old value (new condition)")
	}
}

func TestDiffConditionTransitions_MessageOnlyChange(t *testing.T) {
	oldConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", Message: "Old message"},
	}

	newConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", Message: "New message"},
	}

	transitions := DiffConditionTransitions(oldConditions, newConditions)

	// Message-only change should not trigger a transition
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions for message-only change, got %d", len(transitions))
	}
}

func TestDiffConditionTransitions_ReasonChange(t *testing.T) {
	oldConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OldReason", Message: "Message"},
	}

	newConditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "NewReason", Message: "Message"},
	}

	transitions := DiffConditionTransitions(oldConditions, newConditions)

	// Reason change should trigger a transition
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition for reason change, got %d", len(transitions))
	}

	if transitions[0].Old.Reason != "OldReason" {
		t.Errorf("expected old reason 'OldReason', got %s", transitions[0].Old.Reason)
	}
	if transitions[0].New.Reason != "NewReason" {
		t.Errorf("expected new reason 'NewReason', got %s", transitions[0].New.Reason)
	}
}

func TestConditionManager_WithObservability(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkTrue("Ready", "Success", "All good", WithNormalEvent())

	cond := cm.Get("Ready")
	if cond == nil {
		t.Fatal("expected condition to exist")
	}

	// Verify observability config was set
	cfg := cm.ConfigFor("Ready")
	if cfg.eventMode != EventOnTransition {
		t.Errorf("expected eventMode EventOnTransition, got %v", cfg.eventMode)
	}
	if cfg.eventLevel != LevelNormal {
		t.Errorf("expected eventLevel Normal, got %v", cfg.eventLevel)
	}
}

func TestConditionManager_Conditions(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkTrue("Ready", "Success", "Ready")
	cm.MarkFalse("Available", "Pending", "Not available")

	conditions := cm.Conditions()

	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}

	// Verify we get metav1.Condition objects
	for _, cond := range conditions {
		if cond.Type == "" {
			t.Error("expected condition to have Type")
		}
	}
}

func TestConditionManager_ConfigFor(t *testing.T) {
	cm := NewConditionManager(nil)

	cm.MarkTrue("Ready", "Success", "Ready", WithWarningEvent(), WithInfoLog())

	cfg := cm.ConfigFor("Ready")

	if cfg.eventLevel != LevelWarning {
		t.Errorf("expected eventLevel Warning, got %v", cfg.eventLevel)
	}
	if cfg.logMode != LogOnTransition {
		t.Errorf("expected logMode OnTransition, got %v", cfg.logMode)
	}
	if cfg.logLevel != 0 {
		t.Errorf("expected logLevel 0 (info), got %d", cfg.logLevel)
	}
}

func TestConditionManager_ConfigFor_NotFound(t *testing.T) {
	cm := NewConditionManager(nil)

	cfg := cm.ConfigFor("NonExistent")

	// Should return default config
	if cfg.eventMode != EventNone {
		t.Errorf("expected default eventMode None, got %v", cfg.eventMode)
	}
	if cfg.logMode != LogNone {
		t.Errorf("expected default logMode None, got %v", cfg.logMode)
	}
}

func TestConditionManager_Set(t *testing.T) {
	// Test the Set() convenience method - it's used by processStateEngine
	cm := NewConditionManager(nil)

	// Test setting True
	cm.Set("Ready", metav1.ConditionTrue, "AllGood", "Everything is working", AsInfo())

	conds := cm.Conditions()
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}

	cond := conds[0]
	if cond.Type != "Ready" {
		t.Errorf("Type = %v, want Ready", cond.Type)
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("Status = %v, want True", cond.Status)
	}
	if cond.Reason != "AllGood" {
		t.Errorf("Reason = %v, want AllGood", cond.Reason)
	}
	if cond.Message != "Everything is working" {
		t.Errorf("Message = %v, want 'Everything is working'", cond.Message)
	}

	// Test setting False
	cm.Set("ConfigValid", metav1.ConditionFalse, "InvalidSpec", "Configuration is invalid", AsWarning())

	conds = cm.Conditions()
	if len(conds) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conds))
	}

	// Find ConfigValid condition
	var configCond *metav1.Condition
	for _, c := range conds {
		if c.Type == "ConfigValid" {
			configCond = &c
			break
		}
	}

	if configCond == nil {
		t.Fatal("ConfigValid condition not found")
	}

	if configCond.Status != metav1.ConditionFalse {
		t.Errorf("Status = %v, want False", configCond.Status)
	}
	if configCond.Reason != "InvalidSpec" {
		t.Errorf("Reason = %v, want InvalidSpec", configCond.Reason)
	}

	// Verify observability config was applied
	cfg := cm.ConfigFor("ConfigValid")
	if cfg.eventLevel != LevelWarning {
		t.Errorf("expected eventLevel Warning, got %v", cfg.eventLevel)
	}
}
