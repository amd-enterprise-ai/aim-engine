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

package controller

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

// imageModelFixture runs the AIMModel controller against a fake API server and
// an in-memory registry, and counts the registry reads made while the
// discovery lock's Lease has a holder.
type imageModelFixture struct {
	client         client.Client
	reconciler     *AIMModelReconciler
	reads          atomic.Int32
	readsUnderLock atomic.Int32
}

func newImageModelFixture(t *testing.T, models []string, objects ...client.Object) *imageModelFixture {
	t.Helper()
	// The registry read must be anonymous whatever credentials the host has.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	f := &imageModelFixture{}
	var armed atomic.Bool
	registryHandler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() {
			f.reads.Add(1)
			var lease coordinationv1.Lease
			key := client.ObjectKey{Name: discoverylock.DiscoveryLockName, Namespace: constants.GetOperatorNamespace()}
			if err := f.client.Get(r.Context(), key, &lease); err == nil && lease.Spec.HolderIdentity != nil {
				f.readsUnderLock.Add(1)
			}
		}
		registryHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	image := strings.TrimPrefix(server.URL, "http://") + "/amdenterpriseai/aim-dummy:0.2.2"
	ref, err := name.ParseReference(image)
	if err != nil {
		t.Fatal(err)
	}
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	for _, model := range models {
		objects = append(objects, &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{
				Name: model, Namespace: "team-a", UID: types.UID(model + "-uid"),
				Finalizers: []string{modelProfilesCleanupFinalizerName},
			},
			Spec: aimv1alpha1.AIMModelSpec{Image: image},
		})
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, aimv1alpha1.AddToScheme, aimv1alpha2.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	noIndexValues := func(client.Object) []string { return nil }
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&aimv1alpha2.AIMModel{}).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimmodel.ManagedModelUIDIndexKey, noIndexValues).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, aimmodel.ManagedModelUIDIndexKey, noIndexValues).
		WithInterceptorFuncs(interceptor.Funcs{
			// Store applied discovery Jobs so later reconciles count them.
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() != types.ApplyPatchType {
					return c.Patch(ctx, obj, patch, opts...)
				}
				if job, ok := obj.(*batchv1.Job); ok {
					return client.IgnoreAlreadyExists(c.Create(ctx, job))
				}
				return nil
			},
		}).
		Build()
	f.client = c

	recorder := record.NewFakeRecorder(100)
	clientset := kubefake.NewSimpleClientset()
	r := &AIMModelReconciler{Client: c, Scheme: scheme, Recorder: recorder, Clientset: clientset}
	r.reconciler = &aimmodel.ModelReconciler{Clientset: clientset}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ModelFetchResult,
		aimmodel.ModelObservation,
	]{
		Client: c, StatusClient: c.Status(), Recorder: recorder, ControllerName: modelControllerName,
		Reconciler: r.reconciler, Scheme: scheme, Clientset: clientset, ForceApply: true,
	}
	f.reconciler = r
	armed.Store(true)
	return f
}

func (f *imageModelFixture) reconcile(model string) error {
	_, err := f.reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "team-a", Name: model},
	})
	return err
}

func TestReconcile_ReadsTheRegistryOutsideTheDiscoveryLock(t *testing.T) {
	f := newImageModelFixture(t, []string{"dummy"})
	if err := f.reconcile("dummy"); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if f.reads.Load() == 0 || f.readsUnderLock.Load() != 0 {
		t.Fatalf("%d registry reads, %d of them while the discovery lock was held; want reads, none under the lock",
			f.reads.Load(), f.readsUnderLock.Load())
	}
	active, err := discoverylock.CountActiveDiscoveryJobs(context.Background(), f.client)
	if err != nil || active != 1 {
		t.Fatalf("active discovery Jobs = %d, %v; want the reconcile to create one", active, err)
	}
}

func TestReconcile_ConcurrentModelsTakeOnlyTheLastDiscoverySlot(t *testing.T) {
	var running []client.Object
	for i := range constants.MaxConcurrentDiscoveryJobs - 1 {
		running = append(running, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("running-%d", i), Namespace: "team-b",
			Labels: map[string]string{
				"app.kubernetes.io/name":       discoverylock.DiscoveryLabelName,
				"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
				"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
			},
		}})
	}
	models := []string{"model-a", "model-b"}
	f := newImageModelFixture(t, models, running...)

	var wg sync.WaitGroup
	for _, model := range models {
		wg.Go(func() {
			if err := f.reconcile(model); err != nil {
				t.Errorf("Reconcile(%s) error = %v", model, err)
			}
		})
	}
	wg.Wait()

	active, err := discoverylock.CountActiveDiscoveryJobs(context.Background(), f.client)
	if err != nil || active != constants.MaxConcurrentDiscoveryJobs {
		t.Fatalf("active discovery Jobs = %d, %v; want exactly the cap of %d", active, err, constants.MaxConcurrentDiscoveryJobs)
	}
}
