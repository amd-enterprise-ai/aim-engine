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

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	// Kubernetes name limit
	kubernetesNameMaxLength = 63

	// Job name components
	discoveryJobPrefix = "discover-"
	discoveryJobSuffix = "-"

	// Hash length for job uniqueness (4 bytes = 8 hex chars)
	discoveryJobHashLength = 4
	discoveryJobHashHexLen = 8

	// DiscoveryJobBackoffLimit is the number of pod retries before marking the discovery job as failed.
	// Set to 0 so that pod failure immediately fails the job, allowing the controller to manage
	// retries with exponential backoff instead of Kubernetes' built-in retry mechanism.
	DiscoveryJobBackoffLimit = 0

	// DiscoveryJobTTLSeconds defines how long completed discovery jobs persist
	// before automatic cleanup. This allows time for status inspection and log retrieval.
	DiscoveryJobTTLSeconds = 60
)

// DiscoveryJobSpec defines parameters for creating a discovery job.
type DiscoveryJobSpec struct {
	TemplateName     string
	Namespace        string
	ModelID          string
	Image            string
	Env              []corev1.EnvVar
	ImagePullSecrets []corev1.LocalObjectReference
	ServiceAccount   string
	TemplateSpec     aimv1alpha1.AIMServiceTemplateSpecCommon
	// OwnerRef sets the owner reference on the discovery Job for garbage collection.
	// When the template is deleted, the discovery Job will be automatically cleaned up.
	OwnerRef metav1.OwnerReference
}

// BuildDiscoveryJob creates a Job that runs model discovery dry-run.
func BuildDiscoveryJob(spec DiscoveryJobSpec) *batchv1.Job {
	// Create deterministic job name with hash of ALL parameters that affect the Job spec
	// This ensures that any change to the spec results in a new Job instead of an update attempt
	hashInput := spec.ModelID + spec.Image + spec.ServiceAccount

	// Include env vars in hash (sorted for determinism)
	for _, env := range spec.Env {
		hashInput += env.Name + env.Value
	}

	// Include image pull secrets in hash
	for _, secret := range spec.ImagePullSecrets {
		hashInput += secret.Name
	}

	// Include template spec fields that affect env vars
	if spec.TemplateSpec.Metric != nil {
		hashInput += string(*spec.TemplateSpec.Metric)
	}
	if spec.TemplateSpec.Precision != nil {
		hashInput += string(*spec.TemplateSpec.Precision)
	}
	if spec.TemplateSpec.GpuSelector != nil {
		hashInput += spec.TemplateSpec.GpuSelector.Model + strconv.Itoa(int(spec.TemplateSpec.GpuSelector.Count))
	}
	if spec.TemplateSpec.ProfileId != "" {
		hashInput += spec.TemplateSpec.ProfileId
	}

	hash := sha256.Sum256([]byte(hashInput))
	hashHex := fmt.Sprintf("%x", hash[:discoveryJobHashLength])

	// Calculate max template name length to keep total <= 63 chars
	// Format: "discover-<template>-<hash>"
	reservedLength := len(discoveryJobPrefix) + len(discoveryJobSuffix) + discoveryJobHashHexLen
	maxTemplateNameLength := kubernetesNameMaxLength - reservedLength

	// Truncate template name if necessary
	templateName := spec.TemplateName
	if len(templateName) > maxTemplateNameLength {
		templateName = templateName[:maxTemplateNameLength]
	}

	jobName := fmt.Sprintf("%s%s%s%s", discoveryJobPrefix, templateName, discoveryJobSuffix, hashHex)

	backoffLimit := int32(DiscoveryJobBackoffLimit)
	ttlSeconds := int32(DiscoveryJobTTLSeconds)

	// Build environment variables
	env := []corev1.EnvVar{
		// Silence logging to produce clean JSON output
		{Name: "AIM_LOG_LEVEL_ROOT", Value: "CRITICAL"},
		{Name: "AIM_LOG_LEVEL", Value: "CRITICAL"},
	}
	env = append(env, spec.Env...)

	if spec.TemplateSpec.Metric != nil {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_METRIC",
			Value: string(*spec.TemplateSpec.Metric),
		})
	}

	if spec.TemplateSpec.Precision != nil {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_PRECISION",
			Value: string(*spec.TemplateSpec.Precision),
		})
	}

	if spec.TemplateSpec.GpuSelector != nil {
		if spec.TemplateSpec.GpuSelector.Model != "" {
			env = append(env, corev1.EnvVar{
				Name:  "AIM_GPU_MODEL",
				Value: spec.TemplateSpec.GpuSelector.Model,
			})
		}
		if spec.TemplateSpec.GpuSelector.Count > 0 {
			env = append(env, corev1.EnvVar{
				Name:  "AIM_GPU_COUNT",
				Value: strconv.Itoa(int(spec.TemplateSpec.GpuSelector.Count)),
			})
		}
	}

	// If a profile ID is set, propagate it to the discovery job
	if profileId := spec.TemplateSpec.ProfileId; profileId != "" {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_PROFILE_ID",
			Value: profileId,
		})
	}

	// Security context for pod security standards compliance
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	runAsUser := int64(65532) // Standard non-root user ID (commonly used in distroless images)
	seccompProfile := &corev1.SeccompProfile{
		Type: corev1.SeccompProfileTypeRuntimeDefault,
	}

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "aim-discovery",
				"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
				"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
				constants.LabelKeyTemplate:     spec.TemplateName,
			},
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						constants.LabelKeyTemplate: spec.TemplateName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ImagePullSecrets:   spec.ImagePullSecrets,
					ServiceAccountName: spec.ServiceAccount,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &runAsNonRoot,
						RunAsUser:      &runAsUser,
						SeccompProfile: seccompProfile,
					},
					Containers: []corev1.Container{
						{
							Name:  "discovery",
							Image: spec.Image,
							Args: []string{
								"dry-run",
								"--format=json",
							},
							Env: env,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								RunAsNonRoot:             &runAsNonRoot,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}

	return job
}

