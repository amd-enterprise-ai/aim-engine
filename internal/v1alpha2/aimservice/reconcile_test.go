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
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	testProfileA    = "profile-a"
	testServiceName = "svc"
)

func TestGenerateProfileCacheName_Deterministic(t *testing.T) {
	a, err := GenerateProfileCacheName("my-profile", "ns-alpha")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateProfileCacheName("my-profile", "ns-alpha")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a != b {
		t.Fatalf("cache name should be deterministic: %q vs %q", a, b)
	}
}

func TestGenerateProfileCacheName_NamespaceScoped(t *testing.T) {
	same, _ := GenerateProfileCacheName("profile-x", "ns-a")
	other, _ := GenerateProfileCacheName("profile-x", "ns-b")
	if same == other {
		t.Fatalf("profile cache names should differ when namespace differs")
	}
}

func TestComposeState_NoProfile(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	fetch := ServiceFetchResult{service: service}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)

	if obs.resolvedProfileSpec != nil {
		t.Errorf("expected no resolved profile spec when no profile fetched")
	}
	if obs.hasModelSources {
		t.Errorf("hasModelSources should be false when no profile fetched")
	}
	if obs.profileCacheReady {
		t.Errorf("profileCacheReady should be false when no profile fetched")
	}
}

func TestComposeState_NamespaceProfileResolved(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
	}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)

	if obs.profileName != testProfileA {
		t.Errorf("profile name not resolved: %q", obs.profileName)
	}
	if obs.profileScope != aimv1alpha1.AIMResolutionScopeNamespace {
		t.Errorf("profile scope should be namespace: %v", obs.profileScope)
	}
	if obs.resolvedProfileSpec == nil {
		t.Errorf("resolved profile spec should be populated")
	}
}

func TestComposeState_ClusterProfileFallback(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-profile"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	fetch := ServiceFetchResult{
		service:        service,
		clusterProfile: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: clusterProfile},
	}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)
	if obs.profileScope != aimv1alpha1.AIMResolutionScopeCluster {
		t.Errorf("profile scope should be cluster: %v", obs.profileScope)
	}
	if obs.profileName != "cluster-profile" {
		t.Errorf("cluster profile name not resolved: %q", obs.profileName)
	}
}

func TestBuildInferenceServiceFromProfile_BasicShape(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileStatus := &aimv1alpha2.AIMProfileStatus{
		Status: constants.AIMStatusReady,
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}

	r := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
		},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: profileStatus,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	r.composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames returned error: %v", obs.configErr)
	}

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("buildInferenceServiceFromProfile returned nil")
	}

	if isvc.Namespace != "ns" {
		t.Errorf("unexpected namespace: %q", isvc.Namespace)
	}
	if isvc.Labels[constants.LabelService] != testServiceName {
		t.Errorf("missing service label: %v", isvc.Labels)
	}
	if isvc.Labels[constants.LabelProfile] != testProfileA {
		t.Errorf("missing profile label: %v", isvc.Labels)
	}

	if len(isvc.Spec.Predictor.Containers) == 0 {
		t.Fatalf("expected at least one container")
	}
	container := isvc.Spec.Predictor.Containers[0]
	if container.Image != profileSpec.Image {
		t.Errorf("container image mismatch: got %q want %q", container.Image, profileSpec.Image)
	}

	// Profile volume + mount should be present.
	foundProfileVol := false
	for _, v := range isvc.Spec.Predictor.Volumes {
		if v.Name == profileVolumePrefix {
			foundProfileVol = true
		}
	}
	if !foundProfileVol {
		t.Errorf("profile volume %q not found on ISVC", profileVolumePrefix)
	}

	foundProfileMount := false
	for _, m := range container.VolumeMounts {
		if m.Name == profileVolumePrefix {
			foundProfileMount = true
		}
	}
	if !foundProfileMount {
		t.Errorf("profile volume mount %q not found on container", profileVolumePrefix)
	}

	// AIM_PROFILE_ID env var should be present and reference custom/<aimId>/...
	foundEnv := false
	for _, e := range container.Env {
		if e.Name == constants.EnvAIMProfileID {
			foundEnv = true
			if e.Value == "" {
				t.Errorf("AIM_PROFILE_ID env value is empty")
			}
		}
	}
	if !foundEnv {
		t.Errorf("expected AIM_PROFILE_ID env var on container")
	}

	// Resource requirements from profile status should be propagated.
	if container.Resources.Requests.Cpu().Cmp(resource.MustParse("8")) != 0 {
		t.Errorf("CPU requests not propagated: %v", container.Resources.Requests.Cpu())
	}
}

