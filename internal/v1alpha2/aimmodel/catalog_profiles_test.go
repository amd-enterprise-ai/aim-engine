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

package aimmodel

import (
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

func TestCatalogProfileSpecPublishesBaseImageRef(t *testing.T) {
	const baseImage = "docker.io/amdenterpriseai/aim-base:0.11.2"
	got := CatalogProfileSpec(
		aimprofile.DiscoveryCatalogItem{
			BaseImage: baseImage,
			Spec:      aimv1alpha2.AIMProfileSpecCommon{Image: "registry.example/model:1.0"},
		},
		&aimv1alpha1.AIMModelSpec{},
		nil,
	)

	if got.BaseImageRef != baseImage {
		t.Fatalf("baseImageRef = %q, want %q", got.BaseImageRef, baseImage)
	}
}
