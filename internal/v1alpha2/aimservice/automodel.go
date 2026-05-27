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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// autoModelImageHashLength controls how many characters of the image
	// SHA we keep in the auto-created AIMModel name. Six characters give
	// 24 bits of collision resistance, which is comfortably sufficient
	// since the name is already anchored on (namespace, serviceName) —
	// the hash only has to disambiguate the image part for the rare case
	// where two services in the same namespace happen to share a name
	// scheme.
	autoModelImageHashLength = 6

	// autoModelNamePrefix is appended between the service name and the
	// image hash in the generated AIMModel name so operators can tell
	// auto-created models from hand-authored ones at a glance (`kubectl
	// get aimmodel -A` shows `chat-auto-a1b2c3` next to `chat-llama`).
	autoModelNamePrefix = "auto"

	// AnnotationAutoModelSource records *what* user authoring choice
	// produced this auto-created AIMModel ("spec.model.image" today;
	// reserved key so additional auto-create sources can be added later
	// without breaking client filters). The owner reference records
	// *who* triggered the creation, so we don't need a redundant
	// "dedicated-to" label.
	AnnotationAutoModelSource = constants.AimLabelDomain + "/auto-source"

	// AutoModelSourceSpecModelImage is the value stamped on
	// AnnotationAutoModelSource for image-shape auto-creates.
	AutoModelSourceSpecModelImage = "spec.model.image"
)

// GenerateAutoModelName returns the deterministic name for the
// AIMService-owned AIMModel that the v1alpha2 profile pipeline
// auto-creates when the user authored `spec.model.image` and no existing
// model matches. The shape is `<svcName>-auto-<6charHash(image)>`:
//
//   - svcName anchors the name on the owning service so two services
//     pulling the same image get two models (matching the dedicated
//     ownership semantics — see comment on planAutoCreatedAIMModel).
//   - "auto" is a stable marker that surfaces the provenance in `kubectl
//     get aimmodel` output without having to inspect labels.
//   - the 6-character image hash disambiguates the (namespace, service,
//     image) tuple deterministically across reconciles.
//
// Returns an error only when the image URI is empty (a programming
// contract violation — the resolver guards against this) so callers can
// simply propagate the error up to the reconcile pipeline's component-
// health surface.
func GenerateAutoModelName(serviceName, image string) (string, error) {
	if serviceName == "" {
		return "", fmt.Errorf("service name is empty")
	}
	if image == "" {
		return "", fmt.Errorf("image URI is empty")
	}
	return utils.GenerateDerivedName(
		[]string{serviceName, autoModelNamePrefix},
		utils.WithHashSource(image),
		utils.WithHashLength(autoModelImageHashLength),
	)
}

// buildAutoCreatedAIMModel constructs the desired v1alpha2 AIMModel for
// the image-shape auto-create path. The object is intentionally minimal:
//
//   - apiVersion: aim.eai.amd.com/v1alpha2 so it goes through v1alpha2
//     image discovery (which emits AIMProfile, the resource the v1alpha2
//     service resolver needs). A v1alpha1-shaped object would emit
//     AIMServiceTemplates instead and the profile resolver would never
//     find a match.
//   - spec.image only (v1alpha2 CEL forbids spec.profiles, spec.aimId,
//     spec.discovery, spec.defaultServiceTemplate, spec.runtimeConfigName,
//     spec.env, spec.imageMetadata, spec.modelSources, spec.custom,
//     spec.customTemplates, spec.profileCopy on a freshly-created object
//     — see api/v1alpha2/aimmodel_types.go). Inheriting
//     imagePullSecrets/serviceAccountName from the service is intentional
//     so private-registry pulls work out of the box.
//   - LabelKeyOrigin = "auto-generated" so the existing client filter
//     (`kubectl get aimmodel -l 'aim.eai.amd.com/origin!=auto-generated'`)
//     hides every auto-created model regardless of which version of the
//     pipeline created it.
//
// Owner reference is intentionally NOT set here — the framework's
// ApplyDesiredState path stamps the AIMService as controller on every
// applied object (see internal/controller/utils/apply.go::setOwnerReference),
// so we get the dedicated-per-service GC semantics for free. The Apply()
// caller in PlanResources must use Apply (not ApplyWithoutOwnerRef) to
// preserve this contract.
func buildAutoCreatedAIMModel(service *aimv1alpha1.AIMService, image string) (*aimv1alpha2.AIMModel, error) {
	name, err := GenerateAutoModelName(service.Name, image)
	if err != nil {
		return nil, fmt.Errorf("generate auto-model name: %w", err)
	}

	model := &aimv1alpha2.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelKeyOrigin: constants.LabelValueOriginAutoGenerated,
			},
			Annotations: map[string]string{
				AnnotationAutoModelSource: AutoModelSourceSpecModelImage,
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              image,
			ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
			ServiceAccountName: service.Spec.ServiceAccountName,
		},
	}

	return model, nil
}

// shouldPlanAutoCreatedModel returns true when the v1alpha2 reconcile
// pipeline must SSA-apply a dedicated AIMModel for the image-shape
// service. The gate is intentionally narrow:
//
//   - Resolution must have hit the image shape and signalled
//     needsAutoModel from the resolver. Both checks together prevent
//     accidentally creating models for services that landed on the
//     profile pipeline via the annotation but authored spec.model.name
//     (in which case the resolver would already have found a real
//     AIMModel via the by-name path and never set needsAutoModel).
//   - List errors must not have occurred — if the image lookup itself
//     failed we want to retry rather than race to create models the
//     operator can't yet enumerate.
func shouldPlanAutoCreatedModel(obs ServiceObservation) bool {
	res := obs.resolution
	if res.shape != resolutionShapeModelImage {
		return false
	}
	if !res.needsAutoModel {
		return false
	}
	if res.listErr != nil {
		return false
	}
	if res.autoModelImage == "" {
		return false
	}
	return true
}
