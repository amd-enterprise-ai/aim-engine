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
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestMatchesProfileCopySelector_PartialEngineArgs(t *testing.T) {
	t.Parallel()

	ok, err := MatchesProfileCopySelector(ProfileCopyCandidate{
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			AimId:   "qwen/qwen3-32b",
			ModelId: "qwen/qwen3-32b-fp8",
			EngineArgs: mustJSON(t, map[string]any{
				"tensor-parallel-size": 2,
				"max-model-len":        8192,
			}),
		},
	}, aimv1alpha1.ProfileSelector{
		AimId: "qwen/qwen3-32b",
		EngineArgs: mustJSON(t, map[string]any{
			"tensor-parallel-size": 2,
		}),
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if !ok {
		t.Fatal("MatchesProfileCopySelector() = false, want true")
	}

	ok, err = MatchesProfileCopySelector(ProfileCopyCandidate{
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			EngineArgs: mustJSON(t, map[string]any{"tensor-parallel-size": 2}),
		},
	}, aimv1alpha1.ProfileSelector{
		EngineArgs: mustJSON(t, map[string]any{"tensor-parallel-size": 4}),
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() mismatch error = %v", err)
	}
	if ok {
		t.Fatal("MatchesProfileCopySelector() = true, want false for mismatched engineArgs")
	}
}

func TestMeetsMinimumType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		candidate aimv1alpha1.AIMProfileType
		minimum   aimv1alpha1.AIMProfileTypeFloor
		want      bool
	}{
		{"empty floor accepts anything", aimv1alpha1.AIMProfileTypeUnoptimized, "", true},
		{"any floor accepts anything", aimv1alpha1.AIMProfileTypeUnoptimized, aimv1alpha1.AIMProfileTypeFloorAny, true},
		{"optimized floor rejects unoptimized", aimv1alpha1.AIMProfileTypeUnoptimized, aimv1alpha1.AIMProfileTypeFloorOptimized, false},
		{"optimized floor rejects preview", aimv1alpha1.AIMProfileTypePreview, aimv1alpha1.AIMProfileTypeFloorOptimized, false},
		{"optimized floor accepts optimized", aimv1alpha1.AIMProfileTypeOptimized, aimv1alpha1.AIMProfileTypeFloorOptimized, true},
		{"optimized floor rejects untyped (treated as unoptimized)", aimv1alpha1.AIMProfileType(""), aimv1alpha1.AIMProfileTypeFloorOptimized, false},
		{"unoptimized floor accepts untyped", aimv1alpha1.AIMProfileType(""), aimv1alpha1.AIMProfileTypeFloorUnoptimized, true},
		{"any floor accepts untyped", aimv1alpha1.AIMProfileType(""), aimv1alpha1.AIMProfileTypeFloorAny, true},
		{"preview floor accepts general", aimv1alpha1.AIMProfileTypeGeneral, aimv1alpha1.AIMProfileTypeFloorPreview, true},
		{"preview floor accepts preview", aimv1alpha1.AIMProfileTypePreview, aimv1alpha1.AIMProfileTypeFloorPreview, true},
		{"preview floor rejects unoptimized", aimv1alpha1.AIMProfileTypeUnoptimized, aimv1alpha1.AIMProfileTypeFloorPreview, false},
		{"unoptimized floor accepts unoptimized", aimv1alpha1.AIMProfileTypeUnoptimized, aimv1alpha1.AIMProfileTypeFloorUnoptimized, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MeetsMinimumType(tt.candidate, tt.minimum); got != tt.want {
				t.Errorf("MeetsMinimumType(%q, %q) = %v, want %v", tt.candidate, tt.minimum, got, tt.want)
			}
		})
	}
}

