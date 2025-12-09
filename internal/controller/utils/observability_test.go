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
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.eventMode != EventNone {
		t.Errorf("expected eventMode=%v, got %v", EventNone, cfg.eventMode)
	}
	if cfg.logMode != LogNone {
		t.Errorf("expected logMode=%v, got %v", LogNone, cfg.logMode)
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
				logLevel: 1,
			},
		},
		{
			name: "WithDebugLog",
			opt:  WithDebugLog(),
			expected: ObservabilityConfig{
				logMode:  LogOnTransition,
				logLevel: 2,
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
				logMode:  LogAlways,
				logLevel: 0,
			},
		},
		{
			name: "WithRecurringWarningEvent",
			opt:  WithRecurringWarningEvent(),
			expected: ObservabilityConfig{
				eventMode:  EventAlways,
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
			expectedLog:   LogNone,
		},
		{
			name:          "with log on transition",
			baseOpts:      []ObservabilityOption{WithInfoLog(), WithRecurring()},
			expectedEvent: EventNone,
			expectedLog:   LogAlways,
		},
		{
			name:          "with both event and log",
			baseOpts:      []ObservabilityOption{WithWarningEvent(), WithErrorLog(), WithRecurring()},
			expectedEvent: EventAlways,
			expectedLog:   LogAlways,
		},
		{
			name:          "recurring on nothing does nothing",
			baseOpts:      []ObservabilityOption{WithRecurring()},
			expectedEvent: EventNone,
			expectedLog:   LogNone,
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
			expectedEvent: EventNone,
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
			if tt.expectedEvent != EventNone && cfg.eventLevel != tt.expectedLevel {
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
		name          string
		opt           ObservabilityOption
		expectedEvent EventMode
		expectedLevel EventLevel
		expectedLog   LogMode
		expectedLogLv int
	}{
		{
			name:          "AsInfo",
			opt:           AsInfo(),
			expectedEvent: EventOnTransition,
			expectedLevel: LevelNormal,
			expectedLog:   LogOnTransition,
			expectedLogLv: 1,
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
		})
	}
}
