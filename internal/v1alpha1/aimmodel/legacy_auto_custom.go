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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/equality"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// AnnotationLegacyAutoCustomSpec retains the predecessor inline-custom input
// that cannot be represented in the v1alpha2 AIMModel schema. It is an
// internal compatibility contract, not a user-authored API.
const AnnotationLegacyAutoCustomSpec = constants.AimLabelDomain + "/legacy-auto-custom-spec"

// LegacyAutoCustomSpecProblem identifies deterministic compatibility-state
// failures so callers can report them as invalid input rather than retryable
// infrastructure failures.
type LegacyAutoCustomSpecProblem string

const (
	LegacyAutoCustomSpecProblemAnnotationBudget    LegacyAutoCustomSpecProblem = "AnnotationBudgetExceeded"
	LegacyAutoCustomSpecProblemMalformedJSON       LegacyAutoCustomSpecProblem = "MalformedJSON"
	LegacyAutoCustomSpecProblemInvalidShape        LegacyAutoCustomSpecProblem = "InvalidShape"
	LegacyAutoCustomSpecProblemMissingPayload      LegacyAutoCustomSpecProblem = "MissingPayload"
	LegacyAutoCustomSpecProblemMissingBaseImage    LegacyAutoCustomSpecProblem = "MissingBaseImage"
	LegacyAutoCustomSpecProblemMissingModelSources LegacyAutoCustomSpecProblem = "MissingModelSources"
	LegacyAutoCustomSpecProblemMissingModelID      LegacyAutoCustomSpecProblem = "MissingModelID"
	LegacyAutoCustomSpecProblemMissingSourceURI    LegacyAutoCustomSpecProblem = "MissingSourceURI"
	LegacyAutoCustomSpecProblemMissingHardware     LegacyAutoCustomSpecProblem = "MissingHardware"
	LegacyAutoCustomSpecProblemCredentialLiteral   LegacyAutoCustomSpecProblem = "CredentialLiteral"
	LegacyAutoCustomSpecProblemNonCanonicalPayload LegacyAutoCustomSpecProblem = "NonCanonicalPayload"
	LegacyAutoCustomSpecProblemMissingProvenance   LegacyAutoCustomSpecProblem = "MissingProvenance"
	LegacyAutoCustomSpecProblemProvenanceMismatch  LegacyAutoCustomSpecProblem = "ProvenanceMismatch"
	LegacyAutoCustomSpecProblemSourceMismatch      LegacyAutoCustomSpecProblem = "SourceMismatch"
	LegacyAutoCustomSpecProblemContradictoryModel  LegacyAutoCustomSpecProblem = "ContradictoryModel"
)

// LegacyAutoCustomSpecValidationError is returned for malformed,
// contradictory, or provenance-invalid compatibility state.
type LegacyAutoCustomSpecValidationError struct {
	Problem LegacyAutoCustomSpecProblem
	Detail  string
}

func (e *LegacyAutoCustomSpecValidationError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("invalid legacy auto-custom compatibility state: %s", e.Problem)
	}
	return fmt.Sprintf("invalid legacy auto-custom compatibility state: %s: %s", e.Problem, e.Detail)
}

// LegacyAutoCustomSpecProblemOf extracts the stable problem classification
// from a compatibility-state validation error.
func LegacyAutoCustomSpecProblemOf(err error) (LegacyAutoCustomSpecProblem, bool) {
	var validationErr *LegacyAutoCustomSpecValidationError
	if !errors.As(err, &validationErr) {
		return "", false
	}
	return validationErr.Problem, true
}

// EncodeLegacyAutoCustomSpec strictly validates and encodes the predecessor
// inline-custom shape.
func EncodeLegacyAutoCustomSpec(custom *v1alpha1.AIMServiceModelCustom) (string, error) {
	if err := validateLegacyAutoCustomSpec(custom); err != nil {
		return "", err
	}

	payload, err := json.Marshal(custom)
	if err != nil {
		return "", &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  fmt.Sprintf("encode custom model input: %v", err),
		}
	}

	// Use Kubernetes' established aggregate annotation budget instead of an
	// independent compatibility-specific size limit.
	if errs := apivalidation.ValidateAnnotations(
		map[string]string{AnnotationLegacyAutoCustomSpec: string(payload)},
		field.NewPath("metadata", "annotations"),
	); len(errs) > 0 {
		return "", &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemAnnotationBudget,
			Detail:  errs.ToAggregate().Error(),
		}
	}

	return string(payload), nil
}

