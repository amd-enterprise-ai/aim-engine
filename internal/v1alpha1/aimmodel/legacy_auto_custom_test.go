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

package aimmodel

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestLegacyAutoCustomSpecExactRoundTrip(t *testing.T) {
	custom := testLegacyAutoCustomSpec()
	payload, err := EncodeLegacyAutoCustomSpec(custom)
	if err != nil {
		t.Fatalf("EncodeLegacyAutoCustomSpec() error = %v", err)
	}
	decoded, err := DecodeLegacyAutoCustomSpec(payload)
	if err != nil {
		t.Fatalf("DecodeLegacyAutoCustomSpec() error = %v", err)
	}
	if !equality.Semantic.DeepEqual(decoded, custom) {
		t.Fatalf("round trip changed custom input:\ngot:  %#v\nwant: %#v", decoded, custom)
	}
}

func TestLegacyAutoCustomSpecUsesKubernetesAnnotationBudget(t *testing.T) {
	custom := testLegacyAutoCustomSpec()
	custom.ModelSources[0].Env = append(custom.ModelSources[0].Env, corev1.EnvVar{
		Name:  "LARGE_BUT_API_VALID",
		Value: strings.Repeat("x", 70*1024),
	})
	if _, err := EncodeLegacyAutoCustomSpec(custom); err != nil {
		t.Fatalf("70 KiB compatibility payload should fit Kubernetes annotation budget: %v", err)
	}

	custom.ModelSources[0].Env[1].Value = strings.Repeat("x", 300*1024)
	_, err := EncodeLegacyAutoCustomSpec(custom)
	assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemAnnotationBudget)
}

func TestLegacyAutoCustomSpecRejectsCredentialLiterals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*aimv1alpha1.AIMServiceModelCustom)
	}{
		{
			name: "credential environment value",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].Env = append(custom.ModelSources[0].Env, corev1.EnvVar{
					Name:  "HF_TOKEN",
					Value: "fixture-token",
				})
			},
		},
		{
			name: "URI user information",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].SourceURI = "s3://fixture-user:fixture-password@models/model"
			},
		},
		{
			name: "signed URI query",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].SourceURI = "s3://models/model?X-Amz-Signature=fixture-signature"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			custom := testLegacyAutoCustomSpec()
			tt.mutate(custom)

			_, err := EncodeLegacyAutoCustomSpec(custom)
			assertLegacyAutoCustomProblem(
				t,
				err,
				LegacyAutoCustomSpecProblem("CredentialLiteral"),
			)
		})
	}
}

func TestLegacyAutoCustomSpecAllowsSecretReferencesAndNonCredentialLiterals(t *testing.T) {
	custom := testLegacyAutoCustomSpec()
	custom.ModelSources[0].Env = append(custom.ModelSources[0].Env,
		corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "model-download"},
					Key:                  "token",
				},
			},
		},
		corev1.EnvVar{Name: "TOKENIZER", Value: "gpt2"},
	)

	payload, err := EncodeLegacyAutoCustomSpec(custom)
	if err != nil {
		t.Fatalf("EncodeLegacyAutoCustomSpec() error = %v", err)
	}
	if strings.Contains(payload, "fixture-token") {
		t.Fatalf("compatibility payload contains a credential literal: %s", payload)
	}
	if !strings.Contains(payload, "model-download") || !strings.Contains(payload, `"TOKENIZER"`) {
		t.Fatalf("compatibility payload omitted safe references or values: %s", payload)
	}
}

func TestDecodeLegacyAutoCustomSpecRejectsHistoricalCredentialLiteral(t *testing.T) {
	payload := `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":"s3://models/b","env":[{"name":"HF_TOKEN","value":"fixture-token"}]}],"hardware":{"gpu":{}}}`

	_, err := DecodeLegacyAutoCustomSpec(payload)
	assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemCredentialLiteral)
}

func TestDecodeLegacyAutoCustomSpecRejectsNonCanonicalPayload(t *testing.T) {
	payload, err := EncodeLegacyAutoCustomSpec(testLegacyAutoCustomSpec())
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecodeLegacyAutoCustomSpec(payload + " ")
	assertLegacyAutoCustomProblem(
		t,
		err,
		LegacyAutoCustomSpecProblem("NonCanonicalPayload"),
	)
}

