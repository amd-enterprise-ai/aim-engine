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

// Package profileyaml defines the versioned wire-format contract carried from
// an AIM image's source profile YAML to the profile YAML AIM Engine projects at
// runtime. Controllers keep using the canonical AIMProfile CRD; only this
// boundary understands source field presence and unmodeled runtime extensions.
package profileyaml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// AnnotationContract persists the source profile's runtime-YAML wire
	// contract on AIMProfile and AIMClusterProfile objects. Derived profiles
	// inherit it so runtime projection can reproduce the source format without
	// inspecting or parsing the deployment image version.
	AnnotationContract = constants.AimLabelDomain + "/profile-yaml-contract"

	// CodecV1 is the current AIM ProfileData / ModelProfileData document shape.
	// Legacy gpu fields, transitional accelerator fields, and the strict current
	// schema are field-presence variants of this same structural codec.
	CodecV1 = "aim-profile/v1"

	// MaxContractAnnotationBytes leaves ample room under Kubernetes' aggregate
	// 256 KiB annotation limit for the profile's other controller and user
	// annotations. If real source profiles outgrow this bound, the extension
	// payload needs a referenced object rather than a larger annotation.
	MaxContractAnnotationBytes = 64 * 1024

	fieldEngine                      = "engine"
	fieldGPU                         = "gpu"
	fieldGPUCount                    = "gpu_count"
	fieldAcceleratorModel            = "accelerator_model"
	fieldAcceleratorType             = "accelerator_type"
	fieldAcceleratorCount            = "accelerator_count"
	fieldAcceleratorVendor           = "accelerator_vendor"
	fieldAcceleratorPartitioningMode = "accelerator_partitioning_mode"
	fieldManualSelectionOnly         = "manual_selection_only"
	fieldMetric                      = "metric"
	fieldPrecision                   = "precision"
	fieldType                        = "type"
	fieldVariant                     = "variant"
	fieldFeatures                    = "features"
	fieldPrimary                     = "primary"
	fieldAutoSelectionPolicy         = "auto_selection_policy"
)

var (
	v1CoreMetadataFields = []string{
		fieldEngine,
		fieldMetric,
		fieldPrecision,
		fieldType,
	}

	v1OwnedMetadataFields = map[string]struct{}{
		fieldEngine:                      {},
		fieldGPU:                         {},
		fieldGPUCount:                    {},
		fieldAcceleratorModel:            {},
		fieldAcceleratorType:             {},
		fieldAcceleratorCount:            {},
		fieldAcceleratorVendor:           {},
		fieldAcceleratorPartitioningMode: {},
		fieldManualSelectionOnly:         {},
		fieldMetric:                      {},
		fieldPrecision:                   {},
		fieldType:                        {},
		fieldVariant:                     {},
		fieldFeatures:                    {},
		fieldPrimary:                     {},
		fieldAutoSelectionPolicy:         {},
	}

	v1OwnedTopLevelFields = map[string]struct{}{
		"aim_id":      {},
		"model_id":    {},
		"metadata":    {},
		"engine_args": {},
		"env_vars":    {},
	}
)

// Extensions contains source-owned fields that AIM Engine does not model.
// Metadata additions such as runtime capabilities survive here. TopLevel is
// reserved for future additive ProfileData fields. Values are semantic JSON;
// YAML comments and ordering are intentionally not part of the contract.
type Extensions struct {
	Metadata map[string]any `json:"metadata,omitempty"`
	TopLevel map[string]any `json:"topLevel,omitempty"`
}

// Contract identifies the source wire codec, records presence for fields AIM
// Engine owns, and carries unmodeled source extensions as canonical JSON.
//
// Its representation is private so contracts can only come from source
// inspection, annotation parsing, or the canonical default. That keeps Encode
// infallible for controller call sites while Parse remains the validation
// boundary for persisted data.
type Contract struct {
	codec          string
	metadataFields []string
	extensionsJSON string
}

