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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/controller/v1alpha2/llmisvc"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const scaledObjectKind = "ScaledObject"

var testLLMSupport = serving.LLMInferenceServiceSupport{
	Enabled:                true,
	KServeNamespace:        "kserve-system",
	OpenTelemetryCollector: true,
}

func llmTestService() *aimv1alpha2.AIMService {
	return &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:    &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
			ServingAPI: aimv1alpha1.ServingAPILLMInferenceService,
			Replicas:   ptr.To[int32](2),
		},
	}
}

func llmTestClusterProfile() *aimv1alpha2.AIMClusterProfile {
	return &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterProfileName, UID: "profile-uid"},
		Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: *profileSpecWithModelSources()},
		Status:     aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
}

func notFoundError(resource, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "test", Resource: resource}, name)
}

// llmTestFetch is a fetch result for an LLMInferenceService-served service
// whose namespaced projection and LLMInferenceService do not exist yet.
func llmTestFetch(service *aimv1alpha2.AIMService, profile *aimv1alpha2.AIMClusterProfile) ServiceFetchResult {
	runtimeName := serving.RuntimeName(profile.Name)
	cache := readyCacheFixture()
	return ServiceFetchResult{
		service:             service,
		servingAPI:          aimv1alpha1.ServingAPILLMInferenceService,
		servesLLM:           true,
		clusterProfile:      controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: profile},
		profileCache:        controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: cache},
		runtimeProfileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: cache},
		llmInferenceService: controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{
			Error: notFoundError("llminferenceservices", testServiceName),
		},
		profileRuntime: controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{
			Error: notFoundError("servingruntimes", runtimeName),
		},
		profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{
			Error: notFoundError("configmaps", runtimeName),
		},
		profileLLMConfig: controllerutils.FetchResult[*kservev1alpha2.LLMInferenceServiceConfig]{
			Error: notFoundError("llminferenceserviceconfigs", runtimeName),
		},
	}
}

func appliedLLMInferenceService(objects []client.Object) *kservev1alpha2.LLMInferenceService {
	for _, obj := range objects {
		if llmService, ok := obj.(*kservev1alpha2.LLMInferenceService); ok {
			return llmService
		}
	}
	return nil
}

func TestResolveServingAPI(t *testing.T) {
	for _, tc := range []struct {
		name     string
		spec     aimv1alpha1.AIMServingAPI
		recorded aimv1alpha1.AIMServingAPI
		want     aimv1alpha1.AIMServingAPI
	}{
		{"unset resolves to InferenceService", "", "", aimv1alpha1.ServingAPIInferenceService},
		{"explicit LLMInferenceService", aimv1alpha1.ServingAPILLMInferenceService, "", aimv1alpha1.ServingAPILLMInferenceService},
		{"a lost status does not change the API", aimv1alpha1.ServingAPILLMInferenceService, "", aimv1alpha1.ServingAPILLMInferenceService},
		{"the spec wins over a stale status", aimv1alpha1.ServingAPIInferenceService, aimv1alpha1.ServingAPILLMInferenceService, aimv1alpha1.ServingAPIInferenceService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &aimv1alpha2.AIMService{
				Spec:   aimv1alpha1.AIMServiceSpec{ServingAPI: tc.spec},
				Status: aimv1alpha1.AIMServiceStatus{ServingAPI: tc.recorded},
			}
			if got := resolveServingAPI(service); got != tc.want {
				t.Fatalf("resolveServingAPI() = %q, want %q", got, tc.want)
			}
		})
	}
}

func kserveConfigFetch(ingress string) controllerutils.FetchResult[*corev1.ConfigMap] {
	return controllerutils.FetchResult[*corev1.ConfigMap]{Value: &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "inferenceservice-config", Namespace: "kserve-system"},
		Data:       map[string]string{"ingress": ingress},
	}}
}

func TestValidateServingAPI(t *testing.T) {
	notFound := controllerutils.FetchResult[*corev1.ConfigMap]{
		Error: apierrors.NewNotFound(corev1.Resource("configmaps"), "inferenceservice-config"),
	}
	forbidden := controllerutils.FetchResult[*corev1.ConfigMap]{
		Error: apierrors.NewForbidden(corev1.Resource("configmaps"), "inferenceservice-config", errors.New("denied")),
	}
	for _, tc := range []struct {
		name         string
		servingAPI   aimv1alpha1.AIMServingAPI
		support      serving.LLMInferenceServiceSupport
		kserveConfig controllerutils.FetchResult[*corev1.ConfigMap]
		wantReason   string
		wantErr      string
	}{
		{
			"InferenceService needs no support", aimv1alpha1.ServingAPIInferenceService,
			serving.LLMInferenceServiceSupport{},
			kserveConfigFetch(`{"enableLLMInferenceServiceTLS": true}`), "", "",
		},
		{"supported LLMInferenceService", aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport, notFound, "", ""},
		{
			"TLS off", aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport,
			kserveConfigFetch(`{"enableLLMInferenceServiceTLS": false}`), "", "",
		},
		{
			"missing CRDs", aimv1alpha1.ServingAPILLMInferenceService,
			serving.LLMInferenceServiceSupport{},
			notFound,
			aimv1alpha1.AIMServiceReasonServingAPIUnsupported, "LLMInferenceService support",
		},
		{
			"TLS on", aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport,
			kserveConfigFetch(`{"enableLLMInferenceServiceTLS": true}`),
			aimv1alpha1.AIMServiceReasonLLMInferenceServiceTLSEnabled, "kserve-system/inferenceservice-config",
		},
		{
			"unparsable ingress config", aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport,
			kserveConfigFetch(`{`), reasonFetchError, "parse the ingress entry",
		},
		{
			"unreadable ConfigMap", aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport, forbidden,
			reasonFetchError, "read KServe ConfigMap",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServingAPI(tc.servingAPI, tc.support, tc.kserveConfig)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.wantErr)
			}
			if health := servingAPIComponentHealth(tc.servingAPI, err); health.Reason != tc.wantReason {
				t.Errorf("health reason = %q, want %q", health.Reason, tc.wantReason)
			}
		})
	}
}

// staleFailedCondition seeds a condition manager with the False condition a
// component left on an earlier reconcile.
func staleFailedCondition(component string) *controllerutils.ConditionManager {
	return controllerutils.NewConditionManager([]metav1.Condition{{
		Type:   component + controllerutils.ComponentConditionSuffix,
		Status: metav1.ConditionFalse,
		Reason: "EarlierFailure",
	}})
}

// assertConditionClears checks that a fixed component no longer leaves its
// earlier False condition: it either reports Ready, which the pipeline writes
// over the condition, or no longer applies, and DecorateStatus deletes it.
func assertConditionClears(
	t *testing.T,
	component string,
	health controllerutils.ComponentHealth,
	obs ServiceObservation,
	wantReady bool,
) {
	t.Helper()
	cm := staleFailedCondition(component)
	deleteInapplicableLLMConditions(cm, obs)
	conditionType := component + controllerutils.ComponentConditionSuffix
	if wantReady {
		if health.Component != component || health.State != constants.AIMStatusReady {
			t.Fatalf("health = %+v, want %s Ready", health, component)
		}
		if cm.Get(conditionType) == nil {
			t.Errorf("%s was deleted although the check still applies", conditionType)
		}
		return
	}
	if health.Component != "" {
		t.Fatalf("health = %+v, want none once the check no longer applies", health)
	}
	if cm.Get(conditionType) != nil {
		t.Errorf("%s stays after the check stopped applying", conditionType)
	}
}

func TestServingAPICondition_failThenFixed(t *testing.T) {
	llm := aimv1alpha1.ServingAPILLMInferenceService
	tlsOn := kserveConfigFetch(`{"enableLLMInferenceServiceTLS": true}`)
	tlsOff := kserveConfigFetch(`{"enableLLMInferenceServiceTLS": false}`)

	t.Run("support installed", func(t *testing.T) {
		failed := servingAPIComponentHealth(llm, validateServingAPI(llm, serving.LLMInferenceServiceSupport{}, tlsOff))
		if failed.State != constants.AIMStatusFailed {
			t.Fatalf("health = %+v, want Failed", failed)
		}
		fixed := servingAPIComponentHealth(llm, validateServingAPI(llm, testLLMSupport, tlsOff))
		obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{service: llmTestService(), servingAPI: llm}}
		assertConditionClears(t, servingAPIComponent, fixed, obs, true)
	})

	t.Run("TLS turned off", func(t *testing.T) {
		failed := servingAPIComponentHealth(llm, validateServingAPI(llm, testLLMSupport, tlsOn))
		if failed.Reason != aimv1alpha1.AIMServiceReasonLLMInferenceServiceTLSEnabled {
			t.Fatalf("health = %+v, want Failed/%s", failed, aimv1alpha1.AIMServiceReasonLLMInferenceServiceTLSEnabled)
		}
		fixed := servingAPIComponentHealth(llm, validateServingAPI(llm, testLLMSupport, tlsOff))
		if fixed.Reason != aimv1alpha1.AIMServiceReasonServingAPISupported {
			t.Errorf("health reason = %q, want %q", fixed.Reason, aimv1alpha1.AIMServiceReasonServingAPISupported)
		}
		obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{service: llmTestService(), servingAPI: llm}}
		assertConditionClears(t, servingAPIComponent, fixed, obs, true)
	})

	t.Run("InferenceService-served", func(t *testing.T) {
		isvc := aimv1alpha1.ServingAPIInferenceService
		health := servingAPIComponentHealth(isvc, validateServingAPI(isvc, serving.LLMInferenceServiceSupport{}, tlsOn))
		obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{service: llmTestService(), servingAPI: isvc}}
		assertConditionClears(t, servingAPIComponent, health, obs, false)
	})
}

func TestLLMBaseRefsCondition_failThenFixed(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	obs.llmBaseRefs = []llmBaseRef{{name: "team-args"}}
	if failed := llmBaseRefsComponentHealth(obs.llmBaseRefs, "kserve-system", "ns"); failed.State != constants.AIMStatusFailed {
		t.Fatalf("health = %+v, want Failed for a missing ref", failed)
	}

	t.Run("ref created", func(t *testing.T) {
		fixed := obs
		fixed.llmBaseRefs = []llmBaseRef{{name: "team-args", config: llmPreset("team-args", "ns", nil)}}
		assertConditionClears(t, llmBaseRefsComponent, llmBaseRefsComponentHealth(fixed.llmBaseRefs, "kserve-system", "ns"), fixed, true)
	})

	t.Run("ref removed", func(t *testing.T) {
		fixed := obs
		fixed.llmBaseRefs = nil
		assertConditionClears(t, llmBaseRefsComponent, llmBaseRefsComponentHealth(fixed.llmBaseRefs, "kserve-system", "ns"), fixed, false)
	})
}