// SetLegacyAutoCustomSpec records the compatibility payload without mutating
// the model when its full annotation set would exceed Kubernetes API bounds.
func SetLegacyAutoCustomSpec(model *v1alpha2.AIMModel, custom *v1alpha1.AIMServiceModelCustom) error {
	if model == nil {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  "model is nil",
		}
	}

	payload, err := EncodeLegacyAutoCustomSpec(custom)
	if err != nil {
		return err
	}

	annotations := make(map[string]string, len(model.Annotations)+1)
	for key, value := range model.Annotations {
		annotations[key] = value
	}
	annotations[AnnotationLegacyAutoCustomSpec] = payload
	if errs := apivalidation.ValidateAnnotations(
		annotations,
		field.NewPath("metadata", "annotations"),
	); len(errs) > 0 {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemAnnotationBudget,
			Detail:  errs.ToAggregate().Error(),
		}
	}

	model.Annotations = annotations
	return nil
}

// DecodeLegacyAutoCustomSpec strictly decodes a compatibility payload.
func DecodeLegacyAutoCustomSpec(payload string) (*v1alpha1.AIMServiceModelCustom, error) {
	if errs := apivalidation.ValidateAnnotations(
		map[string]string{AnnotationLegacyAutoCustomSpec: payload},
		field.NewPath("metadata", "annotations"),
	); len(errs) > 0 {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemAnnotationBudget,
			Detail:  errs.ToAggregate().Error(),
		}
	}

	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()

	var custom v1alpha1.AIMServiceModelCustom
	if err := decoder.Decode(&custom); err != nil {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMalformedJSON,
			Detail:  err.Error(),
		}
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMalformedJSON,
			Detail:  err.Error(),
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMalformedJSON,
			Detail:  err.Error(),
		}
	}
	hardware, hardwareFound := raw["hardware"]
	if !hardwareFound || bytes.Equal(bytes.TrimSpace(hardware), []byte("null")) {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingHardware,
			Detail:  "hardware is required and must be a JSON object",
		}
	}
	if err := validateLegacyAutoCustomSpec(&custom); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(&custom)
	if err != nil {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  fmt.Sprintf("encode canonical custom model input: %v", err),
		}
	}
	if !bytes.Equal([]byte(payload), canonical) {
		return nil, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemNonCanonicalPayload,
			Detail:  "compatibility payload must exactly match the canonical encoding",
		}
	}

	return &custom, nil
}

// DecodeLegacyAutoCustomModel decodes and verifies the v1alpha2 fields that
// redundantly anchor a compatibility AIMModel.
func DecodeLegacyAutoCustomModel(model *v1alpha2.AIMModel) (*v1alpha1.AIMServiceModelCustom, bool, error) {
	if model == nil {
		return nil, false, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  "model is nil",
		}
	}
	payload, found := model.Annotations[AnnotationLegacyAutoCustomSpec]
	if !found {
		if _, grandfathered := grandfatheredLegacyAutoCustomSpec(model); grandfathered {
			// Models created by the predecessor already persist the complete
			// legacy spec, so the legacy reconciler can consume them directly.
			return nil, false, nil
		}
		if model.Labels[constants.LabelKeyCustomModel] == "true" ||
			strings.TrimSpace(model.Labels[constants.LabelKeyService]) != "" {
			return nil, true, &LegacyAutoCustomSpecValidationError{
				Problem: LegacyAutoCustomSpecProblemMissingPayload,
				Detail:  "legacy auto-custom provenance labels exist without a compatibility payload",
			}
		}
		return nil, false, nil
	}

	custom, err := DecodeLegacyAutoCustomSpec(payload)
	if err != nil {
		return nil, true, err
	}
	if model.Spec.Image != custom.BaseImage {
		return nil, true, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemContradictoryModel,
			Detail:  "spec.image does not match the encoded base image",
		}
	}
	if len(model.Spec.ModelSources) != 0 || model.Spec.Custom != nil {
		return nil, true, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemContradictoryModel,
			Detail:  "stored v1alpha2 model contains predecessor-only custom or model-source fields",
		}
	}

	return custom, true, nil
}

