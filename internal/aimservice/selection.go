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
)

// TemplateScope indicates whether a template is namespace-scoped or cluster-scoped.
type TemplateScope string

const (
	TemplateScopeNone      TemplateScope = ""
	TemplateScopeNamespace TemplateScope = "namespace"
	TemplateScopeCluster   TemplateScope = "cluster"
)

// TemplateCandidate captures the information needed to evaluate a template during selection.
type TemplateCandidate struct {
	Name      string
	Namespace string
	Scope     TemplateScope
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
	if selected.Scope == TemplateScopeNamespace {
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
				Scope:     TemplateScopeNamespace,
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
				Scope:  TemplateScopeCluster,
				Spec:   t.Spec.AIMServiceTemplateSpecCommon,
				Status: t.Status,
			})
		}
	}

	return candidates, nil
}

// listAvailableGPUs returns the list of GPU models available in the cluster.
func listAvailableGPUs(ctx context.Context, c client.Client) ([]string, error) {
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		return nil, err
	}

	gpuSet := make(map[string]struct{})
	for _, node := range nodes.Items {
		model := extractGPUModelFromNodeLabels(node.Labels)
		if model != "" {
			gpuSet[normalizeGPUModel(model)] = struct{}{}
		}
	}

	gpus := make([]string, 0, len(gpuSet))
	for gpu := range gpuSet {
		gpus = append(gpus, gpu)
	}
	return gpus, nil
}

// selectBestTemplate selects the best template from candidates.
// Selection criteria (in order of priority):
// 1. Only Available templates
// 2. Filter unoptimized if not allowed
// 3. Filter by service overrides
// 4. Filter by GPU availability
// 5. Prefer namespace-scoped over cluster-scoped
// 6. Prefer: optimized > latency > lower precision > higher-tier GPU
func selectBestTemplate(
	candidates []TemplateCandidate,
	overrides *aimv1alpha1.AIMServiceOverrides,
	availableGPUs []string,
	allowUnoptimized bool,
) (*TemplateCandidate, int, SelectionDiagnostics, []CandidateEvaluation) {
	diag := SelectionDiagnostics{TotalCandidates: len(candidates)}
	evaluations := make([]CandidateEvaluation, 0, len(candidates))

	const (
		stageAvailability = "availability"
		stageUnoptimized  = "unoptimized"
		stageOverrides    = "overrides"
		stageGPU          = "gpu"
	)
	rejectedByStage := make(map[string][]TemplateCandidate)

	// Stage 1: Availability filter
	var filtered []TemplateCandidate
	for _, c := range candidates {
		if c.Status.Status == constants.AIMStatusReady {
			filtered = append(filtered, c)
		} else {
			rejectedByStage[stageAvailability] = append(rejectedByStage[stageAvailability], c)
		}
	}
	diag.AfterAvailabilityFilter = len(filtered)
	if len(filtered) == 0 {
		appendRejections(&evaluations, rejectedByStage)
		return nil, 0, diag, evaluations
	}

	// Stage 2: Unoptimized filter
	beforeUnoptimized := filtered
	filtered = filtered[:0]
	for _, c := range beforeUnoptimized {
		if c.Status.Profile.Metadata.Type == aimv1alpha1.AIMProfileTypeOptimized || allowUnoptimized {
			filtered = append(filtered, c)
		} else {
			rejectedByStage[stageUnoptimized] = append(rejectedByStage[stageUnoptimized], c)
		}
	}
	diag.AfterUnoptimizedFilter = len(filtered)
	diag.UnoptimizedTemplatesWereFiltered = len(rejectedByStage[stageUnoptimized]) > 0
	if len(filtered) == 0 {
		appendRejections(&evaluations, rejectedByStage)
		return nil, 0, diag, evaluations
	}

	// Stage 3: Overrides filter
	if overrides != nil {
		beforeOverrides := filtered
		filtered = filterTemplatesByOverrides(filtered, overrides)
		diag.AfterOverridesFilter = len(filtered)
		if len(filtered) == 0 {
			rejectedByStage[stageOverrides] = beforeOverrides
			appendRejections(&evaluations, rejectedByStage)
			return nil, 0, diag, evaluations
		}
	} else {
		diag.AfterOverridesFilter = len(filtered)
	}

	// Stage 4: GPU availability filter
	beforeGPU := filtered
	filtered = filterTemplatesByGPUAvailability(filtered, availableGPUs)
	diag.AfterGPUAvailabilityFilter = len(filtered)
	if len(filtered) == 0 {
		rejectedByStage[stageGPU] = beforeGPU
		appendRejections(&evaluations, rejectedByStage)
		return nil, 0, diag, evaluations
	}

	// Stage 5: Prefer namespace-scoped templates
	filtered = preferNamespaceTemplates(filtered)

	if len(filtered) == 1 {
		appendRejections(&evaluations, rejectedByStage)
		evaluations = append(evaluations, CandidateEvaluation{
			Candidate: filtered[0],
			Status:    "chosen",
			Reason:    "BestMatch",
			Rank:      1,
		})
		return &filtered[0], 1, diag, evaluations
	}

	// Stage 6: Preference scoring
	selected, count := choosePreferredTemplate(filtered)
	appendRejections(&evaluations, rejectedByStage)

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

	return selected, count, diag, evaluations
}

