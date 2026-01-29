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
	"math"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

// mustParseBytes parses a quantity string and returns bytes as int64.
// This helper works around the pointer receiver on Quantity.Value().
func mustParseBytes(s string) int64 {
	q := resource.MustParse(s)
	return q.Value()
}

func TestFormatBytesHumanReadable(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		// Zero and negative edge cases
		{
			name:     "zero bytes",
			bytes:    0,
			expected: "0 B",
		},
		{
			name:     "negative value",
			bytes:    -1,
			expected: "-1 B",
		},
		{
			name:     "large negative value",
			bytes:    -1073741824,
			expected: "-1073741824 B",
		},

		// Bytes (< 1 KiB)
		{
			name:     "one byte",
			bytes:    1,
			expected: "1 B",
		},
		{
			name:     "small bytes",
			bytes:    512,
			expected: "512 B",
		},
		{
			name:     "just under 1 KiB",
			bytes:    1023,
			expected: "1023 B",
		},

		// KiB range
		{
			name:     "exactly 1 KiB",
			bytes:    mustParseBytes("1Ki"),
			expected: "1 KiB",
		},
		{
			name:     "1.5 KiB",
			bytes:    mustParseBytes("1536"), // 1.5 * 1024
			expected: "1.5 KiB",
		},
		{
			name:     "10 KiB",
			bytes:    mustParseBytes("10Ki"),
			expected: "10 KiB",
		},
		{
			name:     "just under 1 MiB",
			bytes:    mustParseBytes("1Mi") - 1,
			expected: "1024 KiB",
		},

		// MiB range
		{
			name:     "exactly 1 MiB",
			bytes:    mustParseBytes("1Mi"),
			expected: "1 MiB",
		},
		{
			name:     "1.5 MiB",
			bytes:    mustParseBytes("1536Ki"),
			expected: "1.5 MiB",
		},
		{
			name:     "100 MiB",
			bytes:    mustParseBytes("100Mi"),
			expected: "100 MiB",
		},
		{
			name:     "500 MiB",
			bytes:    mustParseBytes("500Mi"),
			expected: "500 MiB",
		},
		{
			name:     "999 MiB",
			bytes:    mustParseBytes("999Mi"),
			expected: "999 MiB",
		},

		// GiB range (common for model sizes)
		{
			name:     "exactly 1 GiB",
			bytes:    mustParseBytes("1Gi"),
			expected: "1 GiB",
		},
		{
			name:     "1.5 GiB",
			bytes:    mustParseBytes("1536Mi"),
			expected: "1.5 GiB",
		},
		{
			name:     "8 GiB (typical small model)",
			bytes:    mustParseBytes("8Gi"),
			expected: "8 GiB",
		},
		{
			name:     "8.5 GiB",
			bytes:    mustParseBytes("8704Mi"), // 8.5 * 1024
			expected: "8.5 GiB",
		},
		{
			name:     "40 GiB (typical medium model)",
			bytes:    mustParseBytes("40Gi"),
			expected: "40 GiB",
		},
		{
			name:     "70 GiB (typical large model)",
			bytes:    mustParseBytes("70Gi"),
			expected: "70 GiB",
		},
		{
			name:     "140 GiB",
			bytes:    mustParseBytes("140Gi"),
			expected: "140 GiB",
		},

		// TiB range
		{
			name:     "exactly 1 TiB",
			bytes:    mustParseBytes("1Ti"),
			expected: "1 TiB",
		},
		{
			name:     "1.5 TiB",
			bytes:    mustParseBytes("1536Gi"),
			expected: "1.5 TiB",
		},
		{
			name:     "10 TiB",
			bytes:    mustParseBytes("10Ti"),
			expected: "10 TiB",
		},

		// PiB range
		{
			name:     "exactly 1 PiB",
			bytes:    mustParseBytes("1Pi"),
			expected: "1 PiB",
		},
		{
			name:     "1.5 PiB",
			bytes:    mustParseBytes("1536Ti"),
			expected: "1.5 PiB",
		},

		// EiB range (near int64 max)
		{
			name:     "exactly 1 EiB",
			bytes:    mustParseBytes("1Ei"),
			expected: "1 EiB",
		},
		{
			name:     "2 EiB",
			bytes:    mustParseBytes("2Ei"),
			expected: "2 EiB",
		},
		{
			name:     "max int64 (approximately 8 EiB)",
			bytes:    math.MaxInt64,
			expected: "8 EiB",
		},

		// Rounding edge cases using binary fractions
		{
			name:     "1.25 GiB rounds to 1.3",
			bytes:    mustParseBytes("1280Mi"), // 1.25 GiB
			expected: "1.3 GiB",
		},
		{
			name:     "1.75 GiB rounds to 1.8",
			bytes:    mustParseBytes("1792Mi"), // 1.75 GiB
			expected: "1.8 GiB",
		},
		{
			name:     "9.5 GiB",
			bytes:    mustParseBytes("9728Mi"), // 9.5 GiB
			expected: "9.5 GiB",
		},
		{
			name:     "99.5 GiB rounds to 100",
			bytes:    mustParseBytes("101888Mi"), // 99.5 GiB
			expected: "100 GiB",
		},

		// Significant figures boundary tests
		{
			name:     "exactly 10 GiB (boundary)",
			bytes:    mustParseBytes("10Gi"),
			expected: "10 GiB",
		},
		{
			name:     "exactly 100 GiB (boundary)",
			bytes:    mustParseBytes("100Gi"),
			expected: "100 GiB",
		},
		{
			name:     "10.5 GiB",
			bytes:    mustParseBytes("10752Mi"), // 10.5 GiB
			expected: "10.5 GiB",
		},

		// Unit boundary transitions
		{
			name:     "just over 1 GiB boundary",
			bytes:    mustParseBytes("1Gi") + 1,
			expected: "1 GiB",
		},
		{
			name:     "just under 1 TiB boundary",
			bytes:    mustParseBytes("1Ti") - 1,
			expected: "1024 GiB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatBytesHumanReadable(tt.bytes)
			if result != tt.expected {
				t.Errorf("FormatBytesHumanReadable(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestFormatWithTwoSigFigs(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		suffix   string
		expected string
	}{
		// < 10 range (one decimal)
		{"1.0", 1.0, "GiB", "1 GiB"},
		{"1.5", 1.5, "GiB", "1.5 GiB"},
		{"9.9", 9.9, "GiB", "9.9 GiB"},
		{"9.95 rounds to 10", 9.95, "GiB", "10 GiB"},
		{"5.05 rounds to 5.1", 5.05, "GiB", "5.1 GiB"},
		{"5.04 rounds to 5", 5.04, "GiB", "5 GiB"},

		// 10-99 range (one decimal if significant)
		{"10.0", 10.0, "GiB", "10 GiB"},
		{"10.5", 10.5, "GiB", "10.5 GiB"},
		{"42.0", 42.0, "GiB", "42 GiB"},
		{"42.7", 42.7, "GiB", "42.7 GiB"},
		{"99.0", 99.0, "GiB", "99 GiB"},
		{"99.9", 99.9, "GiB", "99.9 GiB"},
		{"99.95 rounds to 100", 99.95, "GiB", "100 GiB"},

		// 100+ range (integer only)
		{"100.0", 100.0, "GiB", "100 GiB"},
		{"100.4 rounds down", 100.4, "GiB", "100 GiB"},
		{"100.5 rounds up", 100.5, "GiB", "101 GiB"},
		{"150.0", 150.0, "GiB", "150 GiB"},
		{"999.0", 999.0, "GiB", "999 GiB"},
		{"1000.0", 1000.0, "GiB", "1000 GiB"},
		{"1024.0", 1024.0, "GiB", "1024 GiB"},

		// Different suffixes
		{"bytes", 512.0, "B", "512 B"},
		{"KiB", 1.5, "KiB", "1.5 KiB"},
		{"MiB", 256.0, "MiB", "256 MiB"},
		{"TiB", 2.5, "TiB", "2.5 TiB"},
		{"PiB", 1.0, "PiB", "1 PiB"},
		{"EiB", 8.0, "EiB", "8 EiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatWithTwoSigFigs(tt.value, tt.suffix)
			if result != tt.expected {
				t.Errorf("formatWithTwoSigFigs(%v, %q) = %q, want %q", tt.value, tt.suffix, result, tt.expected)
			}
		})
	}
}

func BenchmarkFormatBytesHumanReadable(b *testing.B) {
	sizes := []int64{
		0,
		mustParseBytes("1Ki"),
		mustParseBytes("1Mi"),
		mustParseBytes("1Gi"),
		mustParseBytes("40Gi"),
		mustParseBytes("1Ti"),
		math.MaxInt64,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, size := range sizes {
			_ = FormatBytesHumanReadable(size)
		}
	}
}