// FetchDiscoveryJob fetches the discovery job for a template.
// Returns the newest job (by CreationTimestamp) if multiple exist.
func FetchDiscoveryJob(ctx context.Context, c client.Client, namespace, templateName string) controllerutils.FetchResult[*batchv1.Job] {
	logger := log.FromContext(ctx)

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList,
		client.InNamespace(namespace),
		client.MatchingLabels{constants.LabelKeyTemplate: templateName},
	); err != nil {
		return controllerutils.FetchResult[*batchv1.Job]{Error: err}
	}

	if len(jobList.Items) == 0 {
		logger.V(1).Info("no discovery job found",
			"templateName", templateName,
			"namespace", namespace)
		return controllerutils.FetchResult[*batchv1.Job]{Value: nil}
	}

	// Sort by CreationTimestamp descending (newest first)
	sort.Slice(jobList.Items, func(i, j int) bool {
		return jobList.Items[i].CreationTimestamp.After(jobList.Items[j].CreationTimestamp.Time)
	})

	job := &jobList.Items[0]
	logger.V(1).Info("found discovery job",
		"templateName", templateName,
		"namespace", namespace,
		"jobName", job.Name,
		"isComplete", IsJobComplete(job))

	// Return the newest job
	return controllerutils.FetchResult[*batchv1.Job]{Value: job}
}

