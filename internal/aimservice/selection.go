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

package aimservice

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// TemplateCandidate captures the information needed to evaluate a template during selection.
type TemplateCandidate struct {
	Name      string
	Namespace string
	Scope     aimv1alpha1.AIMResolutionScope
	Spec      aimv1alpha1.AIMServiceTemplateSpecCommon
	Status    aimv1alpha1.AIMServiceTemplateStatus
}

// TemplateSelectionResult captures the result of template auto-selection.
type TemplateSelectionResult struct {
	SelectedTemplate          *aimv1alpha1.AIMServiceTemplate
	SelectedClusterTemplate   *aimv1alpha1.AIMClusterServiceTemplate
	CandidateCount            int
	TemplatesExistButNotReady bool
	SelectionReason           string
	SelectionMessage          string
	MatchingResults           []aimv1alpha1.AIMTemplateCandidateResult
	Error                     error
}

// SelectionDiagnostics provides detailed information about why template selection failed.
type SelectionDiagnostics struct {
	TotalCandidates                  int
	AfterAvailabilityFilter          int
	AfterUnoptimizedFilter           int
	AfterOverridesFilter             int
	AfterGPUAvailabilityFilter       int
	UnoptimizedTemplatesWereFiltered bool
}

// CandidateEvaluation captures why a specific candidate was chosen or rejected.
type CandidateEvaluation struct {
	Candidate TemplateCandidate
	Status    string // "chosen" or "rejected"
	Reason    string // CamelCase reason
	Rank      int    // For candidates that passed all filters
}

// selectTemplateForModel selects the best template for a given model.
func selectTemplateForModel(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	modelName string,
) *TemplateSelectionResult {
	logger := log.FromContext(ctx)
	result := &TemplateSelectionResult{}

	// List all template candidates for this model
	candidates, err := listTemplateCandidatesForModel(ctx, c, service.Namespace, modelName)
	if err != nil {
		result.Error = err
		return result
	}

	if len(candidates) == 0 {
		result.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateNotFound
		result.SelectionMessage = fmt.Sprintf("No templates found for model %q", modelName)
		return result
	}

	// Get available GPUs in the cluster
	availableGPUs, err := listAvailableGPUs(ctx, c)
	if err != nil {
		result.Error = fmt.Errorf("failed to list available GPUs: %w", err)
		return result
	}

	// Determine if unoptimized templates are allowed
	allowUnoptimized := service.Spec.Template.AllowUnoptimized

	// Select the best template
	selected, count, diag, evaluations := selectBestTemplate(
		candidates,
		service.Spec.Overrides,
		availableGPUs,
		allowUnoptimized,
	)

	result.CandidateCount = count
	result.MatchingResults = convertToTemplateMatchingResults(evaluations)

	if selected == nil {
		// No template selected - determine why
		if len(candidates) > 0 && diag.AfterAvailabilityFilter == 0 {
			result.TemplatesExistButNotReady = true
			result.SelectionReason = ""
			result.SelectionMessage = ""
		} else if diag.AfterUnoptimizedFilter == 0 && diag.UnoptimizedTemplatesWereFiltered {
			result.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateNotFound
			result.SelectionMessage = fmt.Sprintf(
				"No available templates match requirements for model %q: "+
					"%d unoptimized template(s) filtered out. Set allowUnoptimized to use them.",
				modelName, diag.AfterAvailabilityFilter)
		} else {
			result.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateNotFound
			result.SelectionMessage = fmt.Sprintf("No available templates match requirements for model %q", modelName)
		}
		return result
	}

	if count > 1 {
		result.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateSelectionAmbiguous
		result.SelectionMessage = fmt.Sprintf("Multiple templates (%d) satisfy model %q", count, modelName)
		return result
	}

	// Fetch the actual template object
	if selected.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
		template := &aimv1alpha1.AIMServiceTemplate{}
		err := c.Get(ctx, client.ObjectKey{
			Namespace: selected.Namespace,
			Name:      selected.Name,
		}, template)
		if err != nil {
			result.Error = err
			return result
		}
		result.SelectedTemplate = template
	} else {
		template := &aimv1alpha1.AIMClusterServiceTemplate{}
		err := c.Get(ctx, client.ObjectKey{Name: selected.Name}, template)
		if err != nil {
			result.Error = err
			return result
		}
		result.SelectedClusterTemplate = template
	}

	logger.V(1).Info("template selected", "template", selected.Name, "scope", selected.Scope)
	return result
}

