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

package aimprofile

import (
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

const testFamilyPartitionMode = "CPX-NPS4"

func TestLatestPreservesHighestMatchingVersionSemantics(t *testing.T) {
	base := ProfileCopyCandidate{
		Spec:   aimv1alpha2.AIMProfileSpecCommon{AimId: "first", ModelId: "first", ProfileId: "gpu", Engine: "vllm", Image: "registry/runtime:1.0", AcceleratorModel: "MI300X", AcceleratorCount: 1},
		Status: aimv1alpha2.AIMProfileStatus{Version: "1.0"},
	}
	newer := base
	newer.Spec.Image, newer.Status.Version = "registry/runtime:2.0", "2.0"
	model := base
	model.Spec.ModelId = "second"
	hardware := base
	hardware.Spec.AcceleratorPartitioningMode = testFamilyPartitionMode
	runtime := base
	runtime.Spec.EngineArgs = &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":2}`)}
	repository := base
	repository.Spec.Image = "registry/other:1.0"
	versionless := base
	versionless.Spec.ModelId, versionless.Status.Version = "digest-model", ""
	candidates := []ProfileCopyCandidate{base, newer, model, hardware, runtime, repository, versionless}
	result, err := FilterProfileCopyCandidateIndexes(candidates, ProfileCopyRequest{VersionPolicy: aimv1alpha1.ProfileVersionPolicyLatest})
	if err != nil || len(result) != 1 || result[0] != 1 {
		t.Fatalf("latest must retain the highest matching source version: %v, %v", result, err)
	}
	all, err := FilterProfileCopyCandidates(candidates, ProfileCopyRequest{VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll})
	if err != nil || len(all) != len(candidates) {
		t.Fatal("all excluded versionless source")
	}
}

func TestProfileSetImagePrecedenceAndLiteralTPSelection(t *testing.T) {
	source := aimv1alpha2.AIMProfileSpecCommon{Image: "original", EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor_parallel_size":1}`)}}
	selector := aimv1alpha1.ProfileSelector{EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)}}
	matches, err := MatchesProfileCopySelector(ProfileCopyCandidate{Spec: source}, selector)
	if err != nil || matches {
		t.Fatal("engine arg spelling was treated as equivalent")
	}
	source.EngineArgs = nil
	matches, err = MatchesProfileCopySelector(ProfileCopyCandidate{Spec: source}, selector)
	if err != nil || matches {
		t.Fatal("missing TP treated as 1")
	}
	result, err := ApplyProfileCopyOverrides(source, &aimv1alpha1.ProfileOverrides{Image: "nested"}, "top-level", "")
	if err != nil || result.Image != "nested" {
		t.Fatal("nested image override precedence changed")
	}
}
