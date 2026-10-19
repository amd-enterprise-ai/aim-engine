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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const (
	upgradePhaseSeed    = "seed"
	upgradeDirectSource = "direct"
	upgradeOptIn        = "true"
)

// This opt-in control-plane test installs the checked-out CRDs into an explicitly
// supplied ISOLATED cluster. It runs the real Fetch/Compose/Plan/StateEngine/SSA/
// Status pipeline with informer indexes. It does not launch discovery images,
// profile/runtime controllers, or GPU workloads; discovery and profile readiness
// are controlled inputs. No default kubeconfig is ever used.
//
// Run phase=seed on the release baseline (this file can be overlaid unchanged),
// then phase=verify on the candidate against the same vcluster. Both phases keep
// their labelled fixtures for inspection; use a fresh namespace per run.
//
//nolint:gocyclo // Keep the two-phase upgrade and fault/recovery sequence together; this test is deliberately opt-in.
func TestVClusterCatalogUpgrade(t *testing.T) {
	kubeconfig := os.Getenv("AIM_CATALOG_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("requires an explicit isolated vcluster kubeconfig")
	}
	phase := os.Getenv("AIM_CATALOG_TEST_PHASE")
	namespace := os.Getenv("AIM_CATALOG_TEST_NAMESPACE")
	if (phase != upgradePhaseSeed && phase != "verify") || namespace == "" {
		t.Fatal("set AIM_CATALOG_TEST_PHASE=seed|verify and a dedicated AIM_CATALOG_TEST_NAMESPACE")
	}
	if os.Getenv("AIM_SYSTEM_NAMESPACE") == "" {
		t.Fatal("set AIM_SYSTEM_NAMESPACE explicitly for cluster-model catalogs")
	}
	log.SetLogger(logr.Discard())
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	existing := true
	env := &envtest.Environment{
		Config: cfg, UseExistingCluster: &existing,
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if _, err := env.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, aimv1alpha1.AddToScheme, aimv1alpha2.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	informer, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range []client.Object{&aimv1alpha2.AIMProfile{}, &aimv1alpha2.AIMClusterProfile{}} {
		if err := informer.IndexField(ctx, obj, ManagedProfileSetUIDIndexKey, func(o client.Object) []string {
			return []string{o.GetAnnotations()[annotationProfileSetUID]}
		}); err != nil {
			t.Fatal(err)
		}
		if err := informer.IndexField(ctx, obj, aimv1alpha2.ProfileAimIdIndexKey, func(o client.Object) []string {
			spec, _ := profileSpecCommon(o)
			return []string{spec.AimId}
		}); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		if err := informer.Start(ctx); err != nil {
			t.Error(err)
		}
	}()
	if !informer.WaitForCacheSync(ctx) {
		t.Fatal("informer cache did not sync")
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme, Cache: &client.CacheOptions{Reader: informer}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(obj client.Object) {
		t.Helper()
		if err := direct.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	if phase == upgradePhaseSeed {
		create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: map[string]string{"aim-catalog-upgrade-test": upgradeOptIn}}})
	}
	type snapshot struct {
		Name   string
		UID    types.UID
		Owners []metav1.OwnerReference
	}
	snapshots := map[string]snapshot{}
	snapshotCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "catalog-upgrade-snapshot", Namespace: namespace}}
	if phase == "verify" {
		if err := direct.Get(ctx, client.ObjectKeyFromObject(snapshotCM), snapshotCM); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(snapshotCM.Data["profiles"]), &snapshots); err != nil {
			t.Fatal(err)
		}
	}
	for _, cluster := range []bool{false, true} {
		for _, source := range []string{"catalog", "explicit", upgradeDirectSource, "chain", "chain-vendor"} {
			scope := "ns"
			if cluster {
				scope = "cluster"
			}
			name := namespace + "-" + scope + "-" + source
			t.Run(scope+"/"+source, func(t *testing.T) {
				chained := source == "chain" || source == "chain-vendor"
				modelMeta := metav1.ObjectMeta{Name: name, Namespace: namespace}
				if cluster {
					modelMeta.Namespace = ""
				}
				modelSpec := aimv1alpha1.AIMModelSpec{Image: "registry.example/aim-model:1.2.3"}
				var model client.Object = &aimv1alpha2.AIMModel{ObjectMeta: modelMeta, Spec: modelSpec}
				modelKind := "AIMModel"
				if cluster {
					model = &aimv1alpha2.AIMClusterModel{ObjectMeta: modelMeta, Spec: modelSpec}
					modelKind = "AIMClusterModel"
				}
				cacheName, err := aimmodel.DiscoveryCacheName(name)
				if err != nil {
					t.Fatal(err)
				}
				cmNamespace := namespace
				if cluster {
					cmNamespace = constants.GetOperatorNamespace()
				}
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cacheName, Namespace: cmNamespace}}
				setSpec := aimv1alpha1.AIMProfileSetSpec{
					Selector:      aimv1alpha1.ProfileSelector{AimId: name},
					VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
					Overrides:     &aimv1alpha1.ProfileOverrides{AcceleratorModel: "MI325X"},
				}
				if source == "explicit" {
					setSpec.SourceRef = &aimv1alpha1.ProfileSourceRef{Name: cacheName}
				}
				if chained {
					setSpec.Overrides.AimId = name + "-derived"
				}
				if source == "chain-vendor" {
					setSpec.Overrides.AcceleratorVendor = aimv1alpha1.AcceleratorVendorAMD
				}
				var set client.Object = &aimv1alpha2.AIMProfileSet{ObjectMeta: modelMeta, Spec: setSpec}
				if cluster {
					set = &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: modelMeta, Spec: setSpec}
				}
				if phase == upgradePhaseSeed {
					if source != upgradeDirectSource {
						create(model)
					}
					raw := []byte("aim_id: " + name + "\nmodel_id: weights/model\nmetadata:\n  engine: vllm\n  metric: latency\n  precision: bf16\n  type: optimized\n  primary: true\n  accelerator_model: MI300X\n  accelerator_type: gpu\n  accelerator_vendor: amd\n  accelerator_count: 1\nengine_args:\n  tensor-parallel-size: 1\n")
					cm, err = aimmodel.BuildDiscoveryCacheConfigMap(aimmodel.DiscoveryCacheInput{
						Name: cacheName, Namespace: cmNamespace, ModelName: name, ModelUID: string(model.GetUID()),
						SourceImage: modelSpec.Image, AimID: name, BaseImage: "registry.example/aim-base:1.2",
						SourceSpecHash: aimmodel.ComputeDiscoverySpecHash(modelSpec.Image, nil, "", aimmodel.DiscoveryCommandVersion),
						Profiles:       map[string][]byte{"model/gpu.yaml": raw},
					})
					if err != nil {
						t.Fatal(err)
					}
					if source != upgradeDirectSource {
						cm.OwnerReferences = []metav1.OwnerReference{aimmodel.CacheOwnerReference(aimv1alpha2.GroupVersion.String(), modelKind, name, string(model.GetUID()))}
						create(cm)
					}
					catalog, err := aimprofile.ParseDiscoveryCatalog(cm)
					if err != nil {
						t.Fatal(err)
					}
					entry := catalog.Profiles[0]
					liveName, err := utils.GenerateDerivedName([]string{name, entry.Spec.ProfileId}, utils.WithHashSource(name, entry.Spec.ProfileId))
					if err != nil {
						t.Fatal(err)
					}
					meta := metav1.ObjectMeta{
						Name: liveName, Namespace: modelMeta.Namespace,
						Annotations: profileyaml.Mark(map[string]string{aimprofile.AnnotationProfileCopyable: upgradeOptIn}, entry.YAMLContract),
					}
					if source != upgradeDirectSource {
						meta.OwnerReferences = cm.OwnerReferences
						meta.Annotations[aimmodel.AnnotationModelUID()] = string(model.GetUID())
					} else {
						// Vendor was already supported on direct CRD sources.
						entry.Spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorAMD
					}
					var live client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta, Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: entry.Spec}}
					if cluster {
						live = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta, Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: entry.Spec}}
					}
					aimprofile.StampProfileProvenance(live, "deployable", aimv1alpha1.ProfileOriginDiscovered, nil)
					create(live)
					upgradeMarkReady(t, ctx, direct, live)
					create(set)
				} else {
					if err := direct.Get(ctx, client.ObjectKeyFromObject(set), set); err != nil {
						t.Fatal(err)
					}
					if source != upgradeDirectSource {
						if err := direct.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
							t.Fatal(err)
						}
					}
				}
				run := func() {
					t.Helper()
					if err := direct.Get(ctx, client.ObjectKeyFromObject(set), set); err != nil {
						t.Fatal(err)
					}
					recorder := record.NewFakeRecorder(100)
					if cluster {
						p := controllerutils.Pipeline[*aimv1alpha2.AIMClusterProfileSet, *aimv1alpha1.AIMProfileSetStatus, ClusterProfileSetFetchResult, ClusterProfileSetObservation]{
							Client: c, StatusClient: direct.Status(), Scheme: scheme, Clientset: clientset, Recorder: recorder,
							ControllerName: "cluster-profile-set-v1alpha2", Reconciler: &ClusterProfileSetReconciler{Scheme: scheme, APIReader: direct},
						}
						if _, err := p.Run(ctx, set.(*aimv1alpha2.AIMClusterProfileSet)); err != nil {
							t.Fatal(err)
						}
					} else {
						p := controllerutils.Pipeline[*aimv1alpha2.AIMProfileSet, *aimv1alpha1.AIMProfileSetStatus, ProfileSetFetchResult, ProfileSetObservation]{
							Client: c, StatusClient: direct.Status(), Scheme: scheme, Clientset: clientset, Recorder: recorder,
							ControllerName: "profile-set-v1alpha2", Reconciler: &ProfileSetReconciler{Scheme: scheme, APIReader: direct},
						}
						if _, err := p.Run(ctx, set.(*aimv1alpha2.AIMProfileSet)); err != nil {
							t.Fatal(err)
						}
					}
				}
				outputs := func() []client.Object {
					t.Helper()
					var result []client.Object
					if cluster {
						var list aimv1alpha2.AIMClusterProfileList
						if err := direct.List(ctx, &list); err != nil {
							t.Fatal(err)
						}
						for i := range list.Items {
							if list.Items[i].Annotations[annotationProfileSetUID] == string(set.GetUID()) {
								result = append(result, &list.Items[i])
							}
						}
					} else {
						var list aimv1alpha2.AIMProfileList
						if err := direct.List(ctx, &list, client.InNamespace(namespace)); err != nil {
							t.Fatal(err)
						}
						for i := range list.Items {
							if list.Items[i].Annotations[annotationProfileSetUID] == string(set.GetUID()) {
								result = append(result, &list.Items[i])
							}
						}
					}
					return result
				}
				converge := func() client.Object {
					t.Helper()
					var output client.Object
					err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(context.Context) (bool, error) {
						run()
						list := outputs()
						if len(list) != 1 {
							return false, nil
						}
						output = list[0]
						upgradeMarkReady(t, ctx, direct, output)
						var status aimv1alpha1.AIMProfileSetStatus
						if cluster {
							status = set.(*aimv1alpha2.AIMClusterProfileSet).Status
						} else {
							status = set.(*aimv1alpha2.AIMProfileSet).Status
						}
						return status.Status == constants.AIMStatusReady, nil
					})
					if err != nil {
						t.Fatalf("profile set did not reach Ready: %v; object=%+v", err, set)
					}
					return output
				}
				output := converge()
				if chained {
					before := output.DeepCopyObject().(client.Object)
					output.GetAnnotations()[aimprofile.AnnotationProfileCopyable] = upgradeOptIn
					if err := direct.Patch(ctx, output, client.MergeFrom(before)); err != nil {
						t.Fatal(err)
					}
					nextMeta := metav1.ObjectMeta{Name: name + "-next", Namespace: modelMeta.Namespace}
					nextSpec := aimv1alpha1.AIMProfileSetSpec{
						Selector: aimv1alpha1.ProfileSelector{AimId: name + "-derived"}, VersionPolicy: aimv1alpha1.ProfileVersionPolicyAll,
					}
					set = &aimv1alpha2.AIMProfileSet{ObjectMeta: nextMeta, Spec: nextSpec}
					if cluster {
						set = &aimv1alpha2.AIMClusterProfileSet{ObjectMeta: nextMeta, Spec: nextSpec}
					}
					if phase == upgradePhaseSeed {
						create(set)
					}
					output = converge()
				}
				actual := snapshot{output.GetName(), output.GetUID(), output.GetOwnerReferences()}
				service := &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
				if phase == upgradePhaseSeed {
					snapshots[name] = actual
					service.Spec.Profile = &aimv1alpha1.AIMServiceProfileConfig{Name: actual.Name}
					create(service)
					t.Logf("baseline output %s UID=%s", actual.Name, actual.UID)
					return
				}
				if !reflect.DeepEqual(actual, snapshots[name]) {
					t.Fatalf("upgrade replaced/renamed/reowned output: got=%+v baseline=%+v", actual, snapshots[name])
				}
				if err := direct.Get(ctx, client.ObjectKeyFromObject(service), service); err != nil {
					t.Fatal(err)
				}
				if service.Spec.Profile == nil || service.Spec.Profile.Name != output.GetName() {
					t.Fatal("explicit service binding lost")
				}
				// Repeated reconciles, including a fresh pipeline instance each time,
				// must leave output UID/ownership/generation stable.
				generation := output.GetGeneration()
				for range 3 {
					run()
					if err := direct.Get(ctx, client.ObjectKeyFromObject(output), output); err != nil {
						t.Fatal(err)
					}
					if output.GetUID() != actual.UID || output.GetGeneration() != generation {
						t.Fatal("no-op reconcile changed identity or generation")
					}
				}
				if source != upgradeDirectSource {
					// Missing explicit catalogs and broken default catalogs must both
					// stop all writes. Wait for the informer to observe the fault.
					saved := cm.DeepCopy()
					if err := direct.Delete(ctx, cm); err != nil {
						t.Fatal(err)
					}
					if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
						var observed corev1.ConfigMap
						err := c.Get(ctx, client.ObjectKeyFromObject(cm), &observed)
						return client.IgnoreNotFound(err) == nil && err != nil, client.IgnoreNotFound(err)
					}); err != nil {
						t.Fatal(err)
					}
					rv := output.GetResourceVersion()
					run()
					if err := direct.Get(ctx, client.ObjectKeyFromObject(output), output); err != nil {
						t.Fatal(err)
					}
					if output.GetResourceVersion() != rv {
						t.Fatal("source failure wrote a working output")
					}
					saved.ResourceVersion, saved.UID = "", ""
					saved.CreationTimestamp = metav1.Time{}
					saved.ManagedFields = nil
					create(saved)
				}
				if source == "catalog" {
					// Hardware dematerialization removes the raw profile, not its
					// catalog. Recreating a derived output must still work.
					rawName, err := utils.GenerateDerivedName([]string{name, "gpu"}, utils.WithHashSource(name, "gpu"))
					if err != nil {
						t.Fatal(err)
					}
					meta := metav1.ObjectMeta{Name: rawName, Namespace: modelMeta.Namespace}
					var raw client.Object = &aimv1alpha2.AIMProfile{ObjectMeta: meta}
					if cluster {
						raw = &aimv1alpha2.AIMClusterProfile{ObjectMeta: meta}
					}
					if err := direct.Delete(ctx, raw); err != nil {
						t.Fatal(err)
					}
				}
				// Child recreation must predict the same legacy name after restart;
				// it must not depend on annotations on the deleted child.
				if err := direct.Delete(ctx, output); err != nil {
					t.Fatal(err)
				}
				err = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(context.Context) (bool, error) {
					run()
					list := outputs()
					if len(list) != 1 {
						return false, nil
					}
					if list[0].GetName() != actual.Name {
						t.Fatal("child recreation changed derived name")
					}
					return list[0].GetUID() != actual.UID, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("verified stable name/UID/owner, admitted defaults, explicit binding, no-op and recreation: %s", actual.Name)
			})
		}
	}
	if phase == upgradePhaseSeed {
		raw, err := json.Marshal(snapshots)
		if err != nil {
			t.Fatal(err)
		}
		snapshotCM.Data = map[string]string{"profiles": string(raw)}
		create(snapshotCM)
	}
}

func upgradeMarkReady(t *testing.T, ctx context.Context, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	status := aimv1alpha2.AIMProfileStatus{ObservedGeneration: obj.GetGeneration(), Status: constants.AIMStatusReady, Version: "1.2.3"}
	switch p := obj.(type) {
	case *aimv1alpha2.AIMProfile:
		if reflect.DeepEqual(p.Status, status) {
			return
		}
		p.Status = status
	case *aimv1alpha2.AIMClusterProfile:
		if reflect.DeepEqual(p.Status, status) {
			return
		}
		p.Status = status
	}
	if err := c.Status().Update(ctx, obj); err != nil {
		t.Fatal(err)
	}
}
