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

package aimprofileset

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const testSourceAccelerator = "MI300X"

const definitionYAML = `aim_id: architecture
model_id: weights/model
metadata:
  engine: vllm
  metric: latency
  type: optimized
  accelerator_model: MI300X
  accelerator_vendor: amd
  accelerator_type: gpu
  accelerator_count: 1
  accelerator_partitioning_mode: unpartitioned
  precision: bf16
  primary: true
  runtime_extension: keep-me
engine_args:
  tensor-parallel-size: 1
  runtime-flag: true
env_vars:
  MODEL_SETTING: present
runtime_extension:
  nested: retained
`

func sourceFixture(t *testing.T, namespace, name string) (sourceModel, *corev1.ConfigMap) {
	t.Helper()
	spec := aimv1alpha1.AIMModelSpec{
		Image:              "registry.example/aim-model:1.2.3",
		ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "registry-creds"}},
		ServiceAccountName: "model-account",
	}
	meta := metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(name + "-uid")}
	var model sourceModel
	if namespace == "" {
		m := &aimv1alpha2.AIMClusterModel{ObjectMeta: meta, Spec: spec}
		model = sourceModel{m, &m.Spec, &m.Status, "AIMClusterModel"}
		namespace = constants.GetOperatorNamespace()
	} else {
		m := &aimv1alpha2.AIMModel{ObjectMeta: meta, Spec: spec}
		model = sourceModel{m, &m.Spec, &m.Status, "AIMModel"}
	}
	model.status.Status = constants.AIMStatusNotAvailable
	cacheName, err := aimmodel.DiscoveryCacheName(name)
	if err != nil {
		t.Fatal(err)
	}
	cm, err := aimmodel.BuildDiscoveryCacheConfigMap(aimmodel.DiscoveryCacheInput{
		Name: cacheName, Namespace: namespace, ModelUID: string(meta.UID), ModelName: name,
		SourceImage: spec.Image, AimID: "architecture", BaseImage: "registry.example/aim-base:1.2",
		SourceSpecHash: aimmodel.ComputeDiscoverySpecHash(spec.Image, spec.ImagePullSecrets, spec.ServiceAccountName, aimmodel.DiscoveryCommandVersion),
		Profiles:       map[string][]byte{"architecture/gpu.yaml": []byte(definitionYAML)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cm.OwnerReferences = []metav1.OwnerReference{aimmodel.CacheOwnerReference(aimv1alpha2.GroupVersion.String(), model.kind, name, string(meta.UID))}
	return model, cm
}

func sourceClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	index := func(obj client.Object) []string { return []string{obj.GetAnnotations()[annotationProfileSetUID]} }
	modelIndex := func(obj client.Object) []string {
		return []string{obj.GetAnnotations()[constants.AimLabelDomain+"/model-uid"]}
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha2.AIMProfile{}, &aimv1alpha2.AIMClusterProfile{}, &aimv1alpha2.AIMProfileSet{}, &aimv1alpha2.AIMClusterProfileSet{}, &aimv1alpha2.AIMModel{}, &aimv1alpha2.AIMClusterModel{}).
		WithIndex(&aimv1alpha2.AIMProfile{}, ManagedProfileSetUIDIndexKey, index).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, ManagedProfileSetUIDIndexKey, index).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimmodel.ManagedModelUIDIndexKey, modelIndex).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, aimmodel.ManagedModelUIDIndexKey, modelIndex).
		WithObjects(objects...).Build()
}

func TestCatalogNamingPreservesLegacySourceRepresentation(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		for _, explicit := range []bool{false, true} {
			for _, emptyType := range []bool{false, true} {
				m, cm := sourceFixture(t, namespace, "model")
				if emptyType {
					key := aimprofile.FlattenProfilePath("architecture/gpu.yaml")
					cm.Data[key] = strings.ReplaceAll(cm.Data[key], "  type: optimized\n", "  type: \"\"\n")
				}
				c := sourceClient(t, m.object, cm)
				set := partitionSet(namespace)
				if explicit {
					set.Spec.SourceRef = &aimv1alpha1.ProfileSourceRef{Name: cm.Name}
				}
				var candidates []profileSetCandidate
				var err error
				if namespace == "" {
					candidates, err = loadClusterCandidates(context.Background(), c, c, &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec})
				} else {
					candidates, err = loadNamespaceCandidates(context.Background(), c, c, set)
				}
				if err != nil || len(candidates) != 1 {
					t.Fatalf("load: %d, %v", len(candidates), err)
				}
				// Reconstruct the pre-feature catalog/source representation, not
				// the new publication's naming hint. The old parser ignored vendor.
				// Type was copied unchanged; the profile CRD has no type default.
				catalog, err := aimprofile.ParseDiscoveryCatalog(cm)
				if err != nil {
					t.Fatal(err)
				}
				legacy := catalog.ToCandidates()[0]
				legacy.Spec.AcceleratorVendor = ""
				if !explicit {
					legacy.Name, err = utils.GenerateDerivedName([]string{"model", "gpu"}, utils.WithHashSource("model", "gpu"))
					if err != nil {
						t.Fatal(err)
					}
				}
				want, err := derivedProfileName(set.Name, legacy)
				if err != nil {
					t.Fatal(err)
				}
				got, err := derivedProfileName(set.Name, candidates[0].ProfileCopyCandidate)
				if err != nil || got != want {
					t.Fatalf("namespace=%q explicit=%v emptyType=%v: name=%s want=%s err=%v", namespace, explicit, emptyType, got, want, err)
				}
				if candidates[0].Spec.AcceleratorVendor != aimv1alpha1.AcceleratorVendorAMD {
					t.Fatal("legacy naming discarded vendor from the selected spec")
				}
				recreated := candidates[0].ProfileCopyCandidate
				recreated.SourceID = "new-incarnation/new-catalog-revision"
				if again, err := derivedProfileName(set.Name, recreated); err != nil || again != got {
					t.Fatal("source recreation renamed derived output")
				}
			}
		}
	}
}

