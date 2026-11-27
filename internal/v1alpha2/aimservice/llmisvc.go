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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/kmeta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimadapter"
	servicepolicy "github.com/amd-enterprise-ai/aim-engine/internal/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1svc "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	llmInferenceServiceComponent     = "LLMInferenceService"
	llmInferenceServicePodsComponent = "LLMInferenceServicePods"
	servingAPIComponent              = "ServingAPI"

	reasonLLMInferenceServiceNotReady = "LLMInferenceServiceNotReady"

	llmWorkloadDeploymentPollInterval = 30 * time.Second
	kserveConfigPollInterval          = time.Minute
)

// resolveServingAPI returns the KServe API that serves the service.
// spec.servingAPI is immutable, so a service never moves between kinds.
func resolveServingAPI(service *aimv1alpha2.AIMService) aimv1alpha1.AIMServingAPI {
	return service.Spec.GetServingAPI()
}

// servingAPIError explains why the requested serving API cannot serve the
// service.
type servingAPIError struct {
	reason string
	err    error
}

func (e *servingAPIError) Error() string { return e.err.Error() }

func (e *servingAPIError) Unwrap() error { return e.err }

// validateServingAPI rejects an LLMInferenceService-served service when the
// operator runs without the KServe LLMInferenceService API, or when KServe
// serves LLMInferenceServices over TLS. KServe then switches the probes and the
// workload Service to HTTPS, while AIM images serve plain HTTP, so the workload
// could never become ready.
func validateServingAPI(
	servingAPI aimv1alpha1.AIMServingAPI,
	support serving.LLMInferenceServiceSupport,
	kserveConfig controllerutils.FetchResult[*corev1.ConfigMap],
) error {
	if servingAPI != aimv1alpha1.ServingAPILLMInferenceService {
		return nil
	}
	if !support.Enabled {
		return &servingAPIError{
			reason: aimv1alpha1.AIMServiceReasonServingAPIUnsupported,
			err: errors.New("spec.servingAPI is LLMInferenceService, but AIM Engine runs without KServe " +
				"LLMInferenceService support: install the serving.kserve.io/v1alpha2 LLMInferenceService CRDs " +
				"and restart the operator with --enable-llm-inference-service"),
		}
	}
	tls, err := llmInferenceServiceTLSEnabled(kserveConfig)
	if err != nil {
		return &servingAPIError{reason: reasonFetchError, err: err}
	}
	if tls {
		return &servingAPIError{
			reason: aimv1alpha1.AIMServiceReasonLLMInferenceServiceTLSEnabled,
			err: fmt.Errorf("KServe serves LLMInferenceServices over TLS (enableLLMInferenceServiceTLS in the "+
				"ingress config of ConfigMap %s/%s), which AIM images do not support: disable it, or use "+
				"spec.servingAPI=InferenceService", support.KServeNamespace, kserveconstants.InferenceServiceConfigMapName),
		}
	}
	return nil
}

// deleteInapplicableLLMConditions removes the conditions of the
// LLMInferenceService checks that no longer apply to the service. The pipeline
// only updates the conditions of the components it is given, so a failure that
// stops applying, for example after its baseRefs are removed, would otherwise
// stay False.
func deleteInapplicableLLMConditions(cm *controllerutils.ConditionManager, obs ServiceObservation) {
	if cm == nil {
		return
	}
	if obs.servingAPI != aimv1alpha1.ServingAPILLMInferenceService {
		cm.Delete(servingAPIComponent + controllerutils.ComponentConditionSuffix)
	}
	if len(obs.llmBaseRefs) == 0 {
		cm.Delete(llmBaseRefsComponent + controllerutils.ComponentConditionSuffix)
	}
	if !llmAutoscalingApplies(obs.service, obs.ServiceFetchResult) {
		cm.Delete(llmAutoscalingComponent + controllerutils.ComponentConditionSuffix)
	}
}

// fetchKServeConfig reads KServe's inferenceservice-config ConfigMap. It
// carries no AIM labels, so the operator's cache leaves it out.
func fetchKServeConfig(
	ctx context.Context,
	reader client.Reader,
	kserveNamespace string,
) controllerutils.FetchResult[*corev1.ConfigMap] {
	return controllerutils.FetchDirect(ctx, reader, client.ObjectKey{
		Namespace: kserveNamespace,
		Name:      kserveconstants.InferenceServiceConfigMapName,
	}, &corev1.ConfigMap{})
}

