# API Reference

## Packages
- [aim.eai.amd.com/v1alpha1](#aimeaiamdcomv1alpha1)


## aim.eai.amd.com/v1alpha1

Package v1alpha1 contains API Schema definitions for the aim v1alpha1 API group.

### Resource Types
- [AIMClusterModel](#aimclustermodel)
- [AIMClusterModelList](#aimclustermodellist)
- [AIMClusterModelSource](#aimclustermodelsource)
- [AIMClusterModelSourceList](#aimclustermodelsourcelist)
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)
- [AIMClusterRuntimeConfigList](#aimclusterruntimeconfiglist)
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)
- [AIMClusterServiceTemplateList](#aimclusterservicetemplatelist)
- [AIMModel](#aimmodel)
- [AIMModelCache](#aimmodelcache)
- [AIMModelCacheList](#aimmodelcachelist)
- [AIMModelList](#aimmodellist)
- [AIMRuntimeConfig](#aimruntimeconfig)
- [AIMRuntimeConfigList](#aimruntimeconfiglist)
- [AIMService](#aimservice)
- [AIMServiceList](#aimservicelist)
- [AIMServiceTemplate](#aimservicetemplate)
- [AIMServiceTemplateList](#aimservicetemplatelist)
- [AIMTemplateCache](#aimtemplatecache)
- [AIMTemplateCacheList](#aimtemplatecachelist)



#### AIMCachingMode

_Underlying type:_ _string_

AIMCachingMode controls caching behavior for a service.

_Validation:_
- Enum: [Auto Always Never]

_Appears in:_
- [AIMServiceCachingConfig](#aimservicecachingconfig)

| Field | Description |
| --- | --- |
| `Auto` | CachingModeAuto uses cache if it exists, but doesn't create one.<br />This is the default mode.<br /> |
| `Always` | CachingModeAlways always uses cache, creating one if it doesn't exist.<br /> |
| `Never` | CachingModeNever never uses cache, even if one exists.<br /> |


#### AIMClusterModel



AIMClusterModel is a cluster-scoped model catalog entry for AIM container images.

Cluster-scoped models can be referenced by AIMServices in any namespace, making them ideal for
shared model deployments across teams and projects. Like namespace-scoped AIMModels, cluster models
trigger discovery jobs to extract metadata and generate service templates.

When both cluster and namespace models exist for the same container image, services will preferentially
use the namespace-scoped AIMModel when referenced by image URI.



_Appears in:_
- [AIMClusterModelList](#aimclustermodellist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModel` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMClusterModelList



AIMClusterModelList contains a list of AIMClusterModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterModel](#aimclustermodel) array_ |  |  |  |


#### AIMClusterModelSource



AIMClusterModelSource automatically discovers and syncs AI model images from container registries.



_Appears in:_
- [AIMClusterModelSourceList](#aimclustermodelsourcelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelSource` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterModelSourceSpec](#aimclustermodelsourcespec)_ |  |  |  |
| `status` _[AIMClusterModelSourceStatus](#aimclustermodelsourcestatus)_ |  |  |  |


#### AIMClusterModelSourceList



AIMClusterModelSourceList contains a list of AIMClusterModelSource.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelSourceList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterModelSource](#aimclustermodelsource) array_ |  |  |  |


#### AIMClusterModelSourceSpec



AIMClusterModelSourceSpec defines the desired state of AIMClusterModelSource.



_Appears in:_
- [AIMClusterModelSource](#aimclustermodelsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `registry` _string_ | Registry to sync from (e.g., docker.io, ghcr.io, gcr.io).<br />Defaults to docker.io if not specified. | docker.io |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets contains references to secrets for authenticating to private registries.<br />Secrets must exist in the operator namespace (typically aim-engine-system).<br />Used for both registry catalog listing and image metadata extraction. |  |  |
| `filters` _[ModelSourceFilter](#modelsourcefilter) array_ | Filters define which images to discover and sync.<br />Each filter specifies an image pattern with optional version constraints and exclusions.<br />Multiple filters are combined with OR logic (any match includes the image). |  | MaxItems: 100 <br />MinItems: 1 <br /> |
| `syncInterval` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#duration-v1-meta)_ | SyncInterval defines how often to sync with the registry.<br />Defaults to 1h. Minimum recommended interval is 15m to avoid rate limiting.<br />Format: duration string (e.g., "30m", "1h", "2h30m"). | 1h |  |
| `versions` _string array_ | Versions specifies global semantic version constraints applied to all filters.<br />Individual filters can override this with their own version constraints.<br />Constraints use semver syntax: >=1.0.0, <2.0.0, ~1.2.0, ^1.0.0, etc.<br />Non-semver tags (e.g., "latest", "dev") are silently skipped.<br />Version ranges work on all registries (including ghcr.io, gcr.io) when combined with<br />exact repository names (no wildcards). The controller uses the Tags List API to fetch<br />all tags for the repository and filters them by the semver constraint.<br />Example: registry=ghcr.io, filters=[\{image: "silogen/aim-llama"\}], versions=[">=1.0.0"]<br />will fetch all tags from ghcr.io/silogen/aim-llama and include only those >=1.0.0. |  |  |
| `maxModels` _integer_ | MaxModels is the maximum number of AIMClusterModel resources to create from this source.<br />Once this limit is reached, no new models will be created, even if more matching images are discovered.<br />Existing models are never deleted.<br />This prevents runaway model creation from overly broad filters. | 100 | Maximum: 10000 <br />Minimum: 1 <br /> |


#### AIMClusterModelSourceStatus



AIMClusterModelSourceStatus defines the observed state of AIMClusterModelSource.



_Appears in:_
- [AIMClusterModelSource](#aimclustermodelsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `status` _string_ | Status represents the overall state of the model source. |  | Enum: [Pending Starting Progressing Ready Running Degraded NotAvailable Failed] <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastSyncTime is the timestamp of the last successful registry sync.<br />Updated after each successful sync operation. |  |  |
| `discoveredModels` _integer_ | DiscoveredModels is the count of AIMClusterModel resources managed by this source.<br />Includes both existing and newly created models. |  |  |
| `availableModels` _integer_ | AvailableModels is the total count of images discovered in the registry that match the filters.<br />This may be higher than DiscoveredModels if maxModels limit was reached. |  |  |
| `modelsLimitReached` _boolean_ | ModelsLimitReached indicates whether the maxModels limit has been reached.<br />When true, no new models will be created even if more matching images are discovered. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest available observations of the source's state.<br />Standard conditions: Ready, Syncing, RegistryReachable. |  |  |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently observed spec. |  |  |


#### AIMClusterRuntimeConfig



AIMClusterRuntimeConfig is a cluster-scoped runtime configuration for AIM services, models, and templates.

Cluster-scoped runtime configs provide platform-wide defaults that apply to all namespaces,
making them ideal for organization-level policies such as storage classes, discovery behavior,
model creation scope, and routing configuration.

When both cluster and namespace runtime configs exist with the same name, the configs are merged, and
the namespace-scoped AIMRuntimeConfig takes precedence for any field that is set in both.



_Appears in:_
- [AIMClusterRuntimeConfigList](#aimclusterruntimeconfiglist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterRuntimeConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)_ |  |  |  |
| `status` _[AIMRuntimeConfigStatus](#aimruntimeconfigstatus)_ |  |  |  |


#### AIMClusterRuntimeConfigList



AIMClusterRuntimeConfigList contains a list of AIMClusterRuntimeConfig.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterRuntimeConfigList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterRuntimeConfig](#aimclusterruntimeconfig) array_ |  |  |  |


#### AIMClusterRuntimeConfigSpec



AIMClusterRuntimeConfigSpec defines cluster-wide defaults for AIM resources.



_Appears in:_
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > RuntimeConfig.Env > Template.Env > Profile.Env |  |  |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  |  |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  |  |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  |  |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  |  |


#### AIMClusterServiceTemplate



AIMClusterServiceTemplate is a cluster-scoped template that defines runtime profiles for AIM services.

Cluster-scoped templates can be used by AIMServices in any namespace, making them ideal for
platform-wide model configurations that should be shared across teams and projects.
Unlike namespace-scoped AIMServiceTemplates, cluster templates do not support caching configuration
and must be managed by cluster administrators, since caches themselves are namespace-scoped.

When both cluster and namespace templates exist with the same name, the namespace-scoped template
takes precedence for services in that namespace.



_Appears in:_
- [AIMClusterServiceTemplateList](#aimclusterservicetemplatelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterServiceTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)_ |  |  |  |
| `status` _[AIMServiceTemplateStatus](#aimservicetemplatestatus)_ |  |  |  |


#### AIMClusterServiceTemplateList



AIMClusterServiceTemplateList contains a list of AIMClusterServiceTemplate.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterServiceTemplateList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterServiceTemplate](#aimclusterservicetemplate) array_ |  |  |  |


#### AIMClusterServiceTemplateSpec



AIMClusterServiceTemplateSpec defines the desired state of AIMClusterServiceTemplate (cluster-scoped).

A cluster-scoped template that selects a runtime profile for a given AIM model.



_Appears in:_
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable.<br />Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | Gpu specifies GPU requirements for each replica.<br />Defines the GPU count and model types required for deployment.<br />When multiple models are specified, the template is ready if any are available,<br />and node affinity ensures pods land on nodes with matching GPUs.<br />This field is immutable after creation. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  |  |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  |  |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  |  |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  |  |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized preview unoptimized] <br /> |


#### AIMCpuRequirements



AIMCpuRequirements specifies CPU resource requirements.



_Appears in:_
- [AIMHardwareRequirements](#aimhardwarerequirements)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Requests is the number of CPU cores to request. Required and must be > 0. |  |  |
| `limits` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Limits is the maximum number of CPU cores to allow. |  |  |


#### AIMCustomModelSpec



AIMCustomModelSpec contains configuration for custom models.
These fields are only used when modelSources is specified (custom models).
For image-based models, these settings come from discovery.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies default hardware requirements for all templates.<br />Individual templates can override these defaults.<br />Required when modelSources is set and customTemplates is empty. |  |  |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies default type for all templates.<br />Individual templates can override this default.<br />When nil, templates default to "unoptimized". |  | Enum: [optimized preview unoptimized] <br /> |


#### AIMCustomTemplate



AIMCustomTemplate defines a custom template configuration for a model.
When modelSources are specified directly on AIMModel, customTemplates allow
defining explicit hardware requirements and profiles, skipping the discovery job.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the template name. If not provided, auto-generated from model name + profile. |  | MaxLength: 63 <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization status of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied | unoptimized | Enum: [optimized preview unoptimized] <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variable overrides when this template is selected. |  | MaxItems: 64 <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for this template.<br />Optional when spec.hardware is set (inherits from spec).<br />When both are set, values are merged field-by-field with template taking precedence. |  |  |
| `profile` _[AIMTemplateProfile](#aimtemplateprofile)_ | Profile declares runtime profile variables for template selection.<br />Used when multiple templates exist to select based on metric/precision. |  |  |




#### AIMDiscoveryProfileMetadata



AIMDiscoveryProfileMetadata describes the characteristics of a discovered deployment profile.



_Appears in:_
- [AIMDiscoveryProfile](#aimdiscoveryprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  |  |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  |  |
| `gpu_count` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  |  |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [bf16 fp16 fp8 int8] <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies the optimization level of this profile (optimized, unoptimized, preview). |  | Enum: [optimized preview unoptimized] <br /> |


#### AIMGpuRequirements



AIMGpuRequirements specifies GPU resource requirements.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMHardwareRequirements](#aimhardwarerequirements)
- [AIMRuntimeParameters](#aimruntimeparameters)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _integer_ | Requests is the number of GPUs to set as requests/limits.<br />Set to 0 to target GPU nodes without consuming GPU resources (useful for testing). |  | Minimum: 0 <br /> |
| `model` _string_ | Model limits deployment to a specific GPU model.<br />Example: "MI300X" |  | MaxLength: 64 <br /> |
| `minVram` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | MinVRAM limits deployment to GPUs having at least this much VRAM.<br />Used for capacity planning when model size is known. |  |  |
| `resourceName` _string_ | ResourceName is the Kubernetes resource name for GPU resources.<br />Defaults to "amd.com/gpu" if not specified. | amd.com/gpu |  |


#### AIMHardwareRequirements



AIMHardwareRequirements specifies compute resource requirements for custom models.
Used in AIMModelSpec and AIMCustomTemplate to define GPU and CPU needs.



_Appears in:_
- [AIMCustomModelSpec](#aimcustommodelspec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMServiceModelCustom](#aimservicemodelcustom)
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | GPU specifies GPU requirements. If not set, no GPUs are requested (CPU-only model). |  |  |
| `cpu` _[AIMCpuRequirements](#aimcpurequirements)_ | CPU specifies CPU requirements. |  |  |


#### AIMMetric

_Underlying type:_ _string_

AIMMetric enumerates the targeted service characteristic

_Validation:_
- Enum: [latency throughput]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMRuntimeParameters](#aimruntimeparameters)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMTemplateProfile](#aimtemplateprofile)

| Field | Description |
| --- | --- |
| `latency` |  |
| `throughput` |  |


#### AIMModel



AIMModel is the Schema for namespace-scoped AIM model catalog entries.



_Appears in:_
- [AIMModelList](#aimmodellist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModel` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMModelCache



AIMModelCache is the Schema for the modelcaches API



_Appears in:_
- [AIMModelCacheList](#aimmodelcachelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModelCache` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelCacheSpec](#aimmodelcachespec)_ |  |  |  |
| `status` _[AIMModelCacheStatus](#aimmodelcachestatus)_ |  |  |  |


#### AIMModelCacheList



AIMModelCacheList contains a list of AIMModelCache





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModelCacheList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMModelCache](#aimmodelcache) array_ |  |  |  |


#### AIMModelCacheMode

_Underlying type:_ _string_

AIMModelCacheMode indicates the ownership mode of a model cache, derived from owner references.

_Validation:_
- Enum: [Dedicated Shared]

_Appears in:_
- [AIMModelCacheStatus](#aimmodelcachestatus)

| Field | Description |
| --- | --- |
| `Dedicated` | ModelCacheModeDedicated indicates the cache has owner references and will be<br />garbage collected when its owners are deleted.<br /> |
| `Shared` | ModelCacheModeShared indicates the cache has no owner references and persists<br />independently, available for sharing across services.<br /> |


#### AIMModelCacheSpec



AIMModelCacheSpec defines the desired state of AIMModelCache



_Appears in:_
- [AIMModelCache](#aimmodelcache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceUri` _string_ | SourceURI specifies the source location of the model to download.<br />Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage).<br />This field uniquely identifies the model cache and is immutable after creation.<br />Example: hf://meta-llama/Llama-3-8B |  | MinLength: 1 <br />Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br /> |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format.<br />Determines the cache download path: /workspace/model-cache/\{modelId\}<br />For HuggingFace sources, this is typically derived from the URI (e.g., "meta-llama/Llama-3-8B").<br />For S3 sources, this must be explicitly provided (e.g., "my-team/fine-tuned-llama").<br />When not specified, derived from SourceURI for HuggingFace sources. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br /> |
| `storageClassName` _string_ | StorageClassName specifies the storage class for the cache volume.<br />When not specified, uses the cluster default storage class. |  |  |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size specifies the size of the cache volume |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env lists the environment variables to use for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  |  |
| `modelDownloadImage` _string_ | ModelDownloadImage specifies the container image used to download and initialize the model cache.<br />This image runs as a job to download model artifacts from the source URI to the cache volume.<br />When not specified, defaults to kserve/storage-initializer:v0.16.0. | kserve/storage-initializer:v0.16.0 |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |


#### AIMModelCacheStatus



AIMModelCacheStatus defines the observed state of AIMModelCache



_Appears in:_
- [AIMModelCache](#aimmodelcache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest available observations of the model cache's state |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current status of the model cache | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `progress` _[DownloadProgress](#downloadprogress)_ | Progress represents the download progress when Status is Progressing |  |  |
| `lastUsed` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastUsed represents the last time a model was deployed that used this cache |  |  |
| `persistentVolumeClaim` _string_ | PersistentVolumeClaim represents the name of the created PVC |  |  |
| `mode` _[AIMModelCacheMode](#aimmodelcachemode)_ | Mode indicates the ownership mode of this model cache, derived from owner references.<br />- Dedicated: Has owner references, will be garbage collected when owners are deleted.<br />- Shared: No owner references, persists independently and can be shared. |  | Enum: [Dedicated Shared] <br /> |


#### AIMModelConfig







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `autoDiscovery` _boolean_ | AutoDiscovery controls whether models run discovery by default.<br />When true, models run discovery jobs to extract metadata and auto-create templates.<br />When false, discovery is skipped. Discovery failures are non-fatal and reported via conditions. |  |  |


#### AIMModelDiscoveryConfig



AIMModelDiscoveryConfig controls discovery behavior for a model.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `extractMetadata` _boolean_ | ExtractMetadata controls whether metadata extraction runs for this model.<br />During metadata extraction, the controller connects to the image registry and<br />extracts the image's labels. | true |  |
| `createServiceTemplates` _boolean_ | CreateServiceTemplates controls whether (cluster) service templates are auto-created from the image metadata. | true |  |


#### AIMModelList



AIMModelList contains a list of AIMModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMModel](#aimmodel) array_ |  |  |  |


#### AIMModelSource



AIMModelSource describes a model artifact that must be downloaded for inference.
Discovery extracts these from the container's configuration to enable caching and validation.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMModelSpec](#aimmodelspec)
- [AIMServiceModelCustom](#aimservicemodelcustom)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)
- [AIMTemplateCacheSpec](#aimtemplatecachespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format.<br />Determines the cache mount path: /workspace/model-cache/\{modelId\}<br />For HuggingFace sources, this typically mirrors the URI path (e.g., meta-llama/Llama-3-8B).<br />For S3 sources, users define their own organizational structure. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br /> |
| `sourceUri` _string_ | SourceURI is the location from which the model should be downloaded.<br />Supported schemes:<br />- hf://org/model - Hugging Face Hub model<br />- s3://bucket/key - S3-compatible storage |  | Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br /> |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size is the expected storage space required for this model artifact.<br />Used for PVC sizing and capacity planning during cache creation.<br />Required for custom models (discovery does not run for inline sources).<br />For image-based models, this is populated by the discovery job. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies per-source credential overrides.<br />These variables are used for authentication when downloading this specific source.<br />Takes precedence over base-level env for the same variable name. |  |  |


#### AIMModelSourceType

_Underlying type:_ _string_

AIMModelSourceType indicates how a model's artifacts are sourced.

_Validation:_
- Enum: [Image Custom]

_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description |
| --- | --- |
| `Image` | AIMModelSourceTypeImage indicates the model is discovered from container image labels.<br /> |
| `Custom` | AIMModelSourceTypeCustom indicates the model uses explicit spec.modelSources.<br /> |


#### AIMModelSpec



AIMModelSpec defines the desired state of AIMModel.



_Appears in:_
- [AIMClusterModel](#aimclustermodel)
- [AIMModel](#aimmodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the container image URI for this AIM model.<br />This image is inspected by the operator to select runtime profiles used by templates.<br />Discovery behavior is controlled by the discovery field and runtime config's AutoDiscovery setting. |  | MinLength: 1 <br /> |
| `discovery` _[AIMModelDiscoveryConfig](#aimmodeldiscoveryconfig)_ | Discovery controls discovery behavior for this model.<br />When unset, uses runtime config defaults. |  |  |
| `defaultServiceTemplate` _string_ | DefaultServiceTemplate specifies the default AIMServiceTemplate to use when creating services for this model.<br />When set, services that reference this model will use this template if no template is explicitly specified.<br />If this is not set, a template will be automatically selected. |  |  |
| `custom` _[AIMCustomModelSpec](#aimcustommodelspec)_ | Custom contains configuration for custom models (models with inline modelSources).<br />Only used when modelSources are specified; ignored for image-based models. |  |  |
| `customTemplates` _[AIMCustomTemplate](#aimcustomtemplate) array_ | CustomTemplates defines explicit template configurations for this model.<br />These templates are created directly without running a discovery job.<br />Can be used with or without modelSources to define custom deployment configurations.<br />If omitted when modelSources is set, a single template is auto-generated<br />using the custom.hardware requirements. |  | MaxItems: 16 <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use for this model.<br />When specified, these sources are used instead of auto-discovery from the container image.<br />This enables pre-creating custom models with explicit model sources.<br />For custom models, modelSources[].size is required (discovery does not run).<br />AIM runtime currently supports only one model source. |  | MaxItems: 1 <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling the model container image.<br />These secrets are used for:<br />- OCI registry metadata extraction during discovery<br />- Pulling the image for inference services<br />The secrets are merged with any runtime config defaults.<br />For namespace-scoped models, secrets must exist in the same namespace.<br />For cluster-scoped models, secrets must exist in the operator namespace. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for authentication during model discovery and metadata extraction.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  |  |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model.<br />This includes metadata extraction jobs and any other model-related operations.<br />If empty, the default service account for the namespace is used. |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default resource requirements for services using this model.<br />Template- or service-level values override these defaults. |  |  |
| `imageMetadata` _[ImageMetadata](#imagemetadata)_ | ImageMetadata is the metadata that is used to determine which recommended service templates to create,<br />and to drive clients with richer metadata regarding this particular model. For most cases the user does<br />not need to set this field manually, for images that have the supported labels embedded in them<br />the `AIM(Cluster)Model.status.imageMetadata` field is automatically filled from the container image labels.<br />This field is intended to be used when there are network restrictions, or in other similar situations.<br />If this field is set, the remote extraction will not be performed at all. |  |  |


#### AIMModelStatus



AIMModelStatus defines the observed state of AIMModel.



_Appears in:_
- [AIMClusterModel](#aimclustermodel)
- [AIMModel](#aimmodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the overall status of the image based on its templates | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest available observations of the model's state |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  |  |
| `imageMetadata` _[ImageMetadata](#imagemetadata)_ | ImageMetadata is the metadata extracted from an AIM image |  |  |
| `sourceType` _[AIMModelSourceType](#aimmodelsourcetype)_ | SourceType indicates how this model's artifacts are sourced.<br />- "Image": Model discovered from container image labels<br />- "Custom": Model uses explicit spec.modelSources<br />Set by the controller based on whether spec.modelSources is populated. |  | Enum: [Image Custom] <br /> |


#### AIMPrecision

_Underlying type:_ _string_

AIMPrecision enumerates supported numeric precisions

_Validation:_
- Enum: [bf16 fp16 fp8 int8]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMRuntimeParameters](#aimruntimeparameters)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMTemplateProfile](#aimtemplateprofile)

| Field | Description |
| --- | --- |
| `auto` |  |
| `fp4` |  |
| `fp8` |  |
| `fp16` |  |
| `fp32` |  |
| `bf16` |  |
| `int4` |  |
| `int8` |  |


#### AIMProfile



AIMProfile contains the cached discovery results for a template.
This is the processed and validated version of AIMDiscoveryProfile that is stored
in the template's status after successful discovery.

The profile serves as a cache of runtime configuration, eliminating the need to
re-run discovery for each service that uses this template. Services and caching
mechanisms reference this cached profile for deployment parameters and model sources.

See discovery.go for AIMDiscoveryProfile (the raw discovery output) and the
relationship between these types.



_Appears in:_
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine_args` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains runtime-specific engine configuration as a free-form JSON object.<br />The structure depends on the inference engine being used (e.g., vLLM, TGI).<br />These arguments are passed to the runtime container to configure model loading and inference. |  | Schemaless: \{\} <br /> |
| `env_vars` _object (keys:string, values:string)_ | EnvVars contains environment variables required by the runtime for this profile.<br />These may include engine-specific settings, optimization flags, or hardware configuration. |  |  |
| `metadata` _[AIMProfileMetadata](#aimprofilemetadata)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `originalDiscoveryOutput` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | OriginalDiscoveryOutput contains the raw discovery job JSON output.<br />This preserves the complete discovery result from the dry-run container,<br />including all fields that may not be mapped to structured fields above. |  | Schemaless: \{\} <br /> |


#### AIMProfileMetadata



AIMProfileMetadata describes the characteristics of a cached deployment profile.
This is identical to AIMDiscoveryProfileMetadata but exists in the template status namespace.



_Appears in:_
- [AIMProfile](#aimprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  |  |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  |  |
| `gpuCount` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  |  |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [bf16 fp16 fp8 int8] <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this profile (optimized, preview, unoptimized). |  | Enum: [optimized preview unoptimized] <br /> |


#### AIMProfileType

_Underlying type:_ _string_

AIMProfileType indicates the optimization level of a deployment profile.

_Validation:_
- Enum: [optimized preview unoptimized]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomModelSpec](#aimcustommodelspec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileTypeOptimized indicates the profile has been fully optimized.<br /> |
| `preview` | AIMProfileTypePreview indicates the profile is in preview/beta state.<br /> |
| `unoptimized` | AIMProfileTypeUnoptimized indicates the profile has not been optimized.<br /> |


#### AIMResolutionScope

_Underlying type:_ _string_

AIMResolutionScope describes the scope of a resolved reference.

_Validation:_
- Enum: [Namespace Cluster Merged Unknown]

_Appears in:_
- [AIMResolvedReference](#aimresolvedreference)

| Field | Description |
| --- | --- |
| `Namespace` | AIMResolutionScopeNamespace denotes a namespace-scoped resource.<br /> |
| `Cluster` | AIMResolutionScopeCluster denotes a cluster-scoped resource.<br /> |
| `Merged` | AIMResolutionScopeMerged denotes that both cluster and namespace configs were merged.<br /> |
| `Unknown` | AIMResolutionScopeUnknown denotes that the scope could not be determined.<br /> |


#### AIMResolvedModelCache







_Appears in:_
- [AIMTemplateCacheStatus](#aimtemplatecachestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `uid` _string_ | UID of the AIMModelCache resource |  |  |
| `name` _string_ | Name of the AIMModelCache resource |  |  |
| `model` _string_ | Model is the name of the model that is cached |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status of the model cache |  |  |
| `persistentVolumeClaim` _string_ | PersistentVolumeClaim name if available |  |  |
| `mountPoint` _string_ | MountPoint is the mount point for the model cache |  |  |


#### AIMResolvedReference



AIMResolvedReference captures metadata about a resolved reference.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)
- [AIMServiceCacheStatus](#aimservicecachestatus)
- [AIMServiceStatus](#aimservicestatus)
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)
- [AIMTemplateCacheStatus](#aimtemplatecachestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the resource name that satisfied the reference. |  |  |
| `namespace` _string_ | Namespace identifies where the resource was found when namespace-scoped.<br />Empty indicates a cluster-scoped resource. |  |  |
| `scope` _[AIMResolutionScope](#aimresolutionscope)_ | Scope indicates whether the resolved resource was namespace or cluster scoped. |  | Enum: [Namespace Cluster Merged Unknown] <br /> |
| `kind` _string_ | Kind is the fully-qualified kind of the resolved reference, when known. |  |  |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#uid-types-pkg)_ | UID captures the unique identifier of the resolved reference, when known. |  |  |


#### AIMRuntimeConfig



AIMRuntimeConfig is the Schema for namespace-scoped AIM runtime configurations.



_Appears in:_
- [AIMRuntimeConfigList](#aimruntimeconfiglist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMRuntimeConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMRuntimeConfigSpec](#aimruntimeconfigspec)_ |  |  |  |
| `status` _[AIMRuntimeConfigStatus](#aimruntimeconfigstatus)_ |  |  |  |


#### AIMRuntimeConfigCommon



AIMRuntimeConfigCommon captures configuration fields shared across cluster and namespace scopes.
These settings apply to both AIMRuntimeConfig (namespace-scoped) and AIMClusterRuntimeConfig (cluster-scoped).
It embeds AIMServiceRuntimeConfig which contains fields that can also be overridden at the service level.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > RuntimeConfig.Env > Template.Env > Profile.Env |  |  |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  |  |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  |  |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  |  |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  |  |


#### AIMRuntimeConfigLabelPropagationSpec







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled, if true, allows propagating parent labels to all child resources it creates directly<br />Only label keys that match the ones in Match are propagated. | false |  |
| `match` _string array_ | Match is a list of label keys that will be propagated to any child resources created.<br />Wildcards are supported, so for example `org.my/my-key-*` would match any label with that prefix. |  |  |


#### AIMRuntimeConfigList



AIMRuntimeConfigList contains a list of AIMRuntimeConfig.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMRuntimeConfigList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMRuntimeConfig](#aimruntimeconfig) array_ |  |  |  |


#### AIMRuntimeConfigSpec



AIMRuntimeConfigSpec defines namespace-scoped overrides for AIM resources.



_Appears in:_
- [AIMRuntimeConfig](#aimruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > RuntimeConfig.Env > Template.Env > Profile.Env |  |  |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  |  |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  |  |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  |  |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  |  |


#### AIMRuntimeConfigStatus



AIMRuntimeConfigStatus records the resolved config reference surfaced to consumers.



_Appears in:_
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)
- [AIMRuntimeConfig](#aimruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the last reconciled generation. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions communicate reconciliation progress. |  |  |


#### AIMRuntimeParameters



AIMRuntimeParameters contains the runtime configuration parameters shared
across templates and services. Fields use pointers to allow optional usage
in different contexts (required in templates, optional in service overrides).



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | Gpu specifies GPU requirements for each replica.<br />Defines the GPU count and model types required for deployment.<br />When multiple models are specified, the template is ready if any are available,<br />and node affinity ensures pods land on nodes with matching GPUs.<br />This field is immutable after creation. |  |  |


#### AIMRuntimeRoutingConfig



AIMRuntimeRoutingConfig configures HTTP routing defaults for inference services.
These settings control how Gateway API HTTPRoutes are created and configured.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether HTTP routing is managed for inference services using this config.<br />When true, the operator creates HTTPRoute resources for services that reference this config.<br />When false or unset, routing must be explicitly enabled on each service.<br />This provides a namespace or cluster-wide default that individual services can override. |  |  |
| `gatewayRef` _[ParentReference](#parentreference)_ | GatewayRef specifies the Gateway API Gateway resource that should receive HTTPRoutes.<br />This identifies the parent gateway for routing traffic to inference services.<br />The gateway can be in any namespace (cross-namespace references are supported).<br />If routing is enabled but GatewayRef is not specified, service reconciliation will fail<br />with a validation error. |  |  |
| `pathTemplate` _string_ | PathTemplate defines the HTTP path template for routes, evaluated using JSONPath expressions.<br />The template is rendered against the AIMService object to generate unique paths.<br />Example templates:<br />- `/\{.metadata.namespace\}/\{.metadata.name\}` - namespace and service name<br />- `/\{.metadata.namespace\}/\{.metadata.labels['team']\}/inference` - with label<br />- `/models/\{.spec.aimModelName\}` - based on model name<br />The template must:<br />- Use valid JSONPath expressions wrapped in \{...\}<br />- Reference fields that exist on the service<br />- Produce a path ≤ 200 characters after rendering<br />- Result in valid URL path segments (lowercase, RFC 1123 compliant)<br />If evaluation fails, the service enters Degraded state with PathTemplateInvalid reason.<br />Individual services can override this template via spec.routing.pathTemplate. |  |  |
| `requestTimeout` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#duration-v1-meta)_ | RequestTimeout defines the HTTP request timeout for routes.<br />This sets the maximum duration for a request to complete before timing out.<br />The timeout applies to the entire request/response cycle.<br />If not specified, no timeout is set on the route.<br />Individual services can override this value via spec.routing.requestTimeout. |  |  |
| `annotations` _object (keys:string, values:string)_ | Annotations defines default annotations to add to all HTTPRoute resources.<br />Services can add additional annotations or override these via spec.routingAnnotations.<br />When both are specified, service annotations take precedence for conflicting keys.<br />Common use cases include ingress controller settings, rate limiting, monitoring labels,<br />and security policies that should apply to all services using this config. |  |  |


#### AIMService



AIMService manages a KServe-based AIM inference service for the selected model and template.
Note: KServe uses {name}-{namespace} format which must not exceed 63 characters.
This constraint is validated at runtime since CEL cannot access metadata.namespace.



_Appears in:_
- [AIMServiceList](#aimservicelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMService` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMServiceSpec](#aimservicespec)_ |  |  |  |
| `status` _[AIMServiceStatus](#aimservicestatus)_ |  |  |  |


#### AIMServiceAutoScaling



AIMServiceAutoScaling configures KEDA-based autoscaling with custom metrics.
This enables automatic scaling based on metrics collected from OpenTelemetry.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metrics` _[AIMServiceMetricsSpec](#aimservicemetricsspec) array_ | Metrics is a list of metrics to be used for autoscaling.<br />Each metric defines a source (PodMetric) and target values. |  |  |


#### AIMServiceCacheStatus



AIMServiceCacheStatus captures cache-related status for an AIMService.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateCacheRef` _[AIMResolvedReference](#aimresolvedreference)_ | TemplateCacheRef references the TemplateCache being used, if any. |  |  |
| `retryAttempts` _integer_ | RetryAttempts tracks how many times this service has attempted to retry a failed cache.<br />Each service gets exactly one retry attempt. When a TemplateCache enters Failed state,<br />this counter is incremented from 0 to 1 after deleting failed ModelCaches.<br />If the retry fails (cache enters Failed again with attempts == 1), the service degrades. |  |  |


#### AIMServiceCachingConfig



AIMServiceCachingConfig controls caching behavior for a service.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[AIMCachingMode](#aimcachingmode)_ | Mode controls when to use caching.<br />- Auto (default): Use cache if it exists, but don't create one<br />- Always: Always use cache, create if it doesn't exist<br />- Never: Don't use cache even if it exists | Auto | Enum: [Auto Always Never] <br /> |


#### AIMServiceList



AIMServiceList contains a list of AIMService.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMService](#aimservice) array_ |  |  |  |


#### AIMServiceMetricTarget



AIMServiceMetricTarget defines the target value for a metric.
Specifies how the metric value should be interpreted and what target to maintain.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type specifies how to interpret the metric value.<br />"Value": absolute value target (use Value field)<br />"AverageValue": average value across all pods (use AverageValue field)<br />"Utilization": percentage utilization for resource metrics (use AverageUtilization field) |  | Enum: [Value AverageValue Utilization] <br /> |
| `value` _string_ | Value is the target value of the metric (as a quantity).<br />Used when Type is "Value".<br />Example: "1" for 1 request, "100m" for 100 millicores |  |  |
| `averageValue` _string_ | AverageValue is the target value of the average of the metric across all relevant pods (as a quantity).<br />Used when Type is "AverageValue".<br />Example: "100m" for 100 millicores per pod |  |  |
| `averageUtilization` _integer_ | AverageUtilization is the target value of the average of the resource metric across all relevant pods,<br />represented as a percentage of the requested value of the resource for the pods.<br />Used when Type is "Utilization". Only valid for Resource metric source type.<br />Example: 80 for 80% utilization |  |  |


#### AIMServiceMetricsSpec



AIMServiceMetricsSpec defines a single metric for autoscaling.
Specifies the metric source type and configuration.



_Appears in:_
- [AIMServiceAutoScaling](#aimserviceautoscaling)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type is the type of metric source.<br />Valid values: "PodMetric" (per-pod custom metrics). |  | Enum: [PodMetric] <br /> |
| `podmetric` _[AIMServicePodMetricSource](#aimservicepodmetricsource)_ | PodMetric refers to a metric describing each pod in the current scale target.<br />Used when Type is "PodMetric". Supports backends like OpenTelemetry for custom metrics. |  |  |


#### AIMServiceModel



AIMServiceModel specifies which model to deploy. Exactly one field must be set.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name references an existing AIMModel or AIMClusterModel by metadata.name.<br />The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel.<br />Example: `meta-llama-3-8b` |  |  |
| `image` _string_ | Image specifies a container image URI directly.<br />The controller searches for an existing model with this image, or creates one if none exists.<br />The scope of the created model is controlled by the runtime config's ModelCreationScope field.<br />Example: `ghcr.io/silogen/llama-3-8b:v1.2.0` |  |  |
| `custom` _[AIMServiceModelCustom](#aimservicemodelcustom)_ | Custom specifies a custom model configuration with explicit base image,<br />model sources, and hardware requirements. The controller will search for<br />an existing matching AIMModel or auto-create one if not found. |  |  |


#### AIMServiceModelCustom



AIMServiceModelCustom specifies a custom model configuration with explicit base image,
model sources, and hardware requirements. Used for ad-hoc custom model deployments.



_Appears in:_
- [AIMServiceModel](#aimservicemodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `baseImage` _string_ | BaseImage is the container image URI for the AIM base image.<br />This will be used as the image for the auto-created AIMModel.<br />Example: `ghcr.io/silogen/aim-base:0.7.0` |  |  |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use.<br />The controller will search for or create an AIMModel with these sources.<br />For custom models, modelSources[].size should be specified (discovery does not run).<br />AIM runtime currently supports only one model source. |  | MaxItems: 1 <br />MinItems: 1 <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies the GPU and CPU requirements for this custom model.<br />GPU is optional - if not set, no GPUs are requested (CPU-only model). |  |  |


#### AIMServiceOverrides



AIMServiceOverrides allows overriding template parameters at the service level.
All fields are optional. When specified, they override the corresponding values
from the referenced AIMServiceTemplate.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | Gpu specifies GPU requirements for each replica.<br />Defines the GPU count and model types required for deployment.<br />When multiple models are specified, the template is ready if any are available,<br />and node affinity ensures pods land on nodes with matching GPUs.<br />This field is immutable after creation. |  |  |


#### AIMServicePodMetric



AIMServicePodMetric identifies the pod metric and its backend.
Supports multiple metrics backends including OpenTelemetry.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `backend` _string_ | Backend defines the metrics backend to use.<br />If not specified, defaults to "opentelemetry". | opentelemetry | Enum: [opentelemetry] <br /> |
| `serverAddress` _string_ | ServerAddress specifies the address of the metrics backend server.<br />If not specified, defaults to "keda-otel-scaler.keda.svc:4317" for OpenTelemetry backend. |  |  |
| `metricNames` _string array_ | MetricNames specifies which metrics to collect from pods and send to ServerAddress.<br />Example: ["vllm:num_requests_running"] |  |  |
| `query` _string_ | Query specifies the query to run to retrieve metrics from the backend.<br />The query syntax depends on the backend being used.<br />Example: "vllm:num_requests_running" for OpenTelemetry. |  |  |
| `operationOverTime` _string_ | OperationOverTime specifies the operation to aggregate metrics over time.<br />Valid values: "last_one", "avg", "max", "min", "rate", "count"<br />Default: "last_one" |  |  |


#### AIMServicePodMetricSource



AIMServicePodMetricSource defines pod-level metrics configuration.
Specifies the metric identification and target values for pod-based autoscaling.



_Appears in:_
- [AIMServiceMetricsSpec](#aimservicemetricsspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMServicePodMetric](#aimservicepodmetric)_ | Metric contains the metric identification and backend configuration.<br />Defines which metrics to collect and how to query them. |  |  |
| `target` _[AIMServiceMetricTarget](#aimservicemetrictarget)_ | Target specifies the target value for the metric.<br />The autoscaler will scale to maintain this target value. |  |  |


#### AIMServiceRoutingStatus



AIMServiceRoutingStatus captures observed routing details.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the HTTP path prefix used when routing is enabled.<br />Example: `/tenant/svc-uuid` |  |  |


#### AIMServiceRuntimeConfig



AIMServiceRuntimeConfig contains runtime configuration fields that apply to services.
This struct is shared between AIMService.spec (inlined) and AIMRuntimeConfigCommon,
allowing services to override these specific runtime settings while inheriting defaults
from namespace/cluster RuntimeConfigs.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > RuntimeConfig.Env > Template.Env > Profile.Env |  |  |


#### AIMServiceSpec



AIMServiceSpec defines the desired state of AIMService.

Binds a canonical model to an AIMServiceTemplate and configures replicas,
caching behavior, and optional overrides. The template governs the base
runtime selection knobs, while the overrides field allows service-specific
customization.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[AIMServiceModel](#aimservicemodel)_ | Model specifies which model to deploy using one of the available reference methods.<br />Use `ref` to reference an existing AIMModel/AIMClusterModel by name, or use `image`<br />to specify a container image URI directly (which will auto-create a model if needed). |  |  |
| `template` _[AIMServiceTemplateConfig](#aimservicetemplateconfig)_ | Template contains template selection and configuration.<br />Use Template.Name to specify an explicit template, or omit to auto-select. |  |  |
| `caching` _[AIMServiceCachingConfig](#aimservicecachingconfig)_ | Caching controls caching behavior for this service.<br />When nil, defaults to Auto mode (use cache if available, don't create). |  |  |
| `cacheModel` _boolean_ | DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version.<br />For backward compatibility, if Caching is not set, this field is used.<br />Tri-state logic: nil=Auto, true=Always, false=Never |  |  |
| `replicas` _integer_ | Replicas specifies the number of replicas for this service.<br />When not specified, defaults to 1 replica.<br />This value overrides any replica settings from the template.<br />For autoscaling, use MinReplicas and MaxReplicas instead. | 1 |  |
| `minReplicas` _integer_ | MinReplicas specifies the minimum number of replicas for autoscaling.<br />Defaults to 1. Scale to zero is not supported.<br />When specified with MaxReplicas, enables autoscaling for the service. |  | Minimum: 1 <br /> |
| `maxReplicas` _integer_ | MaxReplicas specifies the maximum number of replicas for autoscaling.<br />Required when MinReplicas is set or when AutoScaling configuration is provided. |  | Minimum: 1 <br /> |
| `autoScaling` _[AIMServiceAutoScaling](#aimserviceautoscaling)_ | AutoScaling configures advanced autoscaling behavior using KEDA.<br />Supports custom metrics from OpenTelemetry backend.<br />When specified, MinReplicas and MaxReplicas should also be set. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > RuntimeConfig.Env > Template.Env > Profile.Env |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources overrides the container resource requirements for this service.<br />When specified, these values take precedence over the template and image defaults. |  |  |
| `overrides` _[AIMServiceOverrides](#aimserviceoverrides)_ | Overrides allows overriding specific template parameters for this service.<br />When specified, these values take precedence over the template values. |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  |  |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for the inference workload.<br />This service account is used by the deployed inference pods.<br />If empty, the default service account for the namespace is used. |  |  |


#### AIMServiceStatus



AIMServiceStatus defines the observed state of AIMService.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  |  |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the service lifecycle.<br />Values: `Pending`, `Starting`, `Running`, `Degraded`, `Failed`. | Pending | Enum: [Pending Starting Running Degraded Failed] <br /> |
| `routing` _[AIMServiceRoutingStatus](#aimserviceroutingstatus)_ | Routing surfaces information about the configured HTTP routing, when enabled. |  |  |
| `resolvedTemplate` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedTemplate captures metadata about the template that satisfied the reference. |  |  |
| `cache` _[AIMServiceCacheStatus](#aimservicecachestatus)_ | Cache captures cache-related status for this service. |  |  |




#### AIMServiceTemplate



AIMServiceTemplate is the Schema for namespace-scoped AIM service templates.



_Appears in:_
- [AIMServiceTemplateList](#aimservicetemplatelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMServiceTemplateSpec](#aimservicetemplatespec)_ |  |  |  |
| `status` _[AIMServiceTemplateStatus](#aimservicetemplatestatus)_ |  |  |  |


#### AIMServiceTemplateConfig



AIMServiceTemplateConfig contains template selection configuration for AIMService.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use.<br />The template selects the runtime profile and GPU parameters.<br />When not specified, a template will be automatically selected based on the model. |  |  |
| `allowUnoptimized` _boolean_ | AllowUnoptimized, if true, will allow automatic selection of templates<br />that resolve to an unoptimized profile. |  |  |


#### AIMServiceTemplateList



AIMServiceTemplateList contains a list of AIMServiceTemplate.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceTemplateList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMServiceTemplate](#aimservicetemplate) array_ |  |  |  |


#### AIMServiceTemplateScope

_Underlying type:_ _string_

AIMServiceTemplateScope is retained for backwards compatibility with existing consumers.

_Validation:_
- Enum: [Namespace Cluster Unknown]

_Appears in:_
- [AIMTemplateCacheSpec](#aimtemplatecachespec)



#### AIMServiceTemplateSpec



AIMServiceTemplateSpec defines the desired state of AIMServiceTemplate (namespace-scoped).

A namespaced and versioned template that selects a runtime profile
for a given AIM model (by canonical name). Templates are intentionally
narrow: they describe runtime selection knobs for the AIM container and do
not redefine the full Kubernetes deployment shape.



_Appears in:_
- [AIMServiceTemplate](#aimservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable.<br />Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | Gpu specifies GPU requirements for each replica.<br />Defines the GPU count and model types required for deployment.<br />When multiple models are specified, the template is ready if any are available,<br />and node affinity ensures pods land on nodes with matching GPUs.<br />This field is immutable after creation. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  |  |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  |  |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  |  |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  |  |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized preview unoptimized] <br /> |
| `caching` _[AIMTemplateCachingConfig](#aimtemplatecachingconfig)_ | Caching configures model caching behavior for this namespace-scoped template.<br />When enabled, models will be cached using the specified environment variables<br />during download. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables to use for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  |  |


#### AIMServiceTemplateSpecCommon







_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable.<br />Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | Gpu specifies GPU requirements for each replica.<br />Defines the GPU count and model types required for deployment.<br />When multiple models are specified, the template is ready if any are available,<br />and node affinity ensures pods land on nodes with matching GPUs.<br />This field is immutable after creation. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  |  |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  |  |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  |  |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  |  |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized preview unoptimized] <br /> |


#### AIMServiceTemplateStatus



AIMServiceTemplateStatus defines the observed state of AIMServiceTemplate.



_Appears in:_
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)
- [AIMServiceTemplate](#aimservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  |  |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  |  |
| `resolvedCache` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedCache captures metadata about which cache is used for this template |  |  |
| `resolvedHardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | ResolvedHardware contains the resolved hardware requirements for this template.<br />These values are computed from discovery results and spec defaults, and represent<br />what will actually be used when creating InferenceServices.<br />Resolution order: discovery output > spec values > defaults. |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the template lifecycle.<br />Values: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`. | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources list the models that this template requires to run. These are the models that will be<br />cached, if this template is cached. |  |  |
| `profile` _[AIMProfile](#aimprofile)_ | Profile contains the full discovery result profile as a free-form JSON object.<br />This includes metadata, engine args, environment variables, and model details. |  |  |
| `discoveryJob` _[AIMResolvedReference](#aimresolvedreference)_ | DiscoveryJob is a reference to the job that was run for discovery |  |  |


#### AIMStorageConfig



AIMStorageConfig configures storage defaults for model caches and PVCs.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `defaultStorageClassName` _string_ | DefaultStorageClassName specifies the storage class to use for model caches and PVCs<br />when the consuming resource (AIMModelCache, AIMTemplateCache, AIMServiceTemplate) does not<br />specify a storage class. If this field is empty, the cluster's default storage class is used. |  |  |
| `pvcHeadroomPercent` _integer_ | PVCHeadroomPercent specifies the percentage of extra space to add to PVCs<br />for model storage. This accounts for filesystem overhead and temporary files<br />during model loading. The value represents a percentage (e.g., 10 means 10% extra space).<br />If not specified, defaults to 10%. | 10 | Minimum: 0 <br /> |


#### AIMTemplateCache



AIMTemplateCache pre-warms model caches for a specified template.



_Appears in:_
- [AIMTemplateCacheList](#aimtemplatecachelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMTemplateCache` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMTemplateCacheSpec](#aimtemplatecachespec)_ |  |  |  |
| `status` _[AIMTemplateCacheStatus](#aimtemplatecachestatus)_ |  |  |  |


#### AIMTemplateCacheList



AIMTemplateCacheList contains a list of AIMTemplateCache.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMTemplateCacheList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMTemplateCache](#aimtemplatecache) array_ |  |  |  |


#### AIMTemplateCacheMode

_Underlying type:_ _string_

AIMTemplateCacheMode controls the ownership behavior of model caches created by a template cache.

_Validation:_
- Enum: [Dedicated Shared]

_Appears in:_
- [AIMTemplateCacheSpec](#aimtemplatecachespec)

| Field | Description |
| --- | --- |
| `Dedicated` | TemplateCacheModeDedicated means model caches have owner references to the template cache.<br />When the template cache is deleted, all its model caches are garbage collected.<br />Use this mode for service-specific caches that should be cleaned up with the service.<br /> |
| `Shared` | TemplateCacheModeShared means model caches have no owner references.<br />Model caches persist independently of template cache lifecycle and can be shared.<br />This is the default mode for long-lived, reusable caches.<br /> |


#### AIMTemplateCacheSpec



AIMTemplateCacheSpec defines the desired state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateName` _string_ | TemplateName is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to cache.<br />The controller will first look for a namespace-scoped AIMServiceTemplate in the same namespace.<br />If not found, it will look for a cluster-scoped AIMClusterServiceTemplate with the same name.<br />Namespace-scoped templates take priority over cluster-scoped templates. |  | MinLength: 1 <br /> |
| `templateScope` _[AIMServiceTemplateScope](#aimservicetemplatescope)_ | TemplateScope indicates whether the template is namespace-scoped or cluster-scoped.<br />This field is set by the controller during template resolution. |  | Enum: [Namespace Cluster Unknown] <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables to use for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  |  |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  |  |
| `storageClassName` _string_ | StorageClassName specifies the storage class for cache volumes.<br />When not specified, uses the cluster default storage class. |  |  |
| `downloadImage` _string_ | DownloadImage specifies the container image used to download and initialize model caches.<br />When not specified, the controller uses the default model download image. |  |  |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to cache for this template.<br />These sources are typically copied from the resolved template's model sources. |  |  |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |
| `mode` _[AIMTemplateCacheMode](#aimtemplatecachemode)_ | Mode controls the ownership behavior of model caches created by this template cache.<br />- Dedicated: Model caches are owned by this template cache and garbage collected when it's deleted.<br />- Shared (default): Model caches have no owner references and persist independently.<br />When a Shared template cache encounters model caches with owner references, it promotes them<br />to shared by removing the owner references, ensuring they persist for long-term use. | Shared | Enum: [Dedicated Shared] <br /> |


#### AIMTemplateCacheStatus



AIMTemplateCacheStatus defines the observed state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of the template cache state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high-level status of the template cache. | Pending | Enum: [Pending Progressing Ready Failed Degraded NotAvailable] <br /> |
| `resolvedTemplateKind` _string_ | ResolvedTemplateKind indicates whether the template resolved to a namespace-scoped<br />AIMServiceTemplate or cluster-scoped AIMClusterServiceTemplate.<br />Values: "AIMServiceTemplate", "AIMClusterServiceTemplate" |  |  |
| `modelCaches` _object (keys:string, values:[AIMResolvedModelCache](#aimresolvedmodelcache))_ | ModelCaches maps model names to their resolved AIMModelCache resources. |  |  |


#### AIMTemplateCachingConfig



AIMTemplateCachingConfig configures model caching behavior for namespace-scoped templates.



_Appears in:_
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether caching is enabled for this template.<br />Defaults to `false`. | false |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables to use when downloading the model for caching.<br />These variables are available to the model download process and can be used<br />to configure download behavior, authentication, proxies, etc.<br />If not set, falls back to the template's top-level Env field. |  |  |




#### AIMTemplateProfile



AIMTemplateProfile declares profile variables for template selection.
Used in AIMCustomTemplate to specify optimization targets.



_Appears in:_
- [AIMCustomTemplate](#aimcustomtemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric specifies the optimization target (e.g., latency, throughput). |  | Enum: [latency throughput] <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numerical precision (e.g., fp8, fp16, bf16). |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br /> |


#### DownloadProgress



DownloadProgress represents the download progress for a model cache



_Appears in:_
- [AIMModelCacheStatus](#aimmodelcachestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `totalBytes` _integer_ | TotalBytes is the expected total size of the download in bytes |  |  |
| `downloadedBytes` _integer_ | DownloadedBytes is the number of bytes downloaded so far |  |  |
| `percentage` _integer_ | Percentage is the download progress as a percentage (0-100) |  | Maximum: 100 <br />Minimum: 0 <br /> |
| `displayPercentage` _string_ | DisplayPercentage is a human-readable progress string (e.g., "45 %")<br />This field is automatically populated from Progress.Percentage |  |  |


#### ImageMetadata



ImageMetadata contains metadata extracted from or provided for a container image.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[ModelMetadata](#modelmetadata)_ | Model contains AMD Silogen model-specific metadata. |  |  |
| `oci` _[OCIMetadata](#ocimetadata)_ | OCI contains standard OCI image metadata. |  |  |
| `originalLabels` _object (keys:string, values:string)_ | OriginalLabels contains the raw OCI image labels as a JSON object.<br />This preserves all labels from the image, including those not mapped to structured fields. |  |  |


#### ModelMetadata



ModelMetadata contains AMD Silogen model-specific metadata extracted from image labels.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `canonicalName` _string_ | CanonicalName is the canonical model identifier (e.g., mistralai/Mixtral-8x22B-Instruct-v0.1).<br />Extracted from: org.amd.silogen.model.canonicalName |  |  |
| `source` _string_ | Source is the URL where the model can be found.<br />Extracted from: org.amd.silogen.model.source |  |  |
| `tags` _string array_ | Tags are descriptive tags (e.g., ["text-generation", "chat", "instruction"]).<br />Extracted from: org.amd.silogen.model.tags (comma-separated) |  |  |
| `versions` _string array_ | Versions lists available versions.<br />Extracted from: org.amd.silogen.model.versions (comma-separated) |  |  |
| `variants` _string array_ | Variants lists model variants.<br />Extracted from: org.amd.silogen.model.variants (comma-separated) |  |  |
| `hfTokenRequired` _boolean_ | HFTokenRequired indicates if a HuggingFace token is required.<br />Extracted from: org.amd.silogen.hfToken.required |  |  |
| `title` _string_ | Title is the Silogen-specific title for the model.<br />Extracted from: org.amd.silogen.title |  |  |
| `descriptionFull` _string_ | DescriptionFull is the full description.<br />Extracted from: org.amd.silogen.description.full |  |  |
| `releaseNotes` _string_ | ReleaseNotes contains release notes for this version.<br />Extracted from: org.amd.silogen.release.notes |  |  |
| `recommendedDeployments` _[RecommendedDeployment](#recommendeddeployment) array_ | RecommendedDeployments contains recommended deployment configurations.<br />Extracted from: org.amd.silogen.model.recommendedDeployments (parsed from JSON array) |  |  |


#### ModelSourceFilter



ModelSourceFilter defines a pattern for discovering images.
Supports multiple formats:
- Repository patterns: "org/repo*" - matches repositories with wildcards
- Repository with tag: "org/repo:1.0.0" - exact tag match
- Full URI: "ghcr.io/org/repo:1.0.0" - overrides registry and tag
- Full URI with wildcard: "ghcr.io/org/repo*" - overrides registry, matches pattern



_Appears in:_
- [AIMClusterModelSourceSpec](#aimclustermodelsourcespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image pattern with wildcard and full URI support.<br />Supported formats:<br />- Repository pattern: "amdenterpriseai/aim-*"<br />- Repository with tag: "silogen/aim-llama:1.0.0" (overrides versions field)<br />- Full URI: "ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1" (overrides spec.registry and versions)<br />- Full URI with wildcard: "ghcr.io/silogen/aim-*" (overrides spec.registry)<br />When a full URI is specified (including registry like ghcr.io), only images from that<br />registry will match. When a tag is included, it takes precedence over the versions field.<br />Wildcard: * matches any sequence of characters. |  | MaxLength: 512 <br /> |
| `exclude` _string array_ | Exclude lists specific repository names to skip (exact match on repository name only, not registry).<br />Useful for excluding base images or experimental versions.<br />Examples:<br />- ["amdenterpriseai/aim-base", "amdenterpriseai/aim-experimental"]<br />- ["silogen/aim-base"] - works with "ghcr.io/silogen/aim-*" (registry is not checked in exclusion)<br />Note: Exclusions match against repository names (e.g., "silogen/aim-base"), not full URIs. |  |  |
| `versions` _string array_ | Versions specifies semantic version constraints for this filter.<br />If specified, overrides the global Versions field.<br />Only tags that parse as valid semver are considered (including prereleases like 0.8.1-rc1).<br />Ignored if the Image field includes an explicit tag (e.g., "repo:1.0.0").<br />Examples: ">=1.0.0", "<2.0.0", "~1.2.0" (patch updates), "^1.0.0" (minor updates)<br />Prerelease versions (e.g., 0.8.1-rc1) are supported and follow semver rules:<br />- 0.8.1-rc1 matches ">=0.8.0" (prerelease is part of version 0.8.1)<br />- Use ">=0.8.1-rc1" to match only that prerelease or higher<br />- Leave empty to match all tags (including prereleases and non-semver tags) |  |  |


#### OCIMetadata



OCIMetadata contains standard OCI image metadata extracted from image labels.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `title` _string_ | Title is the human-readable title.<br />Extracted from: org.opencontainers.image.title |  |  |
| `description` _string_ | Description is a brief description.<br />Extracted from: org.opencontainers.image.description |  |  |
| `licenses` _string_ | Licenses is the SPDX license identifier(s).<br />Extracted from: org.opencontainers.image.licenses |  |  |
| `vendor` _string_ | Vendor is the organization that produced the image.<br />Extracted from: org.opencontainers.image.vendor |  |  |
| `authors` _string_ | Authors is contact details of the authors.<br />Extracted from: org.opencontainers.image.authors |  |  |
| `source` _string_ | Source is the URL to the source code repository.<br />Extracted from: org.opencontainers.image.source |  |  |
| `documentation` _string_ | Documentation is the URL to documentation.<br />Extracted from: org.opencontainers.image.documentation |  |  |
| `created` _string_ | Created is the creation timestamp.<br />Extracted from: org.opencontainers.image.created |  |  |
| `revision` _string_ | Revision is the source control revision.<br />Extracted from: org.opencontainers.image.revision |  |  |
| `version` _string_ | Version is the image version.<br />Extracted from: org.opencontainers.image.version |  |  |




#### RecommendedDeployment



RecommendedDeployment describes a recommended deployment configuration for a model.



_Appears in:_
- [ModelMetadata](#modelmetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuModel` _string_ | GPUModel is the GPU model name (e.g., MI300X, MI325X) |  |  |
| `gpuCount` _integer_ | GPUCount is the number of GPUs required |  |  |
| `precision` _string_ | Precision is the recommended precision (e.g., fp8, fp16, bf16) |  |  |
| `metric` _string_ | Metric is the optimization target (e.g., latency, throughput) |  |  |
| `description` _string_ | Description provides additional context about this deployment configuration |  |  |


#### RuntimeConfigRef







_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMModelCacheSpec](#aimmodelcachespec)
- [AIMModelSpec](#aimmodelspec)
- [AIMServiceSpec](#aimservicespec)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMTemplateCacheSpec](#aimtemplatecachespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  |  |