func TestLLMAutoscalingCondition_failThenFixed(t *testing.T) {
	service, obs := autoscaledLLMObservation(t)
	obs.openTelemetryCollector = false
	if failed := llmAutoscalingComponentHealth(service, obs.ServiceFetchResult); failed.State != constants.AIMStatusFailed {
		t.Fatalf("health = %+v, want Failed without the collector API", failed)
	}

	t.Run("collector installed", func(t *testing.T) {
		fixed := obs
		fixed.openTelemetryCollector = true
		health := llmAutoscalingComponentHealth(service, fixed.ServiceFetchResult)
		if health.Reason != aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorAvailable {
			t.Errorf("health reason = %q, want %q", health.Reason, aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorAvailable)
		}
		assertConditionClears(t, llmAutoscalingComponent, health, fixed, true)
	})

	t.Run("autoscaling removed", func(t *testing.T) {
		fixed := obs
		fixed.service = llmTestService()
		health := llmAutoscalingComponentHealth(fixed.service, fixed.ServiceFetchResult)
		assertConditionClears(t, llmAutoscalingComponent, health, fixed, false)
	})
}

func TestPlanResources_llmInferenceServiceWaitsForNamespacedProjection(t *testing.T) {
	service := llmTestService()
	profile := llmTestClusterProfile()
	reconciler := &ProfileServiceReconciler{LLMInferenceService: testLLMSupport}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha2.AIMService]{Object: service}
	runtimeName := serving.RuntimeName(profile.Name)

	first := reconciler.ComposeState(context.Background(), reconcileCtx, llmTestFetch(service, profile))
	if first.configErr != nil || first.servingAPIErr != nil {
		t.Fatalf("ComposeState errors: config=%v servingAPI=%v", first.configErr, first.servingAPIErr)
	}
	firstPlan := reconciler.PlanResources(context.Background(), reconcileCtx, first)
	if appliedLLMInferenceService(firstPlan.GetToApply()) != nil {
		t.Fatal("the LLMInferenceService must wait for the namespaced LLMInferenceServiceConfig")
	}
	var config *kservev1alpha2.LLMInferenceServiceConfig
	var runtime *kservev1alpha1.ServingRuntime
	var configMap *corev1.ConfigMap
	for _, obj := range firstPlan.GetToApplyWithoutOwnerRef() {
		switch typed := obj.(type) {
		case *kservev1alpha2.LLMInferenceServiceConfig:
			config = typed
		case *kservev1alpha1.ServingRuntime:
			runtime = typed
		case *corev1.ConfigMap:
			configMap = typed
		}
	}
	if config == nil || runtime == nil || configMap == nil {
		t.Fatalf("first pass must materialize the config, ServingRuntime, and ConfigMap; got config=%v sr=%v cm=%v",
			config != nil, runtime != nil, configMap != nil)
	}
	if config.Name != runtimeName || config.Namespace != service.Namespace {
		t.Errorf("config %s/%s, want %s/%s", config.Namespace, config.Name, service.Namespace, runtimeName)
	}
	if owner := metav1.GetControllerOf(config); owner == nil || owner.UID != profile.UID {
		t.Errorf("config owner = %#v, want the backing profile", owner)
	}

	fetch := llmTestFetch(service, profile)
	fetch.profileRuntime = controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: runtime}
	fetch.profileRuntimeConfigMap = controllerutils.FetchResult[*corev1.ConfigMap]{Value: configMap}
	fetch.profileLLMConfig = controllerutils.FetchResult[*kservev1alpha2.LLMInferenceServiceConfig]{Value: config}
	second := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)
	if health := second.getProfileRuntimeHealth(); health.State != constants.AIMStatusReady {
		t.Fatalf("projection health = %q/%q, want Ready", health.State, health.Reason)
	}
	secondPlan := reconciler.PlanResources(context.Background(), reconcileCtx, second)
	if planAppliesType[*servingv1beta1.InferenceService](secondPlan.GetToApply()) {
		t.Fatal("an LLMInferenceService-served service must not create an InferenceService")
	}
	assertLLMInferenceServiceOverlay(t, appliedLLMInferenceService(secondPlan.GetToApply()), service, runtimeName)
}

func assertLLMInferenceServiceOverlay(
	t *testing.T,
	llmService *kservev1alpha2.LLMInferenceService,
	service *aimv1alpha2.AIMService,
	configName string,
) {
	t.Helper()
	if llmService == nil {
		t.Fatal("the LLMInferenceService was not planned once the projection is current")
	}
	if len(llmService.Spec.BaseRefs) != 1 || llmService.Spec.BaseRefs[0].Name != configName {
		t.Errorf("baseRefs = %#v, want the projected config %q", llmService.Spec.BaseRefs, configName)
	}
	if llmService.Spec.Replicas == nil || *llmService.Spec.Replicas != *service.Spec.Replicas {
		t.Errorf("replicas = %v, want %d", llmService.Spec.Replicas, *service.Spec.Replicas)
	}
	if llmService.Spec.StorageInitializer == nil || ptr.Deref(llmService.Spec.StorageInitializer.Enabled, true) {
		t.Error("the storage initializer must stay disabled")
	}
	if got := llmService.Spec.Model.URI.String(); got != "hf://org/model" {
		t.Errorf("model.uri = %q, want the profile's model source", got)
	}
	if llmService.Annotations[constants.AnnotationRuntimeProfile] != testClusterProfileName {
		t.Errorf("runtime-profile annotation = %q, want the backing profile", llmService.Annotations[constants.AnnotationRuntimeProfile])
	}
	template := llmService.Spec.Template
	if template == nil || len(template.Containers) != 1 || template.Containers[0].Name != serving.LLMInferenceServiceContainerName {
		t.Fatalf("template = %#v, want a single main container overlay", template)
	}
	if template.Containers[0].Image != "" {
		t.Error("the overlay must not set an image; the projected config owns the pod shape")
	}
}

func TestBuildLLMInferenceServiceFromProfile_serviceOverlays(t *testing.T) {
	service := llmTestService()
	service.Spec.Caching = &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeDedicated}
	service.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")},
	}
	service.Spec.ServiceAccountName = "inference"
	service.Spec.PriorityClassName = "high"
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: readyCacheFixture()},
		},
		resolvedProfileSpec:   profileSpecWithModelSources(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		profileCacheReady:     true,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	template := llmService.Spec.Template
	container := template.Containers[0]
	if !volumeRefsPVC(template.Volumes, "weights-pvc") || len(container.VolumeMounts) == 0 {
		t.Errorf("the Dedicated cache PVC must be mounted into main; volumes=%+v mounts=%+v", template.Volumes, container.VolumeMounts)
	}
	if envMap(container.Env)[constants.EnvAIMModelID] == "" {
		t.Errorf("the Dedicated cache overlay must carry the %s redirect env", constants.EnvAIMModelID)
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.Cmp(resource.MustParse("64Gi")) != 0 {
		t.Errorf("memory request = %s, want the service override 64Gi", got.String())
	}
	if template.ServiceAccountName != "inference" || template.PriorityClassName != "high" {
		t.Errorf("serviceAccount/priorityClass = %q/%q", template.ServiceAccountName, template.PriorityClassName)
	}
}

func llmRuntimeConfigObservation(t *testing.T) (*aimv1alpha2.AIMService, ServiceObservation) {
	t.Helper()
	service := llmTestService()
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	servingRuntime.Spec.Containers = []corev1.Container{{
		Name: constants.ContainerKServe,
		Env:  []corev1.EnvVar{{Name: "PROFILE_VAR", Value: "profile"}},
	}}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service, servesLLM: true},
		resolvedProfileSpec:   profileSpecWithModelSources(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		desiredProfileRuntime: servingRuntime,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)
	return service, obs
}

func TestBuildLLMInferenceServiceFromProfile_runtimeConfigPolicies(t *testing.T) {
	gpuTaint := corev1.Toleration{Key: "amd.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	service, obs := llmRuntimeConfigObservation(t)

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	template := llmService.Spec.Template
	if template.Tolerations != nil || template.SecurityContext != nil || template.Containers[0].SecurityContext != nil {
		t.Fatalf("without configuration the overlay must set no tolerations or security contexts; got %v, %v, %v",
			template.Tolerations, template.SecurityContext, template.Containers[0].SecurityContext)
	}

	runtimeConfig := &aimv1alpha1.AIMRuntimeConfigCommon{}
	runtimeConfig.Tolerations = []corev1.Toleration{gpuTaint}
	runtimeConfig.PodSecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To[int64](1000)}
	runtimeConfig.SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false)}
	runtimeConfig.Env = []corev1.EnvVar{
		{Name: "HOME", Value: "/tmp"},
		{Name: "PROFILE_VAR", Value: "config"},
	}
	obs.mergedRuntimeConfig = controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: runtimeConfig}
	service.Spec.Env = []corev1.EnvVar{{Name: "CHAT_TEMPLATE", Value: "{{ messages }}"}}

	llmService, err = buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() error = %v", err)
	}
	template = llmService.Spec.Template
	container := template.Containers[0]
	if len(template.Tolerations) != 1 || template.Tolerations[0] != gpuTaint {
		t.Errorf("tolerations = %v, want the runtime config's [%v]", template.Tolerations, gpuTaint)
	}
	if !equalPtr(template.SecurityContext.RunAsUser, ptr.To[int64](1000)) {
		t.Errorf("pod security context = %+v, want the runtime config's", template.SecurityContext)
	}
	if !equalPtr(container.SecurityContext.AllowPrivilegeEscalation, ptr.To(false)) {
		t.Errorf("container security context = %+v, want the runtime config's", container.SecurityContext)
	}
	if !equalPtr(container.SecurityContext.RunAsNonRoot, ptr.To(true)) {
		t.Error("the pod runAsNonRoot must be repeated on main, over the projected config's runAsNonRoot: false")
	}

	env := envMap(container.Env)
	if env["HOME"] != "/tmp" {
		t.Errorf("HOME = %q, want the runtime config value", env["HOME"])
	}
	if _, set := env["PROFILE_VAR"]; set {
		t.Error("a runtime config entry the profile sets must leave the profile value in place")
	}
	if got := env["CHAT_TEMPLATE"]; got != "{{`{{`}} messages }}" {
		t.Errorf("CHAT_TEMPLATE = %q, want its \"{{\" escaped for KServe's template rendering", got)
	}
}

func equalPtr[T comparable](got, want *T) bool {
	return got != nil && want != nil && *got == *want
}

