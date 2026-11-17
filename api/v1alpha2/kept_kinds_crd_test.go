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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// TestKeptKindCRDVersionsMatch guards serving v1alpha2 without a conversion
// webhook. v1alpha1 is the only storage version, and the API server returns
// those stored objects at v1alpha2 unchanged. That stays valid only while the
// two versions have the same schema, subresources and print columns.
// v1alpha1 is deprecated with a warning that names v1alpha2; v1alpha2 is not.
func TestKeptKindCRDVersionsMatch(t *testing.T) {
	for _, kind := range []struct {
		resource string
		warning  string
	}{
		{"aimartifacts", "AIMArtifact v1alpha1 is deprecated; use aim.eai.amd.com/v1alpha2"},
		{"aimruntimeconfigs", "AIMRuntimeConfig v1alpha1 is deprecated; use aim.eai.amd.com/v1alpha2"},
		{"aimclusterruntimeconfigs", "AIMClusterRuntimeConfig v1alpha1 is deprecated; use aim.eai.amd.com/v1alpha2"},
		{"aimclustermodelsources", "AIMClusterModelSource v1alpha1 is deprecated; use aim.eai.amd.com/v1alpha2"},
	} {
		t.Run(kind.resource, func(t *testing.T) {
			crd := readCRD(t, kind.resource)
			strategy := apiextensionsv1.NoneConverter
			if crd.Spec.Conversion != nil {
				strategy = crd.Spec.Conversion.Strategy
			}
			if strategy != apiextensionsv1.NoneConverter {
				t.Errorf("conversion strategy = %q, want None", strategy)
			}

			v1alpha1 := crdVersion(t, crd, "v1alpha1")
			v1alpha2 := crdVersion(t, crd, "v1alpha2")
			var storage []string
			for _, version := range crd.Spec.Versions {
				if version.Storage {
					storage = append(storage, version.Name)
				}
			}
			if len(storage) != 1 || storage[0] != "v1alpha1" {
				t.Errorf("storage versions = %v, want [v1alpha1]", storage)
			}
			warning := ""
			if v1alpha1.DeprecationWarning != nil {
				warning = *v1alpha1.DeprecationWarning
			}
			if !v1alpha1.Deprecated || warning != kind.warning {
				t.Errorf("v1alpha1 deprecated=%v warning=%q, want true %q", v1alpha1.Deprecated, warning, kind.warning)
			}
			if v1alpha2.Deprecated || v1alpha2.DeprecationWarning != nil {
				t.Error("v1alpha2 is deprecated, want it not deprecated")
			}

			requireSame(t, "schema", v1alpha1.Schema, v1alpha2.Schema)
			requireSame(t, "subresources", v1alpha1.Subresources, v1alpha2.Subresources)
			requireSame(t, "additionalPrinterColumns", v1alpha1.AdditionalPrinterColumns, v1alpha2.AdditionalPrinterColumns)
		})
	}
}

func readCRD(t *testing.T, resource string) apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	return crd
}

func crdVersion(t *testing.T, crd apiextensionsv1.CustomResourceDefinition, name string) apiextensionsv1.CustomResourceDefinitionVersion {
	t.Helper()
	for _, version := range crd.Spec.Versions {
		if version.Name == name {
			return version
		}
	}
	t.Fatalf("%s has no %s version", crd.Name, name)
	return apiextensionsv1.CustomResourceDefinitionVersion{}
}

func requireSame(t *testing.T, what string, v1alpha1, v1alpha2 any) {
	t.Helper()
	if reflect.DeepEqual(v1alpha1, v1alpha2) {
		return
	}
	left, err := json.MarshalIndent(v1alpha1, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.MarshalIndent(v1alpha2, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Errorf("%s differs between v1alpha1 and v1alpha2\nv1alpha1:\n%s\nv1alpha2:\n%s", what, left, right)
}
