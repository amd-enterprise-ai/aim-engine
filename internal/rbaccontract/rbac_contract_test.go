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

package rbaccontract_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestRBACContract(t *testing.T) {
	path := filepath.Join("..", "..", "config", "rbac", "role.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if role.Name != "manager-role" || len(role.Rules) == 0 {
		t.Fatalf("operator ClusterRole = %q with %d rules, want non-empty manager-role", role.Name, len(role.Rules))
	}

	t.Run("operator role has no least-privilege violations", func(t *testing.T) {
		for _, violation := range findLeastPrivilegeViolations(role) {
			t.Error(violation)
		}
	})

	t.Run("violating rules are reported", func(t *testing.T) {
		violatingRules := []rbacv1.PolicyRule{
			{APIGroups: []string{"*"}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"*"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"*"}},
		}
		for _, verb := range []string{"create", "delete", "deletecollection", "patch", "update"} {
			violatingRules = append(violatingRules,
				rbacv1.PolicyRule{APIGroups: []string{apiextensionsv1.GroupName}, Resources: []string{"customresourcedefinitions"}, Verbs: []string{verb}},
				rbacv1.PolicyRule{APIGroups: []string{apiextensionsv1.GroupName}, Resources: []string{"customresourcedefinitions/status"}, Verbs: []string{verb}},
				rbacv1.PolicyRule{APIGroups: []string{aimv1alpha1.GroupVersion.Group}, Resources: []string{"aimruntimeconfigs"}, Verbs: []string{verb}},
				rbacv1.PolicyRule{APIGroups: []string{aimv1alpha1.GroupVersion.Group}, Resources: []string{"aimclusterruntimeconfigs"}, Verbs: []string{verb}},
			)
		}
		for _, rule := range violatingRules {
			violatingRole := rbacv1.ClusterRole{Rules: []rbacv1.PolicyRule{rule}}
			if len(findLeastPrivilegeViolations(violatingRole)) == 0 {
				t.Errorf("rule is not reported as a least-privilege violation: %#v", rule)
			}
		}
	})

	t.Run("operator role allows what the controllers need", func(t *testing.T) {
		mutableAIMResources := []string{
			"aimartifacts", "aimclustermodels", "aimclustermodelsources",
			"aimclusterprofiles", "aimclusterprofilesets", "aimclusterservicetemplates",
			"aimmodels", "aimprofilecaches", "aimprofiles", "aimprofilesets",
			"aimservices", "aimservicetemplates", "aimtemplatecaches",
		}
		for _, resource := range mutableAIMResources {
			assertRBACAllows(t, role, aimv1alpha1.GroupVersion.Group, resource, "create", "delete", "get", "list", "patch", "update", "watch")
			assertRBACAllows(t, role, aimv1alpha1.GroupVersion.Group, resource+"/status", "get", "patch", "update")
			assertRBACAllows(t, role, aimv1alpha1.GroupVersion.Group, resource+"/finalizers", "update")
		}
		for _, resource := range []string{"aimruntimeconfigs", "aimclusterruntimeconfigs"} {
			assertRBACAllows(t, role, aimv1alpha1.GroupVersion.Group, resource, "get", "list", "watch")
		}
		for _, resource := range []string{"configmaps", "persistentvolumeclaims"} {
			assertRBACAllows(t, role, "", resource, "create", "delete", "get", "list", "patch", "update", "watch")
		}
		assertRBACAllows(t, role, "", "events", "create", "get", "list", "patch", "watch")
		for _, resource := range []string{"namespaces", "nodes", "pods", "secrets"} {
			assertRBACAllows(t, role, "", resource, "get", "list", "watch")
		}
		assertRBACAllows(t, role, "", "pods/log", "get")
		assertRBACAllows(t, role, "autoscaling", "horizontalpodautoscalers", "get", "list", "watch")
		assertRBACAllows(t, role, "batch", "jobs", "create", "delete", "get", "list", "patch", "update", "watch")
		assertRBACAllows(t, role, "coordination.k8s.io", "leases", "create", "delete", "get", "list", "patch", "update", "watch")
		assertRBACAllows(t, role, "gateway.networking.k8s.io", "gateways", "get", "list", "watch")
		for apiGroup, resources := range map[string][]string{
			"gateway.networking.k8s.io": {"httproutes"},
			"keda.sh":                   {"scaledobjects"},
			"serving.kserve.io":         {"clusterservingruntimes", "inferenceservices", "servingruntimes"},
		} {
			for _, resource := range resources {
				assertRBACAllows(t, role, apiGroup, resource, "create", "delete", "get", "list", "patch", "update", "watch")
			}
		}
		assertRBACAllows(t, role, "rbac.authorization.k8s.io", "rolebindings", "create", "get", "list", "patch", "update", "watch")
		assertRBACAllows(t, role, "storage.k8s.io", "storageclasses", "get", "list", "watch")
	})
}

// findLeastPrivilegeViolations reports every rule that grants a wildcard, a
// CRD write, or a write on AIMRuntimeConfig or AIMClusterRuntimeConfig.
func findLeastPrivilegeViolations(role rbacv1.ClusterRole) []string {
	var violations []string
	for _, rule := range role.Rules {
		if containsAny(rule.APIGroups, "*") ||
			containsAny(rule.Resources, "*") ||
			containsAny(rule.Verbs, "*") {
			violations = append(violations, fmt.Sprintf("operator RBAC contains wildcard permission: %#v", rule))
		}
		if containsAny(rule.APIGroups, "*", apiextensionsv1.GroupName) &&
			containsAny(rule.Resources, "*", "customresourcedefinitions", "customresourcedefinitions/status") &&
			containsAny(rule.Verbs, "*", "create", "delete", "deletecollection", "patch", "update") {
			violations = append(violations, fmt.Sprintf("operator RBAC can mutate CRDs: %#v", rule))
		}
		if containsAny(rule.APIGroups, "*", aimv1alpha1.GroupVersion.Group) &&
			containsAny(rule.Resources, "*", "aimruntimeconfigs", "aimclusterruntimeconfigs") &&
			containsAny(rule.Verbs, "*", "create", "delete", "deletecollection", "patch", "update") {
			violations = append(violations, fmt.Sprintf("operator RBAC can write AIMRuntimeConfig or AIMClusterRuntimeConfig: %#v", rule))
		}
	}
	return violations
}

func assertRBACAllows(t *testing.T, role rbacv1.ClusterRole, apiGroup, resource string, verbs ...string) {
	t.Helper()
	for _, verb := range verbs {
		allowed := false
		for _, rule := range role.Rules {
			if containsAny(rule.APIGroups, apiGroup, "*") &&
				containsAny(rule.Resources, resource, "*") &&
				containsAny(rule.Verbs, verb, "*") {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("operator RBAC does not allow %s %s/%s", verb, apiGroup, resource)
		}
	}
}

func containsAny(values []string, candidates ...string) bool {
	for _, candidate := range candidates {
		if slices.Contains(values, candidate) {
			return true
		}
	}
	return false
}