// ValidateGrandfatheredLegacyAutoCustomModel reports whether a predecessor
// auto-custom model exactly matches the AIMService inputs that created it.
func ValidateGrandfatheredLegacyAutoCustomModel(
	model *v1alpha2.AIMModel,
	service *v1alpha1.AIMService,
	custom *v1alpha1.AIMServiceModelCustom,
) (bool, error) {
	storedCustom, grandfathered := grandfatheredLegacyAutoCustomSpec(model)
	if !grandfathered {
		return false, nil
	}

	serviceName, err := LegacyAutoCustomSourceName(model)
	if err != nil {
		return true, err
	}
	if service == nil || service.Namespace != model.Namespace || service.Name != serviceName {
		return true, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemProvenanceMismatch,
			Detail:  "source AIMService identity does not match model provenance",
		}
	}

	storedEncoded, err := EncodeLegacyAutoCustomSpec(storedCustom)
	if err != nil {
		return true, err
	}
	sourceEncoded, err := EncodeLegacyAutoCustomSpec(custom)
	if err != nil || storedEncoded != sourceEncoded {
		return true, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemSourceMismatch,
			Detail:  "stored custom model input does not match the source AIMService",
		}
	}

	expected := v1alpha1.AIMModelSpec{
		Image:        custom.BaseImage,
		ModelSources: custom.ModelSources,
		Custom: &v1alpha1.AIMCustomModelSpec{
			Hardware:      &custom.Hardware,
			VersionPolicy: model.Spec.Custom.VersionPolicy,
		},
		RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
		ImagePullSecrets:   service.Spec.ImagePullSecrets,
		ServiceAccountName: service.Spec.ServiceAccountName,
		Env:                service.Spec.Env,
	}
	if !equality.Semantic.DeepEqual(model.Spec, expected) {
		return true, &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemContradictoryModel,
			Detail:  "stored predecessor model fields do not exactly match the source AIMService",
		}
	}
	return true, nil
}

func grandfatheredLegacyAutoCustomSpec(model *v1alpha2.AIMModel) (*v1alpha1.AIMServiceModelCustom, bool) {
	if model == nil || len(model.OwnerReferences) != 0 {
		return nil, false
	}
	if _, annotated := model.Annotations[AnnotationLegacyAutoCustomSpec]; annotated {
		return nil, false
	}
	if _, err := LegacyAutoCustomSourceName(model); err != nil {
		return nil, false
	}
	if model.Spec.Custom == nil ||
		model.Spec.Custom.Hardware == nil ||
		model.Spec.Custom.Type != nil ||
		(model.Spec.Custom.VersionPolicy != "" &&
			model.Spec.Custom.VersionPolicy != v1alpha1.AIMVersionPolicyPinned) {
		return nil, false
	}

	custom := &v1alpha1.AIMServiceModelCustom{
		BaseImage:    model.Spec.Image,
		ModelSources: deepCopyModelSources(model.Spec.ModelSources),
		Hardware:     *model.Spec.Custom.Hardware.DeepCopy(),
	}
	if err := validateLegacyAutoCustomSpec(custom); err != nil {
		return nil, false
	}

	expected := v1alpha1.AIMModelSpec{
		Image:        custom.BaseImage,
		ModelSources: model.Spec.ModelSources,
		Custom: &v1alpha1.AIMCustomModelSpec{
			Hardware:      model.Spec.Custom.Hardware,
			VersionPolicy: model.Spec.Custom.VersionPolicy,
		},
		RuntimeConfigRef:   model.Spec.RuntimeConfigRef,
		ImagePullSecrets:   model.Spec.ImagePullSecrets,
		ServiceAccountName: model.Spec.ServiceAccountName,
		Env:                model.Spec.Env,
	}
	if !equality.Semantic.DeepEqual(model.Spec, expected) {
		return nil, false
	}
	return custom, true
}

// LegacyAutoCustomSourceName returns the service identity encoded by the
// complete predecessor provenance label set.
func LegacyAutoCustomSourceName(model *v1alpha2.AIMModel) (string, error) {
	if model == nil {
		return "", &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  "model is nil",
		}
	}
	if model.Labels[constants.LabelKeyOrigin] != constants.LabelValueOriginAutoGenerated ||
		model.Labels[constants.LabelKeyCustomModel] != "true" {
		return "", &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingProvenance,
			Detail:  "auto-generated and custom-model provenance labels are required",
		}
	}
	serviceName := strings.TrimSpace(model.Labels[constants.LabelKeyService])
	if serviceName == "" {
		return "", &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingProvenance,
			Detail:  "source AIMService label is required",
		}
	}
	return serviceName, nil
}

