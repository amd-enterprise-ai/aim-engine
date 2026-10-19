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

package templatemigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestWritePlanDirectory_StrictModesDeterministicDocumentsAndNewPath(t *testing.T) {
	first := newPlanFixture("write")
	firstResult := runFixturePlan(t, first)
	firstDirectory := filepath.Join(t.TempDir(), "plan")
	firstPaths, err := WritePlanDirectory(firstDirectory, firstResult)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, firstDirectory, 0o700)
	assertMode(t, firstPaths.Backup, 0o600)
	assertMode(t, firstPaths.Plan, 0o600)
	if len(firstPaths.DedicatedHandoffEvidence) != 0 {
		t.Fatalf("ordinary plan evidence paths = %#v", firstPaths.DedicatedHandoffEvidence)
	}

	second := newPlanFixture("write")
	secondResult := runFixturePlan(t, second)
	secondDirectory := filepath.Join(t.TempDir(), "plan")
	secondPaths, err := WritePlanDirectory(secondDirectory, secondResult)
	if err != nil {
		t.Fatal(err)
	}
	assertFilesEqual(t, firstPaths.Plan, secondPaths.Plan)
	assertFilesEqual(t, firstPaths.Backup, secondPaths.Backup)

	if _, err := WritePlanDirectory(firstDirectory, firstResult); err == nil {
		t.Fatal("WritePlanDirectory reused an existing directory")
	}

	var document PlanDocument
	planBytes, err := os.ReadFile(firstPaths.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planBytes, &document); err != nil {
		t.Fatal(err)
	}
	if document.DocumentVersion != DocumentVersion ||
		len(document.SelectedNamespaces) != 1 ||
		document.SelectedNamespaces[0] != testNamespace {
		t.Fatalf("plan document = %#v", document)
	}
}

func TestWritePlanDirectory_WritesPrivateDedicatedHandoffEvidence(t *testing.T) {
	fixture := newPlanFixture("evidence")
	configureDedicatedPlanFixture(fixture)
	result := runFixturePlan(t, fixture)
	outputDirectory := filepath.Join(t.TempDir(), "plan")

	paths, err := WritePlanDirectory(outputDirectory, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths.DedicatedHandoffEvidence) != 1 {
		t.Fatalf("evidence paths = %#v", paths.DedicatedHandoffEvidence)
	}
	evidencePath := paths.DedicatedHandoffEvidence[0]
	if filepath.Dir(evidencePath) != outputDirectory ||
		filepath.Base(evidencePath) != dedicatedHandoffEvidenceFilename(testNamespace, fixture.service.Name) {
		t.Fatalf("evidence path = %q", evidencePath)
	}
	assertMode(t, evidencePath, 0o600)

	contents, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var evidence DedicatedHandoffEvidenceDocument
	if err := json.Unmarshal(contents, &evidence); err != nil {
		t.Fatal(err)
	}
	assertDedicatedEvidenceEnvelope(t, evidence, fixture)
	assertDedicatedServiceEvidence(t, evidence.Service, fixture)
	assertDedicatedCacheEvidence(t, evidence.SourceTemplateCache, fixture)
	assertDedicatedArtifactEvidence(t, evidence.SourceArtifact, fixture)
	assertDedicatedPVCEvidence(t, evidence.PersistentVolumeClaim, fixture)
}

func assertDedicatedEvidenceEnvelope(
	t *testing.T,
	evidence DedicatedHandoffEvidenceDocument,
	fixture *planFixture,
) {
	t.Helper()
	if evidence.DocumentVersion != DedicatedHandoffEvidenceVersion ||
		evidence.UnitID != testNamespace+"/"+fixture.service.Name ||
		evidence.ManualReason.Code != dedicatedHandoffReasonCode ||
		evidence.RunbookReference != DedicatedHandoffRunbookReference ||
		evidence.PlanningTimestamp.IsZero() ||
		evidence.PlanningTimestamp.Location() != time.UTC {
		t.Fatalf("evidence envelope = %#v", evidence)
	}
}

