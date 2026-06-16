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

package aimartifact

import (
	"context"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestIsAdapter(t *testing.T) {
	tests := []struct {
		name string
		typ  aimv1alpha1.AIMArtifactType
		want bool
	}{
		{name: "adapter", typ: aimv1alpha1.ArtifactTypeAdapter, want: true},
		{name: "model", typ: aimv1alpha1.ArtifactTypeModel, want: false},
		{name: "empty defaults to model", typ: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{Type: tt.typ}}
			if got := isAdapter(mc); got != tt.want {
				t.Errorf("isAdapter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveAdapterPath(t *testing.T) {
	t.Run("defaults to metadata.name", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{ObjectMeta: metav1.ObjectMeta{Name: "my-adapter"}}
		if got := resolveAdapterPath(mc); got != "my-adapter" {
			t.Errorf("resolveAdapterPath() = %q, want my-adapter", got)
		}
	})
	t.Run("frozen status value wins", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{
			ObjectMeta: metav1.ObjectMeta{Name: "my-adapter"},
			Status:     aimv1alpha1.AIMArtifactStatus{AdapterPath: "frozen-path"},
		}
		if got := resolveAdapterPath(mc); got != "frozen-path" {
			t.Errorf("resolveAdapterPath() = %q, want frozen-path", got)
		}
	})
}

// adapterParentFetch builds an ArtifactObservation with a parent FetchResult in
// the desired state for getAdapterComponentHealth tests.
func adapterObsWithParent(parent *aimv1alpha1.AIMArtifact, notFound bool) ArtifactObservation {
	adapter := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "adapter", Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			ParentArtifact: "parent",
		},
	}
	var pf controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]
	switch {
	case notFound:
		pf = controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{
			Error: apierrors.NewNotFound(schema.GroupResource{Group: "aim.eai.amd.com", Resource: "aimartifacts"}, "parent"),
		}
	case parent != nil:
		pf = controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{Value: parent}
	}
	return ArtifactObservation{
		ArtifactFetchResult: ArtifactFetchResult{
			artifact:       adapter,
			parentArtifact: &pf,
		},
	}
}

func findHealth(health []controllerutils.ComponentHealth, component string) *controllerutils.ComponentHealth {
	for i := range health {
		if health[i].Component == component {
			return &health[i]
		}
	}
	return nil
}

func TestGetAdapterComponentHealth(t *testing.T) {
	modelWithDisk := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:        aimv1alpha1.ArtifactTypeModel,
			AdapterDisk: &aimv1alpha1.AIMAdapterDisk{},
		},
	}
	modelNoDisk := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeModel},
	}
	adapterParent := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeAdapter},
	}

	tests := []struct {
		name      string
		obs       ArtifactObservation
		wantState constants.AIMStatus
		wantOK    bool
	}{
		{
			name:      "valid lineage is ready",
			obs:       adapterObsWithParent(modelWithDisk, false),
			wantState: constants.AIMStatusReady,
			wantOK:    true,
		},
		{
			name:      "missing parent is pending",
			obs:       adapterObsWithParent(nil, true),
			wantState: constants.AIMStatusPending,
		},
		{
			name:      "parent lacks adapter disk is pending",
			obs:       adapterObsWithParent(modelNoDisk, false),
			wantState: constants.AIMStatusPending,
		},
		{
			name:      "parent is itself an adapter fails",
			obs:       adapterObsWithParent(adapterParent, false),
			wantState: constants.AIMStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.obs.getAdapterComponentHealth()
			h := findHealth(health, "AdapterParent")
			if h == nil {
				t.Fatal("expected an AdapterParent component health entry")
			}
			if h.State != tt.wantState {
				t.Errorf("AdapterParent state = %q, want %q", h.State, tt.wantState)
			}
		})
	}
}

