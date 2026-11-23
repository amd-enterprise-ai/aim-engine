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

package v1alpha1_test

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

func generatedSchema(t *testing.T, resource, version string) *schema.Structural {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_"+resource+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	for _, v := range crd.Spec.Versions {
		if v.Name != version {
			continue
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
			t.Fatal(err)
		}
		structural, err := schema.NewStructural(&internal)
		if err != nil {
			t.Fatal(err)
		}
		return structural
	}
	t.Fatalf("missing %s schema in %s", version, resource)
	return nil
}

type securityContextCase struct {
	name, spec string
	// wantErr is the start of the expected message; empty means accepted.
	wantErr string
}

func securityContextCases() []securityContextCase {
	capAdd := "securityContext.capabilities.add must be empty;"
	cases := []securityContextCase{
		{"both unset", `{}`, ""},
		{"both empty", `{podSecurityContext: {}, securityContext: {}}`, ""},
		{"restricted guide example", `
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
  supplementalGroups: [44, 110]
  seccompProfile: {type: RuntimeDefault}
securityContext:
  allowPrivilegeEscalation: false
  capabilities: {drop: [ALL]}`, ""},
		{"sysctls empty", `podSecurityContext: {sysctls: []}`, ""},
		{"sysctls", `podSecurityContext: {sysctls: [{name: kernel.shm_rmid_forced, value: "1"}]}`, "podSecurityContext.sysctls must be empty;"},
		{"privileged false", `securityContext: {privileged: false}`, ""},
		{"privileged", `securityContext: {privileged: true}`, "securityContext.privileged must not be true;"},
		{"capabilities add empty", `securityContext: {capabilities: {add: [], drop: [ALL]}}`, ""},
		{"capabilities add NET_BIND_SERVICE", `securityContext: {capabilities: {add: [NET_BIND_SERVICE]}}`, capAdd},
		{"capabilities add SYS_ADMIN", `securityContext: {capabilities: {add: [SYS_ADMIN]}}`, capAdd},
		{"procMount Default", `securityContext: {procMount: Default}`, ""},
		{"procMount Unmasked", `securityContext: {procMount: Unmasked}`, "securityContext.procMount must be Default;"},
		{"procMount empty", `securityContext: {procMount: ""}`, "securityContext.procMount must be Default;"},
	}
	for _, level := range []string{"podSecurityContext", "securityContext"} {
		seccomp := level + ".seccompProfile.type must be RuntimeDefault or Localhost;"
		appArmor := level + ".appArmorProfile.type must be RuntimeDefault or Localhost;"
		seLinuxType := level + ".seLinuxOptions.type must be empty, container_t,"
		seLinuxUserRole := level + ".seLinuxOptions.user and role must be empty;"
		hostProcess := level + ".windowsOptions.hostProcess must not be true;"
		cases = append(cases,
			securityContextCase{level + " runAsUser 0", level + `: {runAsUser: 0}`, ""},
			securityContextCase{level + " seccomp RuntimeDefault", level + `: {seccompProfile: {type: RuntimeDefault}}`, ""},
			securityContextCase{level + " seccomp Localhost", level + `: {seccompProfile: {type: Localhost, localhostProfile: p.json}}`, ""},
			securityContextCase{level + " seccomp Unconfined", level + `: {seccompProfile: {type: Unconfined}}`, seccomp},
			securityContextCase{level + " appArmor RuntimeDefault", level + `: {appArmorProfile: {type: RuntimeDefault}}`, ""},
			securityContextCase{level + " appArmor Localhost", level + `: {appArmorProfile: {type: Localhost, localhostProfile: p}}`, ""},
			securityContextCase{level + " appArmor Unconfined", level + `: {appArmorProfile: {type: Unconfined}}`, appArmor},
			securityContextCase{level + " hostProcess false", level + `: {windowsOptions: {hostProcess: false}}`, ""},
			securityContextCase{level + " hostProcess", level + `: {windowsOptions: {hostProcess: true}}`, hostProcess},
			securityContextCase{level + " seLinux type empty", level + `: {seLinuxOptions: {type: ""}}`, ""},
			securityContextCase{level + " seLinux container_t", level + `: {seLinuxOptions: {type: container_t}}`, ""},
			securityContextCase{level + " seLinux container_init_t", level + `: {seLinuxOptions: {type: container_init_t}}`, ""},
			securityContextCase{level + " seLinux container_kvm_t", level + `: {seLinuxOptions: {type: container_kvm_t}}`, ""},
			securityContextCase{level + " seLinux container_engine_t", level + `: {seLinuxOptions: {type: container_engine_t}}`, ""},
			securityContextCase{level + " seLinux level", level + `: {seLinuxOptions: {level: "s0:c1,c2"}}`, ""},
			securityContextCase{level + " seLinux spc_t", level + `: {seLinuxOptions: {type: spc_t}}`, seLinuxType},
			securityContextCase{level + " seLinux user and role empty", level + `: {seLinuxOptions: {user: "", role: ""}}`, ""},
			securityContextCase{level + " seLinux user", level + `: {seLinuxOptions: {user: system_u}}`, seLinuxUserRole},
			securityContextCase{level + " seLinux role", level + `: {seLinuxOptions: {role: system_r}}`, seLinuxUserRole},
		)
	}
	return cases
}

func TestSecurityContextAdmission(t *testing.T) {
	for _, target := range []struct{ resource, version string }{
		{"aimservices", "v1alpha1"},
		{"aimservices", "v1alpha2"},
		{"aimruntimeconfigs", "v1alpha1"},
		{"aimclusterruntimeconfigs", "v1alpha1"},
	} {
		t.Run(target.resource+"/"+target.version, func(t *testing.T) {
			structural := generatedSchema(t, target.resource, target.version)
			validator := cel.NewValidator(structural, true, 1000000)
			for _, tc := range securityContextCases() {
				t.Run(tc.name, func(t *testing.T) {
					raw, err := yaml.YAMLToJSON([]byte(tc.spec))
					if err != nil {
						t.Fatal(err)
					}
					var spec map[string]any
					if err := kjson.UnmarshalCaseSensitivePreserveInts(raw, &spec); err != nil {
						t.Fatal(err)
					}
					if target.resource == "aimservices" {
						spec["model"] = map[string]any{"name": "model"}
					}
					obj := map[string]any{"spec": spec}
					defaulting.Default(obj, structural)
					errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, nil, 10000000)
					if tc.wantErr == "" {
						if len(errs) != 0 {
							t.Fatalf("want accepted, got %v", errs)
						}
						return
					}
					if len(errs) != 1 || !strings.HasPrefix(errs[0].Detail, tc.wantErr) {
						t.Fatalf("want one error that starts with %q, got %v", tc.wantErr, errs)
					}
				})
			}
		})
	}
}
