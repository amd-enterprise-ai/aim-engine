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

package aimservicetemplate

import "testing"

func TestExtractVersionFromImage(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{
			name:     "standard image with tag",
			image:    "amdenterpriseai/aim-base:0.8.5",
			expected: "0.8.5",
		},
		{
			name:     "image with registry and tag",
			image:    "registry.example.com/amdenterpriseai/aim-base:1.2.3",
			expected: "1.2.3",
		},
		{
			name:     "image with registry port and tag",
			image:    "registry.example.com:5000/aim-base:0.8.5",
			expected: "0.8.5",
		},
		{
			name:     "image without tag",
			image:    "amdenterpriseai/aim-base",
			expected: "",
		},
		{
			name:     "image with digest",
			image:    "amdenterpriseai/aim-base@sha256:abc123",
			expected: "",
		},
		{
			name:     "empty image",
			image:    "",
			expected: "",
		},
		{
			name:     "simple image with tag",
			image:    "nginx:1.25",
			expected: "1.25",
		},
		{
			name:     "image with latest tag",
			image:    "aim-base:latest",
			expected: "latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractVersionFromImage(tt.image)
			if got != tt.expected {
				t.Errorf("ExtractVersionFromImage(%q) = %q, want %q", tt.image, got, tt.expected)
			}
		})
	}
}
