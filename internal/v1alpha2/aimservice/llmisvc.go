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
	"time"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/kmeta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimadapter"
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
	reasonServingAPIUnsupported       = "ServingAPIUnsupported"
)

// resolveServingAPI returns the KServe API that serves the service. A recorded
// status.servingAPI is authoritative so an existing service never moves
// between kinds; otherwise Auto resolves to InferenceService.
func resolveServingAPI(service *aimv1alpha1.AIMService) aimv1alpha1.AIMServingAPI {
	if service.Status.ServingAPI != "" {
		return service.Status.ServingAPI
	}
	if service.Spec.GetServingAPI() == aimv1alpha1.ServingAPILLMInferenceService {
		return aimv1alpha1.ServingAPILLMInferenceService
	}
	return aimv1alpha1.ServingAPIInferenceService
}

// validateServingAPI rejects an LLMInferenceService-served service when the
// operator runs without the KServe LLMInferenceService API.
func validateServingAPI(
	_ *aimv1alpha1.AIMService,
	servingAPI aimv1alpha1.AIMServingAPI,
	support serving.LLMInferenceServiceSupport,
) error {
	if servingAPI != aimv1alpha1.ServingAPILLMInferenceService {
		return nil
	}
	if !support.Enabled {
		return fmt.Errorf("spec.servingAPI is LLMInferenceService, but AIM Engine runs without KServe " +
			"LLMInferenceService support: install the serving.kserve.io/v1alpha2 LLMInferenceService CRDs " +
			"and restart the operator with --enable-llm-inference-service")
	}
	return nil
}

func servingAPIComponentHealth(err error) controllerutils.ComponentHealth {
	if err == nil {
		return controllerutils.ComponentHealth{}
	}
	return controllerutils.ComponentHealth{
		Component:      servingAPIComponent,
		State:          constants.AIMStatusFailed,
		DependencyType: controllerutils.DependencyTypeUpstream,
		Errors: []error{
			controllerutils.NewInvalidSpecError(reasonServingAPIUnsupported, err.Error(), err),
		},
	}
}

// fetchLLMInferenceService fetches the service's LLMInferenceService, which
// shares the InferenceService name.
func fetchLLMInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
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

func llmInferenceServiceComponentHealth(
	fetch controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService],
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
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	pods *controllerutils.FetchResult[*corev1.PodList],
) (controllerutils.ComponentHealth, bool) {
	if pods == nil {
		return controllerutils.ComponentHealth{}, false
	}
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
// fields on the main container, mirroring buildInferenceServiceFromProfile:
// replicas, a resource override, pull secrets, the service account, the
// priority class, a service-owned (Dedicated) cache, and additional CA trust.
func buildLLMInferenceServiceFromProfile(
	service *aimv1alpha1.AIMService,
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
	env, volumes, mounts := serviceOwnedCacheOverlay(service, obs)
	container.Env = env
	container.VolumeMounts = mounts

	serviceAccountName := service.Spec.ServiceAccountName
	if serviceAccountName == "" {
		serviceAccountName = profileSpec.ServiceAccountName
	}
	template := &corev1.PodSpec{
		ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
		ServiceAccountName: serviceAccountName,
		PriorityClassName:  service.Spec.PriorityClassName,
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

	return &kservev1alpha2.LLMInferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha2.SchemeGroupVersion.String(),
			Kind:       "LLMInferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        obs.isvcName,
			Namespace:   service.Namespace,
			Labels:      buildInferenceServiceLabels(service, obs, profileSpec),
			Annotations: buildInferenceServiceAnnotations(service, obs, profileSpec),
		},
		Spec: kservev1alpha2.LLMInferenceServiceSpec{
			Model: kservev1alpha2.LLMModelSpec{
				URI:  *modelURI,
				Name: ptr.To(resolvedModelId(profileSpec)),
			},
			StorageInitializer: &kservev1alpha2.StorageInitializerSpec{Enabled: ptr.To(false)},
			BaseRefs: append(
				[]corev1.LocalObjectReference{{Name: serving.RuntimeName(obs.profileName)}},
				userLLMBaseRefs(service)...,
			),
			WorkloadSpec: kservev1alpha2.WorkloadSpec{
				Replicas:    replicas,
				Annotations: podAnnotations,
				Template:    template,
			},
		},
	}, nil
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
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
	llmServiceExists bool,
	logger logr.Logger,
) {
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
			obs.adapterState.AdapterDiskPVC,
			obs.adapterState.MaxRank,
		)
	}
	planResult.Apply(llmService)
}

// planServiceHTTPRoute plans the AIMService HTTPRoute against the workload
// that serves it. AIM Engine owns routing for LLMInferenceServices too (no
// spec.router), so the route targets the workload Service KServe creates for
// the main deployment.
func planServiceHTTPRoute(ctx context.Context, service *aimv1alpha1.AIMService, obs ServiceObservation) client.Object {
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
