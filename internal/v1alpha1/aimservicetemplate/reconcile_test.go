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

package aimservicetemplate

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestDecorateStatus_SignalsTemplateMigrationForBothScopes(t *testing.T) {
	commonSpec := aimv1alpha1.AIMServiceTemplateSpecCommon{
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "org/model",
			SourceURI: "hf://org/model",
		}},
	}
	tests := []struct {
		name     string
		decorate func(*controllerutils.ConditionManager)
	}{
		{
			name: "namespace",
			decorate: func(cm *controllerutils.ConditionManager) {
				template := &aimv1alpha1.AIMServiceTemplate{
					Spec: aimv1alpha1.AIMServiceTemplateSpec{
						AIMServiceTemplateSpecCommon: commonSpec,
					},
				}
				(&ServiceTemplateReconciler{}).DecorateStatus(
					&aimv1alpha1.AIMServiceTemplateStatus{},
					cm,
					ServiceTemplateObservation{ServiceTemplateFetchResult: ServiceTemplateFetchResult{
						template: template,
					}},
				)
			},
		},
		{
			name: "cluster",
			decorate: func(cm *controllerutils.ConditionManager) {
				template := &aimv1alpha1.AIMClusterServiceTemplate{
					Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
						AIMServiceTemplateSpecCommon: commonSpec,
					},
				}
				(&ClusterServiceTemplateReconciler{}).DecorateStatus(
					&aimv1alpha1.AIMServiceTemplateStatus{},
					cm,
					ClusterServiceTemplateObservation{ClusterServiceTemplateFetchResult: ClusterServiceTemplateFetchResult{
						template: template,
					}},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			test.decorate(cm)
			condition := cm.Get(controllerutils.ConditionTypeMigrationRequired)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				t.Fatalf("MigrationRequired = %#v, want True", condition)
			}
		})
	}
}
