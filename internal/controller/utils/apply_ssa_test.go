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

package controllerutils

import (
	"context"
	"errors"
	"testing"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// These tests lock the contract that distinguishes the two shared apply entry
// points, so a future change cannot silently re-broaden the cooperative default
// back to the blanket ForceOwnership that PR #153 introduced and this PR reverted:
//
//   - ApplyDesiredState          → cooperative SSA (FieldOwner only). A field
//     another manager owns is left untouched; a conflicting write is rejected.
//   - ApplyDesiredStateWithForce → SSA + ForceOwnership. The manager reclaims a
//     field another manager owns and writes its value.
//
// The controller-runtime fake client (v0.22+) drives real structured-merge-diff
// field management via testing.NewFieldManagedObjectTracker, so it faithfully
// simulates SSA ownership, conflict rejection, and ForceOwnership reclaim
// without an API server. This keeps the test hermetic (no envtest binaries / no
// cluster) while still exercising true SSA semantics.
const (
	ssaTestNamespace    = "aim-ssa-test"
	ssaTestConfigMap    = "shared-config"
	ssaForeignManager   = "some-other-manager"
	ssaAimFieldManager  = "aim-test-controller"
	ssaForeignFieldData = "owned-by-foreign-manager"
	ssaAimFieldData     = "owned-by-aim-engine"
	ssaContendedKey     = "contended"
)

func ssaTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	if err := kservev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add KServe v1alpha1 to scheme: %v", err)
	}
	return scheme
}

func TestCreateDesiredState_AlreadyExistsPreservesConcurrentWinner(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	firstOwner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "first-owner",
			Namespace: ssaTestNamespace,
			UID:       "first-owner-uid",
		},
	}
	secondOwner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "second-owner",
			Namespace: ssaTestNamespace,
			UID:       "second-owner-uid",
		},
	}
	first := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "collision", Namespace: ssaTestNamespace},
		Data:       map[string]string{"winner": "first"},
	}
	second := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "collision", Namespace: ssaTestNamespace},
		Data:       map[string]string{"winner": "second"},
	}

	if err := CreateDesiredState(
		ctx,
		kubernetesClient,
		scheme,
		[]client.Object{first},
		firstOwner,
	); err != nil {
		t.Fatal(err)
	}
	err := CreateDesiredState(
		ctx,
		kubernetesClient,
		scheme,
		[]client.Object{second},
		secondOwner,
	)
	if err == nil || !apierrors.IsAlreadyExists(err) {
		t.Fatalf("second create error = %v, want AlreadyExists", err)
	}

	current := &corev1.ConfigMap{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{Namespace: ssaTestNamespace, Name: "collision"},
		current,
	); err != nil {
		t.Fatal(err)
	}
	if current.Data["winner"] != "first" {
		t.Fatalf("concurrent loser overwrote data: %#v", current.Data)
	}
	if len(current.OwnerReferences) != 1 ||
		current.OwnerReferences[0].UID != firstOwner.UID ||
		current.OwnerReferences[0].BlockOwnerDeletion == nil ||
		!*current.OwnerReferences[0].BlockOwnerDeletion {
		t.Fatalf("winner ownership was not preserved and protected: %#v", current.OwnerReferences)
	}
}

func TestApplyDesiredState_PreservesExistingOwnerDeletionSemanticsDuringBridgeGVKConvergence(
	t *testing.T,
) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	owner := &unstructured.Unstructured{}
	owner.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "aim.eai.amd.com",
		Version: "v1alpha1",
		Kind:    "AIMService",
	})
	owner.SetName("legacy-service")
	owner.SetNamespace(ssaTestNamespace)
	owner.SetUID(types.UID("legacy-service-uid"))
	controller := true
	existing := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       coreConfigMapKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-child",
			Namespace: ssaTestNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "aim.eai.amd.com/v1alpha1",
				Kind:       "AIMService",
				Name:       owner.GetName(),
				UID:        owner.GetUID(),
				Controller: &controller,
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := cl.Patch(
		ctx,
		existing,
		client.Apply,
		client.FieldOwner(ssaAimFieldManager),
	); err != nil {
		t.Fatalf("seed existing child: %v", err)
	}
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      existing.Name,
			Namespace: existing.Namespace,
		},
	}

	if err := ApplyDesiredState(
		ctx,
		cl,
		ssaAimFieldManager,
		scheme,
		[]client.Object{desired},
		owner,
	); err != nil {
		t.Fatal(err)
	}

	current := &corev1.ConfigMap{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(existing), current); err != nil {
		t.Fatal(err)
	}
	if len(current.OwnerReferences) != 1 {
		t.Fatalf("owner references = %#v, want one", current.OwnerReferences)
	}
	ref := current.OwnerReferences[0]
	if ref.APIVersion != "aim.eai.amd.com/v1alpha2" {
		t.Errorf("owner apiVersion = %q, want canonical bridge version", ref.APIVersion)
	}
	if ref.Controller == nil || !*ref.Controller {
		t.Errorf("controller = %v, want preserved true", ref.Controller)
	}
	if ref.BlockOwnerDeletion != nil {
		t.Errorf(
			"blockOwnerDeletion = %v, want existing nil semantics preserved",
			*ref.BlockOwnerDeletion,
		)
	}
}

