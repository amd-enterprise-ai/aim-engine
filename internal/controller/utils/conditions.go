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

package controllerutils

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

type LevelCondition struct {
	metav1.Condition
	Level EventLevel
}

// ConditionManager wraps a slice of metav1.Condition and provides helpers.
type ConditionManager struct {
	now        func() time.Time
	conditions []LevelCondition
}

func NewConditionManager(existing []metav1.Condition) *ConditionManager {
	conditions := make([]LevelCondition, len(existing))
	for i := range existing {
		conditions[i] = LevelCondition{existing[i], LevelNone}
	}
	return &ConditionManager{
		now:        time.Now,
		conditions: conditions,
	}
}

func (m *ConditionManager) Conditions() []metav1.Condition {
	conditions := make([]metav1.Condition, len(m.conditions))
	for i := range m.conditions {
		conditions[i] = m.conditions[i].Condition
	}
	return conditions
}

func (m *ConditionManager) Set(conditionType string, status metav1.ConditionStatus, reason, message string, level EventLevel) {
	m.SetCondition(metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	}, level)
}

// SetCondition sets or updates a condition by type.
func (m *ConditionManager) SetCondition(cond metav1.Condition, level EventLevel) {
	cond.LastTransitionTime = metav1.NewTime(m.now())

	idx := indexOfCondition(m.conditions, cond.Type)
	if idx == -1 {
		m.conditions = append(m.conditions, LevelCondition{
			Condition: cond,
			Level:     level,
		})
		return
	}

	existing := m.conditions[idx]
	// If status and reason are unchanged, preserve LastTransitionTime.
	// Message changes are informational and don't constitute a state transition.
	if existing.Status == cond.Status && existing.Reason == cond.Reason {
		cond.LastTransitionTime = existing.LastTransitionTime
	}

	m.conditions[idx].Condition = cond
	m.conditions[idx].Level = level
}

func (m *ConditionManager) MarkTrue(condType, reason, message string, level EventLevel) {
	m.SetCondition(metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	}, level)
}

func (m *ConditionManager) MarkFalse(condType, reason, message string, level EventLevel) {
	m.SetCondition(metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}, level)
}

func (m *ConditionManager) MarkUnknown(condType, reason, message string, level EventLevel) {
	m.SetCondition(metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	}, level)
}

// Delete removes a condition by type if it exists.
// This is useful when a condition becomes irrelevant (e.g., cache condition when caching is disabled).
func (m *ConditionManager) Delete(condType string) {
	idx := indexOfCondition(m.conditions, condType)
	if idx != -1 {
		m.conditions = append(m.conditions[:idx], m.conditions[idx+1:]...)
	}
}

// AllConditionsTrue checks if all the given condition types are true
func (m *ConditionManager) AllConditionsTrue(conditionTypes ...string) bool {
	for _, conditionType := range conditionTypes {
		condition := m.Get(conditionType)
		if condition == nil || condition.Status != metav1.ConditionTrue {
			return false
		}
	}
	return true
}

// AnyConditionTrue checks if any of the given condition types are true
func (m *ConditionManager) AnyConditionTrue(conditionTypes ...string) bool {
	for _, conditionType := range conditionTypes {
		condition := m.Get(conditionType)
		if condition != nil && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// AnyConditionFalse checks if any of the given condition types are false
func (m *ConditionManager) AnyConditionFalse(conditionTypes ...string) bool {
	for _, conditionType := range conditionTypes {
		condition := m.Get(conditionType)
		if condition != nil && condition.Status == metav1.ConditionFalse {
			return true
		}
	}
	return false
}

func (m *ConditionManager) Get(condType string) *metav1.Condition {
	index := indexOfCondition(m.conditions, condType)
	if index == -1 {
		return nil
	}
	condition := m.conditions[index]
	return &condition.Condition
}

func (m *ConditionManager) EventLevelFor(condType string) EventLevel {
	idx := indexOfCondition(m.conditions, condType)
	if idx == -1 {
		return LevelNone
	}
	return m.conditions[idx].Level
}

func indexOfCondition(conditions []LevelCondition, condType string) int {
	for i := range conditions {
		if conditions[i].Type == condType {
			return i
		}
	}
	return -1
}

type ConditionTransition struct {
	Old *metav1.Condition // nil if this condition is new
	New *metav1.Condition // nil if this condition was removed
}

// DiffConditionTransitions returns transitions between old and new condition sets.
// It compares by Type, and considers a transition interesting if Status or Reason changed.
func DiffConditionTransitions(oldConditions, newConditions []metav1.Condition) []ConditionTransition {
	var transitions []ConditionTransition

	oldByType := make(map[string]metav1.Condition, len(oldConditions))
	for _, condition := range oldConditions {
		oldByType[condition.Type] = condition
	}

	newByType := make(map[string]metav1.Condition, len(newConditions))
	for _, condition := range newConditions {
		newByType[condition.Type] = condition
	}

	// Look at new conditions (added or changed)
	for condType, newCondition := range newByType {
		oldCondition, found := oldByType[condType]
		if !found {
			transitions = append(transitions, ConditionTransition{
				Old: nil,
				New: &newCondition,
			})
			continue
		}

		// If status and reason didn't change, ignore
		if oldCondition.Status == newCondition.Status && oldCondition.Reason == newCondition.Reason {
			continue
		}

		transitions = append(transitions, ConditionTransition{
			Old: &oldCondition,
			New: &newCondition,
		})
	}

	return transitions
}

// StatusHelper assists with setting repetitive broad status categories
type StatusHelper struct {
	status StatusWithConditions
	cm     *ConditionManager
}

func NewStatusHelper(
	status StatusWithConditions,
	cm *ConditionManager,
) *StatusHelper {
	return &StatusHelper{status: status, cm: cm}
}

func (h *StatusHelper) Ready(reason, msg string) {
	h.status.SetStatus(string(constants.AIMStatusReady))
	h.cm.Set(string(constants.AIMStatusReady), metav1.ConditionTrue, reason, msg, LevelNormal)
	h.cm.Set(string(constants.AIMStatusProgressing), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusDegraded), metav1.ConditionFalse, reason, msg, LevelNone)
}

func (h *StatusHelper) Progressing(reason, msg string) {
	h.status.SetStatus(string(constants.AIMStatusProgressing))
	h.cm.Set(string(constants.AIMStatusReady), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusProgressing), metav1.ConditionTrue, reason, msg, LevelNormal)
	h.cm.Set(string(constants.AIMStatusDegraded), metav1.ConditionFalse, reason, msg, LevelNone)
}

func (h *StatusHelper) Degraded(reason, msg string) {
	h.status.SetStatus(string(constants.AIMStatusDegraded))
	h.cm.Set(string(constants.AIMStatusReady), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusProgressing), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusDegraded), metav1.ConditionTrue, reason, msg, LevelWarning)
}

func (h *StatusHelper) Failed(reason, msg string) {
	h.status.SetStatus(string(constants.AIMStatusFailed))
	h.cm.Set(string(constants.AIMStatusReady), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusProgressing), metav1.ConditionFalse, reason, msg, LevelNone)
	h.cm.Set(string(constants.AIMStatusDegraded), metav1.ConditionTrue, reason, msg, LevelWarning)
}