func TestBuildInferenceServiceFromProfile_NilSpecReturnsNil(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{service: service},
	}
	if isvc := buildInferenceServiceFromProfile(service, obs); isvc != nil {
		t.Fatalf("expected nil ISVC when profile spec is nil")
	}
}

func TestGetConfigHealth_NoErrorReturnsEmpty(t *testing.T) {
	obs := ServiceObservation{}
	if got := obs.getConfigHealth(); got.Component != "" {
		t.Errorf("expected empty ComponentHealth when configErr is nil, got %+v", got)
	}
}

func TestGetConfigHealth_SurfacesInvalidSpec(t *testing.T) {
	obs := ServiceObservation{
		configErr: errors.New("assemble profile YAML: invalid engineArgs"),
	}
	health := obs.getConfigHealth()

	if health.Component != "ProfileConfig" {
		t.Errorf("unexpected component: %q", health.Component)
	}
	if health.State != constants.AIMStatusFailed {
		t.Errorf("expected Failed state, got %q", health.State)
	}
	if health.DependencyType != controllerutils.DependencyTypeUpstream {
		t.Errorf("expected upstream dependency type, got %q", health.DependencyType)
	}
	if len(health.Errors) != 1 {
		t.Fatalf("expected a single wrapped error, got %d", len(health.Errors))
	}
	if categorized := controllerutils.CategorizeError(health.Errors[0]); categorized.Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("expected InvalidSpec category, got %v", categorized.Category())
	}
}

func TestGetComponentHealth_IncludesConfigAndRouteEntries(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{service: service},
		configErr:          errors.New("bad profile"),
	}
	entries := obs.GetComponentHealth(context.Background(), nil)
	var haveConfig bool
	for _, e := range entries {
		if e.Component == "ProfileConfig" {
			haveConfig = true
		}
		if e.Component == "HTTPRoute" {
			t.Errorf("HTTPRoute entry should be suppressed when routing is disabled")
		}
	}
	if !haveConfig {
		t.Errorf("expected ProfileConfig entry to be emitted when configErr is set")
	}
}

func TestPlanProfileCache_SkipsWhenExisting(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: &aimv1alpha2.AIMProfileCache{}},
		},
		profileName:         testProfileA,
		resolvedProfileSpec: sampleProfileSpec(),
	}
	if got := planProfileCache(service, obs); got != nil {
		t.Fatalf("planProfileCache should return nil when cache already exists, got %+v", got)
	}
}

// buildTestISVC is a small helper that returns a freshly-built ISVC for a
// service spec with a ready profile. It keeps the autoscaling tests short
// and focused on the one dimension each case is probing.
func buildTestISVC(t *testing.T, spec aimv1alpha1.AIMServiceSpec) *servingv1beta1.InferenceService {
	t.Helper()
	spec.Profile = &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA}

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec:       spec,
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames error: %v", obs.configErr)
	}
	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	return isvc
}

