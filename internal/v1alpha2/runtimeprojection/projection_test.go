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

package runtimeprojection

import (
	"testing"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

// makeClusterProfile returns a deployable, cache-free cluster profile that the
// shared builder can turn into a complete namespace ServingRuntime.
func makeClusterProfile(name string) *aimv1alpha2.AIMClusterProfile {
	return &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            "qwen/qwen3-32b",
				ModelId:          "qwen/qwen3-32b-fp8",
				Engine:           "vllm",
				Metric:           aimv1alpha1.AIMMetric("latency"),
				Precision:        aimv1alpha1.AIMPrecision("fp8"),
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha1.AcceleratorType("gpu"),
				AcceleratorCount: 1,
				Image:            "ghcr.io/aim/qwen3-32b:1.0.0",
			},
		},
	}
}

// makeNamespaceProfile returns a deployable, cache-free namespace AIMProfile
// (the Reduced-mode backing that has no eager per-profile ServingRuntime) that
// the shared builder can turn into a complete namespaced ServingRuntime.
func makeNamespaceProfile(name, namespace string) *aimv1alpha2.AIMProfile {
	return &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            "qwen/qwen3-0.6b",
				ModelId:          "qwen/qwen3-0.6b",
				Engine:           "vllm",
				Metric:           aimv1alpha1.AIMMetric("latency"),
				Precision:        aimv1alpha1.AIMPrecision("bf16"),
				AcceleratorType:  aimv1alpha1.AcceleratorType("cpu"),
				AcceleratorCount: 0,
				Image:            "ghcr.io/aim/qwen3-0.6b:1.0.0",
			},
		},
	}
}

// testNamespace is the consumer namespace every fixture ISVC lives in.
const testNamespace = "team-a"

// makeISVC builds a native KServe InferenceService that references runtimeName
// (empty leaves predictor.model.runtime unset).
func makeISVC(runtimeName string) *servingv1beta1.InferenceService {
	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: testNamespace},
	}
	if runtimeName != "" {
		isvc.Spec.Predictor.Model = &servingv1beta1.ModelSpec{
			ModelFormat: servingv1beta1.ModelFormat{Name: "huggingface"},
			Runtime:     ptr.To(runtimeName),
		}
	}
	return isvc
}

