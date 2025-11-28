package aimservicetemplate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// FETCH
// ============================================================================

type ServiceTemplateDiscoveryFetchResult struct {
	DiscoveryJob    *batchv1.Job
	ParsedDiscovery *ParsedDiscovery
	ParseError      error
}

// ParsedDiscovery holds the parsed discovery result
type ParsedDiscovery struct {
	ModelSources []aimv1alpha1.AIMModelSource
	Profile      *aimv1alpha1.AIMProfile
}

func fetchDiscoveryResult(ctx context.Context, c client.Client, clientSet kubernetes.Interface, status aimv1alpha1.AIMServiceTemplateStatus) (*ServiceTemplateDiscoveryFetchResult, error) {
	if ref := status.DiscoveryJobRef; ref != nil && shouldDiscoveryRun(status) {
		discoveryResult := ServiceTemplateDiscoveryFetchResult{}
		discoveryJob := &batchv1.Job{}
		discoveryJobErr := c.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace}, discoveryJob)
		if discoveryJobErr != nil && !apierrors.IsNotFound(discoveryJobErr) {
			return nil, fmt.Errorf("failed to fetch discovery job: %w", discoveryJobErr)
		}
		discoveryResult.DiscoveryJob = discoveryJob

		if discoveryJobErr == nil && utils.IsJobSucceeded(discoveryJob) {
			discovery, logParseErr := ParseDiscoveryLogs(ctx, c, clientSet, discoveryJob)
			discoveryResult.ParsedDiscovery = discovery
			discoveryResult.ParseError = logParseErr
		}
		return &discoveryResult, nil
	}
	return nil, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceTemplateDiscoveryObservation struct {
	ShouldRun       bool
	Failed          bool
	Completed       bool
	DiscoveryResult *ParsedDiscovery
}

func observeDiscovery(discoveryResult *ServiceTemplateDiscoveryFetchResult, status aimv1alpha1.AIMServiceTemplateStatus) ServiceTemplateDiscoveryObservation {
	// TODO update
	obs := ServiceTemplateDiscoveryObservation{}
	// If there is no discovery
	if discoveryResult == nil {
		obs.ShouldRun = shouldDiscoveryRun(status)
		return obs
	}

	job := discoveryResult.DiscoveryJob
	obs.Completed = utils.IsJobComplete(job)
	obs.Failed = utils.IsJobFailed(job)
	if obs.Completed {
		obs.DiscoveryResult = discoveryResult.ParsedDiscovery
	}
	return obs
}

func shouldDiscoveryRun(status aimv1alpha1.AIMServiceTemplateStatus) bool {
	return status.Profile == nil
}

// ============================================================================
// BUILD
// ============================================================================

const (
	// Kubernetes name limit
	kubernetesNameMaxLength = 63

	// Job name components
	discoveryJobPrefix = "discover-"
	discoveryJobSuffix = "-"

	// Hash length for job uniqueness (4 bytes = 8 hex chars)
	discoveryJobHashLength = 4
	discoveryJobHashHexLen = 8

	// DiscoveryJobBackoffLimit is the number of retries before marking the discovery job as failed
	DiscoveryJobBackoffLimit = 3

	// DiscoveryJobTTLSeconds defines how long completed discovery jobs persist
	// before automatic cleanup. This allows time for status inspection and log retrieval.
	DiscoveryJobTTLSeconds = 60
)

// DiscoveryJobBuilderInputs contains the data needed to build a discovery job
type DiscoveryJobBuilderInputs struct {
	TemplateName string
	TemplateSpec aimv1alpha1.AIMServiceTemplateSpecCommon
	Env          []corev1.EnvVar // Auth env vars for model download
	Namespace    string
	Image        string // From observation, not spec
}