func TestMatchesProfileCopySelector_MinimumTypeFloor(t *testing.T) {
	t.Parallel()
	unoptimized := ProfileCopyCandidate{Spec: aimv1alpha2.AIMProfileSpecCommon{
		AimId: "meta-llama/Llama-3.2-1B-Instruct",
		Type:  aimv1alpha1.AIMProfileTypeUnoptimized,
	}}

	// Default-equivalent optimized floor excludes the unoptimized EPYC-style profile.
	ok, err := MatchesProfileCopySelector(unoptimized, aimv1alpha1.ProfileSelector{
		AimId:       "meta-llama/Llama-3.2-1B-Instruct",
		MinimumType: aimv1alpha1.AIMProfileTypeFloorOptimized,
	})
	if err != nil || ok {
		t.Fatalf("optimized floor should exclude unoptimized; ok=%v err=%v", ok, err)
	}

	// Opt-in via minimumType=any includes it.
	ok, err = MatchesProfileCopySelector(unoptimized, aimv1alpha1.ProfileSelector{
		AimId:       "meta-llama/Llama-3.2-1B-Instruct",
		MinimumType: aimv1alpha1.AIMProfileTypeFloorAny,
	})
	if err != nil || !ok {
		t.Fatalf("any floor should include unoptimized; ok=%v err=%v", ok, err)
	}

	// Empty floor (derivation selectors) also includes it.
	ok, err = MatchesProfileCopySelector(unoptimized, aimv1alpha1.ProfileSelector{
		AimId: "meta-llama/Llama-3.2-1B-Instruct",
	})
	if err != nil || !ok {
		t.Fatalf("empty floor should include unoptimized; ok=%v err=%v", ok, err)
	}
}

