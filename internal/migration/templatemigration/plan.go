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

// Package templatemigration implements the bounded administrator-run
// v1alpha1 template to v1alpha2 profile migration.
package templatemigration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
	v1alpha2aimservice "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	annotationMigrationSourceKind      = constants.AimLabelDomain + "/migration-source-kind"
	annotationMigrationSourceNamespace = constants.AimLabelDomain + "/migration-source-namespace"
	annotationMigrationSourceName      = constants.AimLabelDomain + "/migration-source-name"
	annotationMigrationSourceUID       = constants.AimLabelDomain + "/migration-source-uid"
)

// PlanOptions selects the namespaces inspected by Plan.
type PlanOptions struct {
	Namespaces               []string
	RuntimeProjectionMode    aimv1alpha2.RuntimeProjectionMode
	allowDedicatedApplyTests bool
}

type configMapReference struct {
	namespace string
	name      string
	key       string
	use       string
}

type unitState struct {
	unit                *MigrationUnit
	service             *aimv1alpha1.AIMService
	template            client.Object
	templateSpec        *aimv1alpha1.AIMServiceTemplateSpecCommon
	templateStatus      *aimv1alpha1.AIMServiceTemplateStatus
	templateScope       aimv1alpha1.AIMResolutionScope
	templateKind        string
	templateCaching     *aimv1alpha1.AIMTemplateCachingConfig
	model               client.Object
	modelSpec           *aimv1alpha1.AIMModelSpec
	modelStatus         *aimv1alpha1.AIMModelStatus
	modelKind           string
	cache               *aimv1alpha1.AIMTemplateCache
	artifacts           []*aimv1alpha1.AIMArtifact
	runtimeConfigNames  map[string]struct{}
	runtimeConfigs      map[string]client.Object
	runtimeConfigRefs   []*aimv1alpha1.AIMResolvedReference
	configMapReferences map[string]configMapReference
	candidateOperations []ProposedOperation
	targetProfileObject client.Object
	targetCacheObject   *aimv1alpha2.AIMProfileCache
}

type planner struct {
	context                  context.Context
	reader                   client.Reader
	result                   *PlanResult
	runtimeProjectionMode    aimv1alpha2.RuntimeProjectionMode
	allowDedicatedApplyTests bool
}

// Plan inventories every AIMService in the selected namespaces using only
// read operations and direct references from each service's migration closure.
func Plan(ctx context.Context, reader client.Reader, options PlanOptions) (*PlanResult, error) {
	if reader == nil {
		return nil, fmt.Errorf("kubernetes reader is required")
	}
	namespaces, err := normalizeNamespaces(options.Namespaces)
	if err != nil {
		return nil, err
	}
	if options.RuntimeProjectionMode != aimv1alpha2.RuntimeProjectionModeExhaustive &&
		options.RuntimeProjectionMode != aimv1alpha2.RuntimeProjectionModeReduced &&
		options.RuntimeProjectionMode != aimv1alpha2.RuntimeProjectionModeBoth {
		return nil, fmt.Errorf(
			"runtime projection mode must explicitly match the controller deployment: Exhaustive, Reduced, or Both",
		)
	}

	result := &PlanResult{
		Document: PlanDocument{
			DocumentVersion:       DocumentVersion,
			RuntimeProjectionMode: string(options.RuntimeProjectionMode),
			SelectedNamespaces:    namespaces,
			Units:                 []MigrationUnit{},
			Warnings:              []Finding{},
		},
		planningTimestamp: time.Now().UTC(),
	}
	planBuilder := planner{
		context:                  ctx,
		reader:                   reader,
		result:                   result,
		runtimeProjectionMode:    options.RuntimeProjectionMode,
		allowDedicatedApplyTests: options.allowDedicatedApplyTests,
	}

	var services []aimv1alpha1.AIMService
	for _, namespace := range namespaces {
		var list aimv1alpha1.AIMServiceList
		if err := reader.List(ctx, &list, client.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("inventory AIMServices in namespace %q: %w", namespace, err)
		}
		services = append(services, list.Items...)
	}
	sort.SliceStable(services, func(i, j int) bool {
		left := services[i].Namespace + "\x00" + services[i].Name
		right := services[j].Namespace + "\x00" + services[j].Name
		return left < right
	})

	for index := range services {
		unit := planBuilder.planService(&services[index])
		result.Document.Units = append(result.Document.Units, unit)
	}
	normalizeDocument(&result.Document)
	return result, nil
}

func normalizeNamespaces(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, value := range values {
		namespace := strings.TrimSpace(value)
		if namespace == "" {
			return nil, fmt.Errorf("namespace must not be empty")
		}
		seen[namespace] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("at least one explicit namespace is required")
	}
	namespaces := make([]string, 0, len(seen))
	for namespace := range seen {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces, nil
}

func (p *planner) planService(service *aimv1alpha1.AIMService) MigrationUnit {
	unit := MigrationUnit{
		ID:                 service.Namespace + "/" + service.Name,
		Namespace:          service.Namespace,
		Outcome:            OutcomeManual,
		ObservedResources:  []ResourceIdentity{},
		ProposedOperations: []ProposedOperation{},
		Imputations:        []Imputation{},
		Warnings:           []Finding{},
		ManualOutcomes:     []Finding{},
		SecretReferences:   []SecretReference{},
	}
	state := unitState{
		unit:                &unit,
		service:             service,
		runtimeConfigNames:  map[string]struct{}{},
		runtimeConfigs:      map[string]client.Object{},
		configMapReferences: map[string]configMapReference{},
	}
	if !p.runtimeProjectionMode.ProjectsPerProfile() {
		addManual(
			&unit,
			"ReducedRuntimeProjectionUnsupported",
			"Reduced runtime projection does not provide the per-profile runtime required before cutover",
		)
	}

	unit.Service = p.observe(&state, service, aimv1alpha1.GroupVersion.String(), "AIMService", "service", true)
	p.validateService(&state)
	p.resolveTemplate(&state)
	p.resolveModel(&state)
	p.resolveTemplateCache(&state)
	p.resolveArtifacts(&state)
	p.resolveRuntimeConfigs(&state)
	p.resolveConfigMaps(&state)
	p.planTargetProfile(&state)
	p.planTargetCache(&state)
	p.planArtifactProtection(&state)
	p.planServicePatch(&state)

	if len(unit.ManualOutcomes) == 0 {
		unit.Outcome = OutcomeAutomatic
		unit.ProposedOperations = state.candidateOperations
	}
	normalizeUnit(&unit)
	return unit
}

func (p *planner) validateService(state *unitState) {
	service := state.service
	p.addRuntimeConfigName(state, service.Spec.Name)
	p.addRuntimeConfigReference(state, service.Status.ResolvedRuntimeConfig)
	if service.DeletionTimestamp != nil {
		addManual(state.unit, "ServiceTerminating", "the AIMService is terminating")
	}
	if service.Status.Status != constants.AIMStatusRunning {
		addManual(state.unit, "ServiceNotReady", "the legacy AIMService is not Running")
	}
	if service.Generation <= 0 || service.Status.ObservedGeneration != service.Generation {
		addManual(state.unit, "ServiceGenerationUnobserved", "the AIMService status does not observe its current generation")
	}
	if service.Spec.Profile != nil || service.Status.ReconcilerPipeline == "profile" {
		addManual(state.unit, "AlreadyProfileManaged", "the AIMService is already on the profile pipeline")
	}
	if service.Spec.AdaptersEnabled() {
		addManual(
			state.unit,
			"AdapterMigrationUnsupported",
			"adapter-enabled AIMServices require manual migration until target-profile adapter support and adapter storage preservation are implemented and proven",
		)
	}
	if service.Spec.Overrides != nil {
		addManual(state.unit, "LegacyServiceOverrides", "legacy service overrides require administrator translation")
	}
	if service.Spec.Caching != nil {
		switch service.Spec.Caching.Mode {
		case "",
			aimv1alpha1.CachingModeDedicated,
			aimv1alpha1.CachingModeShared,
			aimv1alpha1.CachingModeAuto,
			aimv1alpha1.CachingModeAlways,
			aimv1alpha1.CachingModeNever:
		default:
			addManual(state.unit, "UnsupportedServiceCacheMode", "the AIMService caching mode is not supported")
		}
	}
}

func (p *planner) resolveTemplate(state *unitState) {
	ref := state.service.Status.ResolvedTemplate
	if ref == nil || ref.Name == "" || ref.UID == "" {
		addManual(state.unit, "TemplateIdentityUnavailable", "the resolved template identity is incomplete")
		return
	}
	if state.service.Spec.Template != nil &&
		state.service.Spec.Template.Name != "" &&
		state.service.Spec.Template.Name != ref.Name {
		addManual(state.unit, "TemplateIdentityConflict", "the selected and resolved template names differ")
	}

	switch ref.Scope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		namespace := ref.Namespace
		if namespace == "" {
			namespace = state.service.Namespace
		}
		if namespace != state.service.Namespace {
			addManual(state.unit, "TemplateIdentityConflict", "a namespace template resolved outside the service namespace")
			return
		}
		template := &aimv1alpha1.AIMServiceTemplate{}
		if !p.fetchRequired(
			state,
			client.ObjectKey{Namespace: namespace, Name: ref.Name},
			template,
			resourceKindServiceTemplate,
		) {
			return
		}
		state.template = template
		state.templateSpec = &template.Spec.AIMServiceTemplateSpecCommon
		state.templateStatus = &template.Status
		state.templateScope = aimv1alpha1.AIMResolutionScopeNamespace
		state.templateKind = resourceKindServiceTemplate
		state.templateCaching = template.Spec.Caching
		p.observe(state, template, aimv1alpha1.GroupVersion.String(), state.templateKind, roleSourceTemplate, true)
	case aimv1alpha1.AIMResolutionScopeCluster:
		template := &aimv1alpha1.AIMClusterServiceTemplate{}
		if !p.fetchRequired(
			state,
			client.ObjectKey{Name: ref.Name},
			template,
			resourceKindClusterTemplate,
		) {
			return
		}
		state.template = template
		state.templateSpec = &template.Spec.AIMServiceTemplateSpecCommon
		state.templateStatus = &template.Status
		state.templateScope = aimv1alpha1.AIMResolutionScopeCluster
		state.templateKind = resourceKindClusterTemplate
		p.observe(state, template, aimv1alpha1.GroupVersion.String(), state.templateKind, roleSourceTemplate, true)
	default:
		addManual(state.unit, "TemplateScopeAmbiguous", "the resolved template scope is not Namespace or Cluster")
		return
	}

	if state.template.GetUID() != ref.UID {
		addManual(state.unit, "TemplateIdentityChanged", "the resolved template UID no longer matches")
	}
	if ref.Kind != "" && ref.Kind != state.templateKind {
		addManual(state.unit, "TemplateIdentityConflict", "the resolved template kind no longer matches")
	}
	if state.template.GetDeletionTimestamp() != nil {
		addManual(state.unit, "TemplateTerminating", "the resolved template is terminating")
	}
	if state.templateStatus.Status != constants.AIMStatusReady {
		addManual(state.unit, "TemplateNotReady", "the resolved template is not Ready")
	}
	if state.template.GetGeneration() <= 0 ||
		state.templateStatus.ObservedGeneration != state.template.GetGeneration() {
		addManual(state.unit, "TemplateGenerationUnobserved", "the template status does not observe its current generation")
	}
	p.addRuntimeConfigName(state, state.templateSpec.Name)
	p.addRuntimeConfigReference(state, state.templateStatus.ResolvedRuntimeConfig)
}

