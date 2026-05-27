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

package aimstatus

// AIMStatus is the shared high-level lifecycle state used by AIM custom resources.
type AIMStatus string

const (
	AIMStatusPending      AIMStatus = "Pending"
	AIMStatusStarting     AIMStatus = "Starting"
	AIMStatusProgressing  AIMStatus = "Progressing"
	AIMStatusReady        AIMStatus = "Ready"
	AIMStatusRunning      AIMStatus = "Running"
	AIMStatusDegraded     AIMStatus = "Degraded"
	AIMStatusNotAvailable AIMStatus = "NotAvailable"
	AIMStatusFailed       AIMStatus = "Failed"
)

// Parse returns a known AIMStatus and whether the input is valid.
func Parse(value string) (AIMStatus, bool) {
	switch AIMStatus(value) {
	case AIMStatusPending,
		AIMStatusStarting,
		AIMStatusProgressing,
		AIMStatusReady,
		AIMStatusRunning,
		AIMStatusDegraded,
		AIMStatusNotAvailable,
		AIMStatusFailed:
		return AIMStatus(value), true
	default:
		return "", false
	}
}