// TestMatchesProfileCopySelector_StrictIdentityEquality locks in the
// post-CEL contract: selector identity fields are pure source-side
// filters. An empty selector field is a wildcard (matches anything), a
// non-empty selector field requires strict equality on the candidate.
// The previous role=base wildcard semantics (where empty source identity
// matched a non-empty selector identity) are gone — CEL on
// AIMProfileSetSpec / AIMModelProfilesSpec now rejects selector identity
// fields when role=base, so the wildcard branch is dead and target
// identity for derived profiles lives in `overrides.{aimId,modelId,profileId}`.
func TestMatchesProfileCopySelector_StrictIdentityEquality(t *testing.T) {
	t.Parallel()

	baseProfile := ProfileCopyCandidate{
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			Engine:           "vllm",
			AcceleratorModel: "MI300X",
			AcceleratorCount: 1,
		},
	}

	// Empty selector identity is a wildcard against any candidate,
	// including an empty-identity base profile. This is the supported
	// shape for role=base derivations now (CEL forbids selector.aimId
	// when role=base, so the selector reaches this code with AimId="").
	ok, err := MatchesProfileCopySelector(baseProfile, aimv1alpha1.ProfileSelector{
		Role:             aimv1alpha1.ProfileSelectorRoleBase,
		AcceleratorModel: "MI300X",
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if !ok {
		t.Fatal("empty selector identity should wildcard-match empty base profile, got false")
	}

	// Non-empty selector identity is a strict filter: the candidate's
	// identity must equal it. An empty candidate identity no longer
	// matches a non-empty selector — even with role=base (a leftover
	// from the dead wildcard branch). This safeguards in case CEL is
	// bypassed (kubectl --validate=false, older API client) so we
	// don't silently derive against the wrong candidate.
	ok, err = MatchesProfileCopySelector(baseProfile, aimv1alpha1.ProfileSelector{
		Role:  aimv1alpha1.ProfileSelectorRoleBase,
		AimId: testAimIDCustomTransformer,
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if ok {
		t.Fatal("non-empty selector.aimId should not match empty candidate.AimId even with role=base, got true")
	}

	// Non-empty selector identity matches a non-empty candidate
	// identity when they are equal (role=deployable filter case).
	deployable := ProfileCopyCandidate{
		Spec: aimv1alpha2.AIMProfileSpecCommon{
			AimId:            testAimIDCustomTransformer,
			ModelId:          testAimIDCustomTransformer,
			Engine:           "vllm",
			AcceleratorModel: "MI300X",
			AcceleratorCount: 1,
		},
	}
	ok, err = MatchesProfileCopySelector(deployable, aimv1alpha1.ProfileSelector{
		AimId: testAimIDCustomTransformer,
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if !ok {
		t.Fatal("matching selector.aimId on a deployable candidate should match, got false")
	}

	// Non-empty selector identity rejects a mismatched candidate.
	ok, err = MatchesProfileCopySelector(deployable, aimv1alpha1.ProfileSelector{
		AimId: "acme/different",
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if ok {
		t.Fatal("mismatched selector.aimId on a deployable candidate should reject, got true")
	}

	// Engine and other intrinsic fields stay strict (unchanged behaviour).
	ok, err = MatchesProfileCopySelector(baseProfile, aimv1alpha1.ProfileSelector{
		Role:   aimv1alpha1.ProfileSelectorRoleBase,
		Engine: "trtllm",
	})
	if err != nil {
		t.Fatalf("MatchesProfileCopySelector() error = %v", err)
	}
	if ok {
		t.Fatal("MatchesProfileCopySelector(Base, engine mismatch) = true, want false (engine is intrinsic to base profile, no wildcard)")
	}
}

// TestApplyProfileCopyOverrides_IdentityFromOverrides covers the
// override-driven identity-stamping contract that replaced the
// selector-based PromoteBaseProfileIdentity hack. For role=base
// derivations the source base profile carries no identity; the user
// expresses the TARGET identity via overrides.{aimId,modelId,profileId}.
// Override values must win unconditionally — including overwriting any
// identity the source profile happens to carry (deployable remix case).
func TestApplyProfileCopyOverrides_IdentityFromOverrides(t *testing.T) {
	t.Parallel()

	source := aimv1alpha2.AIMProfileSpecCommon{
		Engine:           "vllm",
		AcceleratorModel: "MI300X",
		AcceleratorCount: 1,
	}

	spec, err := ApplyProfileCopyOverrides(
		source,
		&aimv1alpha1.ProfileOverrides{
			AimId:     testAimIDCustomTransformer,
			ModelId:   "acme/custom-transformer-3b",
			ProfileId: "vllm-mi300x-bf16-tp1-latency",
		},
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}
	if spec.AimId != testAimIDCustomTransformer {
		t.Fatalf("AimId = %q, want overrides.AimId stamped onto empty source", spec.AimId)
	}
	if spec.ModelId != "acme/custom-transformer-3b" {
		t.Fatalf("ModelId = %q, want overrides.ModelId stamped onto empty source", spec.ModelId)
	}
	if spec.ProfileId != "vllm-mi300x-bf16-tp1-latency" {
		t.Fatalf("ProfileId = %q, want overrides.ProfileId stamped onto empty source", spec.ProfileId)
	}
}

// TestApplyProfileCopyOverrides_IdentityOverridesWinOverSource verifies
// that overrides.{aimId,modelId,profileId} unconditionally replace the
// source profile's identity — this matters for role=deployable remixes
// where the source already carries identity but the user wants the
// derived profile to be identified differently.
func TestApplyProfileCopyOverrides_IdentityOverridesWinOverSource(t *testing.T) {
	t.Parallel()

	source := aimv1alpha2.AIMProfileSpecCommon{
		AimId:     "acme/upstream",
		ModelId:   "acme/upstream-model",
		ProfileId: "acme/upstream-profile",
		Engine:    "vllm",
	}

	spec, err := ApplyProfileCopyOverrides(
		source,
		&aimv1alpha1.ProfileOverrides{
			AimId:     "acme/remix",
			ModelId:   "acme/remix-model",
			ProfileId: "acme/remix-profile",
		},
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}
	if spec.AimId != "acme/remix" || spec.ModelId != "acme/remix-model" || spec.ProfileId != "acme/remix-profile" {
		t.Fatalf("identity overrides did not win over source: AimId=%q ModelId=%q ProfileId=%q",
			spec.AimId, spec.ModelId, spec.ProfileId)
	}
}

// TestApplyProfileCopyOverrides_ModelSourcesAutoDeriveFallback verifies
// that when overrides.ModelId is unset, modelSources[0].modelId still
// auto-derives onto the result (historical convenience for
// role=deployable remixes). An explicit overrides.ModelId wins over the
// auto-derivation — required for role=base by CEL.
func TestApplyProfileCopyOverrides_ModelSourcesAutoDeriveFallback(t *testing.T) {
	t.Parallel()

	source := aimv1alpha2.AIMProfileSpecCommon{Engine: "vllm"}

	// No overrides.ModelId → auto-derive from modelSources[0].
	spec, err := ApplyProfileCopyOverrides(
		source,
		&aimv1alpha1.ProfileOverrides{
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "acme/auto-derived", SourceURI: "hf://acme/auto-derived"},
			},
		},
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}
	if spec.ModelId != "acme/auto-derived" {
		t.Fatalf("ModelId = %q, want auto-derive from modelSources[0].modelId", spec.ModelId)
	}

	// Explicit overrides.ModelId wins.
	spec, err = ApplyProfileCopyOverrides(
		source,
		&aimv1alpha1.ProfileOverrides{
			ModelId: "acme/explicit",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "acme/from-sources", SourceURI: "hf://acme/from-sources"},
			},
		},
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}
	if spec.ModelId != "acme/explicit" {
		t.Fatalf("ModelId = %q, want overrides.ModelId to win over auto-derivation", spec.ModelId)
	}
}

func TestApplyProfileCopyOverrides_MergesAndReplaces(t *testing.T) {
	t.Parallel()

	count := int32(4)
	spec, err := ApplyProfileCopyOverrides(
		aimv1alpha2.AIMProfileSpecCommon{
			// Fine-tuned-style source: AimId + ModelSources make this a
			// deployable seed (IsProfileDeployable == true), so the
			// rebase from sourceBaseImage onto source.Image kicks in.
			AimId:            "official/model",
			ModelId:          "official/model",
			Image:            "ghcr.io/amd/full-image:0.9.1",
			ModelSources:     []aimv1alpha1.AIMModelSource{{ModelID: "official/model", SourceURI: "hf://official/model"}},
			AcceleratorModel: "MI300X",
			AcceleratorCount: 1,
			ContainerEnv: []corev1.EnvVar{
				{Name: "KEEP", Value: "1"},
				{Name: "OVERRIDE", Value: "old"},
			},
			EngineEnv: map[string]string{
				"KEEP":     "1",
				"OVERRIDE": "old",
			},
			EngineArgs: mustJSON(t, map[string]any{
				"tensor-parallel-size": 2,
				"max-model-len":        8192,
			}),
		},
		&aimv1alpha1.ProfileOverrides{
			ModelSources:     []aimv1alpha1.AIMModelSource{{ModelID: "custom/model", SourceURI: "s3://bucket/custom-model"}},
			AcceleratorModel: "MI325X",
			AcceleratorCount: &count,
			ContainerEnv: []corev1.EnvVar{
				{Name: "OVERRIDE", Value: "new"},
				{Name: "ADDED", Value: "2"},
			},
			EngineEnv: map[string]string{
				"OVERRIDE": "new",
				"ADDED":    "2",
			},
			EngineArgs: mustJSON(t, map[string]any{
				"tensor-parallel-size": 4,
			}),
		},
		"",
		"ghcr.io/amd/aim-base:0.9.1",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}

	// Base image tag is normalised to MAJOR.MINOR (`0.9.1` → `0.9`) so the
	// rebased reference points at the stable aim-base rolling tag that the
	// registry actually publishes.
	if spec.Image != "ghcr.io/amd/aim-base:0.9" {
		t.Fatalf("Image = %q, want base image fallback normalised to MAJOR.MINOR", spec.Image)
	}
	if spec.ModelId != "custom/model" {
		t.Fatalf("ModelId = %q, want overridden model source model ID", spec.ModelId)
	}
	if spec.AcceleratorModel != "MI325X" || spec.AcceleratorCount != 4 {
		t.Fatalf("Accelerator override = (%q,%d), want (MI325X,4)", spec.AcceleratorModel, spec.AcceleratorCount)
	}
	if len(spec.ContainerEnv) != 3 || spec.ContainerEnv[1].Value != "new" || spec.ContainerEnv[2].Name != "ADDED" {
		t.Fatalf("ContainerEnv = %#v, want merged env vars with override winner", spec.ContainerEnv)
	}
	if spec.EngineEnv["KEEP"] != "1" || spec.EngineEnv["OVERRIDE"] != "new" || spec.EngineEnv["ADDED"] != "2" {
		t.Fatalf("EngineEnv = %#v, want merged env map", spec.EngineEnv)
	}

	gotArgs := map[string]any{}
	if err := json.Unmarshal(spec.EngineArgs.Raw, &gotArgs); err != nil {
		t.Fatalf("unmarshal merged EngineArgs: %v", err)
	}
	if gotArgs["tensor-parallel-size"] != float64(4) || gotArgs["max-model-len"] != float64(8192) {
		t.Fatalf("EngineArgs = %#v, want shallow merge with override winner", gotArgs)
	}
}

// TestApplyProfileCopyOverrides_OverlayOntoBaseProfileSourceProducesDeployable
// covers the iteration-2 custom-model derivation flow: when the source
// catalog item is a base profile (carries aim_id from the base image but no
// model_id / model_sources), overriding modelSources alone must auto-fill
// modelId from modelSources[0] and result in a spec where
// IsProfileDeployable returns true.
func TestApplyProfileCopyOverrides_OverlayOntoBaseProfileSourceProducesDeployable(t *testing.T) {
	t.Parallel()

	baseProfileSource := aimv1alpha2.AIMProfileSpecCommon{
		AimId:            testAimIDCustomTransformer,
		ProfileId:        "vllm-mi300x-bf16-tp1-latency",
		Engine:           "vllm",
		AcceleratorModel: "MI300X",
		AcceleratorCount: 1,
	}
	if IsProfileDeployable(baseProfileSource) {
		t.Fatal("test precondition: baseProfileSource must not be deployable before overlay")
	}

	derived, err := ApplyProfileCopyOverrides(
		baseProfileSource,
		&aimv1alpha1.ProfileOverrides{
			ModelSources: []aimv1alpha1.AIMModelSource{{
				ModelID:   testAimIDCustomTransformer,
				SourceURI: "s3://acme-models/custom-transformer",
			}},
		},
		"",
		"ghcr.io/silogen/aim-base-vllm:0.1.0",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}

	if derived.AimId != testAimIDCustomTransformer {
		t.Fatalf("derived.AimId = %q, want aimId inherited from base-profile source", derived.AimId)
	}
	if derived.ModelId != testAimIDCustomTransformer {
		t.Fatalf("derived.ModelId = %q, want auto-filled from overrides.modelSources[0].modelId", derived.ModelId)
	}
	if len(derived.ModelSources) != 1 {
		t.Fatalf("derived.ModelSources = %#v, want single overridden source", derived.ModelSources)
	}
	if derived.ModelSources[0].SourceURI != "s3://acme-models/custom-transformer" {
		t.Fatalf("derived.ModelSources[0].SourceURI = %q, want override SourceURI", derived.ModelSources[0].SourceURI)
	}
	if !IsProfileDeployable(derived) {
		t.Fatal("IsProfileDeployable(derived) = false; overlay onto base profile must produce a deployable spec")
	}
	if got := ProfileRoleLabelValue(derived); got != "deployable" {
		t.Fatalf("ProfileRoleLabelValue(derived) = %q, want \"deployable\"", got)
	}
}

func TestFilterProfileCopyCandidates_LatestVersion(t *testing.T) {
	t.Parallel()

	matched, err := FilterProfileCopyCandidates([]ProfileCopyCandidate{
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "0.9.1"}},
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "0.10.0"}},
	}, ProfileCopyRequest{
		Selector:      aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
		VersionPolicy: aimv1alpha1.ProfileVersionPolicyLatest,
	})
	if err != nil {
		t.Fatalf("FilterProfileCopyCandidates() error = %v", err)
	}
	if len(matched) != 1 || matched[0].Status.Version != "0.10.0" {
		t.Fatalf("matched = %#v, want only latest version", matched)
	}
}

