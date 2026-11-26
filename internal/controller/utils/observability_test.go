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
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.eventMode != EventOnTransition {
		t.Errorf("expected eventMode=%v, got %v", EventOnTransition, cfg.eventMode)
	}
	if cfg.logMode != LogOnTransition {
		t.Errorf("expected logMode=%v, got %v", LogOnTransition, cfg.logMode)
	}
}

func TestWithLog(t *testing.T) {
	tests := []struct {
		name     string
		level    int
		expected ObservabilityConfig
	}{
		{
			name:  "custom level 0",
			level: 0,
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 0,
			},
		},
		{
			name:  "custom level 2",
			level: 2,
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			WithLog(tt.level)(&cfg)

			if cfg.logMode != tt.expected.logMode {
				t.Errorf("expected logMode=%v, got %v", tt.expected.logMode, cfg.logMode)
			}
			if cfg.logLevel != tt.expected.logLevel {
				t.Errorf("expected logLevel=%v, got %v", tt.expected.logLevel, cfg.logLevel)
			}
		})
	}
}

func TestLogHelpers(t *testing.T) {
	tests := []struct {
		name     string
		opt      ObservabilityOption
		expected ObservabilityConfig
	}{
		{
			name: "WithErrorLog",
			opt:  WithErrorLog(),
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 0,
			},
		},
		{
			name: "WithInfoLog",
			opt:  WithInfoLog(),
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 0, // V(0) = visible at default info level
			},
		},
		{
			name: "WithDebugLog",
			opt:  WithDebugLog(),
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 1, // V(1) = only visible with debug
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.opt(&cfg)

			if cfg.logMode != tt.expected.logMode {
				t.Errorf("expected logMode=%v, got %v", tt.expected.logMode, cfg.logMode)
			}
			if cfg.logLevel != tt.expected.logLevel {
				t.Errorf("expected logLevel=%v, got %v", tt.expected.logLevel, cfg.logLevel)
			}
		})
	}
}

func TestEventHelpers(t *testing.T) {
	tests := []struct {
		name     string
		opt      ObservabilityOption
		expected ObservabilityConfig
	}{
		{
			name: "WithNormalEvent",
			opt:  WithNormalEvent(),
			expected: ObservabilityConfig{
				eventMode:  EventOnTransition,
				eventLevel: LevelNormal,
			},
		},
		{
			name: "WithWarningEvent",
			opt:  WithWarningEvent(),
			expected: ObservabilityConfig{
				eventMode:  EventOnTransition,
				eventLevel: LevelWarning,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.opt(&cfg)

			if cfg.eventMode != tt.expected.eventMode {
				t.Errorf("expected eventMode=%v, got %v", tt.expected.eventMode, cfg.eventMode)
			}
			if cfg.eventLevel != tt.expected.eventLevel {
				t.Errorf("expected eventLevel=%v, got %v", tt.expected.eventLevel, cfg.eventLevel)
			}
		})
	}
}

func TestRecurringHelpers(t *testing.T) {
	tests := []struct {
		name     string
		opt      ObservabilityOption
		expected ObservabilityConfig
	}{
		{
			name: "WithRecurringErrorLog",
			opt:  WithRecurringErrorLog(),
			expected: ObservabilityConfig{
				eventMode:  EventOnTransition, // default
				eventLevel: LevelNormal,       // default
				logMode:    LogAlways,
				logLevel:   0,
			},
		},
		{
			name: "WithRecurringWarningEvent",
			opt:  WithRecurringWarningEvent(),
			expected: ObservabilityConfig{
				eventMode:  EventAlways,
				eventLevel: LevelWarning,
				logMode:    LogOnTransition, // default
				logLevel:   0,               // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.opt(&cfg)

			if cfg.eventMode != tt.expected.eventMode {
				t.Errorf("expected eventMode=%v, got %v", tt.expected.eventMode, cfg.eventMode)
			}
			if cfg.eventLevel != tt.expected.eventLevel {
				t.Errorf("expected eventLevel=%v, got %v", tt.expected.eventLevel, cfg.eventLevel)
			}
			if cfg.logMode != tt.expected.logMode {
				t.Errorf("expected logMode=%v, got %v", tt.expected.logMode, cfg.logMode)
			}
			if cfg.logLevel != tt.expected.logLevel {
				t.Errorf("expected logLevel=%v, got %v", tt.expected.logLevel, cfg.logLevel)
			}
		})
	}
}