// BuildDiscoveryJob creates a Job that runs model discovery dry-run
func BuildDiscoveryJob(inputs DiscoveryJobBuilderInputs) *batchv1.Job {
	// Create deterministic job name with hash of ALL parameters that affect the Job spec
	// This ensures that any change to the spec results in a new Job instead of an update attempt

	jobName, _ := utils.GenerateDerivedNameWithHashLength(
		[]string{""},
		4,
		inputs.TemplateSpec.ModelName,
		inputs.Image,
		inputs.TemplateSpec.ServiceAccountName,
	)

	backoffLimit := int32(DiscoveryJobBackoffLimit)
	ttlSeconds := int32(DiscoveryJobTTLSeconds)

	// Add AIM environmental variables

	// Silence logging to produce clean JSON output
	env := []corev1.EnvVar{
		{
			Name:  "AIM_LOG_LEVEL_ROOT",
			Value: "CRITICAL",
		},
		{
			Name:  "AIM_LOG_LEVEL",
			Value: "CRITICAL",
		},
	}
	env = append(env, inputs.Env...)

	if inputs.TemplateSpec.Metric != nil {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_METRIC",
			Value: string(*inputs.TemplateSpec.Metric),
		})
	}

	if inputs.TemplateSpec.Precision != nil {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_PRECISION",
			Value: string(*inputs.TemplateSpec.Precision),
		})
	}

	if inputs.TemplateSpec.GpuSelector != nil {
		if inputs.TemplateSpec.GpuSelector.Model != "" {
			env = append(env, corev1.EnvVar{
				Name:  "AIM_GPU_MODEL",
				Value: inputs.TemplateSpec.GpuSelector.Model,
			})
		}
		if inputs.TemplateSpec.GpuSelector.Count > 0 {
			env = append(env, corev1.EnvVar{
				Name:  "AIM_GPU_COUNT",
				Value: strconv.Itoa(int(inputs.TemplateSpec.GpuSelector.Count)),
			})
		}
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
			Namespace: inputs.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       constants.LabelValueDiscoveryName,
				"app.kubernetes.io/component":  constants.LabelValueDiscoveryComponent,
				"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
				constants.LabelKeyTemplate:     inputs.TemplateName,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ImagePullSecrets:   inputs.TemplateSpec.ImagePullSecrets,
					ServiceAccountName: inputs.TemplateSpec.ServiceAccountName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &runAsNonRoot,
						RunAsUser:      &runAsUser,
						SeccompProfile: seccompProfile,
					},
					Containers: []corev1.Container{
						{
							Name:  "discovery",
							Image: inputs.Image,
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

// ============================================================================
// PROJECT
// ============================================================================

func projectDiscovery(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	observation ServiceTemplateDiscoveryObservation,
) bool {
	//if status.DiscoveryJobRef == nil {
	//	if job := observation.ClusterServiceTemplateFetchResult.Discovery.DiscoveryJob; job != nil {
	//		status.DiscoveryJobRef = &types.NamespacedName{Name: job.Name, Namespace: job.Namespace}
	//	}
	//}
	// TODO
	// Set ref
	return true
}

// UTILS

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
			// Note: In production, this should use a proper logger from the context
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
func ParseDiscoveryLogs(ctx context.Context, k8sClient client.Client, clientset kubernetes.Interface, job *batchv1.Job) (*ParsedDiscovery, error) {
	// Check for cancellation early
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before parsing discovery logs: %w", err)
	}

	if !utils.IsJobSucceeded(job) {
		return nil, fmt.Errorf("job has not succeeded yet")
	}

	// Find successful pod
	successfulPod, err := utils.FindSuccessfulPodForJob(ctx, k8sClient, job)
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

// convertToAIMProfile converts the raw discovery profile to AIMProfile API type
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
		},
	}, nil
}

// convertToAIMModelSources converts raw discovery models to AIMModelSource API types
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

// discoveryResult represents the raw output from a discovery job.
// This is an internal type used only for parsing the JSON output.
type discoveryResult struct {
	Filename string                 `json:"filename"`
	Profile  discoveryProfileResult `json:"profile"`
	Models   []discoveryModelResult `json:"models"`
}

// discoveryProfileResult is the raw profile format from discovery job output
type discoveryProfileResult struct {
	Model          string            `json:"model"`
	QuantizedModel string            `json:"quantized_model"`
	Metadata       profileMetadata   `json:"metadata"`
	EngineArgs     map[string]any    `json:"engine_args"`
	EnvVars        map[string]string `json:"env_vars"`
}

// profileMetadata is the raw metadata format from discovery job output
type profileMetadata struct {
	Engine    string `json:"engine"`
	GPU       string `json:"gpu"`
	Precision string `json:"precision"`
	GPUCount  int32  `json:"gpu_count"`
	Metric    string `json:"metric"`
}

// discoveryModelResult represents a model in the raw discovery output
type discoveryModelResult struct {
	Name   string  `json:"name"`
	Source string  `json:"source"`
	SizeGB float64 `json:"size_gb"`
}

// DiscoveryJobSpec defines parameters for creating a discovery job
type DiscoveryJobSpec struct {
	TemplateName     string
	TemplateSpec     aimv1alpha1.AIMServiceTemplateSpecCommon
	Namespace        string
	ModelID          string
	Image            string
	Env              []corev1.EnvVar
	ImagePullSecrets []corev1.LocalObjectReference
	ServiceAccount   string
	OwnerRef         metav1.OwnerReference
}
