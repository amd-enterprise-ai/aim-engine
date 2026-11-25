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

package aimservice

import (
	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// InferenceSecurityContexts returns the Pod and container security contexts
// of a service's inference pods. Each one is the service's own value when it
// sets one, even an empty one, and otherwise the merged runtime config's.
func InferenceSecurityContexts(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (*corev1.PodSecurityContext, *corev1.SecurityContext) {
	pod, container := service.Spec.PodSecurityContext, service.Spec.SecurityContext
	if runtimeConfig != nil {
		if pod == nil {
			pod = runtimeConfig.PodSecurityContext
		}
		if container == nil {
			container = runtimeConfig.SecurityContext
		}
	}
	return pod.DeepCopy(), container.DeepCopy()
}
