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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestGetModelHealth(t *testing.T) {
	tests := []struct {
		name           string
		model          *aimv1alpha1.AIMModel
		expectedState  constants.AIMStatus
		expectedReason string
	}{
		{
			name:           "nil model",
			model:          nil,
			expectedState:  constants.AIMStatusPending,
			expectedReason: "ModelNotFound",
		},
		{
			name: "model with empty image",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{},
			},
			expectedState:  constants.AIMStatusFailed,
			expectedReason: "ImageNotSpecified",
		},
		{
			name: "healthy model",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusReady,
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ModelFound",
		},
		{
			name: "model failed due to image issue - propagate failure",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
					Conditions: []metav1.Condition{
						{Type: "ImageMetadataReady", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
						{Type: "ServiceTemplatesReady", Status: metav1.ConditionTrue, Reason: "AllTemplatesReady"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
					},
				},
			},
			expectedState:  constants.AIMStatusFailed,
			expectedReason: "ModelFailed",
		},
		{
			name: "model failed only due to templates - ignore to avoid deadlock",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
					Conditions: []metav1.Condition{
						{Type: "RuntimeConfigReady", Status: metav1.ConditionTrue, Reason: "ConfigFound"},
						{Type: "ImageMetadataReady", Status: metav1.ConditionTrue, Reason: "ImageMetadataFound"},
						{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse, Reason: "AllTemplatesFailed"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "AllTemplatesFailed"},
					},
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ModelFound",
		},
		{
			name: "model failed with both image and template issues - propagate",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
					Conditions: []metav1.Condition{
						{Type: "ImageMetadataReady", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
						{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse, Reason: "AllTemplatesFailed"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
					},
				},
			},
			expectedState:  constants.AIMStatusFailed,
			expectedReason: "ModelFailed",
		},
		{
			name: "model failed with no conditions yet - treat as healthy",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ModelFound",
		},
		{
			name: "model degraded due to image issue - propagate degraded",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusDegraded,
					Conditions: []metav1.Condition{
						{Type: "ImageMetadataReady", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
						{Type: "ServiceTemplatesReady", Status: metav1.ConditionTrue, Reason: "AllTemplatesReady"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
					},
				},
			},
			expectedState:  constants.AIMStatusDegraded,
			expectedReason: "ModelFailed",
		},
		{
			name: "model degraded only due to templates - ignore to avoid deadlock",
			model: &aimv1alpha1.AIMModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusDegraded,
					Conditions: []metav1.Condition{
						{Type: "RuntimeConfigReady", Status: metav1.ConditionTrue, Reason: "ConfigFound"},
						{Type: "ImageMetadataReady", Status: metav1.ConditionTrue, Reason: "ImageMetadataFound"},
						{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse, Reason: "SomeTemplatesDegraded"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "SomeTemplatesDegraded"},
					},
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ModelFound",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			health := GetModelHealth(tc.model)
			if health.State != tc.expectedState {
				t.Errorf("expected state %q, got %q", tc.expectedState, health.State)
			}
			if health.Reason != tc.expectedReason {
				t.Errorf("expected reason %q, got %q", tc.expectedReason, health.Reason)
			}
		})
	}
}