func TestCatalogDerivationPreservesType(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		for _, explicit := range []bool{false, true} {
			for _, profileType := range []aimv1alpha1.AIMProfileType{
				"", aimv1alpha1.AIMProfileTypeOptimized, aimv1alpha1.AIMProfileTypeGeneral,
				aimv1alpha1.AIMProfileTypePreview, aimv1alpha1.AIMProfileTypeUnoptimized,
			} {
				t.Run(fmt.Sprintf("namespace=%s/explicit=%v/type=%s", namespace, explicit, profileType), func(t *testing.T) {
					m, cm := sourceFixture(t, namespace, "model")
					key := aimprofile.FlattenProfilePath("architecture/gpu.yaml")
					cm.Data[key] = strings.ReplaceAll(cm.Data[key], "  type: optimized\n", fmt.Sprintf("  type: %q\n", profileType))
					c := sourceClient(t, m.object, cm)
					set := partitionSet(namespace)
					if explicit {
						set.Spec.SourceRef = &aimv1alpha1.ProfileSourceRef{Name: cm.Name}
					}
					for _, selection := range []struct {
						name    string
						exact   aimv1alpha1.AIMProfileType
						minimum aimv1alpha1.AIMProfileTypeFloor
						match   bool
					}{
						{name: "no-tier-filter", match: true},
						{name: "any-tier", minimum: aimv1alpha1.AIMProfileTypeFloorAny, match: true},
						{name: "optimized-exact", exact: aimv1alpha1.AIMProfileTypeOptimized, match: profileType == aimv1alpha1.AIMProfileTypeOptimized},
						{name: "optimized-floor", minimum: aimv1alpha1.AIMProfileTypeFloorOptimized, match: profileType == aimv1alpha1.AIMProfileTypeOptimized},
						{name: "unoptimized-exact", exact: aimv1alpha1.AIMProfileTypeUnoptimized, match: profileType == aimv1alpha1.AIMProfileTypeUnoptimized},
						{name: "unoptimized-floor", minimum: aimv1alpha1.AIMProfileTypeFloorUnoptimized, match: true},
					} {
						t.Run(selection.name, func(t *testing.T) {
							set.Spec.Selector.Type = selection.exact
							set.Spec.Selector.MinimumType = selection.minimum
							var candidates []profileSetCandidate
							var err error
							clusterSet := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec}
							if namespace == "" {
								candidates, err = loadClusterCandidates(context.Background(), c, c, clusterSet)
							} else {
								candidates, err = loadNamespaceCandidates(context.Background(), c, c, set)
							}
							if err != nil {
								t.Fatal(err)
							}
							for _, candidate := range candidates {
								if candidate.Spec.Type != profileType {
									t.Errorf("candidate type = %q, want %q", candidate.Spec.Type, profileType)
								}
							}
							selected, err := resolveSelectedProfileSetCandidates(candidates, profileCopyRequest(set.Spec))
							if err != nil {
								t.Fatal(err)
							}
							var desired []desiredProfile
							if namespace == "" {
								desired, err = buildDesiredClusterProfiles(clusterSet, selected, nil)
							} else {
								desired, err = buildDesiredNamespaceProfiles(set, selected, nil)
							}
							if err != nil {
								t.Fatal(err)
							}
							want := 0
							if selection.match {
								want = 1
							}
							if len(desired) != want {
								t.Fatalf("derived profiles = %d, want %d", len(desired), want)
							}
							for _, profile := range desired {
								spec, _ := profileSpecCommon(profile.Object)
								if spec.Type != profileType {
									t.Errorf("derived type = %q, want %q", spec.Type, profileType)
								}
							}
						})
					}
				})
			}
		}
	}
}

func TestCatalogNamingSurvivesOptedInDerivationChains(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		for _, vendorOverride := range []aimv1alpha1.AcceleratorVendor{"", aimv1alpha1.AcceleratorVendorAMD} {
			m, cm := sourceFixture(t, namespace, "chain-model")
			c := sourceClient(t, m.object, cm)
			set := partitionSet(namespace)
			set.Spec.Overrides.AcceleratorVendor = vendorOverride
			sources, err := loadDefaultScope(context.Background(), c, namespace, namespace == "", set.Spec, string(set.UID))
			if err != nil || len(sources) != 1 {
				t.Fatalf("sources=%d, %v", len(sources), err)
			}
			source := sources[0].ProfileCopyCandidate
			set.Spec.Selector = aimv1alpha1.ProfileSelector{Engine: "vllm"}
			for _, setName := range []string{"first", "second", "third"} {
				set.Name = setName
				legacy := source
				legacy.LegacyCatalogNaming = false
				if source.LegacyCatalogNaming {
					legacy.Spec.AcceleratorVendor = ""
				}
				want, err := derivedProfileName(setName, legacy)
				if err != nil {
					t.Fatal(err)
				}
				var outputs []desiredProfile
				if namespace == "" {
					outputs, err = buildDesiredClusterProfiles(&aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec}, []aimprofile.ProfileCopyCandidate{source}, nil)
				} else {
					outputs, err = buildDesiredNamespaceProfiles(set, []aimprofile.ProfileCopyCandidate{source}, nil)
				}
				if err != nil || len(outputs) != 1 {
					t.Fatalf("outputs=%d, %v", len(outputs), err)
				}
				p := outputs[0].Object
				if p.GetName() != want {
					t.Fatalf("chain renamed output: %s, want %s", p.GetName(), want)
				}
				if (p.GetAnnotations()[aimprofile.AnnotationLegacyCatalogNaming] == legacyCatalogNamingValue) != (vendorOverride == "") {
					t.Fatal("historical vendor override lost from naming lineage")
				}
				spec, _ := profileSpecCommon(p)
				source = liveSourceCandidate(p, spec, aimv1alpha2.AIMProfileStatus{Version: "1.2.3"}, "AIMProfile").ProfileCopyCandidate
				source.YAMLContract = profileyaml.CanonicalContract(&spec)
				// Subsequent sets copy the produced resource without vendor overrides.
				set.Spec.Overrides.AcceleratorVendor = ""
			}
		}
	}
}

