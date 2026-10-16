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

package aimprofileset

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

const clusterModelKind = "AIMClusterModel"

// sourceErrors identifies unavailable sources. Any source failure retains the
// entire set without writes; partial-progress reconciliation is deferred.
type sourceErrors struct {
	errs []error
}

func (e *sourceErrors) Error() string   { return errors.Join(e.errs...).Error() }
func (e *sourceErrors) Unwrap() []error { return e.errs }
func (e *sourceErrors) add(group string, err error) {
	if err == nil {
		return
	}
	e.errs = append(e.errs, fmt.Errorf("%s: %w", group, err))
}

func (e *sourceErrors) err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return e
}

type sourceModel struct {
	object client.Object
	spec   *aimv1alpha1.AIMModelSpec
	status *aimv1alpha1.AIMModelStatus
	kind   string
}

func (m sourceModel) group() string {
	return aimmodel.CatalogSourceGroup(m.kind, m.object.GetNamespace(), m.object.GetName(), string(m.object.GetUID()))
}

func (m sourceModel) catalog(ctx context.Context, c client.Reader) (*aimprofile.DiscoveryCatalog, error) {
	name, err := aimmodel.DiscoveryCacheName(m.object.GetName())
	if err != nil {
		return nil, err
	}
	namespace := m.object.GetNamespace()
	if m.kind == clusterModelKind {
		namespace = constants.GetOperatorNamespace()
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &cm); err != nil {
		return nil, err
	}
	hash := aimmodel.ComputeDiscoverySpecHash(m.spec.Image, m.spec.ImagePullSecrets, m.spec.ServiceAccountName, aimmodel.DiscoveryCommandVersion)
	if !aimmodel.CacheOwnedByModel(&cm, string(m.object.GetUID())) || !aimmodel.CacheHasMatchingSpecHash(&cm, hash) {
		return nil, fmt.Errorf("discovery catalog is not current for this model")
	}
	catalog, err := aimprofile.CachedDiscoveryCatalog(&cm)
	if err != nil {
		return nil, err
	}
	if catalog.SourceImage != m.spec.Image {
		return nil, fmt.Errorf("discovery catalog source image does not match model image")
	}
	return catalog, nil
}

