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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofileset"
)

func runTypedReconcile[T client.Object](
	ctx context.Context,
	c client.Client,
	req ctrl.Request,
	obj T,
	fetchErrMsg string,
	run func(context.Context, T) (ctrl.Result, error),
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if err := c.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, fetchErrMsg)
		return ctrl.Result{}, err
	}
	return run(ctx, obj)
}

func registerObjectIndex[T client.Object](
	ctx context.Context,
	mgr ctrl.Manager,
	obj T,
	field string,
	extract func(T) []string,
) error {
	return mgr.GetFieldIndexer().IndexField(ctx, obj, field, func(raw client.Object) []string {
		typed, ok := raw.(T)
		if !ok {
			return nil
		}
		return extract(typed)
	})
}

func registerProfileCommonIndexes[T client.Object](
	ctx context.Context,
	mgr ctrl.Manager,
	obj T,
	aimID func(T) string,
	managedProfileSetUID func(T) string,
	managedModelUID func(T) string,
) error {
	if err := registerObjectIndex(ctx, mgr, obj, aimv1alpha2.ProfileAimIdIndexKey, func(obj T) []string {
		return singleValueIndex(aimID(obj))
	}); err != nil {
		return err
	}
	if err := registerObjectIndex(ctx, mgr, obj, aimprofileset.ManagedProfileSetUIDIndexKey, func(obj T) []string {
		return singleValueIndex(managedProfileSetUID(obj))
	}); err != nil {
		return err
	}
	return registerObjectIndex(ctx, mgr, obj, aimmodel.ManagedModelUIDIndexKey, func(obj T) []string {
		return singleValueIndex(managedModelUID(obj))
	})
}

func listRequests[T client.Object](
	ctx context.Context,
	listFn func(context.Context) ([]T, error),
	logMsg string,
	keysAndValues ...any,
) []reconcile.Request {
	objects, err := listFn(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, logMsg, keysAndValues...)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(objects))
	for _, obj := range objects {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	}
	return requests
}

func enqueueRequestsForFilteredObjects[T client.Object](
	ctx context.Context,
	listFn func(context.Context) ([]T, error),
	logMsg string,
	include func(T) bool,
) []reconcile.Request {
	objects, err := listFn(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, logMsg)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(objects))
	for _, obj := range objects {
		if include(obj) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
		}
	}
	return requests
}

func directProfileSetRequestForScope(obj client.Object, requireNamespace bool) []reconcile.Request {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return nil
	}

	name := annotations[aimprofileset.AnnotationProfileSetName()]
	namespace := annotations[aimprofileset.AnnotationProfileSetNamespace()]
	if name == "" || (requireNamespace && namespace == "") {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: namespace}}}
}

func singleValueIndex(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