func TestDistinctDefinitionsWithSameLegacyNameFailClosed(t *testing.T) {
	m, cm := sourceFixture(t, "team", "model")
	c := sourceClient(t, m.object, cm)
	set := partitionSet("team")
	sources, err := loadNamespaceCandidates(context.Background(), c, c, set)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%d, %v", len(sources), err)
	}
	duplicate := sources[0].ProfileCopyCandidate
	duplicate.SourceID += "/distinct-definition"
	candidates := []aimprofile.ProfileCopyCandidate{sources[0].ProfileCopyCandidate, duplicate}
	if _, err := buildDesiredNamespaceProfiles(set, candidates, nil); err == nil {
		t.Fatal("distinct definitions silently overwrote the same namespaced output")
	}
	cluster := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: metav1.ObjectMeta{Name: set.Name}, Spec: set.Spec}
	if _, err := buildDesiredClusterProfiles(cluster, candidates, nil); err == nil {
		t.Fatal("distinct definitions silently overwrote the same cluster output")
	}
}

func TestIneligibleModelsDoNotBlockResourceSources(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		for _, mode := range []string{"discovery-disabled", "legacy-custom", "legacy-derivation"} {
			m, _ := sourceFixture(t, namespace, "legacy")
			switch mode {
			case "discovery-disabled":
				disabled := false
				m.spec.Discovery = &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: &disabled}
			case "legacy-custom":
				m.spec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "weights/model", SourceURI: "hf://weights/model"}}
			case "legacy-derivation":
				m.spec.ProfileCopy = &aimv1alpha1.AIMProfileSetSpec{Selector: aimv1alpha1.ProfileSelector{Engine: "vllm"}}
			}
			meta := metav1.ObjectMeta{
				Name: "legacy-profile", Namespace: namespace,
				Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true", aimmodel.AnnotationModelUID(): string(m.object.GetUID())},
			}
			spec := aimv1alpha2.AIMProfileSpecCommon{AimId: "architecture", ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "weights/model", SourceURI: "hf://weights/model"}}, Engine: "vllm"}
			var live client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta, Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: spec}}
			if namespace == "" {
				live = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta, Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: spec}}
			}
			c := sourceClient(t, m.object, live)
			sources, err := loadDefaultScope(context.Background(), c, namespace, namespace == "", aimv1alpha1.AIMProfileSetSpec{
				Selector: aimv1alpha1.ProfileSelector{Engine: "vllm"}, VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
			}, "set-uid")
			if err != nil || len(sources) != 1 || sources[0].Name != live.GetName() {
				t.Fatalf("namespace=%q mode=%s: sources=%d, %v", namespace, mode, len(sources), err)
			}
		}
	}
}

// Exercise the production state-engine path with a non-nil Clientset, not only
// Compose/Plan. Both missing explicit catalogs and partial inventory failures
// must leave existing outputs byte-for-byte untouched.
func TestSourceFailurePipelineDoesNotWriteProfiles(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			namespace := "team"
			if cluster {
				namespace = ""
			}
			m, cm := sourceFixture(t, namespace, "broken")
			set := partitionSet(namespace)
			if explicit {
				set.Spec.SourceRef = &aimv1alpha1.ProfileSourceRef{Name: cm.Name}
			}
			meta := metav1.ObjectMeta{Name: "keep", Namespace: namespace, Annotations: map[string]string{annotationProfileSetUID: string(set.UID)}}
			var parent client.Object = set
			var output client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta}
			clusterSet := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec}
			if cluster {
				parent = clusterSet
				output = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta}
			}
			c := sourceClient(t, m.object, parent, output)
			before := output.DeepCopyObject().(client.Object)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(output), before); err != nil {
				t.Fatal(err)
			}
			var err error
			if cluster {
				p := controllerutils.Pipeline[*aimv1alpha2.AIMClusterProfileSet, *aimv1alpha1.AIMProfileSetStatus, ClusterProfileSetFetchResult, ClusterProfileSetObservation]{
					Client: c, StatusClient: c.Status(), Scheme: c.Scheme(), Clientset: kubernetesfake.NewClientset(),
					Recorder: record.NewFakeRecorder(100), ControllerName: "cluster-profile-set", Reconciler: &ClusterProfileSetReconciler{Scheme: c.Scheme()},
				}
				_, err = p.Run(context.Background(), clusterSet)
			} else {
				p := controllerutils.Pipeline[*aimv1alpha2.AIMProfileSet, *aimv1alpha1.AIMProfileSetStatus, ProfileSetFetchResult, ProfileSetObservation]{
					Client: c, StatusClient: c.Status(), Scheme: c.Scheme(), Clientset: kubernetesfake.NewClientset(),
					Recorder: record.NewFakeRecorder(100), ControllerName: "profile-set", Reconciler: &ProfileSetReconciler{Scheme: c.Scheme()},
				}
				_, err = p.Run(context.Background(), set)
			}
			if err != nil {
				t.Fatal(err)
			}
			after := output.DeepCopyObject().(client.Object)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(output), after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source failure wrote an existing profile")
			}
			status := set.Status
			if cluster {
				status = clusterSet.Status
			}
			found := false
			for _, condition := range status.Conditions {
				if condition.Reason == "SourceLoadFailed" {
					found = true
				}
			}
			if !found || status.Status == constants.AIMStatusReady {
				t.Fatalf("source failure hidden: %+v", status)
			}
		}
	}
}

