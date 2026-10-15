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

package v1alpha2_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/util/validation/field"
	kjson "sigs.k8s.io/json"
	"sigs.k8s.io/yaml"
)

func generatedSchemaForVersion(t *testing.T, resource, version string) *schema.Structural {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	for _, crdVersion := range crd.Spec.Versions {
		if crdVersion.Name != version {
			continue
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crdVersion.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
			t.Fatal(err)
		}
		structural, err := schema.NewStructural(&internal)
		if err != nil {
			t.Fatal(err)
		}
		return structural
	}
	t.Fatalf("missing %s schema for %s", version, resource)
	return nil
}

func validateYAML(t *testing.T, structural *schema.Structural, manifest string) error {
	t.Helper()
	raw, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := kjson.UnmarshalCaseSensitivePreserveInts(raw, &obj); err != nil {
		t.Fatal(err)
	}
	defaulting.Default(obj, structural)
	validator := cel.NewValidator(structural, true, 1000000)
	errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, nil, 10000000)
	return errs.ToAggregate()
}

func TestCompositionRecipeAdmission(t *testing.T) {
	structural := generatedSchemaForVersion(t, "aimcompositions", "v1alpha2")
	for _, tc := range []struct {
		name  string
		spec  string
		valid bool
	}{
		{"aggregated-mode", `mode: Aggregated`, true},
		{"prefill-decode-mode", `mode: PrefillDecode`, true},
		{"inferred-main", `main: {}`, true},
		{"inferred-prefill", `prefill: {}`, true},
		{"inferred-decode", `decode: {}`, true},
		{"profile-selector-only", `profileSelector: {engine: vllm}`, false},
		{"empty", `{}`, false},
		{"mixed-roles", `main: {}
decode: {}`, false},
		{"aggregated-decode", `mode: Aggregated
decode: {}`, false},
		{"prefill-decode-main", `mode: PrefillDecode
main: {}`, false},
		{"aggregated-transport", `main: {}
transport: {preset: local-kv}`, false},
		{"binding-ref", `main:
  profile:
    ref: {kind: AIMProfile, name: example}`, true},
		{"binding-selector", `main:
  profile:
    selector: {}`, true},
		{"binding-empty", `main:
  profile: {}`, false},
		{"binding-both", `main:
  profile:
    ref: {kind: AIMProfile, name: example}
    selector: {}`, false},
		{"base-selector", `main:
  profile:
    selector: {role: base}`, false},
		{"fixed-scaling-conflict", `decode:
  replicas: 1
  scaling:
    minReplicas: 0
    maxReplicas: 2`, false},
		{"scale-zero", `decode:
  scaling:
    minReplicas: 0
    maxReplicas: 2`, true},
		{"scaling-without-metric", `decode:
  scaling:
    maxReplicas: 2`, false},
		{"scaling-invalid-range", `decode:
  scaling:
    minReplicas: 3
    maxReplicas: 2
    metrics:
    - type: PodMetric
      podmetric:
        metric: {query: requests}
        target: {type: Value, value: "1"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "apiVersion: aim.eai.amd.com/v1alpha2\nkind: AIMComposition\nmetadata: {name: test}\nspec:\n"
			for _, line := range splitLines(tc.spec) {
				manifest += "  " + line + "\n"
			}
			err := validateYAML(t, structural, manifest)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v want=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestClusterCompositionRejectsNamespaceProfileRefs(t *testing.T) {
	structural := generatedSchemaForVersion(t, "aimclustercompositions", "v1alpha2")
	for _, tc := range []struct {
		name, refKind string
		valid         bool
	}{
		{"cluster", "AIMClusterProfile", true},
		{"namespace", "AIMProfile", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := `apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterComposition
metadata: {name: test}
spec:
  main:
    worker:
      profile:
        ref:
          kind: ` + tc.refKind + `
          name: example
`
			err := validateYAML(t, structural, manifest)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v want=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestRuntimeCompositionAdmission(t *testing.T) {
	for _, resource := range []string{"aimruntimeconfigs", "aimclusterruntimeconfigs"} {
		t.Run(resource, func(t *testing.T) {
			structural := generatedSchemaForVersion(t, resource, "v1alpha1")
			for _, tc := range []struct {
				name, composition string
				valid             bool
			}{
				{"selector-defaults", `selectorDefaults:
  common: {engine: vllm}
  prefill: {metric: throughput}
  decode: {metric: latency}`, true},
				{"transport", `defaultTransportPreset: local-kv
transportPresets:
- {name: local-kv, integration: example}`, true},
				{"duplicate-transport", `transportPresets:
- {name: local-kv, integration: first}
- {name: local-kv, integration: second}`, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					manifest := "apiVersion: aim.eai.amd.com/v1alpha1\nkind: AIMRuntimeConfig\nmetadata: {name: test}\nspec:\n  composition:\n"
					for _, line := range splitLines(tc.composition) {
						manifest += "    " + line + "\n"
					}
					err := validateYAML(t, structural, manifest)
					if (err == nil) != tc.valid {
						t.Fatalf("valid=%v want=%v: %v", err == nil, tc.valid, err)
					}
				})
			}
		})
	}
}

func TestRuntimeCompositionCRDsPassKubernetesValidation(t *testing.T) {
	for _, resource := range []string{"aimruntimeconfigs", "aimclusterruntimeconfigs"} {
		t.Run(resource, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var external apiextensionsv1.CustomResourceDefinition
			if err := yaml.Unmarshal(raw, &external); err != nil {
				t.Fatal(err)
			}
			var internal apiextensions.CustomResourceDefinition
			if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&external, &internal, nil); err != nil {
				t.Fatal(err)
			}
			for _, version := range internal.Spec.Versions {
				if version.Storage {
					internal.Status.StoredVersions = []string{version.Name}
					break
				}
			}
			if errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) > 0 {
				t.Fatal(errs.ToAggregate())
			}
		})
	}
}

func splitLines(value string) []string {
	var lines []string
	start := 0
	for i := range value {
		if value[i] == '\n' {
			lines = append(lines, value[start:i])
			start = i + 1
		}
	}
	return append(lines, value[start:])
}