// IsJobComplete returns true if the job has completed (successfully or failed).
func IsJobComplete(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// IsJobSucceeded returns true if the job completed successfully.
func IsJobSucceeded(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// IsJobFailed returns true if the job failed.
func IsJobFailed(job *batchv1.Job) bool {
	if job == nil {
		return false
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// GetDiscoveryJobHealth inspects a discovery job to determine component health.
// This delegates to the shared GetJobHealth function which properly classifies
// failures using the error category system (terminal vs transient).
func GetDiscoveryJobHealth(job *batchv1.Job) controllerutils.ComponentHealth {
	if job == nil {
		// No job found - this is expected initially, will be created
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusPending,
			Reason:  "JobNotCreated",
			Message: "Discovery job has not been created yet",
		}
	}

	// Delegate to shared job health function which properly classifies errors
	return controllerutils.GetJobHealth(job)
}

// ============================================================================
// DISCOVERY LOG PARSING
// ============================================================================

// discoveryResult represents the raw output from a discovery job.
// This is an internal type used only for parsing the JSON output.
type discoveryResult struct {
	Filename string                 `json:"filename"`
	Profile  discoveryProfileResult `json:"profile"`
	Models   []discoveryModelResult `json:"models"`
}

// discoveryProfileResult is the raw profile format from discovery job output.
type discoveryProfileResult struct {
	Model          string            `json:"model"`
	QuantizedModel string            `json:"quantized_model"`
	Metadata       profileMetadata   `json:"metadata"`
	EngineArgs     map[string]any    `json:"engine_args"`
	EnvVars        map[string]string `json:"env_vars"`
}

// profileMetadata is the raw metadata format from discovery job output.
type profileMetadata struct {
	Engine    string `json:"engine"`
	GPU       string `json:"gpu"`
	Precision string `json:"precision"`
	GPUCount  int32  `json:"gpu_count"`
	Metric    string `json:"metric"`
	Type      string `json:"type"`
}

// discoveryModelResult represents a model in the raw discovery output.
type discoveryModelResult struct {
	Name   string  `json:"name"`
	Source string  `json:"source"`
	SizeGB float64 `json:"size_gb"`
}

// ParsedDiscovery holds the parsed discovery result.
type ParsedDiscovery struct {
	ModelSources []aimv1alpha1.AIMModelSource
	Profile      *aimv1alpha1.AIMProfile
}

// convertToAIMProfile converts the raw discovery profile to AIMProfile API type.
func convertToAIMProfile(raw discoveryProfileResult) (*aimv1alpha1.AIMProfile, error) {
	// Marshal engine args to JSON
	engineArgsBytes, err := json.Marshal(raw.EngineArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal engine args: %w", err)
	}

	return &aimv1alpha1.AIMProfile{
		EngineArgs: &apiextensionsv1.JSON{Raw: engineArgsBytes},
		EnvVars:    raw.EnvVars,
		Metadata: aimv1alpha1.AIMProfileMetadata{
			Engine:    raw.Metadata.Engine,
			GPU:       raw.Metadata.GPU,
			GPUCount:  raw.Metadata.GPUCount,
			Metric:    aimv1alpha1.AIMMetric(raw.Metadata.Metric),
			Precision: aimv1alpha1.AIMPrecision(raw.Metadata.Precision),
			Type:      aimv1alpha1.AIMProfileType(raw.Metadata.Type),
		},
	}, nil
}

// convertToAIMModelSources converts raw discovery models to AIMModelSource API types.
func convertToAIMModelSources(models []discoveryModelResult) []aimv1alpha1.AIMModelSource {
	var modelSources []aimv1alpha1.AIMModelSource
	for _, model := range models {
		// Convert GB to bytes for resource.Quantity
		sizeBytes := int64(model.SizeGB * 1024 * 1024 * 1024)
		size := resource.NewQuantity(sizeBytes, resource.BinarySI)

		modelSources = append(modelSources, aimv1alpha1.AIMModelSource{
			Name:      model.Name,
			SourceURI: model.Source,
			Size:      size,
		})
	}
	return modelSources
}

// findSuccessfulPodForJob locates a successfully completed pod for the given job.
func findSuccessfulPodForJob(ctx context.Context, c client.Client, job *batchv1.Job) (*corev1.Pod, error) {
	var podList corev1.PodList
	if err := c.List(ctx, &podList, client.InNamespace(job.Namespace), client.MatchingLabels{
		"job-name": job.Name,
	}); err != nil {
		return nil, fmt.Errorf("failed to list pods for job: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no pods found for job %s", job.Name)
	}

	// Find a successful pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded {
			return pod, nil
		}
	}

	return nil, fmt.Errorf("no successful pod found for job %s", job.Name)
}

// streamPodLogs retrieves logs from the specified pod's discovery container.
func streamPodLogs(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod) ([]byte, error) {
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: "discovery",
	})

	logs, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer func() {
		if closeErr := logs.Close(); closeErr != nil {
			// Log the error but don't fail the operation since we may have already read the logs
			fmt.Fprintf(os.Stderr, "warning: failed to close log stream: %v\n", closeErr)
		}
	}()

	logBytes, err := io.ReadAll(logs)
	if err != nil {
		return nil, fmt.Errorf("failed to read pod logs: %w", err)
	}

	return logBytes, nil
}