// TestBuildInferenceServiceFromProfile_Replicas_DefaultsToOne guards against
// the v1alpha2 builder silently dropping MaxReplicas (previously it only set
// MinReplicas, leaving MaxReplicas=0 which KServe interprets as "no cap").
func TestBuildInferenceServiceFromProfile_Replicas_DefaultsToOne(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 1 {
		t.Errorf("MinReplicas: want *1, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 1 {
		t.Errorf("MaxReplicas: want 1, got %d", got)
	}
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassNone {
		t.Errorf("autoscaler class: want %q, got %q", constants.AutoscalerClassNone, got)
	}
}

func TestBuildInferenceServiceFromProfile_Replicas_FixedDisablesHPA(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{
		Replicas: ptr.To(int32(3)),
	})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 3 {
		t.Errorf("MinReplicas: want *3, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 3 {
		t.Errorf("MaxReplicas: want 3 (HPA disabled, min==max), got %d", got)
	}
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassNone {
		t.Errorf("autoscaler class: want %q (fixed replicas disables HPA), got %q",
			constants.AutoscalerClassNone, got)
	}
}

func TestBuildInferenceServiceFromProfile_Replicas_AutoScalingEnablesKEDA(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{
		MinReplicas: ptr.To(int32(2)),
		MaxReplicas: ptr.To(int32(5)),
	})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 2 {
		t.Errorf("MinReplicas: want *2, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 5 {
		t.Errorf("MaxReplicas: want 5, got %d", got)
	}
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassKeda {
		t.Errorf("autoscaler class: want %q (min/max triggers KEDA), got %q",
			constants.AutoscalerClassKeda, got)
	}
}

// TestBuildInferenceServiceFromProfile_FrameworkEnvVarsWinOverProfile verifies
// that a profile author cannot override AIM_PROFILE_ID via ContainerEnv. This
// is essential because the framework's value is the only one that actually
// resolves to the projected ConfigMap path.
func TestBuildInferenceServiceFromProfile_FrameworkEnvVarsWinOverProfile(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ContainerEnv = []corev1.EnvVar{
		{Name: constants.EnvAIMProfileID, Value: "hijacked"},
		{Name: "PROFILE_ONLY", Value: "kept"},
	}

	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	env := envMap(isvc.Spec.Predictor.Containers[0].Env)

	if env[constants.EnvAIMProfileID] == "hijacked" {
		t.Errorf("profile.ContainerEnv must not be able to override %s", constants.EnvAIMProfileID)
	}
	if env[constants.EnvAIMProfileID] == "" {
		t.Errorf("framework env var %s is missing", constants.EnvAIMProfileID)
	}
	if env["PROFILE_ONLY"] != "kept" {
		t.Errorf("non-overlapping profile env var should survive: got %q", env["PROFILE_ONLY"])
	}
}

// TestBuildInferenceServiceFromProfile_UserOverridesWinOverFramework verifies
// the final layer of precedence: service.Spec.ProfileOverrides.ContainerEnv
// is explicit user intent and is allowed to replace framework values.
func TestBuildInferenceServiceFromProfile_UserOverridesWinOverFramework(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ContainerEnv: []corev1.EnvVar{
					{Name: constants.EnvAIMProfileID, Value: "user-override"},
				},
			},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	env := envMap(isvc.Spec.Predictor.Containers[0].Env)

	if got := env[constants.EnvAIMProfileID]; got != "user-override" {
		t.Errorf("ProfileOverrides.ContainerEnv should take precedence over framework var, got %q", got)
	}
}

func envMap(vars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(vars))
	for _, e := range vars {
		m[e.Name] = e.Value
	}
	return m
}

func TestPlanProfileCache_CreatesSharedCache(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha2.AIMModelSource{{
		ModelID:   "org/model",
		SourceURI: "hf://org/model",
	}}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec: profileSpec,
	}

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned")
	}
	if cache.Namespace != "ns" {
		t.Errorf("unexpected namespace: %q", cache.Namespace)
	}
	if cache.Spec.ProfileName != testProfileA {
		t.Errorf("unexpected profile name on cache spec: %q", cache.Spec.ProfileName)
	}
	if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
		t.Errorf("expected shared cache mode, got %q", cache.Spec.Mode)
	}
	if cache.Labels[constants.LabelService] != testServiceName {
		t.Errorf("expected service label on cache, got %v", cache.Labels)
	}
}