func TestProfileSetPipeline_MissingBaseImageIsConfigurationError(t *testing.T) {
	t.Parallel()

	for _, namespace := range []string{"team", ""} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			sourceSpec := aimv1alpha2.AIMProfileSpecCommon{
				AimId: "team/base", ModelId: "team/base", ProfileId: "gpu",
				Image:        "registry.example/optimized:1.2",
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "team/base", SourceURI: "hf://team/base"}},
			}
			sourceMeta := metav1.ObjectMeta{
				Name: "source", Namespace: namespace,
				Annotations: profileyaml.Mark(map[string]string{
					aimprofile.AnnotationProfileCopyable: "true",
				}, profileyaml.DefaultContract()),
			}
			setMeta := metav1.ObjectMeta{Name: "fine-tune", Namespace: namespace, UID: "set-uid", Generation: 1}
			setSpec := aimv1alpha1.AIMProfileSetSpec{
				Selector:      aimv1alpha1.ProfileSelector{AimId: sourceSpec.AimId},
				VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
				Overrides: &aimv1alpha1.ProfileOverrides{
					ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "team/fine-tune", SourceURI: "hf://team/fine-tune"}},
				},
			}
			outputMeta := metav1.ObjectMeta{
				Name: "keep", Namespace: namespace,
				Annotations: map[string]string{annotationProfileSetUID: string(setMeta.UID)},
			}
			var source, parent, output client.Object
			var status *aimv1alpha1.AIMProfileSetStatus
			var run func(client.Client) (ctrl.Result, error)
			if namespace == "" {
				source = &aimv1alpha2.AIMClusterProfile{ObjectMeta: sourceMeta, Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: sourceSpec}}
				set := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: setMeta, Spec: setSpec}
				parent, status = set, &set.Status
				output = &aimv1alpha2.AIMClusterProfile{ObjectMeta: outputMeta}
				run = func(c client.Client) (ctrl.Result, error) {
					pipeline := controllerutils.Pipeline[*aimv1alpha2.AIMClusterProfileSet, *aimv1alpha1.AIMProfileSetStatus, ClusterProfileSetFetchResult, ClusterProfileSetObservation]{
						Client: c, StatusClient: c.Status(), Scheme: c.Scheme(), Clientset: kubernetesfake.NewClientset(),
						Recorder: record.NewFakeRecorder(100), ControllerName: "cluster-profile-set", Reconciler: &ClusterProfileSetReconciler{Scheme: c.Scheme()},
					}
					return pipeline.Run(context.Background(), set)
				}
			} else {
				source = &aimv1alpha2.AIMProfile{ObjectMeta: sourceMeta, Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: sourceSpec}}
				set := &aimv1alpha2.AIMProfileSet{ObjectMeta: setMeta, Spec: setSpec}
				parent, status = set, &set.Status
				output = &aimv1alpha2.AIMProfile{ObjectMeta: outputMeta}
				run = func(c client.Client) (ctrl.Result, error) {
					pipeline := controllerutils.Pipeline[*aimv1alpha2.AIMProfileSet, *aimv1alpha1.AIMProfileSetStatus, ProfileSetFetchResult, ProfileSetObservation]{
						Client: c, StatusClient: c.Status(), Scheme: c.Scheme(), Clientset: kubernetesfake.NewClientset(),
						Recorder: record.NewFakeRecorder(100), ControllerName: "profile-set", Reconciler: &ProfileSetReconciler{Scheme: c.Scheme()},
					}
					return pipeline.Run(context.Background(), set)
				}
			}
			c := sourceClient(t, source, parent, output)
			before := output.DeepCopyObject().(client.Object)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(output), before); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				result, err := run(c)
				if err != nil || result != (ctrl.Result{}) {
					t.Fatalf("missing lineage must not retry: result=%+v error=%v", result, err)
				}
				condition := meta.FindStatusCondition(status.Conditions, controllerutils.ConditionTypeConfigValid)
				if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != controllerutils.ReasonInvalidSpec {
					t.Fatalf("missing ConfigValid=False: %+v", status.Conditions)
				}
				if condition := meta.FindStatusCondition(status.Conditions, controllerutils.ConditionTypeDependenciesReachable); condition != nil && condition.Status == metav1.ConditionFalse {
					t.Fatalf("missing lineage reported as unreachable infrastructure: %+v", condition)
				}
				derivation := meta.FindStatusCondition(status.Conditions, componentDerivation+controllerutils.ComponentConditionSuffix)
				if derivation == nil || !strings.Contains(derivation.Message, "no AIM_BASE_IMAGE_REF") {
					t.Fatalf("missing actionable lineage diagnostic: %+v", derivation)
				}
			}
			after := output.DeepCopyObject().(client.Object)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(output), after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("missing lineage changed an existing managed profile")
			}
		})
	}
}

