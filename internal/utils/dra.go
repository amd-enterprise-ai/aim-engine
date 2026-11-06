// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

package utils

import (
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ResourceSliceAPIAvailable reports whether the cluster serves
// resource.k8s.io/v1 ResourceSlices. Controllers watch ResourceSlices only
// when it does: a watch on an API the cluster does not serve keeps the
// manager's cache from syncing.
func ResourceSliceAPIAvailable(mapper meta.RESTMapper) bool {
	_, err := mapper.RESTMapping(
		schema.GroupKind{Group: resourcev1.GroupName, Kind: "ResourceSlice"},
		resourcev1.SchemeGroupVersion.Version,
	)
	return err == nil
}

// AMDGPUResourceSlicePredicate passes events for the ResourceSlices of the
// AMD GPU DRA driver, which publish the GPUs of DRA nodes.
func AMDGPUResourceSlicePredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		slice, ok := obj.(*resourcev1.ResourceSlice)
		return ok && slice.Spec.Driver == constants.AMDGPUDRADriverName
	})
}