func TestLLMContainerSecurityContext(t *testing.T) {
	nonRootPod := &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true)}
	for _, tc := range []struct {
		name      string
		pod       *corev1.PodSecurityContext
		container *corev1.SecurityContext
		want      *corev1.SecurityContext
	}{
		{"neither set", nil, nil, nil},
		{"pod without runAsNonRoot", &corev1.PodSecurityContext{RunAsUser: ptr.To[int64](1000)}, nil, nil},
		{"pod runAsNonRoot is repeated", nonRootPod, nil, &corev1.SecurityContext{RunAsNonRoot: ptr.To(true)}},
		{
			"pod runAsNonRoot joins the container value",
			nonRootPod,
			&corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false)},
			&corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false), RunAsNonRoot: ptr.To(true)},
		},
		{
			"container runAsNonRoot wins",
			nonRootPod,
			&corev1.SecurityContext{RunAsNonRoot: ptr.To(false)},
			&corev1.SecurityContext{RunAsNonRoot: ptr.To(false)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := llmContainerSecurityContext(tc.pod, tc.container); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("llmContainerSecurityContext() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuildLLMInferenceServiceFromProfile_workloadPodsCarryAIMLabels(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	podLabels := llmService.Spec.Labels
	if podLabels[constants.LabelProfile] != llmService.Labels[constants.LabelProfile] || podLabels[constants.LabelProfile] == "" {
		t.Errorf("pod profile label = %q, want the LLMInferenceService's %q",
			podLabels[constants.LabelProfile], llmService.Labels[constants.LabelProfile])
	}
	if podLabels[constants.LabelService] == "" {
		t.Error("the pods must carry the service label")
	}
	for key := range podLabels {
		if !strings.HasPrefix(key, constants.AimLabelDomain+"/") {
			t.Errorf("pod label %q is outside the AIM domain; KServe would copy it over its own pod labels", key)
		}
	}
}

func TestBuildLLMInferenceServiceFromProfile_workloadPodsCarryPropagatedLabels(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Labels = map[string]string{
		"team.example.com/owner":      "llm-serving",
		"app.kubernetes.io/name":      "chat",
		"app.kubernetes.io/version":   "v2",
		"app.kubernetes.io/component": "frontend",
		"unmatched":                   "kept-off",
	}
	runtimeConfig := &aimv1alpha1.AIMRuntimeConfigCommon{}
	runtimeConfig.LabelPropagation = &aimv1alpha1.AIMRuntimeConfigLabelPropagationSpec{
		Enabled: true,
		Match:   []string{"team.example.com/*", "app.kubernetes.io/*"},
	}
	obs.mergedRuntimeConfig = controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: runtimeConfig}

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	podLabels := llmService.Spec.Labels
	if podLabels["team.example.com/owner"] != "llm-serving" || podLabels["app.kubernetes.io/version"] != "v2" {
		t.Errorf("pod labels = %v, want the labels the runtime config propagates", podLabels)
	}
	if podLabels[constants.LabelProfile] == "" {
		t.Errorf("pod labels = %v, want the AIM labels kept", podLabels)
	}
	for key := range llmWorkloadPodLabels(llmService.Name) {
		if _, set := podLabels[key]; set {
			t.Errorf("pod label %q is set; KServe would copy it over its Deployment selector", key)
		}
	}
	if _, set := podLabels["unmatched"]; set {
		t.Error("a label the runtime config does not match must not reach the pods")
	}
}

func TestLLMInferenceServiceComponentHealth(t *testing.T) {
	readyService := func(status corev1.ConditionStatus, observedGeneration int64) *kservev1alpha2.LLMInferenceService {
		llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: "svc", Generation: 2}}
		llmService.Status.Status = duckv1.Status{
			ObservedGeneration: observedGeneration,
			Conditions: duckv1.Conditions{{
				Type: apis.ConditionReady, Status: status, Reason: "MinimumReplicasUnavailable", Message: "0/1 ready",
			}},
		}
		return llmService
	}

	for _, tc := range []struct {
		name      string
		fetch     controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]
		wantState constants.AIMStatus
	}{
		{"not found", controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Error: notFoundError("llminferenceservices", "svc")}, constants.AIMStatusProgressing},
		{"ready", controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: readyService(corev1.ConditionTrue, 2)}, constants.AIMStatusReady},
		{"ready for an older generation", controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: readyService(corev1.ConditionTrue, 1)}, constants.AIMStatusProgressing},
		{"not ready", controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: readyService(corev1.ConditionFalse, 2)}, constants.AIMStatusProgressing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := llmInferenceServiceComponentHealth(tc.fetch, nil, controllerutils.FetchResult[*appsv1.Deployment]{}); got.State != tc.wantState {
				t.Fatalf("state = %q (%s), want %q", got.State, got.Message, tc.wantState)
			}
		})
	}
}

func TestLLMInferenceServiceComponentHealth_rollout(t *testing.T) {
	llmService := func(ready corev1.ConditionStatus, observedGeneration int64) *kservev1alpha2.LLMInferenceService {
		svc := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: "svc", Generation: 2}}
		svc.Status.Status = duckv1.Status{
			ObservedGeneration: observedGeneration,
			Conditions:         duckv1.Conditions{{Type: apis.ConditionReady, Status: ready}},
		}
		return svc
	}
	readyPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve-old"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	unschedulablePod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve-new"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  corev1.PodReasonUnschedulable,
				Message: "0/3 nodes are available: 3 Insufficient memory.",
			}},
		},
	}
	crashLoopingPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve-new"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "main",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CrashLoopBackOff",
					Message: "back-off 40s restarting failed container=main",
				}},
			}},
		},
	}
	refused := appsv1.DeploymentCondition{
		Type:    appsv1.DeploymentReplicaFailure,
		Status:  corev1.ConditionTrue,
		Reason:  "FailedCreate",
		Message: `pods "svc-kserve-new" is forbidden: exceeded quota: compute`,
	}
	deadlineExceeded := appsv1.DeploymentCondition{
		Type:    appsv1.DeploymentProgressing,
		Status:  corev1.ConditionFalse,
		Reason:  "ProgressDeadlineExceeded",
		Message: `ReplicaSet "svc-kserve-new" has timed out progressing.`,
	}
	deployment := func(replicas, updated int32, conditions ...appsv1.DeploymentCondition) controllerutils.FetchResult[*appsv1.Deployment] {
		return controllerutils.FetchResult[*appsv1.Deployment]{Value: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve"},
			Status:     appsv1.DeploymentStatus{Replicas: replicas, UpdatedReplicas: updated, ReadyReplicas: 1, Conditions: conditions},
		}}
	}

	for _, tc := range []struct {
		name            string
		llmService      *kservev1alpha2.LLMInferenceService
		pods            []corev1.Pod
		deployment      controllerutils.FetchResult[*appsv1.Deployment]
		wantState       constants.AIMStatus
		wantReason      string
		wantServing     bool
		wantUserFixable bool
		wantMessage     string
	}{
		{
			name:        "new pod crash-loops while KServe reports the LLMInferenceService ready",
			llmService:  llmService(corev1.ConditionTrue, 2),
			pods:        []corev1.Pod{readyPod, crashLoopingPod},
			deployment:  deployment(2, 1),
			wantState:   constants.AIMStatusDegraded,
			wantReason:  aimv1alpha1.AIMServiceReasonRolloutStalled,
			wantServing: true,
			wantMessage: "Workload rollout of Deployment svc-kserve is stalled: pod svc-kserve-new is CrashLoopBackOff: " +
				"back-off 40s restarting failed container=main; 1 pod(s) are ready and serving",
		},
		{
			name:            "progress deadline exceeded on an unschedulable new pod",
			llmService:      llmService(corev1.ConditionTrue, 2),
			pods:            []corev1.Pod{readyPod, unschedulablePod},
			deployment:      deployment(2, 1, deadlineExceeded),
			wantState:       constants.AIMStatusDegraded,
			wantReason:      aimv1alpha1.AIMServiceReasonRolloutStalled,
			wantServing:     true,
			wantUserFixable: true,
		},
		{
			name:        "old revision serves while new pod is unschedulable",
			llmService:  llmService(corev1.ConditionTrue, 2),
			pods:        []corev1.Pod{readyPod, unschedulablePod},
			deployment:  deployment(2, 1),
			wantState:   constants.AIMStatusProgressing,
			wantReason:  aimv1alpha1.AIMServiceReasonRolloutInProgress,
			wantServing: true,
			wantMessage: "Workload rollout in progress: 1 of 2 pods are on the new revision, 1 pod(s) are ready and serving; " +
				"pod svc-kserve-new is Pending: Unschedulable: 0/3 nodes are available: 3 Insufficient memory.",
		},
		{
			name:        "old revision serves while the new pod is refused",
			llmService:  llmService(corev1.ConditionTrue, 2),
			pods:        []corev1.Pod{readyPod},
			deployment:  deployment(1, 0, refused),
			wantState:   constants.AIMStatusProgressing,
			wantReason:  aimv1alpha1.AIMServiceReasonRolloutInProgress,
			wantServing: true,
			wantMessage: "Workload rollout in progress: 0 of 1 pods are on the new revision, 1 pod(s) are ready and serving; " +
				`the workload Deployment cannot create a pod: pods "svc-kserve-new" is forbidden: exceeded quota: compute`,
		},
		{
			name:       "ready with the rollout finished",
			llmService: llmService(corev1.ConditionTrue, 2),
			pods:       []corev1.Pod{readyPod},
			deployment: deployment(1, 1),
			wantState:  constants.AIMStatusReady,
			wantReason: aimv1alpha1.AIMServiceReasonRuntimeReady,
		},
		{
			name:       "ready pods override a stale generation",
			llmService: llmService(corev1.ConditionTrue, 1),
			pods:       []corev1.Pod{readyPod},
			deployment: deployment(1, 1),
			wantState:  constants.AIMStatusReady,
			wantReason: aimv1alpha1.AIMServiceReasonRuntimeScaling,
		},
		{
			name:       "scale-up on the current revision stays ready",
			llmService: llmService(corev1.ConditionFalse, 2),
			pods:       []corev1.Pod{readyPod, unschedulablePod},
			deployment: deployment(2, 2),
			wantState:  constants.AIMStatusReady,
			wantReason: aimv1alpha1.AIMServiceReasonRuntimeScaling,
		},
		{
			name:       "no ready pod during a stale generation",
			llmService: llmService(corev1.ConditionTrue, 1),
			pods:       []corev1.Pod{unschedulablePod},
			deployment: deployment(1, 1),
			wantState:  constants.AIMStatusProgressing,
			wantReason: reasonLLMInferenceServiceNotReady,
		},
		{
			name:       "workload Deployment fetch error is reported",
			llmService: llmService(corev1.ConditionFalse, 2),
			pods:       []corev1.Pod{readyPod},
			deployment: controllerutils.FetchResult[*appsv1.Deployment]{Error: errors.New("api unavailable")},
			wantState:  constants.AIMStatusFailed,
			wantReason: reasonFetchError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pods := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{Items: tc.pods}}
			health := llmInferenceServiceComponentHealth(
				controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: tc.llmService}, pods, tc.deployment,
			)
			if health.State != tc.wantState || health.Reason != tc.wantReason || health.Serving != tc.wantServing ||
				health.UserFixable != tc.wantUserFixable {
				t.Fatalf("health = %s/%s serving=%t userFixable=%t (%s), want %s/%s serving=%t userFixable=%t",
					health.State, health.Reason, health.Serving, health.UserFixable, health.Message,
					tc.wantState, tc.wantReason, tc.wantServing, tc.wantUserFixable)
			}
			if tc.wantMessage != "" && health.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", health.Message, tc.wantMessage)
			}
		})
	}
}