// extractLastValidJSONArray attempts to find and extract a valid JSON array from mixed log output.
// Returns the JSON bytes if found, or an error if extraction fails.
func extractLastValidJSONArray(logBytes []byte) ([]byte, error) {
	lastStartIdx := -1
	lastEndIdx := -1

	// Find the last occurrence of '[' that starts a valid JSON array
	for i := len(logBytes) - 1; i >= 0; i-- {
		if logBytes[i] == ']' && lastEndIdx == -1 {
			lastEndIdx = i
		}
		if logBytes[i] == '[' && lastEndIdx != -1 {
			// Try parsing from this '[' to the found ']'
			testBytes := logBytes[i : lastEndIdx+1]
			var testResults []discoveryResult
			if json.Unmarshal(testBytes, &testResults) == nil && len(testResults) > 0 {
				// Valid JSON array found
				lastStartIdx = i
				break
			}
		}
	}

	if lastStartIdx == -1 || lastEndIdx == -1 {
		return nil, fmt.Errorf("no valid JSON array found in logs")
	}

	return logBytes[lastStartIdx : lastEndIdx+1], nil
}

// parseDiscoveryJSON parses discovery results from log bytes, with fallback for mixed output.
func parseDiscoveryJSON(ctx context.Context, logBytes []byte) ([]discoveryResult, error) {
	// Check for cancellation before expensive parsing
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before parsing JSON: %w", err)
	}

	var results []discoveryResult
	if err := json.Unmarshal(logBytes, &results); err == nil {
		return results, nil
	}

	// Try extracting the last valid JSON array from mixed stdout/stderr
	jsonBytes, err := extractLastValidJSONArray(logBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse discovery JSON: %w", err)
	}

	if err := json.Unmarshal(jsonBytes, &results); err != nil {
		return nil, fmt.Errorf("failed to parse extracted JSON array: %w", err)
	}

	return results, nil
}

// ParseDiscoveryLogs parses the discovery job output to extract model sources and profile.
// Reads pod logs from the completed job and parses the JSON output.
func ParseDiscoveryLogs(ctx context.Context, c client.Client, clientset kubernetes.Interface, job *batchv1.Job) (*ParsedDiscovery, error) {
	// Check for cancellation early
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before parsing discovery logs: %w", err)
	}

	if !IsJobSucceeded(job) {
		return nil, fmt.Errorf("job has not succeeded yet")
	}

	// Find successful pod
	successfulPod, err := findSuccessfulPodForJob(ctx, c, job)
	if err != nil {
		return nil, err
	}

	// Stream pod logs
	logBytes, err := streamPodLogs(ctx, clientset, successfulPod)
	if err != nil {
		return nil, err
	}

	// Parse discovery JSON
	results, err := parseDiscoveryJSON(ctx, logBytes)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("discovery output contains empty array")
	}

	// Use the first result
	result := results[0]

	// Convert raw discovery profile to AIMProfile
	profile, err := convertToAIMProfile(result.Profile)
	if err != nil {
		return nil, fmt.Errorf("failed to convert profile: %w", err)
	}

	// Convert raw models to AIMModelSource
	modelSources := convertToAIMModelSources(result.Models)

	return &ParsedDiscovery{
		ModelSources: modelSources,
		Profile:      profile,
	}, nil
}

// ============================================================================
// DISCOVERY JOB STATE HELPERS
// ============================================================================

// ShouldCheckDiscoveryJob returns true if we should check for discovery jobs.
// We skip checking if the template is already ready or has inline model sources.
func ShouldCheckDiscoveryJob(template *aimv1alpha1.AIMServiceTemplate) bool {
	// Don't check for discovery job if template is already ready
	if template.Status.Status == constants.AIMStatusReady {
		return false
	}
	// Don't check if inline model sources are provided
	if len(template.Spec.ModelSources) > 0 {
		return false
	}
	// Don't check if status is NotAvailable (GPU unavailable)
	if template.Status.Status == constants.AIMStatusNotAvailable {
		return false
	}
	return true
}