func assertDedicatedServiceEvidence(
	t *testing.T,
	service ApplyResourceState,
	fixture *planFixture,
) {
	t.Helper()
	if service.Identity.UID != string(fixture.service.UID) ||
		service.Status == nil ||
		*service.Status != fixture.service.Status.Status ||
		service.ObservedGeneration == nil ||
		*service.ObservedGeneration != fixture.service.Status.ObservedGeneration {
		t.Fatalf("service evidence = %#v", service)
	}
}

func assertDedicatedCacheEvidence(
	t *testing.T,
	cache ApplyResourceState,
	fixture *planFixture,
) {
	t.Helper()
	if cache.Identity.ContentDigest == "" ||
		cache.ReconciliationPaused == nil ||
		*cache.ReconciliationPaused ||
		cache.Status == nil ||
		*cache.Status != fixture.cache.Status.Status ||
		cache.ObservedGeneration == nil ||
		*cache.ObservedGeneration != fixture.cache.Status.ObservedGeneration ||
		len(cache.OwnerReferences) != 1 ||
		cache.OwnerReferences[0].Controller == nil ||
		!*cache.OwnerReferences[0].Controller ||
		cache.OwnerReferences[0].BlockOwnerDeletion == nil ||
		!*cache.OwnerReferences[0].BlockOwnerDeletion ||
		len(cache.ReportedArtifacts) != 1 ||
		cache.ReportedArtifacts[0].UID != string(fixture.artifact.UID) ||
		cache.ReportedArtifacts[0].PersistentVolumeClaim != fixture.claim.Name {
		t.Fatalf("cache evidence = %#v", cache)
	}
}

func assertDedicatedArtifactEvidence(
	t *testing.T,
	artifact ApplyResourceState,
	fixture *planFixture,
) {
	t.Helper()
	if artifact.PersistentVolumeClaim != fixture.claim.Name ||
		artifact.Status == nil ||
		*artifact.Status != fixture.artifact.Status.Status ||
		len(artifact.OwnerReferences) != 1 ||
		artifact.OwnerReferences[0].UID != string(fixture.cache.UID) {
		t.Fatalf("artifact evidence = %#v", artifact)
	}
}

func assertDedicatedPVCEvidence(
	t *testing.T,
	claim ApplyResourceState,
	fixture *planFixture,
) {
	t.Helper()
	if claim.PersistentVolumePhase == nil ||
		*claim.PersistentVolumePhase != fixture.claim.Status.Phase ||
		len(claim.OwnerReferences) != 1 ||
		claim.OwnerReferences[0].UID != string(fixture.artifact.UID) {
		t.Fatalf("PVC evidence = %#v", claim)
	}
}

func TestWritePlanDirectory_RefusesIncompleteDedicatedEvidenceBeforePublication(t *testing.T) {
	fixture := newPlanFixture("incomplete-evidence")
	configureDedicatedPlanFixture(fixture)
	result := runFixturePlan(t, fixture)
	filtered := result.backupResources[:0]
	for _, resource := range result.backupResources {
		if resource.role != "source-pvc" {
			filtered = append(filtered, resource)
		}
	}
	result.backupResources = filtered
	outputDirectory := filepath.Join(t.TempDir(), "plan")

	_, err := WritePlanDirectory(outputDirectory, result)
	if err == nil || !strings.Contains(err.Error(), "required source-pvc state is unavailable") {
		t.Fatalf("WritePlanDirectory() error = %v", err)
	}
	if _, statErr := os.Stat(outputDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("incomplete evidence published directory: %v", statErr)
	}
}

func TestWritePlanDirectory_RejectsSymlinkedParent(t *testing.T) {
	fixture := newPlanFixture("symlink-parent")
	result := runFixturePlan(t, fixture)
	base := t.TempDir()
	actualParent := filepath.Join(base, "actual")
	if err := os.Mkdir(actualParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(base, "linked")
	if err := os.Symlink(actualParent, linkedParent); err != nil {
		t.Fatal(err)
	}

	_, err := WritePlanDirectory(filepath.Join(linkedParent, "plan"), result)
	if err == nil || !strings.Contains(err.Error(), "not a non-symlink directory") {
		t.Fatalf("WritePlanDirectory() error = %v, want symlinked-parent rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(actualParent, "plan")); !os.IsNotExist(statErr) {
		t.Fatalf("plan was published through symlinked parent: %v", statErr)
	}
}

func TestVerifyStableRoot_RejectsDirectoryReplacement(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, "publication")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	root, absolute, err := openNoFollowRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = root.Close()
	}()
	moved := filepath.Join(base, "moved-publication")
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := verifyStableRoot(absolute, root); err == nil ||
		!strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("verifyStableRoot() error = %v, want replacement rejection", err)
	}
}

