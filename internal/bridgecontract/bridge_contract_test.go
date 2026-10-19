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

package bridgecontract_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type crdVersion struct {
	name    string
	served  bool
	storage bool
}

type goType struct {
	pkg  string
	name string
}

type releaseWorkflow struct {
	Jobs map[string]workflowJob `json:"jobs"`
}

type workflowJob struct {
	Name  string         `json:"name"`
	Steps []workflowStep `json:"steps"`
}

type workflowStep struct {
	Name string `json:"name"`
	Run  string `json:"run"`
	Uses string `json:"uses"`
}

type acceptingPatchClient struct {
	client.Client
}

func (acceptingPatchClient) Get(
	_ context.Context,
	key client.ObjectKey,
	_ client.Object,
	_ ...client.GetOption,
) error {
	return apierrors.NewNotFound(schema.GroupResource{Resource: "objects"}, key.Name)
}

func (acceptingPatchClient) Patch(
	context.Context,
	client.Object,
	client.Patch,
	...client.PatchOption,
) error {
	return nil
}

//nolint:gocyclo // Independent contract subtests intentionally share one release-gate entry point.
func TestBridgeContract(t *testing.T) {
	root := repositoryRoot(t)

	t.Run("CRD matrix", func(t *testing.T) {
		expected := map[string][]crdVersion{
			"aim.eai.amd.com_aimservices.yaml":                dualServedVersions(),
			"aim.eai.amd.com_aimmodels.yaml":                  dualServedVersions(),
			"aim.eai.amd.com_aimclustermodels.yaml":           dualServedVersions(),
			"aim.eai.amd.com_aimartifacts.yaml":               retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimclustermodelsources.yaml":     retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimruntimeconfigs.yaml":          retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimclusterruntimeconfigs.yaml":   retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimservicetemplates.yaml":        retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimclusterservicetemplates.yaml": retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimtemplatecaches.yaml":          retainedV1Alpha1Version(),
			"aim.eai.amd.com_aimprofiles.yaml":                v1Alpha2Version(),
			"aim.eai.amd.com_aimclusterprofiles.yaml":         v1Alpha2Version(),
			"aim.eai.amd.com_aimprofilesets.yaml":             v1Alpha2Version(),
			"aim.eai.amd.com_aimclusterprofilesets.yaml":      v1Alpha2Version(),
			"aim.eai.amd.com_aimprofilecaches.yaml":           v1Alpha2Version(),
		}

		crdDirectory := filepath.Join(root, "config", "crd", "bases")
		for filename, wantedVersions := range expected {
			crd := readYAML[apiextensionsv1.CustomResourceDefinition](
				t,
				filepath.Join(crdDirectory, filename),
			)
			if crd.Spec.Conversion != nil &&
				crd.Spec.Conversion.Strategy != apiextensionsv1.NoneConverter {
				t.Errorf("%s conversion strategy = %q, want None", filename, crd.Spec.Conversion.Strategy)
			}
			if len(crd.Spec.Versions) != len(wantedVersions) {
				t.Errorf("%s has %d versions, want %d", filename, len(crd.Spec.Versions), len(wantedVersions))
				continue
			}
			for index, wanted := range wantedVersions {
				actual := crd.Spec.Versions[index]
				if actual.Name != wanted.name ||
					actual.Served != wanted.served ||
					actual.Storage != wanted.storage {
					t.Errorf(
						"%s version %d = (%s, served=%t, storage=%t), want (%s, served=%t, storage=%t)",
						filename,
						index,
						actual.Name,
						actual.Served,
						actual.Storage,
						wanted.name,
						wanted.served,
						wanted.storage,
					)
				}
			}
		}
	})

	t.Run("generated storage and deprecation markers", func(t *testing.T) {
		overlapping := []struct {
			kind           string
			crd            string
			v1alpha1Source string
			v1alpha2Source string
		}{
			{
				kind:           "AIMService",
				crd:            "aim.eai.amd.com_aimservices.yaml",
				v1alpha1Source: "aimservice_types.go",
				v1alpha2Source: "aimservice_types.go",
			},
			{
				kind:           "AIMModel",
				crd:            "aim.eai.amd.com_aimmodels.yaml",
				v1alpha1Source: "aimmodel_types.go",
				v1alpha2Source: "aimmodel_types.go",
			},
			{
				kind:           "AIMClusterModel",
				crd:            "aim.eai.amd.com_aimclustermodels.yaml",
				v1alpha1Source: "aimclustermodel_types.go",
				v1alpha2Source: "aimclustermodel_types.go",
			},
		}

		for _, api := range overlapping {
			t.Run(api.kind, func(t *testing.T) {
				v1alpha1Markers := typeMarkers(
					t,
					filepath.Join(root, "api", "v1alpha1", api.v1alpha1Source),
					api.kind,
				)
				v1alpha2Markers := typeMarkers(
					t,
					filepath.Join(root, "api", "v1alpha2", api.v1alpha2Source),
					api.kind,
				)
				assertMarker(t, v1alpha1Markers, "+kubebuilder:object:root=true", true)
				assertMarker(t, v1alpha1Markers, "+kubebuilder:storageversion", false)
				assertMarker(t, v1alpha2Markers, "+kubebuilder:object:root=true", true)
				assertMarker(t, v1alpha2Markers, "+kubebuilder:storageversion", true)

				v1alpha1Warning := deprecatedWarningFromMarkers(t, v1alpha1Markers)
				if markerWithPrefix(v1alpha2Markers, "+kubebuilder:deprecatedversion") != "" {
					t.Error("v1alpha2 production API is marked deprecated")
				}

				crd := readYAML[apiextensionsv1.CustomResourceDefinition](
					t,
					filepath.Join(root, "config", "crd", "bases", api.crd),
				)
				v1alpha1Version := findCRDVersion(t, crd, "v1alpha1")
				v1alpha2Version := findCRDVersion(t, crd, "v1alpha2")
				if v1alpha1Version.Storage {
					t.Error("v1alpha1 generated version is storage without a storageversion marker")
				}
				if !v1alpha1Version.Deprecated ||
					v1alpha1Version.DeprecationWarning == nil ||
					*v1alpha1Version.DeprecationWarning != v1alpha1Warning {
					t.Errorf(
						"v1alpha1 generated deprecation = (%t, %v), want marker warning %q",
						v1alpha1Version.Deprecated,
						v1alpha1Version.DeprecationWarning,
						v1alpha1Warning,
					)
				}
				if !v1alpha2Version.Storage || v1alpha2Version.Deprecated {
					t.Errorf(
						"v1alpha2 generated storage/deprecated = %t/%t, want true/false",
						v1alpha2Version.Storage,
						v1alpha2Version.Deprecated,
					)
				}
			})
		}
	})

	t.Run("overlapping API registration", func(t *testing.T) {
		scheme := k8sruntime.NewScheme()
		if err := aimv1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := aimv1alpha2.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		assertRegisteredVersion(t, scheme, &aimv1alpha1.AIMService{}, aimv1alpha1.GroupVersion, "AIMService")
		assertRegisteredVersion(t, scheme, &aimv1alpha2.AIMService{}, aimv1alpha2.GroupVersion, "AIMService")
		assertRegisteredVersion(t, scheme, &aimv1alpha1.AIMModel{}, aimv1alpha1.GroupVersion, "AIMModel")
		assertRegisteredVersion(t, scheme, &aimv1alpha2.AIMModel{}, aimv1alpha2.GroupVersion, "AIMModel")
		assertRegisteredVersion(t, scheme, &aimv1alpha1.AIMClusterModel{}, aimv1alpha1.GroupVersion, "AIMClusterModel")
		assertRegisteredVersion(t, scheme, &aimv1alpha2.AIMClusterModel{}, aimv1alpha2.GroupVersion, "AIMClusterModel")
	})

	t.Run("v1alpha2 production roots", func(t *testing.T) {
		registrations := managerControllerRegistrations(
			t,
			filepath.Join(root, "cmd", "main.go"),
		)
		for _, controller := range []goType{
			{pkg: "sharedcontroller", name: "AIMServiceReconciler"},
			{pkg: "v1alpha2controller", name: "AIMModelReconciler"},
			{pkg: "v1alpha2controller", name: "AIMClusterModelReconciler"},
		} {
			if registrations[controller] != 1 {
				t.Errorf(
					"manager registration count for %s.%s = %d, want 1",
					controller.pkg,
					controller.name,
					registrations[controller],
				)
			}
		}
		for _, controller := range []goType{
			{pkg: "v1alpha1controller", name: "AIMModelReconciler"},
			{pkg: "v1alpha1controller", name: "AIMClusterModelReconciler"},
		} {
			if registrations[controller] != 0 {
				t.Errorf(
					"manager registers retiring production root %s.%s",
					controller.pkg,
					controller.name,
				)
			}
		}

		assertControllerRoot(
			t,
			filepath.Join(root, "internal", "controller", "aimservice_controller.go"),
			goType{pkg: "aimv1alpha2", name: "AIMService"},
		)
		assertControllerRoot(
			t,
			filepath.Join(root, "internal", "v1alpha2", "controller", "aimmodel_controller.go"),
			goType{pkg: "aimv1alpha2", name: "AIMModel"},
		)
		assertControllerRoot(
			t,
			filepath.Join(root, "internal", "v1alpha2", "controller", "aimclustermodel_controller.go"),
			goType{pkg: "aimv1alpha2", name: "AIMClusterModel"},
		)
		assertNoV1Alpha1OverlappingRoots(t, root)

		fixtureDirectory := t.TempDir()
		deadRegistrationPath := filepath.Join(fixtureDirectory, "dead-registration.go")
		if err := os.WriteFile(deadRegistrationPath, []byte(`package main
func helper() {
	(&v1alpha2controller.AIMModelReconciler{}).SetupWithManager(mgr)
}
func main() {
	if false {
		if err := (&v1alpha2controller.AIMModelReconciler{}).SetupWithManager(mgr); err != nil {}
	}
}
`), 0o600); err != nil {
			t.Fatal(err)
		}
		if registrations := managerControllerRegistrations(
			t,
			deadRegistrationPath,
		); len(registrations) != 0 {
			t.Errorf("dead or unrelated manager registrations counted: %#v", registrations)
		}
		deadRootPath := filepath.Join(fixtureDirectory, "dead-root.go")
		if err := os.WriteFile(deadRootPath, []byte(`package controller
func (r *Reconciler) SetupWithManager(mgr Manager) error {
	if false {
		return ctrl.NewControllerManagedBy(mgr).For(&aimv1alpha1.AIMModel{}).Complete(r)
	}
	return nil
}
`), 0o600); err != nil {
			t.Fatal(err)
		}
		if roots := controllerRootTypes(t, deadRootPath); len(roots) != 0 {
			t.Errorf("dead controller roots counted: %#v", roots)
		}
	})

	t.Run("retained v1alpha1 APIs", func(t *testing.T) {
		scheme := k8sruntime.NewScheme()
		if err := aimv1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		retained := []k8sruntime.Object{
			&aimv1alpha1.AIMArtifact{},
			&aimv1alpha1.AIMClusterModelSource{},
			&aimv1alpha1.AIMRuntimeConfig{},
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			&aimv1alpha1.AIMServiceTemplate{},
			&aimv1alpha1.AIMClusterServiceTemplate{},
			&aimv1alpha1.AIMTemplateCache{},
		}
		for _, object := range retained {
			gvks, _, err := scheme.ObjectKinds(object)
			if err != nil {
				t.Errorf("%T is not registered: %v", object, err)
				continue
			}
			if len(gvks) != 1 || gvks[0].GroupVersion() != aimv1alpha1.GroupVersion {
				t.Errorf("%T registered as %v, want exactly %s", object, gvks, aimv1alpha1.GroupVersion)
			}
		}
	})

	t.Run("owner reference convergence", func(t *testing.T) {
		scheme := k8sruntime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := aimv1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := aimv1alpha2.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		trueValue := true
		falseValue := false
		tests := []struct {
			name               string
			kind               string
			owner              client.Object
			controller         *bool
			blockOwnerDeletion *bool
		}{
			{
				name:               "service preserves controller deletion block",
				kind:               "AIMService",
				owner:              &aimv1alpha1.AIMService{},
				controller:         &trueValue,
				blockOwnerDeletion: &trueValue,
			},
			{
				name:       "model preserves secondary ownership",
				kind:       "AIMModel",
				owner:      &aimv1alpha1.AIMModel{},
				controller: nil,
			},
			{
				name:               "cluster model preserves explicit false values",
				kind:               "AIMClusterModel",
				owner:              &aimv1alpha1.AIMClusterModel{},
				controller:         &falseValue,
				blockOwnerDeletion: &falseValue,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				test.owner.SetName("owner")
				test.owner.SetUID(types.UID("owner-uid"))
				child := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: "child",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion:         aimv1alpha1.GroupVersion.String(),
						Kind:               test.kind,
						Name:               test.owner.GetName(),
						UID:                test.owner.GetUID(),
						Controller:         test.controller,
						BlockOwnerDeletion: test.blockOwnerDeletion,
					}},
				}}

				if err := controllerutils.ApplyDesiredState(
					context.Background(),
					acceptingPatchClient{},
					"bridge-contract",
					scheme,
					[]client.Object{child},
					test.owner,
				); err != nil {
					t.Fatal(err)
				}
				if len(child.OwnerReferences) != 1 {
					t.Fatalf("owner reference count = %d, want 1", len(child.OwnerReferences))
				}
				ref := child.OwnerReferences[0]
				if ref.APIVersion != aimv1alpha2.GroupVersion.String() ||
					ref.Kind != test.kind ||
					ref.Name != test.owner.GetName() ||
					ref.UID != test.owner.GetUID() {
					t.Errorf("owner reference identity = %#v, want v1alpha2 %s", ref, test.kind)
				}
				assertBoolPointer(t, "controller", ref.Controller, test.controller)
				assertBoolPointer(
					t,
					"blockOwnerDeletion",
					ref.BlockOwnerDeletion,
					test.blockOwnerDeletion,
				)
			})
		}

		t.Run("new reference protects controller owner deletion", func(t *testing.T) {
			owner := &aimv1alpha1.AIMService{ObjectMeta: metav1.ObjectMeta{
				Name: "owner",
				UID:  types.UID("owner-uid"),
			}}
			child := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child"}}
			if err := controllerutils.ApplyDesiredState(
				context.Background(),
				acceptingPatchClient{},
				"bridge-contract",
				scheme,
				[]client.Object{child},
				owner,
			); err != nil {
				t.Fatal(err)
			}
			ref := child.OwnerReferences[0]
			if ref.Controller == nil || !*ref.Controller {
				t.Errorf("new owner reference controller = %v, want true", ref.Controller)
			}
			if ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion {
				t.Errorf(
					"new owner reference blockOwnerDeletion = %v, want true",
					ref.BlockOwnerDeletion,
				)
			}
		})
	})

	t.Run("least privilege RBAC", func(t *testing.T) {
		role := readYAML[rbacv1.ClusterRole](t, filepath.Join(root, "config", "rbac", "role.yaml"))
		if role.Name != "manager-role" || len(role.Rules) == 0 {
			t.Fatalf("operator ClusterRole = %q with %d rules, want non-empty manager-role", role.Name, len(role.Rules))
		}
		for _, rule := range role.Rules {
			if containsAny(rule.APIGroups, "*") ||
				containsAny(rule.Resources, "*") ||
				containsAny(rule.Verbs, "*") {
				t.Errorf("operator RBAC contains wildcard permission: %#v", rule)
			}
			if containsAny(rule.APIGroups, "*", apiextensionsv1.GroupName) &&
				containsAny(rule.Resources, "*", "customresourcedefinitions", "customresourcedefinitions/status") &&
				containsAny(rule.Verbs, "*", "create", "delete", "deletecollection", "patch", "update") {
				t.Errorf("operator RBAC can mutate CRDs: %#v", rule)
			}
			if containsAny(rule.APIGroups, "*", aimv1alpha1.GroupVersion.Group) &&
				containsAny(rule.Resources, "*", "aimruntimeconfigs", "aimclusterruntimeconfigs") &&
				containsAny(rule.Verbs, "*", "create", "delete", "deletecollection", "patch", "update") {
				t.Errorf("operator RBAC grants write access to read-only runtime configuration: %#v", rule)
			}
		}

		mutableAIMResources := []string{
			"aimartifacts", "aimclustermodels", "aimclustermodelsources",
			"aimclusterprofiles", "aimclusterprofilesets", "aimclusterservicetemplates",
			"aimmodels", "aimprofilecaches", "aimprofiles", "aimprofilesets",
			"aimservices", "aimservicetemplates", "aimtemplatecaches",
		}
		for _, resource := range mutableAIMResources {
			assertRBACAllows(
				t,
				role,
				aimv1alpha1.GroupVersion.Group,
				resource,
				"create",
				"delete",
				"get",
				"list",
				"patch",
				"update",
				"watch",
			)
			assertRBACAllows(
				t,
				role,
				aimv1alpha1.GroupVersion.Group,
				resource+"/status",
				"get",
				"patch",
				"update",
			)
			assertRBACAllows(
				t,
				role,
				aimv1alpha1.GroupVersion.Group,
				resource+"/finalizers",
				"update",
			)
		}
		for _, resource := range []string{"aimruntimeconfigs", "aimclusterruntimeconfigs"} {
			assertRBACAllows(
				t,
				role,
				aimv1alpha1.GroupVersion.Group,
				resource,
				"get",
				"list",
				"watch",
			)
		}
		for _, resource := range []string{"configmaps", "persistentvolumeclaims"} {
			assertRBACAllows(
				t,
				role,
				"",
				resource,
				"create",
				"delete",
				"get",
				"list",
				"patch",
				"update",
				"watch",
			)
		}
		assertRBACAllows(t, role, "", "events", "create", "get", "list", "patch", "watch")
		for _, resource := range []string{"namespaces", "nodes", "pods", "secrets"} {
			assertRBACAllows(t, role, "", resource, "get", "list", "watch")
		}
		assertRBACAllows(t, role, "", "pods/log", "get")
		assertRBACAllows(t, role, "autoscaling", "horizontalpodautoscalers", "get", "list", "watch")
		for _, resource := range []string{"jobs"} {
			assertRBACAllows(
				t,
				role,
				"batch",
				resource,
				"create",
				"delete",
				"get",
				"list",
				"patch",
				"update",
				"watch",
			)
		}
		assertRBACAllows(
			t,
			role,
			"coordination.k8s.io",
			"leases",
			"create",
			"delete",
			"get",
			"list",
			"patch",
			"update",
			"watch",
		)
		assertRBACAllows(t, role, "gateway.networking.k8s.io", "gateways", "get", "list", "watch")
		for apiGroup, resources := range map[string][]string{
			"gateway.networking.k8s.io": {"httproutes"},
			"keda.sh":                   {"scaledobjects"},
			"serving.kserve.io":         {"clusterservingruntimes", "inferenceservices", "servingruntimes"},
		} {
			for _, resource := range resources {
				assertRBACAllows(
					t,
					role,
					apiGroup,
					resource,
					"create",
					"delete",
					"get",
					"list",
					"patch",
					"update",
					"watch",
				)
			}
		}
		assertRBACAllows(
			t,
			role,
			"rbac.authorization.k8s.io",
			"rolebindings",
			"create",
			"get",
			"list",
			"patch",
			"update",
			"watch",
		)
		assertRBACAllows(t, role, "storage.k8s.io", "storageclasses", "get", "list", "watch")
	})

	t.Run("release ordering and excluded tooling", func(t *testing.T) {
		workflow := readYAML[releaseWorkflow](
			t,
			filepath.Join(root, ".github", "workflows", "compile-release.yaml"),
		)
		job, found := workflow.Jobs["compile-release"]
		if !found {
			t.Fatal("compile-release workflow has no compile-release job")
		}
		generateIndex := workflowStepIndex(job.Steps, "Generate CRDs", "")
		contractIndex := workflowStepIndex(
			job.Steps,
			"Verify minimal v1alpha1 bridge contract",
			"make test-bridge-contract",
		)
		generatedIndex := workflowStepIndex(
			job.Steps,
			"Verify generated CRD and RBAC are current",
			"git diff --exit-code -- config/crd/bases config/rbac/role.yaml",
		)
		imageIndex := workflowStepIndex(job.Steps, "Build and push Docker image", "")
		packageIndex := workflowStepIndex(job.Steps, "Package Helm chart", "")
		if generateIndex < 0 ||
			generatedIndex < 0 ||
			contractIndex < 0 ||
			imageIndex < 0 ||
			packageIndex < 0 {
			t.Fatalf(
				"release step indexes = generate:%d generated-check:%d contract:%d image:%d package:%d",
				generateIndex,
				generatedIndex,
				contractIndex,
				imageIndex,
				packageIndex,
			)
		}
		if generateIndex >= generatedIndex ||
			generatedIndex >= contractIndex ||
			contractIndex >= imageIndex ||
			contractIndex >= packageIndex {
			t.Errorf(
				"generated-output and bridge checks must precede image/chart publication; indexes = %d, %d, %d, %d, %d",
				generateIndex,
				generatedIndex,
				contractIndex,
				imageIndex,
				packageIndex,
			)
		}

		publishWorkflow := readYAML[releaseWorkflow](
			t,
			filepath.Join(root, ".github", "workflows", "publish-main.yml"),
		)
		publishJob, found := publishWorkflow.Jobs["publish"]
		if !found {
			t.Fatal("publish-main workflow has no publish job")
		}
		publishGenerateIndex := workflowStepIndex(publishJob.Steps, "Generate CRDs", "")
		publishContractIndex := workflowStepIndex(
			publishJob.Steps,
			"Verify minimal v1alpha1 bridge contract",
			"make test-bridge-contract",
		)
		publishGeneratedIndex := workflowStepIndex(
			publishJob.Steps,
			"Verify generated CRD and RBAC are current",
			"git diff --exit-code -- config/crd/bases config/rbac/role.yaml",
		)
		firstPublishIndex := workflowStepIndex(
			publishJob.Steps,
			"Build and push artifact-downloader image",
			"",
		)
		publishPackageIndex := workflowStepIndex(publishJob.Steps, "Package Helm chart", "")
		branchIndex := workflowStepIndex(
			publishJob.Steps,
			"Create or update publish-main branch",
			"",
		)
		if publishGenerateIndex < 0 ||
			publishGeneratedIndex < 0 ||
			publishContractIndex < 0 ||
			firstPublishIndex < 0 ||
			publishPackageIndex < 0 ||
			branchIndex < 0 {
			t.Fatalf(
				"publish-main step indexes = generate:%d generated-check:%d contract:%d first-publish:%d package:%d branch:%d",
				publishGenerateIndex,
				publishGeneratedIndex,
				publishContractIndex,
				firstPublishIndex,
				publishPackageIndex,
				branchIndex,
			)
		}
		if publishGenerateIndex >= publishGeneratedIndex ||
			publishGeneratedIndex >= publishContractIndex ||
			publishContractIndex >= firstPublishIndex ||
			publishContractIndex >= publishPackageIndex ||
			publishContractIndex >= branchIndex {
			t.Errorf(
				"publish-main generated-output and bridge checks must precede every image/chart/branch publication; indexes = %d, %d, %d, %d, %d, %d",
				publishGenerateIndex,
				publishGeneratedIndex,
				publishContractIndex,
				firstPublishIndex,
				publishPackageIndex,
				branchIndex,
			)
		}

		forbiddenWorkflowText := []string{
			"bridge-upgrade",
			"candidate staging",
			"candidate-staging",
			"evidence upload",
			"evidence-upload",
			"gpu qualification",
			"gpu-qualification",
			"inventory-legacy-auto-custom",
			"predecessor upgrade",
			"predecessor-upgrade",
			"removal policy",
			"removal-policy",
		}
		for jobID, workflowJob := range workflow.Jobs {
			assertNoForbiddenWorkflowText(
				t,
				"job "+jobID,
				strings.Join([]string{jobID, workflowJob.Name}, "\n"),
				forbiddenWorkflowText,
			)
			for index, step := range workflowJob.Steps {
				assertNoForbiddenWorkflowText(
					t,
					"job "+jobID+" step "+step.Name,
					strings.Join([]string{step.Name, step.Run, step.Uses}, "\n"),
					forbiddenWorkflowText,
				)
				if step.Name == "" {
					t.Errorf("job %s step %d has no name", jobID, index)
				}
			}
		}

		for _, excludedPath := range []string{
			"docs/docs/contributing/bridge-upgrade-qualification.md",
			"hack/bridge-upgrade",
			"hack/inventory-legacy-auto-custom",
		} {
			if _, err := os.Stat(filepath.Join(root, excludedPath)); err == nil {
				t.Errorf("excluded Stage 1 path exists: %s", excludedPath)
			} else if !os.IsNotExist(err) {
				t.Fatalf("inspect excluded Stage 1 path %s: %v", excludedPath, err)
			}
		}
	})

	t.Run("bounded Stage 2 utility shape", func(t *testing.T) {
		wantFiles := []string{
			"cmd/aim-template-migration/main.go",
			"internal/migration/templatemigration/apply.go",
			"internal/migration/templatemigration/document.go",
			"internal/migration/templatemigration/plan.go",
		}
		var productionFiles []string
		for _, directory := range []string{
			"cmd/aim-template-migration",
			"internal/migration/templatemigration",
		} {
			err := filepath.WalkDir(filepath.Join(root, directory), func(
				path string,
				entry fs.DirEntry,
				walkErr error,
			) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() ||
					filepath.Ext(path) != ".go" ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				productionFiles = append(productionFiles, filepath.ToSlash(relative))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		slices.Sort(productionFiles)
		if !slices.Equal(productionFiles, wantFiles) {
			t.Errorf("Stage 2 production files = %v, want exactly %v", productionFiles, wantFiles)
		}

		commandFile := parseGoFile(
			t,
			filepath.Join(root, "cmd", "aim-template-migration", "main.go"),
			0,
		)
		if commands := migrationCommandNames(t, commandFile); !slices.Equal(commands, []string{"apply", "plan"}) {
			t.Errorf("migration commands = %v, want exactly [apply plan]", commands)
		}
	})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate bridge contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readYAML[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func parseGoFile(t *testing.T, path string, mode parser.Mode) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, mode)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func assertRegisteredVersion(
	t *testing.T,
	scheme *k8sruntime.Scheme,
	object k8sruntime.Object,
	groupVersion schema.GroupVersion,
	kind string,
) {
	t.Helper()
	gvks, _, err := scheme.ObjectKinds(object)
	if err != nil {
		t.Fatalf("%T is not registered: %v", object, err)
	}
	want := groupVersion.WithKind(kind)
	if len(gvks) != 1 || gvks[0] != want {
		t.Errorf("%T registered as %v, want exactly %s", object, gvks, want)
	}
}

func assertControllerRoot(t *testing.T, path string, wanted goType) {
	t.Helper()
	roots := controllerRootTypes(t, path)
	if len(roots) != 1 || roots[0] != wanted {
		t.Errorf("%s controller roots = %#v, want exactly %#v", path, roots, wanted)
	}
}

func dualServedVersions() []crdVersion {
	return []crdVersion{
		{name: "v1alpha1", served: true, storage: false},
		{name: "v1alpha2", served: true, storage: true},
	}
}

func retainedV1Alpha1Version() []crdVersion {
	return []crdVersion{{name: "v1alpha1", served: true, storage: true}}
}

func v1Alpha2Version() []crdVersion {
	return []crdVersion{{name: "v1alpha2", served: true, storage: true}}
}

func containsAny(values []string, candidates ...string) bool {
	for _, candidate := range candidates {
		if slices.Contains(values, candidate) {
			return true
		}
	}
	return false
}

func typeMarkers(t *testing.T, path, typeName string) []string {
	t.Helper()
	file := parseGoFile(t, path, parser.ParseComments)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			var groups []*ast.CommentGroup
			if general.Doc != nil {
				groups = append(groups, general.Doc)
			}
			if typeSpec.Doc != nil {
				groups = append(groups, typeSpec.Doc)
			}
			var markers []string
			for _, group := range groups {
				for _, comment := range group.List {
					line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
					if strings.HasPrefix(line, "+kubebuilder:") {
						markers = append(markers, line)
					}
				}
			}
			return markers
		}
	}
	t.Fatalf("%s does not declare type %s", path, typeName)
	return nil
}

