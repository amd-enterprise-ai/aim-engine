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

import "strings"

// Well-known preference orders consumed by the auto-selection paths in
// both v1alpha1 (AIMServiceTemplate scoring) and v1alpha2 (AIMProfile
// resolver). They exist here so the two surfaces stay in lock-step: a
// single edit propagates to both code paths.
//
// Earlier entries beat later entries. Values not present in a list tie
// at the bottom (lowest equal priority) via PreferenceScore's fallback,
// so the deterministic alphabetical-name tie-break later in the ranker
// still produces a stable winner.
var (
	// AcceleratorModelPreferenceOrder ranks AMD accelerator models from
	// most-preferred to least-preferred. Instinct data-center
	// accelerators rank above Radeon workstation GPUs.
	//
	// Maintenance: when supporting a new accelerator, add it in the
	// appropriate family slot. New-generation Instincts go at the front
	// of the MI series; newer Radeons go at the front of the R/W
	// series.
	//
	// TODO(operator-tuning): consider exposing a per-cluster override
	// via a well-known ConfigMap (e.g. `aim-accelerator-ranking`) so
	// operators with non-default fleet preferences can re-rank without
	// a code change. Not built yet — wait for field demand.
	AcceleratorModelPreferenceOrder = []string{
		"MI325X", "MI300X", "MI250X", "MI210",
		"R9700", "W7900",
	}

	// MetricPreferenceOrder ranks profile latency/throughput metrics.
	// Latency-optimized profiles win ties because most ad-hoc
	// deployments care about per-request response time.
	MetricPreferenceOrder = []string{
		"latency", "throughput",
	}

	// PrecisionPreferenceOrder ranks numeric precisions. Primary
	// ordering by bit-width (smaller preferred for performance);
	// secondary ordering by type (fp > bf > int, floating-point
	// preferred for accuracy).
	PrecisionPreferenceOrder = []string{
		"fp4", "int4", "fp8", "int8", "fp16", "bf16", "fp32",
	}
)

// MakePreferenceMap returns a lookup from upper-cased preference values
// to their 0-based rank index (lower is better).
func MakePreferenceMap(prefs []string) map[string]int {
	m := make(map[string]int, len(prefs))
	for i, p := range prefs {
		m[strings.ToUpper(p)] = i
	}
	return m
}

// PreferenceScore returns the rank index for value in prefMap, or a
// large sentinel for unknown values so they tie at the bottom of any
// ranking that uses this score directly.
func PreferenceScore(value string, prefMap map[string]int) int {
	if score, ok := prefMap[strings.ToUpper(value)]; ok {
		return score
	}
	return len(prefMap) + 1000
}