func TestFilterProfileCopyCandidates_PinnedRequiresVersion(t *testing.T) {
	t.Parallel()

	_, err := FilterProfileCopyCandidates([]ProfileCopyCandidate{
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}},
	}, ProfileCopyRequest{
		Selector:      aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
		VersionPolicy: aimv1alpha1.ProfileVersionPolicyPinned,
	})
	if err == nil {
		t.Fatal("FilterProfileCopyCandidates() error = nil, want pinned policy without version to fail")
	}
}

func TestFilterProfileCopyCandidates_AllReturnsAllMatches(t *testing.T) {
	t.Parallel()

	matched, err := FilterProfileCopyCandidates([]ProfileCopyCandidate{
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "release-9"}},
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "release-10"}},
	}, ProfileCopyRequest{
		Selector:      aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
		VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
	})
	if err != nil {
		t.Fatalf("FilterProfileCopyCandidates() error = %v", err)
	}
	if len(matched) != 2 {
		t.Fatalf("len(matched) = %d, want 2", len(matched))
	}
}

func TestFilterProfileCopyCandidates_LatestVersionUsesNaturalOrderingForNonSemver(t *testing.T) {
	t.Parallel()

	matched, err := FilterProfileCopyCandidates([]ProfileCopyCandidate{
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "release-9"}},
		{Spec: aimv1alpha2.AIMProfileSpecCommon{AimId: "qwen/qwen3-32b"}, Status: aimv1alpha2.AIMProfileStatus{Version: "release-10"}},
	}, ProfileCopyRequest{
		Selector:      aimv1alpha1.ProfileSelector{AimId: "qwen/qwen3-32b"},
		VersionPolicy: aimv1alpha1.ProfileVersionPolicyLatest,
	})
	if err != nil {
		t.Fatalf("FilterProfileCopyCandidates() error = %v", err)
	}
	if len(matched) != 1 || matched[0].Status.Version != "release-10" {
		t.Fatalf("matched = %#v, want only release-10", matched)
	}
}

