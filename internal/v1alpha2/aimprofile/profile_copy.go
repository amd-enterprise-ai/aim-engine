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
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
)

// ProfileCopyCandidate is a resource-neutral profile source used by derivation hosts such as AIMModel.
type ProfileCopyCandidate struct {
	Name      string
	Spec      aimv1alpha2.AIMProfileSpecCommon
	Status    aimv1alpha2.AIMProfileStatus
	BaseImage string
}

// ProfileCopyRequest describes a resource-neutral profile derivation request.
type ProfileCopyRequest struct {
	Selector      aimv1alpha1.ProfileSelector
	VersionPolicy aimv1alpha1.ProfileVersionPolicy
	Version       string
	Overrides     *aimv1alpha1.ProfileOverrides
	ImageOverride string
}

// MatchesProfileCopySelector returns true when a source profile satisfies all selector fields.
//
// Note: provenance filters (selector.modelRef, selector.role, selector.origin)
// are applied earlier by the caller against the candidate's labels — the
// underlying aimprofile.ProfileCopyCandidate type does not carry labels.
// AIMProfileSet reconcilers filter the source list with
// FilterProvenanceLabels before constructing candidates.
//
// All selector fields use strict equality: an empty selector field is a
// wildcard (matches anything), a non-empty selector field requires the
// candidate's field to equal it. The previous code had a special
// "role=base ⇒ empty source identity counts as a match" branch so that
// `selector.aimId` could double as the TARGET identity for the derived
// profile via PromoteBaseProfileIdentity. CEL on AIMProfileSetSpec and
// AIMModelProfilesSpec now rejects `selector.{aimId,modelId,profileId}`
// when `selector.role=base`, so for role=base derivations the selector
// identity fields are always empty here and the wildcard branch was dead.
// Target identity now lives in `overrides.{aimId,modelId,profileId}` and
// is stamped by ApplyProfileCopyOverrides.
func MatchesProfileCopySelector(candidate ProfileCopyCandidate, selector aimv1alpha1.ProfileSelector) (bool, error) {
	if selector.AimId != "" && candidate.Spec.AimId != selector.AimId {
		return false, nil
	}
	if selector.ModelId != "" && candidate.Spec.ModelId != selector.ModelId {
		return false, nil
	}
	if selector.ProfileId != "" && candidate.Spec.ProfileId != selector.ProfileId {
		return false, nil
	}
	if selector.Engine != "" && candidate.Spec.Engine != selector.Engine {
		return false, nil
	}
	if selector.Metric != "" && candidate.Spec.Metric != selector.Metric {
		return false, nil
	}
	if selector.Precision != "" && candidate.Spec.Precision != selector.Precision {
		return false, nil
	}
	if selector.Type != "" && candidate.Spec.Type != selector.Type {
		return false, nil
	}
	if selector.AcceleratorModel != "" && candidate.Spec.AcceleratorModel != selector.AcceleratorModel {
		return false, nil
	}
	if !matchesPartitioningSelector(selector.AcceleratorPartitioningMode, candidate.Spec.AcceleratorPartitioningMode) {
		return false, nil
	}
	if selector.AcceleratorType != "" && candidate.Spec.AcceleratorType != selector.AcceleratorType {
		return false, nil
	}
	if selector.AcceleratorCount != nil && candidate.Spec.AcceleratorCount != *selector.AcceleratorCount {
		return false, nil
	}
	return MatchesEngineArgs(candidate.Spec.EngineArgs, selector.EngineArgs)
}