func (p *planner) resolveModel(state *unitState) {
	ref := state.service.Status.ResolvedModel
	if ref == nil || ref.Name == "" || ref.UID == "" {
		addManual(state.unit, "ModelIdentityUnavailable", "the resolved model identity is incomplete")
		return
	}
	switch ref.Scope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		namespace := ref.Namespace
		if namespace == "" {
			namespace = state.service.Namespace
		}
		if namespace != state.service.Namespace {
			addManual(state.unit, "ModelIdentityConflict", "a namespace model resolved outside the service namespace")
			return
		}
		model := &aimv1alpha1.AIMModel{}
		if !p.fetchRequired(state, client.ObjectKey{Namespace: namespace, Name: ref.Name}, model, "AIMModel") {
			return
		}
		state.model = model
		state.modelSpec = &model.Spec
		state.modelStatus = &model.Status
		state.modelKind = "AIMModel"
		p.observe(state, model, aimv1alpha1.GroupVersion.String(), state.modelKind, roleSourceModel, true)
	case aimv1alpha1.AIMResolutionScopeCluster:
		model := &aimv1alpha1.AIMClusterModel{}
		if !p.fetchRequired(state, client.ObjectKey{Name: ref.Name}, model, "AIMClusterModel") {
			return
		}
		state.model = model
		state.modelSpec = &model.Spec
		state.modelStatus = &model.Status
		state.modelKind = "AIMClusterModel"
		p.observe(state, model, aimv1alpha1.GroupVersion.String(), state.modelKind, roleSourceModel, true)
	default:
		addManual(state.unit, "ModelScopeAmbiguous", "the resolved model scope is not Namespace or Cluster")
		return
	}

	if state.model.GetUID() != ref.UID {
		addManual(state.unit, "ModelIdentityChanged", "the resolved model UID no longer matches")
	}
	if ref.Kind != "" && ref.Kind != state.modelKind {
		addManual(state.unit, "ModelIdentityConflict", "the resolved model kind no longer matches")
	}
	if state.model.GetDeletionTimestamp() != nil {
		addManual(state.unit, "ModelTerminating", "the resolved model is terminating")
	}
	if state.modelStatus.Status != constants.AIMStatusReady {
		addManual(state.unit, "ModelNotReady", "the resolved model is not Ready")
	}
	if state.model.GetGeneration() <= 0 ||
		state.modelStatus.ObservedGeneration != state.model.GetGeneration() {
		addManual(state.unit, "ModelGenerationUnobserved", "the model status does not observe its current generation")
	}
	if state.templateSpec != nil &&
		state.templateSpec.ModelName != "" &&
		state.templateSpec.ModelName != state.model.GetName() {
		addManual(state.unit, "ModelIdentityConflict", "the template and service resolve different model names")
	}
	if state.templateStatus != nil && state.templateStatus.ResolvedModel != nil {
		resolved := state.templateStatus.ResolvedModel
		if resolved.Name != state.model.GetName() ||
			(resolved.UID != "" && resolved.UID != state.model.GetUID()) ||
			(resolved.Kind != "" && resolved.Kind != state.modelKind) ||
			(resolved.Namespace != "" && resolved.Namespace != state.model.GetNamespace()) {
			addManual(state.unit, "ModelIdentityConflict", "the template and service resolved model identities differ")
		}
	}
	p.addRuntimeConfigName(state, state.modelSpec.Name)
	p.addRuntimeConfigReference(state, state.modelStatus.ResolvedRuntimeConfig)
}

func (p *planner) resolveTemplateCache(state *unitState) {
	if state.service.Status.Cache == nil || state.service.Status.Cache.TemplateCacheRef == nil {
		addManual(state.unit, "TemplateCacheIdentityUnavailable", "the resolved template cache identity is unavailable")
		return
	}
	ref := state.service.Status.Cache.TemplateCacheRef
	if ref.Name == "" || ref.UID == "" {
		addManual(state.unit, "TemplateCacheIdentityUnavailable", "the resolved template cache identity is incomplete")
		return
	}
	namespace := ref.Namespace
	if namespace == "" {
		namespace = state.service.Namespace
	}
	if namespace != state.service.Namespace {
		addManual(state.unit, "TemplateCacheIdentityConflict", "the template cache is outside the service namespace")
		return
	}
	cache := &aimv1alpha1.AIMTemplateCache{}
	if !p.fetchRequired(state, client.ObjectKey{Namespace: namespace, Name: ref.Name}, cache, resourceKindTemplateCache) {
		return
	}
	state.cache = cache
	p.observe(state, cache, aimv1alpha1.GroupVersion.String(), resourceKindTemplateCache, "source-template-cache", true)
	if cache.UID != ref.UID {
		addManual(state.unit, "TemplateCacheIdentityChanged", "the resolved template cache UID no longer matches")
	}
	if cache.DeletionTimestamp != nil {
		addManual(state.unit, "TemplateCacheTerminating", "the resolved template cache is terminating")
	}
	if cache.Status.Status != constants.AIMStatusReady {
		addManual(state.unit, "TemplateCacheNotReady", "the resolved template cache is not Ready")
	}
	if cache.Generation <= 0 || cache.Status.ObservedGeneration != cache.Generation {
		addManual(state.unit, "TemplateCacheGenerationUnobserved", "the template cache status does not observe its current generation")
	}
	if state.template != nil &&
		(cache.Spec.TemplateName != state.template.GetName() ||
			aimv1alpha1.AIMResolutionScope(cache.Spec.TemplateScope) != state.templateScope) {
		addManual(state.unit, "TemplateCacheIdentityConflict", "the template cache points at a different template")
	}
	if cache.Status.ResolvedTemplateKind != "" &&
		cache.Status.ResolvedTemplateKind != state.templateKind {
		addManual(state.unit, "TemplateCacheIdentityConflict", "the template cache resolved a different template kind")
	}
	if (cache.Spec.Mode == aimv1alpha1.TemplateCacheModeDedicated) !=
		(state.service.Spec.GetCachingMode() == aimv1alpha1.CachingModeDedicated) {
		addManual(state.unit, "TemplateCacheModeConflict", "the template cache mode conflicts with the AIMService caching mode")
	}
	if cache.Spec.DownloadImage != "" || len(cache.Spec.ImagePullSecrets) > 0 {
		addManual(state.unit, "UnsupportedTemplateCacheFields", "the template cache uses unsupported download image or pull-secret fields")
	}
	if len(cache.Spec.ModelSources) > 0 &&
		state.templateSpec != nil &&
		!equality.Semantic.DeepEqual(cache.Spec.ModelSources, state.templateSpec.ModelSources) {
		addManual(state.unit, "TemplateCacheSourceConflict", "the template cache model sources conflict with template desired state")
	}
	p.validateCacheOwnership(state)
	p.addRuntimeConfigName(state, cache.Spec.Name)
	p.addRuntimeConfigReference(state, cache.Status.ResolvedRuntimeConfig)
}

func (p *planner) validateCacheOwnership(state *unitState) {
	cache := state.cache
	if cache == nil {
		return
	}
	controller := metav1.GetControllerOf(cache)
	switch cache.Spec.Mode {
	case aimv1alpha1.TemplateCacheModeDedicated:
		if len(cache.OwnerReferences) != 1 ||
			controller == nil ||
			(controller.APIVersion != aimv1alpha1.GroupVersion.String() &&
				controller.APIVersion != aimv1alpha2.GroupVersion.String()) ||
			controller.Kind != resourceKindService ||
			controller.Name != state.service.Name ||
			controller.UID != state.service.UID ||
			controller.Controller == nil ||
			!*controller.Controller {
			addManual(state.unit, "UnsupportedTemplateCacheOwnership", "the Dedicated template cache does not have this AIMService as its exact sole controller owner")
		}
	case aimv1alpha1.TemplateCacheModeShared:
		if len(cache.OwnerReferences) != 0 {
			addManual(state.unit, "UnsupportedTemplateCacheOwnership", "the Shared template cache has owner references")
		}
	default:
		addManual(state.unit, "UnsupportedTemplateCacheMode", "the template cache mode is unsupported")
	}
}