func TestComposeState_llmInferenceServiceWithoutSupportIsInvalid(t *testing.T) {
	service := llmTestService()
	fetch := llmTestFetch(service, llmTestClusterProfile())
	fetch.servesLLM = false
	reconciler := &ProfileServiceReconciler{}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha2.AIMService]{Object: service}

	obs := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)
	if obs.servingAPIErr == nil {
		t.Fatal("expected an LLMInferenceService service without support to be rejected")
	}
	plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)
	if len(plan.GetToApply()) != 0 || len(plan.GetToApplyWithoutOwnerRef()) != 0 {
		t.Fatal("an unsupported serving API must not plan any resources")
	}

	status := &aimv1alpha1.AIMServiceStatus{}
	reconciler.DecorateStatus(status, controllerutils.NewConditionManager(nil), obs)
	if status.ServingAPI != aimv1alpha1.ServingAPILLMInferenceService {
		t.Errorf("status.servingAPI = %q, want the requested API recorded", status.ServingAPI)
	}
}

func TestLLMWorkloadServiceName(t *testing.T) {
	if got := llmWorkloadServiceName("svc-abc123"); got != "svc-abc123-kserve-workload-svc" {
		t.Fatalf("llmWorkloadServiceName() = %q", got)
	}
}

func autoscaledLLMObservation(t *testing.T) (*aimv1alpha2.AIMService, ServiceObservation) {
	t.Helper()
	service := llmTestService()
	service.Spec.MinReplicas = ptr.To[int32](1)
	service.Spec.MaxReplicas = ptr.To[int32](4)
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{{
			Type: "PodMetric",
			PodMetric: &aimv1alpha1.AIMServicePodMetricSource{
				Metric: &aimv1alpha1.AIMServicePodMetric{
					MetricNames: []string{"vllm:num_requests_running"},
					Query:       "vllm:num_requests_running",
				},
				Target: &aimv1alpha1.AIMServiceMetricTarget{Type: "Value", Value: "2"},
			},
		}},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:                service,
			servingAPI:             aimv1alpha1.ServingAPILLMInferenceService,
			servesLLM:              true,
			openTelemetryCollector: true,
		},
		resolvedProfileSpec:   profileSpecWithModelSources(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testClusterProfileName,
		profileScope:          aimv1alpha1.AIMResolutionScopeCluster,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)
	return service, obs
}

func TestBuildLLMInferenceServiceFromProfile_autoscaledLeavesReplicasToKEDA(t *testing.T) {
	service, obs := autoscaledLLMObservation(t)
	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	if llmService.Spec.Replicas != nil {
		t.Errorf("replicas = %d, want unset so KServe preserves the KEDA-managed count", *llmService.Spec.Replicas)
	}
	if got := llmService.Spec.Annotations[constants.AnnotationOTelSidecarInject]; got != llmDeploymentName(obs.isvcName) {
		t.Errorf("sidecar inject annotation = %q, want the collector %q", got, llmDeploymentName(obs.isvcName))
	}
}

func TestPlanAutoscaling_llmInferenceServiceTargetsWorkloadDeployment(t *testing.T) {
	service, obs := autoscaledLLMObservation(t)
	deploymentName := llmDeploymentName(obs.isvcName)

	var scaledObject, collector map[string]interface{}
	for _, obj := range planAutoscaling(context.Background(), service, obs, nil) {
		u, ok := obj.(interface{ UnstructuredContent() map[string]interface{} })
		if !ok {
			t.Fatalf("unexpected autoscaling object %T", obj)
		}
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case scaledObjectKind:
			scaledObject = u.UnstructuredContent()
		case "OpenTelemetryCollector":
			collector = u.UnstructuredContent()
		}
		if obj.GetName() != deploymentName {
			t.Errorf("%s name = %q, want the workload Deployment %q", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), deploymentName)
		}
	}
	if scaledObject == nil || collector == nil {
		t.Fatalf("expected a ScaledObject and an OpenTelemetryCollector, got scaledObject=%v collector=%v", scaledObject != nil, collector != nil)
	}

	spec := scaledObject["spec"].(map[string]interface{})
	if target := spec["scaleTargetRef"].(map[string]interface{})["name"]; target != deploymentName {
		t.Errorf("scaleTargetRef = %v, want %q", target, deploymentName)
	}
	trigger := spec["triggers"].([]interface{})[0].(map[string]interface{})
	query := trigger["metadata"].(map[string]interface{})["metricQuery"].(string)
	if !strings.Contains(query, `deployment="`+deploymentName+`"`) {
		t.Errorf("metric query %q is not scoped to the workload Deployment", query)
	}

	collectorSpec := collector["spec"].(map[string]interface{})
	if collectorSpec["mode"] != "sidecar" {
		t.Errorf("collector mode = %v, want sidecar", collectorSpec["mode"])
	}
	processors := collectorSpec["config"].(map[string]interface{})["processors"].(map[string]interface{})
	if _, ok := processors["filter/metrics"]; !ok {
		t.Error("declared metric names must restrict what the collector forwards")
	}
}

func TestPlanAutoscaling_llmInferenceServiceActivationQueryPerScope(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	deploymentName := llmDeploymentName(obs.isvcName)
	backendServiceName := llmWorkloadServiceName(obs.isvcName)

	tests := []struct {
		name          string
		scope         string
		queryTemplate string
		want          string
	}{
		{
			name:  "kgateway matches the backend Service, not the Deployment",
			scope: constants.GatewayActivationScopeDeployment,
			want:  `deployment="` + backendServiceName + `"`,
		},
		{
			name:  "envoy gateway matches the HTTPRoute",
			scope: constants.GatewayActivationScopeHTTPRoute,
			want:  "envoy_http_lua_aim_activation_requests_n2_ns_r",
		},
		{
			name:          "custom template resolves predictorDeployment to the workload Deployment",
			scope:         constants.GatewayActivationScopeCustom,
			queryTemplate: `sum(requests{deployment="${predictorDeployment}"})`,
			want:          `sum(requests{deployment="` + deploymentName + `"})`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(constants.EnvAIMGatewayActivationScope, tt.scope)
			service, obs := autoscaledLLMObservation(t)
			service.Spec.MinReplicas = ptr.To[int32](0)
			if tt.queryTemplate != "" {
				service.Spec.ScaleFromZero = &aimv1alpha1.AIMScaleFromZeroConfig{ActivationMetricQueryTemplate: tt.queryTemplate}
			}

			var triggers []interface{}
			for _, obj := range planAutoscaling(context.Background(), service, obs, nil) {
				if obj.GetObjectKind().GroupVersionKind().Kind != scaledObjectKind {
					continue
				}
				spec := obj.(interface{ UnstructuredContent() map[string]interface{} }).UnstructuredContent()["spec"].(map[string]interface{})
				triggers = spec["triggers"].([]interface{})
			}
			if len(triggers) != 2 {
				t.Fatalf("triggers = %d, want activation + user metric", len(triggers))
			}
			activation := triggers[0].(map[string]interface{})["metadata"].(map[string]interface{})["metricQuery"].(string)
			if !strings.Contains(activation, tt.want) {
				t.Errorf("activation query = %q, want it to contain %q", activation, tt.want)
			}
			user := triggers[1].(map[string]interface{})["metadata"].(map[string]interface{})["metricQuery"].(string)
			if !strings.Contains(user, `deployment="`+deploymentName+`"`) {
				t.Errorf("user metric query = %q, want it scoped to the workload Deployment", user)
			}
		})
	}
}

func TestLLMAutoscaling_withoutOpenTelemetryCollector(t *testing.T) {
	service, obs := autoscaledLLMObservation(t)
	if health := llmAutoscalingComponentHealth(service, obs.ServiceFetchResult); health.State != constants.AIMStatusReady {
		t.Fatalf("health = %+v, want Ready when the collector API is served", health)
	}

	obs.openTelemetryCollector = false
	health := llmAutoscalingComponentHealth(service, obs.ServiceFetchResult)
	if health.State != constants.AIMStatusFailed ||
		health.Reason != aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorUnavailable {
		t.Fatalf("health = %+v, want Failed/%s", health, aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorUnavailable)
	}
	if len(health.Errors) != 1 {
		t.Fatalf("health errors = %v, want one", health.Errors)
	}
	if se, ok := health.Errors[0].(controllerutils.StateEngineError); !ok || se.Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("health error = %v, want an invalid-spec error so ConfigValid=False", health.Errors[0])
	}
	for _, obj := range planAutoscaling(context.Background(), service, obs, nil) {
		if obj.GetObjectKind().GroupVersionKind().Kind == "OpenTelemetryCollector" {
			t.Error("the collector must not be planned when its API is not served")
		}
	}

	fixed := llmTestService()
	if health := llmAutoscalingComponentHealth(fixed, obs.ServiceFetchResult); health.Component != "" {
		t.Errorf("health = %+v, want none for a fixed-replica service", health)
	}
}

func TestPlanAutoscaling_fixedReplicasPlansNothing(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	fixed := llmTestService()
	obs.service = fixed
	if objects := planAutoscaling(context.Background(), fixed, obs, nil); len(objects) != 0 {
		t.Fatalf("planAutoscaling() = %d objects, want none for a fixed-replica service", len(objects))
	}
}