// listTemplateCandidatesForModel lists all templates that match the given model name.
func listTemplateCandidatesForModel(
	ctx context.Context,
	c client.Client,
	namespace string,
	modelName string,
) ([]TemplateCandidate, error) {
	var candidates []TemplateCandidate

	// List namespace-scoped templates
	nsTemplates := &aimv1alpha1.AIMServiceTemplateList{}
	if err := c.List(ctx, nsTemplates, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	for _, t := range nsTemplates.Items {
		if t.Spec.ModelName == modelName {
			candidates = append(candidates, TemplateCandidate{
				Name:      t.Name,
				Namespace: t.Namespace,
				Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
				Spec:      t.Spec.AIMServiceTemplateSpecCommon,
				Status:    t.Status,
			})
		}
	}

	// List cluster-scoped templates
	clusterTemplates := &aimv1alpha1.AIMClusterServiceTemplateList{}
	if err := c.List(ctx, clusterTemplates); err != nil {
		return nil, err
	}

	for _, t := range clusterTemplates.Items {
		if t.Spec.ModelName == modelName {
			candidates = append(candidates, TemplateCandidate{
				Name:   t.Name,
				Scope:  aimv1alpha1.AIMResolutionScopeCluster,
				Spec:   t.Spec.AIMServiceTemplateSpecCommon,
				Status: t.Status,
			})
		}
	}

	return candidates, nil
}

// listAvailableGPUs returns the list of GPU models available in the cluster.
// Uses device ID-based extraction for AMD GPUs and label-based extraction for NVIDIA GPUs.
func listAvailableGPUs(ctx context.Context, c client.Client) ([]string, error) {
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		return nil, err
	}

	gpuSet := make(map[string]struct{})
	for _, node := range nodes.Items {
		// Try AMD GPU extraction (device ID-based)
		if model := utils.ExtractAMDModel(node.Labels); model != "" {
			gpuSet[model] = struct{}{}
		}
		// Add NVIDIA extraction if needed in the future
	}

	gpus := make([]string, 0, len(gpuSet))
	for gpu := range gpuSet {
		gpus = append(gpus, gpu)
	}
	return gpus, nil
}

// Filter stage identifiers for tracking rejections
const (
	stageAvailability = "availability"
	stageUnoptimized  = "unoptimized"
	stageOverrides    = "overrides"
	stageGPU          = "gpu"
)

// filterByAvailability removes candidates that are not Ready.
func filterByAvailability(candidates []TemplateCandidate, rejected map[string][]TemplateCandidate) []TemplateCandidate {
	var result []TemplateCandidate
	for _, c := range candidates {
		if c.Status.Status == constants.AIMStatusReady {
			result = append(result, c)
		} else {
			rejected[stageAvailability] = append(rejected[stageAvailability], c)
		}
	}
	return result
}

// filterByOptimizationStatus removes unoptimized templates if not allowed.
func filterByOptimizationStatus(candidates []TemplateCandidate, allowUnoptimized bool, rejected map[string][]TemplateCandidate) []TemplateCandidate {
	var result []TemplateCandidate
	for _, c := range candidates {
		// If profile is nil, treat as unoptimized
		profileType := aimv1alpha1.AIMProfileTypeUnoptimized
		if c.Status.Profile != nil {
			profileType = c.Status.Profile.Metadata.Type
		}
		if profileType == aimv1alpha1.AIMProfileTypeOptimized || allowUnoptimized {
			result = append(result, c)
		} else {
			rejected[stageUnoptimized] = append(rejected[stageUnoptimized], c)
		}
	}
	return result
}

// buildFinalEvaluations creates the evaluation list for the final selected candidates.
func buildFinalEvaluations(filtered []TemplateCandidate, selected *TemplateCandidate, rejected map[string][]TemplateCandidate) []CandidateEvaluation {
	evaluations := make([]CandidateEvaluation, 0, len(filtered)+len(rejected))
	appendRejections(&evaluations, rejected)

	for i, c := range filtered {
		if c.Name == selected.Name {
			evaluations = append(evaluations, CandidateEvaluation{
				Candidate: c,
				Status:    "chosen",
				Reason:    "BestMatch",
				Rank:      1,
			})
		} else {
			evaluations = append(evaluations, CandidateEvaluation{
				Candidate: c,
				Status:    "rejected",
				Reason:    "LowerPreferenceRank",
				Rank:      i + 1,
			})
		}
	}
	return evaluations
}