// matchesPartitioningSelector implements the partial-order match for
// selector.acceleratorPartitioningMode against a candidate profile's mode.
// Deliberately asymmetric with the spec field (the hyphenless "<C>" prefix
// branch is selector-only):
//
//	selector ""              -> no filter (always matches).
//	selector "unpartitioned" -> candidate mode "" or "unpartitioned".
//	selector "partitioned"   -> candidate mode non-trivial (anything but "" / "unpartitioned").
//	selector "<C>"           -> candidate mode begins with "<C>-" (e.g. "CPX" matches
//	                            "CPX-NPS1", "CPX-NPS4"). Hyphenless = prefix branch.
//	selector "<C>-<M>"       -> exact-string match.
func matchesPartitioningSelector(selectorMode, candidateMode string) bool {
	// Canonicalize both sides so matching is case-insensitive and consistent
	// with the affinity-building seam (see canonicalizePartitioningMode).
	sel := canonicalizePartitioningMode(selectorMode)
	cand := canonicalizePartitioningMode(candidateMode)
	switch sel {
	case "":
		return true
	case PartitioningModeUnpartitioned:
		return cand == "" || cand == PartitioningModeUnpartitioned
	case PartitioningModePartitioned:
		return cand != "" && cand != PartitioningModeUnpartitioned
	default:
		if strings.Contains(sel, "-") {
			return cand == sel
		}
		return strings.HasPrefix(cand, sel+"-")
	}
}

// MatchesEngineArgs returns true when every top-level selector key exists in the source engineArgs
// object with an equal JSON value.
func MatchesEngineArgs(sourceArgs, selectorArgs *apiextensionsv1.JSON) (bool, error) {
	if selectorArgs == nil || len(selectorArgs.Raw) == 0 {
		return true, nil
	}

	sourceMap, err := jsonObject(sourceArgs)
	if err != nil {
		return false, fmt.Errorf("parse source engineArgs: %w", err)
	}
	selectorMap, err := jsonObject(selectorArgs)
	if err != nil {
		return false, fmt.Errorf("parse selector engineArgs: %w", err)
	}

	for key, want := range selectorMap {
		got, ok := sourceMap[key]
		if !ok || !reflect.DeepEqual(got, want) {
			return false, nil
		}
	}
	return true, nil
}

// FilterProfileCopyCandidates applies selector and version-policy matching.
func FilterProfileCopyCandidates(candidates []ProfileCopyCandidate, req ProfileCopyRequest) ([]ProfileCopyCandidate, error) {
	if req.VersionPolicy == aimv1alpha1.ProfileVersionPolicyPinned && req.Version == "" {
		return nil, fmt.Errorf("version is required when versionPolicy is pinned")
	}

	matched := make([]ProfileCopyCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ok, err := MatchesProfileCopySelector(candidate, req.Selector)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, candidate)
		}
	}

	switch req.VersionPolicy {
	case aimv1alpha1.ProfileVersionPolicyLatest:
		version := latestProfileCopyVersion(matched)
		if version == "" {
			return matched[:0], nil
		}
		filtered := matched[:0]
		for _, candidate := range matched {
			if candidate.Status.Version == version {
				filtered = append(filtered, candidate)
			}
		}
		return filtered, nil
	case aimv1alpha1.ProfileVersionPolicyAll:
		return matched, nil
	default:
		if req.Version == "" {
			return matched, nil
		}
		filtered := matched[:0]
		for _, candidate := range matched {
			if candidate.Status.Version == req.Version {
				filtered = append(filtered, candidate)
			}
		}
		return filtered, nil
	}
}