func (p *planner) resolveArtifacts(state *unitState) {
	if state.cache == nil {
		return
	}
	if len(state.cache.Status.Artifacts) != 1 {
		addManual(state.unit, "AmbiguousArtifacts", "the template cache does not resolve exactly one artifact")
	}
	names := make([]string, 0, len(state.cache.Status.Artifacts))
	for name := range state.cache.Status.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, key := range names {
		resolved := state.cache.Status.Artifacts[key]
		if resolved.Name == "" || resolved.UID == "" {
			addManual(state.unit, "ArtifactIdentityUnavailable", "a resolved artifact identity is incomplete")
			continue
		}
		artifact := &aimv1alpha1.AIMArtifact{}
		if !p.fetchRequired(
			state,
			client.ObjectKey{Namespace: state.service.Namespace, Name: resolved.Name},
			artifact,
			resourceKindArtifact,
		) {
			continue
		}
		state.artifacts = append(state.artifacts, artifact)
		p.observe(state, artifact, aimv1alpha1.GroupVersion.String(), resourceKindArtifact, "source-artifact", true)
		if string(artifact.UID) != resolved.UID {
			addManual(state.unit, "ArtifactIdentityChanged", "a resolved artifact UID no longer matches")
		}
		if artifact.Spec.Type != "" && artifact.Spec.Type != aimv1alpha1.ArtifactTypeModel {
			addManual(state.unit, "UnsupportedArtifactType", "a resolved cache artifact is not a model artifact")
		}
		modelSources := exactTemplateModelSources(state)
		if len(modelSources) == 1 {
			source := modelSources[0]
			if key != resolved.Name {
				addManual(state.unit, "ArtifactIdentityConflict", "the template cache artifact key does not match the resolved artifact name")
			}
			if resolved.Model != source.ModelID ||
				artifact.Spec.ModelID != source.ModelID ||
				artifact.Spec.SourceURI != source.SourceURI {
				addManual(state.unit, "ArtifactSourceConflict", "a resolved artifact conflicts with the exact template model source")
			}
		} else {
			addManual(state.unit, "AmbiguousArtifacts", "the template does not identify exactly one artifact model source")
		}
		if artifact.DeletionTimestamp != nil {
			addManual(state.unit, "ArtifactTerminating", "a resolved artifact is terminating")
		}
		if artifact.Status.Status != constants.AIMStatusReady ||
			resolved.Status != constants.AIMStatusReady {
			addManual(state.unit, "ArtifactNotReady", "a resolved artifact is not Ready")
		}
		if artifact.Generation <= 0 || artifact.Status.ObservedGeneration != artifact.Generation {
			addManual(state.unit, "ArtifactGenerationUnobserved", "an artifact status does not observe its current generation")
		}
		if artifact.Status.PersistentVolumeClaim == "" ||
			resolved.PersistentVolumeClaim == "" ||
			artifact.Status.PersistentVolumeClaim != resolved.PersistentVolumeClaim {
			addManual(state.unit, "PersistentVolumeClaimIdentityConflict", "artifact and cache PVC identities are incomplete or conflicting")
		} else {
			p.resolvePersistentVolumeClaim(state, artifact, artifact.Status.PersistentVolumeClaim)
		}
		if (artifact.Status.Mode == aimv1alpha1.ArtifactModeDedicated) !=
			(state.cache.Spec.Mode == aimv1alpha1.TemplateCacheModeDedicated) {
			addManual(state.unit, "ArtifactModeConflict", "a resolved artifact mode conflicts with its template cache")
		}
		p.validateArtifactOwnership(state, artifact)
		p.addRuntimeConfigName(state, artifact.Spec.Name)
	}
}

func exactTemplateModelSources(state *unitState) []aimv1alpha1.AIMModelSource {
	if state == nil {
		return nil
	}
	if state.templateSpec != nil && len(state.templateSpec.ModelSources) > 0 {
		return state.templateSpec.ModelSources
	}
	if state.templateStatus != nil {
		return state.templateStatus.ModelSources
	}
	return nil
}

func (p *planner) validateArtifactOwnership(state *unitState, artifact *aimv1alpha1.AIMArtifact) {
	if state.cache == nil || artifact == nil {
		return
	}
	controller := metav1.GetControllerOf(artifact)
	switch state.cache.Spec.Mode {
	case aimv1alpha1.TemplateCacheModeDedicated:
		if len(artifact.OwnerReferences) != 1 ||
			controller == nil ||
			controller.APIVersion != aimv1alpha1.GroupVersion.String() ||
			controller.Kind != resourceKindTemplateCache ||
			controller.Name != state.cache.Name ||
			controller.UID != state.cache.UID ||
			controller.Controller == nil ||
			!*controller.Controller {
			addManual(state.unit, "UnsupportedArtifactOwnership", "the Dedicated artifact does not have the resolved template cache as its exact sole controller owner")
		}
	case aimv1alpha1.TemplateCacheModeShared:
		if len(artifact.OwnerReferences) != 0 {
			addManual(state.unit, "UnsupportedArtifactOwnership", "the Shared artifact is not ownerless")
		}
	}
}

func (p *planner) resolvePersistentVolumeClaim(
	state *unitState,
	artifact *aimv1alpha1.AIMArtifact,
	name string,
) {
	claim := &corev1.PersistentVolumeClaim{}
	if !p.fetchRequired(
		state,
		client.ObjectKey{Namespace: state.service.Namespace, Name: name},
		claim,
		"PersistentVolumeClaim",
	) {
		return
	}
	p.observe(state, claim, corev1.SchemeGroupVersion.String(), "PersistentVolumeClaim", "source-pvc", true)
	if claim.UID == "" {
		addManual(state.unit, "PersistentVolumeClaimIdentityUnavailable", "the artifact PVC UID is unavailable")
	}
	if claim.DeletionTimestamp != nil {
		addManual(state.unit, "PersistentVolumeClaimTerminating", "the artifact PVC is terminating")
	}
	if claim.Status.Phase != corev1.ClaimBound {
		addManual(state.unit, "PersistentVolumeClaimNotReady", "the artifact PVC is not Bound")
	}
	if state.cache != nil &&
		state.cache.Spec.Mode == aimv1alpha1.TemplateCacheModeDedicated &&
		artifact != nil {
		controller := metav1.GetControllerOf(claim)
		if len(claim.OwnerReferences) != 1 ||
			controller == nil ||
			controller.APIVersion != aimv1alpha1.GroupVersion.String() ||
			controller.Kind != resourceKindArtifact ||
			controller.Name != artifact.Name ||
			controller.UID != artifact.UID ||
			controller.Controller == nil ||
			!*controller.Controller {
			addManual(state.unit, "UnsupportedPersistentVolumeClaimOwnership", "the Dedicated artifact PVC does not have the artifact as its exact sole controller owner")
		}
	}
}

func (p *planner) resolveRuntimeConfigs(state *unitState) {
	names := make([]string, 0, len(state.runtimeConfigNames))
	for name := range state.runtimeConfigNames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		found := false
		namespaceConfig := &aimv1alpha1.AIMRuntimeConfig{}
		err := p.reader.Get(p.context, client.ObjectKey{Namespace: state.service.Namespace, Name: name}, namespaceConfig)
		switch {
		case err == nil:
			found = true
			state.runtimeConfigs[runtimeConfigKey(
				aimv1alpha1.AIMResolutionScopeNamespace,
				namespaceConfig.Namespace,
				namespaceConfig.Name,
			)] = namespaceConfig
			p.observe(state, namespaceConfig, aimv1alpha1.GroupVersion.String(), resourceKindRuntimeConfig, "runtime-config", false)
			p.validateRuntimeConfig(state, namespaceConfig, resourceKindRuntimeConfig)
		case apierrors.IsNotFound(err):
		default:
			addWarning(state.unit, "RuntimeConfigReadFailed", fmt.Sprintf("could not read AIMRuntimeConfig %s/%s", state.service.Namespace, name))
			addManual(state.unit, "RuntimeConfigReadFailed", "a referenced runtime configuration could not be read")
		}

		clusterConfig := &aimv1alpha1.AIMClusterRuntimeConfig{}
		err = p.reader.Get(p.context, client.ObjectKey{Name: name}, clusterConfig)
		switch {
		case err == nil:
			found = true
			state.runtimeConfigs[runtimeConfigKey(
				aimv1alpha1.AIMResolutionScopeCluster,
				"",
				clusterConfig.Name,
			)] = clusterConfig
			p.observe(state, clusterConfig, aimv1alpha1.GroupVersion.String(), resourceKindClusterRuntimeConfig, "runtime-config", false)
			p.validateRuntimeConfig(state, clusterConfig, resourceKindClusterRuntimeConfig)
		case apierrors.IsNotFound(err):
		default:
			addWarning(state.unit, "RuntimeConfigReadFailed", fmt.Sprintf("could not read AIMClusterRuntimeConfig %s", name))
			addManual(state.unit, "RuntimeConfigReadFailed", "a referenced cluster runtime configuration could not be read")
		}
		if !found && name != constants.DefaultRuntimeConfigName {
			addManual(state.unit, "RuntimeConfigNotFound", "a named runtime configuration was not found")
		}
	}
	p.validateRuntimeConfigReferences(state)
}

func (p *planner) validateRuntimeConfig(
	state *unitState,
	config client.Object,
	kind string,
) {
	if config.GetDeletionTimestamp() != nil {
		addManual(state.unit, "RuntimeConfigTerminating", fmt.Sprintf("the referenced %s is terminating", kind))
	}
}

func (p *planner) validateRuntimeConfigReferences(state *unitState) {
	for _, reference := range state.runtimeConfigRefs {
		if reference == nil {
			continue
		}
		namespace := reference.Namespace
		if reference.Scope == aimv1alpha1.AIMResolutionScopeNamespace && namespace == "" {
			namespace = state.service.Namespace
		}
		config := state.runtimeConfigs[runtimeConfigKey(reference.Scope, namespace, reference.Name)]
		if config == nil {
			addManual(state.unit, "RuntimeConfigIdentityChanged", "a resolved runtime configuration is no longer present")
			continue
		}
		expectedKind := resourceKindRuntimeConfig
		if reference.Scope == aimv1alpha1.AIMResolutionScopeCluster {
			expectedKind = resourceKindClusterRuntimeConfig
		}
		if reference.UID == "" ||
			config.GetUID() != reference.UID ||
			(reference.Kind != "" && reference.Kind != expectedKind) {
			addManual(state.unit, "RuntimeConfigIdentityChanged", "a resolved runtime configuration identity no longer matches")
		}
	}
}

func runtimeConfigKey(scope aimv1alpha1.AIMResolutionScope, namespace, name string) string {
	return string(scope) + "\x00" + namespace + "\x00" + name
}

func (p *planner) resolveConfigMaps(state *unitState) {
	references := make([]configMapReference, 0, len(state.configMapReferences))
	for _, reference := range state.configMapReferences {
		references = append(references, reference)
	}
	sort.SliceStable(references, func(i, j int) bool {
		left := references[i].namespace + "\x00" + references[i].name + "\x00" + references[i].key
		right := references[j].namespace + "\x00" + references[j].name + "\x00" + references[j].key
		return left < right
	})
	seen := map[string]struct{}{}
	for _, reference := range references {
		key := reference.namespace + "\x00" + reference.name
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		configMap := &corev1.ConfigMap{}
		err := p.reader.Get(
			p.context,
			client.ObjectKey{Namespace: reference.namespace, Name: reference.name},
			configMap,
		)
		if err != nil {
			addWarning(state.unit, "BackupDependencyReadFailed", fmt.Sprintf(
				"could not read referenced ConfigMap %s/%s",
				reference.namespace,
				reference.name,
			))
			continue
		}
		p.observe(state, configMap, corev1.SchemeGroupVersion.String(), "ConfigMap", "referenced-config", false)
	}
}