// selectBestTemplate selects the best template from candidates.
// Selection criteria (in order of priority):
// 1. Only Available templates (status == Ready)
// 2. Filter unoptimized if not allowed
// 3. Filter by service overrides (metric, precision, GPU)
// 4. Filter by GPU availability in cluster
// 5. Prefer namespace-scoped over cluster-scoped
// 6. Prefer by profile type > GPU tier > metric > precision
func selectBestTemplate(
	candidates []TemplateCandidate,
	overrides *aimv1alpha1.AIMServiceOverrides,
	availableGPUs []string,
	allowUnoptimized bool,
) (*TemplateCandidate, int, SelectionDiagnostics, []CandidateEvaluation) {
	diag := SelectionDiagnostics{TotalCandidates: len(candidates)}
	rejectedByStage := make(map[string][]TemplateCandidate)

	// Stage 1: Availability filter - only Ready templates can be selected
	filtered := filterByAvailability(candidates, rejectedByStage)
	diag.AfterAvailabilityFilter = len(filtered)
	if len(filtered) == 0 {
		evals := make([]CandidateEvaluation, 0)
		appendRejections(&evals, rejectedByStage)
		return nil, 0, diag, evals
	}

	// Stage 2: Unoptimized filter - exclude unoptimized unless explicitly allowed
	filtered = filterByOptimizationStatus(filtered, allowUnoptimized, rejectedByStage)
	diag.AfterUnoptimizedFilter = len(filtered)
	diag.UnoptimizedTemplatesWereFiltered = len(rejectedByStage[stageUnoptimized]) > 0
	if len(filtered) == 0 {
		evals := make([]CandidateEvaluation, 0)
		appendRejections(&evals, rejectedByStage)
		return nil, 0, diag, evals
	}

	// Stage 3: Overrides filter - match service-specified constraints
	if overrides != nil {
		beforeOverrides := filtered
		filtered = filterTemplatesByOverrides(filtered, overrides)
		diag.AfterOverridesFilter = len(filtered)
		if len(filtered) == 0 {
			rejectedByStage[stageOverrides] = beforeOverrides
			evals := make([]CandidateEvaluation, 0)
			appendRejections(&evals, rejectedByStage)
			return nil, 0, diag, evals
		}
	} else {
		diag.AfterOverridesFilter = len(filtered)
	}

	// Stage 4: GPU availability filter - only templates for GPUs present in cluster
	beforeGPU := filtered
	filtered = filterTemplatesByGPUAvailability(filtered, availableGPUs)
	diag.AfterGPUAvailabilityFilter = len(filtered)
	if len(filtered) == 0 {
		rejectedByStage[stageGPU] = beforeGPU
		evals := make([]CandidateEvaluation, 0)
		appendRejections(&evals, rejectedByStage)
		return nil, 0, diag, evals
	}

	// Stage 5: Scope preference - namespace templates over cluster templates
	filtered = preferNamespaceTemplates(filtered)

	// Single candidate remaining - select it
	if len(filtered) == 1 {
		evals := buildFinalEvaluations(filtered, &filtered[0], rejectedByStage)
		return &filtered[0], 1, diag, evals
	}

	// Stage 6: Preference scoring - rank by profile type, GPU, metric, precision
	selected, count := choosePreferredTemplate(filtered)
	evals := buildFinalEvaluations(filtered, selected, rejectedByStage)

	return selected, count, diag, evals
}

func appendRejections(evals *[]CandidateEvaluation, rejectedByStage map[string][]TemplateCandidate) {
	for _, c := range rejectedByStage[stageAvailability] {
		*evals = append(*evals, CandidateEvaluation{
			Candidate: c,
			Status:    "rejected",
			Reason:    getRejectionReasonForStatus(c.Status.Status),
		})
	}

	addWithReason := func(stage string, reason string) {
		for _, c := range rejectedByStage[stage] {
			*evals = append(*evals, CandidateEvaluation{
				Candidate: c,
				Status:    "rejected",
				Reason:    reason,
			})
		}
	}

	addWithReason(stageUnoptimized, "UnoptimizedTemplateFiltered")
	addWithReason(stageOverrides, "ServiceOverridesNotMatched")
	addWithReason(stageGPU, "RequiredGPUNotInCluster")
}

func getRejectionReasonForStatus(status constants.AIMStatus) string {
	switch status {
	case constants.AIMStatusPending:
		return "TemplatePending"
	case constants.AIMStatusProgressing:
		return "TemplateProgressing"
	case constants.AIMStatusNotAvailable:
		return "TemplateNotAvailable"
	case constants.AIMStatusDegraded:
		return "TemplateDegraded"
	case constants.AIMStatusFailed:
		return "TemplateFailed"
	default:
		return "TemplateNotReady"
	}
}