func TestComposeAdapterStateSetsLineage(t *testing.T) {
	parent := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:        aimv1alpha1.ArtifactTypeModel,
			ModelID:     "org/base-model",
			AdapterDisk: &aimv1alpha1.AIMAdapterDisk{},
		},
	}
	pf := controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{Value: parent}
	fetch := ArtifactFetchResult{
		artifact: &aimv1alpha1.AIMArtifact{
			ObjectMeta: metav1.ObjectMeta{Name: "adapter"},
			Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeAdapter, ParentArtifact: "parent"},
		},
		parentArtifact: &pf,
	}

	obs := composeAdapterState(fetch)
	if obs.adapterPath != "adapter" {
		t.Errorf("adapterPath = %q, want adapter", obs.adapterPath)
	}
	if obs.parentModelID != "org/base-model" {
		t.Errorf("parentModelID = %q, want org/base-model", obs.parentModelID)
	}
	if obs.parentResolved == nil {
		t.Error("expected parentResolved to be set")
	}
}

func TestDecorateAdapterStatusFreezesPath(t *testing.T) {
	obs := ArtifactObservation{adapterPath: "computed-path"}
	status := &aimv1alpha1.AIMArtifactStatus{}
	decorateAdapterStatus(status, nil, obs)
	if status.AdapterPath != "computed-path" {
		t.Errorf("AdapterPath = %q, want computed-path", status.AdapterPath)
	}
	if status.PersistentVolumeClaim != "" {
		t.Errorf("adapters must not allocate a cache PVC, got %q", status.PersistentVolumeClaim)
	}

	// Already-frozen path must not be overwritten.
	status2 := &aimv1alpha1.AIMArtifactStatus{AdapterPath: "frozen"}
	decorateAdapterStatus(status2, nil, ArtifactObservation{adapterPath: "different"})
	if status2.AdapterPath != "frozen" {
		t.Errorf("AdapterPath = %q, want frozen (immutable)", status2.AdapterPath)
	}
}

func TestGenerateAdapterPvcNameDeterministic(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base-model", UID: types.UID("abc-123")},
	}
	n1 := GenerateAdapterPvcName(mc)
	n2 := GenerateAdapterPvcName(mc)
	if n1 != n2 {
		t.Errorf("GenerateAdapterPvcName not deterministic: %q != %q", n1, n2)
	}
	if n1 == "" {
		t.Error("GenerateAdapterPvcName returned empty")
	}

	// Different UID => different name (delete/recreate safety).
	other := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base-model", UID: types.UID("xyz-789")},
	}
	if GenerateAdapterPvcName(other) == n1 {
		t.Error("expected different UID to yield a different adapter PVC name")
	}
}

func TestBuildAdapterPvcDefaults(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base-model", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeModel},
	}
	pvc := buildAdapterPvc(mc, "longhorn", DefaultAdapterDiskSize)

	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != "ReadWriteMany" {
		t.Errorf("adapter disk must be RWX, got %v", pvc.Spec.AccessModes)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "longhorn" {
		t.Errorf("storage class not propagated: %v", pvc.Spec.StorageClassName)
	}
	got := pvc.Spec.Resources.Requests["storage"]
	if got.Cmp(DefaultAdapterDiskSize) != 0 {
		t.Errorf("default adapter disk size = %s, want %s", got.String(), DefaultAdapterDiskSize.String())
	}
}

// TestAdapterDiskSizeResolution covers the size precedence: artifact override >
// runtime-config cluster default > built-in default.
func TestAdapterDiskSizeResolution(t *testing.T) {
	rcSize := resource.MustParse("80Gi")
	rc := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Storage: &aimv1alpha1.AIMStorageConfig{AdapterDiskSize: &rcSize},
		},
	}

	t.Run("built-in default when nothing set", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{}}}
		if got := adapterDiskSize(mc, nil); got.Cmp(DefaultAdapterDiskSize) != 0 {
			t.Errorf("size = %s, want built-in %s", got.String(), DefaultAdapterDiskSize.String())
		}
	})
	t.Run("runtime config default wins over built-in", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{}}}
		if got := adapterDiskSize(mc, rc); got.Cmp(rcSize) != 0 {
			t.Errorf("size = %s, want runtime-config %s", got.String(), rcSize.String())
		}
	})
	t.Run("artifact override wins over runtime config", func(t *testing.T) {
		want := resource.MustParse("10Gi")
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{Size: want}}}
		if got := adapterDiskSize(mc, rc); got.Cmp(want) != 0 {
			t.Errorf("size = %s, want artifact override %s", got.String(), want.String())
		}
	})
}