func TestCatalogSourceSelectionPruneSafety(t *testing.T) {
	for _, namespace := range []string{"team", ""} {
		scope := constants.LabelValueSourceModelScopeNamespace
		if namespace == "" {
			scope = constants.LabelValueSourceModelScopeCluster
		}
		for _, tc := range []struct {
			name              string
			explicit, unowned bool
			modelRef          *aimv1alpha1.ProfileSelectorModelRef
			noMatch, invalid  bool
		}{
			{name: "source-only", explicit: true},
			{name: "unowned-source-only", explicit: true, unowned: true},
			{name: "model-only", modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model"}},
			{name: "confirmed-empty", explicit: true, noMatch: true},
			{name: "source-and-model", explicit: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model"}, invalid: true},
			{name: "source-and-auto-model", explicit: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model", Scope: aimv1alpha1.ProfileSelectorScopeAuto}, invalid: true},
			{name: "source-and-namespace-model", explicit: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model", Scope: aimv1alpha1.ProfileSelectorScopeNamespace}, invalid: true},
			{name: "source-and-cluster-model", explicit: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model", Scope: aimv1alpha1.ProfileSelectorScopeCluster}, invalid: true},
			{name: "unowned-source-and-model", explicit: true, unowned: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model"}, invalid: true},
			{name: "source-and-empty-model", explicit: true, modelRef: &aimv1alpha1.ProfileSelectorModelRef{}, invalid: true},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				m, cm := sourceFixture(t, namespace, "model")
				if tc.unowned {
					cm.OwnerReferences = nil
				}
				set := partitionSet(namespace)
				if tc.explicit {
					set.Spec.SourceRef = &aimv1alpha1.ProfileSourceRef{Name: cm.Name}
				}
				set.Spec.Selector.ModelRef = tc.modelRef
				if tc.noMatch {
					set.Spec.Selector.Engine = "no-matching-engine"
				}
				meta := metav1.ObjectMeta{Name: "keep", Namespace: namespace, Annotations: map[string]string{annotationProfileSetUID: string(set.UID)}}
				var existing client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta}
				if namespace == "" {
					existing = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta}
				}
				c := sourceClient(t, m.object, cm, existing)
				// Every case has a readable, non-empty catalog. A source conflict
				// must not be mistaken for a successfully evaluated empty selection.
				catalog, err := aimprofile.CachedDiscoveryCatalog(cm)
				if err != nil || len(catalog.Profiles) != 1 {
					t.Fatalf("catalog unavailable: %v", err)
				}
				var loadErr error
				var pruneSafe bool
				var plan controllerutils.PlanResult
				if namespace == "" {
					clusterSet := &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec}
					rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfileSet]{Object: clusterSet}
					r := &ClusterProfileSetReconciler{}
					fetch := r.FetchRemoteState(ctx, c, rc)
					obs := r.ComposeState(ctx, rc, fetch)
					loadErr, pruneSafe = fetch.candidates.Error, obs.pruneSafe
					plan = r.PlanResources(ctx, rc, obs)
				} else {
					rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}
					r := &ProfileSetReconciler{}
					fetch := r.FetchRemoteState(ctx, c, rc)
					obs := r.ComposeState(ctx, rc, fetch)
					loadErr, pruneSafe = fetch.candidates.Error, obs.pruneSafe
					plan = r.PlanResources(ctx, rc, obs)
				}
				if tc.invalid {
					if loadErr == nil || !strings.Contains(loadErr.Error(), "sourceRef and selector.modelRef are mutually exclusive") {
						t.Fatalf("expected actionable source conflict, got %v", loadErr)
					}
					if controllerutils.CategorizeError(loadErr).Category() != controllerutils.ErrorCategoryInvalidSpec {
						t.Fatalf("source conflict is not an invalid spec: %v", loadErr)
					}
					if pruneSafe || len(plan.GetToApply()) != 0 || len(plan.GetToDelete()) != 0 {
						t.Fatalf("invalid selection must retain all outputs without writes: pruneSafe=%v plan=%+v", pruneSafe, plan)
					}
					return
				}
				if loadErr != nil || !pruneSafe {
					t.Fatalf("valid selection failed: pruneSafe=%v err=%v", pruneSafe, loadErr)
				}
				wantApply := 1
				if tc.noMatch {
					wantApply = 0
				}
				if len(plan.GetToApply()) != wantApply || len(plan.GetToDelete()) != 1 {
					t.Fatalf("valid selection should apply %d profiles and prune obsolete output: %+v", wantApply, plan)
				}
			})
		}
	}
}

func partitionSet(namespace string) *aimv1alpha2.AIMProfileSet {
	count := int32(1)
	return &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{Name: "partitioned", Namespace: namespace, UID: "set-uid"},
		Spec: aimv1alpha1.AIMProfileSetSpec{
			VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
			Selector: aimv1alpha1.ProfileSelector{
				AcceleratorModel: testSourceAccelerator, AcceleratorCount: &count,
				AcceleratorPartitioningMode: "unpartitioned",
				EngineArgs:                  &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
			},
			Overrides: &aimv1alpha1.ProfileOverrides{AcceleratorPartitioningMode: "CPX-NPS4", AcceleratorCount: &count},
		},
	}
}

