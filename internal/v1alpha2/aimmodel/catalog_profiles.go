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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// CatalogSourceGroup identifies a model incarnation, not its discovery revision
// or the hardware currently available to materialize its profiles.
func CatalogSourceGroup(kind, namespace, name, uid string) string {
	return fmt.Sprintf("%s/%s/%s/%s", kind, namespace, name, uid)
}

// PublishesCatalog must agree with native discovery eligibility. Legacy custom
// and derivation models keep using their existing resource-backed profiles.
func PublishesCatalog(spec *aimv1alpha1.AIMModelSpec) bool {
	return spec != nil && spec.Image != "" && spec.Profiles == nil && derivationSpec(spec) == nil &&
		!legacyaimmodel.IsCustomModel(spec) && shouldRunNativeDiscovery(spec)
}

// CatalogProfileSpec supplies the same defaults to logical definitions and
// materialized profiles. Nested catalog data remains immutable; callers clone
// only when applying overrides or creating API objects.
func CatalogProfileSpec(entry aimprofile.DiscoveryCatalogItem, spec *aimv1alpha1.AIMModelSpec, recommended []aimv1alpha1.RecommendedDeployment) aimv1alpha2.AIMProfileSpecCommon {
	result := entry.Spec
	result.ImagePullSecrets = spec.ImagePullSecrets
	result.ServiceAccountName = spec.ServiceAccountName
	if !entry.PrimaryExplicit {
		result.Primary = matchesRecommendedDeployment(result, recommended)
	}
	return result
}

func RawCatalogCandidates(catalog *aimprofile.DiscoveryCatalog, spec *aimv1alpha1.AIMModelSpec, status *aimv1alpha1.AIMModelStatus, group string) []aimprofile.ProfileCopyCandidate {
	recommended := recommendedDeploymentsFrom(spec.GetEffectiveImageMetadata(status))
	out := make([]aimprofile.ProfileCopyCandidate, 0, len(catalog.Profiles))
	for _, entry := range catalog.Profiles {
		out = append(out, aimprofile.ProfileCopyCandidate{
			SourceGroup:  group,
			SourceID:     group + "/" + entry.RelPath,
			Name:         entry.Name,
			Spec:         CatalogProfileSpec(entry, spec, recommended),
			Status:       aimv1alpha2.AIMProfileStatus{Version: entry.Version},
			BaseImage:    entry.BaseImage,
			YAMLContract: entry.YAMLContract,
		})
	}
	return out
}

func preserveCopyability(desired []desiredProfile, existing []managedProfile) {
	values := make(map[string]string)
	for _, p := range existing {
		if value, exists := p.Object.GetAnnotations()[aimprofile.AnnotationProfileCopyable]; exists {
			values[p.Object.GetName()] = value
		}
	}
	for _, p := range desired {
		if value, exists := values[p.Object.GetName()]; exists {
			p.Object.GetAnnotations()[aimprofile.AnnotationProfileCopyable] = value
		}
	}
}

func ownedCopyabilityOptOuts(model client.Object, existing []managedProfile) map[string]bool {
	names := make(map[string]bool)
	for _, profile := range existing {
		if !metav1.IsControlledBy(profile.Object, model) {
			continue
		}
		annotations := profile.Object.GetAnnotations()
		if _, explicit := annotations[aimprofile.AnnotationProfileCopyable]; explicit &&
			!aimprofile.IsProfileCopyable(annotations) {
			names[profile.Object.GetName()] = true
		}
	}
	return names
}