type wireContract struct {
	Codec          string      `json:"codec"`
	MetadataFields []string    `json:"metadataFields,omitempty"`
	Extensions     *Extensions `json:"extensions,omitempty"`
}

// DefaultContract is used for generated and hand-authored profiles that have
// no source YAML to inspect. It targets the current strict v1 accelerator
// schema and carries no source extensions.
func DefaultContract() Contract {
	fields := append([]string(nil), v1CoreMetadataFields...)
	fields = append(fields,
		fieldAcceleratorModel,
		fieldAcceleratorType,
		fieldAcceleratorCount,
	)
	return newContract(CodecV1, fields, Extensions{})
}

// CanonicalContract returns the current strict contract for a generated or
// independently hand-authored profile. Optional canonical fields are included
// only when the profile actually supplies a value, keeping the renderer purely
// contract-driven without introducing empty optional fields.
func CanonicalContract(spec *aimv1alpha2.AIMProfileSpecCommon) Contract {
	contract := DefaultContract()
	if spec == nil {
		return contract
	}
	fields := append([]string(nil), contract.metadataFields...)
	if spec.Variant != "" {
		fields = append(fields, fieldVariant)
	}
	if len(spec.Features) > 0 {
		fields = append(fields, fieldFeatures)
	}
	return newContract(CodecV1, fields, Extensions{})
}

// Inspect detects the source wire codec from the profile document itself,
// records every AIM Engine-owned metadata field that was present, and captures
// only unmodeled fields as opaque extensions.
//
// v1 profiles may optionally declare top-level profile_schema_version: 1. Old
// profiles omit it and are recognized by their established document shape.
// Unknown explicit versions fail closed rather than silently dropping fields.
func Inspect(raw []byte) (Contract, error) {
	var profile map[string]any
	if err := yaml.Unmarshal(raw, &profile); err != nil {
		return Contract{}, fmt.Errorf("unmarshal source profile YAML: %w", err)
	}

	codec, err := detectCodec(profile)
	if err != nil {
		return Contract{}, err
	}
	switch codec {
	case CodecV1:
		contract, err := inspectV1(profile)
		if err != nil {
			return Contract{}, err
		}
		if err := validateEncodedSize(contract.Encode()); err != nil {
			return Contract{}, err
		}
		return contract, nil
	default:
		return Contract{}, fmt.Errorf("unsupported profile YAML codec %q", codec)
	}
}

// Encode returns the stable JSON annotation representation.
func (c Contract) Encode() string {
	if err := c.Validate(); err != nil {
		panic(fmt.Sprintf("encode invalid profile YAML contract: %v", err))
	}
	extensions := c.extensions()
	wire := wireContract{
		Codec:          c.codec,
		MetadataFields: append([]string(nil), c.metadataFields...),
	}
	if !extensions.empty() {
		wire.Extensions = &extensions
	}
	// Contract values only contain JSON decoded by Inspect/Parse or primitive
	// values created by this package, so wireContract is always JSON-marshalable.
	encoded, err := json.Marshal(wire)
	if err != nil {
		panic(fmt.Sprintf("marshal validated profile YAML contract: %v", err))
	}
	return string(encoded)
}

// Parse decodes an annotation value into a contract. The annotation format is
// strict: unknown envelope fields fail closed so an older operator cannot
// silently discard semantics introduced by a newer contract representation.
func Parse(value string) (Contract, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Contract{}, fmt.Errorf("profile YAML contract annotation is empty")
	}
	if err := validateEncodedSize(value); err != nil {
		return Contract{}, err
	}

	var wire wireContract
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Contract{}, fmt.Errorf("parse profile YAML contract JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Contract{}, fmt.Errorf("parse profile YAML contract JSON: %w", err)
	}
	if wire.Codec == "" {
		return Contract{}, fmt.Errorf("profile YAML contract codec is empty")
	}
	if wire.Codec != CodecV1 {
		return Contract{}, fmt.Errorf("unsupported profile YAML codec %q", wire.Codec)
	}
	if err := validateV1MetadataFields(wire.MetadataFields); err != nil {
		return Contract{}, err
	}
	extensions := Extensions{}
	if wire.Extensions != nil {
		extensions = *wire.Extensions
	}
	contract := newContract(wire.Codec, wire.MetadataFields, extensions)
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// FromAnnotations returns the persisted contract and whether the annotation was
// present. Absence is not assigned a meaning at this low-level boundary.
func FromAnnotations(annotations map[string]string) (Contract, bool, error) {
	if annotations == nil || strings.TrimSpace(annotations[AnnotationContract]) == "" {
		return Contract{}, false, nil
	}
	contract, err := Parse(annotations[AnnotationContract])
	if err != nil {
		return Contract{}, true, err
	}
	return contract, true, nil
}