func TestDefaultDefinitionsWithoutMaterialization(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		t.Run(map[bool]string{false: "namespace", true: "cluster"}[cluster], func(t *testing.T) {
			namespace := "team"
			if cluster {
				namespace = ""
			}
			m1, cm1 := sourceFixture(t, namespace, "first")
			m2, cm2 := sourceFixture(t, namespace, "second")
			c := sourceClient(t, m1.object, cm1, m2.object, cm2)
			set := partitionSet(namespace)
			candidates, err := loadDefaultScope(context.Background(), c, namespace, cluster, set.Spec, string(set.UID))
			if err != nil || len(candidates) != 2 {
				t.Fatalf("candidates=%d err=%v", len(candidates), err)
			}
			if candidates[0].SourceID == candidates[1].SourceID {
				t.Fatal("independent models collapsed")
			}
			selected, err := resolveSelectedProfileSetCandidates(candidates, profileCopyRequest(set.Spec))
			if err != nil {
				t.Fatal(err)
			}
			desired, err := buildDesiredNamespaceProfiles(set, selected, nil)
			if cluster {
				desired, err = buildDesiredClusterProfiles(&aimv1alpha2.AIMClusterProfileSet{ObjectMeta: set.ObjectMeta, Spec: set.Spec}, selected, nil)
			}
			if err != nil || len(desired) != 2 {
				t.Fatalf("outputs=%d err=%v", len(desired), err)
			}
			for _, output := range desired {
				spec, ok := profileSpecCommon(output.Object)
				if !ok || spec.AcceleratorPartitioningMode != "CPX-NPS4" || spec.AcceleratorCount != 1 || spec.AcceleratorVendor != aimv1alpha1.AcceleratorVendorAMD {
					t.Fatalf("hardware not transformed: %+v", spec)
				}
				if !spec.Primary || spec.ServiceAccountName != "model-account" || spec.ImagePullSecrets[0].Name != "registry-creds" {
					t.Fatalf("model defaults lost: %+v", spec)
				}
				if output.Object.GetAnnotations()[profileyaml.AnnotationContract] == "" {
					t.Fatal("YAML contract lost")
				}
			}
			// Selection has not materialized any raw profile objects.
			var live aimv1alpha2.AIMProfileList
			if err := c.List(context.Background(), &live); err != nil || len(live.Items) != 0 {
				t.Fatal("unexpected raw profiles")
			}
		})
	}
}

func TestDefinitionIdentityAndOptOutAcrossMaterialization(t *testing.T) {
	ctx := context.Background()
	m, cm := sourceFixture(t, "team", "model")
	c := sourceClient(t, m.object, cm)
	set := partitionSet("team")
	before, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(before) != 1 {
		t.Fatalf("before=%v err=%v", before, err)
	}
	selected, _ := resolveSelectedProfileSetCandidates(before, profileCopyRequest(set.Spec))
	outputBefore, _ := buildDesiredNamespaceProfiles(set, selected, nil)
	name, _ := utils.GenerateDerivedName([]string{"model", "gpu"}, utils.WithHashSource("model", "gpu"))
	live := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", Annotations: map[string]string{
			aimmodel.AnnotationModelUID(): string(m.object.GetUID()), aimprofile.AnnotationProfileCopyable: "true",
			aimprofile.AnnotationProfileSource: aimprofile.ProfileSourceImage,
		}},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *before[0].Spec.DeepCopy()},
	}
	if err := c.Create(ctx, live); err != nil {
		t.Fatal(err)
	}
	after, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(after) != 1 {
		t.Fatalf("after=%v err=%v", after, err)
	}
	selected, _ = resolveSelectedProfileSetCandidates(after, profileCopyRequest(set.Spec))
	outputAfter, _ := buildDesiredNamespaceProfiles(set, selected, nil)
	if !reflect.DeepEqual(outputBefore, outputAfter) {
		t.Fatal("materialization changed derived name/spec/contract")
	}
	live.Annotations[aimprofile.AnnotationProfileCopyable] = "false"
	if err := c.Update(ctx, live); err != nil {
		t.Fatal(err)
	}
	after, err = loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(after) != 0 {
		t.Fatalf("opt-out bypassed by catalog: %d, %v", len(after), err)
	}
	if err := c.Delete(ctx, live); err != nil {
		t.Fatal(err)
	}
	after, err = loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(after) != 1 || after[0].SourceID != before[0].SourceID {
		t.Fatal("explicit profile deletion should clear the opt-out without changing identity")
	}
}

func TestDirectAndOptedInDerivedSourcesRemainIndependent(t *testing.T) {
	ctx := context.Background()
	m, cm := sourceFixture(t, "team", "model")
	c := sourceClient(t, m.object, cm)
	set := partitionSet("team")
	original, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, owner, optIn string }{
		{"direct", "", "true"},
		{"derived", "other-set", "true"},
		{"private", "other-set", ""},
		{"own", "set-uid", "true"},
	} {
		p := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: fixture.name, Namespace: "team", Annotations: map[string]string{
				aimprofile.AnnotationProfileCopyable: fixture.optIn, annotationProfileSetUID: fixture.owner,
				aimprofile.AnnotationSourceGroup: original[0].SourceGroup,
			}},
			Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *original[0].Spec.DeepCopy()},
		}
		if err := c.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(sources) != 3 {
		t.Fatalf("want model + direct + opted-in derived, got %d: %v", len(sources), err)
	}
	pools, err := loadNamespaceCandidatePools(ctx, c, c, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools.eligible) != 3 || len(pools.excludedByCopyable) != 1 || pools.excludedByCopyable[0].Name != "private" {
		t.Fatalf("candidate pools = eligible:%v excluded:%v, want private derived profile retained only for diagnosis", pools.eligible, pools.excludedByCopyable)
	}
}

func TestDefaultScopeExcludesProfilesWhoseLineageContainsCurrentSet(t *testing.T) {
	ctx := context.Background()
	set := partitionSet("team")
	spec := aimv1alpha2.AIMProfileSpecCommon{
		AcceleratorModel:            testSourceAccelerator,
		AcceleratorCount:            1,
		AcceleratorPartitioningMode: "unpartitioned",
		EngineArgs:                  &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
	}
	profile := func(name, owner, lineage string) *aimv1alpha2.AIMProfile {
		annotations := map[string]string{
			aimprofile.AnnotationProfileCopyable: "true",
			annotationProfileSetUID:              owner,
		}
		if lineage != "" {
			annotations[annotationProfileSetLineage] = lineage
		}
		return &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "team", UID: types.UID(name + "-uid"),
				Annotations: annotations,
			},
			Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: spec},
		}
	}
	c := sourceClient(t,
		profile("direct", "", ""),
		profile("sibling", "set-b", ""),
		profile("descendant", "set-b", "set-uid,set-b"),
	)

	candidates, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Name != "direct" || candidates[1].Name != "sibling" {
		t.Fatalf("candidates = %v, want direct and sibling only", candidates)
	}
}