func TestBuildBackupYAML_SanitizesDesiredStateAndIsolatesFailures(t *testing.T) {
	controller := true
	blockDeletion := true
	service := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            "desired-state",
				"namespace":       testNamespace,
				"uid":             "object-uid",
				"resourceVersion": "22",
				"generation":      int64(4),
				"managedFields":   []any{map[string]any{"manager": "fixture"}},
				"ownerReferences": []any{map[string]any{
					"apiVersion":         "v1",
					"kind":               "ConfigMap",
					"name":               "missing-owner",
					"uid":                "owner-uid",
					"controller":         controller,
					"blockOwnerDeletion": blockDeletion,
				}},
			},
			"data": map[string]any{
				"safe":            "value",
				"token":           "fixture-token",
				"secretAccessKey": "fixture-secret-access-key",
				"embedded":        "token=fixture-embedded-token",
				"sourceURI":       "https://fixture-user:fixture-password@example.invalid/model",
			},
			"status": map[string]any{"phase": "Ready"},
		},
	}
	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "must-not-be-backed-up", Namespace: testNamespace},
		StringData: map[string]string{"token": "fixture-token"},
	}
	failing := &failingMarshalConfigMap{
		ConfigMap: corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: "unserializable", Namespace: testNamespace},
		},
	}

	backup, warnings := buildBackupYAML([]backupResource{
		{object: service, apiVersion: "v1", kind: "ConfigMap"},
		{object: secret, apiVersion: "v1", kind: "Secret"},
		{object: failing, apiVersion: "v1", kind: "ConfigMap"},
	})
	text := string(backup)
	for _, forbidden := range []string{
		"status:",
		"object-uid",
		"owner-uid",
		"resourceVersion",
		"managedFields",
		"ownerReferences",
		"fixture-token",
		"fixture-secret-access-key",
		"fixture-embedded-token",
		"fixture-password",
		"must-not-be-backed-up",
		"unserializable",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("backup contains %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "safe: value") {
		t.Fatalf("backup omitted safe desired state:\n%s", text)
	}
	if len(warnings) != 1 ||
		warnings[0].Code != "BackupSerializationFailed" {
		t.Fatalf("backup warnings = %#v", warnings)
	}
}