func (m sourceModel) publishedCandidates(ctx context.Context, c client.Reader) ([]aimprofile.ProfileCopyCandidate, map[string]string, error) {
	catalog, err := m.catalog(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return m.cachedPublication(catalog)
}

func (m sourceModel) buildPublication(catalog *aimprofile.DiscoveryCatalog) ([]aimprofile.ProfileCopyCandidate, map[string]string, error) {
	raw := aimmodel.RawCatalogCandidates(catalog, m.spec, m.status, m.group())
	// Materialized names map to definition identities for existing objects that
	// predate source-id annotations as well as current materializations.
	liveNames := make(map[string]string, len(raw))
	for i := range raw {
		candidate := &raw[i]
		name, err := utils.GenerateDerivedName([]string{m.object.GetName(), candidate.Spec.ProfileId}, utils.WithHashSource(m.object.GetName(), candidate.Spec.ProfileId))
		if err != nil {
			return nil, nil, err
		}
		liveNames[name] = candidate.SourceID
		// Use the model's materialized profile name to preserve pre-catalog
		// derived-name inputs. Keep the catalog's Type unchanged, including
		// an unset value; it participates in both selection and naming.
		candidate.Name = name
		candidate.LegacyCatalogNaming = true
	}
	return raw, liveNames, nil
}

func (m sourceModel) provenance(spec aimv1alpha2.AIMProfileSpecCommon) map[string]string {
	scope := constants.LabelValueSourceModelScopeNamespace
	if m.kind == clusterModelKind {
		scope = constants.LabelValueSourceModelScopeCluster
	}
	return map[string]string{
		constants.LabelKeySourceModel:      m.object.GetName(),
		constants.LabelKeySourceModelScope: scope,
		constants.LabelKeyProfileRole:      aimprofile.ProfileRoleLabelValue(spec),
		constants.LabelKeyProfileOrigin:    string(aimv1alpha1.ProfileOriginDiscovered),
	}
}

func listSourceModels(ctx context.Context, c client.Client, namespace string, cluster bool, selector aimv1alpha1.ProfileSelector) ([]sourceModel, error) {
	var out []sourceModel
	modelName := ""
	if selector.ModelRef != nil {
		modelName = selector.ModelRef.Name
	}
	if cluster {
		var list aimv1alpha2.AIMClusterModelList
		if modelName != "" {
			var model aimv1alpha2.AIMClusterModel
			if err := c.Get(ctx, client.ObjectKey{Name: modelName}, &model); err != nil {
				if apierrors.IsNotFound(err) {
					return nil, nil
				}
				return nil, err
			}
			list.Items = []aimv1alpha2.AIMClusterModel{model}
		} else {
			if err := c.List(ctx, &list); err != nil {
				return nil, err
			}
		}
		for i := range list.Items {
			m := &list.Items[i]
			out = append(out, sourceModel{m, &m.Spec, &m.Status, clusterModelKind})
		}
	} else {
		var list aimv1alpha2.AIMModelList
		if modelName != "" {
			var model aimv1alpha2.AIMModel
			if err := c.Get(ctx, client.ObjectKey{Name: modelName, Namespace: namespace}, &model); err != nil {
				if apierrors.IsNotFound(err) {
					return nil, nil
				}
				return nil, err
			}
			list.Items = []aimv1alpha2.AIMModel{model}
		} else {
			if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
				return nil, err
			}
		}
		for i := range list.Items {
			m := &list.Items[i]
			out = append(out, sourceModel{m, &m.Spec, &m.Status, "AIMModel"})
		}
	}
	filtered := out[:0]
	for _, m := range out {
		if !aimmodel.PublishesCatalog(m.spec) || !m.object.GetDeletionTimestamp().IsZero() {
			continue
		}
		if selector.ModelRef != nil && selector.ModelRef.Name != "" && selector.ModelRef.Name != m.object.GetName() {
			continue
		}
		if selector.Origin != "" && selector.Origin != aimv1alpha1.ProfileOriginDiscovered {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered, nil
}

func liveSourceCandidate(obj client.Object, spec aimv1alpha2.AIMProfileSpecCommon, status aimv1alpha2.AIMProfileStatus, kind string) profileSetCandidate {
	group := fmt.Sprintf("%s/%s/%s/%s", kind, obj.GetNamespace(), obj.GetName(), obj.GetUID())
	annotations := obj.GetAnnotations()
	candidate := profileSetCandidate{
		ProfileCopyCandidate: aimprofile.ProfileCopyCandidate{
			Name: obj.GetName(), SourceID: group, SourceGroup: group, Spec: spec, Status: status,
			BaseImage:           aimprofile.BaseImageFromProfile(obj, status.BaseImage),
			LegacyCatalogNaming: annotations[aimprofile.AnnotationLegacyCatalogNaming] == legacyCatalogNamingValue,
		},
		annotations: annotations, labels: obj.GetLabels(), sourceKind: kind,
	}
	if owner := metav1.GetControllerOf(obj); owner != nil {
		switch owner.Kind {
		case "AIMModel", clusterModelKind:
			candidate.modelOwnerUID = string(owner.UID)
		case "AIMProfileSet", "AIMClusterProfileSet":
			candidate.profileSetOwnerUID = string(owner.UID)
		}
	}
	candidate.ProfileSetUIDs = parseProfileSetLineage(annotations[annotationProfileSetLineage])
	if len(candidate.ProfileSetUIDs) == 0 {
		ownerUID := annotations[annotationProfileSetUID]
		if ownerUID == "" {
			ownerUID = candidate.profileSetOwnerUID
		}
		if ownerUID != "" {
			candidate.ProfileSetUIDs = []string{ownerUID}
		}
	}
	return candidate
}

func parseProfileSetLineage(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	lineage := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		uid := strings.TrimSpace(part)
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		lineage = append(lineage, uid)
	}
	return lineage
}

func containsProfileSetUID(lineage []string, uid string) bool {
	for _, ancestor := range lineage {
		if ancestor == uid {
			return true
		}
	}
	return false
}

func listLiveSources(ctx context.Context, c client.Client, namespace string, cluster bool) ([]profileSetCandidate, error) {
	var live []profileSetCandidate
	if cluster {
		var list aimv1alpha2.AIMClusterProfileList
		if err := c.List(ctx, &list); err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := &list.Items[i]
			live = append(live, liveSourceCandidate(p, p.Spec.AIMProfileSpecCommon, p.Status, "AIMClusterProfile"))
		}
	} else {
		var list aimv1alpha2.AIMProfileList
		if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := &list.Items[i]
			live = append(live, liveSourceCandidate(p, p.Spec.AIMProfileSpecCommon, p.Status, "AIMProfile"))
		}
	}
	return live, nil
}

type candidatePools struct {
	eligible           []profileSetCandidate
	excludedByCopyable []profileSetCandidate
}

// loadDefaultScope reads informer-backed inventories once per scope. Unsupported
// raw definitions never create API resources; they participate only in selection.
func loadDefaultScope(ctx context.Context, c client.Client, namespace string, cluster bool, spec aimv1alpha1.AIMProfileSetSpec, selfUID string) ([]profileSetCandidate, error) {
	pools, err := loadDefaultScopePools(ctx, c, namespace, cluster, spec, selfUID)
	return pools.eligible, err
}

