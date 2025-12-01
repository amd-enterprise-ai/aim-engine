/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimservicetemplate

//
//import (
//	"context"
//	"crypto/sha256"
//	"encoding/json"
//	"errors"
//	"fmt"
//	"strings"
//
//	aimtemplate "github.com/amd-enterprise-ai/aim-engine/internal/pkg/aimservicetemplate"
//	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
//	types2 "k8s.io/apimachinery/pkg/types"
//
//	corev1 "k8s.io/api/core/v1"
//	apiequality "k8s.io/apimachinery/pkg/api/equality"
//	apierrors "k8s.io/apimachinery/pkg/api/errors"
//	"sigs.k8s.io/controller-runtime/pkg/client"
//	"sigs.k8s.io/controller-runtime/pkg/log"
//
//	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
//)
//
//const templateNameMaxLength = 63
//
//// TemplateResolution captures the result of resolving a template name for a service.
//type TemplateResolution struct {
//	BaseName  string
//	FinalName string
//	Derived   bool
//	Scope     types.TemplateScope
//}
//
//// TemplateSelectionStatus captures metadata about automatic template template.
//type TemplateSelectionStatus struct {
//	AutoSelected              bool
//	CandidateCount            int
//	SelectionReason           string
//	SelectionMessage          string
//	TemplatesExistButNotReady bool
//	ImageReady                bool
//	ImageReadyReason          string
//	ImageReadyMessage         string
//	ModelResolutionErr        error
//}
//
//// ServiceObservation holds observed state for an AIMService reconciliation.
//type ServiceObservation struct {
//	templateName                  string
//	BaseTemplateName              string
//	Scope                         types.TemplateScope
//	AutoSelectedTemplate          bool
//	TemplateAvailable             bool
//	TemplateOwnedByService        bool
//	ShouldCreateTemplate          bool
//	RuntimeConfigSpec             aimv1alpha1.AIMRuntimeConfigSpec
//	ResolvedRuntimeConfig         *aimv1alpha1.AIMResolvedReference
//	ResolvedImage                 *aimv1alpha1.AIMResolvedReference
//	RoutePath                     string
//	routeTimeout                  *string
//	pathTemplateErr               error
//	RuntimeConfigErr              error
//	ImageErr                      error
//	ModelResolutionErr            error
//	TemplateStatus                *aimv1alpha1.AIMServiceTemplateStatus
//	TemplateSpecCommon            aimv1alpha1.AIMServiceTemplateSpecCommon
//	templateSpec                  *aimv1alpha1.AIMServiceTemplateSpec
//	TemplateNamespace             string
//	ImageResources                *corev1.ResourceRequirements
//	TemplateSelectionReason       string
//	TemplateSelectionMessage      string
//	TemplateSelectionCount        int
//	TemplatesExistButNotReady     bool // True when templates exist but aren't Available yet
//	ImageReady                    bool
//	ImageReadyReason              string
//	ImageReadyMessage             string
//	InferenceServicePodImageError *aimtemplate.ImagePullError // Categorized image pull error from inferenceService pods
//	TemplateCache                 *aimv1alpha1.AIMTemplateCache
//	ModelCaches                   *aimv1alpha1.AIMModelCacheList
//}
//
//// TemplateFound returns true if a template was resolved (namespace or cluster scope).
//func (o *ServiceObservation) TemplateFound() bool {
//	return o != nil && o.Scope != types.TemplateScopeNone
//}
//
//// RuntimeName returns the effective runtime name for the service.
//func (o *ServiceObservation) RuntimeName() string {
//	if o == nil {
//		return ""
//	}
//	return o.templateName
//}
//
//// ResolveTemplateNameForService determines the template name to use for a service.
//// It handles default template lookup, base template resolution, and derived template naming.
//// Returns an empty BaseName/FinalName if no template can be resolved, which indicates
//// the service should enter a degraded state.
//func ResolveTemplateNameForService(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//) (TemplateResolution, TemplateSelectionStatus, error) {
//	var res TemplateResolution
//	status := TemplateSelectionStatus{ImageReady: true}
//
//	baseName := strings.TrimSpace(service.Spec.TemplateRef)
//	if baseName != "" {
//		res.BaseName = baseName
//		res.Derived = service.Spec.Overrides != nil
//		if res.Derived {
//			suffix := OverridesSuffix(service.Spec.Overrides)
//			if suffix != "" {
//				res.FinalName = aimtemplate.DerivedTemplateName(baseName, suffix)
//			} else {
//				res.FinalName = baseName
//			}
//		} else {
//			res.FinalName = baseName
//		}
//		return res, status, nil
//	}
//
//	status.AutoSelected = true
//
//	// Resolve model name from service.Spec.Model (ref or image)
//	imageName, err := resolveModelNameFromService(ctx, k8sClient, service)
//	if err != nil {
//		status.ModelResolutionErr = err
//		return res, status, nil
//	}
//
//	if imageName == "" {
//		status.ImageReady = false
//		status.ImageReadyReason = aimv1alpha1.AIMServiceReasonModelNotFound
//		status.ImageReadyMessage = "No model specified in service spec"
//		return res, status, nil
//	}
//
//	ready, _, reason, message, err := evaluateImageReadiness(ctx, k8sClient, service.namespace, imageName)
//	if err != nil {
//		return res, status, err
//	}
//
//	status.ImageReady = ready
//	status.ImageReadyReason = reason
//	status.ImageReadyMessage = message
//
//	if !ready {
//		return res, status, nil
//	}
//
//	candidates, err := listTemplateCandidatesForImage(ctx, k8sClient, service.namespace, imageName)
//	if err != nil {
//		return res, status, err
//	}
//
//	availableGPUs, err := utils.ListAvailableGPUs(ctx, k8sClient)
//	if err != nil {
//		return res, status, fmt.Errorf("failed to list available GPUs: %w", err)
//	}
//
//	// When auto-selecting, don't filter by overrides - we're selecting a base template
//	// to potentially derive from. The derived template will have the overrides applied.
//	selected, count := aimtemplate.SelectBestTemplate(candidates, nil, availableGPUs)
//	status.CandidateCount = count
//
//	if count != 1 {
//		if count == 0 {
//			// Check if any templates exist at all (regardless of availability)
//			if len(candidates) == 0 {
//				// No templates exist at all - this is a failure
//				status.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateNotFound
//				status.SelectionMessage = fmt.Sprintf("No templates found for image %q", imageName)
//				return res, status, nil
//			}
//
//			// Templates exist but selection returned 0 - check why
//			// Count how many are Available vs other statuses
//			availableCount := 0
//			for _, c := range candidates {
//				if c.Status.Status == aimv1alpha1.AIMTemplateStatusReady {
//					availableCount++
//				}
//			}
//
//			if availableCount == 0 {
//				// Templates exist but none are Available yet - service should wait
//				status.TemplatesExistButNotReady = true
//				status.SelectionReason = ""
//				status.SelectionMessage = ""
//			} else {
//				// Templates are Available but don't match overrides/GPU requirements
//				status.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateNotFound
//				status.SelectionMessage = fmt.Sprintf("No available templates match the service requirements for image %q", imageName)
//			}
//		} else {
//			status.SelectionReason = aimv1alpha1.AIMServiceReasonTemplateSelectionAmbiguous
//			status.SelectionMessage = fmt.Sprintf("Multiple templates (%d) satisfy image %q", count, imageName)
//		}
//		return res, status, nil
//	}
//
//	res.BaseName = selected.Name
//	res.Scope = selected.Scope
//	res.Derived = service.Spec.Overrides != nil
//	if res.Derived {
//		suffix := OverridesSuffix(service.Spec.Overrides)
//		if suffix != "" {
//			res.FinalName = aimtemplate.DerivedTemplateName(selected.Name, suffix)
//		} else {
//			res.FinalName = selected.Name
//		}
//	} else {
//		res.FinalName = selected.Name
//	}
//
//	return res, status, nil
//}
//
//// OverridesSuffix computes a hash suffix for service overrides.
//func OverridesSuffix(overrides *aimv1alpha1.AIMServiceOverrides) string {
//	if overrides == nil {
//		return ""
//	}
//
//	bytes, err := json.Marshal(overrides)
//	if err != nil {
//		return ""
//	}
//
//	sum := sha256.Sum256(bytes)
//	return fmt.Sprintf("%x", sum[:])[:8]
//}
//
//// resolveModelNameFromService resolves the model name from service.Spec.Model
//// If Model.Ref is specified, returns it directly.
//// If Model.image is specified, searches for or creates a model with that image.
//// If Model.Custom is specified, creates a model with custom configuration.
//func resolveModelNameFromService(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//) (string, error) {
//	// Check Model.Ref
//	if service.Spec.Model.Ref != nil && *service.Spec.Model.Ref != "" {
//		return strings.TrimSpace(*service.Spec.Model.Ref), nil
//	}
//
//	// Check Model.image
//	if service.Spec.Model.image != nil && *service.Spec.Model.image != "" {
//		imageURI := strings.TrimSpace(*service.Spec.Model.image)
//
//		// Resolve runtime config to get model creation settings
//		runtimeConfigResolution, err := aimtemplate.ResolveRuntimeConfig(ctx, k8sClient, service.namespace, service.Spec.RuntimeConfigName)
//		if err != nil {
//			// If runtime config resolution fails, use defaults
//			// This allows services to work without a runtime config present
//			runtimeConfigResolution = nil
//		}
//
//		var runtimeConfig *aimv1alpha1.AIMRuntimeConfigSpec
//		if runtimeConfigResolution != nil {
//			runtimeConfig = &runtimeConfigResolution.EffectiveSpec
//		}
//
//		// Use service's imagePullSecrets and serviceAccountName for auto-created model
//		imagePullSecrets := service.Spec.ImagePullSecrets
//		serviceAccountName := service.Spec.ServiceAccountName
//
//		// Resolve or create model from image
//		modelName, _, err := aimmodel.ResolveOrCreateModelFromImage(ctx, k8sClient, service.namespace, imageURI, runtimeConfig, imagePullSecrets, serviceAccountName)
//		if err != nil {
//			return "", fmt.Errorf("failed to resolve/create model from image %q: %w", imageURI, err)
//		}
//
//		return modelName, nil
//	}
//
//	// Check Model.Custom
//	if service.Spec.Model.Custom != nil {
//		// Resolve runtime config to get model creation settings
//		runtimeConfigResolution, err := aimtemplate.ResolveRuntimeConfig(ctx, k8sClient, service.namespace, service.Spec.RuntimeConfigName)
//		if err != nil {
//			// If runtime config resolution fails, use defaults
//			runtimeConfigResolution = nil
//		}
//
//		var runtimeConfig *aimv1alpha1.AIMRuntimeConfigSpec
//		if runtimeConfigResolution != nil {
//			runtimeConfig = &runtimeConfigResolution.EffectiveSpec
//		}
//
//		// Use service's imagePullSecrets and serviceAccountName for auto-created model
//		imagePullSecrets := service.Spec.ImagePullSecrets
//		serviceAccountName := service.Spec.ServiceAccountName
//
//		// Create custom model with base image and model sources
//		modelName, _, err := aimmodel.CreateCustomModel(ctx, k8sClient, service.namespace, service.Name, service.Spec.Model.Custom, runtimeConfig, imagePullSecrets, serviceAccountName)
//		if err != nil {
//			return "", fmt.Errorf("failed to create custom model: %w", err)
//		}
//
//		// Create base template for custom model if it doesn't exist
//		// Template name = model name for custom models
//		templateName := modelName
//		var existingTemplate aimv1alpha1.AIMServiceTemplate
//		err = k8sClient.Get(ctx, client.ObjectKey{
//			namespace: service.namespace,
//			Name:      templateName,
//		}, &existingTemplate)
//
//		if apierrors.IsNotFound(err) {
//			// Template doesn't exist - create it
//			template := aimtemplate.BuildCustomModelTemplate(
//				service.namespace,
//				modelName,
//				service.Spec.Model.Custom,
//				service.Spec.env,
//			)
//			if err := k8sClient.Create(ctx, template); err != nil {
//				if !apierrors.IsAlreadyExists(err) {
//					return "", fmt.Errorf("failed to create custom model template: %w", err)
//				}
//				// Race condition - another controller created it, continue
//			}
//		} else if err != nil {
//			return "", fmt.Errorf("failed to check for existing template: %w", err)
//		}
//		// Template exists or was just created - continue
//
//		return modelName, nil
//	}
//
//	// Neither ref, image, nor custom specified (should be caught by CEL validation)
//	return "", nil
//}
//
//// checkModelStatus evaluates a model's status and returns readiness information
//func checkModelStatus(status aimv1alpha1.AIMModelStatusEnum, scope types.TemplateScope, kind, imageName string) (bool, types.TemplateScope, string, string) {
//	switch status {
//	case aimv1alpha1.AIMModelStatusReady:
//		return true, scope, "", ""
//	case aimv1alpha1.AIMModelStatusPending:
//		return false, scope, "ModelPending",
//			fmt.Sprintf("%s %q is pending discovery", kind, imageName)
//	case aimv1alpha1.AIMModelStatusProgressing:
//		return false, scope, "ModelProgressing",
//			fmt.Sprintf("%s %q is running discovery", kind, imageName)
//	case aimv1alpha1.AIMModelStatusFailed:
//		return false, scope, "ModelFailed",
//			fmt.Sprintf("%s %q failed discovery", kind, imageName)
//	case aimv1alpha1.AIMModelStatusDegraded:
//		return false, scope, "ModelDegraded",
//			fmt.Sprintf("%s %q is degraded", kind, imageName)
//	case "":
//		// Model status not yet initialized - treat as pending
//		return false, scope, "ModelPending",
//			fmt.Sprintf("%s %q status not yet initialized", kind, imageName)
//	default:
//		return false, scope, "ModelNotReady",
//			fmt.Sprintf("%s %q is %s", kind, imageName, status)
//	}
//}
//
//func evaluateImageReadiness(
//	ctx context.Context,
//	k8sClient client.Client,
//	namespace string,
//	imageName string,
//) (bool, types.TemplateScope, string, string, error) {
//	logger := log.FromContext(ctx)
//
//	if imageName == "" {
//		return false, types.TemplateScopeNone, aimv1alpha1.AIMServiceReasonModelNotFound, "Model name is empty", nil
//	}
//
//	if namespace != "" {
//		var nsModel aimv1alpha1.AIMModel
//		err := k8sClient.Get(ctx, client.ObjectKey{Name: imageName, namespace: namespace}, &nsModel)
//		switch {
//		case err == nil:
//			ready, scope, reason, message := checkModelStatus(nsModel.Status.Status, types.TemplateScopeNamespace, "AIMModel", imageName)
//			return ready, scope, reason, message, nil
//		case apierrors.IsNotFound(err):
//			logger.V(1).Info("AIMModel not found, checking cluster scope", "model", imageName, "namespace", namespace)
//			// fall through to cluster scope
//		default:
//			return false, types.TemplateScopeNone, "", "", fmt.Errorf("failed to get AIMModel %s/%s: %w", namespace, imageName, err)
//		}
//	}
//
//	var clusterModel aimv1alpha1.AIMClusterModel
//	err := k8sClient.Get(ctx, client.ObjectKey{Name: imageName}, &clusterModel)
//	switch {
//	case err == nil:
//		ready, scope, reason, message := checkModelStatus(clusterModel.Status.Status, types.TemplateScopeCluster, "AIMClusterModel", imageName)
//		return ready, scope, reason, message, nil
//	case apierrors.IsNotFound(err):
//		logger.V(1).Info("Model not found", "model", imageName)
//		return false, types.TemplateScopeNone, aimv1alpha1.AIMServiceReasonModelNotFound,
//			fmt.Sprintf("No AIMModel or AIMClusterModel found for %q", imageName), nil
//	default:
//		return false, types.TemplateScopeNone, "", "", fmt.Errorf("failed to get AIMClusterModel %s: %w", imageName, err)
//	}
//}
//
//func listTemplateCandidatesForImage(
//	ctx context.Context,
//	k8sClient client.Client,
//	namespace string,
//	imageName string,
//) ([]aimtemplate.TemplateCandidate, error) {
//	candidates := make([]aimtemplate.TemplateCandidate, 0)
//
//	if namespace != "" {
//		var templateList aimv1alpha1.AIMServiceTemplateList
//		if err := k8sClient.List(ctx, &templateList, client.InNamespace(namespace)); err != nil {
//			return nil, fmt.Errorf("failed to list AIMServiceTemplates in namespace %q: %w", namespace, err)
//		}
//		for i := range templateList.Items {
//			tpl := &templateList.Items[i]
//			if tpl.Spec.modelName != imageName {
//				continue
//			}
//			if aimtemplate.IsDerivedTemplate(tpl.GetLabels()) {
//				continue
//			}
//			candidates = append(candidates, aimtemplate.TemplateCandidate{
//				Name:      tpl.Name,
//				namespace: tpl.namespace,
//				Scope:     types.TemplateScopeNamespace,
//				Spec:      tpl.Spec.AIMServiceTemplateSpecCommon,
//				Status:    tpl.Status,
//			})
//		}
//	}
//
//	var clusterTemplateList aimv1alpha1.AIMClusterServiceTemplateList
//	if err := k8sClient.List(ctx, &clusterTemplateList); err != nil {
//		return nil, fmt.Errorf("failed to list AIMClusterServiceTemplates: %w", err)
//	}
//	for i := range clusterTemplateList.Items {
//		tpl := &clusterTemplateList.Items[i]
//		if tpl.Spec.modelName != imageName {
//			continue
//		}
//		if aimtemplate.IsDerivedTemplate(tpl.GetLabels()) {
//			continue
//		}
//		candidates = append(candidates, aimtemplate.TemplateCandidate{
//			Name:   tpl.Name,
//			Scope:  types.TemplateScopeCluster,
//			Spec:   tpl.Spec.AIMServiceTemplateSpecCommon,
//			Status: tpl.Status,
//		})
//	}
//
//	return candidates, nil
//}
//
//// LoadBaseTemplateSpec fetches the base template spec for a derived template.
//// Searches namespace-scoped templates first, then falls back to cluster-scoped templates.
//func LoadBaseTemplateSpec(ctx context.Context, k8sClient client.Client, service *aimv1alpha1.AIMService, baseName string) (*aimv1alpha1.AIMServiceTemplateSpec, types.TemplateScope, error) {
//	if baseName == "" {
//		return nil, types.TemplateScopeNone, fmt.Errorf("base template name is empty")
//	}
//
//	if service.namespace != "" {
//		var namespaceTemplate aimv1alpha1.AIMServiceTemplate
//		if err := k8sClient.Get(ctx, client.ObjectKey{namespace: service.namespace, Name: baseName}, &namespaceTemplate); err == nil {
//			return namespaceTemplate.Spec.DeepCopy(), types.TemplateScopeNamespace, nil
//		} else if !apierrors.IsNotFound(err) {
//			return nil, types.TemplateScopeNone, err
//		}
//	}
//
//	var clusterTemplate aimv1alpha1.AIMClusterServiceTemplate
//	if err := k8sClient.Get(ctx, client.ObjectKey{Name: baseName}, &clusterTemplate); err == nil {
//		spec := &aimv1alpha1.AIMServiceTemplateSpec{
//			AIMServiceTemplateSpecCommon: clusterTemplate.Spec.AIMServiceTemplateSpecCommon,
//		}
//		return spec, types.TemplateScopeCluster, nil
//	} else if !apierrors.IsNotFound(err) {
//		return nil, types.TemplateScopeNone, err
//	}
//
//	return nil, types.TemplateScopeNone, fmt.Errorf("base template %q not found", baseName)
//}
//
//// PopulateObservationFromNamespaceTemplate extracts data from a namespace-scoped template into the observation.
//func PopulateObservationFromNamespaceTemplate(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	template *aimv1alpha1.AIMServiceTemplate,
//	obs *ServiceObservation,
//) error {
//	obs.Scope = types.TemplateScopeNamespace
//	obs.TemplateAvailable = template.Status.Status == aimv1alpha1.AIMTemplateStatusReady
//	obs.TemplateOwnedByService = utils.HasOwnerReference(template.GetOwnerReferences(), service.UID) ||
//		aimtemplate.IsDerivedTemplate(template.GetLabels())
//	if template.Status.ResolvedRuntimeConfig != nil {
//		obs.ResolvedRuntimeConfig = template.Status.ResolvedRuntimeConfig
//	}
//	if template.Status.ResolvedModel != nil {
//		obs.ResolvedImage = template.Status.ResolvedModel
//	}
//	obs.TemplateStatus = template.Status.DeepCopy()
//	obs.TemplateSpecCommon = template.Spec.AIMServiceTemplateSpecCommon
//	obs.templateSpec = template.Spec.DeepCopy()
//	runtimeConfigName := RuntimeConfigNameForService(service, obs.TemplateSpecCommon)
//	obs.TemplateSpecCommon.RuntimeConfigName = runtimeConfigName
//	if resolution, resolveErr := aimtemplate.ResolveRuntimeConfig(ctx, k8sClient, service.namespace, runtimeConfigName); resolveErr != nil {
//		if errors.Is(resolveErr, aimtemplate.ErrRuntimeConfigNotFound) {
//			obs.RuntimeConfigErr = fmt.Errorf("AIMRuntimeConfig %q not found in namespace %q", runtimeConfigName, service.namespace)
//		} else {
//			return fmt.Errorf("failed to resolve AIMRuntimeConfig %q in namespace %q: %w", runtimeConfigName, service.namespace, resolveErr)
//		}
//	} else {
//		obs.RuntimeConfigSpec = resolution.EffectiveSpec
//		if resolution.ResolvedRef != nil {
//			obs.ResolvedRuntimeConfig = resolution.ResolvedRef
//		}
//	}
//	obs.TemplateNamespace = template.namespace
//	if image, imageErr := aimmodel.LookupImageForNamespaceTemplate(ctx, k8sClient, template.namespace, template.Spec.modelName); imageErr == nil {
//		obs.ImageResources = image.Resources.DeepCopy()
//	} else if errors.Is(imageErr, aimmodel.ErrImageNotFound) {
//		obs.ImageErr = fmt.Errorf("AIMModel %q not found in namespace %q", template.Spec.modelName, template.namespace)
//	} else {
//		return fmt.Errorf("failed to lookup AIMModel %q in namespace %q: %w", template.Spec.modelName, template.namespace, imageErr)
//	}
//	return nil
//}
//
//// PopulateObservationFromClusterTemplate extracts data from a cluster-scoped template into the observation.
//func PopulateObservationFromClusterTemplate(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	template *aimv1alpha1.AIMClusterServiceTemplate,
//	obs *ServiceObservation,
//) error {
//	obs.Scope = types.TemplateScopeCluster
//	obs.TemplateAvailable = template.Status.Status == aimv1alpha1.AIMTemplateStatusReady
//	if template.Status.ResolvedRuntimeConfig != nil {
//		obs.ResolvedRuntimeConfig = template.Status.ResolvedRuntimeConfig
//	}
//	if template.Status.ResolvedModel != nil {
//		obs.ResolvedImage = template.Status.ResolvedModel
//	}
//	obs.TemplateStatus = template.Status.DeepCopy()
//	obs.TemplateSpecCommon = template.Spec.AIMServiceTemplateSpecCommon
//	obs.templateSpec = &aimv1alpha1.AIMServiceTemplateSpec{
//		AIMServiceTemplateSpecCommon: template.Spec.AIMServiceTemplateSpecCommon,
//	}
//	runtimeConfigName := RuntimeConfigNameForService(service, obs.TemplateSpecCommon)
//	obs.TemplateSpecCommon.RuntimeConfigName = runtimeConfigName
//	if resolution, resolveErr := aimtemplate.ResolveRuntimeConfig(ctx, k8sClient, service.namespace, runtimeConfigName); resolveErr == nil {
//		obs.RuntimeConfigSpec = resolution.EffectiveSpec
//		if resolution.ResolvedRef != nil {
//			obs.ResolvedRuntimeConfig = resolution.ResolvedRef
//		}
//	} else if errors.Is(resolveErr, aimtemplate.ErrRuntimeConfigNotFound) {
//		obs.RuntimeConfigErr = fmt.Errorf("AIMRuntimeConfig %q not found in namespace %q", runtimeConfigName, service.namespace)
//	} else {
//		return fmt.Errorf("failed to resolve AIMRuntimeConfig %q in namespace %q: %w", runtimeConfigName, service.namespace, resolveErr)
//	}
//	if image, imageErr := aimmodel.LookupImageForClusterTemplate(ctx, k8sClient, template.Spec.modelName); imageErr == nil {
//		obs.ImageResources = image.Resources.DeepCopy()
//	} else if errors.Is(imageErr, aimmodel.ErrImageNotFound) {
//		obs.ImageErr = fmt.Errorf("AIMClusterModel %q not found", template.Spec.modelName)
//	} else {
//		return fmt.Errorf("failed to lookup AIMClusterModel %q: %w", template.Spec.modelName, imageErr)
//	}
//	return nil
//}
//
//// RuntimeConfigNameForService determines the effective runtime config name for a service.
//func RuntimeConfigNameForService(service *aimv1alpha1.AIMService, templateSpec aimv1alpha1.AIMServiceTemplateSpecCommon) string {
//	name := service.Spec.RuntimeConfigName
//	if name == "" {
//		name = templateSpec.RuntimeConfigName
//	}
//	return aimtemplate.NormalizeRuntimeConfigName(name)
//}
//
//// ObserveDerivedTemplate handles observation for services with derived templates.
//// It fetches the derived template if it exists, or loads the base template spec for creation.
//func ObserveDerivedTemplate(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	resolution TemplateResolution,
//	obs *ServiceObservation,
//) error {
//	var namespaceTemplate aimv1alpha1.AIMServiceTemplate
//	err := k8sClient.Get(ctx, types2.NamespacedName{
//		namespace: service.namespace,
//		Name:      resolution.FinalName,
//	}, &namespaceTemplate)
//
//	switch {
//	case err == nil:
//		// Derived template exists, populate observation from it
//		return PopulateObservationFromNamespaceTemplate(ctx, k8sClient, service, &namespaceTemplate, obs)
//
//	case apierrors.IsNotFound(err):
//		baseSpec, baseScope, err := LoadBaseTemplateSpec(ctx, k8sClient, service, resolution.BaseName)
//		if err != nil {
//			return err
//		}
//
//		// Resolve model name from service for derived template matching
//		resolvedModelName, err := resolveModelNameFromService(ctx, k8sClient, service)
//		if err != nil {
//			return fmt.Errorf("failed to resolve model name: %w", err)
//		}
//
//		match, matchErr := findMatchingTemplateForDerivedSpec(ctx, k8sClient, service, resolvedModelName, baseSpec)
//		if matchErr != nil {
//			return matchErr
//		}
//
//		if match != nil {
//			if match.NamespaceTemplate != nil {
//				obs.templateName = match.NamespaceTemplate.Name
//				return PopulateObservationFromNamespaceTemplate(ctx, k8sClient, service, match.NamespaceTemplate, obs)
//			}
//
//			if match.ClusterTemplate != nil {
//				obs.templateName = match.ClusterTemplate.Name
//				return PopulateObservationFromClusterTemplate(ctx, k8sClient, service, match.ClusterTemplate, obs)
//			}
//		}
//
//		// Derived template doesn't exist yet, prepare observation for creation
//		return prepareObservationForDerivedCreation(ctx, k8sClient, service, baseSpec, baseScope, obs)
//
//	default:
//		return fmt.Errorf("failed to get AIMServiceTemplate %s/%s: %w", service.namespace, resolution.FinalName, err)
//	}
//}
//
//type templateMatch struct {
//	NamespaceTemplate *aimv1alpha1.AIMServiceTemplate
//	ClusterTemplate   *aimv1alpha1.AIMClusterServiceTemplate
//}
//
//// prepareObservationForDerivedCreation populates observation data required to create a derived template.
//func prepareObservationForDerivedCreation(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	baseSpec *aimv1alpha1.AIMServiceTemplateSpec,
//	baseScope types.TemplateScope,
//	obs *ServiceObservation,
//) error {
//	if baseSpec == nil {
//		obs.ShouldCreateTemplate = true
//		return nil
//	}
//
//	obs.templateSpec = baseSpec
//	obs.TemplateSpecCommon = baseSpec.AIMServiceTemplateSpecCommon
//
//	// Resolve runtime config
//	runtimeConfigName := RuntimeConfigNameForService(service, obs.TemplateSpecCommon)
//	obs.TemplateSpecCommon.RuntimeConfigName = runtimeConfigName
//
//	if err := resolveRuntimeConfigForObservation(ctx, k8sClient, service.namespace, runtimeConfigName, obs); err != nil {
//		return err
//	}
//
//	// Lookup image resources based on base scope
//	if err := lookupImageResourcesForScope(ctx, k8sClient, service.namespace, baseSpec.modelName, baseScope, obs); err != nil {
//		return err
//	}
//
//	obs.ShouldCreateTemplate = true
//	return nil
//}
//
//// findMatchingTemplateForDerivedSpec searches for an existing template whose spec matches the derived spec.
//func findMatchingTemplateForDerivedSpec(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	resolvedModelName string,
//	baseSpec *aimv1alpha1.AIMServiceTemplateSpec,
//) (*templateMatch, error) {
//	if service == nil || service.Spec.Overrides == nil {
//		return nil, nil
//	}
//
//	expectedTemplate := aimtemplate.BuildDerivedTemplate(service, "placeholder", resolvedModelName, baseSpec)
//	expectedSpec := expectedTemplate.Spec
//
//	if service.namespace != "" {
//		var templateList aimv1alpha1.AIMServiceTemplateList
//		if err := k8sClient.List(ctx, &templateList, client.InNamespace(service.namespace)); err != nil {
//			return nil, fmt.Errorf("failed to list AIMServiceTemplates in namespace %q: %w", service.namespace, err)
//		}
//		for i := range templateList.Items {
//			template := &templateList.Items[i]
//			if template.Spec.modelName != expectedSpec.modelName {
//				continue
//			}
//			if !apiequality.Semantic.DeepEqual(template.Spec, expectedSpec) {
//				continue
//			}
//			return &templateMatch{NamespaceTemplate: template.DeepCopy()}, nil
//		}
//	}
//
//	if len(expectedSpec.env) > 0 || len(expectedSpec.ImagePullSecrets) > 0 || expectedSpec.Caching != nil {
//		// Derived spec relies on namespace-scoped fields; cluster templates cannot satisfy it.
//		return nil, nil
//	}
//
//	var clusterTemplateList aimv1alpha1.AIMClusterServiceTemplateList
//	if err := k8sClient.List(ctx, &clusterTemplateList); err != nil {
//		return nil, fmt.Errorf("failed to list AIMClusterServiceTemplates: %w", err)
//	}
//	for i := range clusterTemplateList.Items {
//		template := &clusterTemplateList.Items[i]
//		if template.Spec.modelName != expectedSpec.modelName {
//			continue
//		}
//		if !apiequality.Semantic.DeepEqual(template.Spec.AIMServiceTemplateSpecCommon, expectedSpec.AIMServiceTemplateSpecCommon) {
//			continue
//		}
//		return &templateMatch{ClusterTemplate: template.DeepCopy()}, nil
//	}
//
//	return nil, nil
//}
//
//// ObserveNonDerivedTemplate handles observation for services with non-derived templates.
//// It searches for namespace-scoped templates first, then falls back to cluster-scoped templates.
//// Does not set ShouldCreateTemplate - that decision is made in the controller based on whether
//// an explicit templateRef was provided.
//func ObserveNonDerivedTemplate(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	templateName string,
//	preferredScope types.TemplateScope,
//	obs *ServiceObservation,
//) error {
//	switch preferredScope {
//	case types.TemplateScopeNamespace:
//		var namespaceTemplate aimv1alpha1.AIMServiceTemplate
//		err := k8sClient.Get(ctx, types2.NamespacedName{
//			namespace: service.namespace,
//			Name:      templateName,
//		}, &namespaceTemplate)
//		switch {
//		case err == nil:
//			return PopulateObservationFromNamespaceTemplate(ctx, k8sClient, service, &namespaceTemplate, obs)
//		case apierrors.IsNotFound(err):
//			return nil
//		default:
//			return fmt.Errorf("failed to get AIMServiceTemplate %s/%s: %w", service.namespace, templateName, err)
//		}
//	case types.TemplateScopeCluster:
//		return observeClusterTemplate(ctx, k8sClient, service, templateName, obs)
//	default:
//		var namespaceTemplate aimv1alpha1.AIMServiceTemplate
//		err := k8sClient.Get(ctx, types2.NamespacedName{
//			namespace: service.namespace,
//			Name:      templateName,
//		}, &namespaceTemplate)
//		switch {
//		case err == nil:
//			return PopulateObservationFromNamespaceTemplate(ctx, k8sClient, service, &namespaceTemplate, obs)
//		case apierrors.IsNotFound(err):
//			return observeClusterTemplate(ctx, k8sClient, service, templateName, obs)
//		default:
//			return fmt.Errorf("failed to get AIMServiceTemplate %s/%s: %w", service.namespace, templateName, err)
//		}
//	}
//}
//
//// observeClusterTemplate attempts to fetch and populate observation from a cluster-scoped template.
//// Does not set ShouldCreateTemplate - that decision is made in the controller.
//func observeClusterTemplate(
//	ctx context.Context,
//	k8sClient client.Client,
//	service *aimv1alpha1.AIMService,
//	templateName string,
//	obs *ServiceObservation,
//) error {
//	var clusterTemplate aimv1alpha1.AIMClusterServiceTemplate
//	err := k8sClient.Get(ctx, client.ObjectKey{Name: templateName}, &clusterTemplate)
//
//	switch {
//	case err == nil:
//		return PopulateObservationFromClusterTemplate(ctx, k8sClient, service, &clusterTemplate, obs)
//
//	case apierrors.IsNotFound(err):
//		// Template not found - let the controller decide whether to create one
//		return nil
//
//	default:
//		return fmt.Errorf("failed to get AIMClusterServiceTemplate %s: %w", templateName, err)
//	}
//}
//
//// resolveRuntimeConfigForObservation resolves the runtime config and updates the observation.
//func resolveRuntimeConfigForObservation(
//	ctx context.Context,
//	k8sClient client.Client,
//	namespace string,
//	runtimeConfigName string,
//	obs *ServiceObservation,
//) error {
//	resolution, err := aimtemplate.ResolveRuntimeConfig(ctx, k8sClient, namespace, runtimeConfigName)
//	if err != nil {
//		if errors.Is(err, aimtemplate.ErrRuntimeConfigNotFound) {
//			obs.RuntimeConfigErr = fmt.Errorf("AIMRuntimeConfig %q not found in namespace %q", runtimeConfigName, namespace)
//			return nil
//		}
//		return fmt.Errorf("failed to resolve AIMRuntimeConfig %q in namespace %q: %w", runtimeConfigName, namespace, err)
//	}
//
//	obs.RuntimeConfigSpec = resolution.EffectiveSpec
//	if resolution.ResolvedRef != nil {
//		obs.ResolvedRuntimeConfig = resolution.ResolvedRef
//	}
//	return nil
//}
//
//// lookupImageResourcesForScope looks up image resources based on template scope.
//func lookupImageResourcesForScope(
//	ctx context.Context,
//	k8sClient client.Client,
//	namespace string,
//	imageName string,
//	scope types.TemplateScope,
//	obs *ServiceObservation,
//) error {
//	var image *types.ImageLookupResult
//	var err error
//
//	switch scope {
//	case types.TemplateScopeNamespace:
//		image, err = aimmodel.LookupImageForNamespaceTemplate(ctx, k8sClient, namespace, imageName)
//		if err != nil && !errors.Is(err, aimmodel.ErrImageNotFound) {
//			return fmt.Errorf("failed to lookup AIMModel %q in namespace %q: %w", imageName, namespace, err)
//		}
//		if errors.Is(err, aimmodel.ErrImageNotFound) {
//			obs.ImageErr = fmt.Errorf("AIMModel %q not found in namespace %q", imageName, namespace)
//		}
//
//	case types.TemplateScopeCluster:
//		image, err = aimmodel.LookupImageForClusterTemplate(ctx, k8sClient, imageName)
//		if err != nil && !errors.Is(err, aimmodel.ErrImageNotFound) {
//			return fmt.Errorf("failed to lookup AIMClusterModel %q: %w", imageName, err)
//		}
//		if errors.Is(err, aimmodel.ErrImageNotFound) {
//			obs.ImageErr = fmt.Errorf("AIMClusterModel %q not found", imageName)
//		}
//	}
//
//	if image != nil {
//		obs.ImageResources = image.Resources.DeepCopy()
//	}
//	return nil
//}
