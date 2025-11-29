/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// DefaultSharedMemorySize is the default size allocated for /dev/shm in inference containers.
	// This is required for efficient inter-process communication in model serving workloads.
	DefaultSharedMemorySize = "8Gi"
)

// InferenceServiceNameForService creates a deterministic name for the KServe InferenceService based off an AIMService
func InferenceServiceNameForService(service *aimv1alpha1.AIMService) string {
	namespace := service.Namespace

	// KServe internally appends "-{namespace}" to the InferenceService name
	// We need to ensure: len(isvcName + "-" + namespace) <= 63
	// Include namespace in truncation calculation, then remove it
	// Format from GenerateDerivedName: {serviceName}-{namespace}-{hash}
	truncatedName, _ := utils.GenerateDerivedNameWithHashLength([]string{service.Name, namespace}, 4, service.UID)

	// Remove the "-{namespace}" suffix that was added (it's right before the hash)
	// We know the hash is 4 characters, so we can work backwards
	// Format: ....-{namespace}-{4-char-hash}
	// We want: ....-{4-char-hash}
	suffixToRemove := "-" + namespace
	hashSuffix := truncatedName[len(truncatedName)-5:] // "-{hash}" (1 dash + 4 chars)
	if strings.HasSuffix(truncatedName[:len(truncatedName)-5], suffixToRemove) {
		// Remove "-{namespace}" and re-add "-{hash}"
		withoutNamespace := strings.TrimSuffix(truncatedName[:len(truncatedName)-5], suffixToRemove)
		truncatedName = withoutNamespace + hashSuffix
	}

	return truncatedName
}

// ============================================================================
// FETCH
// ============================================================================

type ServiceKServeFetchResult struct {
	InferenceService *servingv1beta1.InferenceService
	Pods             []corev1.Pod
}

func fetchServiceKServeResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServiceKServeFetchResult, error) {
	result := ServiceKServeFetchResult{}

	inferenceService := &servingv1beta1.InferenceService{}
	if err := c.Get(ctx, client.ObjectKey{Name: InferenceServiceNameForService(service), Namespace: service.Namespace}, inferenceService); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("error fetching InferenceService: %w", err)
	} else if err == nil {
		result.InferenceService = inferenceService
	}

	if result.InferenceService != nil {
		// Fetch pods for InferenceService to detect image pull errors
		// KServe creates pods with the component label
		var podList corev1.PodList
		if err := c.List(ctx, &podList,
			client.InNamespace(service.Namespace),
			client.MatchingLabels{
				"serving.kserve.io/inferenceservice": InferenceServiceNameForService(service),
			}); err != nil {
			// Log error but don't fail - pod fetching is for diagnostics only
			// We can still continue without pod information
		} else {
			result.Pods = podList.Items
		}
	}

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceKServeObservation struct {
	InferenceServiceExists bool
	InferenceServiceReady  bool
	ShouldCreateISVC       bool
	LastFailureInfo        *servingv1beta1.FailureInfo
	PodImagePullError      *utils.ImagePullError
}

func observeServiceKServe(result ServiceKServeFetchResult) ServiceKServeObservation {
	obs := ServiceKServeObservation{}

	if result.InferenceService != nil {
		obs.InferenceServiceExists = true

		// Check if InferenceService is ready using KServe's built-in status method
		obs.InferenceServiceReady = result.InferenceService.Status.IsReady()

		// Extract LastFailureInfo from ModelStatus if available
		if result.InferenceService.Status.ModelStatus.LastFailureInfo != nil {
			obs.LastFailureInfo = result.InferenceService.Status.ModelStatus.LastFailureInfo
		}

		// Check pod statuses for image pull errors
		obs.PodImagePullError = checkPodImagePullErrors(result.Pods)
	} else {
		obs.ShouldCreateISVC = true
	}

	return obs
}

