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
	"errors"
	"fmt"
	"math"
)

var (
	// ErrNegativeSize is returned when a negative byte size is provided.
	ErrNegativeSize = errors.New("byte size cannot be negative")

	// ErrSizeTooLarge is returned when the byte size exceeds the maximum supported value.
	ErrSizeTooLarge = errors.New("byte size exceeds maximum supported value (8 EiB)")
)

// MaxSupportedBytes is the maximum byte size that can be formatted (just under 8 EiB).
// This is set to 8 EiB - 1 byte to ensure formatted output stays within reasonable bounds.
const MaxSupportedBytes = int64(7 << 60) // 7 EiB

// ByteUnit represents a unit of digital storage.
type ByteUnit struct {
	Size   int64
	Suffix string
}

var binaryUnits = []ByteUnit{
	{1 << 60, "EiB"},
	{1 << 50, "PiB"},
	{1 << 40, "TiB"},
	{1 << 30, "GiB"},
	{1 << 20, "MiB"},
	{1 << 10, "KiB"},
	{1, "B"},
}

// FormatBytesHumanReadable converts bytes to a human-readable string
// with two significant digits (e.g., "42 GiB", "1.5 TiB", "850 MiB").
// Returns an error if bytes is negative or exceeds MaxSupportedBytes.
func FormatBytesHumanReadable(bytes int64) (string, error) {
	if bytes < 0 {
		return "", ErrNegativeSize
	}

	if bytes > MaxSupportedBytes {
		return "", ErrSizeTooLarge
	}

	if bytes == 0 {
		return "0 B", nil
	}

	for _, unit := range binaryUnits {
		if bytes >= unit.Size {
			value := float64(bytes) / float64(unit.Size)
			return formatWithTwoSigFigs(value, unit.Suffix), nil
		}
	}

	return fmt.Sprintf("%d B", bytes), nil
}

// formatWithTwoSigFigs formats a value with approximately two significant figures.
func formatWithTwoSigFigs(value float64, suffix string) string {
	if value >= 100 {
		// 100+ -> show as integer (e.g., "150 GiB")
		return fmt.Sprintf("%.0f %s", math.Round(value), suffix)
	} else if value >= 10 {
		// 10-99 -> show one decimal if significant (e.g., "42 GiB" or "42.5 GiB")
		rounded := math.Round(value*10) / 10
		if rounded == math.Floor(rounded) {
			return fmt.Sprintf("%.0f %s", rounded, suffix)
		}
		return fmt.Sprintf("%.1f %s", rounded, suffix)
	} else {
		// 1-9.99 -> show one decimal (e.g., "1.5 TiB", "8.2 GiB")
		rounded := math.Round(value*10) / 10
		if rounded == math.Floor(rounded) {
			return fmt.Sprintf("%.0f %s", rounded, suffix)
		}
		return fmt.Sprintf("%.1f %s", rounded, suffix)
	}
}