// ApplyProfileCopyOverrides mutates a source profile spec according to typed profileCopy overrides.
//
// imageOverride and overrides.Image both name the deployment-image override
// for the derived profile; overrides.Image (the modern shape used by
// AIMModel.spec.profiles) wins when both are set.
func ApplyProfileCopyOverrides(
	source aimv1alpha2.AIMProfileSpecCommon,
	overrides *aimv1alpha1.ProfileOverrides,
	imageOverride string,
	sourceBaseImage string,
) (aimv1alpha2.AIMProfileSpecCommon, error) {
	result := *source.DeepCopy()
	resolvedImageOverride := imageOverride
	if overrides != nil && overrides.Image != "" {
		resolvedImageOverride = overrides.Image
	}
	// sourceIsDeployable + weightsChanged together pick the image-
	// resolution case for ResolveDerivedProfileImage:
	//   - A deployable source is a model-optimized image: its profile is
	//     tuned for a specific model and references that model's weights via
	//     modelSources. When the derivation REPLACES those weights (overrides
	//     supply modelSources) the optimized image no longer matches the new
	//     model, so we resolve back to the lean aim-base runtime and let the
	//     new weights come from overrides.modelSources.
	//   - When the derivation leaves the weights untouched (no
	//     overrides.modelSources) the optimized image and its tuned config
	//     still apply, so we KEEP the optimized source image. This is
	//     what lets a partitioning-only or env-only override stay on the
	//     model-optimized image instead of silently falling back to base.
	//   - A base source (BYO custom-model flow) already IS the runtime
	//     image and must not be resolved to a base image (its AIM_BASE_IMAGE_REF is
	//     the FROM line of aim-base itself, e.g. vllm-openai-rocm —
	//     rebasing onto the source registry+org produces a nonexistent
	//     mirror).
	weightsChanged := overrides != nil && len(overrides.ModelSources) > 0
	result.Image = ResolveDerivedProfileImage(resolvedImageOverride, sourceBaseImage, source.Image, IsProfileDeployable(source), weightsChanged)

	if overrides == nil {
		return result, nil
	}

	// Identity overrides — applied FIRST so the subsequent modelSources
	// auto-derivation can only fill in modelId when the user didn't
	// explicitly override it. AimId/ProfileId have no auto-derivation
	// fallback; their values are only set when explicitly overridden.
	if overrides.AimId != "" {
		result.AimId = overrides.AimId
	}
	if overrides.ModelId != "" {
		result.ModelId = overrides.ModelId
	}
	if overrides.ProfileId != "" {
		result.ProfileId = overrides.ProfileId
	}

	if overrides.ModelSources != nil {
		result.ModelSources = copyModelSources(overrides.ModelSources)
		// Auto-derive modelId from modelSources[0] ONLY when the caller
		// didn't explicitly set overrides.ModelId. This preserves the
		// historical convenience for role=deployable remixes where
		// changing modelSources usually implies a new model identity,
		// while still letting an explicit overrides.ModelId win
		// (required for role=base derivations by CEL).
		if overrides.ModelId == "" && len(result.ModelSources) > 0 && result.ModelSources[0].ModelID != "" {
			result.ModelId = result.ModelSources[0].ModelID
		}
	}
	if overrides.AcceleratorModel != "" {
		result.AcceleratorModel = overrides.AcceleratorModel
	}
	if overrides.AcceleratorCount != nil {
		result.AcceleratorCount = *overrides.AcceleratorCount
	}
	if overrides.AcceleratorPartitioningMode != "" {
		result.AcceleratorPartitioningMode = overrides.AcceleratorPartitioningMode
	}

	result.ContainerEnv = MergeContainerEnv(result.ContainerEnv, overrides.ContainerEnv)
	result.EngineEnv = MergeEngineEnv(result.EngineEnv, overrides.EngineEnv)

	mergedArgs, err := MergeEngineArgs(result.EngineArgs, overrides.EngineArgs)
	if err != nil {
		return aimv1alpha2.AIMProfileSpecCommon{}, err
	}
	result.EngineArgs = mergedArgs

	return result, nil
}

// MergeContainerEnv merges env vars by name, overriding matching base entries.
func MergeContainerEnv(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make([]corev1.EnvVar, len(base))
	copy(result, base)

	indexByName := make(map[string]int, len(result))
	for i := range result {
		indexByName[result[i].Name] = i
	}
	for _, envVar := range overrides {
		if idx, ok := indexByName[envVar.Name]; ok {
			result[idx] = *envVar.DeepCopy()
			continue
		}
		indexByName[envVar.Name] = len(result)
		result = append(result, *envVar.DeepCopy())
	}
	return result
}

