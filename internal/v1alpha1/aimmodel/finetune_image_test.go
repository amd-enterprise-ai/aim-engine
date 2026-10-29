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
	"context"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
)

// aimBase011 is the canonical aim-base:0.11 reference used across tests in
// this package. Centralising it shuts up goconst and makes intent obvious.
const aimBase011 = "ghcr.io/silogen/aim-base:0.11"

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func clusterOwner(name, image, baseImageRef string) *aimv1alpha1.AIMClusterModel {
	owner := &aimv1alpha1.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	if baseImageRef != "" {
		owner.Status.ImageMetadata = &aimv1alpha1.ImageMetadata{BaseImageRef: baseImageRef}
	}
	return owner
}

func nsOwner(namespace, name, image, baseImageRef string) *aimv1alpha1.AIMModel {
	owner := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	if baseImageRef != "" {
		owner.Status.ImageMetadata = &aimv1alpha1.ImageMetadata{BaseImageRef: baseImageRef}
	}
	return owner
}

func match(ownerName, ownerNamespace, version string) TemplateMatchResult {
	return TemplateMatchResult{
		OriginalAimId:   "qwen/qwen3-32b",
		OriginalModelId: "qwen/qwen3-32b-fp8",
		OriginalVersion: version,
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: ownerName,
		},
		OwnerNamespace: ownerNamespace,
		// Discovery output the cloner needs to assemble a CEL-valid copy.
		// Tests that exercise the deferral path should override this.
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
			Metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:     "qwen/qwen3-32b",
				ModelID:   "qwen/qwen3-32b-fp8",
				Engine:    "vllm",
				GPU:       testGPUModel,
				GPUCount:  1,
				Metric:    aimv1alpha1.AIMMetric("latency"),
				Precision: aimv1alpha1.AIMPrecision("fp8"),
			},
		},
	}
}