func TestStaleAutoscalingCleanup_autoscalingTurnedOff(t *testing.T) {
	for _, tc := range []struct {
		name       string
		servingAPI aimv1alpha1.AIMServingAPI
		wantKinds  []string
	}{
		{"InferenceService", aimv1alpha1.ServingAPIInferenceService, []string{"ScaledObject"}},
		{"LLMInferenceService", aimv1alpha1.ServingAPILLMInferenceService, []string{"OpenTelemetryCollector", "ScaledObject"}},
	} {
		for _, controlled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s controlled=%t", tc.name, controlled), func(t *testing.T) {
				service, obs := autoscaledLLMObservation(t)
				service.Spec.ServingAPI = tc.servingAPI
				obs.servingAPI = tc.servingAPI
				obs.servesLLM = tc.servingAPI == aimv1alpha1.ServingAPILLMInferenceService

				planned := planAutoscaling(context.Background(), service, obs, nil)
				var kinds []string
				builder := fake.NewClientBuilder().WithScheme(runtime.NewScheme())
				for _, obj := range planned {
					kinds = append(kinds, obj.GetObjectKind().GroupVersionKind().Kind)
					owner := service.DeepCopy()
					if !controlled {
						owner.Name, owner.UID = "other-service", "other-uid"
					}
					obj.SetOwnerReferences([]metav1.OwnerReference{
						*metav1.NewControllerRef(owner, aimv1alpha2.GroupVersion.WithKind("AIMService")),
					})
					builder = builder.WithObjects(obj)
				}
				slices.Sort(kinds)
				if !slices.Equal(kinds, tc.wantKinds) {
					t.Fatalf("planned kinds = %v, want %v", kinds, tc.wantKinds)
				}
				c := builder.Build()
				kedaHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
					Value: &autoscalingv2.HorizontalPodAutoscaler{},
				}
				if fetched := fetchStaleAutoscaling(context.Background(), c, service, kedaHPA, obs.servesLLM, true); fetched != nil {
					t.Fatalf("an autoscaled service read %d stale autoscaling objects, want none", len(fetched))
				}

				service.Spec.MinReplicas, service.Spec.MaxReplicas, service.Spec.AutoScaling = nil, nil, nil
				if objects := planAutoscaling(context.Background(), service, obs, nil); len(objects) != 0 {
					t.Fatalf("planAutoscaling() = %d objects once autoscaling is off, want none", len(objects))
				}
				noHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
					Error: notFoundError("horizontalpodautoscalers", "keda-hpa"),
				}
				if fetched := fetchStaleAutoscaling(context.Background(), c, service, noHPA, obs.servesLLM, true); fetched != nil {
					t.Fatalf("read %d autoscaling objects without KEDA's HPA, want none", len(fetched))
				}
				obs.staleAutoscaling = fetchStaleAutoscaling(context.Background(), c, service, kedaHPA, obs.servesLLM, true)
				var plan controllerutils.PlanResult
				planStaleAutoscalingCleanup(&plan, obs, logr.Discard())

				var deleted []string
				for _, obj := range plan.GetToDelete() {
					deleted = append(deleted, obj.GetObjectKind().GroupVersionKind().Kind)
				}
				slices.Sort(deleted)
				want := tc.wantKinds
				if !controlled {
					want = nil
				}
				if !slices.Equal(deleted, want) {
					t.Errorf("deleted kinds = %v, want %v", deleted, want)
				}
			})
		}
	}
}

func TestLLMWorkloadPodsHealth_deletedFailedPod(t *testing.T) {
	readyPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve-new", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	// The kubelet reports a deleted pod Failed once a container exits non-zero
	// on SIGTERM.
	failedPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-kserve-old", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "main",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 2, Reason: "Error"}},
			}},
		},
	}
	deletedFailedPod := *failedPod.DeepCopy()
	deletedFailedPod.DeletionTimestamp = ptr.To(metav1.Now())

	scaleToZero := llmTestService()
	scaleToZero.Spec.Replicas = nil
	scaleToZero.Spec.MinReplicas = ptr.To[int32](0)
	scaleToZero.Spec.MaxReplicas = ptr.To[int32](3)
	idleHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: &autoscalingv2.HorizontalPodAutoscaler{Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
				{Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "ScalingDisabled"},
			},
		}},
	}

	for _, tc := range []struct {
		name       string
		service    *aimv1alpha2.AIMService
		hpa        controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]
		pods       []corev1.Pod
		wantState  constants.AIMStatus
		wantReason string
		wantErrors bool
	}{
		{
			name:       "a pod that failed while a rollout replaced it does not fail the service",
			service:    llmTestService(),
			pods:       []corev1.Pod{readyPod, deletedFailedPod},
			wantState:  constants.AIMStatusReady,
			wantReason: "PodsReady",
		},
		{
			name:       "a pod that failed while scaling to zero leaves the service idle",
			service:    scaleToZero,
			hpa:        idleHPA,
			pods:       []corev1.Pod{deletedFailedPod},
			wantState:  constants.AIMStatusReady,
			wantReason: aimv1alpha1.AIMServiceReasonScaledToZero,
		},
		{
			name:       "a failed pod that is not being deleted still fails the service",
			service:    llmTestService(),
			pods:       []corev1.Pod{readyPod, failedPod},
			wantState:  constants.AIMStatusFailed,
			wantReason: "PodFailed",
			wantErrors: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pods := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{Items: tc.pods}}
			health, ok := llmWorkloadPodsComponentHealth(context.Background(), nil, tc.service, tc.hpa, pods)
			if !ok {
				t.Fatal("llmWorkloadPodsComponentHealth() reported nothing for a fetched pod list")
			}
			if health.GetState() != tc.wantState || health.GetReason() != tc.wantReason {
				t.Fatalf("health = %s/%s, want %s/%s", health.GetState(), health.GetReason(), tc.wantState, tc.wantReason)
			}
			if gotErrors := len(health.Errors) > 0; gotErrors != tc.wantErrors {
				t.Errorf("errors = %v, want errors: %t", health.Errors, tc.wantErrors)
			}
		})
	}
}

func llmPreset(name, namespace string, mutate func(*kservev1alpha2.LLMInferenceServiceConfig)) *kservev1alpha2.LLMInferenceServiceConfig {
	config := &kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if mutate != nil {
		mutate(config)
	}
	return config
}

func TestFetchLLMBaseRefs_resolvesServiceNamespaceThenKServeNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kservev1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		llmPreset("team-args", "ns", nil),
		llmPreset("team-args", "kserve-system", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
			cfg.Labels = map[string]string{"shadowed": "true"}
		}),
		llmPreset("cluster-tracing", "kserve-system", nil),
	).Build()

	service := llmTestService()
	service.Spec.LLMInferenceService = &aimv1alpha1.AIMServiceLLMInferenceServiceConfig{
		BaseRefs: []aimv1alpha1.AIMLLMInferenceServiceConfigReference{{Name: "team-args"}, {Name: "cluster-tracing"}, {Name: "missing"}},
	}
	refs := fetchLLMBaseRefs(context.Background(), c, service, "kserve-system")

	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3", len(refs))
	}
	if refs[0].config == nil || refs[0].config.Namespace != "ns" {
		t.Errorf("team-args resolved to %#v, want the service-namespace config", refs[0].config)
	}
	if refs[1].config == nil || refs[1].config.Namespace != "kserve-system" {
		t.Errorf("cluster-tracing resolved to %#v, want the KServe-namespace config", refs[1].config)
	}
	if refs[2].config != nil || refs[2].err != nil {
		t.Errorf("missing resolved to config=%v err=%v, want a confident absence", refs[2].config, refs[2].err)
	}
}

func TestLLMBaseRefsComponentHealth(t *testing.T) {
	routed := llmPreset("team-route", "ns", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
		cfg.Spec.Router = &kservev1alpha2.RouterSpec{}
	})
	scaled := llmPreset("team-scale", "ns", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
		cfg.Spec.Replicas = ptr.To[int32](3)
		cfg.Spec.Model.LoRA = &kservev1alpha2.LoRASpec{}
	})
	traced := llmPreset("team-tracing", "ns", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
		cfg.Spec.Tracing = &kservev1alpha2.TracingSpec{}
	})
	for _, tc := range []struct {
		name        string
		refs        []llmBaseRef
		wantState   constants.AIMStatus
		wantReason  string
		wantMessage string
		usable      bool
	}{
		{"no refs", nil, "", "", "", true},
		{
			"resolved",
			[]llmBaseRef{{name: "team-args", config: llmPreset("team-args", "ns", nil)}},
			constants.AIMStatusReady, reasonLLMBaseRefsReady, "", true,
		},
		{
			"missing",
			[]llmBaseRef{{name: "team-args"}},
			constants.AIMStatusFailed, reasonLLMBaseRefNotFound, "team-args not found", false,
		},
		{
			"conflicting",
			[]llmBaseRef{{name: "team-route", config: routed}, {name: "team-scale", config: scaled}},
			constants.AIMStatusFailed, reasonLLMBaseRefConflict, "team-scale sets spec.replicas, spec.model.lora", false,
		},
		{
			"tracing",
			[]llmBaseRef{{name: "team-tracing", config: traced}},
			constants.AIMStatusFailed, reasonLLMBaseRefConflict, "team-tracing sets spec.tracing", false,
		},
		{
			"fetch error",
			[]llmBaseRef{{name: "team-args", err: apierrors.NewServiceUnavailable("api down")}},
			constants.AIMStatusFailed, reasonFetchError, `"team-args": api down`, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health := llmBaseRefsComponentHealth(tc.refs, "kserve-system", "ns")
			if health.State != tc.wantState || health.Reason != tc.wantReason {
				t.Fatalf("health = %q/%q, want %q/%q", health.State, health.Reason, tc.wantState, tc.wantReason)
			}
			if !strings.Contains(health.Message, tc.wantMessage) {
				t.Errorf("message %q does not mention %q", health.Message, tc.wantMessage)
			}
			if got := llmBaseRefsUsable(tc.refs); got != tc.usable {
				t.Errorf("llmBaseRefsUsable() = %v, want %v", got, tc.usable)
			}
		})
	}
}

func profileContractConfig() *kservev1alpha2.LLMInferenceServiceConfig {
	return llmPreset("aim-profile", "ns", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
		gpus := corev1.ResourceList{constants.AMDGPUResourceName: resource.MustParse("2")}
		cfg.Spec.Template = &corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      serving.LLMInferenceServiceContainerName,
				Image:     "docker.io/amdenterpriseai/aim-qwen:1.0",
				Command:   []string{"./entrypoint.py"},
				Resources: corev1.ResourceRequirements{Requests: gpus, Limits: gpus},
			}},
			Affinity: &corev1.Affinity{NodeAffinity: gpuModelAffinity("MI300X")},
		}
	})
}

func gpuModelAffinity(model string) *corev1.NodeAffinity {
	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: "amd.com/gpu.product-name", Operator: corev1.NodeSelectorOpIn, Values: []string{model},
				}},
			}},
		},
	}
}

