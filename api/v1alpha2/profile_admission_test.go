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

package v1alpha2_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/util/validation/field"
	kjson "sigs.k8s.io/json"
	"sigs.k8s.io/yaml"
)

func generatedSchema(t *testing.T, resource string) *schema.Structural {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	for _, version := range crd.Spec.Versions {
		if version.Name != "v1alpha2" {
			continue
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(version.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
			t.Fatal(err)
		}
		structural, err := schema.NewStructural(&internal)
		if err != nil {
			t.Fatal(err)
		}
		return structural
	}
	t.Fatal("missing v1alpha2 schema")
	return nil
}

func TestProfileTypeHasNoAdmissionDefault(t *testing.T) {
	for _, resource := range []string{"aimprofiles", "aimclusterprofiles"} {
		t.Run(resource, func(t *testing.T) {
			structural := generatedSchema(t, resource)
			for _, profileType := range []string{"", "optimized", "general", "preview", "unoptimized"} {
				t.Run("type="+profileType, func(t *testing.T) {
					spec := map[string]any{}
					if profileType != "" {
						spec["type"] = profileType
					}
					obj := map[string]any{"spec": spec}
					defaulting.Default(obj, structural)
					got, present := spec["type"]
					if profileType == "" {
						if present {
							t.Fatalf("omitted type was defaulted to %v", got)
						}
					} else if got != profileType {
						t.Fatalf("type = %v, want %s", got, profileType)
					}
					if spec["autoSelectionPolicy"] != "optimized" {
						t.Fatalf("autoSelectionPolicy default = %v, want optimized", spec["autoSelectionPolicy"])
					}
				})
			}
		})
	}
}

func TestModelProfileAdmission(t *testing.T) {
	for _, resource := range []string{"aimmodels", "aimclustermodels"} {
		t.Run(resource, func(t *testing.T) {
			structural := generatedSchema(t, resource)
			validator := cel.NewValidator(structural, true, 1000000)
			for _, tc := range []struct {
				name, spec string
				valid      bool
			}{
				{"image", `image: registry/model:1.0`, true},
				{"inline-all", `image: registry/model:1.0
profiles:
  overrides: {acceleratorModel: MI325X}`, false},
				{"inline-selected", `image: registry/model:1.0
profiles:
  derivedFrom:
    selector: {acceleratorCount: 1}
  overrides: {acceleratorPartitioningMode: CPX-NPS4, acceleratorCount: 1}`, false},
				{"inline-no-source-count", `image: registry/model:1.0
profiles:
  overrides: {acceleratorPartitioningMode: CPX-NPS4, acceleratorCount: 1}`, false},
				{"inline-no-target-count", `image: registry/model:1.0
profiles:
  derivedFrom:
    selector: {acceleratorCount: 1}
  overrides: {acceleratorPartitioningMode: CPX-NPS4}`, false},
				{"inline-sourceRef", `image: registry/model:1.0
profiles:
  derivedFrom:
    sourceRef: {name: cache}
    selector: {engine: vllm}`, false},
				{"inline-modelRef", `image: registry/model:1.0
profiles:
  derivedFrom:
    selector:
      modelRef: {name: other}`, false},
				{"inline-version", `image: registry/model:1.0
profiles: {version: "1.0"}`, false},
				{"inline-version-policy", `image: registry/model:1.0
profiles: {versionPolicy: all}`, false},
				{"generated-inline", `modelId: weights/model
profiles: {overrides: {acceleratorModel: MI325X}}`, false},
				{"generated-image", `modelId: weights/model
image: registry/model:1.0`, false},
				{"external-overrides-only", `profiles: {overrides: {acceleratorModel: MI325X}}`, false},
				{"external-unversioned", `profiles:
  derivedFrom:
    selector: {engine: vllm}`, false},
				{"external-pinned", `profiles:
  version: "1.0"
  derivedFrom:
    selector: {engine: vllm}`, true},
				{"external-all", `profiles:
  versionPolicy: all
  derivedFrom:
    selector: {engine: vllm}`, true},
				{"base-no-identity", `profiles:
  versionPolicy: all
  derivedFrom:
    selector: {role: base}`, false},
				{"base-identity", `profiles:
  versionPolicy: all
  derivedFrom:
    selector: {role: base}
  overrides: {aimId: architecture, modelId: weights/model}`, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var spec map[string]any
					raw, err := yaml.YAMLToJSON([]byte(tc.spec))
					if err != nil {
						t.Fatal(err)
					}
					if err := kjson.UnmarshalCaseSensitivePreserveInts(raw, &spec); err != nil {
						t.Fatal(err)
					}
					obj := map[string]any{"apiVersion": "aim.eai.amd.com/v1alpha2", "kind": "AIMModel", "metadata": map[string]any{"name": "test"}, "spec": spec}
					defaulting.Default(obj, structural)
					errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, nil, 10000000)
					if (len(errs) == 0) != tc.valid {
						t.Fatalf("valid=%v want=%v: %v", len(errs) == 0, tc.valid, errs)
					}
				})
			}
		})
	}
}