func TestResolveDerivedProfileImage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name               string
		imageOverride      string
		sourceBaseImage    string
		sourceImage        string
		sourceIsDeployable bool
		weightsChanged     bool
		want               string
	}{
		{
			name:               "imageOverride wins outright",
			imageOverride:      "quay.io/team/custom:1.0",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "quay.io/team/custom:1.0",
		},
		{
			name:               "imageOverride wins even when source is base-role",
			imageOverride:      "quay.io/team/custom:1.0",
			sourceBaseImage:    "docker.io/vllm/vllm-openai-rocm:v0.16.0",
			sourceImage:        "amdenterpriseai/aim-base:0.11",
			sourceIsDeployable: false,
			weightsChanged:     true,
			want:               "quay.io/team/custom:1.0",
		},
		{
			// Weights replaced (modelSources overridden): resolve the
			// optimized image back to its aim-base runtime.
			name:               "deployable source + weights changed: sourceBaseImage rebased onto source registry+org",
			sourceBaseImage:    "docker.io/amdenterpriseai/aim-base:0.11",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/aim-base:0.11",
		},
		{
			// The fix: weights untouched (no modelSources override) keeps
			// the optimized image so partitioning-only / env-only
			// overrides don't silently downgrade to the base runtime.
			name:               "deployable source + weights unchanged: keeps optimized image despite base image ref",
			sourceBaseImage:    "docker.io/amdenterpriseai/aim-base:0.11",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11",
			sourceIsDeployable: true,
			weightsChanged:     false,
			want:               "ghcr.io/silogen/qwen3:0.11",
		},
		{
			name:               "deployable source + weights changed: sourceBaseImage path keeps source registry+org",
			sourceBaseImage:    "ghcr.io/silogen/aim-base:0.11",
			sourceImage:        "docker.io/amdenterpriseai/qwen3:0.11",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "docker.io/amdenterpriseai/aim-base:0.11",
		},
		{
			// Build pipelines stamp release-candidate tags into
			// AIM_BASE_IMAGE_REF (`0.11-rc21`) but aim-base is published
			// under its stable MAJOR.MINOR rolling tag. Normalisation
			// strips the RC suffix so the rebased reference resolves in
			// the mirrored registry.
			name:               "deployable source + weights changed: rc-tagged sourceBaseImage truncates to major.minor",
			sourceBaseImage:    "ghcr.io/silogen/aim-base:0.11-rc21",
			sourceImage:        "amdenterpriseai/aim-qwen-qwen3-32b:0.11.0",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "amdenterpriseai/aim-base:0.11",
		},
		{
			name:               "deployable source + weights changed: patch-tagged sourceBaseImage truncates to major.minor",
			sourceBaseImage:    "ghcr.io/silogen/aim-base:0.11.2",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11.0",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/aim-base:0.11",
		},
		{
			// Non-semver tags can't be normalised; we leave them alone
			// rather than guess. This is rare in practice (real
			// AIM_BASE_IMAGE_REF values come out of release tags) but
			// keeps the resolver predictable.
			name:               "deployable source + weights changed: non-semver sourceBaseImage tag preserved",
			sourceBaseImage:    "ghcr.io/silogen/aim-base:nightly",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/aim-base:nightly",
		},
		{
			name:               "deployable source + weights changed: legacy fallback synthesizes aim-base from semver tag",
			sourceImage:        "ghcr.io/silogen/qwen3:0.11.2",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:               "deployable source + weights changed: no base image and unparseable tag returns sourceImage unchanged",
			sourceImage:        "ghcr.io/silogen/qwen3:latest",
			sourceIsDeployable: true,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/qwen3:latest",
		},
		{
			name: "all empty returns empty",
		},
		// BYO custom-model flow (regression guard): when the source profile
		// is a base-role profile, source.Image IS already the runtime
		// image (e.g. aim-base itself). Its sourceBaseImage is the
		// AIM_BASE_IMAGE_REF of aim-base (vllm-openai-rocm), which is the
		// upstream FROM line — not a deployable AIM image. Rebasing onto
		// the source registry+org used to produce
		// `amdenterpriseai/vllm-openai-rocm:v0.16.0`, a nonexistent
		// mirror that broke the predictor pod. The fix returns
		// sourceImage unchanged for base-role candidates so the derived
		// profile deploys on the base image declared by its base
		// AIMModel.
		{
			name:               "base source: AIM_BASE_IMAGE_REF rebase is skipped (BYO custom-model)",
			sourceBaseImage:    "docker.io/vllm/vllm-openai-rocm:v0.16.0",
			sourceImage:        "amdenterpriseai/aim-base:0.11",
			sourceIsDeployable: false,
			weightsChanged:     true,
			want:               "amdenterpriseai/aim-base:0.11",
		},
		{
			name:               "base source: legacy aim-base synthesis is also skipped",
			sourceImage:        "ghcr.io/silogen/aim-base:0.11.2",
			sourceIsDeployable: false,
			weightsChanged:     true,
			want:               "ghcr.io/silogen/aim-base:0.11.2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveDerivedProfileImage(tc.imageOverride, tc.sourceBaseImage, tc.sourceImage, tc.sourceIsDeployable, tc.weightsChanged)
			if got != tc.want {
				t.Fatalf("ResolveDerivedProfileImage(%q, %q, %q, %v, %v) = %q, want %q",
					tc.imageOverride, tc.sourceBaseImage, tc.sourceImage, tc.sourceIsDeployable, tc.weightsChanged, got, tc.want)
			}
		})
	}
}