func userPreset(name string, mutate func(*corev1.PodSpec, *kservev1alpha2.LLMInferenceServiceSpec)) llmBaseRef {
	config := llmPreset(name, "ns", func(cfg *kservev1alpha2.LLMInferenceServiceConfig) {
		cfg.Spec.Template = &corev1.PodSpec{Containers: []corev1.Container{{Name: serving.LLMInferenceServiceContainerName}}}
		mutate(cfg.Spec.Template, &cfg.Spec)
	})
	return llmBaseRef{name: name, config: config}
}

func TestLLMProfileContractOverrides(t *testing.T) {
	sameAsProfile := func(pod *corev1.PodSpec, _ *kservev1alpha2.LLMInferenceServiceSpec) {
		pod.Containers[0].Image = "docker.io/amdenterpriseai/aim-qwen:1.0"
		pod.Containers[0].Command = []string{"./entrypoint.py"}
		pod.Containers[0].Resources.Limits = corev1.ResourceList{constants.AMDGPUResourceName: resource.MustParse("2")}
		pod.Affinity = &corev1.Affinity{NodeAffinity: gpuModelAffinity("MI300X")}
	}
	resized := func(pod *corev1.PodSpec, _ *kservev1alpha2.LLMInferenceServiceSpec) {
		pod.Containers[0].Resources.Requests = corev1.ResourceList{
			constants.AMDGPUResourceName: resource.MustParse("4"),
			corev1.ResourceMemory:        resource.MustParse("64Gi"),
		}
	}

	for _, tc := range []struct {
		name         string
		ref          llmBaseRef
		setResources bool
		want         string
	}{
		{
			name: "tuning outside the contract is not flagged",
			ref: userPreset("team-tuning", func(pod *corev1.PodSpec, _ *kservev1alpha2.LLMInferenceServiceSpec) {
				pod.Containers[0].Env = []corev1.EnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}}
				pod.Tolerations = []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}
				pod.Containers[0].Resources.Requests = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")}
			}),
		},
		{
			name: "values equal to the profile are not flagged",
			ref:  userPreset("team-same", sameAsProfile),
		},
		{
			name: "KServe engine fields the profile launch ignores",
			ref: userPreset("team-kserve", func(_ *corev1.PodSpec, spec *kservev1alpha2.LLMInferenceServiceSpec) {
				spec.Parallelism = &kservev1alpha2.ParallelismSpec{Tensor: ptr.To[int32](4)}
				spec.KVCacheOffloading = &kservev1alpha2.KVCacheOffloadingSpec{CPU: resource.MustParse("10Gi")}
			}),
			want: "team-kserve sets spec.parallelism, spec.kvCacheOffloading",
		},
		{
			name: "engine image and launch",
			ref: userPreset("team-launch", func(pod *corev1.PodSpec, _ *kservev1alpha2.LLMInferenceServiceSpec) {
				pod.Containers[0].Image = "docker.io/vllm/vllm-openai:v0.11.0"
				pod.Containers[0].Command = []string{"vllm", "serve"}
				pod.Containers[0].Args = []string{"--tensor-parallel-size=4"}
			}),
			want: "team-launch sets spec.template.containers[main].image, spec.template.containers[main].command, " +
				"spec.template.containers[main].args",
		},
		{
			name: "accelerator count",
			ref:  userPreset("team-gpus", resized),
			want: "team-gpus sets spec.template.containers[main].resources.requests[amd.com/gpu]",
		},
		{
			name:         "accelerator count yields to spec.resources",
			ref:          userPreset("team-gpus", resized),
			setResources: true,
		},
		{
			name: "node affinity",
			ref: userPreset("team-placement", func(pod *corev1.PodSpec, _ *kservev1alpha2.LLMInferenceServiceSpec) {
				pod.Affinity = &corev1.Affinity{NodeAffinity: gpuModelAffinity("MI325X")}
			}),
			want: "team-placement sets spec.template.affinity.nodeAffinity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := llmTestService()
			if tc.setResources {
				service.Spec.Resources = &corev1.ResourceRequirements{}
			}
			got := strings.Join(llmProfileContractOverrides(service, []llmBaseRef{tc.ref}, profileContractConfig()), "; ")
			if got != tc.want {
				t.Errorf("overrides = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetProfileContractCondition(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	obs.desiredProfileLLMConfig = profileContractConfig()
	condType := aimv1alpha1.AIMServiceConditionProfileContractIntact

	for _, tc := range []struct {
		name       string
		refs       []llmBaseRef
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{name: "no baseRefs", refs: nil},
		{name: "unresolved baseRef", refs: []llmBaseRef{{name: "team-missing"}}},
		{
			name:       "intact",
			refs:       []llmBaseRef{{name: "team-args", config: llmPreset("team-args", "ns", nil)}},
			wantStatus: metav1.ConditionTrue,
			wantReason: aimv1alpha1.AIMServiceReasonNoProfileOverrides,
		},
		{
			name: "overridden",
			refs: []llmBaseRef{userPreset("team-kserve", func(_ *corev1.PodSpec, spec *kservev1alpha2.LLMInferenceServiceSpec) {
				spec.Parallelism = &kservev1alpha2.ParallelismSpec{Tensor: ptr.To[int32](4)}
			})},
			wantStatus: metav1.ConditionFalse,
			wantReason: aimv1alpha1.AIMServiceReasonProfileContractOverridden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs.llmBaseRefs = tc.refs
			cm := controllerutils.NewConditionManager([]metav1.Condition{{
				Type: condType, Status: metav1.ConditionTrue, Reason: "Stale",
			}})
			setProfileContractCondition(cm, obs)
			got := cm.Get(condType)
			if tc.wantStatus == "" {
				if got != nil {
					t.Fatalf("condition = %+v, want it removed", got)
				}
				return
			}
			if got == nil || got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("condition = %+v, want %s/%s", got, tc.wantStatus, tc.wantReason)
			}
		})
	}
}

func TestBuildLLMInferenceServiceFromProfile_appendsUserBaseRefsAfterProfileConfig(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	service := llmTestService()
	service.Spec.LLMInferenceService = &aimv1alpha1.AIMServiceLLMInferenceServiceConfig{
		BaseRefs: []aimv1alpha1.AIMLLMInferenceServiceConfigReference{{Name: "team-args"}, {Name: "team-tracing"}},
	}
	obs.service = service

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	var names []string
	for _, ref := range llmService.Spec.BaseRefs {
		names = append(names, ref.Name)
	}
	want := []string{serving.RuntimeName(obs.profileName), "team-args", "team-tracing"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("baseRefs = %v, want %v (profile config first, then presets in order)", names, want)
	}
}

func llmAdapterObservation(t *testing.T) (*aimv1alpha2.AIMService, ServiceObservation) {
	t.Helper()
	_, obs := autoscaledLLMObservation(t)
	service := llmTestService()
	service.Spec.Adapters = []aimv1alpha1.AIMServiceAdapterReference{{Kind: "AIMArtifact", Name: "lora-a"}}
	obs.service = service
	obs.adapterState.AdapterDiskPVC = "adapter-disk"
	obs.adapterState.AdapterDiskPVCUID = "pvc-uid"
	obs.adapterState.MaxRank = 32
	return service, obs
}

func TestPlanLLMInferenceService_mountsAdapterSubtree(t *testing.T) {
	service, obs := llmAdapterObservation(t)
	plan := controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())

	llmService := appliedLLMInferenceService(plan.GetToApply())
	if llmService == nil {
		t.Fatal("the LLMInferenceService was not planned")
	}
	template := llmService.Spec.Template
	if !volumeRefsPVC(template.Volumes, "adapter-disk") {
		t.Errorf("adapter disk PVC not mounted; volumes=%+v", template.Volumes)
	}
	container := template.Containers[0]
	var mounted bool
	for _, mount := range container.VolumeMounts {
		mounted = mounted || (mount.MountPath == constants.AIMAdapterMountPath && mount.ReadOnly)
	}
	if !mounted {
		t.Errorf("adapter subtree not mounted read-only at %s on main: %+v", constants.AIMAdapterMountPath, container.VolumeMounts)
	}
	if got := envMap(container.Env)[constants.EnvAIMAdapterMaxRank]; got != "32" {
		t.Errorf("%s = %q, want 32", constants.EnvAIMAdapterMaxRank, got)
	}
}

func TestPlanLLMInferenceService_preservesMountWhileAdapterDiskUnresolved(t *testing.T) {
	service, obs := llmAdapterObservation(t)
	obs.adapterState.AdapterDiskPVCUID = ""
	plan := controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())

	if appliedLLMInferenceService(plan.GetToApply()) != nil {
		t.Fatal("an existing LLMInferenceService must keep its mount while the adapter disk binding is unresolved")
	}
	if plan.RequeueAfter == 0 {
		t.Error("expected a requeue while the adapter disk binding settles")
	}
}

func TestPlanLLMInferenceService_skipsWhenBaseRefsUnusable(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	service := llmTestService()
	obs.service = service
	obs.llmBaseRefs = []llmBaseRef{{name: "missing"}}
	plan := controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, false, logr.Discard())

	if appliedLLMInferenceService(plan.GetToApply()) != nil {
		t.Fatal("the LLMInferenceService must not be planned with a missing preset")
	}
}