// checkPodImagePullErrors checks pod container statuses for image pull failures
func checkPodImagePullErrors(pods []corev1.Pod) *utils.ImagePullError {
	for i := range pods {
		if err := utils.CheckPodImagePullStatus(&pods[i]); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// PLAN
// ============================================================================

func planServiceInferenceService(
	service *aimv1alpha1.AIMService,
	obs ServiceKServeObservation,
	modelImage string,
	modelName string,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	pvcObs ServicePVCObservation,
	cachingObs ServiceCachingObservation,
) (client.Object, error) {
	if !obs.ShouldCreateISVC {
		return nil, nil
	}

	return buildInferenceService(
		service,
		modelImage,
		modelName,
		templateName,
		templateSpec,
		templateStatus,
		pvcObs,
		cachingObs,
	), nil
}

// buildInferenceService creates a KServe InferenceService for the AIMService inline (no separate ServingRuntime).
func buildInferenceService(
	service *aimv1alpha1.AIMService,
	modelImage string,
	modelName string,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	pvcObs ServicePVCObservation,
	cachingObs ServiceCachingObservation,
) *servingv1beta1.InferenceService {
	// Build labels (user labels + system labels)
	labels := make(map[string]string)
	if service.Labels != nil {
		for k, v := range service.Labels {
			labels[k] = v
		}
	}

	modelNameLabel, _ := utils.SanitizeLabelValue(modelName)
	serviceNameLabel, _ := utils.SanitizeLabelValue(service.Name)

	systemLabels := map[string]string{
		"app.kubernetes.io/name":       constants.LabelValueServiceName,
		"app.kubernetes.io/component":  constants.LabelValueServiceComponent,
		"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
		constants.LabelKeyTemplate:     templateName,
		constants.LabelKeyModelName:    modelNameLabel,
		constants.LabelKeyServiceName:  serviceNameLabel,
	}
	for k, v := range systemLabels {
		labels[k] = v
	}

	// Add metric and precision labels if available
	if templateStatus != nil && templateStatus.Profile != nil {
		labels[constants.LabelKeyMetric], _ = utils.SanitizeLabelValue(string(templateStatus.Profile.Metadata.Metric))
		labels[constants.LabelKeyPrecision], _ = utils.SanitizeLabelValue(string(templateStatus.Profile.Metadata.Precision))
	}

	// Build environment variables (merge service env)
	env := []corev1.EnvVar{
		{
			Name:  "VLLM_ENABLE_METRICS",
			Value: "true",
		},
	}
	// Add model ID if template has model sources
	if templateStatus != nil && len(templateStatus.ModelSources) > 0 {
		env = append(env, corev1.EnvVar{
			Name:  "AIM_MODEL_ID",
			Value: templateStatus.ModelSources[0].SourceURI,
		})
	}
	// Append service-specific env vars
	env = append(env, service.Spec.Env...)

	// Build resource requirements
	resources := buildResourceRequirements(service, templateStatus)

	// Create base InferenceService
	isvc := &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       "InferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        InferenceServiceNameForService(service),
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: service.Annotations,
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				PodSpec: servingv1beta1.PodSpec{
					ImagePullSecrets:   service.Spec.ImagePullSecrets,
					ServiceAccountName: service.Spec.ServiceAccountName,
				},
				Model: &servingv1beta1.ModelSpec{
					ModelFormat: servingv1beta1.ModelFormat{
						Name:    "pytorch",
						Version: ptr.To("1"),
					},
					PredictorExtensionSpec: servingv1beta1.PredictorExtensionSpec{
						Container: corev1.Container{
							Image:     modelImage,
							Env:       env,
							Resources: resources,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8000,
									Name:          "http",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dshm",
									MountPath: "/dev/shm",
								},
							},
						},
					},
				},
			},
		},
	}

	// Set replicas if specified
	if service.Spec.Replicas != nil {
		isvc.Spec.Predictor.MinReplicas = service.Spec.Replicas
		isvc.Spec.Predictor.MaxReplicas = *service.Spec.Replicas
	}

	// Add shared memory volume for vLLM inter-process communication
	dshmSizeLimit := resource.MustParse(DefaultSharedMemorySize)
	isvc.Spec.Predictor.Volumes = []corev1.Volume{
		{
			Name: "dshm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &dshmSizeLimit,
				},
			},
		},
	}

	// Add volumes and volume mounts
	addVolumeMounts(isvc, pvcObs, cachingObs)

	return isvc
}