func TestWithRecurring(t *testing.T) {
	tests := []struct {
		name          string
		baseOpts      []ObservabilityOption
		expectedEvent EventMode
		expectedLog   LogMode
	}{
		{
			name:          "with event on transition",
			baseOpts:      []ObservabilityOption{WithWarningEvent(), WithRecurring()},
			expectedEvent: EventAlways,
			expectedLog:   LogAlways, // default OnTransition gets upgraded too
		},
		{
			name:          "with log on transition",
			baseOpts:      []ObservabilityOption{WithInfoLog(), WithRecurring()},
			expectedEvent: EventAlways, // default OnTransition gets upgraded too
			expectedLog:   LogAlways,
		},
		{
			name:          "with both event and log",
			baseOpts:      []ObservabilityOption{WithWarningEvent(), WithErrorLog(), WithRecurring()},
			expectedEvent: EventAlways,
			expectedLog:   LogAlways,
		},
		{
			name:          "recurring upgrades defaults to always",
			baseOpts:      []ObservabilityOption{WithRecurring()},
			expectedEvent: EventAlways,
			expectedLog:   LogAlways,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			for _, opt := range tt.baseOpts {
				opt(&cfg)
			}

			if cfg.eventMode != tt.expectedEvent {
				t.Errorf("expected eventMode=%v, got %v", tt.expectedEvent, cfg.eventMode)
			}
			if cfg.logMode != tt.expectedLog {
				t.Errorf("expected logMode=%v, got %v", tt.expectedLog, cfg.logMode)
			}
		})
	}
}

func TestMessageOverrides(t *testing.T) {
	t.Run("WithEventReason", func(t *testing.T) {
		cfg := defaultConfig()
		customReason := "CustomReason"
		WithEventReason(customReason)(&cfg)

		if cfg.eventReason == nil {
			t.Error("expected eventReason to be set")
		} else if *cfg.eventReason != customReason {
			t.Errorf("expected eventReason=%v, got %v", customReason, *cfg.eventReason)
		}
	})

	t.Run("WithEventMessage", func(t *testing.T) {
		cfg := defaultConfig()
		customMsg := "Custom event message"
		WithEventMessage(customMsg)(&cfg)

		if cfg.eventMessage == nil {
			t.Error("expected eventMessage to be set")
		} else if *cfg.eventMessage != customMsg {
			t.Errorf("expected eventMessage=%v, got %v", customMsg, *cfg.eventMessage)
		}
	})

	t.Run("WithLogMessage", func(t *testing.T) {
		cfg := defaultConfig()
		customMsg := "Custom log message"
		WithLogMessage(customMsg)(&cfg)

		if cfg.logMessage == nil {
			t.Error("expected logMessage to be set")
		} else if *cfg.logMessage != customMsg {
			t.Errorf("expected logMessage=%v, got %v", customMsg, *cfg.logMessage)
		}
	})
}

func TestWithCriticalError(t *testing.T) {
	cfg := defaultConfig()
	WithCriticalError()(&cfg)

	if cfg.eventMode != EventAlways {
		t.Errorf("expected eventMode=%v, got %v", EventAlways, cfg.eventMode)
	}
	if cfg.eventLevel != LevelWarning {
		t.Errorf("expected eventLevel=%v, got %v", LevelWarning, cfg.eventLevel)
	}
	if cfg.logMode != LogAlways {
		t.Errorf("expected logMode=%v, got %v", LogAlways, cfg.logMode)
	}
	if cfg.logLevel != 0 {
		t.Errorf("expected logLevel=0, got %v", cfg.logLevel)
	}
}

func TestEventLevelToOption(t *testing.T) {
	tests := []struct {
		name          string
		level         EventLevel
		expectedEvent EventMode
		expectedLevel EventLevel
	}{
		{
			name:          "LevelNone",
			level:         LevelNone,
			expectedEvent: EventOnTransition, // no-op, keeps default
			expectedLevel: LevelNormal,       // default
		},
		{
			name:          "LevelNormal",
			level:         LevelNormal,
			expectedEvent: EventOnTransition,
			expectedLevel: LevelNormal,
		},
		{
			name:          "LevelWarning",
			level:         LevelWarning,
			expectedEvent: EventOnTransition,
			expectedLevel: LevelWarning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			EventLevelToOption(tt.level)(&cfg)

			if cfg.eventMode != tt.expectedEvent {
				t.Errorf("expected eventMode=%v, got %v", tt.expectedEvent, cfg.eventMode)
			}
			if cfg.eventLevel != tt.expectedLevel {
				t.Errorf("expected eventLevel=%v, got %v", tt.expectedLevel, cfg.eventLevel)
			}
		})
	}
}

func TestComposability(t *testing.T) {
	t.Run("multiple options compose", func(t *testing.T) {
		cfg := defaultConfig()

		opts := []ObservabilityOption{
			WithWarningEvent(),
			WithErrorLog(),
			WithEventMessage("Custom event"),
			WithLogMessage("Custom log"),
		}

		for _, opt := range opts {
			opt(&cfg)
		}

		if cfg.eventMode != EventOnTransition {
			t.Errorf("expected eventMode=%v, got %v", EventOnTransition, cfg.eventMode)
		}
		if cfg.eventLevel != LevelWarning {
			t.Errorf("expected eventLevel=%v, got %v", LevelWarning, cfg.eventLevel)
		}
		if cfg.logMode != LogOnTransition {
			t.Errorf("expected logMode=%v, got %v", LogOnTransition, cfg.logMode)
		}
		if cfg.logLevel != 0 {
			t.Errorf("expected logLevel=0, got %v", cfg.logLevel)
		}
		if cfg.eventMessage == nil || *cfg.eventMessage != "Custom event" {
			t.Error("expected custom event message")
		}
		if cfg.logMessage == nil || *cfg.logMessage != "Custom log" {
			t.Error("expected custom log message")
		}
	})
}

