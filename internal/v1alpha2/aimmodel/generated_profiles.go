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
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const annotationProfileGenerationFallback = constants.AimLabelDomain + "/profile-generation-fallback"

// defaultGeneratedAcceleratorCount is the accelerator count used when the model
// does not request one. One device is the only shape a generic runtime template
// can assume without knowing the model's memory footprint.
const (
	defaultGeneratedAcceleratorCount    int32 = 1
	maxFallbackRejectionDetails         int   = 3
	maxNoCompatibleRuntimeMessageLength int   = 1000
)

// generatedProfileCandidate is one fallback resolved against a model. It holds
// the profile identity and spec but not a concrete object: AIMProfile and
// AIMClusterProfile differ only in their ObjectMeta, so the scoped builders
// wrap the same candidate rather than this code branching on scope.
type generatedProfileCandidate struct {
	name      string
	spec      aimv1alpha2.AIMProfileSpecCommon
	fallback  aimv1alpha1.AIMProfileGenerationFallback
	available bool
	primary   bool
}

type generatedProfileRejection struct {
	fallbackName string
	reason       string
}

type generatedCandidateResolution struct {
	candidates []generatedProfileCandidate
	rejections []generatedProfileRejection
}

// generatedProfiles is the resolved plan for one modelId-backed model.
type generatedProfiles struct {
	desired             []desiredProfile
	matchedFallbacks    []string
	configuredFallbacks int
	rejections          []generatedProfileRejection
}

func fallbacksFromRuntimeConfig(config *aimv1alpha1.AIMRuntimeConfigCommon) []aimv1alpha1.AIMProfileGenerationFallback {
	if config == nil || config.Model == nil || config.Model.ProfileGeneration == nil {
		return nil
	}
	return config.Model.ProfileGeneration.Fallbacks
}

func effectiveAimID(spec *aimv1alpha1.AIMModelSpec) string {
	if spec == nil {
		return ""
	}
	if spec.AimId != "" {
		return spec.AimId
	}
	return spec.ModelID
}

func effectiveModelSource(spec *aimv1alpha1.AIMModelSpec) aimv1alpha1.AIMModelSource {
	source := aimv1alpha1.AIMModelSource{
		ModelID:   spec.ModelID,
		SourceURI: "hf://" + spec.ModelID,
	}
	if spec.Source == nil {
		return source
	}
	if spec.Source.URI != "" {
		source.SourceURI = spec.Source.URI
	}
	if spec.Source.Size != nil {
		size := spec.Source.Size.DeepCopy()
		source.Size = &size
	}
	source.Precision = spec.Source.Precision
	source.Env = append([]corev1.EnvVar(nil), spec.Source.Env...)
	return source
}

func buildDesiredGeneratedProfiles(
	model *aimv1alpha2.AIMModel,
	fallbacks []aimv1alpha1.AIMProfileGenerationFallback,
	nodes aimprofile.NodeInventory,
	existing []managedProfile,
) (generatedProfiles, error) {
	resolution, err := generatedCandidates(model.Name, model.Spec, fallbacks, nodes, existing)
	if err != nil {
		return generatedProfiles{}, err
	}

	out := generatedProfiles{
		desired:             make([]desiredProfile, 0, len(resolution.candidates)),
		configuredFallbacks: len(fallbacks),
		rejections:          resolution.rejections,
	}
	for _, candidate := range resolution.candidates {
		annotations := generatedProfileAnnotations(model.Name, string(model.UID), candidate.fallback.Name)
		annotations[annotationModelNamespace] = model.Namespace
		annotations = profileyaml.Mark(annotations, profileyaml.CanonicalContract(&candidate.spec))
		profile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:        candidate.name,
				Namespace:   model.Namespace,
				Labels:      generatedProfileLabels(),
				Annotations: annotations,
			},
			Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: candidate.spec},
		}
		profile.Spec.Primary = candidate.primary
		aimprofile.StampProfileProvenance(
			profile,
			constants.LabelValueProfileRoleDeployable,
			aimv1alpha1.ProfileOriginGenerated,
			&aimv1alpha2.ProfileSourceModel{
				Name:      model.Name,
				Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
				Namespace: model.Namespace,
			},
		)
		out.desired = append(out.desired, desiredProfile{Object: profile})
		out.matchedFallbacks = append(out.matchedFallbacks, candidate.fallback.Name)
	}
	return out, nil
}

