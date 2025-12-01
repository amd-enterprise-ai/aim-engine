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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimclustermodelsource"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// AIMClusterModelSourceReconciler reconciles an AIMClusterModelSource object
type AIMClusterModelSourceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMClusterModelSource, *aimv1alpha1.AIMClusterModelSourceStatus, aimclustermodelsource.ClusterModelSourceFetchResult, aimclustermodelsource.ClusterModelSourceObservation]
	pipeline   controllerutils.Pipeline[*aimv1alpha1.AIMClusterModelSource, *aimv1alpha1.AIMClusterModelSourceStatus, aimclustermodelsource.ClusterModelSourceFetchResult, aimclustermodelsource.ClusterModelSourceObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *AIMClusterModelSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the cluster model source
	var source aimv1alpha1.AIMClusterModelSource
	if err := r.Get(ctx, req.NamespacedName, &source); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterModelSource")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &source); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue after sync interval for periodic updates
	syncInterval := source.Spec.SyncInterval.Duration
	if syncInterval <= 0 {
		syncInterval = aimv1alpha1.DefaultSyncInterval
	}

	logger.V(1).Info("Requeuing for next sync", "syncInterval", syncInterval)
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (r *AIMClusterModelSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	r.Recorder = mgr.GetEventRecorderFor("aim-cluster-model-source-controller")

	r.reconciler = &aimclustermodelsource.ClusterModelSourceReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterModelSource,
		*aimv1alpha1.AIMClusterModelSourceStatus,
		aimclustermodelsource.ClusterModelSourceFetchResult,
		aimclustermodelsource.ClusterModelSourceObservation,
	]{
		Client:       mgr.GetClient(),
		StatusClient: mgr.GetClient().Status(),
		Recorder:     r.Recorder,
		FieldOwner:   "aim-cluster-model-source-controller",
		Reconciler:   r.reconciler,
		Scheme:       r.Scheme,
	}

	// Index AIMClusterModel by model-source label for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterModel{}, "metadata.labels."+constants.LabelKeyModelSource, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha1.AIMClusterModel)
		if !ok {
			return nil
		}
		if source, exists := model.Labels[constants.LabelKeyModelSource]; exists {
			return []string{source}
		}
		return nil
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMClusterModelSource{}).
		Owns(&aimv1alpha1.AIMClusterModel{}).
		Named("aim-cluster-model-source").
		Complete(r)
}