func TestHighLevelDefaults(t *testing.T) {
	tests := []struct {
		name              string
		opt               ObservabilityOption
		expectedEvent     EventMode
		expectedLevel     EventLevel
		expectedLog       LogMode
		expectedLogLv     int
		expectedLogAsInfo bool
	}{
		{
			name:          "AsInfo",
			opt:           AsInfo(),
			expectedEvent: EventOnTransition,
			expectedLevel: LevelNormal,
			expectedLog:   LogOnTransition,
			expectedLogLv: 0, // V(0) = visible at default info level
		},
		{
			name:          "AsWarning",
			opt:           AsWarning(),
			expectedEvent: EventOnTransition,
			expectedLevel: LevelWarning,
			expectedLog:   LogOnTransition,
			expectedLogLv: 0,
		},
		{
			name:              "AsUserWarning",
			opt:               AsUserWarning(),
			expectedEvent:     EventOnTransition,
			expectedLevel:     LevelWarning,
			expectedLog:       LogOnTransition,
			expectedLogLv:     0,
			expectedLogAsInfo: true,
		},
		{
			name:          "AsError",
			opt:           AsError(),
			expectedEvent: EventAlways,
			expectedLevel: LevelWarning,
			expectedLog:   LogAlways,
			expectedLogLv: 0,
		},
		{
			name:          "Silent",
			opt:           Silent(),
			expectedEvent: EventNone,
			expectedLog:   LogNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.opt(&cfg)

			if cfg.eventMode != tt.expectedEvent {
				t.Errorf("expected eventMode=%v, got %v", tt.expectedEvent, cfg.eventMode)
			}
			if tt.expectedEvent != EventNone && cfg.eventLevel != tt.expectedLevel {
				t.Errorf("expected eventLevel=%v, got %v", tt.expectedLevel, cfg.eventLevel)
			}
			if cfg.logMode != tt.expectedLog {
				t.Errorf("expected logMode=%v, got %v", tt.expectedLog, cfg.logMode)
			}
			if tt.expectedLog != LogNone && cfg.logLevel != tt.expectedLogLv {
				t.Errorf("expected logLevel=%v, got %v", tt.expectedLogLv, cfg.logLevel)
			}
			if cfg.logAsInfo != tt.expectedLogAsInfo {
				t.Errorf("expected logAsInfo=%v, got %v", tt.expectedLogAsInfo, cfg.logAsInfo)
			}
		})
	}
}

type levelCountingSink struct{ infos, errors int }

func (s *levelCountingSink) Init(logr.RuntimeInfo)          {}
func (s *levelCountingSink) Enabled(int) bool               { return true }
func (s *levelCountingSink) Info(int, string, ...any)       { s.infos++ }
func (s *levelCountingSink) Error(error, string, ...any)    { s.errors++ }
func (s *levelCountingSink) WithValues(...any) logr.LogSink { return s }
func (s *levelCountingSink) WithName(string) logr.LogSink   { return s }

func TestEmit_UserWarningLogsInfoWithWarningEvent(t *testing.T) {
	tests := []struct {
		name       string
		opt        ObservabilityOption
		wantInfos  int
		wantErrors int
	}{
		{name: "AsWarning logs an error", opt: AsWarning(), wantErrors: 1},
		{name: "AsUserWarning logs info", opt: AsUserWarning(), wantInfos: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConditionManager(nil)
			cm.MarkFalse("ProfileReady", "ProfileNotFound", "no AIMProfile matched the selector", tt.opt)
			transitions := DiffConditionTransitions(nil, cm.Conditions())
			sink := &levelCountingSink{}
			ctx := log.IntoContext(context.Background(), logr.New(sink))
			recorder := record.NewFakeRecorder(10)

			// Run twice: the second pass has no transition, so nothing recurs.
			for _, ts := range [][]ConditionTransition{transitions, nil} {
				EmitConditionTransitions(recorder, &testObject{}, ts, cm)
				EmitRecurringEvents(recorder, &testObject{}, cm)
				EmitConditionLogs(ctx, ts, cm)
				EmitRecurringLogs(ctx, cm)
			}

			if sink.infos != tt.wantInfos || sink.errors != tt.wantErrors {
				t.Errorf("logs: infos=%d errors=%d, want infos=%d errors=%d",
					sink.infos, sink.errors, tt.wantInfos, tt.wantErrors)
			}
			if len(recorder.Events) != 1 {
				t.Fatalf("events = %d, want 1", len(recorder.Events))
			}
			if event := <-recorder.Events; !strings.HasPrefix(event, "Warning ProfileNotFound ") {
				t.Errorf("event = %q, want a Warning ProfileNotFound event", event)
			}
		})
	}
}