// TestAdapterDiskStorageClassResolution covers the class precedence: artifact
// override > runtime-config adapter RWX class > generic default.
func TestAdapterDiskStorageClassResolution(t *testing.T) {
	rwx := "rwx-nfs"
	def := "longhorn"
	rc := &aimv1alpha1.AIMRuntimeConfigCommon{
		AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
			Storage: &aimv1alpha1.AIMStorageConfig{
				AdapterDiskStorageClassName: &rwx,
				DefaultStorageClassName:     &def,
			},
		},
	}

	t.Run("adapter RWX class wins over generic default", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{}}}
		if got := adapterDiskStorageClass(mc, rc); got != rwx {
			t.Errorf("class = %q, want %q", got, rwx)
		}
	})
	t.Run("falls back to generic default when adapter class unset", func(t *testing.T) {
		rc2 := &aimv1alpha1.AIMRuntimeConfigCommon{
			AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
				Storage: &aimv1alpha1.AIMStorageConfig{DefaultStorageClassName: &def},
			},
		}
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{}}}
		if got := adapterDiskStorageClass(mc, rc2); got != def {
			t.Errorf("class = %q, want %q", got, def)
		}
	})
	t.Run("artifact override wins", func(t *testing.T) {
		mc := &aimv1alpha1.AIMArtifact{Spec: aimv1alpha1.AIMArtifactSpec{AdapterDisk: &aimv1alpha1.AIMAdapterDisk{StorageClassName: "explicit"}}}
		if got := adapterDiskStorageClass(mc, rc); got != "explicit" {
			t.Errorf("class = %q, want explicit", got)
		}
	})
}

// listLiveAdapterServiceIDs must return only adapter-enabled service UIDs, and
// must PROPAGATE a List error rather than returning an empty keep-list — an empty
// keep-list tells the reaper to delete every subtree, so a transient API error
// must be distinguishable from "no live services" (the must-fix for the reaper).
func TestListLiveAdapterServiceIDs(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	withAdapters := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-a", Namespace: "default", UID: types.UID("uid-a")},
		Spec: aimv1alpha1.AIMServiceSpec{
			Adapters: []aimv1alpha1.AIMServiceAdapterReference{
				{Name: "lora", Kind: aimv1alpha1.AdapterKindAIMArtifact},
			},
		},
	}
	noAdapters := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-b", Namespace: "default", UID: types.UID("uid-b")},
	}

	t.Run("returns only adapter-enabled service UIDs", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(withAdapters, noAdapters).Build()
		ids, err := listLiveAdapterServiceIDs(context.Background(), c, "default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 || ids[0] != "uid-a" {
			t.Errorf("ids = %v, want [uid-a]", ids)
		}
	})

	t.Run("propagates List error instead of an empty keep-list", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
					return fmt.Errorf("boom")
				},
			}).Build()
		ids, err := listLiveAdapterServiceIDs(context.Background(), c, "default")
		if err == nil {
			t.Fatal("expected the List error to be propagated, got nil (would reap every subtree)")
		}
		if ids != nil {
			t.Errorf("ids = %v, want nil on error", ids)
		}
	})
}

func TestBuildAdapterReaperJobMountsPVCAndKeepList(t *testing.T) {
	mc := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base-model", Namespace: "default", UID: types.UID("u-1")},
		Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeModel},
	}
	job := buildAdapterReaperJob(mc, "adapter-pvc", []string{"svc-uid-1", "svc-uid-2"}, nil)

	container := job.Spec.Template.Spec.Containers[0]
	if container.Command[0] != "/adapter-reap.sh" {
		t.Errorf("reaper command = %v, want /adapter-reap.sh", container.Command)
	}
	env := envToMap(container.Env)
	if env["KEEP_SERVICE_IDS"] != "svc-uid-1,svc-uid-2" {
		t.Errorf("KEEP_SERVICE_IDS = %q, want svc-uid-1,svc-uid-2", env["KEEP_SERVICE_IDS"])
	}
	if env["ADAPTER_PVC_ROOT"] != constants.AIMAdapterPVCRoot {
		t.Errorf("ADAPTER_PVC_ROOT = %q, want %q", env["ADAPTER_PVC_ROOT"], constants.AIMAdapterPVCRoot)
	}
	if len(job.Spec.Template.Spec.Volumes) != 1 ||
		job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "adapter-pvc" {
		t.Errorf("reaper must mount the adapter PVC, got %v", job.Spec.Template.Spec.Volumes)
	}
}