// Resolver returns the cluster owner's exact baseImageRef for a cluster-scoped
// match.
func TestImageResolver_ClusterOwner_BaseImageRef(t *testing.T) {
	owner := clusterOwner("qwen3-0.9.0",
		"ghcr.io/silogen/qwen3:0.9.0",
		"ghcr.io/silogen/aim-base:0.9.0",
	)
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId: "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights",
		}},
		Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	got, err := r.Resolve(context.Background(), match("qwen3-0.9.0", "", "0.9.0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ghcr.io/silogen/aim-base:0.9.0"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestImageResolver_PreservesReleaseCandidateTag(t *testing.T) {
	owner := clusterOwner("qwen3-32b",
		"amdenterpriseai/aim-qwen-qwen3-32b:0.11.0",
		"ghcr.io/silogen/aim-base:0.11-rc21",
	)
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	got, err := r.Resolve(context.Background(), match("qwen3-32b", "", "0.11.0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ghcr.io/silogen/aim-base:0.11-rc21"; got != want {
		t.Errorf("Resolve = %q, want exact producer-owned reference %q", got, want)
	}
}

func TestImageResolver_PreservesBaseImageRegistry(t *testing.T) {
	owner := clusterOwner("qwen3-0.11.0",
		"ghcr.io/silogen/qwen3:0.11.0",
		"docker.io/amdenterpriseai/aim-base:0.11",
	)
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
	}, nil)

	got, err := r.Resolve(context.Background(), match("qwen3-0.11.0", "", "0.11.0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "docker.io/amdenterpriseai/aim-base:0.11"; got != want {
		t.Errorf("Resolve = %q, want exact producer-owned reference %q", got, want)
	}
}

func TestImageResolver_AppliesExplicitRegistryMapping(t *testing.T) {
	owner := clusterOwner("qwen3-0.11.0",
		"ghcr.io/private-team/qwen3:0.11.0",
		"docker.io/amdenterpriseai/aim-base:0.11.2",
	)
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
	}, []aimimage.RegistryMapping{{
		From: "docker.io/amdenterpriseai",
		To:   "ghcr.io/private-team",
	}})

	got, err := r.Resolve(context.Background(), match("qwen3-0.11.0", "", "0.11.0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ghcr.io/private-team/aim-base:0.11.2"; got != want {
		t.Errorf("Resolve = %q, want mapped reference %q", got, want)
	}
}

// Missing AIM_BASE_IMAGE_REF is a controlled deferral even when the source tag
// looks like semver. The resolver must not synthesize or truncate an image tag.
func TestImageResolver_DefersWhenBaseImageRefIsMissing(t *testing.T) {
	owner := clusterOwner("qwen3-0.11.0", "ghcr.io/silogen/qwen3:0.11.0", "")
	owner.Status.ImageMetadata = &aimv1alpha1.ImageMetadata{}
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	if _, err := r.Resolve(context.Background(), match("qwen3-0.11.0", "", "0.11.0")); err == nil {
		t.Fatal("expected deferral error for missing AIM_BASE_IMAGE_REF")
	}
}

// Resolver returns the same controlled deferral for non-semver source tags.
func TestImageResolver_DefersWhenUnresolvable(t *testing.T) {
	owner := clusterOwner("qwen3-latest", "ghcr.io/silogen/qwen3:latest", "")
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	if _, err := r.Resolve(context.Background(), match("qwen3-latest", "", "latest")); err == nil {
		t.Fatal("expected deferral error, got nil")
	}
}

// Resolver returns an error when the owner is not found in the cluster.
func TestImageResolver_DefersWhenOwnerMissing(t *testing.T) {
	c := newFakeClient()

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	if _, err := r.Resolve(context.Background(), match("missing-owner", "", "0.9.0")); err == nil {
		t.Fatal("expected deferral error when owner is missing")
	}
}

// versionPolicy=pinned with model.spec.image set short-circuits owner lookup
// — the user's spec.image is the deployment image. This also means a missing
// owner is fine in the pinned case, which keeps pinned services deployable
// even when the source AIM has been GC'd.
func TestImageResolver_PinnedReturnsModelSpecImage(t *testing.T) {
	c := newFakeClient() // intentionally no owner

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		Image:        "ghcr.io/silogen/aim-base:0.8.5",
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
	}, nil)

	got, err := r.Resolve(context.Background(), match("qwen3-0.8.5", "", "0.8.5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ghcr.io/silogen/aim-base:0.8.5"; got != want {
		t.Errorf("Resolve = %q, want %q (pinned should use spec.image)", got, want)
	}
}

// Namespace-scoped sources are looked up in the source template's namespace,
// not the fine-tuned model's namespace.
func TestImageResolver_NamespaceScopedOwner(t *testing.T) {
	owner := nsOwner("team-a", "qwen3-0.9.0",
		"ghcr.io/silogen/qwen3:0.9.0",
		"ghcr.io/silogen/aim-base:0.9.0",
	)
	c := newFakeClient(owner)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
	}, nil)

	got, err := r.Resolve(context.Background(), match("qwen3-0.9.0", "team-a", "0.9.0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ghcr.io/silogen/aim-base:0.9.0"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// Heterogeneous matches: two source owners with different base images for the
// same aimId resolve to different deployment images per match. This is the
// scenario that motivated dropping the single model-spec.image patching and
// moving image resolution onto each template copy.
func TestImageResolver_PerMatchHeterogeneous(t *testing.T) {
	mi300 := clusterOwner("qwen3-mi300-0.11", "ghcr.io/silogen/qwen3:0.11.0", aimBase011)
	epyc := clusterOwner("qwen3-epyc-0.11", "ghcr.io/silogen/qwen3-epyc:0.11.0", "ghcr.io/silogen/aim-epyc-base:0.11")
	c := newFakeClient(mi300, epyc)

	r := newImageResolver(c, &aimv1alpha1.AIMModelSpec{
		AimId:        "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
		Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
	}, nil)

	gpuImage, err := r.Resolve(context.Background(), match("qwen3-mi300-0.11", "", "0.11.0"))
	if err != nil {
		t.Fatalf("Resolve(mi300): %v", err)
	}
	if gpuImage != aimBase011 {
		t.Errorf("mi300 image = %q, want %q", gpuImage, aimBase011)
	}
	cpuImage, err := r.Resolve(context.Background(), match("qwen3-epyc-0.11", "", "0.11.0"))
	if err != nil {
		t.Fatalf("Resolve(epyc): %v", err)
	}
	if want := "ghcr.io/silogen/aim-epyc-base:0.11"; cpuImage != want {
		t.Errorf("epyc image = %q, want %q", cpuImage, want)
	}
}
