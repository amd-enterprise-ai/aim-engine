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
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type EventLevel string

const (
	LevelNone    EventLevel = ""
	LevelNormal  EventLevel = EventLevel(corev1.EventTypeNormal)
	LevelWarning EventLevel = EventLevel(corev1.EventTypeWarning)
)

type EventMode string

const (
	EventNone         EventMode = ""
	EventOnTransition EventMode = "transition"
	EventAlways       EventMode = "always"
)

type LogMode string

const (
	LogNone         LogMode = ""
	LogOnTransition LogMode = "transition"
	LogAlways       LogMode = "always"
)

// ObservabilityConfig controls how condition changes are observed (events and logs).
// This configuration determines when and how to emit Kubernetes events and controller logs
// for condition state changes.
type ObservabilityConfig struct {
	// eventMode controls when to emit Kubernetes events (none, on transition, or always)
	eventMode EventMode
	// eventLevel specifies the event type (Normal or Warning)
	eventLevel EventLevel
	// eventReason overrides the default event reason (default: conditionType + conditionReason)
	eventReason *string
	// eventMessage overrides the default event message (default: condition message)
	eventMessage *string

	// logMode controls when to emit controller logs (none, on transition, or always)
	logMode LogMode
	// logLevel specifies the log verbosity level (0=error, 1=info, 2=debug)
	logLevel int
	// logMessage overrides the default log message (default: condition message)
	logMessage *string
}

type ObservabilityOption func(*ObservabilityConfig)

func defaultConfig() ObservabilityConfig {
	return ObservabilityConfig{
		eventMode: EventNone,
		logMode:   LogNone,
	}
}

// === Core Options ===

// WithRecurring makes any event/log happen every reconcile (not just on transition)
func WithRecurring() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		if c.eventMode == EventOnTransition {
			c.eventMode = EventAlways
		}
		if c.logMode == LogOnTransition {
			c.logMode = LogAlways
		}
	}
}

// === Log Options ===

func WithLog(level int) ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.logMode = LogOnTransition
		c.logLevel = level
	}
}

// WithErrorLog logs at V(0) - always visible. Use for errors that must be seen.
func WithErrorLog() ObservabilityOption {
	return WithLog(0)
}

// WithInfoLog logs at V(0) - visible at default info level.
// Use for important state transitions like Ready conditions.
func WithInfoLog() ObservabilityOption {
	return WithLog(0)
}

// WithDebugLog logs at V(1) - only visible with -zap-log-level=debug.
// Use for verbose operational details.
func WithDebugLog() ObservabilityOption {
	return WithLog(1)
}

// === Event Options ===

func WithNormalEvent() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventMode = EventOnTransition
		c.eventLevel = LevelNormal
	}
}

func WithWarningEvent() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventMode = EventOnTransition
		c.eventLevel = LevelWarning
	}
}

// === Recurring Shorthands ===

func WithRecurringErrorLog() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.logMode = LogAlways
		c.logLevel = 0
	}
}

func WithRecurringWarningEvent() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventMode = EventAlways
		c.eventLevel = LevelWarning
	}
}

// === Message/Reason Overrides ===

func WithEventReason(reason string) ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventReason = &reason
	}
}

func WithEventMessage(msg string) ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventMessage = &msg
	}
}

func WithLogMessage(msg string) ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.logMessage = &msg
	}
}

// === High-Level Defaults ===

// AsInfo is the default for informational/progress updates.
// Emits info log (V(1)) and normal event on transition only.
func AsInfo() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		WithInfoLog()(c)
		WithNormalEvent()(c)
	}
}

// AsWarning is for transient errors or degraded states.
// Emits error log (V(0)) and warning event on transition only.
func AsWarning() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		WithErrorLog()(c)
		WithWarningEvent()(c)
	}
}

// AsError is for critical/persistent errors.
// Emits error log (V(0)) and warning event EVERY reconcile (recurring).
func AsError() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		WithRecurringErrorLog()(c)
		WithRecurringWarningEvent()(c)
	}
}

// Silent explicitly marks a condition as having no events or logs.
// This is the default, but can be used for clarity or to override other options.
func Silent() ObservabilityOption {
	return func(c *ObservabilityConfig) {
		c.eventMode = EventNone
		c.logMode = LogNone
	}
}

// === Convenience Combinations (deprecated - use AsInfo/AsWarning/AsError) ===

func WithCriticalError() ObservabilityOption {
	return AsError()
}

// === Backwards Compatibility Helpers ===

// EventLevelToOption converts the old EventLevel constants to ObservabilityOptions.
// This provides backwards compatibility for existing code.
func EventLevelToOption(level EventLevel) ObservabilityOption {
	switch level {
	case LevelNone:
		// No event, no log
		return func(c *ObservabilityConfig) {}
	case LevelNormal:
		// Normal event on transition
		return WithNormalEvent()
	case LevelWarning:
		// Warning event on transition
		return WithWarningEvent()
	default:
		return func(c *ObservabilityConfig) {}
	}
}

// buildEventReason builds the event reason from a condition, using custom reason if provided.
// Returns just the condition reason since the condition type is already in the message body.
func buildEventReason(conditionReason string, customReason *string) string {
	if customReason != nil {
		return *customReason
	}
	return conditionReason
}