func filterTemplatesByOverrides(candidates []TemplateCandidate, overrides *aimv1alpha1.AIMServiceOverrides) []TemplateCandidate {
	if overrides == nil {
		return candidates
	}

	result := make([]TemplateCandidate, 0, len(candidates))
	for _, c := range candidates {
		templateMetric := candidateMetric(c)
		templatePrecision := candidatePrecision(c)
		templateGPU := candidateGPUModel(c)
		templateGPUCount := candidateGPUCount(c)

		if overrides.Metric != nil && !strings.EqualFold(templateMetric, string(*overrides.Metric)) {
			continue
		}
		if overrides.Precision != nil && !strings.EqualFold(templatePrecision, string(*overrides.Precision)) {
			continue
		}
		if overrides.GpuSelector != nil {
			overrideGPU := strings.TrimSpace(overrides.GpuSelector.Model)
			if overrideGPU != "" && !strings.EqualFold(templateGPU, overrideGPU) {
				continue
			}
			// Filter by GPU count if specified
			if overrides.GpuSelector.Count > 0 && templateGPUCount > 0 && templateGPUCount != overrides.GpuSelector.Count {
				continue
			}
		}
		result = append(result, c)
	}
	return result
}

func filterTemplatesByGPUAvailability(candidates []TemplateCandidate, availableGPUs []string) []TemplateCandidate {
	gpuMap := make(map[string]struct{}, len(availableGPUs))
	for _, gpu := range availableGPUs {
		normalized := utils.NormalizeGPUModel(strings.TrimSpace(gpu))
		if normalized != "" {
			gpuMap[normalized] = struct{}{}
		}
	}

	result := make([]TemplateCandidate, 0, len(candidates))
	for _, c := range candidates {
		model := strings.TrimSpace(candidateGPUModel(c))
		if model == "" {
			result = append(result, c)
			continue
		}
		normalized := utils.NormalizeGPUModel(model)
		if _, ok := gpuMap[normalized]; ok {
			result = append(result, c)
		}
	}
	return result
}

func preferNamespaceTemplates(candidates []TemplateCandidate) []TemplateCandidate {
	hasNamespace := false
	for _, c := range candidates {
		if c.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
			hasNamespace = true
			break
		}
	}
	if !hasNamespace {
		return candidates
	}

	result := make([]TemplateCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
			result = append(result, c)
		}
	}
	return result
}

// choosePreferredTemplate selects the best template from candidates using a preference hierarchy.
//
// Preference hierarchy (highest to lowest priority):
// 1. Profile Type: optimized > preview > unoptimized
// 2. GPU Tier: MI325X > MI300X > MI250X > MI210 > A100 > H100 (AMD preferred)
// 3. Metric: latency > throughput
// 4. Precision: smaller bit-width preferred (fp4 > int4 > fp8 > int8 > fp16 > bf16 > fp32)
//
// Lower scores indicate higher preference. Unknown values get a high score (len+1000).
// Returns the best candidate and count of candidates with identical best scores.
func choosePreferredTemplate(candidates []TemplateCandidate) (*TemplateCandidate, int) {
	if len(candidates) == 0 {
		return nil, 0
	}
	if len(candidates) == 1 {
		return &candidates[0], 1
	}

	// Build preference maps: lower index = higher preference
	gpuPref := makePreferenceMap(gpuPreferenceOrder)
	metricPref := makePreferenceMap(metricPreferenceOrder)
	precisionPref := makePreferenceMap(precisionPreferenceOrder)
	profileTypePref := makePreferenceMap(profileTypePreferenceOrder)

	bestIdx := 0
	bestGPU := getPreferenceScore(candidateGPUModel(candidates[0]), gpuPref)
	bestMetric := getPreferenceScore(candidateMetric(candidates[0]), metricPref)
	bestPrecision := getPreferenceScore(candidatePrecision(candidates[0]), precisionPref)
	bestProfileType := getPreferenceScore(candidateProfileType(candidates[0]), profileTypePref)

	for i := 1; i < len(candidates); i++ {
		gpu := getPreferenceScore(candidateGPUModel(candidates[i]), gpuPref)
		metric := getPreferenceScore(candidateMetric(candidates[i]), metricPref)
		precision := getPreferenceScore(candidatePrecision(candidates[i]), precisionPref)
		profileType := getPreferenceScore(candidateProfileType(candidates[i]), profileTypePref)

		// Compare using lexicographic ordering: profile type > GPU > metric > precision
		// A candidate is better if it has a lower score at the highest-priority dimension
		// where the candidates differ
		if profileType < bestProfileType ||
			(profileType == bestProfileType && gpu < bestGPU) ||
			(profileType == bestProfileType && gpu == bestGPU && metric < bestMetric) ||
			(profileType == bestProfileType && gpu == bestGPU && metric == bestMetric && precision < bestPrecision) {
			bestIdx = i
			bestGPU = gpu
			bestMetric = metric
			bestPrecision = precision
			bestProfileType = profileType
		}
	}

	// Count identical scores (must include all scoring dimensions)
	identicalCount := 0
	for i := range candidates {
		profileType := getPreferenceScore(candidateProfileType(candidates[i]), profileTypePref)
		gpu := getPreferenceScore(candidateGPUModel(candidates[i]), gpuPref)
		metric := getPreferenceScore(candidateMetric(candidates[i]), metricPref)
		precision := getPreferenceScore(candidatePrecision(candidates[i]), precisionPref)
		if profileType == bestProfileType && gpu == bestGPU && metric == bestMetric && precision == bestPrecision {
			identicalCount++
		}
	}

	return &candidates[bestIdx], identicalCount
}