func TestApplyDesiredState_OwnerSemanticsReadFailureStopsApply(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	readErr := errors.New("live child read failed")
	cl := &ownerReadFailureClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		err:    readErr,
	}
	owner := &unstructured.Unstructured{}
	owner.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "aim.eai.amd.com",
		Version: "v1alpha1",
		Kind:    "AIMService",
	})
	owner.SetName("legacy-service")
	owner.SetUID(types.UID("legacy-service-uid"))
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-child",
			Namespace: ssaTestNamespace,
		},
	}

	err := ApplyDesiredState(
		ctx,
		cl,
		ssaAimFieldManager,
		scheme,
		[]client.Object{desired},
		owner,
	)
	if !errors.Is(err, readErr) {
		t.Fatalf("apply error = %v, want owner semantics read error", err)
	}
	if cl.patchCalled {
		t.Fatal("apply continued after owner semantics read failed")
	}
}

// applyAsForeignManager takes SSA ownership of the contended field under a field
// manager that is NOT AIM Engine, creating the ConfigMap in the process.
func applyAsForeignManager(ctx context.Context, t *testing.T, cl client.Client) {
	t.Helper()
	foreign := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: coreConfigMapKind},
		ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: ssaTestConfigMap},
		Data:       map[string]string{ssaContendedKey: ssaForeignFieldData},
	}
	if err := cl.Patch(ctx, foreign, client.Apply, client.FieldOwner(ssaForeignManager)); err != nil {
		t.Fatalf("foreign manager apply failed: %v", err)
	}
}

func contendedValue(ctx context.Context, t *testing.T, cl client.Client) string {
	t.Helper()
	got := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: ssaTestNamespace, Name: ssaTestConfigMap}
	if err := cl.Get(ctx, key, got); err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	return got.Data[ssaContendedKey]
}

// aimDesiredConfigMap is the object AIM Engine wants to apply: the same
// ConfigMap, but with the contended field set to AIM Engine's value.
func aimDesiredConfigMap() []client.Object {
	return []client.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: ssaTestConfigMap},
			Data:       map[string]string{ssaContendedKey: ssaAimFieldData},
		},
	}
}

// TestApplyDesiredState_CooperativeLeavesForeignField locks the cooperative
// default: it must NOT override a field another manager owns. If the default is
// ever re-broadened to ForceOwnership, the apply would succeed and overwrite the
// foreign value — this test fails in that case.
func TestApplyDesiredState_CooperativeLeavesForeignField(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	applyAsForeignManager(ctx, t, cl)

	err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, aimDesiredConfigMap(), nil)
	if err == nil {
		t.Fatal("cooperative ApplyDesiredState succeeded against a foreign-owned field; " +
			"the default must be cooperative SSA (FieldOwner only), not ForceOwnership")
	}
	if !apierrors.IsConflict(err) {
		t.Fatalf("cooperative apply error = %v, want an SSA field-ownership conflict", err)
	}
	if got := contendedValue(ctx, t, cl); got != ssaForeignFieldData {
		t.Fatalf("contended field = %q after cooperative apply, want it left as the foreign value %q", got, ssaForeignFieldData)
	}
}