func TestLegacyAutoCustomSpecRejectsPredecessorConstraintViolations(t *testing.T) {
	minVRAM := resource.MustParse("64Gi")
	tests := []struct {
		name   string
		mutate func(*aimv1alpha1.AIMServiceModelCustom)
	}{
		{
			name: "invalid model ID",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].ModelID = "missing-organization"
			},
		},
		{
			name: "unsupported source scheme",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].SourceURI = "https://models.example/model"
			},
		},
		{
			name: "unsupported precision",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].Precision = aimv1alpha1.AIMPrecision("auto")
			},
		},
		{
			name: "missing hardware",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.Hardware = aimv1alpha1.AIMHardwareRequirements{}
			},
		},
		{
			name: "negative GPU requests",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.Hardware.GPU.Requests = -1
			},
		},
		{
			name: "GPU model and minimum VRAM",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.Hardware.GPU.Model = "MI300X"
				custom.Hardware.GPU.MinVRAM = &minVRAM
			},
		},
		{
			name: "GPU model too long",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.Hardware.GPU.Model = strings.Repeat("x", 65)
			},
		},
		{
			name: "duplicate model-source environment name",
			mutate: func(custom *aimv1alpha1.AIMServiceModelCustom) {
				custom.ModelSources[0].Env = append(
					custom.ModelSources[0].Env,
					corev1.EnvVar{Name: "AWS_ENDPOINT_URL", Value: "https://other.example"},
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			custom := testLegacyAutoCustomSpec()
			tt.mutate(custom)

			_, err := EncodeLegacyAutoCustomSpec(custom)
			assertLegacyAutoCustomProblem(
				t,
				err,
				LegacyAutoCustomSpecProblemInvalidShape,
			)
		})
	}
}

func TestDecodeLegacyAutoCustomSpecRejectsMalformedInput(t *testing.T) {
	valid, err := EncodeLegacyAutoCustomSpec(testLegacyAutoCustomSpec())
	if err != nil {
		t.Fatalf("encode valid fixture: %v", err)
	}
	tests := []struct {
		name    string
		payload string
		problem LegacyAutoCustomSpecProblem
	}{
		{name: "invalid JSON", payload: `{"baseImage":`, problem: LegacyAutoCustomSpecProblemMalformedJSON},
		{name: "unknown field", payload: `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":"s3://x"}],"hardware":{},"extra":true}`, problem: LegacyAutoCustomSpecProblemMalformedJSON},
		{name: "trailing value", payload: valid + `{}`, problem: LegacyAutoCustomSpecProblemMalformedJSON},
		{name: "missing base image", payload: `{"baseImage":"","modelSources":[{"modelId":"a/b","sourceUri":"s3://x"}],"hardware":{}}`, problem: LegacyAutoCustomSpecProblemMissingBaseImage},
		{name: "missing source", payload: `{"baseImage":"image","modelSources":[],"hardware":{}}`, problem: LegacyAutoCustomSpecProblemMissingModelSources},
		{name: "multiple sources", payload: `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":"s3://x"},{"modelId":"a/c","sourceUri":"s3://y"}],"hardware":{}}`, problem: LegacyAutoCustomSpecProblemMissingModelSources},
		{name: "missing model ID", payload: `{"baseImage":"image","modelSources":[{"modelId":"","sourceUri":"s3://x"}],"hardware":{}}`, problem: LegacyAutoCustomSpecProblemMissingModelID},
		{name: "missing source URI", payload: `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":""}],"hardware":{}}`, problem: LegacyAutoCustomSpecProblemMissingSourceURI},
		{name: "missing hardware", payload: `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":"s3://x"}]}`, problem: LegacyAutoCustomSpecProblemMissingHardware},
		{name: "null hardware", payload: `{"baseImage":"image","modelSources":[{"modelId":"a/b","sourceUri":"s3://x"}],"hardware":null}`, problem: LegacyAutoCustomSpecProblemMissingHardware},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeLegacyAutoCustomSpec(tt.payload)
			if err == nil {
				t.Fatal("DecodeLegacyAutoCustomSpec() error = nil")
			}
			problem, ok := LegacyAutoCustomSpecProblemOf(err)
			if !ok || problem != tt.problem {
				t.Fatalf("problem = (%q, %t), want %q; error: %v", problem, ok, tt.problem, err)
			}
		})
	}
}

func TestHydrateLegacyAutoCustomSpecValidatesProvenanceAndDoesNotMutateStoredModel(t *testing.T) {
	custom := testLegacyAutoCustomSpec()
	service := testLegacyAutoCustomService(custom)
	for _, ownerAPIVersion := range []string{
		aimv1alpha1.GroupVersion.String(),
		aimv1alpha2.GroupVersion.String(),
	} {
		t.Run(ownerAPIVersion, func(t *testing.T) {
			model := testLegacyAutoCustomModel(t, service, custom)
			model.OwnerReferences[0].APIVersion = ownerAPIVersion
			original := model.DeepCopy()

			hydrated, found, err := HydrateLegacyAutoCustomSpec(model, service)
			if err != nil || !found {
				t.Fatalf("HydrateLegacyAutoCustomSpec() = (%#v, %t, %v)", hydrated, found, err)
			}
			if !equality.Semantic.DeepEqual(hydrated.Spec.ModelSources, custom.ModelSources) {
				t.Fatalf("hydrated sources = %#v, want %#v", hydrated.Spec.ModelSources, custom.ModelSources)
			}
			if hydrated.Spec.Custom == nil || !equality.Semantic.DeepEqual(hydrated.Spec.Custom.Hardware, &custom.Hardware) {
				t.Fatalf("hydrated hardware = %#v, want %#v", hydrated.Spec.Custom, custom.Hardware)
			}
			if !equality.Semantic.DeepEqual(model, original) {
				t.Fatalf("stored model mutated during hydration:\ngot:  %#v\nwant: %#v", model, original)
			}
		})
	}
}

