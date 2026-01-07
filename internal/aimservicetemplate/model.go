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
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// GetModelHealth inspects an AIMModel to determine component health.
// Used by ServiceTemplateReconciler to check upstream model availability.
func GetModelHealth(model *aimv1alpha1.AIMModel) controllerutils.ComponentHealth {
	if model == nil {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusProgressing,
			Reason:  "ModelNotFound",
			Message: "Waiting for AIMModel to be created",
		}
	}

	if model.Spec.Image == "" {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusDegraded,
			Reason:  "ImageNotSpecified",
			Message: "Model does not specify an image",
		}
	}

	return controllerutils.ComponentHealth{
		State:  constants.AIMStatusReady,
		Reason: "ModelFound",
	}
}

// GetClusterModelHealth inspects an AIMClusterModel to determine component health.
// Used by ClusterServiceTemplateReconciler to check upstream model availability.
func GetClusterModelHealth(model *aimv1alpha1.AIMClusterModel) controllerutils.ComponentHealth {
	if model == nil {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusProgressing,
			Reason:  "ClusterModelNotFound",
			Message: "Waiting for AIMClusterModel to be created",
		}
	}

	if model.Spec.Image == "" {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusDegraded,
			Reason:  "ImageNotSpecified",
			Message: "Cluster model does not specify an image",
		}
	}

	return controllerutils.ComponentHealth{
		State:  constants.AIMStatusReady,
		Reason: "ClusterModelFound",
	}
}
