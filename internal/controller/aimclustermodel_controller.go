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
	"github.com/amd-enterprise-ai/aim-engine/internal/aimmodel"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// AIMClusterModelReconciler reconciles an AIMClusterModel object
type AIMClusterModelReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	modelDomain controllerutils.DomainReconciler[*aimv1alpha1.AIMClusterModel, *aimv1alpha1.AIMModelStatus, aimmodel.ClusterModelFetchResult, aimmodel.ClusterModelObservation]
	pipeline    controllerutils.Pipeline[*aimv1alpha1.AIMClusterModel, *aimv1alpha1.AIMModelStatus, aimmodel.ClusterModelFetchResult, aimmodel.ClusterModelObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *AIMClusterModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the cluster model
	var model aimv1alpha1.AIMClusterModel
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterModel")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &model); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AIMClusterModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	r.Recorder = mgr.GetEventRecorderFor("aim-cluster-image-controller")

	r.modelDomain = &aimmodel.ClusterModelReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ClusterModelFetchResult,
		aimmodel.ClusterModelObservation,
	]{
		Client:       mgr.GetClient(),
		StatusClient: mgr.GetClient().Status(),
		Recorder:     r.Recorder,
		FieldOwner:   "aim-cluster-model-controller",
		Domain:       r.modelDomain,
	}

	// Index AIMClusterServiceTemplate by modelName for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, aimmodel.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}
		return []string{template.Spec.ModelName}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMClusterModel{}).
		Owns(&aimv1alpha1.AIMClusterServiceTemplate{}).
		Named("aim-cluster-model").
		Complete(r)
}