func candidateMetric(c TemplateCandidate) string {
	if c.Status.Profile != nil {
		if m := c.Status.Profile.Metadata.Metric; m != "" {
			return string(m)
		}
	}
	if c.Spec.Metric != nil {
		return string(*c.Spec.Metric)
	}
	return ""
}

func candidatePrecision(c TemplateCandidate) string {
	if c.Status.Profile != nil {
		if p := c.Status.Profile.Metadata.Precision; p != "" {
			return string(p)
		}
	}
	if c.Spec.Precision != nil {
		return string(*c.Spec.Precision)
	}
	return ""
}

func candidateGPUModel(c TemplateCandidate) string {
	if c.Spec.GpuSelector != nil {
		model := strings.TrimSpace(c.Spec.GpuSelector.Model)
		if model != "" {
			return model
		}
	}
	if c.Status.Profile != nil {
		if gpu := strings.TrimSpace(c.Status.Profile.Metadata.GPU); gpu != "" {
			return gpu
		}
	}
	return ""
}

func candidateGPUCount(c TemplateCandidate) int32 {
	if c.Spec.GpuSelector != nil && c.Spec.GpuSelector.Count > 0 {
		return c.Spec.GpuSelector.Count
	}
	if c.Status.Profile != nil && c.Status.Profile.Metadata.GPUCount > 0 {
		return c.Status.Profile.Metadata.GPUCount
	}
	return 0
}

func candidateProfileType(c TemplateCandidate) string {
	if c.Status.Profile != nil {
		return string(c.Status.Profile.Metadata.Type)
	}
	return ""
}

// Preference orders for template selection
var (
	gpuPreferenceOrder = []string{
		"MI325X", "MI300X", "MI250X", "MI210", "A100", "H100",
	}
	metricPreferenceOrder = []string{
		"latency", "throughput",
	}
	// Precision preference: primary ordering by bit-width (smaller preferred for performance).
	// Secondary ordering by type: fp > bf > int (floating point preferred for accuracy).
	precisionPreferenceOrder = []string{
		"fp4", "int4", "fp8", "int8", "fp16", "bf16", "fp32",
	}
	profileTypePreferenceOrder = []string{
		string(aimv1alpha1.AIMProfileTypeOptimized),
		string(aimv1alpha1.AIMProfileTypePreview),
		string(aimv1alpha1.AIMProfileTypeUnoptimized),
	}
)

func makePreferenceMap(prefs []string) map[string]int {
	m := make(map[string]int)
	for i, p := range prefs {
		m[strings.ToUpper(p)] = i
	}
	return m
}

func getPreferenceScore(value string, prefMap map[string]int) int {
	if score, ok := prefMap[strings.ToUpper(value)]; ok {
		return score
	}
	return len(prefMap) + 1000
}

func convertToTemplateMatchingResults(evaluations []CandidateEvaluation) []aimv1alpha1.AIMTemplateCandidateResult {
	if len(evaluations) == 0 {
		return []aimv1alpha1.AIMTemplateCandidateResult{}
	}
	results := make([]aimv1alpha1.AIMTemplateCandidateResult, len(evaluations))
	for i, eval := range evaluations {
		results[i] = aimv1alpha1.AIMTemplateCandidateResult{
			Name:   eval.Candidate.Name,
			Status: eval.Status,
			Reason: eval.Reason,
		}
	}
	return results
}