// buildResourceRequirements builds resource requirements for the container.
// Service-level resources override template defaults.
func buildResourceRequirements(service *aimv1alpha1.AIMService, templateStatus *aimv1alpha1.AIMServiceTemplateStatus) corev1.ResourceRequirements {
	// Start with service resources if specified
	if service.Spec.Resources != nil {
		return *service.Spec.Resources
	}

	// Fall back to template-based defaults
	if templateStatus == nil || templateStatus.Profile == nil {
		return corev1.ResourceRequirements{}
	}

	gpuCount := int64(templateStatus.Profile.Metadata.GPUCount)
	if gpuCount <= 0 {
		return corev1.ResourceRequirements{}
	}

	// Default resource requirements based on GPU count
	requests := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewQuantity(gpuCount*4, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(gpuCount*32, resource.BinarySI), // 32Gi per GPU
	}

	limits := corev1.ResourceList{
		corev1.ResourceMemory: *resource.NewQuantity(gpuCount*48, resource.BinarySI), // 48Gi per GPU
	}

	// Add GPU resource request/limit
	gpuResourceName := corev1.ResourceName("amd.com/gpu")
	requests[gpuResourceName] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
	limits[gpuResourceName] = *resource.NewQuantity(gpuCount, resource.DecimalSI)

	return corev1.ResourceRequirements{
		Requests: requests,
		Limits:   limits,
	}
}

// addVolumeMounts adds PVC and model cache volume mounts to the InferenceService.
func addVolumeMounts(isvc *servingv1beta1.InferenceService, pvcObs ServicePVCObservation, cachingObs ServiceCachingObservation) {
	// Add service PVC mount if using PVC (not using cache)
	if pvcObs.ShouldUsePVC && pvcObs.PVCReady && pvcObs.PVCName != "" {
		addServicePVCMount(isvc, pvcObs.PVCName)
	}

	// Add model cache mounts
	for _, mount := range cachingObs.ModelCachesToMount {
		addModelCacheMount(isvc, mount.Cache, mount.ModelName)
	}
}

// addModelCacheMount adds a model cache PVC volume mount to an InferenceService.
func addModelCacheMount(isvc *servingv1beta1.InferenceService, modelCache aimv1alpha1.AIMModelCache, modelName string) {
	// Sanitize volume name for Kubernetes (no dots allowed in volume names, only lowercase alphanumeric and '-')
	volumeName := utils.MakeRFC1123Compliant(modelCache.Name)
	volumeName = strings.ReplaceAll(volumeName, ".", "-")

	// Add the PVC volume for the model cache
	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: modelCache.Status.PersistentVolumeClaim,
			},
		},
	})

	// Mount at the AIM cache base path + model name
	// e.g., /workspace/model-cache/meta-llama/Llama-3.1-8B
	mountPath := filepath.Join(AIMCacheBasePath, modelName)

	isvc.Spec.Predictor.Model.VolumeMounts = append(
		isvc.Spec.Predictor.Model.VolumeMounts,
		corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		},
	)
}

// ============================================================================
// PROJECT
// ============================================================================

func projectServiceKServe(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs ServiceKServeObservation,
) bool {
	if obs.ShouldCreateISVC {
		h.Progressing(aimv1alpha1.AIMServiceReasonCreatingRuntime, "Creating InferenceService")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRuntimeReady, aimv1alpha1.AIMServiceReasonCreatingRuntime, "InferenceService being created", controllerutils.LevelNormal)
		return false
	}

	// Check for image pull errors first - these are blocking errors
	if obs.PodImagePullError != nil {
		pullErr := obs.PodImagePullError

		// Determine reason based on categorized error type
		var reason string
		switch pullErr.Type {
		case utils.ImagePullErrorAuth:
			reason = "ImagePullAuthFailure"
		case utils.ImagePullErrorNotFound:
			reason = aimv1alpha1.AIMServiceReasonImageNotFound
		default:
			reason = aimv1alpha1.AIMServiceReasonImagePullBackOff
		}

		// Format detailed message with container information
		containerType := "Container"
		if pullErr.IsInitContainer {
			containerType = "Init container"
		}
		message := fmt.Sprintf("%s %q failed to pull image: %s", containerType, pullErr.Container, pullErr.Message)

		h.Degraded(reason, message)
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRuntimeReady, reason, message, controllerutils.LevelWarning)
		return true // Blocking error
	}

	// Check for LastFailureInfo from KServe
	if obs.LastFailureInfo != nil {
		// Build detailed error message from FailureInfo
		failureMsg := obs.LastFailureInfo.Message
		if obs.LastFailureInfo.Reason != "" {
			failureMsg = fmt.Sprintf("%s: %s", obs.LastFailureInfo.Reason, failureMsg)
		}
		if obs.LastFailureInfo.Location != "" {
			failureMsg = fmt.Sprintf("%s (location: %s)", failureMsg, obs.LastFailureInfo.Location)
		}
		if obs.LastFailureInfo.ExitCode != 0 {
			failureMsg = fmt.Sprintf("%s (exit code: %d)", failureMsg, obs.LastFailureInfo.ExitCode)
		}

		h.Degraded(aimv1alpha1.AIMServiceReasonRuntimeFailed, failureMsg)
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRuntimeReady, aimv1alpha1.AIMServiceReasonRuntimeFailed, failureMsg, controllerutils.LevelWarning)
		return true // Blocking error
	}

	if obs.InferenceServiceExists && !obs.InferenceServiceReady {
		h.Progressing(aimv1alpha1.AIMServiceReasonCreatingRuntime, "Waiting for InferenceService to become ready")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRuntimeReady, aimv1alpha1.AIMServiceReasonCreatingRuntime, "InferenceService is not ready", controllerutils.LevelNormal)
		return false
	}

	if obs.InferenceServiceReady {
		status.Status = aimv1alpha1.AIMServiceStatusRunning
		cm.MarkTrue(aimv1alpha1.AIMServiceConditionRuntimeReady, aimv1alpha1.AIMServiceReasonRuntimeReady, "InferenceService is ready", controllerutils.LevelNormal)
	}

	return false
}