func TestGetClusterModelHealth(t *testing.T) {
	tests := []struct {
		name           string
		model          *aimv1alpha1.AIMClusterModel
		expectedState  constants.AIMStatus
		expectedReason string
	}{
		{
			name:           "nil model",
			model:          nil,
			expectedState:  constants.AIMStatusPending,
			expectedReason: "ClusterModelNotFound",
		},
		{
			name: "healthy cluster model",
			model: &aimv1alpha1.AIMClusterModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusReady,
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ClusterModelFound",
		},
		{
			name: "cluster model failed due to image - propagate",
			model: &aimv1alpha1.AIMClusterModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
					Conditions: []metav1.Condition{
						{Type: "ImageMetadataReady", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
						{Type: "ClusterServiceTemplatesReady", Status: metav1.ConditionTrue, Reason: "AllTemplatesReady"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
					},
				},
			},
			expectedState:  constants.AIMStatusFailed,
			expectedReason: "ClusterModelFailed",
		},
		{
			name: "cluster model failed only due to templates - ignore deadlock",
			model: &aimv1alpha1.AIMClusterModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusFailed,
					Conditions: []metav1.Condition{
						{Type: "RuntimeConfigReady", Status: metav1.ConditionTrue, Reason: "ConfigFound"},
						{Type: "ImageMetadataReady", Status: metav1.ConditionTrue, Reason: "ImageMetadataFound"},
						{Type: "ClusterServiceTemplatesReady", Status: metav1.ConditionFalse, Reason: "AllTemplatesFailed"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "AllTemplatesFailed"},
					},
				},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "ClusterModelFound",
		},
		{
			name: "cluster model degraded due to image - propagate degraded",
			model: &aimv1alpha1.AIMClusterModel{
				Spec: aimv1alpha1.AIMModelSpec{Image: "example/image:v1"},
				Status: aimv1alpha1.AIMModelStatus{
					Status: constants.AIMStatusDegraded,
					Conditions: []metav1.Condition{
						{Type: "ImageMetadataReady", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
						{Type: "ClusterServiceTemplatesReady", Status: metav1.ConditionTrue, Reason: "AllTemplatesReady"},
						{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ImageNotFound"},
					},
				},
			},
			expectedState:  constants.AIMStatusDegraded,
			expectedReason: "ClusterModelFailed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			health := GetClusterModelHealth(tc.model)
			if health.State != tc.expectedState {
				t.Errorf("expected state %q, got %q", tc.expectedState, health.State)
			}
			if health.Reason != tc.expectedReason {
				t.Errorf("expected reason %q, got %q", tc.expectedReason, health.Reason)
			}
		})
	}
}

func TestHasNonTemplateFailure(t *testing.T) {
	tests := []struct {
		name              string
		conditions        []metav1.Condition
		templateComponent string
		expected          bool
	}{
		{
			name:              "empty conditions",
			conditions:        nil,
			templateComponent: "ServiceTemplates",
			expected:          false,
		},
		{
			name: "only template condition failing",
			conditions: []metav1.Condition{
				{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse},
				{Type: "Ready", Status: metav1.ConditionFalse},
			},
			templateComponent: "ServiceTemplates",
			expected:          false,
		},
		{
			name: "non-template condition failing",
			conditions: []metav1.Condition{
				{Type: "ImageMetadataReady", Status: metav1.ConditionFalse},
				{Type: "ServiceTemplatesReady", Status: metav1.ConditionTrue},
			},
			templateComponent: "ServiceTemplates",
			expected:          true,
		},
		{
			name: "all conditions passing",
			conditions: []metav1.Condition{
				{Type: "RuntimeConfigReady", Status: metav1.ConditionTrue},
				{Type: "ImageMetadataReady", Status: metav1.ConditionTrue},
				{Type: "ServiceTemplatesReady", Status: metav1.ConditionTrue},
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			templateComponent: "ServiceTemplates",
			expected:          false,
		},
		{
			name: "cluster template variant",
			conditions: []metav1.Condition{
				{Type: "ClusterServiceTemplatesReady", Status: metav1.ConditionFalse},
				{Type: "Ready", Status: metav1.ConditionFalse},
			},
			templateComponent: "ClusterServiceTemplates",
			expected:          false,
		},
		{
			name: "non-Ready conditions are ignored",
			conditions: []metav1.Condition{
				{Type: "ConfigValid", Status: metav1.ConditionFalse},
				{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse},
			},
			templateComponent: "ServiceTemplates",
			expected:          false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasNonTemplateFailure(tc.conditions, tc.templateComponent)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}
