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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// SelectBest returns the item with the highest priority status from the slice.
// Returns the zero value if the slice is empty.
// The getStatus function extracts the AIMStatus from each item.
func SelectBest[T any](items []T, getStatus func(T) constants.AIMStatus) T {
	var best T
	if len(items) == 0 {
		return best
	}

	best = items[0]
	bestStatus := getStatus(best)

	for i := 1; i < len(items); i++ {
		item := items[i]
		status := getStatus(item)
		if constants.CompareAIMStatus(status, bestStatus) > 0 {
			best = item
			bestStatus = status
		}
	}

	return best
}

// SelectWorst returns the item with the lowest priority status from the slice.
// Returns the zero value if the slice is empty.
// The getStatus function extracts the AIMStatus from each item.
func SelectWorst[T any](items []T, getStatus func(T) constants.AIMStatus) T {
	var worst T
	if len(items) == 0 {
		return worst
	}

	worst = items[0]
	worstStatus := getStatus(worst)

	for i := 1; i < len(items); i++ {
		item := items[i]
		status := getStatus(item)
		if constants.CompareAIMStatus(status, worstStatus) < 0 {
			worst = item
			worstStatus = status
		}
	}

	return worst
}

// SelectBestPtr returns a pointer to the item with the highest priority status.
// Returns nil if the slice is empty.
// This variant is useful when working with slices of structs where you need a pointer result.
func SelectBestPtr[T any](items []T, getStatus func(*T) constants.AIMStatus) *T {
	if len(items) == 0 {
		return nil
	}

	best := &items[0]
	bestStatus := getStatus(best)

	for i := 1; i < len(items); i++ {
		item := &items[i]
		status := getStatus(item)
		if constants.CompareAIMStatus(status, bestStatus) > 0 {
			best = item
			bestStatus = status
		}
	}

	return best
}

// SelectWorstPtr returns a pointer to the item with the lowest priority status.
// Returns nil if the slice is empty.
func SelectWorstPtr[T any](items []T, getStatus func(*T) constants.AIMStatus) *T {
	if len(items) == 0 {
		return nil
	}

	worst := &items[0]
	worstStatus := getStatus(worst)

	for i := 1; i < len(items); i++ {
		item := &items[i]
		status := getStatus(item)
		if constants.CompareAIMStatus(status, worstStatus) < 0 {
			worst = item
			worstStatus = status
		}
	}

	return worst
}