// ShouldCheckClusterTemplateDiscoveryJob returns true if we should check for discovery jobs
// for cluster-scoped templates.
func ShouldCheckClusterTemplateDiscoveryJob(template *aimv1alpha1.AIMClusterServiceTemplate) bool {
	if template.Status.Status == constants.AIMStatusReady {
		return false
	}
	if len(template.Spec.ModelSources) > 0 {
		return false
	}
	if template.Status.Status == constants.AIMStatusNotAvailable {
		return false
	}
	return true
}

// HasCompletedDiscoveryJob returns true if a discovery job has completed (succeeded or failed).
func HasCompletedDiscoveryJob(jobResult controllerutils.FetchResult[*batchv1.Job]) bool {
	if !jobResult.OK() || jobResult.Value == nil {
		return false
	}
	return IsJobComplete(jobResult.Value)
}

// HasActiveDiscoveryJob returns true if a discovery job exists and is actively running.
func HasActiveDiscoveryJob(jobResult controllerutils.FetchResult[*batchv1.Job]) bool {
	if !jobResult.OK() || jobResult.Value == nil {
		return false
	}
	return !IsJobComplete(jobResult.Value)
}

// ============================================================================
// CONCURRENT JOB LIMITING & BACKOFF
// ============================================================================

// CountActiveDiscoveryJobs counts the number of active (non-complete) discovery jobs
// across all namespaces. This is used to enforce the concurrent job limit.
func CountActiveDiscoveryJobs(ctx context.Context, c client.Client) (int, error) {
	logger := log.FromContext(ctx)

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.MatchingLabels{
		"app.kubernetes.io/name":       "aim-discovery",
		"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
		"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
	}); err != nil {
		logger.Error(err, "Failed to list discovery jobs")
		return 0, err
	}

	activeCount := 0
	for i := range jobList.Items {
		if !IsJobComplete(&jobList.Items[i]) {
			activeCount++
		}
	}

	return activeCount, nil
}

// CalculateBackoffDuration computes the backoff duration for the given attempt number.
// Uses exponential backoff: base * 2^(attempts-1), capped at max.
func CalculateBackoffDuration(attempts int32) time.Duration {
	if attempts <= 0 {
		return 0
	}

	// Calculate exponential backoff: base * 2^(attempts-1)
	multiplier := int64(1) << (attempts - 1) // 2^(attempts-1)
	backoffSeconds := int64(constants.DiscoveryBaseBackoffSeconds) * multiplier

	// Cap at maximum
	if backoffSeconds > int64(constants.DiscoveryMaxBackoffSeconds) {
		backoffSeconds = int64(constants.DiscoveryMaxBackoffSeconds)
	}

	return time.Duration(backoffSeconds) * time.Second
}

// ShouldCreateDiscoveryJob determines whether a new discovery job should be created
// based on backoff timing from previous attempts.
// Returns (shouldCreate, reason, message).
func ShouldCreateDiscoveryJob(discoveryState *aimv1alpha1.DiscoveryState, now time.Time) (bool, string, string) {
	// No discovery state means this is the first attempt
	if discoveryState == nil || discoveryState.Attempts == 0 {
		return true, "", ""
	}

	// Check backoff period
	if discoveryState.LastAttemptTime != nil && discoveryState.Attempts > 0 {
		backoffDuration := CalculateBackoffDuration(discoveryState.Attempts)
		nextAttemptTime := discoveryState.LastAttemptTime.Add(backoffDuration)

		if now.Before(nextAttemptTime) {
			remaining := nextAttemptTime.Sub(now).Round(time.Second)
			return false, aimv1alpha1.AIMTemplateReasonAwaitingDiscovery,
				fmt.Sprintf("Waiting %s before retry (attempt %d)", remaining, discoveryState.Attempts+1)
		}
	}

	return true, "", ""
}

// GetJobFailureReason extracts the failure reason from a failed job.
// Returns empty string if the job hasn't failed.
func GetJobFailureReason(job *batchv1.Job) string {
	if job == nil {
		return ""
	}

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if condition.Message != "" {
				return condition.Message
			}
			if condition.Reason != "" {
				return condition.Reason
			}
			return "Unknown failure"
		}
	}

	return ""
}