func TestProfileSetPartitioningAdmission(t *testing.T) {
	for _, resource := range []string{"aimprofilesets", "aimclusterprofilesets"} {
		structural := generatedSchema(t, resource)
		validator := cel.NewValidator(structural, true, 1000000)
		for _, sourceCount := range []bool{false, true} {
			for _, targetCount := range []bool{false, true} {
				selector := map[string]any{"acceleratorPartitioningMode": "unpartitioned"}
				overrides := map[string]any{"acceleratorPartitioningMode": "CPX-NPS4"}
				if sourceCount {
					selector["acceleratorCount"] = int64(1)
				}
				if targetCount {
					overrides["acceleratorCount"] = int64(1)
				}
				obj := map[string]any{"spec": map[string]any{"selector": selector, "overrides": overrides, "versionPolicy": "all"}}
				defaulting.Default(obj, structural)
				errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, nil, 10000000)
				if (len(errs) == 0) != targetCount {
					t.Fatalf("%s counts=%v/%v: %v", resource, sourceCount, targetCount, errs)
				}
			}
		}
	}
}

func TestProfileSourceAdmission(t *testing.T) {
	for _, resource := range []string{"aimprofilesets", "aimclusterprofilesets", "aimmodels", "aimclustermodels"} {
		t.Run(resource, func(t *testing.T) {
			structural := generatedSchema(t, resource)
			validator := cel.NewValidator(structural, true, 1000000)
			for _, tc := range []struct {
				name, source string
				valid        bool
			}{
				{"source-only", `sourceRef: {name: cache}`, true},
				{"source-empty-selector", `sourceRef: {name: cache}
selector: {}`, true},
				{"source-filtered", `sourceRef: {name: cache}
selector: {engine: vllm}`, true},
				{"model-only", `selector: {modelRef: {name: model}}`, true},
				{"source-and-model", `sourceRef: {name: cache}
selector: {modelRef: {name: model}}`, false},
				{"source-and-auto-model", `sourceRef: {name: cache}
selector: {modelRef: {name: model, scope: Auto}}`, false},
				{"source-and-namespace-model", `sourceRef: {name: cache}
selector: {modelRef: {name: model, scope: Namespace}}`, false},
				{"source-and-cluster-model", `sourceRef: {name: cache}
selector: {modelRef: {name: model, scope: Cluster}}`, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw, err := yaml.YAMLToJSON([]byte(tc.source))
					if err != nil {
						t.Fatal(err)
					}
					var source map[string]any
					if err := kjson.UnmarshalCaseSensitivePreserveInts(raw, &source); err != nil {
						t.Fatal(err)
					}
					spec := source
					if resource == "aimmodels" || resource == "aimclustermodels" {
						spec = map[string]any{"profiles": map[string]any{"derivedFrom": source, "versionPolicy": "all"}}
					} else {
						spec["versionPolicy"] = "all"
					}
					obj := map[string]any{"spec": spec}
					defaulting.Default(obj, structural)
					errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, nil, 10000000)
					if (len(errs) == 0) != tc.valid {
						t.Fatalf("valid=%v want=%v: %v", len(errs) == 0, tc.valid, errs)
					}
					if !tc.valid && !strings.Contains(errs.ToAggregate().Error(), "sourceRef and selector.modelRef are mutually exclusive") {
						t.Fatalf("missing source conflict validation: %v", errs)
					}
				})
			}
		})
	}
}
