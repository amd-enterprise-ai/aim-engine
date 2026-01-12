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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimkvcache "github.com/amd-enterprise-ai/aim-engine/internal/aimkvcache"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	kvCacheControllerName = "kvcache"
)

// AIMKVCacheReconciler reconciles a AIMKVCache object
type AIMKVCacheReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	// DomainReconciler holds the specific business logic
	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMKVCache,
		*aimv1alpha1.AIMKVCacheStatus,
		aimkvcache.FetchResult,
		aimkvcache.Observation,
	]

	// Pipeline executes the standard reconciliation flow (Fetch -> Observe -> Plan -> Status)
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMKVCache,
		*aimv1alpha1.AIMKVCacheStatus,
		aimkvcache.FetchResult,
		aimkvcache.Observation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimkvcaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimkvcaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimkvcaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *AIMKVCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the CR
	var kvc aimv1alpha1.AIMKVCache
	if err := r.Get(ctx, req.NamespacedName, &kvc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMKVCache")
		return ctrl.Result{}, err
	}

	// Delegate execution to the generic Pipeline
	// This handles the lifecycle, error handling, and status patching automatically
	if err := r.pipeline.Run(ctx, &kvc); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AIMKVCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor(kvCacheControllerName)

	r.reconciler = &aimkvcache.AIMKVCacheReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}

	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMKVCache,
		*aimv1alpha1.AIMKVCacheStatus,
		aimkvcache.FetchResult,
		aimkvcache.Observation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: kvCacheControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMKVCache{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Named(kvCacheControllerName).
		Complete(r)
}