func buildDesiredGeneratedClusterProfiles(
	model *aimv1alpha2.AIMClusterModel,
	fallbacks []aimv1alpha1.AIMProfileGenerationFallback,
	nodes aimprofile.NodeInventory,
	existing []managedProfile,
) (generatedProfiles, error) {
	resolution, err := generatedCandidates(model.Name, model.Spec, fallbacks, nodes, existing)
	if err != nil {
		return generatedProfiles{}, err
	}

	out := generatedProfiles{
		desired:             make([]desiredProfile, 0, len(resolution.candidates)),
		configuredFallbacks: len(fallbacks),
		rejections:          resolution.rejections,
	}
	for _, candidate := range resolution.candidates {
		annotations := profileyaml.Mark(
			generatedProfileAnnotations(model.Name, string(model.UID), candidate.fallback.Name),
			profileyaml.CanonicalContract(&candidate.spec),
		)
		profile := &aimv1alpha2.AIMClusterProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:        candidate.name,
				Labels:      generatedProfileLabels(),
				Annotations: annotations,
			},
			Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: candidate.spec},
		}
		profile.Spec.Primary = candidate.primary
		aimprofile.StampProfileProvenance(
			profile,
			constants.LabelValueProfileRoleDeployable,
			aimv1alpha1.ProfileOriginGenerated,
			&aimv1alpha2.ProfileSourceModel{
				Name: model.Name,
				Kind: aimv1alpha2.ProfileSourceModelKindAIMClusterModel,
			},
		)
		out.desired = append(out.desired, desiredProfile{Object: profile})
		out.matchedFallbacks = append(out.matchedFallbacks, candidate.fallback.Name)
	}
	return out, nil
}

// generatedProfilesStatus reports how a Generated model resolved. Strategy is
// always RuntimeFallback today; a future AMD catalog lookup reports its own
// value here instead of changing status.kind.
func generatedProfilesStatus(generated generatedProfiles) *aimv1alpha1.AIMModelProfileGenerationStatus {
	return &aimv1alpha1.AIMModelProfileGenerationStatus{
		Strategy:         aimv1alpha1.ProfileGenerationStrategyRuntimeFallback,
		MatchedFallbacks: generated.matchedFallbacks,
	}
}

func (generated generatedProfiles) noCompatibleRuntimeMessage() string {
	const base = "No configured runtime fallback matches available cluster hardware"
	if generated.configuredFallbacks == 0 {
		return boundedDiagnosticMessage(base + ": the resolved RuntimeConfig defines no profile-generation fallbacks")
	}
	if len(generated.rejections) == 0 {
		return base
	}

	limit := min(len(generated.rejections), maxFallbackRejectionDetails)
	details := make([]string, 0, limit+1)
	for _, rejection := range generated.rejections[:limit] {
		details = append(details, fmt.Sprintf("fallback %q: %s", rejection.fallbackName, rejection.reason))
	}
	if omitted := len(generated.rejections) - limit; omitted > 0 {
		details = append(details, fmt.Sprintf("%d more fallback(s) rejected", omitted))
	}
	return boundedDiagnosticMessage(base + ": " + strings.Join(details, "; "))
}

func boundedDiagnosticMessage(message string) string {
	if len(message) <= maxNoCompatibleRuntimeMessageLength {
		return message
	}
	return message[:maxNoCompatibleRuntimeMessageLength-3] + "..."
}

func nodeMatchRejectionReason(result aimprofile.NodeMatchResult, nodeCount int) string {
	if nodeCount == 0 {
		return "no cluster nodes were listed"
	}
	if len(result.NodeMismatches) == 0 {
		return "no node satisfied the fallback hardware requirements"
	}

	details := make([]string, 0, len(result.NodeMismatches)+1)
	for _, mismatch := range result.NodeMismatches {
		nodeName := mismatch.NodeName
		if nodeName == "" {
			nodeName = "<unnamed>"
		}
		details = append(details, fmt.Sprintf(
			"node %q: %s",
			nodeName,
			strings.Join(mismatch.Reasons, ", "),
		))
	}
	if result.OmittedMismatches > 0 {
		details = append(details, fmt.Sprintf("%d more node(s) rejected", result.OmittedMismatches))
	}
	return strings.Join(details, "; ")
}

func generatedProfileLabels() map[string]string {
	return map[string]string{constants.LabelK8sManagedBy: constants.LabelValueManagedBy}
}

func generatedProfileAnnotations(modelName, modelUID, fallbackName string) map[string]string {
	return aimprofile.MarkProfileCopyable(
		aimprofile.MarkProfileSource(map[string]string{
			annotationModelUID:                  modelUID,
			annotationModelName:                 modelName,
			annotationProfileGenerationFallback: fallbackName,
		}, aimprofile.ProfileSourceGenerated),
		true,
	)
}