func loadDefaultScopePools(ctx context.Context, c client.Client, namespace string, cluster bool, spec aimv1alpha1.AIMProfileSetSpec, selfUID string) (candidatePools, error) {
	failures := &sourceErrors{}
	provenance, _, err := aimprofile.ProvenanceLabelSelector(spec.Selector)
	if err != nil {
		return candidatePools{}, err
	}
	live, err := listLiveSources(ctx, c, namespace, cluster)
	if err != nil {
		return candidatePools{}, err
	}
	models, modelErr := listSourceModels(ctx, c, namespace, cluster, spec.Selector)
	if modelErr != nil {
		failures.add("model inventory", modelErr)
	}
	byID := make(map[string]profileSetCandidate)
	excludedByID := make(map[string]profileSetCandidate)
	// Associate both current and old materialized names with the model whose
	// definitions replace them. Do not consume stale raw profiles from an
	// unreadable model catalog.
	modelUIDs := make(map[string]bool)
	liveNames := make(map[string]string)
	for _, m := range models {
		modelUIDs[string(m.object.GetUID())] = true
		candidates, names, err := m.publishedCandidates(ctx, c)
		if err != nil {
			failures.add(m.group(), err)
			continue
		}
		for name, id := range names {
			liveNames[name] = id
		}
		for _, candidate := range candidates {
			prov := m.provenance(candidate.Spec)
			if !provenance.Matches(labels.Set(prov)) {
				continue
			}
			byID[candidate.SourceID] = profileSetCandidate{ProfileCopyCandidate: candidate, labels: prov, contractResolved: true}
		}
	}
	for _, candidate := range live {
		annotations := candidate.annotations
		// A materialized definition only contributes a copyability veto; the
		// normalized catalog remains authoritative during spec/status lag.
		if id := liveNames[candidate.Name]; id != "" &&
			(modelUIDs[candidate.modelOwnerUID] || modelUIDs[annotations[aimmodel.AnnotationModelUID()]]) {
			if _, explicit := annotations[aimprofile.AnnotationProfileCopyable]; explicit && !aimprofile.IsProfileCopyable(annotations) {
				if definition, ok := byID[id]; ok {
					excludedByID[id] = definition
					delete(byID, id)
				}
			}
			continue
		}
		if modelUIDs[candidate.modelOwnerUID] || modelUIDs[annotations[aimmodel.AnnotationModelUID()]] {
			continue
		}
		if selfUID != "" && annotations[annotationProfileSetUID] == selfUID {
			continue
		}
		if selfUID != "" && containsProfileSetUID(candidate.ProfileSetUIDs, selfUID) {
			continue
		}
		if !provenance.Matches(labels.Set(candidate.labels)) {
			continue
		}
		if aimprofile.IsProfileCopyable(annotations) {
			byID[candidate.SourceID] = candidate
			delete(excludedByID, candidate.SourceID)
		} else {
			excludedByID[candidate.SourceID] = candidate
		}
	}
	sorted := func(candidates map[string]profileSetCandidate) []profileSetCandidate {
		keys := make([]string, 0, len(candidates))
		for id := range candidates {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		out := make([]profileSetCandidate, 0, len(keys))
		for _, id := range keys {
			out = append(out, candidates[id])
		}
		return out
	}
	return candidatePools{
		eligible:           sorted(byID),
		excludedByCopyable: sorted(excludedByID),
	}, failures.err()
}

// Explicit catalogs retain their raw pre-catalog-selection representation and
// naming. Missing catalogs are unresolved dependencies, never empty snapshots.
func loadExplicitCatalog(ctx context.Context, reader client.Reader, spec aimv1alpha1.AIMProfileSetSpec, namespace string) ([]profileSetCandidate, error) {
	// Admission rejects this combination, but existing objects may predate
	// that rule. Never turn an unsupported provenance filter into a prune-safe
	// empty selection, even when the catalog has a matching model owner.
	if spec.Selector.ModelRef != nil {
		return nil, controllerutils.NewInvalidSpecError("InvalidProfileSelection",
			"sourceRef and selector.modelRef are mutually exclusive; remove selector.modelRef to keep using the explicit catalog", nil)
	}
	cm, catalog, err := aimprofile.LoadDiscoveryCatalog(ctx, reader, *spec.SourceRef, namespace)
	if err != nil {
		return nil, err
	}
	candidates := catalog.ToCandidates()
	for i := range candidates {
		candidates[i].SourceGroup = "ConfigMap/" + namespace + "/" + cm.Name
		candidates[i].SourceID = candidates[i].SourceGroup + "/" + catalog.Profiles[i].RelPath
		candidates[i].LegacyCatalogNaming = true
	}
	return filterCatalogProvenance(candidates, spec.Selector)
}

func filterCatalogProvenance(candidates []aimprofile.ProfileCopyCandidate, selector aimv1alpha1.ProfileSelector) ([]profileSetCandidate, error) {
	provenance, _, err := aimprofile.ProvenanceLabelSelector(selector)
	if err != nil {
		return nil, err
	}
	out := make([]profileSetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		prov := map[string]string{
			constants.LabelKeyProfileRole:   aimprofile.ProfileRoleLabelValue(candidate.Spec),
			constants.LabelKeyProfileOrigin: string(aimv1alpha1.ProfileOriginDiscovered),
		}
		if provenance.Matches(labels.Set(prov)) {
			out = append(out, profileSetCandidate{ProfileCopyCandidate: candidate, labels: prov, contractResolved: true})
		}
	}
	return out, nil
}
