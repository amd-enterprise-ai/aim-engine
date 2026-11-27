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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestInferenceSecurityContexts(t *testing.T) {
	configPod := &corev1.PodSecurityContext{
		RunAsNonRoot:       ptr.To(true),
		RunAsUser:          ptr.To(int64(1000)),
		FSGroup:            ptr.To(int64(1000)),
		SupplementalGroups: []int64{44, 110},
	}
	configContainer := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	servicePod := &corev1.PodSecurityContext{RunAsUser: ptr.To(int64(2000))}
	serviceContainer := &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(true)}

	withSecurityContexts := func(pod *corev1.PodSecurityContext, container *corev1.SecurityContext) *aimv1alpha2.AIMService {
		service := &aimv1alpha2.AIMService{}
		service.Spec.PodSecurityContext = pod
		service.Spec.SecurityContext = container
		return service
	}
	runtimeConfig := &aimv1alpha1.AIMRuntimeConfigCommon{}
	runtimeConfig.PodSecurityContext = configPod
	runtimeConfig.SecurityContext = configContainer

	tests := []struct {
		name          string
		service       *aimv1alpha2.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		wantPod       *corev1.PodSecurityContext
		wantContainer *corev1.SecurityContext
	}{
		{name: "none", service: withSecurityContexts(nil, nil)},
		{name: "runtime config without security contexts", service: withSecurityContexts(nil, nil), runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{}},
		{
			name:          "runtime config",
			service:       withSecurityContexts(nil, nil),
			runtimeConfig: runtimeConfig,
			wantPod:       configPod,
			wantContainer: configContainer,
		},
		{
			name:          "service replaces runtime config as a whole",
			service:       withSecurityContexts(servicePod, serviceContainer),
			runtimeConfig: runtimeConfig,
			wantPod:       servicePod,
			wantContainer: serviceContainer,
		},
		{
			name:          "each field inherits on its own",
			service:       withSecurityContexts(servicePod, nil),
			runtimeConfig: runtimeConfig,
			wantPod:       servicePod,
			wantContainer: configContainer,
		},
		{
			name:          "empty service objects replace runtime config's and are omitted",
			service:       withSecurityContexts(&corev1.PodSecurityContext{}, &corev1.SecurityContext{}),
			runtimeConfig: runtimeConfig,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPod, gotContainer := InferenceSecurityContexts(tt.service, tt.runtimeConfig)
			if !reflect.DeepEqual(gotPod, tt.wantPod) {
				t.Errorf("pod security context = %v, want %v", gotPod, tt.wantPod)
			}
			if !reflect.DeepEqual(gotContainer, tt.wantContainer) {
				t.Errorf("container security context = %v, want %v", gotContainer, tt.wantContainer)
			}
		})
	}

	gotPod, gotContainer := InferenceSecurityContexts(withSecurityContexts(nil, nil), runtimeConfig)
	gotPod.SupplementalGroups[0] = 0
	gotContainer.Capabilities.Drop[0] = "NET_RAW"
	if configPod.SupplementalGroups[0] != 44 || configContainer.Capabilities.Drop[0] != "ALL" {
		t.Error("InferenceSecurityContexts() returned the runtime config's objects instead of copies")
	}
}