func assertMarker(t *testing.T, markers []string, wanted string, shouldExist bool) {
	t.Helper()
	exists := slices.Contains(markers, wanted)
	if exists != shouldExist {
		t.Errorf("marker %q exists = %t, want %t in %v", wanted, exists, shouldExist, markers)
	}
}

func markerWithPrefix(markers []string, prefix string) string {
	for _, marker := range markers {
		if strings.HasPrefix(marker, prefix) {
			return marker
		}
	}
	return ""
}

func deprecatedWarningFromMarkers(t *testing.T, markers []string) string {
	t.Helper()
	const prefix = "+kubebuilder:deprecatedversion:warning="
	marker := markerWithPrefix(markers, prefix)
	if marker == "" {
		t.Fatal("v1alpha1 API has no deprecatedversion warning marker")
	}
	warning, err := strconv.Unquote(strings.TrimPrefix(marker, prefix))
	if err != nil {
		t.Fatalf("decode deprecatedversion marker %q: %v", marker, err)
	}
	return warning
}

func findCRDVersion(
	t *testing.T,
	crd apiextensionsv1.CustomResourceDefinition,
	version string,
) apiextensionsv1.CustomResourceDefinitionVersion {
	t.Helper()
	for _, candidate := range crd.Spec.Versions {
		if candidate.Name == version {
			return candidate
		}
	}
	t.Fatalf("%s has no version %s", crd.Name, version)
	return apiextensionsv1.CustomResourceDefinitionVersion{}
}

