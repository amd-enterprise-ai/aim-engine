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
					BaseImageRef: "ghcr.io/silogen/aim-base:0.11",
				},
			},
			wantBaseImage:    "ghcr.io/silogen/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name: "v1alpha2 status.baseImage propagates into imageMetadata",
			spec: aimv1alpha1.AIMModelSpec{},
			status: aimv1alpha1.AIMModelStatus{
				BaseImage: "ghcr.io/silogen/aim-base:0.11",
			},
			wantBaseImage:    "ghcr.io/silogen/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/silogen/aim-base:0.11",
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
				BaseImage: "ghcr.io/silogen/aim-base:0.11",
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					BaseImageRef: "ghcr.io/silogen/aim-base:0.11",
				},
			},
			wantBaseImage:    "ghcr.io/silogen/aim-base:0.11",
			wantImageMetaRef: "ghcr.io/silogen/aim-base:0.11",
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

// TestHasLegacyInputs_DocumentedSignalsTrigger pins the legacy-pipeline
// gate to its documented v1alpha1-only signals. Adding new entries here
// without thinking through the dual-pipeline interaction would silently
// route pure v1alpha2 specs through the OCI inspection path.
func TestHasLegacyInputs_DocumentedSignalsTrigger(t *testing.T) {
	cases := map[string]aimv1alpha1.AIMModelSpec{
		"image":                  {Image: "ghcr.io/x/y:tag"},
		"customTemplates":        {CustomTemplates: []aimv1alpha1.AIMCustomTemplate{{Name: "t"}}},
		"modelSources":           {ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "m", SourceURI: "hf://m"}}},
		"aimId":                  {AimId: "aim/one"},
		"imageMetadata":          {ImageMetadata: &aimv1alpha1.ImageMetadata{}},
		"discovery":              {Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{}},
		"defaultServiceTemplate": {DefaultServiceTemplate: "tmpl"},
		"custom":                 {Custom: &aimv1alpha1.AIMCustomModelSpec{}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if !hasLegacyInputs(spec) {
				t.Fatalf("expected hasLegacyInputs to be true when %s is set", name)
			}
		})
	}
}

// TestHasLegacyInputs_RuntimeConfigNameDoesNotTrigger guards against the
// fix where the embedded RuntimeConfigRef.Name field (i.e.
// spec.runtimeConfigName) used to trigger the legacy pipeline gate. The
// field is supported by both v1alpha1 and v1alpha2 and on its own says
// nothing about whether the legacy pipeline has real work to do.
func TestHasLegacyInputs_RuntimeConfigNameDoesNotTrigger(t *testing.T) {
	spec := aimv1alpha1.AIMModelSpec{
		Profiles: &aimv1alpha1.AIMModelProfilesSpec{
			DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
				Selector: aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
		RuntimeConfigRef: aimv1alpha1.RuntimeConfigRef{Name: "my-runtime-config"},
	}
	if hasLegacyInputs(spec) {
		t.Fatalf("pure v1alpha2 spec with runtimeConfigName must not trigger the legacy pipeline; got hasLegacyInputs = true")
	}
}

// TestHasLegacyInputs_PureProfileCopyDoesNotTrigger keeps the documented
// "profileCopy-only" pure-v1alpha2 case skipping the legacy pipeline so
// it does not surface spurious ImageMetadataReady=False conditions.
func TestHasLegacyInputs_PureProfileCopyDoesNotTrigger(t *testing.T) {
	spec := aimv1alpha1.AIMModelSpec{
		ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
			Selector: aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		},
	}
	if hasLegacyInputs(spec) {
		t.Fatalf("pure profileCopy spec must not trigger the legacy pipeline; got hasLegacyInputs = true")
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