func TestNamespaceFallbackUsesCompleteSelectionAndVersion(t *testing.T) {
	ctx := context.Background()
	local, localCM := sourceFixture(t, "team", "local")
	cluster, clusterCM := sourceFixture(t, "", "global")
	c := sourceClient(t, local.object, localCM, cluster.object, clusterCM)
	set := partitionSet("team")
	set.Spec.VersionPolicy, set.Spec.Version = aimv1alpha1.ProfileVersionPolicyPinned, "1.2.3"
	// A local definition exists but does not match the selected GPU.
	localCM.Data[aimprofile.FlattenProfilePath("architecture/gpu.yaml")] = `aim_id: architecture
model_id: weights/model
metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: optimized
  accelerator_model: MI325X
  accelerator_count: 1
`
	if err := c.Update(ctx, localCM); err != nil {
		t.Fatal(err)
	}
	candidates, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(candidates) != 1 || candidates[0].SourceGroup != cluster.group() {
		t.Fatalf("fallback failed: %+v, %v", candidates, err)
	}
	// Discovery pending locally cannot establish empty namespace scope.
	if err := c.Delete(ctx, localCM); err != nil {
		t.Fatal(err)
	}
	candidates, err = loadNamespaceCandidates(ctx, c, c, set)
	if err == nil || len(candidates) != 0 {
		t.Fatal("unreadable local source incorrectly allowed fallback")
	}
}

func TestInlineModelsAreNotCatalogSources(t *testing.T) {
	ctx := context.Background()
	m, cm := sourceFixture(t, "team", "model")
	m.spec.Profiles = &aimv1alpha1.AIMModelProfilesSpec{Overrides: &aimv1alpha1.ProfileOverrides{
		AcceleratorModel: "MI325X", ModelId: "new/weights", AimId: "new-architecture",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "new/weights", SourceURI: "hf://new/weights"}},
	}}
	c := sourceClient(t, m.object, cm)
	consumer := partitionSet("team")
	consumer.Spec.Selector = aimv1alpha1.ProfileSelector{AcceleratorModel: "MI325X"}
	sources, err := loadNamespaceCandidates(ctx, c, c, consumer)
	if err != nil || len(sources) != 0 {
		t.Fatalf("inline sources=%d err=%v", len(sources), err)
	}
}

func TestPartialSourceFailureRetainsEntireSetWithoutWrites(t *testing.T) {
	ctx := context.Background()
	healthy, healthyCM := sourceFixture(t, "team", "healthy")
	broken, brokenCM := sourceFixture(t, "team", "broken")
	c := sourceClient(t, healthy.object, healthyCM, broken.object, brokenCM)
	set := partitionSet("team")
	candidates, err := loadNamespaceCandidates(ctx, c, c, set)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := resolveSelectedProfileSetCandidates(candidates, profileCopyRequest(set.Spec))
	outputs, _ := buildDesiredNamespaceProfiles(set, selected, nil)
	existing := make([]managedProfile, 0, len(outputs)+1)
	for _, output := range outputs {
		existing = append(existing, managedProfile{Object: output.Object})
	}
	removed := outputs[0].Object.DeepCopyObject().(client.Object)
	removed.SetName("removed-healthy-definition")
	removed.GetAnnotations()[aimprofile.AnnotationSourceGroup] = healthy.group()
	existing = append(existing, managedProfile{Object: removed})
	brokenCM.Data[aimprofile.FlattenProfilePath("architecture/gpu.yaml")] = "metadata: ["
	if err := c.Update(ctx, brokenCM); err != nil {
		t.Fatal(err)
	}
	candidates, err = loadNamespaceCandidates(ctx, c, c, set)
	if err == nil || len(candidates) != 1 {
		t.Fatal("expected partial source error")
	}
	r := &ProfileSetReconciler{}
	obs := r.ComposeState(ctx, controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}, ProfileSetFetchResult{
		set: set, candidates: controllerutils.FetchResult[[]profileSetCandidate]{Value: candidates, Error: err},
		managed: controllerutils.FetchResult[[]managedProfile]{Value: existing},
	})
	if obs.pruneSafe || len(obs.desiredProfiles) != 0 {
		t.Fatalf("unknown sources must block the whole set: %+v", obs)
	}
	if obs.componentHealth[0].Reason != "SourceLoadFailed" {
		t.Fatal("load failure became NoMatchingProfiles")
	}
	plan := r.PlanResources(ctx, controllerutils.ReconcileContext[*aimv1alpha2.AIMProfileSet]{Object: set}, obs)
	if len(plan.GetToApply()) != 0 || len(plan.GetToDelete()) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	// Confirmed model deletion is not a load failure.
	if err := c.Delete(ctx, broken.object); err != nil {
		t.Fatal(err)
	}
	candidates, err = loadNamespaceCandidates(ctx, c, c, set)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("confirmed deletion not removed: %v", err)
	}
}

func TestCatalogRequiresCurrentOwnershipAndDiscoveryInputs(t *testing.T) {
	for _, edit := range []string{"uid", "image", "credentials", "service-account", "owner"} {
		t.Run(edit, func(t *testing.T) {
			m, cm := sourceFixture(t, "team", "model")
			switch edit {
			case "uid":
				m.object.SetUID("recreated")
			case "image":
				m.spec.Image = "registry.example/aim-model:2.0"
			case "credentials":
				m.spec.ImagePullSecrets = nil
			case "service-account":
				m.spec.ServiceAccountName = "different"
			case "owner":
				cm.OwnerReferences[0].UID = "other-owner"
			}
			c := sourceClient(t, m.object, cm)
			_, err := m.catalog(context.Background(), c)
			if err == nil {
				t.Fatal("stale catalog accepted")
			}
		})
	}
}