func (p *planner) planTargetProfile(state *unitState) {
	if state.templateSpec == nil || state.template == nil || state.modelSpec == nil {
		return
	}
	if state.templateSpec.CustomProfile == nil {
		p.planNativeProfileReuse(state)
		return
	}
	desired, sourceYAML, contract, ok := p.buildExactProfile(state)
	if !ok {
		return
	}

	targetIdentity := ResourceIdentity{
		Role:       "target-profile",
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Name:       state.template.GetName(),
	}
	var desiredObject client.Object
	switch state.templateScope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		targetIdentity.Kind = resourceKindProfile
		targetIdentity.Namespace = state.service.Namespace
		desiredProfile := &aimv1alpha2.AIMProfile{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha2.GroupVersion.String(),
				Kind:       resourceKindProfile,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        state.template.GetName(),
				Namespace:   state.service.Namespace,
				Annotations: migrationProfileAnnotations(state.template, state.templateKind, contract),
			},
			Spec: aimv1alpha2.AIMProfileSpec{
				AIMProfileSpecCommon: *desired.DeepCopy(),
				Caching:              copyProfileCaching(state.templateCaching),
			},
		}
		desiredObject = desiredProfile
		p.resolveNamespaceTargetProfile(state, desiredProfile, sourceYAML, contract, &targetIdentity)
	case aimv1alpha1.AIMResolutionScopeCluster:
		targetIdentity.Kind = resourceKindClusterProfile
		desiredProfile := &aimv1alpha2.AIMClusterProfile{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha2.GroupVersion.String(),
				Kind:       resourceKindClusterProfile,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        state.template.GetName(),
				Annotations: migrationProfileAnnotations(state.template, state.templateKind, contract),
			},
			Spec: aimv1alpha2.AIMClusterProfileSpec{
				AIMProfileSpecCommon: *desired.DeepCopy(),
			},
		}
		desiredObject = desiredProfile
		p.resolveClusterTargetProfile(state, desiredProfile, sourceYAML, contract, &targetIdentity)
	}
	if targetIdentity.Kind == "" {
		return
	}
	state.targetProfileObject = desiredObject
	state.unit.Imputations = append(state.unit.Imputations,
		Imputation{
			Field:  "targetProfile.metadata.name",
			Source: "legacy template metadata.name",
			Rule:   "retain exactly",
		},
		Imputation{
			Field:  "targetProfile.kind",
			Source: "resolved legacy template scope",
			Rule:   "map AIMServiceTemplate to AIMProfile and AIMClusterServiceTemplate to AIMClusterProfile",
		},
		Imputation{
			Field:  "targetProfile.metadata.annotations.migration-source-*",
			Source: "resolved legacy template identity",
			Rule:   "record source kind, namespace, name, and UID",
		},
	)
	state.unit.TargetProfile = &targetIdentity
	if hasManualCode(state.unit, "TargetProfileConflict") ||
		hasManualCode(state.unit, "TargetProfileNotReady") ||
		hasManualCode(state.unit, "TargetProfileReadFailed") {
		return
	}
	if targetIdentity.UID != "" {
		state.candidateOperations = append(state.candidateOperations, ProposedOperation{
			Action:   OperationReuse,
			Resource: targetIdentity,
		})
		return
	}
	object, secretLiteral, err := sanitizeObject(desiredObject, targetIdentity.APIVersion, targetIdentity.Kind)
	if err != nil {
		addManual(state.unit, "TargetProfileSerializationFailed", "the exact target profile could not be serialized")
		return
	}
	if secretLiteral {
		addManual(state.unit, "SecretLiteral", "the target profile would contain a secret-looking literal")
		return
	}
	state.candidateOperations = append(state.candidateOperations, ProposedOperation{
		Action:   OperationCreate,
		Resource: targetIdentity,
		Object:   object,
	})
}

func (p *planner) planNativeProfileReuse(state *unitState) {
	candidates, err := p.findNativeProfileCandidates(state)
	if err != nil {
		addWarning(state.unit, "TargetProfileReadFailed", "could not list native profiles for an exact match")
		addManual(state.unit, "TargetProfileReadFailed", "the target native profile could not be checked")
		return
	}
	if len(candidates) > 1 {
		addManual(state.unit, "AmbiguousTargetProfiles", "more than one proven native profile exactly matches the legacy template")
		return
	}
	if len(candidates) == 0 {
		p.classifyMissingNativeProfile(state)
		return
	}

	candidate, err := p.getNativeProfileCandidate(candidates[0])
	if err != nil {
		addWarning(state.unit, "TargetProfileReadFailed", "could not read the uniquely matching native profile")
		addManual(state.unit, "TargetProfileReadFailed", "the target native profile could not be checked")
		return
	}
	if !nativeProfileMatchesLegacy(
		state,
		candidate.object,
		nativeProfileSpec(candidate.object),
		nativeProfileCaching(candidate.object),
	) || !validProfileProvenance(candidate.object, candidate.status, state) {
		addManual(state.unit, "TargetProfileConflict", "the uniquely matching native profile changed while it was being selected")
		return
	}
	identity := p.observe(
		state,
		candidate.object,
		aimv1alpha2.GroupVersion.String(),
		candidate.kind,
		"target-profile",
		true,
	)
	state.unit.TargetProfile = &identity
	state.targetProfileObject = candidate.object
	if candidate.object.GetDeletionTimestamp() != nil ||
		candidate.status.Status != constants.AIMStatusReady ||
		candidate.object.GetGeneration() <= 0 ||
		candidate.status.ObservedGeneration != candidate.object.GetGeneration() {
		addManual(state.unit, "TargetProfileNotReady", "the matching native profile is not current and Ready")
		return
	}
	state.unit.Imputations = append(state.unit.Imputations,
		Imputation{
			Field:  "spec.profile.name",
			Source: "unique existing native profile with exact legacy semantics and source provenance",
			Rule:   "reuse the proven native profile identity",
		},
	)
	state.candidateOperations = append(state.candidateOperations, ProposedOperation{
		Action:   OperationReuse,
		Resource: identity,
	})
}

type nativeProfileCandidate struct {
	object client.Object
	status *aimv1alpha2.AIMProfileStatus
	kind   string
}

func (p *planner) findNativeProfileCandidates(state *unitState) ([]nativeProfileCandidate, error) {
	var candidates []nativeProfileCandidate
	switch state.templateScope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		profiles := &aimv1alpha2.AIMProfileList{}
		if err := p.reader.List(p.context, profiles, client.InNamespace(state.service.Namespace)); err != nil {
			return nil, err
		}
		for index := range profiles.Items {
			profile := &profiles.Items[index]
			if nativeProfileMatchesLegacy(
				state,
				profile,
				&profile.Spec.AIMProfileSpecCommon,
				profile.Spec.Caching,
			) && validProfileProvenance(profile, &profile.Status, state) {
				candidates = append(candidates, nativeProfileCandidate{
					object: profile,
					status: &profile.Status,
					kind:   resourceKindProfile,
				})
			}
		}
	case aimv1alpha1.AIMResolutionScopeCluster:
		profiles := &aimv1alpha2.AIMClusterProfileList{}
		if err := p.reader.List(p.context, profiles); err != nil {
			return nil, err
		}
		for index := range profiles.Items {
			profile := &profiles.Items[index]
			if nativeProfileMatchesLegacy(
				state,
				profile,
				&profile.Spec.AIMProfileSpecCommon,
				nil,
			) && validProfileProvenance(profile, &profile.Status, state) {
				candidates = append(candidates, nativeProfileCandidate{
					object: profile,
					status: &profile.Status,
					kind:   resourceKindClusterProfile,
				})
			}
		}
	default:
		return nil, nil
	}

	return candidates, nil
}

func (p *planner) getNativeProfileCandidate(
	candidate nativeProfileCandidate,
) (nativeProfileCandidate, error) {
	key := client.ObjectKey{
		Namespace: candidate.object.GetNamespace(),
		Name:      candidate.object.GetName(),
	}
	switch candidate.kind {
	case resourceKindProfile:
		profile := &aimv1alpha2.AIMProfile{}
		if err := p.reader.Get(p.context, key, profile); err != nil {
			return nativeProfileCandidate{}, err
		}
		return nativeProfileCandidate{object: profile, status: &profile.Status, kind: candidate.kind}, nil
	case resourceKindClusterProfile:
		profile := &aimv1alpha2.AIMClusterProfile{}
		if err := p.reader.Get(p.context, key, profile); err != nil {
			return nativeProfileCandidate{}, err
		}
		return nativeProfileCandidate{object: profile, status: &profile.Status, kind: candidate.kind}, nil
	default:
		return nativeProfileCandidate{}, fmt.Errorf("unsupported native profile kind %q", candidate.kind)
	}
}

func nativeProfileSpec(object client.Object) *aimv1alpha2.AIMProfileSpecCommon {
	switch profile := object.(type) {
	case *aimv1alpha2.AIMProfile:
		return &profile.Spec.AIMProfileSpecCommon
	case *aimv1alpha2.AIMClusterProfile:
		return &profile.Spec.AIMProfileSpecCommon
	default:
		return nil
	}
}

func nativeProfileCaching(object client.Object) *aimv1alpha2.AIMProfileCachingConfig {
	profile, ok := object.(*aimv1alpha2.AIMProfile)
	if !ok {
		return nil
	}
	return profile.Spec.Caching
}

func (p *planner) classifyMissingNativeProfile(state *unitState) {
	identity := ResourceIdentity{
		Role:       "target-profile",
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Name:       state.template.GetName(),
	}
	var object client.Object
	switch state.templateScope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		identity.Kind = resourceKindProfile
		identity.Namespace = state.service.Namespace
		object = &aimv1alpha2.AIMProfile{}
	case aimv1alpha1.AIMResolutionScopeCluster:
		identity.Kind = resourceKindClusterProfile
		object = &aimv1alpha2.AIMClusterProfile{}
	default:
		return
	}
	err := p.reader.Get(p.context, client.ObjectKey{
		Namespace: identity.Namespace,
		Name:      identity.Name,
	}, object)
	if apierrors.IsNotFound(err) {
		state.unit.TargetProfile = &identity
		addManual(state.unit, "LossyDiscoveredStatus", "the discovered template has no exact native profile to reuse")
		return
	}
	if err != nil {
		addWarning(state.unit, "TargetProfileReadFailed", fmt.Sprintf("could not read the target %s", identity.Kind))
		addManual(state.unit, "TargetProfileReadFailed", "the target native profile could not be checked")
		return
	}
	identity = p.observe(
		state,
		object,
		aimv1alpha2.GroupVersion.String(),
		identity.Kind,
		identity.Role,
		true,
	)
	state.unit.TargetProfile = &identity
	addManual(state.unit, "TargetProfileConflict", "an object with the retained template name exists but is not an exact, proven native profile match")
}

