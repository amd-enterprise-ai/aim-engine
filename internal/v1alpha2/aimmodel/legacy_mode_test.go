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
)

func TestEnsureBaseImageBridge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		spec             aimv1alpha1.AIMModelSpec
		status           aimv1alpha1.AIMModelStatus
		wantBaseImage    string
		wantImageMetaRef string
	}{
		{
			name:   "no metadata, no base image - bridge no-op",
			spec:   aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{},
		},
		{
			name: "v1alpha1 status.imageMetadata.baseImageRef populates baseImage",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					BaseImageRef: "ghcr.io/amdenterpriseai/aim-base:0.11",
				},
			},
			wantBaseImage:    "ghcr.io/amdenterpriseai/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "v1alpha2 status.baseImage propagates into existing imageMetadata",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				BaseImage:     "ghcr.io/amdenterpriseai/aim-base:0.11",
				ImageMetadata: &aimv1alpha1.ImageMetadata{Model: &aimv1alpha1.ModelMetadata{}},
			},
			wantBaseImage:    "ghcr.io/amdenterpriseai/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "v1alpha2 status.baseImage alone does not create imageMetadata",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.11",
			},
			wantBaseImage: "ghcr.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "spec.imageMetadata.baseImageRef populates baseImage without creating imageMetadata",
			spec: aimv1alpha1.AIMModelSpec{
				ImageMetadata: &aimv1alpha1.ImageMetadata{BaseImageRef: "ghcr.io/amdenterpriseai/aim-base:0.11"},
			},
			status:        aimv1alpha1.AIMModelStatus{},
			wantBaseImage: "ghcr.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "existing imageMetadata.baseImageRef is preserved when baseImage is empty",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					BaseImageRef: "docker.io/amd/aim-base:0.10",
				},
			},
			wantBaseImage:    "docker.io/amd/aim-base:0.10",
			wantImageMetaRef: "docker.io/amd/aim-base:0.10",
		},
		{
			name: "both fields already aligned - bridge is idempotent",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.11",
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					BaseImageRef: "ghcr.io/amdenterpriseai/aim-base:0.11",
				},
			},
			wantBaseImage:    "ghcr.io/amdenterpriseai/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/amdenterpriseai/aim-base:0.11",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status := *tc.status.DeepCopy()
			EnsureBaseImageBridge(&tc.spec, &status)
			if status.BaseImage != tc.wantBaseImage {
				t.Errorf("status.BaseImage = %q, want %q", status.BaseImage, tc.wantBaseImage)
			}
			gotMetaRef := ""
			if status.ImageMetadata != nil {
				gotMetaRef = status.ImageMetadata.BaseImageRef
			}
			if gotMetaRef != tc.wantImageMetaRef {
				t.Errorf("status.imageMetadata.baseImageRef = %q, want %q", gotMetaRef, tc.wantImageMetaRef)
			}
		})
	}
}

func TestEnsureBaseImageBridge_NilStatusIsNoOp(t *testing.T) {
	t.Parallel()
	EnsureBaseImageBridge(&aimv1alpha1.AIMModelSpec{}, nil)
}

func TestProfilesSpecToProfileSet_PropagatesOutputPolicy(t *testing.T) {
	t.Parallel()

	profiles := &aimv1alpha1.AIMModelProfilesSpec{
		DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
			Selector: aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		},
		OutputPolicy: &aimv1alpha1.ProfileOutputPolicy{
			Copyable: true,
		},
	}

	set := profilesSpecToProfileSet(profiles)
	if set == nil || set.OutputPolicy == nil || !set.OutputPolicy.Copyable {
		t.Fatalf("translated profile set output policy = %#v, want copyable=true", set)
	}
}

// TestClassifyModelKind covers the three v1alpha2 onboarding flows
// surfaced as status.kind. The classifier is the source of truth for
// the discriminator field that users read from kubectl printcolumns,
// so each spec shape must map to a single, stable kind.
func TestClassifyModelKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec *aimv1alpha1.AIMModelSpec
		want aimv1alpha1.AIMModelKind
	}{
		{
			name: "image-flow: spec.image set, no profiles → Image",
			spec: &aimv1alpha1.AIMModelSpec{Image: "ghcr.io/acme/aim-llama:0.11"},
			want: aimv1alpha1.AIMModelKindImage,
		},
		{
			name: "image-flow: base-image AIMModel is still Image (a base image is a kind of image, not a custom-derivation)",
			spec: &aimv1alpha1.AIMModelSpec{Image: "amdenterpriseai/aim-base:0.11"},
			want: aimv1alpha1.AIMModelKindImage,
		},
		{
			name: "custom-flow: spec.profiles + role=base → Custom",
			spec: &aimv1alpha1.AIMModelSpec{
				Profiles: &aimv1alpha1.AIMModelProfilesSpec{
					DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
						Selector: aimv1alpha1.ProfileSelector{
							Role: aimv1alpha1.ProfileSelectorRoleBase,
						},
					},
					Overrides: &aimv1alpha1.ProfileOverrides{
						AimId:   "acme/byo",
						ModelId: "acme/byo",
					},
				},
			},
			want: aimv1alpha1.AIMModelKindCustom,
		},
		{
			name: "derived-flow: spec.profiles + role=deployable → Derived",
			spec: &aimv1alpha1.AIMModelSpec{
				Profiles: &aimv1alpha1.AIMModelProfilesSpec{
					DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
						Selector: aimv1alpha1.ProfileSelector{
							Role:  aimv1alpha1.ProfileSelectorRoleDeployable,
							AimId: "qwen/qwen3-32b",
						},
					},
				},
			},
			want: aimv1alpha1.AIMModelKindDerived,
		},
		{
			name: "derived-flow: spec.profiles + role unset defaults to Derived (mirrors selector.role default)",
			spec: &aimv1alpha1.AIMModelSpec{
				Profiles: &aimv1alpha1.AIMModelProfilesSpec{
					DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
						Selector: aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
					},
				},
			},
			want: aimv1alpha1.AIMModelKindDerived,
		},
		{
			name: "legacy-flow: v1alpha1 spec.profileCopy with role=base → Custom",
			spec: &aimv1alpha1.AIMModelSpec{
				ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
					Selector: aimv1alpha1.ProfileSelector{
						Role: aimv1alpha1.ProfileSelectorRoleBase,
					},
				},
			},
			want: aimv1alpha1.AIMModelKindCustom,
		},
		{
			name: "image wins over profiles when both are set (defensive — CEL should prevent)",
			spec: &aimv1alpha1.AIMModelSpec{
				Image: "ghcr.io/acme/aim-llama:0.11",
				Profiles: &aimv1alpha1.AIMModelProfilesSpec{
					DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
						Selector: aimv1alpha1.ProfileSelector{
							Role: aimv1alpha1.ProfileSelectorRoleBase,
						},
					},
				},
			},
			want: aimv1alpha1.AIMModelKindImage,
		},
		{
			name: "empty spec → empty Kind (no classification possible)",
			spec: &aimv1alpha1.AIMModelSpec{},
			want: "",
		},
		{
			name: "nil spec → empty Kind (defensive)",
			spec: nil,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyModelKind(tc.spec)
			if got != tc.want {
				t.Fatalf("classifyModelKind() = %q, want %q", got, tc.want)
			}
		})
	}
}