func TestDesiredFor_NoRuntimeReference(t *testing.T) {
	got, err := DesiredFor(makeISVC(""), ProjectionState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime != nil {
		t.Errorf("expected nothing materialized when no ServingRuntime or ClusterServingRuntime is referenced, got %v", got.Runtime)
	}
}

func TestDesiredFor_ForeignRuntimeNameLeftAlone(t *testing.T) {
	// A name outside the reserved aim- prefix is hand-authored; never touched,
	// even when a profile happens to resolve.
	state := ProjectionState{ManagedRuntimeProfile: makeClusterProfile("qwen3-32b")}
	got, err := DesiredFor(makeISVC("my-own-runtime"), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime != nil {
		t.Errorf("expected foreign ServingRuntime name to be left alone, got %v", got.Runtime)
	}
}

func TestDesiredFor_NamespaceRuntimeComplete(t *testing.T) {
	// Same-namespace profile (or a prior shadow): the KServe ServingRuntime or
	// ClusterServingRuntime reference already resolves to a complete namespaced
	// ServingRuntime, so no shadow is needed.
	state := ProjectionState{
		NamespaceRuntimeComplete: true,
		ManagedRuntimeProfile:    makeClusterProfile("qwen3-32b"),
	}
	got, err := DesiredFor(makeISVC(serving.RuntimeName("qwen3-32b")), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime != nil {
		t.Errorf("expected nothing when a complete namespaced ServingRuntime exists, got %v", got.Runtime)
	}
}

func TestDesiredFor_ManagedClusterRuntimeMaterializesNamespaceRuntime(t *testing.T) {
	profile := makeClusterProfile("qwen3-32b")
	runtimeName := serving.RuntimeName(profile.Name)
	state := ProjectionState{ManagedRuntimeProfile: profile}

	got, err := DesiredFor(makeISVC(runtimeName), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime == nil || got.ConfigMap == nil {
		t.Fatalf("expected a ServingRuntime and ConfigMap, got servingRuntime=%v configMap=%v", got.Runtime, got.ConfigMap)
	}
	if got.Owner == nil || got.Owner.GetName() != profile.Name {
		t.Fatalf("expected owner to be the backing cluster profile %q, got %v", profile.Name, got.Owner)
	}

	// Materialized under the reserved aim- prefix, in the ISVC's namespace.
	if got.Runtime.Name != runtimeName {
		t.Errorf("ServingRuntime name = %q, want %q", got.Runtime.Name, runtimeName)
	}
	if got.Runtime.Namespace != "team-a" {
		t.Errorf("ServingRuntime namespace = %q, want team-a", got.Runtime.Namespace)
	}
	if got.ConfigMap.Name != runtimeName || got.ConfigMap.Namespace != "team-a" {
		t.Errorf("configMap = %s/%s, want team-a/%s", got.ConfigMap.Namespace, got.ConfigMap.Name, runtimeName)
	}

	// Complete: the ServingRuntime mounts the colocated profile ConfigMap.
	if !runtimeMountsConfigMap(got.Runtime, runtimeName) {
		t.Errorf("ServingRuntime does not mount colocated ConfigMap %q: volumes=%v", runtimeName, got.Runtime.Spec.Volumes)
	}

	// Regression guard: a per-profile ServingRuntime keeps autoSelect off so
	// native KServe auto-selection is never made ambiguous by multiple
	// ServingRuntime objects sharing the same model format.
	if runtimeAutoSelect(got.Runtime) {
		t.Errorf("expected per-profile ServingRuntime to keep autoSelect=false, got %v", got.Runtime.Spec.SupportedModelFormats)
	}
}

// TestDesiredFor_ModelSlugCompletion: a native InferenceService referencing the
// model-slug primary ServingRuntime (aim-<model-slug>, from the backing primary
// profile's aimId) materializes a complete namespaced ServingRuntime + ConfigMap
// under the referenced slug name (autoSelect off), owned by the backing primary
// profile — even though that profile's per-profile name is the distinct
// aim-<profile>-<hash>.
func TestDesiredFor_ModelSlugCompletion(t *testing.T) {
	profile := makeClusterProfile("qwen3-32b")
	slugRuntimeName := serving.ModelSlugRuntimeName(profile.Spec.AimId)
	// Sanity: the slug name must differ from the per-profile name, otherwise
	// this test would not actually exercise the slug seam.
	if slugRuntimeName == serving.RuntimeName(profile.Name) {
		t.Fatalf("test fixture broken: slug name %q equals per-profile name", slugRuntimeName)
	}
	state := ProjectionState{ManagedRuntimeProfile: profile}

	got, err := DesiredFor(makeISVC(slugRuntimeName), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime == nil || got.ConfigMap == nil {
		t.Fatalf("expected a ServingRuntime and ConfigMap, got servingRuntime=%v configMap=%v", got.Runtime, got.ConfigMap)
	}
	// Owned/correlated to the backing primary profile (GC'd with it).
	if got.Owner == nil || got.Owner.GetName() != profile.Name {
		t.Fatalf("expected owner to be the backing primary profile %q, got %v", profile.Name, got.Owner)
	}
	// Materialized under the REFERENCED slug name (not aim-<profile.Name>), in
	// the ISVC's namespace, with a same-named colocated ConfigMap it mounts.
	if got.Runtime.Name != slugRuntimeName || got.Runtime.Namespace != testNamespace {
		t.Errorf("ServingRuntime = %s/%s, want %s/%s", got.Runtime.Namespace, got.Runtime.Name, testNamespace, slugRuntimeName)
	}
	if got.ConfigMap.Name != slugRuntimeName || got.ConfigMap.Namespace != testNamespace {
		t.Errorf("configMap = %s/%s, want %s/%s", got.ConfigMap.Namespace, got.ConfigMap.Name, testNamespace, slugRuntimeName)
	}
	if !runtimeMountsConfigMap(got.Runtime, slugRuntimeName) {
		t.Errorf("ServingRuntime does not mount colocated ConfigMap %q: volumes=%v", slugRuntimeName, got.Runtime.Spec.Volumes)
	}
	// The model-slug primary keeps autoSelect off (mirrors the eager slug
	// ServingRuntime, planNamespaceModelSlugRuntime): the shared model format
	// would otherwise collide across models in KServe auto-selection.
	if runtimeAutoSelect(got.Runtime) {
		t.Errorf("expected model-slug ServingRuntime to keep autoSelect=false, got %v", got.Runtime.Spec.SupportedModelFormats)
	}
	// The correlator label still points to the backing primary profile even
	// though the object is named after the slug.
	if got.Runtime.Labels[constants.LabelProfile] != profile.Name {
		t.Errorf("profile correlator label = %q, want %q", got.Runtime.Labels[constants.LabelProfile], profile.Name)
	}
}

// TestDesiredFor_NamespaceProfileBacking: a namespace AIMProfile (which gets NO
// eager per-profile ServingRuntime under Reduced) resolved via the annotation
// fast-path materializes a complete namespaced ServingRuntime plus colocated
// ConfigMap named via RuntimeName, owned by the namespace profile. With no
// reversible name-lookup fallback, the annotation is the only cross-scope path
// for a not-yet-created ServingRuntime.
func TestDesiredFor_NamespaceProfileBacking(t *testing.T) {
	profile := makeNamespaceProfile("qwen3-0-6b", testNamespace)
	runtimeName := serving.RuntimeName(profile.Name)

	state := ProjectionState{AnnotatedProfile: profile}
	got, err := DesiredFor(makeISVC(runtimeName), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime == nil || got.ConfigMap == nil {
		t.Fatalf("expected a ServingRuntime and ConfigMap, got servingRuntime=%v configMap=%v", got.Runtime, got.ConfigMap)
	}
	// Owned by the namespace profile (so it GCs with it).
	if _, ok := got.Owner.(*aimv1alpha2.AIMProfile); !ok {
		t.Fatalf("expected owner to be a namespace AIMProfile, got %T", got.Owner)
	}
	if got.Owner.GetName() != profile.Name {
		t.Fatalf("owner name = %q, want %q", got.Owner.GetName(), profile.Name)
	}
	// Materialized under the reserved prefix in the ISVC's namespace,
	// mounting the colocated ConfigMap of the same name.
	if got.Runtime.Name != runtimeName || got.Runtime.Namespace != testNamespace {
		t.Errorf("ServingRuntime = %s/%s, want %s/%s", got.Runtime.Namespace, got.Runtime.Name, testNamespace, runtimeName)
	}
	if !runtimeMountsConfigMap(got.Runtime, runtimeName) {
		t.Errorf("ServingRuntime does not mount colocated ConfigMap %q: volumes=%v", runtimeName, got.Runtime.Spec.Volumes)
	}
}

// TestDesiredFor_ResolutionOrder exercises the backing-profile resolution order
// at the seam: a managed ClusterServingRuntime wins, then the AIMService-stamped
// annotation. There is no name-lookup fallback (hashed per-profile names are not
// reversible).
func TestDesiredFor_ResolutionOrder(t *testing.T) {
	managed := makeClusterProfile("from-managed")
	annotated := makeClusterProfile("from-annotation")

	tests := []struct {
		name      string
		state     ProjectionState
		wantOwner string // "" means nothing materialized
	}{
		{
			name: "managed ClusterServingRuntime wins over annotation",
			state: ProjectionState{
				ManagedRuntimeProfile: managed,
				AnnotatedProfile:      annotated,
			},
			wantOwner: managed.Name,
		},
		{
			name:      "annotation resolves when no managed ClusterServingRuntime",
			state:     ProjectionState{AnnotatedProfile: annotated},
			wantOwner: annotated.Name,
		},
		{
			name:      "nothing resolves",
			state:     ProjectionState{},
			wantOwner: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The referenced ServingRuntime or ClusterServingRuntime name must
			// match the profile the order selects; otherwise the mismatch guard
			// short-circuits.
			expected := tc.state.BackingProfile()
			runtimeName := serving.RuntimeNamePrefix + "unresolved"
			if expected != nil {
				runtimeName = serving.RuntimeName(expected.GetName())
			}

			got, err := DesiredFor(makeISVC(runtimeName), tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantOwner == "" {
				if got.Runtime != nil {
					t.Fatalf("expected nothing materialized, got owner %v", got.Owner)
				}
				return
			}
			if got.Owner == nil || got.Owner.GetName() != tc.wantOwner {
				t.Fatalf("resolved owner = %v, want %q", got.Owner, tc.wantOwner)
			}
			if got.Runtime.Name != runtimeName {
				t.Errorf("ServingRuntime name = %q, want %q", got.Runtime.Name, runtimeName)
			}
		})
	}
}

// TestDesiredFor_NameMismatchLeftAlone guards against shadowing a
// ServingRuntime or ClusterServingRuntime name the resolved profile does not
// actually project — neither its per-profile name (aim-<profile.Name>) nor its
// Reduced/Both-mode model-slug name (aim-<model-slug>).
func TestDesiredFor_NameMismatchLeftAlone(t *testing.T) {
	profile := makeClusterProfile("qwen3-32b")
	state := ProjectionState{ManagedRuntimeProfile: profile}
	// aim-other is neither aim-qwen3-32b (per-profile) nor aim-qwen-qwen3-32b
	// (the slug derived from aimId qwen/qwen3-32b), so it must be declined.
	mismatch := serving.RuntimeName("other")
	if mismatch == serving.RuntimeName(profile.Name) || mismatch == serving.ModelSlugRuntimeName(profile.Spec.AimId) {
		t.Fatalf("test fixture broken: %q unexpectedly matches a projected name", mismatch)
	}
	got, err := DesiredFor(makeISVC(mismatch), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime != nil {
		t.Errorf("expected nothing when the resolved profile does not own the referenced name, got %v", got.Runtime)
	}
}

// TestDesiredFor_ProfileOwnedCacheMounted asserts a ready profile-owned cache
// contributes its PVC mount to the materialized ServingRuntime.
func TestDesiredFor_ProfileOwnedCacheMounted(t *testing.T) {
	profile := makeClusterProfile("qwen3-32b")
	runtimeName := serving.RuntimeName(profile.Name)
	state := ProjectionState{
		ManagedRuntimeProfile: profile,
		Cache: &aimv1alpha2.AIMProfileCache{
			Status: aimv1alpha2.AIMProfileCacheStatus{
				Status: constants.AIMStatusReady,
				Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
					"weights": {
						Name:                  "weights",
						Status:                constants.AIMStatusReady,
						PersistentVolumeClaim: "weights-pvc",
						MountPoint:            "/workspace/cache/qwen",
					},
				},
			},
		},
	}

	got, err := DesiredFor(makeISVC(runtimeName), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Runtime == nil {
		t.Fatalf("expected a ServingRuntime to be materialized")
	}
	if !runtimeMountsPVC(got.Runtime, "weights-pvc") {
		t.Errorf("ServingRuntime does not mount the profile-owned cache PVC: volumes=%v", got.Runtime.Spec.Volumes)
	}
}

func TestDesiredForRuntime_UsesRuntimeKeyAndExistingOwnerFirst(t *testing.T) {
	existing := makeClusterProfile("existing-owner")
	managed := makeClusterProfile("managed-owner")
	annotated := makeNamespaceProfile("annotated-owner", "other-namespace")
	runtimeName := serving.ModelSlugRuntimeName(existing.Spec.AimId)

	got, err := DesiredForRuntime("runtime-namespace", runtimeName, ProjectionState{
		ExistingShadowProfile: existing,
		ManagedRuntimeProfile: managed,
		AnnotatedProfile:      annotated,
	})
	if err != nil {
		t.Fatalf("DesiredForRuntime() error = %v", err)
	}
	if got.Runtime == nil || got.ConfigMap == nil {
		t.Fatalf("expected ServingRuntime-keyed siblings, got servingRuntime=%v configMap=%v", got.Runtime, got.ConfigMap)
	}
	if got.Runtime.Namespace != "runtime-namespace" || got.Runtime.Name != runtimeName {
		t.Fatalf("ServingRuntime key = %s/%s, want runtime-namespace/%s", got.Runtime.Namespace, got.Runtime.Name, runtimeName)
	}
	if got.Owner != existing {
		t.Fatalf("owner = %v, want existing sibling owner %q", got.Owner, existing.Name)
	}
}

func TestDesiredForRuntime_DRAVariantDropsDevicePluginGPU(t *testing.T) {
	profile := makeClusterProfile("dra-profile")
	profile.Status.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("8"),
			corev1.ResourceCPU: resource.MustParse("32"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("8"),
		},
	}
	profile.Status.ResolvedNodeAffinity = &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
				Key: "feature.node.kubernetes.io/aim-accelerator.MI355X", Operator: corev1.NodeSelectorOpExists,
			}}}},
		},
	}
	runtimeName := serving.DRARuntimeName(profile.Name)

	got, err := DesiredForRuntime("ns", runtimeName, ProjectionState{AnnotatedProfile: profile})
	if err != nil {
		t.Fatalf("DesiredForRuntime() error = %v", err)
	}
	if got.Runtime == nil || got.ConfigMap == nil || got.Runtime.Name != runtimeName || got.ConfigMap.Name != runtimeName {
		t.Fatalf("expected the DRA ServingRuntime and ConfigMap named %s, got %+v", runtimeName, got)
	}
	resources := got.Runtime.Spec.Containers[0].Resources
	if _, ok := resources.Requests[corev1.ResourceName(constants.AMDGPUResourceName)]; ok {
		t.Errorf("DRA runtime requests %s: %v", constants.AMDGPUResourceName, resources.Requests)
	}
	if _, ok := resources.Limits[corev1.ResourceName(constants.AMDGPUResourceName)]; ok {
		t.Errorf("DRA runtime limits %s: %v", constants.AMDGPUResourceName, resources.Limits)
	}
	if cpu := resources.Requests[corev1.ResourceCPU]; cpu.String() != "32" {
		t.Errorf("DRA runtime CPU request = %s, want the profile's 32", cpu.String())
	}
	if got.Runtime.Spec.Affinity != nil {
		t.Errorf("DRA runtime has node affinity %+v; the ResourceClaim decides placement", got.Runtime.Spec.Affinity)
	}
	if _, ok := profile.Status.Resources.Requests[corev1.ResourceName(constants.AMDGPUResourceName)]; !ok {
		t.Error("DesiredForRuntime modified the profile's status resources")
	}

	devicePlugin, err := DesiredForRuntime("ns", serving.RuntimeName(profile.Name), ProjectionState{AnnotatedProfile: profile})
	if err != nil || devicePlugin.Runtime == nil {
		t.Fatalf("per-profile runtime: %v, %v", devicePlugin, err)
	}
	if _, ok := devicePlugin.Runtime.Spec.Containers[0].Resources.Requests[corev1.ResourceName(constants.AMDGPUResourceName)]; !ok {
		t.Error("the per-profile runtime lost its device-plugin GPU request")
	}
}

func runtimeMountsConfigMap(runtime *kservev1alpha1.ServingRuntime, configMapName string) bool {
	for _, v := range runtime.Spec.Volumes {
		if v.ConfigMap != nil && v.ConfigMap.Name == configMapName {
			return true
		}
	}
	return false
}

func runtimeMountsPVC(runtime *kservev1alpha1.ServingRuntime, claimName string) bool {
	for _, v := range runtime.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claimName {
			return true
		}
	}
	return false
}

// runtimeAutoSelect reports whether any of the ServingRuntime's
// supportedModelFormats advertises autoSelect=true. Every projected
// ServingRuntime keeps it off, so this is a regression guard that projection
// never turns it on.
func runtimeAutoSelect(runtime *kservev1alpha1.ServingRuntime) bool {
	for _, f := range runtime.Spec.SupportedModelFormats {
		if f.AutoSelect != nil && *f.AutoSelect {
			return true
		}
	}
	return false
}