func generatedCandidates(
	modelName string,
	spec aimv1alpha1.AIMModelSpec,
	fallbacks []aimv1alpha1.AIMProfileGenerationFallback,
	nodes aimprofile.NodeInventory,
	existing []managedProfile,
) (generatedCandidateResolution, error) {
	existingFallbacks := make(map[string]struct{}, len(existing))
	for _, profile := range existing {
		if name := profile.Object.GetAnnotations()[annotationProfileGenerationFallback]; name != "" {
			existingFallbacks[name] = struct{}{}
		}
	}

	resolution := generatedCandidateResolution{
		candidates: make([]generatedProfileCandidate, 0, len(fallbacks)),
		rejections: make([]generatedProfileRejection, 0, len(fallbacks)),
	}
	for _, fallback := range fallbacks {
		if err := validateGenerationFallback(fallback); err != nil {
			return generatedCandidateResolution{}, err
		}
		if reason := fallbackRequestMismatchReason(fallback, spec.Accelerator); reason != "" {
			resolution.rejections = append(resolution.rejections, generatedProfileRejection{
				fallbackName: fallback.Name,
				reason:       reason,
			})
			continue
		}
		profileSpec := generatedProfileSpec(spec, fallback)
		resolvedResources := aimprofile.ResolveProfileResources(profileSpec)
		matchResult := aimprofile.MatchProfileNodes(nodes, profileSpec, resolvedResources)
		available := matchResult.TotalMatchingNodes() > 0
		if _, retained := existingFallbacks[fallback.Name]; !available && !retained {
			resolution.rejections = append(resolution.rejections, generatedProfileRejection{
				fallbackName: fallback.Name,
				reason:       nodeMatchRejectionReason(matchResult, len(nodes.Nodes)),
			})
			continue
		}

		name, err := utils.GenerateDerivedName(
			[]string{modelName, fallback.Name},
			// aimId is immutable on AIMProfile/AIMClusterProfile. Include the
			// effective identity in the generated name so changing AIMModel.aimId
			// creates a replacement child instead of repeatedly attempting an
			// admission-rejected update to the existing child.
			utils.WithHashSource(modelName, fallback.Name, profileSpec.AimId),
		)
		if err != nil {
			return generatedCandidateResolution{}, fmt.Errorf("generate profile name for fallback %q: %w", fallback.Name, err)
		}

		resolution.candidates = append(resolution.candidates, generatedProfileCandidate{
			name:      name,
			spec:      profileSpec,
			fallback:  fallback,
			available: available,
		})
	}

	sort.Slice(resolution.candidates, func(i, j int) bool {
		return resolution.candidates[i].name < resolution.candidates[j].name
	})
	assignGeneratedPrimary(resolution.candidates)
	return resolution, nil
}

// fallbackRequestMismatchReason reports why a cluster-policy fallback cannot
// serve the model's requested hardware. An empty result means it is compatible.
func fallbackRequestMismatchReason(
	fallback aimv1alpha1.AIMProfileGenerationFallback,
	request *aimv1alpha1.AIMModelAcceleratorRequest,
) string {
	if request == nil {
		return ""
	}
	if request.Vendor != "" && fallback.Match.AcceleratorVendor != request.Vendor {
		return fmt.Sprintf(
			"model requests accelerator vendor %q, fallback targets %q",
			request.Vendor,
			fallback.Match.AcceleratorVendor,
		)
	}
	if request.Model != "" &&
		fallback.Match.AcceleratorModel != "" &&
		!strings.EqualFold(fallback.Match.AcceleratorModel, request.Model) {
		return fmt.Sprintf(
			"model requests accelerator model %q, fallback targets %q",
			request.Model,
			fallback.Match.AcceleratorModel,
		)
	}
	if request.PartitioningMode != "" &&
		fallback.Match.AcceleratorPartitioningMode != "" &&
		!strings.EqualFold(fallback.Match.AcceleratorPartitioningMode, request.PartitioningMode) {
		return fmt.Sprintf(
			"model requests partitioning mode %q, fallback targets %q",
			request.PartitioningMode,
			fallback.Match.AcceleratorPartitioningMode,
		)
	}
	return ""
}

