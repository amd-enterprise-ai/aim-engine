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

package controller

import (
	"context"

	"k8s.io/client-go/tools/record"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimmodelcache"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// AIMModelCacheReconciler reconciles a AIMModelCache object
type AIMModelCacheReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler *aimmodelcache.Reconciler
	pipeline   controllerutils.Pipeline[*aimv1alpha1.AIMModelCache, *aimv1alpha1.AIMModelCacheStatus, aimmodelcache.FetchResult, aimmodelcache.Observation]
}

// RBAC markers
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodelcaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodelcaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodelcaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

const (
	modelCacheFieldOwner = "modelcache-controller"
)

func (r *AIMModelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch CR
	var cache aimv1alpha1.AIMModelCache
	if err := r.Get(ctx, req.NamespacedName, &cache); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling AIMModelCache", "name", cache.Name, "namespace", cache.Namespace)

	if err := r.pipeline.Run(ctx, &cache); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AIMModelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("modelcache-controller")

	r.reconciler = &aimmodelcache.Reconciler{
		Scheme:    r.Scheme,
		Clientset: r.Clientset,
	}

	r.pipeline = controllerutils.Pipeline[*aimv1alpha1.AIMModelCache, *aimv1alpha1.AIMModelCacheStatus, aimmodelcache.FetchResult, aimmodelcache.Observation]{
		Client:       r.Client,
		StatusClient: r.Status(),
		Recorder:     r.Recorder,
		FieldOwner:   modelCacheFieldOwner,
		Reconciler:   r.reconciler,
		Scheme:       r.Scheme,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMModelCache{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named("modelcache-controller").
		Complete(r)
}