func managerControllerRegistrations(t *testing.T, path string) map[goType]int {
	t.Helper()
	file := parseGoFile(t, path, 0)
	registrations := make(map[goType]int)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "main" || function.Body == nil {
			continue
		}
		for _, statement := range function.Body.List {
			conditional, ok := statement.(*ast.IfStmt)
			if !ok || conditional.Init == nil {
				continue
			}
			assignment, ok := conditional.Init.(*ast.AssignStmt)
			if !ok {
				continue
			}
			for _, expression := range assignment.Rhs {
				ast.Inspect(expression, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "SetupWithManager" {
						return true
					}
					controllerType, ok := pointerCompositeType(selector.X)
					if ok {
						registrations[controllerType]++
					}
					return true
				})
			}
		}
	}
	return registrations
}

func controllerRootTypes(t *testing.T, path string) []goType {
	t.Helper()
	file := parseGoFile(t, path, 0)
	var roots []goType
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok ||
			function.Name.Name != "SetupWithManager" ||
			function.Body == nil ||
			function.Recv == nil ||
			len(function.Recv.List) != 1 {
			continue
		}
		inspectExpression := func(expression ast.Expr) {
			ast.Inspect(expression, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "For" {
					return true
				}
				rootType, ok := pointerCompositeType(call.Args[0])
				if ok {
					roots = append(roots, rootType)
				}
				return true
			})
		}
		for _, statement := range function.Body.List {
			switch typed := statement.(type) {
			case *ast.AssignStmt:
				for _, expression := range typed.Rhs {
					inspectExpression(expression)
				}
			case *ast.ReturnStmt:
				for _, expression := range typed.Results {
					inspectExpression(expression)
				}
			}
		}
	}
	return roots
}