func TestHydrateLegacyAutoCustomSpecRejectsContradictoryAndMismatchedState(t *testing.T) {
	custom := testLegacyAutoCustomSpec()
	service := testLegacyAutoCustomService(custom)

	t.Run("missing source service", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		_, _, err := HydrateLegacyAutoCustomSpec(model, nil)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemProvenanceMismatch)
	})

	t.Run("controller owner UID mismatch", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		model.OwnerReferences[0].UID = types.UID("different-uid")
		_, _, err := HydrateLegacyAutoCustomSpec(model, service)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemProvenanceMismatch)
	})

	t.Run("controller owner permits deletion without protection", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		blockDeletion := false
		model.OwnerReferences[0].BlockOwnerDeletion = &blockDeletion
		_, _, err := HydrateLegacyAutoCustomSpec(model, service)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemProvenanceMismatch)
	})

	t.Run("additional owner reference", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		model.OwnerReferences = append(model.OwnerReferences, metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "other-owner",
			UID:        types.UID("other-owner-uid"),
		})
		_, _, err := HydrateLegacyAutoCustomSpec(model, service)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemProvenanceMismatch)
	})

	t.Run("unsupported controller owner API version", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		model.OwnerReferences[0].APIVersion = "aim.eai.amd.com/v9"
		_, _, err := HydrateLegacyAutoCustomSpec(model, service)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemProvenanceMismatch)
	})

	t.Run("stored predecessor field contradicts v1alpha2 representation", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		model.Spec.ModelSources = custom.ModelSources
		_, _, err := HydrateLegacyAutoCustomSpec(model, service)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemContradictoryModel)
	})

	t.Run("source service custom input changed", func(t *testing.T) {
		model := testLegacyAutoCustomModel(t, service, custom)
		changed := service.DeepCopy()
		changed.Spec.Model.Custom.ModelSources[0].SourceURI = "s3://models/changed"
		_, _, err := HydrateLegacyAutoCustomSpec(model, changed)
		assertLegacyAutoCustomProblem(t, err, LegacyAutoCustomSpecProblemSourceMismatch)
	})
}

func testLegacyAutoCustomSpec() *aimv1alpha1.AIMServiceModelCustom {
	minVRAM := resource.MustParse("64Gi")
	size := resource.MustParse("42Gi")
	return &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amd/aim-base:v1",
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "acme/model",
			SourceURI: "s3://models/model",
			Size:      &size,
			Env: []corev1.EnvVar{{
				Name: "AWS_ENDPOINT_URL", Value: "https://object-store.example",
			}},
		}},
		Hardware: aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{
				Requests:     2,
				MinVRAM:      &minVRAM,
				ResourceName: "amd.com/gpu",
			},
		},
	}
}

func testLegacyAutoCustomService(custom *aimv1alpha1.AIMServiceModelCustom) *aimv1alpha1.AIMService {
	service := &aimv1alpha1.AIMService{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha1.GroupVersion.String(), Kind: "AIMService"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-service",
			Namespace: "models",
			UID:       types.UID("source-uid"),
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model:              &aimv1alpha1.AIMServiceModel{Custom: custom.DeepCopy()},
			RuntimeConfigRef:   aimv1alpha1.RuntimeConfigRef{Name: "runtime"},
			ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "registry"}},
			ServiceAccountName: "runner",
		},
	}
	service.Spec.Env = []corev1.EnvVar{{Name: "SERVICE_ENV", Value: "value"}}
	return service
}

func testLegacyAutoCustomModel(
	t *testing.T,
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
) *aimv1alpha2.AIMModel {
	t.Helper()
	model := &aimv1alpha2.AIMModel{
		TypeMeta: metav1.TypeMeta{APIVersion: aimv1alpha2.GroupVersion.String(), Kind: "AIMModel"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "compatibility-model",
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelKeyOrigin:      constants.LabelValueOriginAutoGenerated,
				constants.LabelKeyCustomModel: "true",
				constants.LabelKeyService:     service.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(service, aimv1alpha1.GroupVersion.WithKind("AIMService")),
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              custom.BaseImage,
			RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
			ImagePullSecrets:   service.Spec.ImagePullSecrets,
			ServiceAccountName: service.Spec.ServiceAccountName,
			Env:                service.Spec.Env,
		},
	}
	if err := SetLegacyAutoCustomSpec(model, custom); err != nil {
		t.Fatalf("SetLegacyAutoCustomSpec() error = %v", err)
	}
	return model
}

func assertLegacyAutoCustomProblem(t *testing.T, err error, want LegacyAutoCustomSpecProblem) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	problem, ok := LegacyAutoCustomSpecProblemOf(err)
	if !ok || problem != want {
		t.Fatalf("problem = (%q, %t), want %q; error: %v", problem, ok, want, err)
	}
}