func appendRejections(evals *[]CandidateEvaluation, rejectedByStage map[string][]TemplateCandidate) {
	const (
		stageAvailability = "availability"
		stageUnoptimized  = "unoptimized"
		stageOverrides    = "overrides"
		stageGPU          = "gpu"
	)

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
		}
		result = append(result, c)
	}
	return result
}

func filterTemplatesByGPUAvailability(candidates []TemplateCandidate, availableGPUs []string) []TemplateCandidate {
	gpuMap := make(map[string]struct{}, len(availableGPUs))
	for _, gpu := range availableGPUs {
		normalized := normalizeGPUModel(strings.TrimSpace(gpu))
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
		normalized := normalizeGPUModel(model)
		if _, ok := gpuMap[normalized]; ok {
			result = append(result, c)
		}
	}
	return result
}

func preferNamespaceTemplates(candidates []TemplateCandidate) []TemplateCandidate {
	hasNamespace := false
	for _, c := range candidates {
		if c.Scope == TemplateScopeNamespace {
			hasNamespace = true
			break
		}
	}
	if !hasNamespace {
		return candidates
	}

	result := make([]TemplateCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Scope == TemplateScopeNamespace {
			result = append(result, c)
		}
	}
	return result
}

func choosePreferredTemplate(candidates []TemplateCandidate) (*TemplateCandidate, int) {
	if len(candidates) == 0 {
		return nil, 0
	}
	if len(candidates) == 1 {
		return &candidates[0], 1
	}

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

		// Compare by: profile type > GPU > metric > precision
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

	// Count identical scores
	identicalCount := 0
	for i := range candidates {
		gpu := getPreferenceScore(candidateGPUModel(candidates[i]), gpuPref)
		metric := getPreferenceScore(candidateMetric(candidates[i]), metricPref)
		precision := getPreferenceScore(candidatePrecision(candidates[i]), precisionPref)
		if gpu == bestGPU && metric == bestMetric && precision == bestPrecision {
			identicalCount++
		}
	}

	return &candidates[bestIdx], identicalCount
}

func candidateMetric(c TemplateCandidate) string {
	if m := c.Status.Profile.Metadata.Metric; m != "" {
		return string(m)
	}
	if c.Spec.Metric != nil {
		return string(*c.Spec.Metric)
	}
	return ""
}

func candidatePrecision(c TemplateCandidate) string {
	if p := c.Status.Profile.Metadata.Precision; p != "" {
		return string(p)
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
	if gpu := strings.TrimSpace(c.Status.Profile.Metadata.GPU); gpu != "" {
		return gpu
	}
	return ""
}

func candidateProfileType(c TemplateCandidate) string {
	return string(c.Status.Profile.Metadata.Type)
}

// Preference orders for template selection
var (
	gpuPreferenceOrder = []string{
		"MI325X", "MI300X", "MI250X", "MI210", "A100", "H100",
	}
	metricPreferenceOrder = []string{
		"latency", "throughput",
	}
	precisionPreferenceOrder = []string{
		"fp8", "fp16", "bf16", "fp32",
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
