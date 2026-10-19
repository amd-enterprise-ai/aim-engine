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

package aimmodel

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	legacyserver "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
)

const (
	componentLegacyAutoCustomSpec = "LegacyAutoCustomSpec"
	legacyAutoCustomConditionType = componentLegacyAutoCustomSpec + controllerutils.ComponentConditionSuffix
	legacyAutoCustomFailureReason = "InvalidLegacyAutoCustomSpec"
)

// hydrateLegacyAutoCustomModel validates compatibility state and its live
// source before returning a predecessor-shaped in-memory copy. The caller must
// never apply the returned model.
func hydrateLegacyAutoCustomModel(
	ctx context.Context,
	c client.Client,
	model *v1alpha2.AIMModel,
) (*v1alpha1.AIMModel, bool, error) {
	_, found, err := legacyserver.DecodeLegacyAutoCustomModel(model)
	if err != nil {
		return nil, true, invalidLegacyAutoCustomError(model, err)
	}
	if !found {
		return asLegacyModel(model), false, nil
	}

	serviceName, err := legacyserver.LegacyAutoCustomSourceName(model)
	if err != nil {
		return nil, true, invalidLegacyAutoCustomError(model, err)
	}

	var source v1alpha1.AIMService
	if err := c.Get(ctx, client.ObjectKey{Namespace: model.Namespace, Name: serviceName}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, controllerutils.NewInvalidSpecError(
				legacyAutoCustomFailureReason,
				fmt.Sprintf(
					"Compatibility AIMModel %s/%s is orphaned because source AIMService %s/%s no longer exists. "+
						"Automatic adoption or repair is disabled; an administrator must repair or replace this model.",
					model.Namespace, model.Name, model.Namespace, serviceName,
				),
				err,
			)
		}
		return nil, true, fmt.Errorf(
			"get source AIMService %s/%s for compatibility model %s: %w",
			model.Namespace, serviceName, model.Name, err,
		)
	}

	hydrated, _, err := legacyserver.HydrateLegacyAutoCustomSpec(model, &source)
	if err != nil {
		return nil, true, invalidLegacyAutoCustomError(model, err)
	}
	return asLegacyModel(hydrated), true, nil
}

func invalidLegacyAutoCustomError(model *v1alpha2.AIMModel, cause error) error {
	problem, classified := legacyserver.LegacyAutoCustomSpecProblemOf(cause)
	detail := cause.Error()
	if classified {
		detail = string(problem)
	}
	return controllerutils.NewInvalidSpecError(
		legacyAutoCustomFailureReason,
		fmt.Sprintf(
			"Compatibility AIMModel %s/%s is invalid (%s). Automatic adoption or repair is disabled; "+
				"an administrator must restore the exact source AIMService provenance and compatibility payload, "+
				"or replace the model through a supported workflow.",
			model.Namespace, model.Name, detail,
		),
		cause,
	)
}