func validateGenerationFallback(fallback aimv1alpha1.AIMProfileGenerationFallback) error {
	switch {
	case fallback.Name == "":
		return fmt.Errorf("profile generation fallback name is required")
	case fallback.Runtime.Image == "":
		return fmt.Errorf("profile generation fallback %q runtime image is required", fallback.Name)
	case fallback.Runtime.Engine == "":
		return fmt.Errorf("profile generation fallback %q runtime engine is required", fallback.Name)
	case fallback.Match.AcceleratorVendor == "":
		return fmt.Errorf("profile generation fallback %q accelerator vendor is required", fallback.Name)
	case fallback.Match.AcceleratorType == "":
		return fmt.Errorf("profile generation fallback %q accelerator type is required", fallback.Name)
	default:
		return nil
	}
}

func generatedProfileSpec(
	modelSpec aimv1alpha1.AIMModelSpec,
	fallback aimv1alpha1.AIMProfileGenerationFallback,
) aimv1alpha2.AIMProfileSpecCommon {
	source := effectiveModelSource(&modelSpec)
	request := modelSpec.Accelerator

	// The model's request wins on every axis it sets; the fallback supplies the
	// rest. Count has no fallback-side counterpart — a runtime template is
	// device-count agnostic — so it comes from the request or the default.
	acceleratorModel := fallback.Match.AcceleratorModel
	partitioningMode := fallback.Match.AcceleratorPartitioningMode
	acceleratorCount := defaultGeneratedAcceleratorCount
	if request != nil {
		if request.Model != "" {
			acceleratorModel = request.Model
		}
		if request.PartitioningMode != "" {
			partitioningMode = request.PartitioningMode
		}
		if request.Count != nil {
			acceleratorCount = *request.Count
		}
	}

	profileType := fallback.Type
	if profileType == "" {
		profileType = aimv1alpha1.AIMProfileTypeUnoptimized
	}
	autoSelectionPolicy := fallback.AutoSelectionPolicy
	if autoSelectionPolicy == "" {
		autoSelectionPolicy = aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized
	}

	return aimv1alpha2.AIMProfileSpecCommon{
		AimId:                       effectiveAimID(&modelSpec),
		ModelId:                     modelSpec.ModelID,
		ProfileId:                   fallback.Name,
		Engine:                      fallback.Runtime.Engine,
		Precision:                   source.Precision,
		Type:                        profileType,
		AutoSelectionPolicy:         autoSelectionPolicy,
		EngineArgs:                  copyJSON(fallback.Runtime.EngineArgs),
		EngineEnv:                   copyStringMap(fallback.Runtime.EngineEnv),
		AcceleratorVendor:           fallback.Match.AcceleratorVendor,
		AcceleratorModel:            acceleratorModel,
		AcceleratorType:             fallback.Match.AcceleratorType,
		AcceleratorCount:            acceleratorCount,
		AcceleratorPartitioningMode: partitioningMode,
		Resources:                   copyResources(fallback.Runtime.Resources),
		Image:                       fallback.Runtime.Image,
		ModelSources:                []aimv1alpha1.AIMModelSource{source},
		ContainerEnv:                append([]corev1.EnvVar(nil), fallback.Runtime.ContainerEnv...),
		ImagePullSecrets:            append([]corev1.LocalObjectReference(nil), fallback.Runtime.ImagePullSecrets...),
		ServiceAccountName:          fallback.Runtime.ServiceAccountName,
	}
}

func assignGeneratedPrimary(candidates []generatedProfileCandidate) {
	if len(candidates) == 0 {
		return
	}
	hasAvailable := false
	for i := range candidates {
		if candidates[i].available {
			hasAvailable = true
			break
		}
	}
	best := -1
	for i := range candidates {
		if hasAvailable && !candidates[i].available {
			continue
		}
		if best == -1 || generatedFallbackBetter(candidates[i].fallback, candidates[best].fallback) {
			best = i
		}
	}
	for i := range candidates {
		candidates[i].primary = i == best
	}
}

func generatedFallbackBetter(a, b aimv1alpha1.AIMProfileGenerationFallback) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	aSpecificity := generatedFallbackSpecificity(a)
	bSpecificity := generatedFallbackSpecificity(b)
	if aSpecificity != bSpecificity {
		return aSpecificity > bSpecificity
	}
	return a.Name < b.Name
}

func generatedFallbackSpecificity(fallback aimv1alpha1.AIMProfileGenerationFallback) int {
	score := 0
	if fallback.Match.AcceleratorModel != "" {
		score += 2
	}
	if fallback.Match.AcceleratorPartitioningMode != "" {
		score++
	}
	return score
}

func copyJSON(in *apiextensionsv1.JSON) *apiextensionsv1.JSON {
	if in == nil {
		return nil
	}
	return &apiextensionsv1.JSON{Raw: append([]byte(nil), in.Raw...)}
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyResources(in *corev1.ResourceRequirements) *corev1.ResourceRequirements {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}
