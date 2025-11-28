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

package controller

//
//// TemplateState captures the resolved data required to materialize runtimes and services from a template.
//type TemplateState struct {
//	Name              string
//	Namespace         string
//	SpecCommon        aimv1alpha1.AIMServiceTemplateSpecCommon
//	Image             string
//	ImageResources    *corev1.ResourceRequirements
//	ImagePullSecrets  []corev1.LocalObjectReference
//	Env               []corev1.EnvVar
//	RuntimeConfigSpec aimv1alpha1.AIMRuntimeConfigSpec
//	Status            *aimv1alpha1.AIMServiceTemplateStatus
//	ModelSource       *aimv1alpha1.AIMModelSource
//}
//
//// NewTemplateState constructs a TemplateState from the provided base values.
//// Callers populate the struct with template-derived data before invoking this helper.
//func NewTemplateState(base TemplateState) TemplateState {
//	if base.SpecCommon.Resources != nil {
//		base.SpecCommon.Resources = base.SpecCommon.Resources.DeepCopy()
//	}
//
//	if base.ImageResources != nil {
//		base.ImageResources = base.ImageResources.DeepCopy()
//	}
//
//	base.ImagePullSecrets = pkgutils.CopyPullSecrets(base.ImagePullSecrets)
//	base.Env = pkgutils.CopyEnvVars(base.Env)
//
//	if base.Status != nil {
//		base.Status = base.Status.DeepCopy()
//		base.ModelSource = ExtractPrimaryModelSource(base.Status.ModelSources)
//	}
//
//	return base
//}
//
//// ServiceAccountName returns the resolved service account for resources derived from this template.
//func (s TemplateState) ServiceAccountName() string {
//	return s.SpecCommon.ServiceAccountName
//}
//
//// ExtractPrimaryModelSource returns the first non-empty model source from the template status.
//func ExtractPrimaryModelSource(sources []aimv1alpha1.AIMModelSource) *aimv1alpha1.AIMModelSource {
//	for _, source := range sources {
//		if source.SourceURI != "" {
//			return &source
//		}
//	}
//	return nil
//}
//
//// StatusMetric returns the metric discovered during template resolution, if available.
//func (s TemplateState) StatusMetric() *aimv1alpha1.AIMMetric {
//	if s.Status == nil {
//		return nil
//	}
//	if metric := s.Status.Profile.Metadata.Metric; metric != "" {
//		m := metric
//		return &m
//	}
//	return nil
//}
//
//// StatusPrecision returns the precision discovered during template resolution, if available.
//func (s TemplateState) StatusPrecision() *aimv1alpha1.AIMPrecision {
//	if s.Status == nil {
//		return nil
//	}
//	if precision := s.Status.Profile.Metadata.Precision; precision != "" {
//		p := precision
//		return &p
//	}
//	return nil
//}
//
//// BuildTemplateStateFromObservation constructs a TemplateState from the template specification, observation, and status.
//// This is an adapter function that combines template metadata with observed resources.
//func BuildTemplateStateFromObservation(
//	name, namespace string,
//	specCommon aimv1alpha1.AIMServiceTemplateSpecCommon,
//	env []corev1.EnvVar,
//	observation *aimservicetemplate.TemplateObservation,
//	runtimeConfigSpec aimv1alpha1.AIMRuntimeConfigSpec,
//	status *aimv1alpha1.AIMServiceTemplateStatus,
//) TemplateState {
//	base := TemplateState{
//		Name:              name,
//		Namespace:         namespace,
//		SpecCommon:        specCommon,
//		Env:               env,
//		RuntimeConfigSpec: runtimeConfigSpec,
//		Status:            status,
//	}
//
//	if observation != nil {
//		base.Image = observation.Image
//		base.ImageResources = observation.ImageResources
//		base.ImagePullSecrets = observation.ImagePullSecrets
//	}
//
//	return NewTemplateState(base)
//}
//
//// ============================================================================
//// OBSERVATION PHASE
//// ============================================================================
//
//// ServiceTemplateObservation holds all observed state for a service template
//// Used by both namespace and cluster-scoped template controllers
//type ServiceTemplateObservation struct {
//	RuntimeConfig aimconfig.AIMRuntimeConfigObservation
//	Model         ServiceTemplateModelObservation
//	Discovery     ServiceTemplateDiscoveryObservation
//	Cache         ServiceTemplateCacheObservation // Only used by namespace-scoped templates
//	Cluster       ServiceTemplateClusterObservation
//}
//
//// ----- Model Sub-Domain -----
//
//type ServiceTemplateModelObservation struct {
//	Model client.Object // Can be AIMModel or AIMClusterModel
//	Image string        // From model.Spec.Image
//	Scope aimv1alpha1.AIMResolutionScope
//	Error error
//}
//
//type serviceTemplateModelObservationInputs struct {
//	model client.Object
//	image string
//	scope aimv1alpha1.AIMResolutionScope
//	error error
//}
//
//func buildModelObservation(inputs serviceTemplateModelObservationInputs) ServiceTemplateModelObservation {
//	obs := ServiceTemplateModelObservation{}
//
//	if inputs.error != nil {
//		if errors.IsNotFound(inputs.error) {
//			obs.Error = inputs.error // domain-level "model missing"
//			return obs
//		}
//	}
//
//	obs.Model = inputs.model
//	obs.Image = inputs.image
//	obs.Scope = inputs.scope
//
//	return obs
//}
//
//// ----- Discovery Job Sub-Domain -----
//
//type ServiceTemplateDiscoveryObservation struct {
//	ShouldRunDiscovery bool
//	DiscoveryJob       *batchv1.Job
//	DiscoveryResult    *aimservicetemplate.ParsedDiscovery
//	Error              error
//}
//
//type serviceTemplateDiscoveryObservationInputs struct {
//	profileExists      bool
//	discoveryJobRefSet bool
//	discoveryJob       *batchv1.Job
//	discoveryResult    *aimservicetemplate.ParsedDiscovery
//	fetchError         error
//	parseError         error
//}
//
//func observeDiscoveryJob(ctx context.Context, c client.Client, clientset kubernetes.Interface, namespace, templateName string, status *aimv1alpha1.AIMServiceTemplateStatus) (ServiceTemplateDiscoveryObservation, error) {
//	var discoveryJob *batchv1.Job
//	var discoveryResult *aimservicetemplate.ParsedDiscovery
//	var fetchError error
//	var parseError error
//
//	// Extract status fields
//	profileExists := status != nil && status.Profile != nil
//	discoveryJobRefSet := status != nil && status.DiscoveryJobRef != nil
//
//	// Fetch discovery job if needed
//	// TODO make more robust (detect previous failure)
//	if !profileExists && discoveryJobRefSet {
//		job, err := aimservicetemplate.GetDiscoveryJob(ctx, c, namespace, templateName)
//		if err != nil {
//			// If the job was run and cleaned up, but the profile is still unset, raise an error (should not happen)
//			return ServiceTemplateDiscoveryObservation{}, fmt.Errorf("failed to fetch discovery job even though job has been set: %w", err)
//		}
//		discoveryJob = job
//
//		// Parse logs if job succeeded
//		if aimservicetemplate.IsJobSucceeded(job) {
//			discovery, err := aimservicetemplate.ParseDiscoveryLogs(ctx, c, clientset, job)
//			if err != nil {
//				parseError = err
//			} else {
//				discoveryResult = discovery
//			}
//		}
//		// TODO check for ImagePullBackOff errors
//	}
//
//	// Build observation from fetched data
//	return buildDiscoveryObservation(serviceTemplateDiscoveryObservationInputs{
//		profileExists:      profileExists,
//		discoveryJobRefSet: discoveryJobRefSet,
//		discoveryJob:       discoveryJob,
//		discoveryResult:    discoveryResult,
//		fetchError:         fetchError,
//		parseError:         parseError,
//	}), nil
//}
//
//func buildDiscoveryObservation(inputs serviceTemplateDiscoveryObservationInputs) ServiceTemplateDiscoveryObservation {
//	obs := ServiceTemplateDiscoveryObservation{}
//
//	// TODO with custom models, skip discovery if sources are defined inline
//
//	// Determine if we should run discovery
//	if !inputs.profileExists && !inputs.discoveryJobRefSet {
//		obs.ShouldRunDiscovery = true
//	}
//
//	if inputs.fetchError != nil {
//		obs.Error = inputs.fetchError
//		return obs
//	}
//
//	if inputs.parseError != nil {
//		obs.Error = inputs.parseError
//		return obs
//	}
//
//	if inputs.discoveryJob != nil {
//		obs.DiscoveryJob = inputs.discoveryJob
//	}
//
//	if inputs.discoveryResult != nil {
//		obs.DiscoveryResult = inputs.discoveryResult
//	}
//
//	return obs
//}
//
//// ----- Cache Sub-Domain -----
//
//type ServiceTemplateCacheObservation struct {
//	ShouldCreateCache      bool
//	ExistingTemplateCaches []aimv1alpha1.AIMTemplateCache
//	BestTemplateCache      *aimv1alpha1.AIMTemplateCache
//	ListError              error
//	GetError               error
//}
//
//type serviceTemplateCacheObservationInputs struct {
//	existingTemplateCaches []aimv1alpha1.AIMTemplateCache
//	cachingEnabled         bool
//	listError              error
//}
//
//func buildTemplateCacheObservation(inputs serviceTemplateCacheObservationInputs) ServiceTemplateCacheObservation {
//	obs := ServiceTemplateCacheObservation{}
//
//	if inputs.listError != nil {
//		obs.ListError = inputs.listError
//		return obs
//	}
//
//	// If we have existing template caches, determine the one that's closest to availability and the newest
//	if len(inputs.existingTemplateCaches) > 0 {
//		obs.ExistingTemplateCaches = inputs.existingTemplateCaches
//
//		// Find the cache with the best status (highest priority)
//		// If multiple caches have the same status, choose the newest one
//		var bestCache *aimv1alpha1.AIMTemplateCache
//		bestPriority := -1
//
//		for i := range inputs.existingTemplateCaches {
//			cache := &inputs.existingTemplateCaches[i]
//			priority := constants2.AIMStatusPriority[cache.Status.Status]
//
//			if bestCache == nil {
//				bestCache = cache
//				bestPriority = priority
//				continue
//			}
//
//			// Choose cache with higher priority status
//			if priority > bestPriority {
//				bestCache = cache
//				bestPriority = priority
//			} else if priority == bestPriority {
//				// If same priority, choose newer cache
//				if cache.CreationTimestamp.After(bestCache.CreationTimestamp.Time) {
//					bestCache = cache
//				}
//			}
//		}
//
//		obs.BestTemplateCache = bestCache
//	} else if inputs.cachingEnabled {
//		// Should create cache if no cache exists but caching is enabled
//		obs.ShouldCreateCache = true
//	}
//
//	return obs
//}

// ----- Cluster/GPU Sub-Domain -----

// ============================================================================
// PLAN PHASE
// ============================================================================

// ----- Discovery Job Builder -----