// ValidateLegacyAutoCustomSource requires a live, UID-bound source AIMService
// whose complete predecessor input exactly matches the compatibility model.
func ValidateLegacyAutoCustomSource(
	model *v1alpha2.AIMModel,
	service *v1alpha1.AIMService,
	custom *v1alpha1.AIMServiceModelCustom,
) error {
	serviceName, err := LegacyAutoCustomSourceName(model)
	if err != nil {
		return err
	}
	if service == nil || service.Namespace != model.Namespace || service.Name != serviceName {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemProvenanceMismatch,
			Detail:  "source AIMService identity does not match model provenance",
		}
	}
	controller := metav1.GetControllerOf(model)
	if len(model.OwnerReferences) != 1 ||
		controller == nil ||
		!isSupportedAIMServiceOwnerAPIVersion(controller.APIVersion) ||
		controller.Kind != "AIMService" ||
		controller.Name != service.Name ||
		controller.UID != service.UID ||
		controller.BlockOwnerDeletion == nil ||
		!*controller.BlockOwnerDeletion {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemProvenanceMismatch,
			Detail:  "sole deletion-protected controller owner does not match the live source AIMService UID",
		}
	}
	if service.Spec.Model.Custom == nil {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemSourceMismatch,
			Detail:  "source AIMService no longer has an inline custom model",
		}
	}

	encoded, err := EncodeLegacyAutoCustomSpec(custom)
	if err != nil {
		return err
	}
	sourceEncoded, err := EncodeLegacyAutoCustomSpec(service.Spec.Model.Custom)
	if err != nil {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemSourceMismatch,
			Detail:  fmt.Sprintf("source AIMService custom model is invalid: %v", err),
		}
	}
	if encoded != sourceEncoded {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemSourceMismatch,
			Detail:  "encoded custom model input does not match the live source AIMService",
		}
	}

	expected := v1alpha1.AIMModelSpec{
		Image:              custom.BaseImage,
		RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
		ImagePullSecrets:   service.Spec.ImagePullSecrets,
		ServiceAccountName: service.Spec.ServiceAccountName,
		Env:                service.Spec.Env,
	}
	if !equality.Semantic.DeepEqual(model.Spec, expected) {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemContradictoryModel,
			Detail:  "stored v1alpha2 model fields do not exactly match the live source AIMService",
		}
	}

	return nil
}

func isSupportedAIMServiceOwnerAPIVersion(apiVersion string) bool {
	return apiVersion == v1alpha1.GroupVersion.String() ||
		apiVersion == v1alpha2.GroupVersion.String()
}

// HydrateLegacyAutoCustomSpec restores predecessor-only fields on an in-memory
// deep copy after strict payload and live-source provenance validation.
func HydrateLegacyAutoCustomSpec(
	model *v1alpha2.AIMModel,
	service *v1alpha1.AIMService,
) (*v1alpha2.AIMModel, bool, error) {
	custom, found, err := DecodeLegacyAutoCustomModel(model)
	if err != nil || !found {
		return nil, found, err
	}
	if err := ValidateLegacyAutoCustomSource(model, service, custom); err != nil {
		return nil, true, err
	}

	hydrated := model.DeepCopy()
	hydrated.Spec.ModelSources = deepCopyModelSources(custom.ModelSources)
	hydrated.Spec.Custom = &v1alpha1.AIMCustomModelSpec{
		Hardware: custom.Hardware.DeepCopy(),
	}
	return hydrated, true, nil
}