// Reference

//// HandleInferenceServicePodImageError checks for image pull errors in InferenceService pods.
//// Returns true if an image pull error was detected.
//func HandleInferenceServicePodImageError(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.InferenceServicePodImageError == nil {
//		return false
//	}
//
//	pullErr := obs.InferenceServicePodImageError
//
//	// Determine the condition reason based on error type
//	var conditionReason string
//	switch pullErr.Type {
//	case aimmodel2.ImagePullErrorAuth:
//		conditionReason = aimv1alpha1.AIMServiceReasonImagePullAuthFailure
//	case aimmodel2.ImagePullErrorNotFound:
//		conditionReason = aimv1alpha1.AIMServiceReasonImageNotFound
//	default:
//		conditionReason = aimv1alpha1.AIMServiceReasonImagePullBackOff
//	}
//
//	// Format detailed message
//	containerType := "Container"
//	if pullErr.IsInitContainer {
//		containerType = "Init container"
//	}
//	detailedMessage := fmt.Sprintf("InferenceService pod %s %q is stuck in %s: %s",
//		containerType, pullErr.Container, pullErr.Reason, pullErr.Message)
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, conditionReason, detailedMessage)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, conditionReason,
//		"InferenceService cannot run due to image pull failure")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, conditionReason,
//		"Service is degraded due to image pull failure")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, conditionReason,
//		"Service cannot be ready due to image pull failure")
//	return true
//}
//
//// EvaluateInferenceServiceStatus checks InferenceService and routing readiness.
//// Updates status conditions based on the InferenceService and routing state.
//func EvaluateInferenceServiceStatus(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	inferenceService *servingv1beta1.InferenceService,
//	httpRoute *gatewayapiv1.HTTPRoute,
//	routingEnabled bool,
//	routingReady bool,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) {
//	if inferenceService == nil {
//		if status.Status != aimv1alpha1.AIMServiceStatusFailed {
//			status.Status = aimv1alpha1.AIMServiceStatusStarting
//		}
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"Waiting for InferenceService creation")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"Reconciling InferenceService resources")
//		setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"InferenceService not yet created")
//		return
//	}
//
//	if inferenceService.Status.IsReady() && routingReady {
//		status.Status = aimv1alpha1.AIMServiceStatusRunning
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"InferenceService is ready")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"Service is running")
//		setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"AIMService is ready to serve traffic")
//		return
//	}
//
//	if status.Status != aimv1alpha1.AIMServiceStatusFailed && status.Status != aimv1alpha1.AIMServiceStatusDegraded {
//		status.Status = aimv1alpha1.AIMServiceStatusStarting
//	}
//	reason := aimv1alpha1.AIMServiceReasonCreatingRuntime
//	message := "Waiting for InferenceService to become ready"
//	if inferenceService.Status.ModelStatus.LastFailureInfo != nil {
//		reason = aimv1alpha1.AIMServiceReasonRuntimeFailed
//		message = inferenceService.Status.ModelStatus.LastFailureInfo.Message
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	}
//	if routingEnabled && !routingReady && reason == aimv1alpha1.AIMServiceReasonCreatingRuntime {
//		reason = aimv1alpha1.AIMServiceReasonConfiguringRoute
//		message = "Waiting for HTTPRoute to become ready"
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, reason, "InferenceService reconciliation in progress")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//}