// TestApplyProfileCopyOverrides_BaseSourceImageNotRebased pins the BYO
// custom-model flow at the ApplyProfileCopyOverrides boundary: a base-role
// source (empty aim_id + empty model_sources) keeps source.Image even when
// an AIM_BASE_IMAGE_REF is recorded on the source. This is the integration
// equivalent of the "base source" cases on ResolveDerivedProfileImage.
func TestApplyProfileCopyOverrides_BaseSourceImageNotRebased(t *testing.T) {
	t.Parallel()

	baseSource := aimv1alpha2.AIMProfileSpecCommon{
		// No AimId / ModelId / ModelSources → role=base.
		Engine:           "vllm",
		AcceleratorModel: "MI300X",
		AcceleratorCount: 2,
		Image:            "amdenterpriseai/aim-base:0.11",
	}
	if IsProfileDeployable(baseSource) {
		t.Fatal("test precondition: base source must not be deployable")
	}

	derived, err := ApplyProfileCopyOverrides(
		baseSource,
		&aimv1alpha1.ProfileOverrides{
			ModelSources: []aimv1alpha1.AIMModelSource{{
				ModelID:   "Qwen/Qwen3-0.6B",
				SourceURI: "hf://Qwen/Qwen3-0.6B",
			}},
		},
		"", // no service-level image override
		// sourceBaseImage is the FROM line of aim-base (vllm-openai-rocm).
		// The pre-fix bug grafted this onto amdenterpriseai/ and produced
		// a nonexistent `amdenterpriseai/vllm-openai-rocm:v0.16.0`.
		"docker.io/vllm/vllm-openai-rocm:v0.16.0",
	)
	if err != nil {
		t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
	}
	if derived.Image != "amdenterpriseai/aim-base:0.11" {
		t.Fatalf("derived.Image = %q, want base source image preserved (got the broken rebase result?)", derived.Image)
	}
}