func validateLegacyAutoCustomSpec(custom *v1alpha1.AIMServiceModelCustom) error {
	if custom == nil {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemInvalidShape,
			Detail:  "custom model input is nil",
		}
	}
	if strings.TrimSpace(custom.BaseImage) == "" {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingBaseImage,
			Detail:  "baseImage is required",
		}
	}
	if len(custom.ModelSources) != 1 {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingModelSources,
			Detail:  "exactly one model source is supported",
		}
	}
	if strings.TrimSpace(custom.ModelSources[0].ModelID) == "" {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingModelID,
			Detail:  "modelSources[0].modelId is required",
		}
	}
	if !legacyModelIDPattern.MatchString(custom.ModelSources[0].ModelID) {
		return invalidLegacyAutoCustomShape(
			"modelSources[0].modelId does not satisfy the predecessor API pattern",
		)
	}
	if strings.TrimSpace(custom.ModelSources[0].SourceURI) == "" {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemMissingSourceURI,
			Detail:  "modelSources[0].sourceUri is required",
		}
	}
	if !legacySourceURIPattern.MatchString(custom.ModelSources[0].SourceURI) {
		return invalidLegacyAutoCustomShape(
			"modelSources[0].sourceUri must use the predecessor hf:// or s3:// shape",
		)
	}
	switch custom.ModelSources[0].Precision {
	case "",
		v1alpha1.AIMPrecisionFP4,
		v1alpha1.AIMPrecisionFP8,
		v1alpha1.AIMPrecisionFP16,
		v1alpha1.AIMPrecisionFP32,
		v1alpha1.AIMPrecisionFP64,
		v1alpha1.AIMPrecisionBF16,
		v1alpha1.AIMPrecisionInt4,
		v1alpha1.AIMPrecisionInt8:
	default:
		return invalidLegacyAutoCustomShape(
			"modelSources[0].precision is not permitted by the predecessor API",
		)
	}
	if custom.Hardware.GPU == nil && custom.Hardware.CPU == nil {
		return invalidLegacyAutoCustomShape(
			"hardware must specify at least one of gpu or cpu",
		)
	}
	if gpu := custom.Hardware.GPU; gpu != nil {
		if gpu.Requests < 0 {
			return invalidLegacyAutoCustomShape("hardware.gpu.requests must be non-negative")
		}
		if utf8.RuneCountInString(gpu.Model) > 64 {
			return invalidLegacyAutoCustomShape("hardware.gpu.model exceeds the predecessor API limit")
		}
		if gpu.Model != "" && gpu.MinVRAM != nil {
			return invalidLegacyAutoCustomShape(
				"hardware.gpu.model and hardware.gpu.minVram are mutually exclusive",
			)
		}
	}
	environmentNames := make(map[string]struct{}, len(custom.ModelSources[0].Env))
	for _, environment := range custom.ModelSources[0].Env {
		if _, exists := environmentNames[environment.Name]; exists {
			return invalidLegacyAutoCustomShape(
				"modelSources[0].env names must be unique",
			)
		}
		environmentNames[environment.Name] = struct{}{}
	}
	if credentialBearingURI(custom.BaseImage) ||
		credentialBearingURI(custom.ModelSources[0].SourceURI) {
		return &LegacyAutoCustomSpecValidationError{
			Problem: LegacyAutoCustomSpecProblemCredentialLiteral,
			Detail:  "inline custom image or model-source URI contains credential material; use a Secret reference",
		}
	}
	for _, environment := range custom.ModelSources[0].Env {
		if environment.Value != "" && credentialFieldName(environment.Name) {
			return &LegacyAutoCustomSpecValidationError{
				Problem: LegacyAutoCustomSpecProblemCredentialLiteral,
				Detail:  "inline custom model-source environment contains a credential literal; use valueFrom.secretKeyRef",
			}
		}
	}
	return nil
}

var (
	legacyModelIDPattern   = regexp.MustCompile(`^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$`)
	legacySourceURIPattern = regexp.MustCompile(`^(hf|s3)://[^ \t\r\n]+$`)
)

func invalidLegacyAutoCustomShape(detail string) error {
	return &LegacyAutoCustomSpecValidationError{
		Problem: LegacyAutoCustomSpecProblemInvalidShape,
		Detail:  detail,
	}
}

func credentialBearingURI(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key, values := range parsed.Query() {
		if credentialFieldName(key) && len(values) > 0 {
			return true
		}
	}
	return false
}

func credentialFieldName(value string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(value))
	for _, marker := range []string{
		"accesskey",
		"accesstoken",
		"apikey",
		"authorization",
		"bearertoken",
		"clientsecret",
		"credential",
		"password",
		"passwd",
		"privatekey",
		"secretkey",
		"sessiontoken",
		"signature",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "sig" ||
		normalized == "token" ||
		strings.HasSuffix(normalized, "token")
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

func deepCopyModelSources(sources []v1alpha1.AIMModelSource) []v1alpha1.AIMModelSource {
	if sources == nil {
		return nil
	}
	copy := make([]v1alpha1.AIMModelSource, len(sources))
	for i := range sources {
		sources[i].DeepCopyInto(&copy[i])
	}
	return copy
}