func nativeProfileMatchesLegacy(
	state *unitState,
	object client.Object,
	candidate *aimv1alpha2.AIMProfileSpecCommon,
	caching *aimv1alpha2.AIMProfileCachingConfig,
) bool {
	if state == nil ||
		object == nil ||
		state.templateSpec == nil ||
		state.templateStatus == nil ||
		state.templateStatus.Profile == nil ||
		candidate == nil {
		return false
	}
	if !nativeProfileMatchesDiscovered(state.templateStatus.Profile, state.templateStatus.ModelSources, candidate) ||
		!nativeProfileMatchesTemplateSpec(state.templateSpec, candidate) ||
		!nativeProfileMatchesImageAndWiring(state, candidate) {
		return false
	}
	if state.templateScope == aimv1alpha1.AIMResolutionScopeNamespace &&
		!equality.Semantic.DeepEqual(caching, copyProfileCaching(state.templateCaching)) {
		return false
	}
	_, found, err := profileyaml.FromAnnotations(object.GetAnnotations())
	return err == nil && found
}

func nativeProfileMatchesDiscovered(
	discovered *aimv1alpha1.AIMDiscoveredProfile,
	modelSources []aimv1alpha1.AIMModelSource,
	candidate *aimv1alpha2.AIMProfileSpecCommon,
) bool {
	metadata := discovered.Metadata
	return metadata.AimID != "" &&
		candidate.AimId == metadata.AimID &&
		metadata.ModelID != "" &&
		candidate.ModelId == metadata.ModelID &&
		metadata.Engine != "" &&
		candidate.Engine == metadata.Engine &&
		metadata.GPU != "" &&
		candidate.AcceleratorModel == metadata.GPU &&
		metadata.GPUCount > 0 &&
		candidate.AcceleratorCount == metadata.GPUCount &&
		metadata.Metric != "" &&
		candidate.Metric == metadata.Metric &&
		metadata.Precision != "" &&
		candidate.Precision == metadata.Precision &&
		metadata.Type != "" &&
		candidate.Type == metadata.Type &&
		candidate.AcceleratorType == aimv1alpha1.AcceleratorTypeGPU &&
		candidate.AcceleratorPartitioningMode == "unpartitioned" &&
		semanticJSONEqual(candidate.EngineArgs, discovered.EngineArgs) &&
		equality.Semantic.DeepEqual(candidate.EngineEnv, discovered.EnvVars) &&
		len(modelSources) > 0 &&
		modelSourcesMatchDiscovered(candidate.ModelSources, modelSources)
}

func nativeProfileMatchesTemplateSpec(
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	candidate *aimv1alpha2.AIMProfileSpecCommon,
) bool {
	if (templateSpec.ProfileId != "" && candidate.ProfileId != templateSpec.ProfileId) ||
		(templateSpec.AimId != "" && candidate.AimId != templateSpec.AimId) ||
		(templateSpec.ModelId != "" && candidate.ModelId != templateSpec.ModelId) ||
		(templateSpec.Metric != nil && candidate.Metric != *templateSpec.Metric) ||
		(templateSpec.Precision != nil && candidate.Precision != *templateSpec.Precision) ||
		(templateSpec.Type != nil && candidate.Type != *templateSpec.Type) ||
		(len(templateSpec.ModelSources) > 0 &&
			!equality.Semantic.DeepEqual(candidate.ModelSources, templateSpec.ModelSources)) {
		return false
	}
	return nativeProfileMatchesTemplateHardware(templateSpec.Hardware, candidate)
}

func modelSourcesMatchDiscovered(
	candidate []aimv1alpha1.AIMModelSource,
	discovered []aimv1alpha1.AIMModelSource,
) bool {
	if len(candidate) != len(discovered) {
		return false
	}
	for index := range candidate {
		left := candidate[index]
		right := discovered[index]
		if left.Size != nil && right.Size != nil && left.Size.Cmp(*right.Size) != 0 {
			return false
		}
		left.Size = nil
		right.Size = nil
		if !equality.Semantic.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func nativeProfileMatchesTemplateHardware(
	hardware *aimv1alpha1.AIMHardwareRequirements,
	candidate *aimv1alpha2.AIMProfileSpecCommon,
) bool {
	if hardware == nil {
		return true
	}
	if hardware.CPU != nil || hardware.GPU == nil {
		return false
	}
	gpu := hardware.GPU
	vendor, _, ok := legacyAcceleratorVendor(gpu.ResourceName)
	candidateVendor := candidate.AcceleratorVendor
	if candidateVendor == "" {
		candidateVendor = aimv1alpha1.AcceleratorVendorAMD
	}
	return ok &&
		candidateVendor == vendor &&
		(gpu.Model == "" || candidate.AcceleratorModel == gpu.Model) &&
		(gpu.Requests <= 0 || candidate.AcceleratorCount == gpu.Requests) &&
		gpu.MinVRAM == nil
}

func nativeProfileMatchesImageAndWiring(
	state *unitState,
	candidate *aimv1alpha2.AIMProfileSpecCommon,
) bool {
	templateSpec := state.templateSpec
	expectedImage := strings.TrimSpace(state.template.GetAnnotations()[constants.AnnotationDeploymentImageRef])
	if expectedImage == "" {
		expectedImage = strings.TrimSpace(state.modelSpec.Image)
	}
	return expectedImage != "" &&
		candidate.Image == expectedImage &&
		equality.Semantic.DeepEqual(candidate.ContainerEnv, templateSpec.Env) &&
		equality.Semantic.DeepEqual(candidate.ImagePullSecrets, templateSpec.ImagePullSecrets) &&
		candidate.ServiceAccountName == templateSpec.ServiceAccountName
}

func (p *planner) buildExactProfile(
	state *unitState,
) (*aimv1alpha2.AIMProfileSpecCommon, []byte, profileyaml.Contract, bool) {
	spec := state.templateSpec
	if spec.CustomProfile == nil {
		addManual(state.unit, "LossyDiscoveredStatus", "the template has no exact inline customProfile")
		return nil, nil, profileyaml.Contract{}, false
	}
	if spec.AimId == "" || spec.ModelId == "" || spec.Metric == nil || spec.Precision == nil {
		addManual(state.unit, "IncompleteCustomProfile", "the inline customProfile identity is incomplete")
		return nil, nil, profileyaml.Contract{}, false
	}
	if spec.Hardware == nil || spec.Hardware.GPU == nil || spec.Hardware.CPU != nil {
		addManual(state.unit, "UnsupportedHardware", "the inline customProfile does not have one exact GPU mapping")
		return nil, nil, profileyaml.Contract{}, false
	}
	if spec.Hardware.GPU.MinVRAM != nil {
		addManual(state.unit, "UnsupportedHardware", "minimum-VRAM hardware selection cannot be mapped exactly")
		return nil, nil, profileyaml.Contract{}, false
	}
	if len(spec.ModelSources) != 1 {
		addManual(state.unit, "UnsupportedModelSources", "the template does not declare exactly one model source")
		return nil, nil, profileyaml.Contract{}, false
	}
	if state.templateStatus == nil ||
		!equality.Semantic.DeepEqual(state.templateStatus.ModelSources, spec.ModelSources) {
		addManual(state.unit, "ModelSourceConflict", "template status does not exactly match desired model sources")
		return nil, nil, profileyaml.Contract{}, false
	}

	image := strings.TrimSpace(state.template.GetAnnotations()[constants.AnnotationDeploymentImageRef])
	imageSource := "source template deployment-image annotation"
	if image == "" {
		image = strings.TrimSpace(state.modelSpec.Image)
		imageSource = "resolved model spec.image"
	}
	if image == "" {
		addManual(state.unit, "MissingDeploymentImage", "the legacy deployment image cannot be proven from desired state")
		return nil, nil, profileyaml.Contract{}, false
	}

	sourceYAML, filename, err := v1alpha1utils.AssembleProfileYAML(spec)
	if err != nil {
		addManual(state.unit, "InvalidCustomProfile", "the inline customProfile cannot be rendered exactly")
		return nil, nil, profileyaml.Contract{}, false
	}
	contract, err := profileyaml.Inspect(sourceYAML)
	if err != nil {
		addManual(state.unit, "InvalidProfileYAMLContract", "the inline customProfile wire contract cannot be inspected")
		return nil, nil, profileyaml.Contract{}, false
	}

	profileType := aimv1alpha1.AIMProfileTypeUnoptimized
	typeSource := "documented inline customProfile default"
	if spec.Type != nil {
		profileType = *spec.Type
		typeSource = "source template spec.type"
	}
	acceleratorVendor, vendorSource, ok := legacyAcceleratorVendor(spec.Hardware.GPU.ResourceName)
	if !ok {
		addManual(state.unit, "UnsupportedHardware", "the GPU resource name has no exact native-profile mapping")
		return nil, nil, profileyaml.Contract{}, false
	}

	desired := &aimv1alpha2.AIMProfileSpecCommon{
		AimId:                       spec.AimId,
		ModelId:                     spec.ModelId,
		ProfileId:                   strings.TrimSuffix(filename, ".yaml"),
		Engine:                      "vllm",
		Metric:                      *spec.Metric,
		Precision:                   *spec.Precision,
		Type:                        profileType,
		AutoSelectionPolicy:         aimv1alpha1.AIMProfileAutoSelectionPolicyOptimized,
		Primary:                     false,
		ManualSelectionOnly:         false,
		EngineArgs:                  spec.CustomProfile.EngineArgs.DeepCopy(),
		EngineEnv:                   copyStringMap(spec.CustomProfile.EnvVars),
		AcceleratorVendor:           acceleratorVendor,
		AcceleratorModel:            spec.Hardware.GPU.Model,
		AcceleratorType:             aimv1alpha1.AcceleratorTypeGPU,
		AcceleratorCount:            spec.Hardware.GPU.Requests,
		AcceleratorPartitioningMode: "unpartitioned",
		Resources:                   spec.Resources.DeepCopy(),
		Image:                       image,
		ModelSources:                copyModelSources(spec.ModelSources),
		ContainerEnv:                copyEnv(spec.Env),
		ImagePullSecrets:            append([]corev1.LocalObjectReference(nil), spec.ImagePullSecrets...),
		ServiceAccountName:          spec.ServiceAccountName,
	}
	targetYAML, _, err := serving.AssembleProfileYAMLForContract(desired, contract)
	if err != nil || !semanticYAMLEqual(sourceYAML, targetYAML) {
		addManual(state.unit, "ProfileRenderingMismatch", "the native profile does not reproduce the inline customProfile exactly")
		return nil, nil, profileyaml.Contract{}, false
	}

	state.unit.Imputations = append(state.unit.Imputations, []Imputation{
		{Field: "spec.aimId", Source: "source template spec.aimId", Rule: "copied exactly"},
		{Field: "spec.modelId", Source: "source template spec.modelId", Rule: "copied exactly"},
		{Field: "spec.profileId", Source: "legacy custom-profile renderer filename", Rule: "remove the .yaml suffix"},
		{Field: "spec.engine", Source: "legacy custom-profile renderer", Rule: "use its fixed vllm engine"},
		{Field: "spec.metric", Source: "source template spec.metric", Rule: "copied exactly"},
		{Field: "spec.precision", Source: "source template spec.precision", Rule: "copied exactly"},
		{Field: "spec.type", Source: typeSource, Rule: "preserve the explicit value or use unoptimized"},
		{Field: "spec.autoSelectionPolicy", Source: "AIMProfile API default", Rule: "use optimized"},
		{Field: "spec.primary", Source: "legacy custom-profile behavior", Rule: "use false"},
		{Field: "spec.manualSelectionOnly", Source: "legacy custom-profile renderer", Rule: "use false"},
		{Field: "spec.engineArgs", Source: "source template spec.customProfile.engineArgs", Rule: "copied exactly"},
		{Field: "spec.engineEnv", Source: "source template spec.customProfile.envVars", Rule: "rename the field and copy exactly"},
		{Field: "spec.acceleratorVendor", Source: vendorSource, Rule: "map the Kubernetes GPU resource name"},
		{Field: "spec.acceleratorModel", Source: "source template spec.hardware.gpu.model", Rule: "copied exactly"},
		{Field: "spec.acceleratorType", Source: "source template GPU hardware", Rule: "use gpu"},
		{Field: "spec.acceleratorCount", Source: "source template spec.hardware.gpu.requests", Rule: "copied exactly"},
		{Field: "spec.acceleratorPartitioningMode", Source: "AIMProfile API default", Rule: "use unpartitioned"},
		{Field: "spec.image", Source: imageSource, Rule: "copied exactly"},
		{Field: "spec.modelSources", Source: "source template spec.modelSources", Rule: "copied exactly"},
		{Field: "spec.containerEnv", Source: "source template spec.env", Rule: "rename the field and copy exactly"},
		{Field: "spec.resources", Source: "source template spec.resources", Rule: "copied exactly"},
		{Field: "spec.imagePullSecrets", Source: "source template spec.imagePullSecrets", Rule: "copied exactly"},
		{Field: "spec.serviceAccountName", Source: "source template spec.serviceAccountName", Rule: "copied exactly"},
	}...)
	if state.templateScope == aimv1alpha1.AIMResolutionScopeNamespace {
		state.unit.Imputations = append(state.unit.Imputations, Imputation{
			Field:  "spec.caching",
			Source: "source template spec.caching",
			Rule:   "copied exactly",
		})
	}
	return desired, sourceYAML, contract, true
}

func (p *planner) resolveNamespaceTargetProfile(
	state *unitState,
	desired *aimv1alpha2.AIMProfile,
	sourceYAML []byte,
	contract profileyaml.Contract,
	targetIdentity *ResourceIdentity,
) {
	existing := &aimv1alpha2.AIMProfile{}
	err := p.reader.Get(
		p.context,
		client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name},
		existing,
	)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		addWarning(state.unit, "TargetProfileReadFailed", "could not read the target AIMProfile")
		addManual(state.unit, "TargetProfileReadFailed", "the target profile could not be checked")
		return
	}
	*targetIdentity = p.observe(state, existing, aimv1alpha2.GroupVersion.String(), resourceKindProfile, "target-profile", true)
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) ||
		!profileContractMatches(existing.Annotations, contract) ||
		!validProfileProvenance(existing, &existing.Status, state) ||
		!profileRendersExactly(&existing.Spec.AIMProfileSpecCommon, existing.Annotations, existing.Status.Origin, sourceYAML) {
		addManual(state.unit, "TargetProfileConflict", "an existing AIMProfile with the retained template name does not exactly match")
		return
	}
	if existing.DeletionTimestamp != nil ||
		existing.Status.Status != constants.AIMStatusReady ||
		existing.Generation <= 0 ||
		existing.Status.ObservedGeneration != existing.Generation {
		addManual(state.unit, "TargetProfileNotReady", "the matching existing AIMProfile is not current and Ready")
	}
}