// MergeEngineEnv merges engine env maps with override values winning on key conflicts.
func MergeEngineEnv(base, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

// MergeEngineArgs shallow-merges two JSON objects, overriding matching top-level keys.
func MergeEngineArgs(base, overrides *apiextensionsv1.JSON) (*apiextensionsv1.JSON, error) {
	if (base == nil || len(base.Raw) == 0) && (overrides == nil || len(overrides.Raw) == 0) {
		return nil, nil
	}

	if overrides == nil || len(overrides.Raw) == 0 {
		return cloneJSON(base), nil
	}

	baseMap, err := jsonObject(base)
	if err != nil {
		return nil, fmt.Errorf("parse base engineArgs: %w", err)
	}
	overrideMap, err := jsonObject(overrides)
	if err != nil {
		return nil, fmt.Errorf("parse override engineArgs: %w", err)
	}
	for key, value := range overrideMap {
		baseMap[key] = value
	}

	raw, err := json.Marshal(baseMap)
	if err != nil {
		return nil, fmt.Errorf("marshal merged engineArgs: %w", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}

// ResolveDerivedProfileImage determines the deployment image for a derived profile.
//
// Resolution order:
//
//  1. imageOverride (typed profileCopy override) wins outright.
//  2. When sourceIsDeployable is false (the BYO custom-model flow: the
//     source profile is itself a `base`-role profile emitted from an
//     aim-base image), sourceImage IS already the runtime image we want.
//     Skipping the rebase here is essential: a base profile's
//     sourceBaseImage is the FROM line of aim-base itself
//     (e.g. `docker.io/vllm/vllm-openai-rocm:v0.16.0`), which is *not* a
//     deployable AIM image. Rebasing onto sourceImage's registry+org
//     would produce a nonexistent mirror like
//     `amdenterpriseai/vllm-openai-rocm:v0.16.0` and break the predictor
//     pod. Custom-model derivations therefore deploy on the base image
//     declared by their base AIMModel; weights come from
//     `overrides.modelSources`, not from a rebased image.
//  3. When the source is deployable but the derivation does NOT change the
//     weights (weightsChanged is false, i.e. overrides supply no
//     modelSources), keep the optimized sourceImage. The model-optimized
//     image and its tuned engine config still apply, so a partitioning-only
//     or env-only override stays on it instead of falling back to the lean
//     base runtime. Resolving to the base image here would discard the model
//     optimizations and silently downgrade the deployment.
//  4. sourceBaseImage (the AIM_BASE_IMAGE_REF the inspector extracted from
//     the source image) is normalised to MAJOR.MINOR (build pipelines often
//     stamp release-candidate tags like `0.11-rc21` but the deployable
//     aim-base image is always tagged with its MAJOR.MINOR rolling tag)
//     and rebased onto sourceImage's registry+org so private mirrors don't
//     reach back to the upstream aim-base. This is the fine-tuned flow:
//     source is a model-optimized image, the derivation REPLACES its
//     weights via overrides.modelSources, and we resolve back to the matching
//     aim-base runtime.
//  5. As a legacy fallback for installs whose imageMetadata was cached
//     before AIM_BASE_IMAGE_REF extraction existed, synthesize
//     aim-base:MAJOR.MINOR from sourceImage's tag and rebase onto its
//     registry+org.
//  6. Otherwise return sourceImage unchanged.
//
// weightsChanged is true when the derivation replaces the source profile's
// model weights (overrides.modelSources is non-empty). It only gates the
// deployable-source base-image resolution: a base source and an explicit imageOverride both
// resolve identically regardless of whether weights changed.
func ResolveDerivedProfileImage(imageOverride, sourceBaseImage, sourceImage string, sourceIsDeployable, weightsChanged bool) string {
	if imageOverride != "" {
		return imageOverride
	}
	if !sourceIsDeployable {
		return sourceImage
	}
	if !weightsChanged {
		return sourceImage
	}
	if sourceBaseImage != "" {
		return aimimage.RebaseRegistry(sourceImage, aimimage.NormalizeBaseImageTag(sourceBaseImage))
	}
	if legacy := aimimage.LegacyBaseImageFromSource(sourceImage); legacy != "" {
		return aimimage.RebaseRegistry(sourceImage, legacy)
	}
	return sourceImage
}

func latestProfileCopyVersion(profiles []ProfileCopyCandidate) string {
	if len(profiles) == 0 {
		return ""
	}

	bestVersion := ""
	bestSemver := semver.Version{}
	bestParsed := false

	for _, profile := range profiles {
		version := profile.Status.Version
		if version == "" {
			continue
		}
		parsed, err := semver.ParseTolerant(version)
		if err != nil {
			if !bestParsed && compareVersionStrings(version, bestVersion) > 0 {
				bestVersion = version
			}
			continue
		}
		if !bestParsed || parsed.GT(bestSemver) {
			bestParsed = true
			bestSemver = parsed
			bestVersion = version
		}
	}

	return bestVersion
}

func compareVersionStrings(left, right string) int {
	switch {
	case left == right:
		return 0
	case left == "":
		return -1
	case right == "":
		return 1
	}

	leftTokens := tokenizeVersionString(left)
	rightTokens := tokenizeVersionString(right)
	for i := 0; i < len(leftTokens) && i < len(rightTokens); i++ {
		if leftTokens[i] == rightTokens[i] {
			continue
		}
		leftNumber, leftIsNumber := parseNumericToken(leftTokens[i])
		rightNumber, rightIsNumber := parseNumericToken(rightTokens[i])
		switch {
		case leftIsNumber && rightIsNumber:
			if leftNumber > rightNumber {
				return 1
			}
			return -1
		case leftTokens[i] > rightTokens[i]:
			return 1
		default:
			return -1
		}
	}

	switch {
	case len(leftTokens) > len(rightTokens):
		return 1
	case len(leftTokens) < len(rightTokens):
		return -1
	case left > right:
		return 1
	default:
		return -1
	}
}

func tokenizeVersionString(value string) []string {
	if value == "" {
		return nil
	}

	tokens := make([]string, 0, len(value))
	start := 0
	lastWasDigit := unicode.IsDigit(rune(value[0]))
	for i, r := range value {
		currentIsDigit := unicode.IsDigit(r)
		if i == 0 {
			continue
		}
		if currentIsDigit == lastWasDigit {
			continue
		}
		tokens = append(tokens, value[start:i])
		start = i
		lastWasDigit = currentIsDigit
	}
	tokens = append(tokens, value[start:])
	return tokens
}

func parseNumericToken(value string) (int, bool) {
	if value == "" {
		return 0, false
	}

	total := 0
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return 0, false
		}
		total = total*10 + int(r-'0')
	}
	return total, true
}

func jsonObject(value *apiextensionsv1.JSON) (map[string]any, error) {
	if value == nil || len(value.Raw) == 0 {
		return map[string]any{}, nil
	}

	var decoded any
	if err := json.Unmarshal(value.Raw, &decoded); err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON object")
	}
	return object, nil
}

func cloneJSON(value *apiextensionsv1.JSON) *apiextensionsv1.JSON {
	if value == nil {
		return nil
	}
	out := &apiextensionsv1.JSON{Raw: make([]byte, len(value.Raw))}
	copy(out.Raw, value.Raw)
	return out
}

func copyModelSources(in []aimv1alpha1.AIMModelSource) []aimv1alpha1.AIMModelSource {
	if len(in) == 0 {
		return nil
	}
	out := make([]aimv1alpha1.AIMModelSource, len(in))
	copy(out, in)
	return out
}
