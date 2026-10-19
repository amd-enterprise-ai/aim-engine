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

package aimservice

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
)

// FindMatchingCustomModel resolves the service-scoped model name first, then
// falls back to an exact compatibility model at the predecessor name.
func FindMatchingCustomModel(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
) (*aimv1alpha1.AIMModel, error) {
	logger := log.FromContext(ctx)

	if custom == nil {
		return nil, nil
	}

	modelName := GenerateCustomModelName(service, custom)
	candidate, found, err := fetchCustomModelCandidate(ctx, c, service.Namespace, modelName)
	if err != nil {
		return nil, err
	}
	if found {
		return matchServiceScopedCustomModelCandidate(service, candidate)
	}
	logger.V(1).Info("no model at service-scoped custom-model name", "name", modelName)

	legacyModelName := generateLegacyCustomModelName(custom)
	if legacyModelName == modelName {
		return nil, nil
	}
	candidate, found, err = fetchCustomModelCandidate(ctx, c, service.Namespace, legacyModelName)
	if err != nil {
		return nil, err
	}
	if !found {
		logger.V(1).Info("no model at predecessor custom-model name", "name", legacyModelName)
		return nil, nil
	}
	if !legacyCustomModelPertainsToService(candidate, service) {
		logger.V(1).Info(
			"ignoring predecessor custom-model name owned by another service",
			"name", legacyModelName,
		)
		return nil, nil
	}

	return matchPredecessorCustomModelCandidate(service, custom, candidate)
}

func fetchCustomModelCandidate(
	ctx context.Context,
	c client.Client,
	namespace string,
	name string,
) (*aimv1alpha2.AIMModel, bool, error) {
	var candidate aimv1alpha2.AIMModel
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &candidate); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get custom model candidate %s/%s: %w", namespace, name, err)
	}
	return &candidate, true, nil
}

func matchServiceScopedCustomModelCandidate(
	service *aimv1alpha1.AIMService,
	candidate *aimv1alpha2.AIMModel,
) (*aimv1alpha1.AIMModel, error) {
	hydrated, compatibilityFound, err := legacyaimmodel.HydrateLegacyAutoCustomSpec(candidate, service)
	if compatibilityFound && err == nil {
		return legacyModelFromV1alpha2(hydrated), nil
	}
	if err == nil {
		err = fmt.Errorf("object is not a valid UID-bound compatibility model")
	}
	return nil, customModelCollisionError(service, candidate, err)
}

func matchPredecessorCustomModelCandidate(
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
	candidate *aimv1alpha2.AIMModel,
) (*aimv1alpha1.AIMModel, error) {
	hydrated, compatibilityFound, err := legacyaimmodel.HydrateLegacyAutoCustomSpec(candidate, service)
	if compatibilityFound {
		if err != nil {
			return nil, customModelCollisionError(service, candidate, err)
		}
		return legacyModelFromV1alpha2(hydrated), nil
	}

	grandfathered, err := legacyaimmodel.ValidateGrandfatheredLegacyAutoCustomModel(
		candidate,
		service,
		custom,
	)
	if grandfathered {
		if err != nil {
			return nil, customModelCollisionError(service, candidate, err)
		}
		return legacyModelFromV1alpha2(candidate), nil
	}

	return nil, customModelCollisionError(
		service,
		candidate,
		fmt.Errorf("object is not a valid compatibility model"),
	)
}

func legacyCustomModelPertainsToService(
	model *aimv1alpha2.AIMModel,
	service *aimv1alpha1.AIMService,
) bool {
	controller := metav1.GetControllerOf(model)
	if controller != nil && controller.Kind == "AIMService" {
		return controller.Name == service.Name && controller.UID == service.UID
	}
	return model.Labels[constants.LabelKeyService] == service.Name
}

func legacyModelFromV1alpha2(model *aimv1alpha2.AIMModel) *aimv1alpha1.AIMModel {
	copy := model.DeepCopy()
	return &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: copy.ObjectMeta,
		Spec:       copy.Spec,
		Status:     copy.Status,
	}
}

func customModelCollisionError(
	service *aimv1alpha1.AIMService,
	model *aimv1alpha2.AIMModel,
	cause error,
) error {
	return controllerutils.NewInvalidSpecError(
		"LegacyAutoCustomModelCollision",
		fmt.Sprintf(
			"AIMModel %s/%s occupies the deterministic inline-custom model name but is not an exact, "+
				"UID-bound match for AIMService %s/%s; automatic adoption or repair is disabled",
			model.Namespace, model.Name, service.Namespace, service.Name,
		),
		cause,
	)
}

// modelSourcesMatch compares two slices of AIMModelSource for equality.
// Order matters - sources must be in the same order.
func modelSourcesMatch(a, b []aimv1alpha1.AIMModelSource) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !modelSourceEquals(a[i], b[i]) {
			return false
		}
	}

	return true
}

// modelSourceEquals compares two AIMModelSource for equality.
// Compares modelId, sourceUri, and env vars (for S3 endpoint differentiation).
func modelSourceEquals(a, b aimv1alpha1.AIMModelSource) bool {
	if a.ModelID != b.ModelID {
		return false
	}
	if a.SourceURI != b.SourceURI {
		return false
	}

	// Compare relevant env vars (AWS_ENDPOINT_URL is important for S3 differentiation)
	aEndpoint := getEnvValue(a.Env, "AWS_ENDPOINT_URL")
	bEndpoint := getEnvValue(b.Env, "AWS_ENDPOINT_URL")
	return aEndpoint == bEndpoint
}

// getEnvValue extracts the value of an environment variable by name.
// Returns empty string if not found or if it uses a valueFrom reference.
func getEnvValue(envVars []corev1.EnvVar, name string) string {
	for _, env := range envVars {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

// GenerateCustomModelName generates a service-scoped name for a custom model.
// The service UID prevents identical custom inputs on distinct service objects
// from resolving to the same compatibility model.
// Format: {modelId-sanitized}-{hash}
func GenerateCustomModelName(
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
) string {
	return generateCustomModelName(custom, string(service.UID))
}

func generateLegacyCustomModelName(custom *aimv1alpha1.AIMServiceModelCustom) string {
	return generateCustomModelName(custom)
}

func generateCustomModelName(
	custom *aimv1alpha1.AIMServiceModelCustom,
	additionalHashInputs ...any,
) string {
	if len(custom.ModelSources) == 0 {
		// No model sources - generate a generic name with hash
		name, _ := utils.GenerateDerivedName(
			[]string{"custom-model"},
			utils.WithHashSource(append([]any{custom.BaseImage}, additionalHashInputs...)...),
		)
		return name
	}

	// Use the first model source's modelId as the base name part
	modelID := custom.ModelSources[0].ModelID

	// Build hash inputs for uniqueness - include ALL model sources
	// to distinguish models that share the first source but differ in others
	hashInputs := []any{custom.BaseImage}
	for _, source := range custom.ModelSources {
		hashInputs = append(hashInputs, source.ModelID, source.SourceURI)
		if endpoint := getEnvValue(source.Env, "AWS_ENDPOINT_URL"); endpoint != "" {
			hashInputs = append(hashInputs, endpoint)
		}
	}
	hashInputs = append(hashInputs, additionalHashInputs...)

	// GenerateDerivedName handles sanitization, length limits, and hash suffix
	name, _ := utils.GenerateDerivedName(
		[]string{modelID},
		utils.WithHashSource(hashInputs...),
	)

	return name
}