func TestPlanStaleChildCleanup_llmInferenceService(t *testing.T) {
	service := llmTestService()
	owner := []metav1.OwnerReference{{Kind: "AIMService", Name: service.Name, UID: service.UID, Controller: ptr.To(true)}}
	pod := func(profile string, ready bool, mutate ...func(*corev1.Pod)) corev1.Pod {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		p := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{constants.LabelProfile: profile}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
		}
		for _, m := range mutate {
			m(&p)
		}
		return p
	}
	unlabeled := func(p *corev1.Pod) { p.Labels = nil }
	terminating := func(p *corev1.Pod) { p.DeletionTimestamp = ptr.To(metav1.Now()) }
	pods := func(items ...corev1.Pod) *controllerutils.FetchResult[*corev1.PodList] {
		return &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{Items: items}}
	}

	tests := []struct {
		name       string
		llmProfile string
		llmReady   bool
		pods       *controllerutils.FetchResult[*corev1.PodList]
		want       []string
	}{
		{name: "a ready pod serves profile-b", llmProfile: "profile-b", llmReady: true, pods: pods(pod("profile-b", true)), want: []string{"cache-a", "profile-a"}},
		{name: "LLMInferenceService still on profile-a", llmProfile: "profile-a", llmReady: true, pods: pods(pod("profile-a", true))},
		{name: "LLMInferenceService not ready", llmProfile: "profile-b", pods: pods(pod("profile-b", true))},
		{name: "pod on profile-a still running", llmProfile: "profile-b", llmReady: true, pods: pods(pod("profile-b", true), pod("profile-a", true))},
		{name: "profile-b pod not ready", llmProfile: "profile-b", llmReady: true, pods: pods(pod("profile-b", false))},
		{name: "only a terminating profile-b pod", llmProfile: "profile-b", llmReady: true, pods: pods(pod("profile-b", true, terminating))},
		{name: "pod without the profile label", llmProfile: "profile-b", llmReady: true, pods: pods(pod("profile-b", true), pod("", true, unlabeled))},
		{name: "scaled to zero", llmProfile: "profile-b", llmReady: true, pods: pods()},
		{name: "pod list failed", llmProfile: "profile-b", llmReady: true, pods: &controllerutils.FetchResult[*corev1.PodList]{Error: notFoundError("pods", "x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readyStatus := corev1.ConditionFalse
			if tt.llmReady {
				readyStatus = corev1.ConditionTrue
			}
			llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{
				Name: testServiceName, Generation: 1, Labels: map[string]string{constants.LabelProfile: tt.llmProfile},
			}}
			llmService.Status.Status = duckv1.Status{
				ObservedGeneration: 1,
				Conditions:         duckv1.Conditions{{Type: apis.ConditionReady, Status: readyStatus}},
			}
			obs := ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service:                 service,
					servesLLM:               true,
					llmInferenceService:     controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: llmService},
					llmInferenceServicePods: tt.pods,
					serviceProfileCaches: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCacheList]{Value: &aimv1alpha2.AIMProfileCacheList{
						Items: []aimv1alpha2.AIMProfileCache{
							{ObjectMeta: metav1.ObjectMeta{Name: "cache-a", OwnerReferences: owner}, Spec: aimv1alpha2.AIMProfileCacheSpec{ProfileName: "profile-a"}},
							{ObjectMeta: metav1.ObjectMeta{Name: "cache-b", OwnerReferences: owner}, Spec: aimv1alpha2.AIMProfileCacheSpec{ProfileName: "profile-b"}},
						},
					}},
					serviceOverlayProfiles: controllerutils.FetchResult[*aimv1alpha2.AIMProfileList]{Value: &aimv1alpha2.AIMProfileList{
						Items: []aimv1alpha2.AIMProfile{
							{ObjectMeta: metav1.ObjectMeta{Name: "profile-a", OwnerReferences: owner}},
							{ObjectMeta: metav1.ObjectMeta{Name: "profile-b", OwnerReferences: owner}},
						},
					}},
				},
				profileName: "profile-b",
			}

			plan := controllerutils.PlanResult{}
			planStaleChildCleanup(&plan, obs, logr.Discard())

			var got []string
			for _, obj := range plan.GetToDelete() {
				got = append(got, obj.GetName())
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("deleted %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLLMInferenceServiceComponentHealth_refusedPod(t *testing.T) {
	llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: "svc", Generation: 1}}
	llmService.Status.Status = duckv1.Status{
		ObservedGeneration: 1,
		Conditions:         duckv1.Conditions{{Type: apis.ConditionReady, Status: corev1.ConditionFalse}},
	}
	fetch := controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: llmService}
	refusal := `pods "svc-kserve-abc" is forbidden: violates PodSecurity "restricted:latest": runAsNonRoot != true`
	refused := controllerutils.FetchResult[*appsv1.Deployment]{Value: &appsv1.Deployment{
		Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: refusal,
		}}},
	}}
	noPods := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{}}
	startingPod := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{Items: []corev1.Pod{{
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}}}}

	for _, tc := range []struct {
		name       string
		pods       *controllerutils.FetchResult[*corev1.PodList]
		deployment controllerutils.FetchResult[*appsv1.Deployment]
		wantState  constants.AIMStatus
		wantReason string
	}{
		{"refused without pods", noPods, refused, constants.AIMStatusFailed, aimv1alpha1.AIMServiceReasonPodCreationFailed},
		{"refused with a starting pod", startingPod, refused, constants.AIMStatusProgressing, aimv1alpha1.AIMServiceReasonCreatingRuntime},
		{"Deployment not fetched", noPods, controllerutils.FetchResult[*appsv1.Deployment]{}, constants.AIMStatusProgressing, reasonLLMInferenceServiceNotReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := llmInferenceServiceComponentHealth(fetch, tc.pods, tc.deployment)
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("health = %s/%s, want %s/%s", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
			if tc.deployment.Value != nil && !strings.Contains(got.Message, refusal) {
				t.Errorf("message %q must carry the refusal", got.Message)
			}
		})
	}
}

func TestPlanLLMInferenceService_pollsWorkloadDeploymentUntilSettled(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	plan := controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())
	if plan.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %s without a fetched workload Deployment, want 0", plan.RequeueAfter)
	}

	obs.llmWorkloadDeployment = controllerutils.FetchResult[*appsv1.Deployment]{Value: &appsv1.Deployment{}}
	plan = controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())
	if plan.RequeueAfter != llmWorkloadDeploymentPollInterval {
		t.Errorf("RequeueAfter = %s while not ready, want %s", plan.RequeueAfter, llmWorkloadDeploymentPollInterval)
	}

	llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: "svc", Generation: 1}}
	llmService.Status.ObservedGeneration = 1
	llmService.Status.Conditions = duckv1.Conditions{{Type: apis.ConditionReady, Status: corev1.ConditionTrue}}
	obs.llmInferenceService = controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: llmService}
	obs.llmWorkloadDeployment.Value.Status = appsv1.DeploymentStatus{Replicas: 1, UpdatedReplicas: 1}
	plan = controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())
	if plan.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %s for a ready, rolled-out workload, want 0", plan.RequeueAfter)
	}

	obs.llmWorkloadDeployment.Value.Status = appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 1}
	plan = controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())
	if plan.RequeueAfter != llmWorkloadDeploymentPollInterval {
		t.Errorf("RequeueAfter = %s during a rollout, want %s", plan.RequeueAfter, llmWorkloadDeploymentPollInterval)
	}
}

func TestBuildLLMInferenceServiceFromProfile_annotationsRenderLiterally(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	// Unescaped, each q renders a '"', so the value closes the annotations and
	// metadata objects, adds spec.template.hostPID, and reopens both.
	q := "{{printf `%c` 34}}"
	hostile := q + "}}," + q + "spec" + q + ":{ " + q + "template" + q + ":{ " + q + "hostPID" + q + ":true}}," +
		q + "metadata" + q + ":{ " + q + "annotations" + q + ":{ " + q + "k" + q + ":" + q + "v"
	service.Annotations = map[string]string{constants.AnnotationPrefixClusterAuth + "allowed-group": hostile}

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	rendered, err := llmisvc.ReplaceVariables(
		llmService,
		&kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: *llmService.ObjectMeta.DeepCopy(), Spec: llmService.Spec},
		&llmisvc.Config{},
	)
	if err != nil {
		t.Fatalf("ReplaceVariables: %v", err)
	}
	if got := rendered.Annotations[constants.AnnotationPrefixClusterAuth+"allowed-group"]; got != hostile {
		t.Errorf("rendered annotation = %q, want the literal %q", got, hostile)
	}
	if rendered.Spec.Template.HostPID {
		t.Error("an annotation value set hostPID on the rendered workload")
	}
}

func TestPlanServiceHTTPRoute_targetsLLMWorkloadService(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
		Enabled:    ptr.To(true),
		GatewayRef: &gatewayapiv1.ParentReference{Name: "inference-gateway"},
	}

	route, ok := planServiceHTTPRoute(context.Background(), service, obs).(*gatewayapiv1.HTTPRoute)
	if !ok || route == nil {
		t.Fatalf("planServiceHTTPRoute() = %T, want an HTTPRoute", route)
	}
	if len(route.Spec.Rules) == 0 || len(route.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("route rules = %+v, want one backend", route.Spec.Rules)
	}
	backend := route.Spec.Rules[0].BackendRefs[0]
	if want := llmWorkloadServiceName(obs.isvcName); string(backend.Name) != want {
		t.Errorf("backend = %q, want the KServe workload Service %q", backend.Name, want)
	}
	if backend.Port == nil || *backend.Port != constants.DefaultHTTPPort {
		t.Errorf("backend port = %v, want %d", backend.Port, constants.DefaultHTTPPort)
	}
}

func TestBuildLLMInferenceServiceFromProfile_projectsAdditionalCABundle(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	runtimeConfig := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{Name: "company-ca", Key: "ca.crt"},
		Artifact:              &aimv1alpha1.AIMArtifactConfig{ModelDownloadImage: "registry.example.test/downloader:v1"},
	}
	obs.mergedRuntimeConfig = controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: runtimeConfig}

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	if len(llmService.Spec.Template.InitContainers) != 0 {
		t.Fatalf("init containers = %d without the CA bundle ConfigMap, want none", len(llmService.Spec.Template.InitContainers))
	}

	obs.additionalCABundle = controllerutils.FetchResult[*corev1.ConfigMap]{
		Value: &corev1.ConfigMap{Data: map[string]string{"ca.crt": "rotation-one"}},
	}
	llmService, err = buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	template := llmService.Spec.Template
	if len(template.InitContainers) != 1 || template.InitContainers[0].Image != "registry.example.test/downloader:v1" {
		t.Fatalf("init containers = %+v, want the trust projection with the download image", template.InitContainers)
	}
	if got := overlayEnvValue(template.Containers[0].Env, "REQUESTS_CA_BUNDLE"); got != trust.OutputFilePath {
		t.Errorf("REQUESTS_CA_BUNDLE = %q, want %q", got, trust.OutputFilePath)
	}
	if len(template.Volumes) == 0 || len(template.Containers[0].VolumeMounts) == 0 {
		t.Errorf("volumes = %+v, mounts = %+v, want the combined bundle mounted", template.Volumes, template.Containers[0].VolumeMounts)
	}
}

func idleLLMObservation(t *testing.T) ServiceObservation {
	t.Helper()
	service, obs := llmRuntimeConfigObservation(t)
	service.Spec.MinReplicas = ptr.To[int32](0)
	service.Spec.MaxReplicas = ptr.To[int32](3)
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}
	obs.resolvedProfileStatus = &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true}
	obs.hpa = controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{Value: &autoscalingv2.HorizontalPodAutoscaler{
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
				{Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "ScalingDisabled"},
			},
		},
	}}
	llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: obs.isvcName, Generation: 1}}
	llmService.Status.ObservedGeneration = 1
	llmService.Status.Conditions = duckv1.Conditions{{Type: apis.ConditionReady, Status: corev1.ConditionTrue}}
	obs.llmInferenceService = controllerutils.FetchResult[*kservev1alpha2.LLMInferenceService]{Value: llmService}
	obs.llmInferenceServicePods = &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{}}
	obs.llmWorkloadDeployment = controllerutils.FetchResult[*appsv1.Deployment]{Value: &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: llmDeploymentName(obs.isvcName)},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](0)},
	}}
	return obs
}

