/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// A failed label lookup reaches the model's conditions with a reason per
// failure. Only a missing image blocks the discovery Job, and then no
// discovery attempt is counted.
func TestPipelineRun_RegistryLookupErrors(t *testing.T) {
	// The lookup must be anonymous whatever registry credentials the host has.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	tests := []struct {
		name              string
		registry          func(t *testing.T) string
		wantReason        string
		wantAuthInvalid   bool
		wantConfigInvalid bool
		wantJob           bool
	}{
		{
			name:              "missing tag",
			registry:          registryAnswering(http.StatusNotFound, transport.ManifestUnknownErrorCode),
			wantReason:        constants.ReasonImageNotFound,
			wantConfigInvalid: true,
		},
		{
			name:            "unauthorized",
			registry:        registryAnswering(http.StatusUnauthorized, transport.UnauthorizedErrorCode),
			wantReason:      constants.ReasonImagePullAuthFailure,
			wantAuthInvalid: true,
			wantJob:         true,
		},
		{
			name:            "forbidden",
			registry:        registryAnswering(http.StatusForbidden, transport.DeniedErrorCode),
			wantReason:      constants.ReasonImagePullAuthFailure,
			wantAuthInvalid: true,
			wantJob:         true,
		},
		{
			name:       "network error",
			registry:   unreachableRegistry,
			wantReason: "ConnectionRefused",
			wantJob:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tt.registry(t)
			check := func(t *testing.T, image string, status *aimv1alpha1.AIMModelStatus, applied []client.Object) {
				t.Helper()
				metadata := meta.FindStatusCondition(status.Conditions, componentImageMetadata+controllerutils.ComponentConditionSuffix)
				if metadata == nil || metadata.Status != metav1.ConditionFalse || metadata.Reason != tt.wantReason {
					t.Fatalf("ImageMetadataReady = %+v, want False/%s; status %+v", metadata, tt.wantReason, status)
				}
				if !strings.Contains(metadata.Message, image) {
					t.Errorf("ImageMetadataReady message %q does not name %s", metadata.Message, image)
				}
				if unread := strings.Contains(metadata.Message, "labels were not read"); unread != tt.wantJob {
					t.Errorf("ImageMetadataReady message %q: reports unread labels = %v, want %v", metadata.Message, unread, tt.wantJob)
				}
				if got := meta.IsStatusConditionFalse(status.Conditions, controllerutils.ConditionTypeAuthValid); got != tt.wantAuthInvalid {
					t.Errorf("AuthValid=False is %v, want %v: %+v", got, tt.wantAuthInvalid, status.Conditions)
				}
				if got := meta.IsStatusConditionFalse(status.Conditions, controllerutils.ConditionTypeConfigValid); got != tt.wantConfigInvalid {
					t.Errorf("ConfigValid=False is %v, want %v: %+v", got, tt.wantConfigInvalid, status.Conditions)
				}
				if meta.IsStatusConditionFalse(status.Conditions, controllerutils.ConditionTypeDependenciesReachable) {
					t.Errorf("DependenciesReachable=False: the registry read must not gate the model: %+v", status.Conditions)
				}
				jobs := 0
				for _, obj := range applied {
					if _, ok := obj.(*batchv1.Job); ok {
						jobs++
					}
				}
				if (jobs > 0) != tt.wantJob {
					t.Errorf("applied %d discovery Jobs, want a Job = %v", jobs, tt.wantJob)
				}
				if status.Discovery == nil || (status.Discovery.Attempts > 0) != tt.wantJob {
					t.Errorf("status.discovery = %+v, want an attempt counted = %v", status.Discovery, tt.wantJob)
				}
			}

			t.Run("AIMModel", func(t *testing.T) {
				model := &aimv1alpha2.AIMModel{
					ObjectMeta: metav1.ObjectMeta{Name: "dummy", Namespace: "team-a", UID: "model-uid"},
					Spec:       aimv1alpha1.AIMModelSpec{Image: host + "/amdenterpriseai/aim-dummy:0.2.2"},
				}
				applied := reconcileWithRegistry[*aimv1alpha2.AIMModel, ModelFetchResult, ModelObservation](t, model, &ModelReconciler{})
				check(t, model.Spec.Image, &model.Status, applied)
			})
			t.Run("AIMClusterModel", func(t *testing.T) {
				model := &aimv1alpha2.AIMClusterModel{
					ObjectMeta: metav1.ObjectMeta{Name: "dummy", UID: "cluster-model-uid"},
					Spec:       aimv1alpha1.AIMModelSpec{Image: host + "/amdenterpriseai/aim-cluster-dummy:0.2.2"},
				}
				applied := reconcileWithRegistry[*aimv1alpha2.AIMClusterModel, ClusterModelFetchResult, ClusterModelObservation](t, model, &ClusterModelReconciler{})
				check(t, model.Spec.Image, &model.Status, applied)
			})
		})
	}
}

// registryAnswering serves the registry API root and answers every other
// request with status and a registry error code.
func registryAnswering(status int, code transport.ErrorCode) func(t *testing.T) string {
	return func(t *testing.T) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/" {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"errors":[{"code":%q,"message":"registry test error"}]}`, code)
		}))
		t.Cleanup(server.Close)
		return strings.TrimPrefix(server.URL, "http://")
	}
}

func unreachableRegistry(t *testing.T) string {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	return strings.TrimPrefix(server.URL, "http://")
}

// reconcileWithRegistry runs the production pipeline once and returns the
// objects it server-side applies, which the fake client records instead.
func reconcileWithRegistry[T controllerutils.ObjectWithStatus[*aimv1alpha1.AIMModelStatus], F, Obs any](
	t *testing.T, model T, reconciler controllerutils.DomainReconciler[T, *aimv1alpha1.AIMModelStatus, F, Obs],
) []client.Object {
	t.Helper()
	scheme := modelTestScheme(t)
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var applied []client.Object
	noIndexValues := func(client.Object) []string { return nil }
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		WithIndex(&aimv1alpha2.AIMProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() != types.ApplyPatchType {
					return c.Patch(ctx, obj, patch, opts...)
				}
				applied = append(applied, obj)
				return nil
			},
		}).
		Build()
	p := controllerutils.Pipeline[T, *aimv1alpha1.AIMModelStatus, F, Obs]{
		Client: c, StatusClient: c.Status(), Scheme: scheme, Recorder: record.NewFakeRecorder(50),
		ControllerName: "registry-errors-test", Reconciler: reconciler, Clientset: kubefake.NewSimpleClientset(),
	}
	if _, err := p.Run(context.Background(), model); err != nil {
		t.Fatalf("pipeline run: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(model), model); err != nil {
		t.Fatal(err)
	}
	return applied
}
