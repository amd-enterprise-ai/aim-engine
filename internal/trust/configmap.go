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

package trust

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// FetchAdditionalCABundle directly resolves the namespace-local ConfigMap.
// Missing ConfigMaps and keys are optional and return an empty result; PEM
// parsing remains inside the workload.
func FetchAdditionalCABundle(
	ctx context.Context,
	r client.Reader,
	namespace string,
	config *aimv1alpha1.AIMRuntimeConfigCommon,
) controllerutils.FetchResult[*corev1.ConfigMap] {
	if config == nil || config.AdditionalCABundleRef == nil {
		return controllerutils.FetchResult[*corev1.ConfigMap]{}
	}
	result := controllerutils.FetchDirect(ctx, r, client.ObjectKey{
		Namespace: namespace,
		Name:      config.AdditionalCABundleRef.Name,
	}, &corev1.ConfigMap{})
	if result.IsNotFound() {
		return controllerutils.FetchResult[*corev1.ConfigMap]{}
	}
	if !result.OK() {
		return result
	}
	if _, ok := BundleData(result.Value, config.AdditionalCABundleRef.Key); !ok {
		return controllerutils.FetchResult[*corev1.ConfigMap]{}
	}
	return result
}

// BundleData returns a selected text or binary ConfigMap value.
func BundleData(configMap *corev1.ConfigMap, key string) ([]byte, bool) {
	if configMap == nil {
		return nil, false
	}
	if value, ok := configMap.Data[key]; ok {
		return []byte(value), true
	}
	value, ok := configMap.BinaryData[key]
	return value, ok
}