func componentHealthByName(entries []controllerutils.ComponentHealth, component string) *controllerutils.ComponentHealth {
	for i := range entries {
		if entries[i].Component == component {
			return &entries[i]
		}
	}
	return nil
}

func TestGetComponentHealth_idleScaleToZeroLLMReportsReady(t *testing.T) {
	entries := idleLLMObservation(t).GetComponentHealth(context.Background(), nil)

	for _, tc := range []struct{ component, reason string }{
		{llmInferenceServiceComponent, aimv1alpha1.AIMServiceReasonRuntimeReady},
		{llmInferenceServicePodsComponent, aimv1alpha1.AIMServiceReasonScaledToZero},
		{"HPA", aimv1alpha1.AIMServiceReasonScaledToZero},
	} {
		health := componentHealthByName(entries, tc.component)
		if health == nil {
			t.Fatalf("no %s entry in %+v", tc.component, entries)
		}
		if health.State != constants.AIMStatusReady || health.Reason != tc.reason {
			t.Errorf("%s = %s/%s (%s), want Ready/%s", tc.component, health.State, health.Reason, health.Message, tc.reason)
		}
	}
}

func TestGetComponentHealth_scaleToZeroLLMWithRefusedPod(t *testing.T) {
	const refusal = `pods "svc-kserve-6d8f-abcde" is forbidden: exceeded quota`
	obs := idleLLMObservation(t)
	obs.llmInferenceService.Value.Status.Conditions = duckv1.Conditions{{
		Type: apis.ConditionReady, Status: corev1.ConditionFalse, Reason: "MinimumReplicasUnavailable",
	}}
	obs.llmWorkloadDeployment.Value.Spec.Replicas = ptr.To[int32](1)
	obs.llmWorkloadDeployment.Value.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: refusal,
	}}

	entries := obs.GetComponentHealth(context.Background(), nil)

	llmHealth := componentHealthByName(entries, llmInferenceServiceComponent)
	if llmHealth == nil || llmHealth.State != constants.AIMStatusFailed ||
		llmHealth.Reason != aimv1alpha1.AIMServiceReasonPodCreationFailed || !strings.Contains(llmHealth.Message, refusal) {
		t.Fatalf("LLMInferenceService = %+v, want Failed/PodCreationFailed with the refusal text", llmHealth)
	}
	for _, component := range []string{llmInferenceServicePodsComponent, "HPA"} {
		health := componentHealthByName(entries, component)
		if health == nil || health.State != constants.AIMStatusReady || health.Reason != aimv1alpha1.AIMServiceReasonScaledToZero {
			t.Errorf("%s = %+v, want Ready/ScaledToZero", component, health)
		}
	}
}

func TestPlanResources_llmActivationMetricGraceDeadline(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	obs := idleLLMObservation(t)
	obs.hpa.Value = &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			Metrics: []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: "s0-" + obs.service.Name},
					Target: autoscalingv2.MetricTarget{Type: autoscalingv2.ValueMetricType, Value: ptr.To(resource.MustParse("1"))},
				},
			}},
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			ObservedGeneration: ptr.To(int64(1)),
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{{
				Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "FailedGetExternalMetric",
			}},
		},
	}
	obs.llmInferenceServicePods = &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{Items: []corev1.Pod{{}}}}

	plan := (&ProfileServiceReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMService]{Object: obs.service},
		obs,
	)
	const gracePeriod = 3 * time.Minute
	if plan.RequeueAfter < gracePeriod-10*time.Second || plan.RequeueAfter > gracePeriod {
		t.Errorf("RequeueAfter = %s, want approximately %s", plan.RequeueAfter, gracePeriod)
	}
}

// TestBuildLLMInferenceServiceFromProfile_keepsConfigEnvEntryKServeCannotReplace
// pins that KServe's merge of the LLMInferenceService over the profile's
// LLMInferenceServiceConfig, which merges env entries by name, never gets a
// service entry whose value or valueFrom would combine with the profile's.
func TestBuildLLMInferenceServiceFromProfile_keepsConfigEnvEntryKServeCannotReplace(t *testing.T) {
	profileSecret := &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "profile-token"},
		Key:                  "token",
	}}
	serviceSecret := &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "service-home"},
		Key:                  "home",
	}}
	for _, tc := range []struct {
		name       string
		profileEnv corev1.EnvVar
		serviceEnv corev1.EnvVar
	}{
		{"value over a profile valueFrom", corev1.EnvVar{Name: "HF_TOKEN", ValueFrom: profileSecret}, corev1.EnvVar{Name: "HF_TOKEN", Value: "hf_service"}},
		{"valueFrom over a profile value", corev1.EnvVar{Name: "HOME", Value: "/profile-home"}, corev1.EnvVar{Name: "HOME", ValueFrom: serviceSecret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, obs := llmRuntimeConfigObservation(t)
			obs.desiredProfileRuntime.Spec.Containers[0].Env = []corev1.EnvVar{tc.profileEnv}
			service.Spec.Env = []corev1.EnvVar{tc.serviceEnv}

			llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
			if err != nil || llmService == nil {
				t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
			}
			for _, env := range llmService.Spec.Template.Containers[0].Env {
				if env.Name == tc.serviceEnv.Name {
					t.Errorf("env %+v is set; KServe would merge it with the profile entry into one with a value and a valueFrom", env)
				}
			}
		})
	}
}

func TestBuildLLMInferenceServiceFromProfile_annotationPropagation(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Annotations = map[string]string{
		"cluster-auth/allowed-group":            "ce0c754f-bb1b-63bb-5134-5501142effe7",
		constants.AnnotationModelId:             "user-tried-to-override",
		"aim.eai.amd.com/reconciliation-paused": "true",
		"example.com/foreign":                   "drop-me",
	}

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	annotations := llmService.Annotations
	if got := annotations["cluster-auth/allowed-group"]; got != "ce0c754f-bb1b-63bb-5134-5501142effe7" {
		t.Errorf("cluster-auth annotation = %q, want it propagated", got)
	}
	for _, key := range []string{"example.com/foreign", "aim.eai.amd.com/reconciliation-paused"} {
		if _, set := annotations[key]; set {
			t.Errorf("annotation %q must not be propagated", key)
		}
	}
	if got, want := annotations[constants.AnnotationModelId], serving.ResolvedServedModelID(obs.resolvedProfileSpec); got != want {
		t.Errorf("model-id annotation = %q, want the controller-owned %q", got, want)
	}
	if got := annotations[constants.AnnotationRuntimeProfile]; got != testProfileA {
		t.Errorf("runtime-profile annotation = %q, want %q", got, testProfileA)
	}
}

func TestBuildLLMInferenceServiceFromProfile_replicasDefaultToOne(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Spec.Replicas = nil

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	if got := llmService.Spec.Replicas; got == nil || *got != 1 {
		t.Errorf("replicas = %v, want 1", got)
	}
}

func TestBuildLLMInferenceServiceFromProfile_serviceAccountAndPullSecrets(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	obs.resolvedProfileSpec.ServiceAccountName = "workload-profile-sa"
	service.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "registry-creds"}}

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	template := llmService.Spec.Template
	if template.ServiceAccountName != "workload-profile-sa" {
		t.Errorf("service account = %q, want the profile's workload-profile-sa", template.ServiceAccountName)
	}
	if len(template.ImagePullSecrets) != 1 || template.ImagePullSecrets[0].Name != "registry-creds" {
		t.Errorf("image pull secrets = %+v, want the service's registry-creds", template.ImagePullSecrets)
	}

	service.Spec.ServiceAccountName = "workload-service-sa"
	llmService, err = buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	if got := llmService.Spec.Template.ServiceAccountName; got != "workload-service-sa" {
		t.Errorf("service account = %q, want the service's workload-service-sa", got)
	}
}

func TestBuildLLMInferenceServiceFromProfile_sharedCacheNotOverlaid(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Spec.Caching = &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeShared}
	obs.profileCache = controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: readyCacheFixture()}
	obs.profileCacheReady = true

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	template := llmService.Spec.Template
	if len(template.Volumes) != 0 || len(template.Containers[0].VolumeMounts) != 0 {
		t.Errorf("a Shared cache is mounted by the profile's LLMInferenceServiceConfig; volumes=%+v mounts=%+v",
			template.Volumes, template.Containers[0].VolumeMounts)
	}
	if _, set := envMap(template.Containers[0].Env)[constants.EnvAIMModelID]; set {
		t.Errorf("a Shared cache must not add the %s redirect env", constants.EnvAIMModelID)
	}
}

func TestBuildLLMInferenceServiceFromProfile_dedicatedCacheOverridesDirectVLLMModelReference(t *testing.T) {
	service, obs := llmRuntimeConfigObservation(t)
	service.Spec.Caching = &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeDedicated}
	obs.resolvedProfileSpec.Engine = "vllm"
	obs.resolvedProfileSpec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA
	obs.profileCache = controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: readyCacheFixture()}
	obs.profileCacheReady = true

	llmService, err := buildLLMInferenceServiceFromProfile(service, obs)
	if err != nil || llmService == nil {
		t.Fatalf("buildLLMInferenceServiceFromProfile() = %v, %v", llmService, err)
	}
	env := envMap(llmService.Spec.Template.Containers[0].Env)
	if got := env[constants.EnvAIMVLLMModel]; got != "/workspace/cache/org/model" {
		t.Errorf("%s = %q, want the dedicated cache mount", constants.EnvAIMVLLMModel, got)
	}
	if env["HF_HUB_OFFLINE"] != "1" || env["TRANSFORMERS_OFFLINE"] != "1" {
		t.Errorf("a dedicated direct-vLLM cache must force offline mode: %#v", env)
	}
}

func TestPlanLLMInferenceService_dynamicAdapterMode(t *testing.T) {
	service, obs := llmAdapterObservation(t)
	service.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	plan := controllerutils.PlanResult{}
	planLLMInferenceService(&plan, service, obs, true, logr.Discard())

	llmService := appliedLLMInferenceService(plan.GetToApply())
	if llmService == nil {
		t.Fatal("the LLMInferenceService was not planned")
	}
	env := envMap(llmService.Spec.Template.Containers[0].Env)
	if got := env[constants.EnvAIMAdapterMode]; got != string(aimv1alpha1.AdapterModeDynamic) {
		t.Errorf("%s = %q, want %q", constants.EnvAIMAdapterMode, got, aimv1alpha1.AdapterModeDynamic)
	}
	if env[constants.EnvAIMAdapterRefreshInterval] == "" {
		t.Errorf("dynamic mode must set %s; env = %#v", constants.EnvAIMAdapterRefreshInterval, env)
	}
}