// buildEventMessage builds the event/log message from a condition, using custom message if provided.
// Format: "{Component} is ready: {reason}" or "{Component} is not ready: {reason}"
func buildEventMessage(conditionType string, conditionStatus metav1.ConditionStatus, conditionReason, conditionMessage string, customMessage *string) string {
	if customMessage != nil {
		return *customMessage
	}
	if conditionMessage != "" {
		return conditionMessage
	}

	// Strip "Ready" suffix from condition type for cleaner messages
	// e.g., "RuntimeConfigReady" -> "RuntimeConfig"
	component := strings.TrimSuffix(conditionType, ComponentConditionSuffix)

	var readyStatus string
	switch conditionStatus {
	case metav1.ConditionTrue:
		readyStatus = "is ready"
	case metav1.ConditionFalse:
		readyStatus = "is not ready"
	default:
		readyStatus = "status unknown"
	}

	return fmt.Sprintf("%s %s: %s", component, readyStatus, conditionReason)
}

func EmitConditionTransitions(
	recorder record.EventRecorder,
	obj runtime.Object,
	transitions []ConditionTransition,
	manager *ConditionManager,
) {
	if recorder == nil || manager == nil {
		return
	}

	for _, transition := range transitions {
		// We only care about conditions that now exist
		if transition.New == nil {
			continue
		}
		newCondition := transition.New

		// Look up the observability config for this condition
		cfg := manager.ConfigFor(newCondition.Type)
		if cfg.eventMode == EventNone {
			continue
		}

		// Only emit if mode is EventOnTransition (handled by transitions)
		// EventAlways is handled separately in EmitRecurringEvents
		if cfg.eventMode != EventOnTransition {
			continue
		}

		eventType := string(cfg.eventLevel)
		reason := buildEventReason(newCondition.Reason, cfg.eventReason)
		message := buildEventMessage(newCondition.Type, newCondition.Status, newCondition.Reason, newCondition.Message, cfg.eventMessage)

		recorder.Event(obj, eventType, reason, message)
	}
}

// EmitRecurringEvents emits events for all conditions configured with EventAlways,
// regardless of whether they transitioned
func EmitRecurringEvents(
	recorder record.EventRecorder,
	obj runtime.Object,
	manager *ConditionManager,
) {
	if recorder == nil || manager == nil {
		return
	}

	for _, cc := range manager.conditions {
		if cc.Config.eventMode != EventAlways {
			continue
		}

		eventType := string(cc.Config.eventLevel)
		reason := buildEventReason(cc.Reason, cc.Config.eventReason)
		message := buildEventMessage(cc.Type, cc.Status, cc.Reason, cc.Message, cc.Config.eventMessage)

		recorder.Event(obj, eventType, reason, message)
	}
}

// EmitConditionLogs logs condition transitions based on their observability config
func EmitConditionLogs(
	ctx context.Context,
	transitions []ConditionTransition,
	manager *ConditionManager,
) {
	if manager == nil {
		return
	}

	logger := log.FromContext(ctx)

	for _, transition := range transitions {
		// We only care about conditions that now exist
		if transition.New == nil {
			continue
		}
		newCondition := transition.New

		// Look up the observability config for this condition
		cfg := manager.ConfigFor(newCondition.Type)
		if cfg.logMode == LogNone {
			continue
		}

		// Only log if mode is LogOnTransition (handled by transitions)
		// LogAlways is handled separately in EmitRecurringLogs
		if cfg.logMode != LogOnTransition {
			continue
		}

		message := buildEventMessage(newCondition.Type, newCondition.Status, newCondition.Reason, newCondition.Message, cfg.logMessage)

		// Use Error() for warning-level events (actual errors), Info() for normal events
		// We can't use logLevel alone since both INFO and ERROR use V(0)
		if cfg.eventLevel == LevelWarning {
			logger.Error(nil, message,
				"condition", newCondition.Type,
				"status", newCondition.Status,
				"reason", newCondition.Reason,
			)
		} else {
			logger.V(cfg.logLevel).Info(message,
				"condition", newCondition.Type,
				"status", newCondition.Status,
				"reason", newCondition.Reason,
			)
		}
	}
}

// EmitRecurringLogs logs all conditions configured with LogAlways,
// regardless of whether they transitioned
func EmitRecurringLogs(
	ctx context.Context,
	manager *ConditionManager,
) {
	if manager == nil {
		return
	}

	logger := log.FromContext(ctx)

	for _, cc := range manager.conditions {
		if cc.Config.logMode != LogAlways {
			continue
		}

		message := buildEventMessage(cc.Type, cc.Status, cc.Reason, cc.Message, cc.Config.logMessage)

		// Use Error() for warning-level events (actual errors), Info() for normal events
		// We can't use logLevel alone since both INFO and ERROR use V(0)
		if cc.Config.eventLevel == LevelWarning {
			logger.Error(nil, message,
				"condition", cc.Type,
				"status", cc.Status,
				"reason", cc.Reason,
			)
		} else {
			logger.V(cc.Config.logLevel).Info(message,
				"condition", cc.Type,
				"status", cc.Status,
				"reason", cc.Reason,
			)
		}
	}
}