func (p *planner) resolveClusterTargetProfile(
	state *unitState,
	desired *aimv1alpha2.AIMClusterProfile,
	sourceYAML []byte,
	contract profileyaml.Contract,
	targetIdentity *ResourceIdentity,
) {
	existing := &aimv1alpha2.AIMClusterProfile{}
	err := p.reader.Get(p.context, client.ObjectKey{Name: desired.Name}, existing)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		addWarning(state.unit, "TargetProfileReadFailed", "could not read the target AIMClusterProfile")
		addManual(state.unit, "TargetProfileReadFailed", "the target cluster profile could not be checked")
		return
	}
	*targetIdentity = p.observe(state, existing, aimv1alpha2.GroupVersion.String(), resourceKindClusterProfile, "target-profile", true)
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) ||
		!profileContractMatches(existing.Annotations, contract) ||
		!validProfileProvenance(existing, &existing.Status, state) ||
		!profileRendersExactly(&existing.Spec.AIMProfileSpecCommon, existing.Annotations, existing.Status.Origin, sourceYAML) {
		addManual(state.unit, "TargetProfileConflict", "an existing AIMClusterProfile with the retained template name does not exactly match")
		return
	}
	if existing.DeletionTimestamp != nil ||
		existing.Status.Status != constants.AIMStatusReady ||
		existing.Generation <= 0 ||
		existing.Status.ObservedGeneration != existing.Generation {
		addManual(state.unit, "TargetProfileNotReady", "the matching existing AIMClusterProfile is not current and Ready")
	}
}

func (p *planner) planTargetCache(state *unitState) {
	if state.unit.TargetProfile == nil || state.cache == nil || state.targetProfileObject == nil {
		return
	}
	desired, err := buildControllerCompatibleProfileCache(
		state.service,
		*state.unit.TargetProfile,
		state.targetProfileObject,
	)
	if err != nil {
		addManual(state.unit, "TargetCacheShapeFailed", "the controller-compatible target profile cache could not be generated")
		return
	}
	state.targetCacheObject = desired
	if desired.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated &&
		!p.allowDedicatedApplyTests {
		addManual(
			state.unit,
			dedicatedHandoffReasonCode,
			dedicatedHandoffReason,
		)
		return
	}
	if !p.validateSharedTargetCacheAuthority(state, desired) {
		return
	}
	identity := ResourceIdentity{
		Role:       "target-profile-cache",
		APIVersion: aimv1alpha2.GroupVersion.String(),
		Kind:       resourceKindProfileCache,
		Namespace:  desired.Namespace,
		Name:       desired.Name,
	}
	state.unit.Imputations = append(state.unit.Imputations,
		Imputation{Field: "targetCache.metadata.name", Source: "profile-cache naming helper and pinned service identity", Rule: "generate deterministically"},
		Imputation{Field: "targetCache.spec.profileName", Source: "retained target profile name", Rule: "copied exactly"},
		Imputation{Field: "targetCache.spec.profileScope", Source: "resolved legacy template scope", Rule: "copied exactly"},
		Imputation{Field: "targetCache.spec.storageClassName", Source: "AIMService profile-cache controller shape", Rule: "copy the service storage override exactly"},
		Imputation{Field: "targetCache.spec.env", Source: "AIMProfile and AIMService caching environments", Rule: "merge with service values taking precedence"},
		Imputation{Field: "targetCache.spec.runtimeConfigName", Source: "AIMService runtime configuration reference", Rule: "copied exactly"},
		Imputation{Field: "targetCache.spec.mode", Source: "AIMService effective caching mode", Rule: "map Dedicated or Shared exactly"},
		Imputation{Field: "targetCache.spec.requiresAdapterDisk", Source: "AIMService adapter mode and references", Rule: "enable when adapters require a disk"},
	)
	existing := &aimv1alpha2.AIMProfileCache{}
	err = p.reader.Get(p.context, client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name}, existing)
	switch {
	case apierrors.IsNotFound(err):
		object, secretLiteral, marshalErr := sanitizeObject(desired, identity.APIVersion, identity.Kind)
		if marshalErr != nil {
			addManual(state.unit, "TargetCacheSerializationFailed", "the target profile cache could not be serialized")
			return
		}
		if secretLiteral {
			addManual(state.unit, "SecretLiteral", "the target profile cache would contain a secret-looking literal")
			return
		}
		state.candidateOperations = append(state.candidateOperations, ProposedOperation{
			Action:   OperationCreate,
			Resource: identity,
			Object:   object,
		})
	case err != nil:
		addWarning(state.unit, "TargetCacheReadFailed", "could not read the target AIMProfileCache")
		addManual(state.unit, "TargetCacheReadFailed", "the target profile cache could not be checked")
	default:
		if desired.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated {
			addManual(state.unit, "TargetCacheConflict", "Dedicated handoff requires the deterministic target AIMProfileCache to be absent when planned")
			return
		}
		if len(existing.OwnerReferences) != 0 {
			addManual(state.unit, "TargetCacheConflict", "the existing Shared target AIMProfileCache is not ownerless")
			return
		}
		identity = p.observe(state, existing, aimv1alpha2.GroupVersion.String(), identity.Kind, identity.Role, true)
		if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
			addManual(state.unit, "TargetCacheConflict", "the existing target AIMProfileCache has a conflicting spec")
			return
		}
		if !targetCachePreservesArtifacts(existing, state.artifacts) {
			addManual(state.unit, "TargetCacheConflict", "the existing target AIMProfileCache does not report the exact source artifacts")
			return
		}
		state.candidateOperations = append(state.candidateOperations, ProposedOperation{
			Action:   OperationReuse,
			Resource: identity,
		})
	}
}

