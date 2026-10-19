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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestGeneratedAIMServicePipelineCRDContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	data, err := os.ReadFile(filepath.Join(
		root,
		"config",
		"crd",
		"bases",
		"aim.eai.amd.com_aimservices.yaml",
	))
	if err != nil {
		t.Fatal(err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode AIMService CRD: %v", err)
	}

	for _, versionName := range []string{"v1alpha1", "v1alpha2"} {
		version := findCRDVersion(t, crd.Spec.Versions, versionName)
		schema := version.Schema.OpenAPIV3Schema
		statusSchema := schema.Properties["status"]
		pipelineSchema, found := statusSchema.Properties["reconcilerPipeline"]
		if !found {
			t.Fatalf("%s status.reconcilerPipeline schema is missing", versionName)
		}
		var enumValues []string
		for _, value := range pipelineSchema.Enum {
			enumValues = append(enumValues, strings.Trim(string(value.Raw), `"`))
		}
		for _, wanted := range []string{"template", "profile"} {
			if !slices.Contains(enumValues, wanted) {
				t.Errorf("%s pipeline status enum %v is missing %q", versionName, enumValues, wanted)
			}
		}
		for _, validation := range schema.XValidations {
			if strings.Contains(validation.Rule, "metadata.annotations") {
				t.Errorf(
					"%s root validation accesses metadata.annotations, which CRD CEL does not expose: %q",
					versionName,
					validation.Rule,
				)
			}
		}
	}
}

func findCRDVersion(
	t *testing.T,
	versions []apiextensionsv1.CustomResourceDefinitionVersion,
	name string,
) apiextensionsv1.CustomResourceDefinitionVersion {
	t.Helper()
	for _, version := range versions {
		if version.Name == name {
			if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
				t.Fatalf("%s schema is missing", name)
			}
			return version
		}
	}
	t.Fatalf("CRD version %s is missing", name)
	return apiextensionsv1.CustomResourceDefinitionVersion{}
}