// llmInferenceServiceTLSEnabled reports KServe's enableLLMInferenceServiceTLS
// setting. A missing ConfigMap or ingress entry leaves TLS off, as in KServe.
func llmInferenceServiceTLSEnabled(fetch controllerutils.FetchResult[*corev1.ConfigMap]) (bool, error) {
	if fetch.IsNotFound() {
		return false, nil
	}
	if fetch.Error != nil {
		return false, fmt.Errorf("read KServe ConfigMap %s: %w", kserveconstants.InferenceServiceConfigMapName, fetch.Error)
	}
	if fetch.Value == nil {
		return false, nil
	}
	raw, ok := fetch.Value.Data[servingv1beta1.IngressConfigKeyName]
	if !ok {
		return false, nil
	}
	var ingress struct {
		EnableLLMInferenceServiceTLS bool `json:"enableLLMInferenceServiceTLS,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &ingress); err != nil {
		return false, fmt.Errorf("parse the %s entry of KServe ConfigMap %s: %w",
			servingv1beta1.IngressConfigKeyName, kserveconstants.InferenceServiceConfigMapName, err)
	}
	return ingress.EnableLLMInferenceServiceTLS, nil
}

// servingAPIComponentHealth reports whether the requested serving API can
// serve an LLMInferenceService-served service. It reports nothing for an
// InferenceService-served one; deleteInapplicableLLMConditions removes the
// condition.
func servingAPIComponentHealth(servingAPI aimv1alpha1.AIMServingAPI, err error) controllerutils.ComponentHealth {
	if servingAPI != aimv1alpha1.ServingAPILLMInferenceService {
		return controllerutils.ComponentHealth{}
	}
	health := controllerutils.ComponentHealth{
		Component:      servingAPIComponent,
		DependencyType: controllerutils.DependencyTypeUpstream,
	}
	if err == nil {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonServingAPISupported
		health.Message = "AIM Engine can serve the service through a KServe LLMInferenceService"
		return health
	}
	reason := aimv1alpha1.AIMServiceReasonServingAPIUnsupported
	var apiErr *servingAPIError
	if errors.As(err, &apiErr) {
		reason = apiErr.reason
	}
	health.State = constants.AIMStatusFailed
	health.Reason = reason
	health.Message = err.Error()
	if reason == reasonFetchError {
		health.Errors = []error{controllerutils.NewInfrastructureError(reason, err.Error(), err)}
	} else {
		health.Errors = []error{controllerutils.NewInvalidSpecError(reason, err.Error(), err)}
	}
	return health
}

// fetchLLMInferenceService fetches the service's LLMInferenceService, which
// shares the InferenceService name.
func fetchLLMInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha2.AIMService,
) controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService] {
	name, err := v1alpha1svc.GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Error: err}
	}
	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      name,
	}, &kservev1alpha2.LLMInferenceService{})
}

// llmWorkloadPodLabels selects the pods KServe runs for an LLMInferenceService's
// main workload.
func llmWorkloadPodLabels(llmServiceName string) client.MatchingLabels {
	return client.MatchingLabels{
		kserveconstants.KubernetesAppNameLabelKey:   llmServiceName,
		kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
		kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
	}
}

func fetchLLMWorkloadPods(
	ctx context.Context,
	c client.Client,
	llmService *kservev1alpha2.LLMInferenceService,
) controllerutils.FetchResult[*corev1.PodList] {
	return controllerutils.FetchList(ctx, c, &corev1.PodList{},
		client.InNamespace(llmService.Namespace),
		llmWorkloadPodLabels(llmService.Name),
	)
}

// llmInferenceServiceReady reports whether the LLMInferenceService has a
// Ready=True condition for its current generation.
func llmInferenceServiceReady(fetch controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]) bool {
	if !fetch.OK() || fetch.Value == nil {
		return false
	}
	llmService := fetch.Value
	if llmService.Status.ObservedGeneration != llmService.Generation {
		return false
	}
	ready := llmService.Status.GetCondition(apis.ConditionReady)
	return ready != nil && ready.IsTrue()
}

// llmInferenceServiceComponentHealth is the LLMInferenceService health
// verdict. A workload Deployment that cannot create a Pod makes the service
// Failed with the refusal text, as for the InferenceService predictor, unless
// a pod exists that can still become ready.
func llmInferenceServiceComponentHealth(
	fetch controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService],
	pods *controllerutils.FetchResult[*corev1.PodList],
	deployment controllerutils.FetchResult[*appsv1.Deployment],
) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      llmInferenceServiceComponent,
		DependencyType: controllerutils.DependencyTypeDownstream,
	}
	if !fetch.OK() && !fetch.IsNotFound() {
		health.State = constants.AIMStatusFailed
		health.Reason = reasonFetchError
		health.Message = fetch.Error.Error()
		health.Errors = []error{fetch.Error}
		return health
	}
	if fetch.Value == nil {
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
		health.Message = "LLMInferenceService not found"
		return health
	}
	if llmInferenceServiceReady(fetch) {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonRuntimeReady
		health.Message = "LLMInferenceService is ready"
		return health
	}

	if refusal, refused := v1alpha1svc.DeploymentPodCreationFailure(deployment.Value); refused {
		if starting := v1alpha1svc.ActivePodCount(pods); starting > 0 {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
			health.Message = fmt.Sprintf("LLMInferenceService has %d starting pod(s); "+
				"the workload Deployment cannot create a pod: %s", starting, refusal)
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonPodCreationFailed
		health.Message = "The workload Deployment cannot create a pod: " + refusal
		return health
	}

	health.State = constants.AIMStatusProgressing
	health.Reason = reasonLLMInferenceServiceNotReady
	health.Message = "LLMInferenceService is not ready yet"
	if ready := fetch.Value.Status.GetCondition(apis.ConditionReady); ready != nil && ready.Message != "" {
		health.Message = fmt.Sprintf("LLMInferenceService is not ready: %s: %s", ready.Reason, ready.Message)
	}
	return health
}

// llmWorkloadPodsComponentHealth classifies the LLMInferenceService workload
// pods with the shared pod inspector. Like the InferenceService predictor
// pods, they are owned downstream resources, so an unpullable image degrades
// readiness without blocking apply.
func llmWorkloadPodsComponentHealth(
	ctx context.Context,
	clientset kubernetes.Interface,
	service *aimv1alpha2.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	pods *controllerutils.FetchResult[*corev1.PodList],
) (controllerutils.ComponentHealth, bool) {
	if pods == nil {
		return controllerutils.ComponentHealth{}, false
	}
	// A rollout or scale-down deletes workload pods, and one whose container
	// exits non-zero on SIGTERM stays Failed until it is gone.
	pods = v1alpha1svc.WithoutDeletedFailedPods(pods)
	if pods.OK() && pods.Value != nil && len(pods.Value.Items) == 0 &&
		v1alpha1svc.ScaledToZero(service, hpa, 0) {
		return controllerutils.ComponentHealth{
			Component:      llmInferenceServicePodsComponent,
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.AIMServiceReasonScaledToZero,
			Message:        "Service is idle: KEDA has scaled the deployment to zero replicas; pods will be created on activity",
			DependencyType: controllerutils.DependencyTypeDownstream,
		}, true
	}
	return controllerutils.DemoteUpstreamDependencyErrors(pods.ToComponentHealthWithContext(
		ctx, clientset, llmInferenceServicePodsComponent, controllerutils.GetPodsHealth,
	)), true
}

// buildLLMInferenceServiceFromProfile constructs the KServe
// LLMInferenceService that serves the resolved profile. It references the
// profile's projected LLMInferenceServiceConfig (same name as its
// ServingRuntime) through spec.baseRefs and overlays only service-owned
// fields, mirroring buildInferenceServiceFromProfile: replicas, a resource
// override, the runtime config and service env, tolerations, security
// contexts, pull secrets, the service account, the priority class, a
// service-owned (Dedicated) cache, and additional CA trust. The workload pods
// carry the AIM labels of the LLMInferenceService.
func buildLLMInferenceServiceFromProfile(
	service *aimv1alpha2.AIMService,
	obs ServiceObservation,
) (*kservev1alpha2.LLMInferenceService, error) {
	profileSpec := obs.resolvedProfileSpec
	profileStatus := obs.resolvedProfileStatus
	if profileSpec == nil || profileStatus == nil || obs.isvcName == "" {
		return nil, nil
	}
	if profileStatus.Status != constants.AIMStatusReady && service.Spec.Resources == nil {
		return nil, nil
	}
	modelURI, err := serving.LLMModelURI(profileSpec)
	if err != nil {
		return nil, err
	}

	container := corev1.Container{Name: serving.LLMInferenceServiceContainerName}
	if service.Spec.Resources != nil {
		if obs.effectiveResources != nil {
			container.Resources = *obs.effectiveResources.DeepCopy()
		} else {
			container.Resources = resolveResourcesFromProfile(service, profileSpec, profileStatus)
		}
	}
	// The cache env is applied after the runtime config and service env, so it
	// wins, as on the InferenceService.
	cacheEnv, volumes, mounts := serviceOwnedCacheOverlay(service, obs)
	container.Env = upsertEnvVars(
		runtimeConfigEnv(service, obs.mergedRuntimeConfig.Value, profileSpec, obs.desiredProfileRuntime),
		cacheEnv,
	)
	container.VolumeMounts = mounts
	podSecurityContext, containerSecurityContext := servicepolicy.InferenceSecurityContexts(service, obs.mergedRuntimeConfig.Value)
	container.SecurityContext = llmContainerSecurityContext(podSecurityContext, containerSecurityContext)

	serviceAccountName := service.Spec.ServiceAccountName
	if serviceAccountName == "" {
		serviceAccountName = profileSpec.ServiceAccountName
	}
	template := &corev1.PodSpec{
		ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
		ServiceAccountName: serviceAccountName,
		PriorityClassName:  service.Spec.PriorityClassName,
		Tolerations:        servicepolicy.InferenceTolerations(service, obs.mergedRuntimeConfig.Value),
		SecurityContext:    podSecurityContext,
		Volumes:            volumes,
	}
	if projection := runtimeTrustProjection(obs.mergedRuntimeConfig.Value, obs.additionalCABundle.Value); projection != nil {
		template.InitContainers = append(template.InitContainers, projection.InitContainer)
		template.Volumes = append(template.Volumes, projection.Volumes...)
		container.VolumeMounts = append(container.VolumeMounts, projection.VolumeMounts...)
		container.Env = trust.UpsertEnv(container.Env, projection.Env)
	}
	template.Containers = []corev1.Container{container}

	// An autoscaled workload leaves replicas unset so KServe preserves the
	// Deployment replica count KEDA manages; the pods inject the per-service
	// OpenTelemetry collector sidecar that feeds the ScaledObject's triggers.
	replicas := ptr.To[int32](1)
	var podAnnotations map[string]string
	if v1alpha1svc.HasAutoscaling(service) {
		replicas = nil
		podAnnotations = map[string]string{
			constants.AnnotationOTelSidecarInject: llmDeploymentName(obs.isvcName),
		}
	} else if service.Spec.Replicas != nil {
		replicas = ptr.To(*service.Spec.Replicas)
	}

	labels := buildInferenceServiceLabels(service, obs, profileSpec)
	spec, err := serving.EscapeKServeTemplateActions(kservev1alpha2.LLMInferenceServiceSpec{
		Model: kservev1alpha2.LLMModelSpec{
			URI:  *modelURI,
			Name: ptr.To(serving.ResolvedServedModelID(profileSpec)),
		},
		StorageInitializer: &kservev1alpha2.StorageInitializerSpec{Enabled: ptr.To(false)},
		BaseRefs: append(
			[]corev1.LocalObjectReference{{Name: serving.RuntimeName(obs.profileName)}},
			userLLMBaseRefs(service)...,
		),
		WorkloadSpec: kservev1alpha2.WorkloadSpec{
			Replicas:    replicas,
			Labels:      llmWorkloadPodAIMLabels(labels),
			Annotations: podAnnotations,
			Template:    template,
		},
	})
	if err != nil {
		return nil, err
	}

	return &kservev1alpha2.LLMInferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha2.SchemeGroupVersion.String(),
			Kind:       "LLMInferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        obs.isvcName,
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: serving.EscapeKServeTemplateAnnotations(buildInferenceServiceAnnotations(service, obs, profileSpec)),
		},
		Spec: spec,
	}, nil
}

// llmContainerSecurityContext returns the security context AIM Engine sets on
// the main container. KServe merges it field by field over its base template
// and the profile's projected config, and the projected config sets
// runAsNonRoot: false for AIM images, which run as root by default. A merge
// can't unset that value, and a container value wins over the Pod value, so a
// runAsNonRoot that only the Pod security context sets is repeated on the
// container. Otherwise the Pod would run as root, and Pod Security restricted
// would refuse it.
func llmContainerSecurityContext(
	pod *corev1.PodSecurityContext,
	container *corev1.SecurityContext,
) *corev1.SecurityContext {
	if pod == nil || pod.RunAsNonRoot == nil || (container != nil && container.RunAsNonRoot != nil) {
		return container
	}
	if container == nil {
		container = &corev1.SecurityContext{}
	}
	container.RunAsNonRoot = ptr.To(*pod.RunAsNonRoot)
	return container
}

// llmWorkloadPodAIMLabels returns the AIM labels for the workload pods. KServe
// copies spec.labels over the pod labels it sets, which include its Deployment
// selector, so only labels in the AIM domain are passed on.
func llmWorkloadPodAIMLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range labels {
		if strings.HasPrefix(key, constants.AimLabelDomain+"/") {
			out[key] = value
		}
	}
	return out
}

// profileLLMConfigCurrent reports whether the namespaced LLMInferenceServiceConfig
// the service references reflects the current profile and cache state and
// belongs to the current backing profile incarnation. It compares the content
// hash the producer stamped from its desired state rather than re-hashing the
// observed spec, which API-server defaulting of the embedded pod template could
// change.
func (obs *ServiceObservation) profileLLMConfigCurrent() bool {
	desired := obs.desiredProfileLLMConfig
	if !obs.profileLLMConfig.OK() || obs.profileLLMConfig.Value == nil || desired == nil {
		return false
	}
	expectedHash := desired.GetAnnotations()[constants.AnnotationRuntimeProjectionContentHash]
	return expectedHash != "" &&
		obs.profileLLMConfig.Value.GetAnnotations()[constants.AnnotationRuntimeProjectionContentHash] == expectedHash &&
		controllerOwnerUIDMatches(obs.profileLLMConfig.Value, desired)
}

// planLLMInferenceService plans the service's LLMInferenceService. LoRA
// adapters use AIM Engine's adapter contract, exactly as for an
// InferenceService: the service's adapter subtree is mounted read-only into
// the main container, and an existing workload keeps its current mount while
// the adapter disk binding is not yet mountable.
func planLLMInferenceService(
	planResult *controllerutils.PlanResult,
	service *aimv1alpha2.AIMService,
	obs ServiceObservation,
	llmServiceExists bool,
	logger logr.Logger,
) {
	if obs.llmWorkloadDeployment.OK() && obs.llmWorkloadDeployment.Value != nil {
		// The workload Deployment is not cached, and KServe does not surface
		// its ReplicaFailure condition, so a refused pod triggers no reconcile.
		planResult.RequestRequeueAfter(llmWorkloadDeploymentPollInterval)
	}
	if !llmBaseRefsUsable(obs.llmBaseRefs) {
		logger.V(1).Info("spec.llmInferenceService.baseRefs not usable; skipping LLMInferenceService")
		return
	}
	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil {
		logger.Error(err, "failed to build LLMInferenceService", "profile", obs.profileName)
		return
	}
	if llmService == nil {
		return
	}
	if service.Spec.AdaptersEnabled() {
		if aimadapter.PreserveExistingMount(service, obs.adapterState) && llmServiceExists {
			logger.V(1).Info("Adapter disk binding not mountable yet; preserving running LLMInferenceService adapter wiring")
			planResult.RequestRequeueAfter(5 * time.Second)
			return
		}
		template := llmService.Spec.Template
		aimadapter.AddToPod(
			&template.Volumes,
			&template.Containers[0],
			service,
			obs.adapterState.MountedAdapterDiskPVC(),
			obs.adapterState.MaxRank,
		)
	}
	planResult.Apply(llmService)
}

// planServiceHTTPRoute plans the AIMService HTTPRoute against the workload
// that serves it. AIM Engine owns routing for LLMInferenceServices too (no
// spec.router), so the route targets the workload Service KServe creates for
// the main deployment.
func planServiceHTTPRoute(ctx context.Context, service *aimv1alpha2.AIMService, obs ServiceObservation) client.Object {
	if !obs.servesLLM {
		return v1alpha1svc.PlanHTTPRoute(ctx, service, obs.mergedRuntimeConfig.Value, obs.gateway.Value)
	}
	return v1alpha1svc.PlanHTTPRouteToBackend(
		ctx, service, obs.mergedRuntimeConfig.Value, obs.gateway.Value,
		v1alpha1svc.RouteBackend{
			ServiceName: llmWorkloadServiceName(obs.isvcName),
			Port:        constants.DefaultHTTPPort,
		},
	)
}

// llmWorkloadServiceName is the Service KServe creates in front of an
// LLMInferenceService's main workload pods.
func llmWorkloadServiceName(llmServiceName string) string {
	return kmeta.ChildName(llmServiceName, "-kserve-workload-svc")
}