func TestLastCatalogEntryRemovalIsConfirmedEmpty(t *testing.T) {
	m, cm := sourceFixture(t, "team", "model")
	delete(cm.Data, aimprofile.FlattenProfilePath("architecture/gpu.yaml"))
	c := sourceClient(t, m.object, cm)
	candidates, err := loadNamespaceCandidates(context.Background(), c, c, partitionSet("team"))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("last entry removal treated as source failure: %v", err)
	}
}

func TestReadinessTracksDesiredSpecAndObservedGeneration(t *testing.T) {
	source := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "output", Namespace: "team", Generation: 2},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{AcceleratorModel: "MI300X"}},
	}
	desired := source.DeepCopy()
	desired.Spec.AcceleratorModel = "MI325X"
	current := managedProfile{Object: source, Status: aimv1alpha2.AIMProfileStatus{ObservedGeneration: 2, Status: constants.AIMStatusReady}}
	counts, _ := summarizeManagedProfiles([]desiredProfile{{Object: desired}}, []managedProfile{current})
	if counts.Ready != 0 {
		t.Fatal("old spec counted as ready")
	}
	source.Spec = desired.Spec
	current.Status.ObservedGeneration = 1
	counts, _ = summarizeManagedProfiles([]desiredProfile{{Object: desired}}, []managedProfile{current})
	if counts.Ready != 0 {
		t.Fatal("stale observed generation counted as ready")
	}
	current.Status.ObservedGeneration = 2
	source.Spec.AcceleratorPartitioningMode = "unpartitioned"
	source.Spec.AutoSelectionPolicy = aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized
	counts, _ = summarizeManagedProfiles([]desiredProfile{{Object: desired}}, []managedProfile{current})
	if counts.Ready != 1 {
		t.Fatal("API-defaulted equivalent spec not ready")
	}
}

func TestReadinessPreservesProfileType(t *testing.T) {
	profileTypes := []aimv1alpha1.AIMProfileType{
		"", aimv1alpha1.AIMProfileTypeOptimized, aimv1alpha1.AIMProfileTypeGeneral,
		aimv1alpha1.AIMProfileTypePreview, aimv1alpha1.AIMProfileTypeUnoptimized,
	}
	for _, desiredType := range profileTypes {
		for _, currentType := range profileTypes {
			t.Run(fmt.Sprintf("desired=%s/current=%s", desiredType, currentType), func(t *testing.T) {
				current := &aimv1alpha2.AIMProfile{
					ObjectMeta: metav1.ObjectMeta{Name: "output", Namespace: "team", Generation: 1},
					Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{Type: currentType}},
				}
				desired := current.DeepCopy()
				desired.Spec.Type = desiredType
				counts, existingDesiredCount := summarizeManagedProfiles(
					[]desiredProfile{{Object: desired}},
					[]managedProfile{{Object: current, Status: aimv1alpha2.AIMProfileStatus{ObservedGeneration: 1, Status: constants.AIMStatusReady}}},
				)
				var want int32
				if desiredType == currentType {
					want = 1
				}
				if counts.Ready != want || existingDesiredCount != want {
					t.Fatalf("ready=%d existingDesired=%d, want %d; distinct types must not compare equal", counts.Ready, existingDesiredCount, want)
				}
			})
		}
	}
}

func TestNamespaceFallbackAfterPinnedVersionSelection(t *testing.T) {
	count := int32(1)
	spec := aimv1alpha2.AIMProfileSpecCommon{AcceleratorModel: testSourceAccelerator, AcceleratorCount: count}
	local := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "local", Namespace: "team", Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"}},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: spec}, Status: aimv1alpha2.AIMProfileStatus{Version: "1.0"},
	}
	cluster := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Annotations: map[string]string{aimprofile.AnnotationProfileCopyable: "true"}},
		Spec:       aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: spec}, Status: aimv1alpha2.AIMProfileStatus{Version: "2.0"},
	}
	c := sourceClient(t, local, cluster)
	set := partitionSet("team")
	set.Spec.Selector = aimv1alpha1.ProfileSelector{AcceleratorModel: testSourceAccelerator, AcceleratorCount: &count}
	set.Spec.VersionPolicy, set.Spec.Version = aimv1alpha1.ProfileVersionPolicyPinned, "2.0"
	candidates, err := loadNamespaceCandidates(context.Background(), c, c, set)
	if err != nil || len(candidates) != 1 || candidates[0].Name != "cluster" {
		t.Fatalf("wrong fallback: %+v, %v", candidates, err)
	}
}

func TestPublicationCacheReuseAndInvalidation(t *testing.T) {
	m, cm := sourceFixture(t, "team", "cached-model")
	c := sourceClient(t, m.object, cm)
	first, _, err := m.publishedCandidates(context.Background(), c)
	if err != nil || len(first) != 1 {
		t.Fatalf("publication failed: %v", err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, _, err := m.publishedCandidates(context.Background(), c)
			if err != nil || len(next) != 1 || &next[0] != &first[0] {
				t.Errorf("effective definitions not shared: %v", err)
			}
		}()
	}
	wg.Wait()
	cm.Data[aimprofile.FlattenProfilePath("architecture/gpu.yaml")] = definitionYAML + "\n# new catalog revision\n"
	if err := c.Update(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	next, _, err := m.publishedCandidates(context.Background(), c)
	if err != nil || len(next) != 1 || &next[0] == &first[0] {
		t.Fatal("catalog edit did not invalidate publication")
	}
	if first[0].Spec.AcceleratorModel != testSourceAccelerator {
		t.Fatal("cached publication was mutated")
	}
}