// TestApplyProfileCopyOverrides_DeployableSourceKeepsImageWhenWeightsUnchanged
// pins the partitioning/env-only override flow: deriving from a deployable
// (optimized) source profile WITHOUT replacing the weights keeps the
// optimized source image. Only an override that supplies modelSources (new
// weights) resolves the derived profile back to aim-base. This is the
// integration equivalent of the weightsChanged cases on
// ResolveDerivedProfileImage.
func TestApplyProfileCopyOverrides_DeployableSourceKeepsImageWhenWeightsUnchanged(t *testing.T) {
	t.Parallel()

	// Deployable source: aim_id + modelSources present (role=deployable).
	deployableSource := aimv1alpha2.AIMProfileSpecCommon{
		AimId:            "openai/gpt-oss-20b",
		ModelId:          "openai/gpt-oss-20b",
		Engine:           "vllm",
		AcceleratorModel: "MI300X",
		AcceleratorCount: 1,
		Image:            "amdenterpriseai/aim-openai-gpt-oss-20b:0.11.1",
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "openai/gpt-oss-20b",
			SourceURI: "hf://openai/gpt-oss-20b",
		}},
	}
	if !IsProfileDeployable(deployableSource) {
		t.Fatal("test precondition: source must be deployable")
	}
	// AIM_BASE_IMAGE_REF the inspector would record on the optimized image.
	const sourceBaseImage = "ghcr.io/silogen/aim-base:0.11"

	t.Run("partitioning-only override keeps optimized image", func(t *testing.T) {
		t.Parallel()
		derived, err := ApplyProfileCopyOverrides(
			deployableSource,
			&aimv1alpha1.ProfileOverrides{AcceleratorPartitioningMode: "CPX-NPS4"},
			"", // no explicit image override
			sourceBaseImage,
		)
		if err != nil {
			t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
		}
		if derived.Image != "amdenterpriseai/aim-openai-gpt-oss-20b:0.11.1" {
			t.Fatalf("derived.Image = %q, want optimized source image preserved", derived.Image)
		}
	})

	t.Run("modelSources override resolves back to aim-base", func(t *testing.T) {
		t.Parallel()
		derived, err := ApplyProfileCopyOverrides(
			deployableSource,
			&aimv1alpha1.ProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "acme/my-finetune",
					SourceURI: "hf://acme/my-finetune",
				}},
			},
			"", // no explicit image override
			sourceBaseImage,
		)
		if err != nil {
			t.Fatalf("ApplyProfileCopyOverrides() error = %v", err)
		}
		if derived.Image != "amdenterpriseai/aim-base:0.11" {
			t.Fatalf("derived.Image = %q, want resolve back to aim-base when weights replaced", derived.Image)
		}
	})
}

func mustJSON(t *testing.T, in map[string]any) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}
}