func TestSanitizeObject_RejectsNestedAndOpaqueCredentialLiterals(t *testing.T) {
	object := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "credential-shapes",
				"namespace": testNamespace,
				"annotations": map[string]any{
					"aim.eai.amd.com/legacy-auto-custom-spec": `{"modelSources":[{"env":[{"name":"HF_TOKEN","value":"fixture-annotation-secret"}]}]}`,
				},
			},
			"data": map[string]any{
				"engineArgs": []any{
					"--token",
					"fixture-array-secret",
				},
				"numericSecret": float64(123456),
				"signedURI":     "s3://models/model?X-Amz-Signature=fixture-signature",
				"tokenizer":     "gpt2",
				"nested": []any{
					"safe-value",
					map[string]any{"password": "fixture-nested-secret"},
				},
			},
		},
	}

	sanitized, secretLiteral, err := sanitizeObject(object, "v1", "ConfigMap")
	if err != nil {
		t.Fatal(err)
	}
	if !secretLiteral {
		t.Fatal("sanitizeObject() did not report credential literals")
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{
		"fixture-annotation-secret",
		"fixture-array-secret",
		"fixture-signature",
		"fixture-nested-secret",
		"123456",
		"legacy-auto-custom-spec",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sanitized object contains %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"tokenizer":"gpt2"`) ||
		!strings.Contains(text, `"safe-value"`) {
		t.Fatalf("sanitized object omitted safe values: %s", text)
	}
}

func TestContentDigest_NormalizesDeprecatedFalseProfileDefault(t *testing.T) {
	typed := &aimv1alpha2.AIMProfile{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       resourceKindProfile,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "profile-a",
			Namespace: testNamespace,
		},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "org/model",
				ModelId:   "org/model",
				ProfileId: "profile-a",
				Primary:   true,
			},
		},
	}
	encoded, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	raw := &unstructured.Unstructured{}
	if err := json.Unmarshal(encoded, &raw.Object); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(raw.Object, false, "spec", "manualSelectionOnly"); err != nil {
		t.Fatal(err)
	}

	typedDigest, _, err := contentDigest(typed, aimv1alpha2.GroupVersion.String(), resourceKindProfile)
	if err != nil {
		t.Fatal(err)
	}
	rawDigest, _, err := contentDigest(raw, aimv1alpha2.GroupVersion.String(), resourceKindProfile)
	if err != nil {
		t.Fatal(err)
	}
	if rawDigest != typedDigest {
		t.Fatalf("typed digest %q differs from defaulted unstructured digest %q", typedDigest, rawDigest)
	}

	if err := unstructured.SetNestedField(raw.Object, true, "spec", "manualSelectionOnly"); err != nil {
		t.Fatal(err)
	}
	trueDigest, _, err := contentDigest(raw, aimv1alpha2.GroupVersion.String(), resourceKindProfile)
	if err != nil {
		t.Fatal(err)
	}
	if trueDigest == typedDigest {
		t.Fatal("explicit manualSelectionOnly=true was incorrectly normalized away")
	}
}

func TestContentDigest_NormalizesExplicitZeroTemplateGPURequest(t *testing.T) {
	typed := &aimv1alpha1.AIMServiceTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       resourceKindServiceTemplate,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "template-a",
			Namespace: testNamespace,
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "model-a",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							ResourceName: "amd.com/gpu",
						},
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	raw := &unstructured.Unstructured{}
	if err := json.Unmarshal(encoded, &raw.Object); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(raw.Object, int64(0), "spec", "hardware", "gpu", "requests"); err != nil {
		t.Fatal(err)
	}

	typedDigest, _, err := contentDigest(
		typed,
		aimv1alpha1.GroupVersion.String(),
		resourceKindServiceTemplate,
	)
	if err != nil {
		t.Fatal(err)
	}
	rawDigest, _, err := contentDigest(
		raw,
		aimv1alpha1.GroupVersion.String(),
		resourceKindServiceTemplate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawDigest != typedDigest {
		t.Fatalf("typed digest %q differs from explicit-zero unstructured digest %q", typedDigest, rawDigest)
	}

	if err := unstructured.SetNestedField(raw.Object, int64(1), "spec", "hardware", "gpu", "requests"); err != nil {
		t.Fatal(err)
	}
	nonZeroDigest, _, err := contentDigest(
		raw,
		aimv1alpha1.GroupVersion.String(),
		resourceKindServiceTemplate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if nonZeroDigest == typedDigest {
		t.Fatal("non-zero GPU request was incorrectly normalized away")
	}
}

func TestWritePlanDirectory_PersistsBackupWarningsInPlan(t *testing.T) {
	result := &PlanResult{
		Document: PlanDocument{
			DocumentVersion:    DocumentVersion,
			SelectedNamespaces: []string{testNamespace},
		},
		backupResources: []backupResource{{
			object: &failingMarshalConfigMap{
				ConfigMap: corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
					ObjectMeta: metav1.ObjectMeta{Name: "unserializable", Namespace: testNamespace},
				},
			},
			apiVersion: "v1",
			kind:       "ConfigMap",
		}},
	}
	paths, err := WritePlanDirectory(filepath.Join(t.TempDir(), "plan"), result)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(paths.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte(`"code": "BackupSerializationFailed"`)) {
		t.Fatalf("plan does not include backup warning:\n%s", contents)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func assertFilesEqual(t *testing.T, first, second string) {
	t.Helper()
	firstContents, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondContents, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstContents, secondContents) {
		t.Fatalf("%s and %s differ", first, second)
	}
}

type failingMarshalConfigMap struct {
	corev1.ConfigMap
}

func (*failingMarshalConfigMap) MarshalJSON() ([]byte, error) {
	return nil, errors.New("fixture serialization failure")
}

func (object *failingMarshalConfigMap) DeepCopyObject() runtime.Object {
	return &failingMarshalConfigMap{ConfigMap: *object.DeepCopy()}
}
