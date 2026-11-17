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
	"strings"
	"testing"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

var testLLMSupport = serving.LLMInferenceServiceSupport{Enabled: true, KServeNamespace: "kserve-system"}

func llmTestService() *aimv1alpha1.AIMService {
	return &aimv1alpha1.AIMService{
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
func llmTestFetch(service *aimv1alpha1.AIMService, profile *aimv1alpha2.AIMClusterProfile) ServiceFetchResult {
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
		{"auto resolves to InferenceService", aimv1alpha1.ServingAPIAuto, "", aimv1alpha1.ServingAPIInferenceService},
		{"unset resolves to InferenceService", "", "", aimv1alpha1.ServingAPIInferenceService},
		{"explicit LLMInferenceService", aimv1alpha1.ServingAPILLMInferenceService, "", aimv1alpha1.ServingAPILLMInferenceService},
		{"recorded API is sticky", aimv1alpha1.ServingAPIAuto, aimv1alpha1.ServingAPILLMInferenceService, aimv1alpha1.ServingAPILLMInferenceService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &aimv1alpha1.AIMService{
				Spec:   aimv1alpha1.AIMServiceSpec{ServingAPI: tc.spec},
				Status: aimv1alpha1.AIMServiceStatus{ServingAPI: tc.recorded},
			}
			if got := resolveServingAPI(service); got != tc.want {
				t.Fatalf("resolveServingAPI() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateServingAPI(t *testing.T) {
	autoscaled := llmTestService()
	autoscaled.Spec.MaxReplicas = ptr.To[int32](3)

	for _, tc := range []struct {
		name       string
		service    *aimv1alpha1.AIMService
		servingAPI aimv1alpha1.AIMServingAPI
		support    serving.LLMInferenceServiceSupport
		wantErr    string
	}{
		{"InferenceService needs no support", llmTestService(), aimv1alpha1.ServingAPIInferenceService, serving.LLMInferenceServiceSupport{}, ""},
		{"supported LLMInferenceService", llmTestService(), aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport, ""},
		{"missing CRDs", llmTestService(), aimv1alpha1.ServingAPILLMInferenceService, serving.LLMInferenceServiceSupport{}, "LLMInferenceService support"},
		{"autoscaling is supported", autoscaled, aimv1alpha1.ServingAPILLMInferenceService, testLLMSupport, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServingAPI(tc.service, tc.servingAPI, tc.support)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlanResources_llmInferenceServiceWaitsForNamespacedProjection(t *testing.T) {
	service := llmTestService()
	profile := llmTestClusterProfile()
	reconciler := &ProfileServiceReconciler{LLMInferenceService: testLLMSupport}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service}
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
	service *aimv1alpha1.AIMService,
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
			if got := llmInferenceServiceComponentHealth(tc.fetch); got.State != tc.wantState {
				t.Fatalf("state = %q (%s), want %q", got.State, got.Message, tc.wantState)
			}
		})
	}
}

func TestComposeState_llmInferenceServiceWithoutSupportIsInvalid(t *testing.T) {
	service := llmTestService()
	fetch := llmTestFetch(service, llmTestClusterProfile())
	fetch.servesLLM = false
	reconciler := &ProfileServiceReconciler{}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service}

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

func autoscaledLLMObservation(t *testing.T) (*aimv1alpha1.AIMService, ServiceObservation) {
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
			service:    service,
			servingAPI: aimv1alpha1.ServingAPILLMInferenceService,
			servesLLM:  true,
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
		case "ScaledObject":
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

func TestPlanAutoscaling_fixedReplicasPlansNothing(t *testing.T) {
	_, obs := autoscaledLLMObservation(t)
	fixed := llmTestService()
	obs.service = fixed
	if objects := planAutoscaling(context.Background(), fixed, obs, nil); len(objects) != 0 {
		t.Fatalf("planAutoscaling() = %d objects, want none for a fixed-replica service", len(objects))
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

func llmAdapterObservation(t *testing.T) (*aimv1alpha1.AIMService, ServiceObservation) {
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