// ForProfile resolves the rendering contract for a concrete profile.
// Source-derived profiles must carry the inspected contract; silently treating
// a missing annotation as the current schema can rewrite a legacy runtime
// profile before its producer has backfilled the annotation during an upgrade.
// Generated and independently hand-authored profiles deliberately use the
// current canonical contract.
func ForProfile(
	annotations map[string]string,
	origin aimv1alpha1.ProfileOrigin,
	spec *aimv1alpha2.AIMProfileSpecCommon,
) (Contract, error) {
	contract, found, err := FromAnnotations(annotations)
	if err != nil {
		return Contract{}, err
	}
	if found {
		return contract, nil
	}
	switch origin {
	case aimv1alpha1.ProfileOriginDiscovered, aimv1alpha1.ProfileOriginDerived:
		return Contract{}, fmt.Errorf("source-derived profile is missing %s", AnnotationContract)
	default:
		return CanonicalContract(spec), nil
	}
}

// EffectiveOrigin prefers the producer-stamped provenance label and falls back
// to status. Callers outside the profile reconciler use this while status is
// still converging after object creation or an operator upgrade.
func EffectiveOrigin(labels map[string]string, statusOrigin aimv1alpha1.ProfileOrigin) aimv1alpha1.ProfileOrigin {
	if labels != nil {
		switch origin := aimv1alpha1.ProfileOrigin(labels[constants.LabelKeyProfileOrigin]); origin {
		case aimv1alpha1.ProfileOriginDiscovered,
			aimv1alpha1.ProfileOriginDerived,
			aimv1alpha1.ProfileOriginGenerated,
			aimv1alpha1.ProfileOriginUserAuthored:
			return origin
		}
	}
	return statusOrigin
}

// Mark records a contract while preserving all existing annotations.
func Mark(annotations map[string]string, contract Contract) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationContract] = contract.Encode()
	return annotations
}

// Codec returns the versioned wire codec used to render this contract.
func (c Contract) Codec() string {
	return c.codec
}

// HasMetadataField reports whether an AIM Engine-owned metadata field was
// present in the source schema.
func (c Contract) HasMetadataField(name string) bool {
	fields := canonicalFields(c.metadataFields)
	index := sort.SearchStrings(fields, name)
	return index < len(fields) && fields[index] == name
}

// Extensions returns an independent copy of the source-owned extension bag.
func (c Contract) Extensions() Extensions {
	return c.extensions()
}

// Validate verifies that the contract is complete and internally consistent.
func (c Contract) Validate() error {
	if c.codec == "" {
		return fmt.Errorf("profile YAML contract codec is empty")
	}
	if c.codec != CodecV1 {
		return fmt.Errorf("unsupported profile YAML codec %q", c.codec)
	}
	if err := validateV1MetadataFields(c.metadataFields); err != nil {
		return err
	}
	if err := validateRequiredV1MetadataFields(c.metadataFields); err != nil {
		return err
	}
	extensions := c.extensions()
	for key := range extensions.Metadata {
		if c.HasMetadataField(key) {
			return fmt.Errorf("profile YAML contract metadata field %q is both modeled and opaque", key)
		}
	}
	for key := range extensions.TopLevel {
		if _, owned := v1OwnedTopLevelFields[key]; owned {
			return fmt.Errorf("profile YAML contract top-level field %q is both modeled and opaque", key)
		}
	}
	return nil
}