func pointerCompositeType(expression ast.Expr) (goType, bool) {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	unary, ok := expression.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return goType{}, false
	}
	composite, ok := unary.X.(*ast.CompositeLit)
	if !ok {
		return goType{}, false
	}
	selector, ok := composite.Type.(*ast.SelectorExpr)
	if !ok {
		return goType{}, false
	}
	packageIdentifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return goType{}, false
	}
	return goType{pkg: packageIdentifier.Name, name: selector.Sel.Name}, true
}

func assertNoV1Alpha1OverlappingRoots(t *testing.T, root string) {
	t.Helper()
	overlappingKinds := []string{"AIMService", "AIMModel", "AIMClusterModel"}
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == "api/v1alpha1" ||
				entry.Name() == "vendor" ||
				(path != "." && strings.HasPrefix(entry.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file := parseGoFile(t, filepath.Join(root, path), 0)
		var aliases []string
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, `"`) !=
				"github.com/amd-enterprise-ai/aim-engine/api/v1alpha1" {
				continue
			}
			alias := "v1alpha1"
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			if alias == "." {
				t.Errorf("%s dot-imports api/v1alpha1, preventing root inspection", path)
			}
			if alias != "." && alias != "_" {
				aliases = append(aliases, alias)
			}
		}
		if len(aliases) == 0 {
			return nil
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "For" {
				return true
			}
			rootType, ok := pointerCompositeType(call.Args[0])
			if ok &&
				slices.Contains(aliases, rootType.pkg) &&
				slices.Contains(overlappingKinds, rootType.name) {
				t.Errorf("%s registers retiring root %s.%s", path, rootType.pkg, rootType.name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertBoolPointer(t *testing.T, field string, actual, expected *bool) {
	t.Helper()
	switch {
	case expected == nil && actual != nil:
		t.Errorf("%s = %t, want nil", field, *actual)
	case expected != nil && (actual == nil || *actual != *expected):
		t.Errorf("%s = %v, want %t", field, actual, *expected)
	}
}

func assertRBACAllows(
	t *testing.T,
	role rbacv1.ClusterRole,
	apiGroup string,
	resource string,
	verbs ...string,
) {
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

func workflowStepIndex(steps []workflowStep, name, run string) int {
	for index, step := range steps {
		if step.Name == name && (run == "" || strings.TrimSpace(step.Run) == run) {
			return index
		}
	}
	return -1
}

func assertNoForbiddenWorkflowText(
	t *testing.T,
	location string,
	text string,
	forbidden []string,
) {
	t.Helper()
	lowerText := strings.ToLower(text)
	for _, phrase := range forbidden {
		if strings.Contains(lowerText, phrase) {
			t.Errorf("%s contains excluded Stage 1 workflow text %q", location, phrase)
		}
	}
}

func migrationCommandNames(t *testing.T, file *ast.File) []string {
	t.Helper()
	var commands []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "run" || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				name, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("decode migration command %q: %v", literal.Value, err)
				}
				switch name {
				case "help", "-h", "--help":
					continue
				default:
					commands = append(commands, name)
				}
			}
			return true
		})
	}
	slices.Sort(commands)
	return commands
}