// TestApplyDesiredStateWithForce_ReclaimsForeignField locks the force path: it
// must reclaim a field another manager owns and write AIM Engine's value. If
// ApplyDesiredStateWithForce ever drops ForceOwnership, the apply would conflict
// and leave the foreign value — this test fails in that case.
func TestApplyDesiredStateWithForce_ReclaimsForeignField(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	applyAsForeignManager(ctx, t, cl)

	if err := ApplyDesiredStateWithForce(ctx, cl, ssaAimFieldManager, scheme, aimDesiredConfigMap(), nil); err != nil {
		t.Fatalf("force ApplyDesiredStateWithForce failed against a foreign-owned field: %v", err)
	}
	if got := contendedValue(ctx, t, cl); got != ssaAimFieldData {
		t.Fatalf("contended field = %q after force apply, want it reclaimed to AIM Engine's value %q", got, ssaAimFieldData)
	}
}

func TestApplyDesiredStateWithForce_StillAttemptsEveryObject(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	cl := &failConfigMapPatchClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		err:    errors.New("configmap apply failed"),
	}
	desired := []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "first"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "second"}},
	}

	if err := ApplyDesiredStateWithForce(ctx, cl, ssaAimFieldManager, scheme, desired, nil); err == nil {
		t.Fatal("ApplyDesiredStateWithForce() error = nil, want aggregate apply error")
	}
	if len(cl.patchedKinds) != len(desired) {
		t.Fatalf("Patch() calls = %d, want %d", len(cl.patchedKinds), len(desired))
	}
}

func TestApplyDesiredStateWithForceFailFast_ConfigMapFailureStopsRuntimeApply(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	configMapErr := errors.New("configmap apply failed")
	cl := &failConfigMapPatchClient{
		Client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		failName: "aim-runtime-b",
		err:      configMapErr,
	}
	desired := []client.Object{
		&kservev1alpha1.ServingRuntime{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "aim-runtime-a"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "aim-runtime-b"},
		},
		&kservev1alpha1.ServingRuntime{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "aim-runtime-b"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: "aim-runtime-a"},
		},
	}

	err := ApplyDesiredStateWithForceFailFast(ctx, cl, ssaAimFieldManager, scheme, desired, nil)
	if !errors.Is(err, configMapErr) {
		t.Fatalf("ApplyDesiredStateWithForceFailFast() error = %v, want %v", err, configMapErr)
	}
	if len(cl.patchedKinds) != 2 ||
		cl.patchedKinds[0] != coreConfigMapKind ||
		cl.patchedKinds[1] != coreConfigMapKind {
		t.Fatalf("patched kinds = %v, want both ConfigMaps and no ServingRuntime", cl.patchedKinds)
	}
}

type failConfigMapPatchClient struct {
	client.Client
	err          error
	failName     string
	patchedKinds []string
}

func (c *failConfigMapPatchClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	c.patchedKinds = append(c.patchedKinds, kind)
	if kind == coreConfigMapKind && (c.failName == "" || obj.GetName() == c.failName) {
		return c.err
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

type ownerReadFailureClient struct {
	client.Client
	err         error
	patchCalled bool
}

func (c *ownerReadFailureClient) Get(
	context.Context,
	client.ObjectKey,
	client.Object,
	...client.GetOption,
) error {
	return c.err
}

func (c *ownerReadFailureClient) Patch(
	context.Context,
	client.Object,
	client.Patch,
	...client.PatchOption,
) error {
	c.patchCalled = true
	return nil
}

// TestApplyDesiredState_ReassertsOwnField documents why cooperative SSA is the
// correct default for controllers that exclusively own their children (i.e.
// every pipeline controller that did NOT rename its field manager): cooperative
// apply still updates fields AIM Engine itself owns, so drift on owned fields is
// reconciled without force. Force is only needed to reclaim a field another
// manager owns (a field-manager rename, or the reserved-prefix projected
// runtimes handled via ApplyWithForce).
func TestApplyDesiredState_ReassertsOwnField(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	// AIM Engine first establishes ownership of the field.
	first := []client.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: ssaTestConfigMap},
			Data:       map[string]string{ssaContendedKey: ssaForeignFieldData},
		},
	}
	if err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, first, nil); err != nil {
		t.Fatalf("initial cooperative apply failed: %v", err)
	}

	// A second cooperative apply changing the same field must succeed and take
	// effect, because AIM Engine owns it — no force required.
	if err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, aimDesiredConfigMap(), nil); err != nil {
		t.Fatalf("cooperative re-apply of an AIM-owned field failed: %v", err)
	}
	if got := contendedValue(ctx, t, cl); got != ssaAimFieldData {
		t.Fatalf("contended field = %q after cooperative re-apply, want %q", got, ssaAimFieldData)
	}
}
