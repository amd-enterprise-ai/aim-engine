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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

func TestBuildDesiredOfficialClusterModelProfiles_StampsRoleAndOriginLabels(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage: "quay.io/amd/aim-base:0.10.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/Qwen3-32B",
				ModelId:   "qwen/Qwen3-32B",
				ProfileId: "tp2",
				Image:     "quay.io/amd/aim-qwen:0.10.0",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "qwen/Qwen3-32B",
					SourceURI: "hf://qwen/Qwen3-32B",
				}},
			},
		}},
	}

	desired, err := buildDesiredOfficialClusterModelProfiles(model, catalog, nil, nil)
	if err != nil {
		t.Fatalf("buildDesiredOfficialClusterModelProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile, ok := desired[0].Object.(*aimv1alpha2.AIMClusterProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMClusterProfile", desired[0].Object)
	}
	if got := profile.Labels[constants.LabelKeyProfileRole]; got != constants.LabelValueProfileRoleDeployable {
		t.Fatalf("profile-role label = %q, want %q", got, constants.LabelValueProfileRoleDeployable)
	}
	if got := profile.Labels[constants.LabelKeyProfileOrigin]; got != string(aimv1alpha1.ProfileOriginDiscovered) {
		t.Fatalf("profile-origin label = %q, want %q", got, aimv1alpha1.ProfileOriginDiscovered)
	}
	if got := profile.Labels[constants.LabelKeySourceModelScope]; got != constants.LabelValueSourceModelScopeCluster {
		t.Fatalf("source-model-scope label = %q, want %q", got, constants.LabelValueSourceModelScopeCluster)
	}
}

// TestBuildDesiredOfficialClusterModelProfiles_BaseEntriesStampedAsBase is
// the cluster-scoped counterpart of the namespace test: a catalog item with
// no aimId / modelSources (typical of a base image used as a custom-model
// source) must yield a base AIMClusterProfile labelled `base`. Also
// exercises the summarizeManagedProfiles count path through an
// AIMClusterProfile object.
func TestBuildDesiredOfficialClusterModelProfiles_BaseEntriesStampedAsBase(t *testing.T) {
	t.Parallel()

	model := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-base", UID: "model-uid"},
	}
	catalog := aimprofile.DiscoveryCatalog{
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage: "ghcr.io/silogen/aim-base-vllm:0.1.0",
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				ProfileId: "vllm-cpu-bf16-tp1-latency",
				Engine:    "vllm",
			},
		}},
	}

	desired, err := buildDesiredOfficialClusterModelProfiles(model, catalog, nil, nil)
	if err != nil {
		t.Fatalf("buildDesiredOfficialClusterModelProfiles() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("len(desired) = %d, want 1", len(desired))
	}
	profile, ok := desired[0].Object.(*aimv1alpha2.AIMClusterProfile)
	if !ok {
		t.Fatalf("desired object = %T, want *AIMClusterProfile", desired[0].Object)
	}
	if got := profile.Labels[constants.LabelKeyProfileRole]; got != constants.LabelValueProfileRoleBase {
		t.Fatalf("profile-role label = %q, want %q for base entry", got, constants.LabelValueProfileRoleBase)
	}
	if aimprofile.IsProfileDeployable(profile.Spec.AIMProfileSpecCommon) {
		t.Fatal("base cluster profile spec must not satisfy IsProfileDeployable")
	}

	summary, _ := summarizeManagedProfiles(desired, nil)
	if summary.Total != 1 {
		t.Fatalf("summary.Total = %d, want 1", summary.Total)
	}
	if summary.Base != 1 {
		t.Fatalf("summary.Base = %d, want 1 for base-only cluster catalog", summary.Base)
	}
	if summary.Deployable != 0 {
		t.Fatalf("summary.Deployable = %d, want 0 for base-only cluster catalog", summary.Deployable)
	}
}