func (p *planner) validateSharedTargetCacheAuthority(
	state *unitState,
	desired *aimv1alpha2.AIMProfileCache,
) bool {
	if desired.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
		return true
	}
	if profile, ok := state.targetProfileObject.(*aimv1alpha2.AIMProfile); ok &&
		profile.Spec.Caching != nil &&
		profile.Spec.Caching.Enabled {
		addManual(
			state.unit,
			"ProfileManagedSharedCache",
			"the target profile would create a second Shared cache; automatic migration requires one authoritative service-driven cache",
		)
		return false
	}

	var caches aimv1alpha2.AIMProfileCacheList
	if err := p.reader.List(
		p.context,
		&caches,
		client.InNamespace(desired.Namespace),
	); err != nil {
		addWarning(state.unit, "TargetCacheListFailed", "could not list Shared target cache candidates")
		addManual(state.unit, "TargetCacheListFailed", "Shared cache authority could not be proven")
		return false
	}
	for index := range caches.Items {
		cache := &caches.Items[index]
		if cache.Name == desired.Name ||
			cache.Spec.ProfileName != desired.Spec.ProfileName ||
			normalizedProfileScope(cache.Spec.ProfileScope) !=
				normalizedProfileScope(desired.Spec.ProfileScope) ||
			cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
			continue
		}
		addManual(
			state.unit,
			"SharedCacheAuthorityAmbiguous",
			fmt.Sprintf(
				"Shared AIMProfileCache %s/%s also matches the target profile",
				cache.Namespace,
				cache.Name,
			),
		)
		return false
	}
	return true
}

func buildControllerCompatibleProfileCache(
	service *aimv1alpha1.AIMService,
	targetProfile ResourceIdentity,
	profile client.Object,
) (*aimv1alpha2.AIMProfileCache, error) {
	if service == nil || profile == nil {
		return nil, fmt.Errorf("service and target profile are required")
	}
	return buildControllerCompatibleProfileCacheFromSpec(
		service.Namespace,
		service.Name,
		service.UID,
		&service.Spec,
		targetProfile,
		profile,
	)
}

func buildControllerCompatibleProfileCacheFromSpec(
	serviceNamespace, serviceName string,
	serviceUID types.UID,
	serviceSpec *aimv1alpha1.AIMServiceSpec,
	targetProfile ResourceIdentity,
	profile client.Object,
) (*aimv1alpha2.AIMProfileCache, error) {
	if serviceSpec == nil || profile == nil {
		return nil, fmt.Errorf("service spec and target profile are required")
	}
	scope := aimv1alpha1.AIMResolutionScopeNamespace
	var profileCaching *aimv1alpha2.AIMProfileCachingConfig
	switch typed := profile.(type) {
	case *aimv1alpha2.AIMProfile:
		if targetProfile.Kind != resourceKindProfile ||
			typed.Namespace != serviceNamespace ||
			typed.Name != targetProfile.Name {
			return nil, fmt.Errorf("namespace target profile does not match its pinned identity")
		}
		profileCaching = typed.Spec.Caching
	case *aimv1alpha2.AIMClusterProfile:
		if targetProfile.Kind != resourceKindClusterProfile ||
			typed.Namespace != "" ||
			typed.Name != targetProfile.Name {
			return nil, fmt.Errorf("cluster target profile does not match its pinned identity")
		}
		scope = aimv1alpha1.AIMResolutionScopeCluster
	default:
		return nil, fmt.Errorf("unsupported target profile type %T", profile)
	}

	name, err := v1alpha2aimservice.GenerateProfileCacheName(
		targetProfile.Name,
		serviceNamespace,
		serviceName,
		string(serviceUID),
		serviceSpec.GetCachingMode(),
		scope,
	)
	if err != nil {
		return nil, fmt.Errorf("generate target profile-cache name: %w", err)
	}
	var cacheEnv []corev1.EnvVar
	if profileCaching != nil {
		cacheEnv = utils.MergeEnvVars(cacheEnv, profileCaching.Env)
	}
	if serviceSpec.Caching != nil {
		cacheEnv = utils.MergeEnvVars(cacheEnv, serviceSpec.Caching.Env)
	}
	storageClassName := ""
	if serviceSpec.Storage != nil && serviceSpec.Storage.DefaultStorageClassName != nil {
		storageClassName = *serviceSpec.Storage.DefaultStorageClassName
	}
	mode := aimv1alpha2.ProfileCacheModeShared
	if serviceSpec.GetCachingMode() == aimv1alpha1.CachingModeDedicated {
		mode = aimv1alpha2.ProfileCacheModeDedicated
	}
	serviceLabel, err := utils.SanitizeLabelValue(serviceName)
	if err != nil {
		return nil, fmt.Errorf("sanitize service label: %w", err)
	}
	return &aimv1alpha2.AIMProfileCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       resourceKindProfileCache,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: serviceNamespace,
			Labels: map[string]string{
				constants.LabelService: serviceLabel,
			},
		},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:         targetProfile.Name,
			ProfileScope:        scope,
			StorageClassName:    storageClassName,
			Env:                 cacheEnv,
			RuntimeConfigRef:    serviceSpec.RuntimeConfigRef,
			Mode:                mode,
			RequiresAdapterDisk: serviceSpec.AdaptersEnabled(),
		},
	}, nil
}

func (p *planner) planArtifactProtection(state *unitState) {
	if state.cache == nil ||
		state.cache.Spec.Mode != aimv1alpha1.TemplateCacheModeShared ||
		len(state.artifacts) != 1 {
		return
	}
	artifactIdentity := findObservedIdentity(state.unit.ObservedResources, "source-artifact")
	if artifactIdentity == nil {
		return
	}
	resource := *artifactIdentity
	resource.Role = roleSourceArtifactProtection
	state.candidateOperations = append(state.candidateOperations, ProposedOperation{
		Action:   OperationPatch,
		Resource: resource,
		Patch: map[string]any{
			"apiVersion": aimv1alpha1.GroupVersion.String(),
			"kind":       resourceKindArtifact,
			"metadata": map[string]any{
				"name":      resource.Name,
				"namespace": resource.Namespace,
				"annotations": map[string]any{
					aimv1alpha1.ArtifactEvictionProtectedAnnotation: aimv1alpha1.ArtifactEvictionProtectedValue,
				},
			},
		},
	})
	state.unit.Imputations = append(state.unit.Imputations, Imputation{
		Field:  "sourceArtifact.metadata.annotations." + aimv1alpha1.ArtifactEvictionProtectedAnnotation,
		Source: "verified Shared profile-cache adoption",
		Rule:   "set to true after the exact predecessor artifact UID and PVC are reported",
	})
}

func findObservedIdentity(identities []ResourceIdentity, role string) *ResourceIdentity {
	for index := range identities {
		if identities[index].Role == role {
			return &identities[index]
		}
	}
	return nil
}

func (p *planner) planServicePatch(state *unitState) {
	if state.unit.TargetProfile == nil {
		return
	}
	state.unit.Imputations = append(state.unit.Imputations,
		Imputation{
			Field:  "service.spec.profile.name",
			Source: "retained target profile name",
			Rule:   "set the exact direct profile reference",
		},
		Imputation{
			Field:  "service.spec.template",
			Source: "v1alpha2 profile-pipeline cutover",
			Rule:   "remove the mutually exclusive legacy template selection",
		},
	)
	identity := state.unit.Service
	identity.Role = "service-cutover"
	identity.APIVersion = aimv1alpha2.GroupVersion.String()
	state.candidateOperations = append(state.candidateOperations, ProposedOperation{
		Action:   OperationPatch,
		Resource: identity,
		Patch: map[string]any{
			"apiVersion": aimv1alpha2.GroupVersion.String(),
			"kind":       "AIMService",
			"metadata": map[string]any{
				"name":      state.service.Name,
				"namespace": state.service.Namespace,
				"annotations": map[string]any{
					constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
					constants.AnnotationMigrationPreserveInferenceServiceGeneration: strconv.FormatInt(
						state.service.Generation+1,
						10,
					),
				},
			},
			"spec": map[string]any{
				"profile": map[string]any{
					"name": state.unit.TargetProfile.Name,
				},
				"template": nil,
			},
		},
	})
}

func (p *planner) fetchRequired(
	state *unitState,
	key client.ObjectKey,
	object client.Object,
	kind string,
) bool {
	if err := p.reader.Get(p.context, key, object); err != nil {
		code := kind + "NotFound"
		message := fmt.Sprintf("the referenced %s was not found", kind)
		if !apierrors.IsNotFound(err) {
			code = kind + "ReadFailed"
			message = fmt.Sprintf("the referenced %s could not be read", kind)
			addWarning(state.unit, code, message)
		}
		addManual(state.unit, code, message)
		return false
	}
	return true
}

func (p *planner) observe(
	state *unitState,
	object client.Object,
	apiVersion, kind, role string,
	enforceManagement bool,
) ResourceIdentity {
	digest, secretLiteral, err := contentDigest(object, apiVersion, kind)
	identity := ResourceIdentity{
		Role:            role,
		APIVersion:      apiVersion,
		Kind:            kind,
		Namespace:       object.GetNamespace(),
		Name:            object.GetName(),
		UID:             string(object.GetUID()),
		ResourceVersion: object.GetResourceVersion(),
		Generation:      object.GetGeneration(),
		ContentDigest:   digest,
	}
	if err != nil {
		addWarning(state.unit, "IdentityDigestFailed", fmt.Sprintf("could not hash %s %s", kind, object.GetName()))
	}
	if secretLiteral {
		addManual(state.unit, "SecretLiteral", fmt.Sprintf("%s %s contains a secret-looking literal", kind, object.GetName()))
	}
	if enforceManagement && externallyManaged(object) {
		addManual(state.unit, "DeclaredDesiredStateManagement", fmt.Sprintf("%s %s declares external desired-state management", kind, object.GetName()))
	}
	appendIdentity(state.unit, identity)
	p.result.backupResources = append(p.result.backupResources, backupResource{
		unitID:     state.unit.ID,
		role:       role,
		apiVersion: apiVersion,
		kind:       kind,
		object:     object,
	})
	p.collectReferences(state, object)
	return identity
}

func (p *planner) collectReferences(state *unitState, object client.Object) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return
	}
	walkReferences(value, "", state.service.Namespace, state)
}