func inspectV1(profile map[string]any) (Contract, error) {
	metadataValue, ok := profile["metadata"]
	if !ok {
		return Contract{}, fmt.Errorf("source profile YAML has no metadata object")
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		return Contract{}, fmt.Errorf("source profile YAML metadata is %T, want object", metadataValue)
	}

	fields := make([]string, 0, len(metadata))
	extensions := emptyExtensions()
	for key, value := range metadata {
		if _, owned := v1OwnedMetadataFields[key]; owned {
			fields = append(fields, key)
			continue
		}
		extensions.Metadata[key] = value
	}
	for key, value := range profile {
		if _, owned := v1OwnedTopLevelFields[key]; owned {
			continue
		}
		extensions.TopLevel[key] = value
	}
	if err := validateRequiredV1MetadataFields(fields); err != nil {
		return Contract{}, fmt.Errorf("invalid source profile YAML: %w", err)
	}
	return newContract(CodecV1, fields, extensions), nil
}

func detectCodec(profile map[string]any) (string, error) {
	value, present := profile["profile_schema_version"]
	if !present {
		return CodecV1, nil
	}
	switch version := value.(type) {
	case float64:
		if version == 1 {
			return CodecV1, nil
		}
	case string:
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(version), "v"))
		if parsed, err := strconv.Atoi(trimmed); err == nil && parsed == 1 {
			return CodecV1, nil
		}
	}
	return "", fmt.Errorf("unsupported profile_schema_version %v", value)
}

func validateV1MetadataFields(fields []string) error {
	for _, field := range canonicalFields(fields) {
		if _, owned := v1OwnedMetadataFields[field]; !owned {
			return fmt.Errorf("unsupported %s metadata field %q", CodecV1, field)
		}
	}
	return nil
}

func validateRequiredV1MetadataFields(fields []string) error {
	fields = canonicalFields(fields)
	for _, required := range v1CoreMetadataFields {
		index := sort.SearchStrings(fields, required)
		if index >= len(fields) || fields[index] != required {
			return fmt.Errorf("%s metadata field %q is required", CodecV1, required)
		}
	}
	return nil
}

func newContract(codec string, fields []string, extensions Extensions) Contract {
	normalizeExtensions(&extensions)
	contract := Contract{
		codec:          codec,
		metadataFields: canonicalFields(fields),
	}
	if !extensions.empty() {
		// Values originate from YAML/JSON decoding, so this cannot contain
		// channels, functions, or other non-JSON Go values.
		encoded, err := json.Marshal(extensions)
		if err != nil {
			panic(fmt.Sprintf("marshal decoded profile YAML extensions: %v", err))
		}
		contract.extensionsJSON = string(encoded)
	}
	return contract
}

func (c Contract) extensions() Extensions {
	if c.extensionsJSON == "" {
		return emptyExtensions()
	}
	var extensions Extensions
	if err := json.Unmarshal([]byte(c.extensionsJSON), &extensions); err != nil {
		// extensionsJSON is private and is only populated from json.Marshal.
		panic(fmt.Sprintf("decode validated profile YAML extensions: %v", err))
	}
	normalizeExtensions(&extensions)
	return extensions
}

func canonicalFields(fields []string) []string {
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func emptyExtensions() Extensions {
	return Extensions{
		Metadata: map[string]any{},
		TopLevel: map[string]any{},
	}
}

func normalizeExtensions(extensions *Extensions) {
	if extensions.Metadata == nil {
		extensions.Metadata = map[string]any{}
	}
	if extensions.TopLevel == nil {
		extensions.TopLevel = map[string]any{}
	}
}

func (e Extensions) empty() bool {
	return len(e.Metadata) == 0 && len(e.TopLevel) == 0
}

func validateEncodedSize(value string) error {
	if len(value) > MaxContractAnnotationBytes {
		return fmt.Errorf(
			"profile YAML contract is %d bytes, exceeds the %d-byte annotation budget",
			len(value),
			MaxContractAnnotationBytes,
		)
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
