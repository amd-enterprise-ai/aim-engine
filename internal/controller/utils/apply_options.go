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

// LabelPropagationSettings contains the shared label propagation behavior needed
// during apply without coupling the pipeline to a specific API version.
type LabelPropagationSettings struct {
	Enabled bool
	Match   []string
}

// ApplyOptions lets domain reconcilers provide version-specific apply behavior
// while keeping the pipeline package version-neutral.
type ApplyOptions struct {
	LabelPropagation *LabelPropagationSettings
}

// ApplyOptionsProvider is implemented by reconcilers that need to customize the
// shared apply phase based on the composed observation.
type ApplyOptionsProvider[Obs any] interface {
	GetApplyOptions(obs Obs) ApplyOptions
}