func walkReferences(value any, path, namespace string, state *unitState) {
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["secretKeyRef"].(map[string]any); ok {
			addSecretReference(state.unit, namespace, stringValue(reference["name"]), stringValue(reference["key"]), path)
		}
		if reference, ok := typed["configMapKeyRef"].(map[string]any); ok {
			state.addConfigMapReference(namespace, stringValue(reference["name"]), stringValue(reference["key"]), path)
		}
		if reference, ok := typed["credentialsSecretRef"].(map[string]any); ok {
			name := stringValue(reference["name"])
			for key, item := range reference {
				if strings.HasSuffix(strings.ToLower(key), "key") {
					addSecretReference(state.unit, namespace, name, stringValue(item), path)
				}
			}
		}
		if reference, ok := typed["caBundleRef"].(map[string]any); ok {
			switch stringValue(reference["kind"]) {
			case "Secret":
				addSecretReference(state.unit, namespace, stringValue(reference["name"]), stringValue(reference["key"]), path)
			case "ConfigMap":
				state.addConfigMapReference(namespace, stringValue(reference["name"]), stringValue(reference["key"]), path)
			}
		}
		for key, item := range typed {
			next := key
			if path != "" {
				next = path + "." + key
			}
			if key == "imagePullSecrets" {
				if list, ok := item.([]any); ok {
					for _, entry := range list {
						if reference, ok := entry.(map[string]any); ok {
							addSecretReference(state.unit, namespace, stringValue(reference["name"]), "", next)
						}
					}
				}
			}
			walkReferences(item, next, namespace, state)
		}
	case []any:
		for index, item := range typed {
			walkReferences(item, fmt.Sprintf("%s[%d]", path, index), namespace, state)
		}
	}
}

func (state *unitState) addConfigMapReference(namespace, name, key, use string) {
	if name == "" {
		return
	}
	reference := configMapReference{namespace: namespace, name: name, key: key, use: use}
	state.configMapReferences[namespace+"\x00"+name+"\x00"+key] = reference
}

func addSecretReference(unit *MigrationUnit, namespace, name, key, use string) {
	if name == "" {
		return
	}
	candidate := SecretReference{Namespace: namespace, Name: name, Key: key, Use: use}
	for _, existing := range unit.SecretReferences {
		if existing == candidate {
			return
		}
	}
	unit.SecretReferences = append(unit.SecretReferences, candidate)
}

func stringValue(value any) string {
	stringValue, _ := value.(string)
	return stringValue
}

func (p *planner) addRuntimeConfigName(state *unitState, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = constants.DefaultRuntimeConfigName
	}
	state.runtimeConfigNames[name] = struct{}{}
}

func (p *planner) addRuntimeConfigReference(state *unitState, reference *aimv1alpha1.AIMResolvedReference) {
	if reference == nil {
		return
	}
	copied := *reference
	state.runtimeConfigRefs = append(state.runtimeConfigRefs, &copied)
	p.addRuntimeConfigName(state, copied.Name)
}

func appendIdentity(unit *MigrationUnit, identity ResourceIdentity) {
	for index := range unit.ObservedResources {
		if identityKey(unit.ObservedResources[index]) == identityKey(identity) {
			unit.ObservedResources[index] = identity
			return
		}
	}
	unit.ObservedResources = append(unit.ObservedResources, identity)
}

func addManual(unit *MigrationUnit, code, message string) {
	finding := Finding{Code: code, Message: message}
	for _, existing := range unit.ManualOutcomes {
		if existing == finding {
			return
		}
	}
	unit.ManualOutcomes = append(unit.ManualOutcomes, finding)
}

func addWarning(unit *MigrationUnit, code, message string) {
	finding := Finding{Code: code, Message: message}
	for _, existing := range unit.Warnings {
		if existing == finding {
			return
		}
	}
	unit.Warnings = append(unit.Warnings, finding)
}

func hasManualCode(unit *MigrationUnit, code string) bool {
	for _, finding := range unit.ManualOutcomes {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func externallyManaged(object client.Object) bool {
	if object == nil {
		return false
	}
	if manager := strings.ToLower(object.GetLabels()[constants.LabelK8sManagedBy]); manager != "" &&
		!isAIMManagedByLabel(manager) {
		return true
	}
	for key := range object.GetAnnotations() {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "argocd.argoproj.io/") ||
			strings.HasPrefix(lower, "helm.sh/") ||
			strings.HasPrefix(lower, "meta.helm.sh/") ||
			strings.HasPrefix(lower, "kustomize.toolkit.fluxcd.io/") ||
			strings.HasPrefix(lower, "helm.toolkit.fluxcd.io/") {
			return true
		}
	}
	for _, field := range object.GetManagedFields() {
		manager := strings.ToLower(field.Manager)
		if strings.Contains(manager, "argocd") ||
			strings.Contains(manager, "flux") ||
			strings.Contains(manager, "helm") ||
			strings.Contains(manager, "kustomize") {
			return true
		}
	}
	return false
}

func isAIMManagedByLabel(manager string) bool {
	return manager == constants.LabelValueManagedBy ||
		manager == constants.LabelValueManagedByController ||
		(strings.HasPrefix(manager, "aim-") && strings.HasSuffix(manager, "-controller"))
}

func legacyAcceleratorVendor(resourceName string) (aimv1alpha1.AcceleratorVendor, string, bool) {
	switch strings.TrimSpace(resourceName) {
	case "", constants.AMDGPUResourceName:
		return aimv1alpha1.AcceleratorVendorAMD, "legacy AMD GPU resource default", true
	case constants.NVIDIAGPUResourceName:
		return aimv1alpha1.AcceleratorVendorNVIDIA, "source template GPU resource name", true
	default:
		return "", "", false
	}
}

func migrationProfileAnnotations(
	source client.Object,
	sourceKind string,
	contract profileyaml.Contract,
) map[string]string {
	return profileyaml.Mark(map[string]string{
		annotationMigrationSourceKind:      sourceKind,
		annotationMigrationSourceNamespace: source.GetNamespace(),
		annotationMigrationSourceName:      source.GetName(),
		annotationMigrationSourceUID:       string(source.GetUID()),
	}, contract)
}

func copyProfileCaching(source *aimv1alpha1.AIMTemplateCachingConfig) *aimv1alpha2.AIMProfileCachingConfig {
	if source == nil {
		return nil
	}
	return &aimv1alpha2.AIMProfileCachingConfig{
		Enabled: source.Enabled,
		Env:     copyEnv(source.Env),
	}
}

func copyStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func copyEnv(source []corev1.EnvVar) []corev1.EnvVar {
	if source == nil {
		return nil
	}
	copied := make([]corev1.EnvVar, len(source))
	for index := range source {
		source[index].DeepCopyInto(&copied[index])
	}
	return copied
}

func copyModelSources(source []aimv1alpha1.AIMModelSource) []aimv1alpha1.AIMModelSource {
	if source == nil {
		return nil
	}
	copied := make([]aimv1alpha1.AIMModelSource, len(source))
	for index := range source {
		source[index].DeepCopyInto(&copied[index])
	}
	return copied
}

func semanticJSONEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftBytes, &leftValue) != nil ||
		json.Unmarshal(rightBytes, &rightValue) != nil {
		return false
	}
	return equality.Semantic.DeepEqual(leftValue, rightValue)
}

func semanticYAMLEqual(left, right []byte) bool {
	leftJSON, err := yaml.YAMLToJSON(left)
	if err != nil {
		return false
	}
	rightJSON, err := yaml.YAMLToJSON(right)
	if err != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftJSON, &leftValue) != nil || json.Unmarshal(rightJSON, &rightValue) != nil {
		return false
	}
	return equality.Semantic.DeepEqual(leftValue, rightValue)
}

func profileContractMatches(annotations map[string]string, expected profileyaml.Contract) bool {
	actual, found, err := profileyaml.FromAnnotations(annotations)
	return err == nil && found && actual.Encode() == expected.Encode()
}

func validProfileProvenance(
	profile client.Object,
	status *aimv1alpha2.AIMProfileStatus,
	state *unitState,
) bool {
	annotations := profile.GetAnnotations()
	if annotations[annotationMigrationSourceKind] == state.templateKind &&
		annotations[annotationMigrationSourceNamespace] == state.template.GetNamespace() &&
		annotations[annotationMigrationSourceName] == state.template.GetName() &&
		annotations[annotationMigrationSourceUID] == string(state.template.GetUID()) {
		return true
	}
	if state.model == nil || status == nil || status.SourceModel == nil {
		return false
	}
	source := status.SourceModel
	if source.Name != state.model.GetName() ||
		string(source.Kind) != state.modelKind ||
		source.Namespace != state.model.GetNamespace() {
		return false
	}
	for _, owner := range profile.GetOwnerReferences() {
		if (owner.APIVersion == aimv1alpha1.GroupVersion.String() ||
			owner.APIVersion == aimv1alpha2.GroupVersion.String()) &&
			owner.Kind == state.modelKind &&
			owner.Name == state.model.GetName() &&
			owner.UID == state.model.GetUID() {
			return true
		}
	}
	return false
}

func profileRendersExactly(
	spec *aimv1alpha2.AIMProfileSpecCommon,
	annotations map[string]string,
	origin aimv1alpha1.ProfileOrigin,
	sourceYAML []byte,
) bool {
	contract, err := profileyaml.ForProfile(annotations, origin, spec)
	if err != nil {
		return false
	}
	rendered, _, err := serving.AssembleProfileYAMLForContract(spec, contract)
	return err == nil && semanticYAMLEqual(sourceYAML, rendered)
}

func targetCachePreservesArtifacts(
	cache *aimv1alpha2.AIMProfileCache,
	artifacts []*aimv1alpha1.AIMArtifact,
) bool {
	if cache == nil ||
		cache.DeletionTimestamp != nil ||
		cache.Status.Status != constants.AIMStatusReady ||
		cache.Generation <= 0 ||
		cache.Status.ObservedGeneration != cache.Generation ||
		len(cache.Status.Artifacts) != len(artifacts) {
		return false
	}
	expected := make(map[types.UID]string, len(artifacts))
	for _, artifact := range artifacts {
		expected[artifact.UID] = artifact.Status.PersistentVolumeClaim
	}
	for _, resolved := range cache.Status.Artifacts {
		if expected[types.UID(resolved.UID)] != resolved.PersistentVolumeClaim ||
			resolved.Status != constants.AIMStatusReady {
			return false
		}
	}
	return true
}
