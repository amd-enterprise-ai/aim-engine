# API Reference

## Packages
- [aim.eai.amd.com/v1alpha1](#aimeaiamdcomv1alpha1)


## aim.eai.amd.com/v1alpha1

Package v1alpha1 contains API Schema definitions for the aim v1alpha1 API group.

### Resource Types
- [AIMArtifact](#aimartifact)
- [AIMArtifactList](#aimartifactlist)
- [AIMClusterModel](#aimclustermodel)
- [AIMClusterModelList](#aimclustermodellist)
- [AIMClusterModelSource](#aimclustermodelsource)
- [AIMClusterModelSourceList](#aimclustermodelsourcelist)
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)
- [AIMClusterRuntimeConfigList](#aimclusterruntimeconfiglist)
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)
- [AIMClusterServiceTemplateList](#aimclusterservicetemplatelist)
- [AIMModel](#aimmodel)
- [AIMModelList](#aimmodellist)
- [AIMRuntimeConfig](#aimruntimeconfig)
- [AIMRuntimeConfigList](#aimruntimeconfiglist)
- [AIMService](#aimservice)
- [AIMServiceList](#aimservicelist)
- [AIMServiceTemplate](#aimservicetemplate)
- [AIMServiceTemplateList](#aimservicetemplatelist)
- [AIMTemplateCache](#aimtemplatecache)
- [AIMTemplateCacheList](#aimtemplatecachelist)



#### AIMAdapterDisk



AIMAdapterDisk configures the shared, RWX adapter disk provisioned alongside a
model artifact. When present on a type=model artifact, the controller provisions
a second PersistentVolumeClaim (ReadWriteMany) owned by the model artifact and
shared by every AIMService that serves adapters on this base model.



_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size is the requested size of the adapter disk PVC.<br />Defaults to 50Gi when unset (a cascade default may override it). |  | Optional: \{\} <br /> |
| `storageClassName` _string_ | StorageClassName specifies the storage class for the adapter disk.<br />When empty, the cluster default storage class is used.<br />The access mode is fixed at ReadWriteMany by the controller. |  | Optional: \{\} <br /> |


#### AIMAdapterMode

_Underlying type:_ _string_

AIMAdapterMode selects the adapter contract for the service. Its values are
the lowercase tokens the inference container reads via the AIM_ADAPTER_MODE
env, and the field is immutable after creation.
  - static (default): the served set is fixed at creation — spec.adapters is
    CEL-immutable. The adapter disk is mounted read-only only when the service
    declares at least one adapter.
  - dynamic: spec.adapters may be edited after creation; the runtime
    hot-loads/unloads from the mounted subtree. The adapter disk is mounted
    (immutably) whenever the service is in dynamic mode — even at zero adapters
    — so add/remove never restarts the pod.

A service serves no adapters by simply declaring none: the default-static,
no-adapters case mounts nothing.

_Validation:_
- Enum: [static dynamic]

_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description |
| --- | --- |
| `static` | AdapterModeStatic fixes the adapter set at creation; the disk is mounted<br />only when adapters are declared.<br /> |
| `dynamic` | AdapterModeDynamic permits editing spec.adapters and mounts the disk even<br />at zero adapters.<br /> |


#### AIMAdapterState

_Underlying type:_ _string_

AIMAdapterState is the disk-side lifecycle state of an adapter within a service's
subtree. The controller tracks staging and removal directly; the engine-reported
states (Loaded/LoadRejected) are reserved until the inference container exposes a
per-adapter load-status surface.

_Validation:_
- Enum: [Pending Downloading Downloaded Deleting Loaded LoadRejected]

_Appears in:_
- [AIMServiceAdapterStatus](#aimserviceadapterstatus)

| Field | Description |
| --- | --- |
| `Pending` | AdapterStatePending means the adapter is applied and waiting on a precondition.<br /> |
| `Downloading` | AdapterStateDownloading means a staging Job is running for this adapter.<br /> |
| `Downloaded` | AdapterStateDownloaded means the bytes are staged in this service's subtree.<br /> |
| `Deleting` | AdapterStateDeleting means the adapter was removed from spec.adapters and its<br />bytes are being reclaimed from the service subtree by the subtree-sync Job.<br />The entry is dropped from status once the prune completes.<br /> |
| `Loaded` | AdapterStateLoaded means the inference engine has the adapter in memory.<br />RESERVED: engine-reported, not yet populated by the controller.<br /> |
| `LoadRejected` | AdapterStateLoadRejected means the bytes are present but the engine declined<br />to load the adapter. RESERVED: engine-reported, not yet populated.<br /> |


#### AIMArtifact



AIMArtifact is the Schema for the artifacts API



_Appears in:_
- [AIMArtifactList](#aimartifactlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMArtifact` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMArtifactSpec](#aimartifactspec)_ |  |  |  |
| `status` _[AIMArtifactStatus](#aimartifactstatus)_ |  |  |  |


#### AIMArtifactConfig



AIMArtifactConfig controls artifact-level defaults that are not appropriate for
individual services. These settings apply at namespace/cluster scope only.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `defaultRetentionPriority` _integer_ | DefaultRetentionPriority sets the default retention priority for AIMArtifacts<br />that do not specify one in their spec. When set, artifacts without an explicit<br />retentionPriority become eligible for automatic eviction at this priority level.<br />Lower values are evicted first. If not set, artifacts without an explicit<br />retentionPriority are never automatically evicted. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `modelDownloadImage` _string_ | ModelDownloadImage specifies the default container image for artifact<br />download and size-check jobs. Applies when an AIMArtifact does not set<br />spec.modelDownloadImage. When neither is set, the operator falls back<br />to its build-time default (matching the release version). |  | Optional: \{\} <br /> |


#### AIMArtifactList



AIMArtifactList contains a list of AIMArtifact





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMArtifactList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMArtifact](#aimartifact) array_ |  |  |  |


#### AIMArtifactMode

_Underlying type:_ _string_

AIMArtifactMode indicates the ownership mode of a artifact, derived from owner references.

_Validation:_
- Enum: [Dedicated Shared]

_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)

| Field | Description |
| --- | --- |
| `Dedicated` | ArtifactModeDedicated indicates the cache has owner references and will be<br />garbage collected when its owners are deleted.<br /> |
| `Shared` | ArtifactModeShared indicates the cache has no owner references and persists<br />independently, available for sharing across services.<br /> |


#### AIMArtifactSpec



AIMArtifactSpec defines the desired state of AIMArtifact



_Appears in:_
- [AIMArtifact](#aimartifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[AIMArtifactType](#aimartifacttype)_ | Type discriminates a base model artifact (`model`) from a LoRA adapter<br />definition (`adapter`). Defaults to `model`; immutable after creation.<br />Adapter artifacts require parentArtifact and modelId, and do not get their<br />own cache PVC. | model | Enum: [model adapter] <br />Optional: \{\} <br /> |
| `sourceUri` _string_ | SourceURI specifies the source location of the model to download.<br />Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage).<br />This field uniquely identifies the artifact and is immutable after creation.<br />Example: hf://meta-llama/Llama-3-8B |  | MinLength: 1 <br />Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br /> |
| `parentArtifact` _string_ | ParentArtifact names the base model AIMArtifact (type=model) this adapter is<br />compatible with. Required and only allowed when type=adapter; immutable.<br />The adapter is owned by (cascade-deleted with) the parent. Compatibility is<br />keyed on the parent's modelId. |  | Optional: \{\} <br /> |
| `rank` _integer_ | Rank is the LoRA rank of the adapter. Optional; only meaningful when type=adapter. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `adapterDisk` _[AIMAdapterDisk](#aimadapterdisk)_ | AdapterDisk, when set on a type=model artifact, provisions a shared ReadWriteMany<br />adapter disk owned by this model artifact and partitioned per consuming service.<br />Only allowed when type=model. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format.<br />Determines the cache download path: /workspace/cache/\{modelId\}<br />For HuggingFace sources, this is typically derived from the URI (e.g., "meta-llama/Llama-3-8B").<br />For S3 sources, this must be explicitly provided (e.g., "my-team/fine-tuned-llama").<br />When not specified, derived from SourceURI for HuggingFace sources. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Optional: \{\} <br /> |
| `storageClassName` _string_ | StorageClassName specifies the storage class for the cache volume.<br />When not specified, uses the cluster default storage class. |  | Optional: \{\} <br /> |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size specifies the size of the cache volume |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env lists the environment variables to use for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} <br /> |
| `modelDownloadImage` _string_ | ModelDownloadImage specifies the container image used to download and initialize the artifact.<br />This image runs as a job to download model artifacts from the source URI to the cache volume.<br />When not specified, the controller uses its built-in default (matching the release version). |  | Optional: \{\} <br /> |
| `downloadFilter` _[AIMDownloadFilter](#aimdownloadfilter)_ | DownloadFilter controls which files are included or excluded when downloading from HuggingFace.<br />Overrides any filter set in the runtime config's storage.downloadFilter.<br />When neither is set, subdirectory files are excluded by default (equivalent to exclude: ["*/*"]).<br />To download all files including subdirectories, set this to an empty object: downloadFilter: \{\}.<br />This field is immutable — to change the filter, recreate the artifact. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} <br /> |
| `retentionPriority` _integer_ | RetentionPriority marks this artifact as eligible for automatic eviction<br />when storage quota is exceeded. Lower values are evicted first.<br />Artifacts without this field are only evictable if a defaultRetentionPriority<br />is configured in the runtime config. Use the aim.eai.amd.com/eviction-protected<br />annotation to exempt an artifact from eviction entirely. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |


#### AIMArtifactStatus



AIMArtifactStatus defines the observed state of AIMArtifact



_Appears in:_
- [AIMArtifact](#aimartifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest available observations of the artifact's state |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current status of the artifact | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `progress` _[DownloadProgress](#downloadprogress)_ | Progress represents the download progress when Status is Progressing |  | Optional: \{\} <br /> |
| `download` _[DownloadState](#downloadstate)_ | Download represents the current download attempt state, patched by the downloader pod.<br />Shows which protocol is active, what attempt we're on, etc. |  | Optional: \{\} <br /> |
| `displaySize` _string_ | DisplaySize is the human-readable effective size (spec or discovered) |  | Optional: \{\} <br /> |
| `lastUsed` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastUsed represents the last time a model was deployed that used this cache |  |  |
| `persistentVolumeClaim` _string_ | PersistentVolumeClaim represents the name of the created PVC |  |  |
| `mode` _[AIMArtifactMode](#aimartifactmode)_ | Mode indicates the ownership mode of this artifact, derived from owner references.<br />- Dedicated: Has owner references, will be garbage collected when owners are deleted.<br />- Shared: No owner references, persists independently and can be shared. |  | Enum: [Dedicated Shared] <br />Optional: \{\} <br /> |
| `discoveredSizeBytes` _integer_ | DiscoveredSizeBytes is the model size discovered via check-size job.<br />Populated when spec.size is not provided. |  | Optional: \{\} <br /> |
| `allocatedSize` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | AllocatedSize is the actual PVC size requested (including headroom). |  | Optional: \{\} <br /> |
| `headroomPercent` _integer_ | HeadroomPercent is the headroom percentage that was applied to the PVC size. |  | Optional: \{\} <br /> |
| `resolvedSourceUri` _string_ | ResolvedSourceURI is the effective download source after cache resolution.<br />When the S3 artifact cache has a hit, this contains the rewritten s3:// URI.<br />When empty, spec.sourceUri is used directly. |  | Optional: \{\} <br /> |
| `adapterPersistentVolumeClaim` _string_ | AdapterPersistentVolumeClaim is the name of the shared adapter disk PVC<br />provisioned for a type=model artifact that declares an adapterDisk. Empty<br />otherwise. |  | Optional: \{\} <br /> |
| `adapterPath` _string_ | AdapterPath is the resolved on-disk directory name for a type=adapter artifact,<br />frozen at first resolution (defaults to metadata.name). This is the canonical<br />copy, mirrored into consuming services' status. |  | Optional: \{\} <br /> |
| `resolvedParent` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedParent captures the resolved parent model artifact for a type=adapter<br />artifact, including its UID. |  | Optional: \{\} <br /> |
| `parentModelId` _string_ | ParentModelID is the parent model artifact's modelId, denormalized onto the<br />adapter for convenience (refreshed each reconcile). |  | Optional: \{\} <br /> |


#### AIMArtifactStorageQuota



AIMArtifactStorageQuota configures storage limits for AIMArtifacts.
These settings are only available on AIMClusterRuntimeConfig (cluster-scoped)
because they enforce cluster-wide and cross-namespace policies.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `clusterLimit` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | ClusterLimit is the maximum total allocated storage for all AIMArtifacts cluster-wide.<br />When the sum of all artifact PVC sizes across all namespaces would exceed this limit,<br />new artifact PVCs are blocked until evictable artifacts are cleaned up or the limit is raised. |  | Optional: \{\} <br /> |
| `defaultNamespaceLimit` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | DefaultNamespaceLimit is the default maximum allocated storage for AIMArtifacts per namespace.<br />Can be overridden for individual namespaces via the aim.eai.amd.com/artifact-storage-quota annotation. |  | Optional: \{\} <br /> |


#### AIMArtifactType

_Underlying type:_ _string_

AIMArtifactType discriminates a model artifact from a LoRA adapter artifact.

_Validation:_
- Enum: [model adapter]

_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)

| Field | Description |
| --- | --- |
| `model` | ArtifactTypeModel is a base model artifact backed by a cache PVC. This is the<br />default and matches the behavior of artifacts created before adapters existed.<br /> |
| `adapter` | ArtifactTypeAdapter is a LoRA adapter definition. Adapter artifacts do not get<br />their own cache PVC; their bytes are staged per-consuming-service into the<br />parent model artifact's adapter disk.<br /> |


#### AIMCachingMode

_Underlying type:_ _string_

AIMCachingMode controls caching behavior for a service.
Canonical values are Dedicated and Shared.
Legacy values are accepted for backward compatibility:
- Always maps to Shared
- Auto maps to Shared
- Never maps to Dedicated

_Validation:_
- Enum: [Dedicated Shared Auto Always Never]

_Appears in:_
- [AIMServiceCachingConfig](#aimservicecachingconfig)

| Field | Description |
| --- | --- |
| `Dedicated` | CachingModeDedicated always creates service-owned dedicated caches/artifacts.<br /> |
| `Shared` | CachingModeShared reuses and creates shared caches/artifacts.<br /> |
| `Auto` | CachingModeAuto is deprecated legacy value that maps to Shared.<br /> |
| `Always` | CachingModeAlways is deprecated legacy value that maps to Shared.<br /> |
| `Never` | CachingModeNever is deprecated legacy value that maps to Dedicated.<br /> |


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
| `registry` _string_ | Registry to sync from (e.g., docker.io, ghcr.io, gcr.io).<br />Defaults to docker.io if not specified. | docker.io | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets contains references to secrets for authenticating to private registries.<br />Secrets must exist in the operator namespace (typically aim-system).<br />Used for both registry catalog listing and image metadata extraction. |  | Optional: \{\} <br /> |
| `filters` _[ModelSourceFilter](#modelsourcefilter) array_ | Filters define which images to discover and sync.<br />Each filter specifies an image selector with optional version constraints and exclusions.<br />Multiple filters are combined with OR logic (any match includes the image).<br />Use this field for advanced matching options. For simple explicit image lists, use Images instead. |  | MaxItems: 100 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `images` _string array_ | Images defines a simple explicit list of images to discover and sync.<br />Use this for straightforward static declarations without per-filter options.<br />Supported image formats:<br />- Repository with tag: "amdenterpriseai/aim-qwen-qwen3-32b:0.8.4"<br />- Repository without tag: "amdenterpriseai/aim-qwen-qwen3-32b" (uses Versions if set)<br />- Full URI with tag: "ghcr.io/silogen/aim-llama:1.0.0"<br />Must not be set together with Filters. |  | MaxItems: 100 <br />MinItems: 1 <br />Optional: \{\} <br /> |
| `syncInterval` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#duration-v1-meta)_ | SyncInterval defines how often to sync with the registry.<br />Defaults to 1h. Minimum recommended interval is 15m to avoid rate limiting.<br />Format: duration string (e.g., "30m", "1h", "2h30m"). | 1h | Optional: \{\} <br /> |
| `versions` _string array_ | Versions specifies global semantic version constraints applied to all filters.<br />Individual filters can override this with their own version constraints.<br />Constraints use semver syntax: >=1.0.0, <2.0.0, ~1.2.0, ^1.0.0, etc.<br />Non-semver tags (e.g., "latest", "dev") are silently skipped.<br />Version ranges work on all registries (including ghcr.io, gcr.io) when combined with<br />exact repository names (no wildcards). The controller uses the Tags List API to fetch<br />all tags for the repository and filters them by the semver constraint.<br />Example: registry=ghcr.io, filters=[\{image: "silogen/aim-llama"\}], versions=[">=1.0.0"]<br />will fetch all tags from ghcr.io/silogen/aim-llama and include only those >=1.0.0. |  | Optional: \{\} <br /> |
| `maxModels` _integer_ | MaxModels is the maximum number of AIMClusterModel resources to create from this source.<br />Once this limit is reached, no new models will be created, even if more matching images are discovered.<br />Existing models are never deleted.<br />This prevents runaway model creation from overly broad filters. | 100 | Maximum: 10000 <br />Minimum: 1 <br />Optional: \{\} <br /> |


#### AIMClusterModelSourceStatus



AIMClusterModelSourceStatus defines the observed state of AIMClusterModelSource.



_Appears in:_
- [AIMClusterModelSource](#aimclustermodelsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `status` _string_ | Status represents the overall state of the model source. |  | Enum: [Pending Starting Progressing Ready Running Degraded NotAvailable Failed] <br />Optional: \{\} <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastSyncTime is the timestamp of the last successful registry sync.<br />Updated after each successful sync operation. |  | Optional: \{\} <br /> |
| `discoveredModels` _integer_ | DiscoveredModels is the count of AIMClusterModel resources managed by this source.<br />Includes both existing and newly created models. |  | Optional: \{\} <br /> |
| `availableModels` _integer_ | AvailableModels is the total count of images discovered in the registry that match the filters.<br />This may be higher than DiscoveredModels if maxModels limit was reached. |  | Optional: \{\} <br /> |
| `modelsLimitReached` _boolean_ | ModelsLimitReached indicates whether the maxModels limit has been reached.<br />When true, no new models will be created even if more matching images are discovered. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest available observations of the source's state.<br />Standard conditions: Ready, Syncing, RegistryReachable. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently observed spec. |  | Optional: \{\} <br /> |


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
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} <br /> |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | ArtifactCache configures the S3-backed artifact cache for HuggingFace models.<br />When enabled, the controller checks internal S3 before downloading from HuggingFace. |  | Optional: \{\} <br /> |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  | Optional: \{\} <br /> |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |
| `artifactStorageQuota` _[AIMArtifactStorageQuota](#aimartifactstoragequota)_ | ArtifactStorageQuota configures storage limits for AIMArtifacts.<br />These limits control how much total PVC storage artifacts may consume,<br />both cluster-wide and per-namespace. |  | Optional: \{\} <br /> |


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
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica.<br />For GPU models, defines the GPU count and model types required for deployment.<br />For CPU-only models, defines CPU resource requirements.<br />This field is immutable after creation. |  | Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B").<br />Required when customProfile is set; used to assemble the profile YAML aim_id field<br />and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8").<br />Required when customProfile is set; used for profile YAML model_id field<br />and for weight pre-caching via the discovery job. |  | Optional: \{\} <br /> |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine.<br />When set, the controller assembles a profile YAML from this data and template metadata,<br />creates a ConfigMap, and mounts it into discovery and inference containers.<br />Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  | Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />These variables are passed to the inference runtime and can be used<br />to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} <br /> |


#### AIMCpuRequirements



AIMCpuRequirements specifies CPU resource requirements.



_Appears in:_
- [AIMHardwareRequirements](#aimhardwarerequirements)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Requests is the number of CPU cores to request. Required and must be > 0. |  | Required: \{\} <br /> |
| `limits` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Limits is the maximum number of CPU cores to allow. |  | Optional: \{\} <br /> |


#### AIMCustomModelSpec



AIMCustomModelSpec contains configuration for custom models.
These fields are only used when modelSources is specified (custom models).
For image-based models, these settings come from discovery.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies default hardware requirements for all templates.<br />Individual templates can override these defaults.<br />Required when modelSources is set and customTemplates is empty (unless aimId is set). |  | Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies default type for all templates.<br />Individual templates can override this default.<br />When nil, templates default to "unoptimized". |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `versionPolicy` _[AIMVersionPolicy](#aimversionpolicy)_ | VersionPolicy controls how template versions are filtered during aimId-based matching.<br />- pinned (default): match templates whose status.version equals the model's image tag<br />- latest: match only templates at the newest available status.version<br />- all: match templates at any version<br />- any: deprecated alias of all, kept for backward compatibility<br />Only used when spec.aimId is set. | pinned | Enum: [pinned latest any all] <br />Optional: \{\} <br /> |


#### AIMCustomProfile



AIMCustomProfile defines inline custom profile data for user-provided inference engine configuration.
When set on a template, the controller assembles a profile YAML, creates a ConfigMap,
and mounts it into both the discovery job and the inference service container.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine arguments as a free-form JSON object.<br />These are passed as CLI arguments to the inference engine (e.g., vLLM).<br />Do not include "model" — it is injected separately by the runtime. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `envVars` _object (keys:string, values:string)_ | EnvVars contains environment variables applied to the inference engine process.<br />These are written into the profile YAML and applied by the AIM runtime via os.execv,<br />distinct from container-level Env which targets the AIM runtime container itself.<br />Keys must match ^[A-Z0-9_]+$ (uppercase with underscores). |  | Optional: \{\} <br /> |


#### AIMCustomTemplate



AIMCustomTemplate defines a custom template configuration for a model.
When modelSources are specified directly on AIMModel, customTemplates allow
defining explicit hardware requirements and profiles, skipping the discovery job.
This is an existing struct (not a CRD); it appears as an element of AIMModel.spec.customTemplates[].



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the template name. If not provided, auto-generated from model name + profile. |  | MaxLength: 63 <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization status of this template.<br />- optimized: Template has been tuned for performance<br />- general: General-purpose tuning between optimized and preview<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied | unoptimized | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variable overrides when this template is selected.<br />These are container-level env vars applied to the AIM runtime container. |  | MaxItems: 64 <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for this template.<br />Optional when spec.hardware is set (inherits from spec).<br />When both are set, values are merged field-by-field with template taking precedence. |  | Optional: \{\} <br /> |
| `profile` _[AIMTemplateProfile](#aimtemplateprofile)_ | Profile declares runtime profile variables for template selection.<br />Used when multiple templates exist to select based on metric/precision. |  | Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B").<br />Required when customProfile is set. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8").<br />Required when customProfile is set. |  | Optional: \{\} <br /> |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine.<br />When set, the resulting template will have a custom profile ConfigMap mounted.<br />Requires aimId, modelId, hardware, profile.metric, and profile.precision. |  | Optional: \{\} <br /> |


#### AIMDiscoveredProfile



AIMDiscoveredProfile contains the cached discovery results for a template.
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
| `env_vars` _object (keys:string, values:string)_ | EnvVars contains environment variables required by the runtime for this profile.<br />These may include engine-specific settings, optimization flags, or hardware configuration. |  | Optional: \{\} <br /> |
| `metadata` _[AIMProfileMetadata](#aimprofilemetadata)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `originalDiscoveryOutput` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | OriginalDiscoveryOutput contains the raw discovery job JSON output.<br />This preserves the complete discovery result from the dry-run container,<br />including all fields that may not be mapped to structured fields above. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |




#### AIMDiscoveryProfileMetadata



AIMDiscoveryProfileMetadata describes the characteristics of a discovered deployment profile.



_Appears in:_
- [AIMDiscoveryProfile](#aimdiscoveryprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  | Optional: \{\} <br /> |
| `gpu_count` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies the optimization level of this profile (optimized, unoptimized, preview). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |


#### AIMDownloadFilter



AIMDownloadFilter controls which files are included or excluded during artifact downloads.
Patterns use fnmatch-style glob syntax applied against relative file paths in the repository.
Both the size estimator and downloader apply the same filter, ensuring PVC sizing matches the actual download.

Filter order (matching huggingface_hub behavior):
 1. Include: if set, only files matching at least one include pattern are considered
 2. Exclude: files matching any exclude pattern are then removed

When no filter is configured (neither on the artifact nor in the runtime config),
subdirectory files are excluded by default (equivalent to exclude: ["*/*"]).
To download all files including subdirectories, set an empty filter: downloadFilter: {}.



_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)
- [AIMStorageConfig](#aimstorageconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `include` _string array_ | Include specifies glob patterns for files to download.<br />Only files matching at least one pattern are considered.<br />If empty, all files pass the include check.<br />Patterns use fnmatch syntax (e.g., ["*.safetensors", "config.json"]). |  | Optional: \{\} <br /> |
| `exclude` _string array_ | Exclude specifies glob patterns for files to skip.<br />Files matching any exclude pattern are removed after include filtering.<br />Patterns use fnmatch syntax (e.g., ["*/*", "*.bin"]).<br />Use ["*/*"] to exclude all files in subdirectories (the default when no filter is set). |  | Optional: \{\} <br /> |


#### AIMGpuRequirements



AIMGpuRequirements specifies GPU resource requirements.



_Appears in:_
- [AIMHardwareRequirements](#aimhardwarerequirements)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _integer_ | Requests is the number of GPUs to set as requests/limits.<br />Set to 0 to target GPU nodes without consuming GPU resources (useful for testing). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `model` _string_ | Model limits deployment to a specific GPU model.<br />Example: "MI300X"<br />Cannot be combined with minVram. |  | MaxLength: 64 <br />Optional: \{\} <br /> |
| `minVram` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | MinVRAM limits deployment to GPUs having at least this much VRAM.<br />Used for capacity planning when the model size is known but any GPU with<br />sufficient VRAM is acceptable.<br />Cannot be combined with model. |  | Optional: \{\} <br /> |
| `resourceName` _string_ | ResourceName is the Kubernetes resource name for GPU resources.<br />Defaults to "amd.com/gpu" if not specified. | amd.com/gpu | Optional: \{\} <br /> |


#### AIMHardwareRequirements



AIMHardwareRequirements specifies compute resource requirements for custom models.
Used in AIMModelSpec and AIMCustomTemplate to define GPU and CPU needs.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomModelSpec](#aimcustommodelspec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMRuntimeParameters](#aimruntimeparameters)
- [AIMServiceModelCustom](#aimservicemodelcustom)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | GPU specifies GPU requirements. If not set, no GPUs are requested (CPU-only model). |  | Optional: \{\} <br /> |
| `cpu` _[AIMCpuRequirements](#aimcpurequirements)_ | CPU specifies CPU requirements. |  | Optional: \{\} <br /> |


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
- [ProfileHardwareGroupEntry](#profilehardwaregroupentry)
- [ProfileSelector](#profileselector)

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


#### AIMModelConfig







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `autoDiscovery` _boolean_ | AutoDiscovery controls whether models run discovery by default.<br />When true, models run discovery jobs to extract metadata and auto-create templates.<br />When false, discovery is skipped. Discovery failures are non-fatal and reported via conditions. |  | Optional: \{\} <br /> |


#### AIMModelDiscoveryConfig



AIMModelDiscoveryConfig controls discovery behavior for a model.

The bool fields are pointers so the schema can distinguish "unset" from
explicit false. With a plain bool + omitempty + default=true, the API
server's OpenAPI defaulter cannot tell an explicit false apart from a
missing field (both look like the Go zero value) and silently rewrites
the explicit false to true. Pointers preserve the user's intent.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `extractMetadata` _boolean_ | ExtractMetadata controls whether metadata extraction runs for this model.<br />During metadata extraction, the controller connects to the image registry and<br />extracts the image's labels. | true | Optional: \{\} <br /> |
| `createServiceTemplates` _boolean_ | CreateServiceTemplates controls whether (cluster) service templates are auto-created from the image metadata. | true | Optional: \{\} <br /> |


#### AIMModelKind

_Underlying type:_ _string_

AIMModelKind classifies the v1alpha2 AIMModel onboarding flow that
produced this model's profiles. Populated by the v1alpha2 controller
during reconciliation from the model's spec shape.

The three kinds correspond 1:1 to the "three flows" documented in
concepts/models.md:

  - Image    — spec.image is set; profiles come from in-cluster image
    discovery on that image. Covers both AMD-published official AIMs
    and any private image with profile YAMLs baked in (including
    base images used as source material for Custom-kind models).
  - Derived  — spec.profiles.derivedFrom with selector.role unset or
    "deployable"; profiles are re-derived from another deployable
    model's profiles (e.g. fine-tunes that swap weights but keep
    the original model's architecture, runtime, and accelerator
    shapes).
  - Custom   — spec.profiles.derivedFrom with selector.role=base;
    profiles are derived by overlaying BYO weights + target identity
    onto a base image's generic base profiles.

Empty when the spec hasn't been classified yet (controller hasn't
reconciled) or when the spec shape doesn't match any of the three
flows (a misconfigured spec the CEL validators didn't catch).

_Validation:_
- Enum: [Image Derived Custom]

_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description |
| --- | --- |
| `Image` |  |
| `Derived` |  |
| `Custom` |  |


#### AIMModelList



AIMModelList contains a list of AIMModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMModel](#aimmodel) array_ |  |  |  |


#### AIMModelProfilesDerivedFrom



AIMModelProfilesDerivedFrom describes the source half of a profile-
derivation request: which existing profiles (or discovery cache) the
reconciler should copy from.



_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `selector` _[ProfileSelector](#profileselector)_ | Selector chooses which source profiles to derive from. Discovery of a<br />deployable AIM image stamps role=deployable; selector.role=base targets<br />the base profiles emitted by base-image discovery (custom-model<br />derivation source material). |  | Optional: \{\} <br /> |
| `sourceRef` _[ProfileSourceRef](#profilesourceref)_ | SourceRef points to an alternate discovery cache source (a<br />pre-populated ConfigMap of profile YAMLs) instead of using the<br />visible AIMProfile / AIMClusterProfile objects. |  | Optional: \{\} <br /> |


#### AIMModelProfilesSpec



AIMModelProfilesSpec is the v1alpha2 AIMModel onboarding surface for
profile-derivation flows. It groups the source descriptor, version filter,
and overrides under one block so the spec reads "the model's profiles,
derived from <source>, with <overrides> applied".

The reconciler translates this block into a child AIMProfileSet:
  - DerivedFrom.Selector / DerivedFrom.SourceRef → child AIMProfileSet
    spec.selector / spec.sourceRef.
  - VersionPolicy / Version → child spec.versionPolicy / spec.version.
  - Overrides → child spec.overrides (Image included via overrides.image).



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `derivedFrom` _[AIMModelProfilesDerivedFrom](#aimmodelprofilesderivedfrom)_ | DerivedFrom identifies the source profiles to copy from. |  |  |
| `versionPolicy` _[ProfileVersionPolicy](#profileversionpolicy)_ | VersionPolicy controls how matching profiles are filtered by version. | pinned | Enum: [pinned latest all] <br />Optional: \{\} <br /> |
| `version` _string_ | Version pins matching to a specific source profile version when<br />VersionPolicy is `pinned`. |  | Optional: \{\} <br /> |
| `overrides` _[ProfileOverrides](#profileoverrides)_ | Overrides mutates the copied profile spec after selection and version<br />filtering. Use overrides.image to override the deployment container<br />image used by the derived profiles. |  | Optional: \{\} <br /> |


#### AIMModelSource



AIMModelSource describes a model artifact that must be downloaded for inference.
Discovery extracts these from the container's configuration to enable caching and validation.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMModelSpec](#aimmodelspec)
- [AIMServiceModelCustom](#aimservicemodelcustom)
- [AIMServiceProfileOverrides](#aimserviceprofileoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)
- [AIMTemplateCacheSpec](#aimtemplatecachespec)
- [ProfileOverrides](#profileoverrides)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format.<br />Determines the cache mount path: /workspace/cache/\{modelId\}<br />For HuggingFace sources, this typically mirrors the URI path (e.g., meta-llama/Llama-3-8B).<br />For S3 sources, users define their own organizational structure. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Required: \{\} <br /> |
| `sourceUri` _string_ | SourceURI is the location from which the model should be downloaded.<br />Supported schemes:<br />- hf://org/model - Hugging Face Hub model<br />- s3://bucket/key - S3-compatible storage |  | Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br /> |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size is the expected storage space required for this model artifact.<br />Used for PVC sizing and capacity planning during cache creation.<br />Optional - if not specified, the download job will discover the size automatically.<br />Can be set explicitly to pre-allocate storage or override auto-discovery. |  | Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision describes the runtime precision this source is compatible with.<br />Used to match model sources to profiles during custom weight onboarding. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies per-source credential overrides.<br />These variables are used for authentication when downloading this specific source.<br />Takes precedence over base-level env for the same variable name. |  | Optional: \{\} <br /> |


#### AIMModelSourceType

_Underlying type:_ _string_

AIMModelSourceType indicates how a model's artifacts are sourced.

Only set by the v1alpha1 controller, which lumps fine-tunes and custom
models together as "Custom" — losing the distinction users actually
care about. The v1alpha2 controller intentionally does not populate
this field on v1alpha2-shaped specs; v1alpha2 consumers should read
AIMModelStatus.Kind instead, which is a three-way classifier
(Image / Derived / Custom).

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

Per-version constraints (v1alpha1 forbids derivedFrom and profiles;
v1alpha2 forbids profileCopy/custom/customTemplates/top-level modelSources,
requires image XOR profiles, and forbids profiles mixed with legacy scalar
fields) live on the version-specific AIMModel / AIMClusterModel root types
in the per-version *_types.go files.



_Appears in:_
- [AIMClusterModel](#aimclustermodel)
- [AIMModel](#aimmodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the container image URI for this AIM model.<br />This image is inspected by the operator to select runtime profiles used by templates.<br />Discovery behavior is controlled by the discovery field and runtime config's AutoDiscovery setting.<br />Required unless aimId is set with versionPolicy latest or any, or profileCopy is set. |  | Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "qwen/qwen3-32b").<br />When set together with modelSources, enables aimId-based template matching:<br />the controller finds official templates by aimId, filters by versionPolicy,<br />matches by modelId, and creates copies with the custom weight source. |  | Optional: \{\} <br /> |
| `profileCopy` _[AIMProfileSetSpec](#aimprofilesetspec)_ | ProfileCopy reuses the AIMProfileSet derivation shape so an AIMModel can<br />publish derivative AIMProfiles directly. The controller may synthesize a<br />child AIMProfileSet and fill SourceRef when image-backed discovery is<br />involved. Mutually exclusive with all deprecated v1alpha1 fields.<br />DEPRECATED on v1alpha2: use spec.profiles. v1alpha1 still accepts<br />ProfileCopy. v1alpha2 CRD CEL forbids ProfileCopy. |  | Optional: \{\} <br /> |
| `derivedFrom` _[AIMProfileSetSpec](#aimprofilesetspec)_ | DerivedFrom is the legacy flat shape of v1alpha2's profile-derivation<br />onboarding surface. New objects must use spec.profiles instead; the<br />field is retained so existing v1alpha2 objects (and the v1alpha2<br />reconciler reading them) round-trip cleanly.<br />DEPRECATED: prefer spec.profiles.derivedFrom on v1alpha2. |  | Optional: \{\} <br /> |
| `profiles` _[AIMModelProfilesSpec](#aimmodelprofilesspec)_ | Profiles is the v1alpha2 fine-tune / custom-model onboarding surface.<br />It groups the source descriptor (`profiles.derivedFrom.selector` /<br />`profiles.derivedFrom.sourceRef`), the version filter<br />(`profiles.versionPolicy` / `profiles.version`), and the modifications<br />applied to copies (`profiles.overrides`). When set, the AIMModel<br />reconciler synthesises a child AIMProfileSet from this block.<br />Mutually exclusive with spec.image (exactly one of the two is required<br />for v1alpha2 AIMModel). v1alpha1 rejects spec.profiles via per-version<br />CEL. |  | Optional: \{\} <br /> |
| `discovery` _[AIMModelDiscoveryConfig](#aimmodeldiscoveryconfig)_ | Discovery controls discovery behavior for this model.<br />When unset, uses runtime config defaults. |  | Optional: \{\} <br /> |
| `defaultServiceTemplate` _string_ | DefaultServiceTemplate specifies the default AIMServiceTemplate to use when creating services for this model.<br />When set, services that reference this model will use this template if no template is explicitly specified.<br />If this is not set, a template will be automatically selected. |  | Optional: \{\} <br /> |
| `custom` _[AIMCustomModelSpec](#aimcustommodelspec)_ | Custom contains configuration for custom models (models with inline modelSources).<br />Only used when modelSources are specified; ignored for image-based models. |  | Optional: \{\} <br /> |
| `customTemplates` _[AIMCustomTemplate](#aimcustomtemplate) array_ | CustomTemplates defines explicit template configurations for this model.<br />These templates are created directly without running a discovery job.<br />Can be used with or without modelSources to define custom deployment configurations.<br />If omitted when modelSources is set, a single template is auto-generated<br />using the custom.hardware requirements. |  | MaxItems: 16 <br />Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use for this model.<br />When specified, these sources are used instead of auto-discovery from the container image.<br />This enables pre-creating custom models with explicit model sources.<br />The size field is optional - if not specified, it will be discovered by the download job.<br />AIM runtime currently supports only one model source. |  | MaxItems: 1 <br />Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling the model container image.<br />These secrets are used for:<br />- OCI registry metadata extraction during discovery<br />- Pulling the image for inference services<br />The secrets are merged with any runtime config defaults.<br />For namespace-scoped models, secrets must exist in the same namespace.<br />For cluster-scoped models, secrets must exist in the operator namespace. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for authentication during model discovery and metadata extraction.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model.<br />This includes metadata extraction jobs and any other model-related operations.<br />If empty, the default service account for the namespace is used. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default resource requirements for services using this model.<br />Template- or service-level values override these defaults. |  | Optional: \{\} <br /> |
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
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} <br /> |
| `imageMetadata` _[ImageMetadata](#imagemetadata)_ | ImageMetadata is the metadata extracted from an AIM image |  | Optional: \{\} <br /> |
| `sourceType` _[AIMModelSourceType](#aimmodelsourcetype)_ | SourceType indicates how this model's artifacts are sourced.<br />- "Image": Model discovered from container image labels<br />- "Custom": Model uses explicit spec.modelSources<br />Set by the controller based on whether spec.modelSources is populated.<br />Note: only populated by the v1alpha1 controller; v1alpha2 consumers<br />should read .status.kind instead, which distinguishes fine-tunes<br />(Derived) from BYO base-image overlays (Custom). |  | Enum: [Image Custom] <br />Optional: \{\} <br /> |
| `kind` _[AIMModelKind](#aimmodelkind)_ | Kind classifies the v1alpha2 onboarding flow that produced this<br />model's profiles (Image / Derived / Custom). See AIMModelKind for<br />the per-value semantics. Populated by the v1alpha2 controller from<br />the spec shape; left empty by the v1alpha1 controller. |  | Enum: [Image Derived Custom] <br />Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the resolved model architecture identifier for this model.<br />Populated by the v1alpha2 controller from spec.aimId or discovered metadata. |  | Optional: \{\} <br /> |
| `baseImage` _string_ | BaseImage is the extracted AIM base image reference (AIM_BASE_IMAGE_REF) when known.<br />Used when resolving deployment images for fine-tuned models that have no spec.image. |  | Optional: \{\} <br /> |
| `version` _string_ | Version is the effective image version of the model. Populated from<br />the spec.image tag (`amdenterpriseai/aim-qwen-qwen3-32b:0.11.0` →<br />`0.11.0`) during reconciliation. Empty for models that have no<br />spec.image (e.g. fine-tuned models derived from a parent) or that<br />reference an image by digest.<br />This mirrors AIMProfileStatus.Version so the two surfaces stay in<br />lock-step: a single image-tag extraction rule governs what users<br />see in the kubectl printcolumn for both kinds.<br />The image-author-declared version (i.e. the<br />`org.opencontainers.image.version` OCI label) is preserved separately<br />under .status.imageMetadata.oci.version for users that want to inspect<br />what the image build pipeline stamped. The two values usually agree;<br />when the OCI label is missing or empty, this field still surfaces a<br />useful version from the tag itself. |  | Optional: \{\} <br /> |
| `discoveryCacheRef` _[DiscoveryCacheReference](#discoverycachereference)_ | DiscoveryCacheRef points at the normalized discovery cache ConfigMap for image-backed flows.<br />Populated by the v1alpha2 controller after image inspection succeeds. |  | Optional: \{\} <br /> |
| `discoveredProfiles` _[DiscoveredProfileCounts](#discoveredprofilecounts)_ | DiscoveredProfiles summarizes the profiles found during image discovery. |  | Optional: \{\} <br /> |
| `profileSetRef` _[ProfileSetReference](#profilesetreference)_ | ProfileSetRef identifies the child profile set synthesized for derivation flows. |  | Optional: \{\} <br /> |
| `managedProfiles` _[ManagedProfileCounts](#managedprofilecounts)_ | ManagedProfiles summarizes the direct promoted or derived profiles owned or managed by this model. |  | Optional: \{\} <br /> |
| `discovery` _[ModelDiscoveryState](#modeldiscoverystate)_ | Discovery tracks the state of the image-discovery Job used to populate the discovery cache. |  | Optional: \{\} <br /> |


#### AIMPrecision

_Underlying type:_ _string_

AIMPrecision enumerates supported numeric precisions

_Validation:_
- Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMModelSource](#aimmodelsource)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMRuntimeParameters](#aimruntimeparameters)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMTemplateProfile](#aimtemplateprofile)
- [ProfileHardwareGroupEntry](#profilehardwaregroupentry)
- [ProfileSelector](#profileselector)

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


#### AIMProfileMetadata



AIMProfileMetadata describes the characteristics of a cached deployment profile.
This is identical to AIMDiscoveryProfileMetadata but exists in the template status namespace.



_Appears in:_
- [AIMDiscoveredProfile](#aimdiscoveredprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimID is the model-family identifier from the profile YAML's top-level `aim_id`.<br />Used for cross-scope fine-tune template matching (AIMModel.spec.aimId -> template.spec.aimId). |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelID is the per-profile model variant identifier from the profile YAML's top-level `model_id`. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  | Optional: \{\} <br /> |
| `gpuCount` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this profile (optimized, preview, unoptimized). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |


#### AIMProfileSetSpec



AIMProfileSetSpec defines the desired state of AIMProfileSet and is also reused by AIMModel.profileCopy.

The selector-non-empty rule accepts every documented narrowing field
(including modelRef and origin) and lets sourceRef-only specs through:
when sourceRef is set, the discovery cache itself acts as the source
scope and a separate selector is not required.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceRef` _[ProfileSourceRef](#profilesourceref)_ | SourceRef points to an alternate discovery cache source.<br />When omitted, derivation uses visible AIMProfile and AIMClusterProfile objects.<br />For AIMProfileSet, the referenced ConfigMap is read from the same namespace.<br />For AIMClusterProfileSet, it is read from the operator namespace. |  | Optional: \{\} <br /> |
| `selector` _[ProfileSelector](#profileselector)_ | Selector chooses which source profiles to derive from. |  | Optional: \{\} <br /> |
| `versionPolicy` _[ProfileVersionPolicy](#profileversionpolicy)_ | VersionPolicy controls how matching profiles are filtered by version. | pinned | Enum: [pinned latest all] <br />Optional: \{\} <br /> |
| `version` _string_ | Version pins matching to a specific source profile version when VersionPolicy is pinned. |  | Optional: \{\} <br /> |
| `image` _string_ | Image overrides the runtime image used by the derived profiles. |  | Optional: \{\} <br /> |
| `overrides` _[ProfileOverrides](#profileoverrides)_ | Overrides mutates the copied profile spec after selection and version filtering. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets used for inspecting and pulling container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName is propagated to managed profiles for downstream workloads. |  | Optional: \{\} <br /> |




#### AIMProfileType

_Underlying type:_ _string_

AIMProfileType indicates the optimization level of a deployment profile.
Hierarchy: optimized > general > preview > unoptimized.

_Validation:_
- Enum: [optimized general preview unoptimized]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomModelSpec](#aimcustommodelspec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileTypeOptimized indicates the profile has been fully optimized.<br /> |
| `general` | AIMProfileTypeGeneral indicates a general-purpose profile (between optimized and preview).<br /> |
| `preview` | AIMProfileTypePreview indicates the profile is in preview/beta state.<br /> |
| `unoptimized` | AIMProfileTypeUnoptimized indicates the profile has not been optimized.<br /> |
| `any` | AIMProfileTypeAny is a selector-only sentinel meaning "no optimization<br />floor — accept every tier". It is never stamped on a profile's own<br />spec.type; it is only valid as a ProfileSelector.minimumType value, where<br />it disables the floor (equivalent to flooring at unoptimized, the lowest<br />tier, but reads as intent rather than asking specifically for unoptimized).<br /> |


#### AIMProfileTypeFloor

_Underlying type:_ _string_

AIMProfileTypeFloor enumerates the values accepted by a profile selector's
minimumType floor: the real optimization tiers plus the selector-only "any"
sentinel that disables the floor.

It is a distinct type from AIMProfileType on purpose. MinimumType cannot just
be an AIMProfileType: that type's own enum constrains a profile's spec.type to
the four real tiers (a profile is never "any"), and a field whose type already
declares an enum cannot widen it — controller-gen emits an allOf of the two
enums, whose intersection silently drops "any" and makes the documented
sentinel un-settable. A separate type carries the correct 5-value enum for the
floor while leaving spec.type constrained to real tiers.

_Validation:_
- Enum: [optimized general preview unoptimized any]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileTypeFloorOptimized floors selection at optimized (the default<br />for AIMService auto-selection).<br /> |
| `general` | AIMProfileTypeFloorGeneral floors selection at general.<br /> |
| `preview` | AIMProfileTypeFloorPreview floors selection at preview.<br /> |
| `unoptimized` | AIMProfileTypeFloorUnoptimized floors selection at unoptimized (the<br />lowest real tier — admits every typed profile).<br /> |
| `any` | AIMProfileTypeFloorAny disables the floor entirely (accept every tier,<br />including untyped/unknown).<br /> |


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


#### AIMResolvedArtifact







_Appears in:_
- [AIMTemplateCacheStatus](#aimtemplatecachestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `uid` _string_ | UID of the AIMArtifact resource |  |  |
| `name` _string_ | Name of the AIMArtifact resource |  |  |
| `model` _string_ | Model is the name of the model that is cached |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status of the artifact |  |  |
| `persistentVolumeClaim` _string_ | PersistentVolumeClaim name if available |  |  |
| `mountPoint` _string_ | MountPoint is the mount point for the artifact |  |  |


#### AIMResolvedReference



AIMResolvedReference captures metadata about a resolved reference.



_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)
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
| `kind` _string_ | Kind is the fully-qualified kind of the resolved reference, when known. |  | Optional: \{\} <br /> |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#uid-types-pkg)_ | UID captures the unique identifier of the resolved reference, when known. |  | Optional: \{\} <br /> |


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
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} <br /> |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | ArtifactCache configures the S3-backed artifact cache for HuggingFace models.<br />When enabled, the controller checks internal S3 before downloading from HuggingFace. |  | Optional: \{\} <br /> |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  | Optional: \{\} <br /> |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |


#### AIMRuntimeConfigLabelPropagationSpec







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled, if true, allows propagating parent labels to all child resources it creates directly<br />Only label keys that match the ones in Match are propagated. | false | Optional: \{\} <br /> |
| `match` _string array_ | Match is a list of label keys that will be propagated to any child resources created.<br />Wildcards are supported, so for example `org.my/my-key-*` would match any label with that prefix. |  | Optional: \{\} <br /> |


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
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} <br /> |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy.<br />This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} <br /> |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | ArtifactCache configures the S3-backed artifact cache for HuggingFace models.<br />When enabled, the controller checks internal S3 before downloading from HuggingFace. |  | Optional: \{\} <br /> |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources.<br />When enabled, labels matching the specified patterns are automatically copied from parent resources<br />(e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).<br />This is useful for propagating organizational metadata like cost centers, team identifiers,<br />or compliance labels through the resource hierarchy. |  | Optional: \{\} <br /> |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.<br />For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,<br />the value will be automatically migrated. |  | Optional: \{\} <br /> |


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
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica.<br />For GPU models, defines the GPU count and model types required for deployment.<br />For CPU-only models, defines CPU resource requirements.<br />This field is immutable after creation. |  | Optional: \{\} <br /> |


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
| `enabled` _boolean_ | Enabled controls whether HTTP routing is managed for inference services using this config.<br />When true, the operator creates HTTPRoute resources for services that reference this config.<br />When false or unset, routing must be explicitly enabled on each service.<br />This provides a namespace or cluster-wide default that individual services can override. |  | Optional: \{\} <br /> |
| `gatewayRef` _[ParentReference](#parentreference)_ | GatewayRef specifies the Gateway API Gateway resource that should receive HTTPRoutes.<br />This identifies the parent gateway for routing traffic to inference services.<br />The gateway can be in any namespace (cross-namespace references are supported).<br />If routing is enabled but GatewayRef is not specified, service reconciliation will fail<br />with a validation error. |  | Optional: \{\} <br /> |
| `hostnames` _Hostname array_ | Hostnames pins generated HTTPRoutes to these hostnames so a route only<br />attaches to the matching Gateway listener instead of every listener on<br />the parent gateway. Without a hostname, an HTTPRoute matches all of the<br />parent gateway's listener hostnames, which can expose a service on<br />listeners that do not enforce the intended authentication.<br />This field is required when the parent gateway exposes more than one<br />listener: in that case a service with routing enabled but no hostnames<br />configured will not get an HTTPRoute and reports ConfigValid=False with<br />reason RouteHostnameRequired. When the parent gateway has a single<br />listener, leaving this empty preserves the existing behavior (the route<br />inherits that listener's hostnames).<br />Individual services can override this list via spec.routing.hostnames. |  | Optional: \{\} <br /> |
| `pathTemplate` _string_ | PathTemplate defines the HTTP path template for routes, evaluated using JSONPath expressions.<br />The template is rendered against the AIMService object to generate unique paths.<br />Example templates:<br />- `/\{.metadata.namespace\}/\{.metadata.name\}` - namespace and service name<br />- `/\{.metadata.namespace\}/\{.metadata.labels['team']\}/inference` - with label<br />- `/models/\{.metadata.name\}` - based on service name<br />The template must:<br />- Use valid JSONPath expressions wrapped in \{...\}<br />- Reference fields that exist on the service<br />- Produce a path ≤ 200 characters after rendering<br />- Result in valid URL path segments (lowercase, RFC 1123 compliant)<br />If evaluation fails, the service enters Degraded state with PathTemplateInvalid reason.<br />Individual services can override this template via spec.routing.pathTemplate. |  | Optional: \{\} <br /> |
| `requestTimeout` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#duration-v1-meta)_ | RequestTimeout defines the HTTP request timeout for routes.<br />This sets the maximum duration for a request to complete before timing out.<br />The timeout applies to the entire request/response cycle.<br />If not specified, no timeout is set on the route.<br />Individual services can override this value via spec.routing.requestTimeout. |  | Optional: \{\} <br /> |
| `annotations` _object (keys:string, values:string)_ | Annotations defines default annotations to add to all HTTPRoute resources.<br />Services can add additional annotations or override these via spec.routing.annotations.<br />When both are specified, service annotations take precedence for conflicting keys.<br />Common use cases include ingress controller settings, rate limiting, monitoring labels,<br />and security policies that should apply to all services using this config. |  | Optional: \{\} <br /> |


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


#### AIMServiceAdapterKind

_Underlying type:_ _string_

AIMServiceAdapterKind enumerates the kinds an adapter reference may target.
Restricted to AIMArtifact in v1; reserved to admit a future AIMAdapter catalog kind.

_Validation:_
- Enum: [AIMArtifact]

_Appears in:_
- [AIMServiceAdapterReference](#aimserviceadapterreference)

| Field | Description |
| --- | --- |
| `AIMArtifact` | AdapterKindAIMArtifact references an AIMArtifact (type=adapter).<br /> |


#### AIMServiceAdapterReference



AIMServiceAdapterReference is a typed reference to a LoRA adapter served by this
service. Today it is resolved as a pure reference to an existing adapter
artifact. The inline bootstrap fields (sourceUri/modelId/rank) are reserved:
the schema accepts them, but create-if-missing self-healing is not yet wired,
so a referenced adapter artifact must currently exist.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the metadata.name of the referenced adapter artifact. |  | MaxLength: 253 <br />MinLength: 1 <br /> |
| `kind` _[AIMServiceAdapterKind](#aimserviceadapterkind)_ | Kind is the kind of the referenced adapter. Required; restricted to AIMArtifact in v1. |  | Enum: [AIMArtifact] <br /> |
| `sourceUri` _string_ | SourceURI is an optional create-if-missing bootstrap source. When set and no<br />adapter artifact named Name exists, a later release will create one from this<br />source; a pre-existing artifact always wins. RESERVED: not yet acted on. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelID is the adapter's canonical model id. Required when SourceURI is set.<br />RESERVED: only meaningful alongside SourceURI. |  | Optional: \{\} <br /> |
| `rank` _integer_ | Rank is the optional LoRA rank for the bootstrapped adapter. RESERVED: only<br />meaningful alongside SourceURI. |  | Minimum: 1 <br />Optional: \{\} <br /> |


#### AIMServiceAdapterStatus



AIMServiceAdapterStatus is the per-adapter status aggregated onto an AIMService.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the adapter reference name. |  |  |
| `adapterPath` _string_ | AdapterPath is the on-disk directory name (mirrored from the artifact). |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelID is the adapter's canonical model id (mirrored from the artifact). |  | Optional: \{\} <br /> |
| `state` _[AIMAdapterState](#aimadapterstate)_ | State is the disk-side state of the adapter for this service. |  | Enum: [Pending Downloading Downloaded Deleting Loaded LoadRejected] <br />Optional: \{\} <br /> |
| `loadedReplicas` _string_ | LoadedReplicas reports how many serving replicas have the adapter loaded,<br />as "loaded/total" (e.g. "3/3"). RESERVED: engine-reported, not yet populated. |  | Optional: \{\} <br /> |
| `lastObserved` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastObserved is when the controller last observed this adapter's state. |  | Optional: \{\} <br /> |
| `lastError` _string_ | LastError carries the most recent error for this adapter (e.g. a mirrored<br />failing reason from the underlying artifact). |  | Optional: \{\} <br /> |


#### AIMServiceAutoScaling



AIMServiceAutoScaling configures KEDA-based autoscaling with custom metrics.
This enables automatic scaling based on metrics collected from OpenTelemetry.
A present autoScaling block must carry at least one real setting: a fully
empty block is a partially-complete spec that yields no usable configuration,
so it is rejected. Omit the whole block to use default scaling instead. Any
single valid sub-field (metrics, pollingInterval, or cooldownPeriod) is enough.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metrics` _[AIMServiceMetricsSpec](#aimservicemetricsspec) array_ | Metrics is a list of metrics to be used for autoscaling.<br />Each metric defines a source (PodMetric) and target values. |  | Optional: \{\} <br /> |
| `pollingInterval` _integer_ | PollingInterval is the KEDA polling interval in seconds. Defaults to 5<br />when spec.minReplicas == 0 (so a single request reliably activates the<br />deployment within ~10s); otherwise unset (KEDA default of 30 applies). |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `cooldownPeriod` _integer_ | CooldownPeriod is the seconds-of-inactivity budget KEDA waits before<br />scaling back to minReplicaCount. Under scale-to-zero, defaults to a<br />memory-derived value (300-1200s); otherwise unset (KEDA default of 300). |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### AIMServiceCacheStatus



AIMServiceCacheStatus captures cache-related status for an AIMService.

Exactly one of TemplateCacheRef / ProfileCacheRef is populated, depending on
which reconciliation path produced the cache:
  - TemplateCacheRef is set by the v1alpha1 (template-based) path and points
    to an AIMTemplateCache.
  - ProfileCacheRef is set by the v1alpha2 (profile-based) path and points to
    an AIMProfileCache.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateCacheRef` _[AIMResolvedReference](#aimresolvedreference)_ | TemplateCacheRef references the AIMTemplateCache being used, if any.<br />Set by the v1alpha1 (template-based) reconciliation path. |  | Optional: \{\} <br /> |
| `profileCacheRef` _[AIMResolvedReference](#aimresolvedreference)_ | ProfileCacheRef references the AIMProfileCache being used, if any.<br />Set by the v1alpha2 (profile-based) reconciliation path. |  | Optional: \{\} <br /> |
| `retryAttempts` _integer_ | RetryAttempts tracks how many times this service has attempted to retry a failed cache.<br />Each service gets exactly one retry attempt. When a cache enters Failed state,<br />this counter is incremented from 0 to 1 after deleting failed Artifacts.<br />If the retry fails (cache enters Failed again with attempts == 1), the service degrades. |  | Optional: \{\} <br /> |


#### AIMServiceCachingConfig



AIMServiceCachingConfig controls caching behavior for a service.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[AIMCachingMode](#aimcachingmode)_ | Mode controls when to use caching.<br />Canonical values:<br />- Shared (default): reuse/create shared cache assets<br />- Dedicated: create service-owned dedicated cache assets<br />Legacy values are accepted and normalized:<br />- Always -> Shared<br />- Auto -> Shared<br />- Never -> Dedicated | Shared | Enum: [Dedicated Shared Auto Always Never] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env supplies credentials for model downloads (for example a HuggingFace<br />token via secretKeyRef). Unlike the inference container env, these<br />variables reach only the model-download Job, so download-only secrets are<br />never injected into the serving container. They are also reachable for<br />cluster-scoped and overlay profiles, where the profile's own caching.env<br />does not exist. Merged over the profile's caching.env (service wins). |  | Optional: \{\} <br /> |


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
The value field that matches the chosen type must be set, otherwise the
target carries no threshold and the scaler cannot make a decision.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type specifies how to interpret the metric value.<br />"Value": absolute value target (use Value field)<br />"AverageValue": average value across all pods (use AverageValue field)<br />"Utilization": percentage utilization for resource metrics (use AverageUtilization field) |  | Enum: [Value AverageValue Utilization] <br /> |
| `value` _string_ | Value is the target value of the metric (as a quantity).<br />Used when Type is "Value".<br />Example: "1" for 1 request, "100m" for 100 millicores |  | Optional: \{\} <br /> |
| `averageValue` _string_ | AverageValue is the target value of the average of the metric across all relevant pods (as a quantity).<br />Used when Type is "AverageValue".<br />Example: "100m" for 100 millicores per pod |  | Optional: \{\} <br /> |
| `averageUtilization` _integer_ | AverageUtilization is the target value of the average of the resource metric across all relevant pods,<br />represented as a percentage of the requested value of the resource for the pods.<br />Used when Type is "Utilization". Only valid for Resource metric source type.<br />Example: 80 for 80% utilization |  | Optional: \{\} <br /> |


#### AIMServiceMetricsSpec



AIMServiceMetricsSpec defines a single metric for autoscaling.
Specifies the metric source type and configuration.



_Appears in:_
- [AIMServiceAutoScaling](#aimserviceautoscaling)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type is the type of metric source.<br />Valid values: "PodMetric" (per-pod custom metrics). |  | Enum: [PodMetric] <br /> |
| `podmetric` _[AIMServicePodMetricSource](#aimservicepodmetricsource)_ | PodMetric refers to a metric describing each pod in the current scale target.<br />Used when Type is "PodMetric". Supports backends like OpenTelemetry for custom metrics. |  | Optional: \{\} <br /> |


#### AIMServiceModel



AIMServiceModel specifies which model to deploy. Exactly one field must be set.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name references an existing AIMModel or AIMClusterModel by metadata.name.<br />The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel.<br />Example: `meta-llama-3-8b` |  | Optional: \{\} <br /> |
| `image` _string_ | Image specifies a container image URI directly.<br />The controller searches for an existing model with this image, or creates one if none exists.<br />Auto-created models are namespace-scoped and can be reused by other services.<br />Example: `ghcr.io/silogen/llama-3-8b:v1.2.0` |  | Optional: \{\} <br /> |
| `custom` _[AIMServiceModelCustom](#aimservicemodelcustom)_ | Custom specifies a custom model configuration with explicit base image,<br />model sources, and hardware requirements. The controller will search for<br />an existing matching AIMModel or auto-create one if not found. |  | Optional: \{\} <br /> |


#### AIMServiceModelCustom



AIMServiceModelCustom specifies a custom model configuration with explicit base image,
model sources, and hardware requirements. Used for ad-hoc custom model deployments.



_Appears in:_
- [AIMServiceModel](#aimservicemodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `baseImage` _string_ | BaseImage is the container image URI for the AIM base image.<br />This will be used as the image for the auto-created AIMModel.<br />Example: `ghcr.io/silogen/aim-base:0.7.0` |  | Required: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use.<br />The controller will search for or create an AIMModel with these sources.<br />The size field is optional - if not specified, it will be discovered by the download job.<br />AIM runtime currently supports only one model source. |  | MaxItems: 1 <br />MinItems: 1 <br />Required: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies the GPU and CPU requirements for this custom model.<br />GPU is optional - if not set, no GPUs are requested (CPU-only model). |  | Required: \{\} <br /> |


#### AIMServiceOverrides



AIMServiceOverrides allows overriding template parameters at the service level.
All fields are optional. When specified, they override the corresponding values
from the referenced AIMServiceTemplate.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica.<br />For GPU models, defines the GPU count and model types required for deployment.<br />For CPU-only models, defines CPU resource requirements.<br />This field is immutable after creation. |  | Optional: \{\} <br /> |


#### AIMServicePodMetric



AIMServicePodMetric identifies the pod metric and its backend.
Supports multiple metrics backends including OpenTelemetry.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `backend` _string_ | Backend defines the metrics backend to use.<br />If not specified, defaults to "opentelemetry". | opentelemetry | Enum: [opentelemetry] <br />Optional: \{\} <br /> |
| `serverAddress` _string_ | ServerAddress specifies the address of the metrics backend server.<br />If not specified, defaults to "keda-otel-scaler.keda.svc:4317" for OpenTelemetry backend. |  | Optional: \{\} <br /> |
| `metricNames` _string array_ | MetricNames specifies which metrics to collect from pods and send to ServerAddress.<br />Example: ["vllm:num_requests_running"] |  | Optional: \{\} <br /> |
| `query` _string_ | Query specifies the query to run to retrieve metrics from the backend.<br />The query syntax depends on the backend being used.<br />Example: "vllm:num_requests_running" for OpenTelemetry. |  | Optional: \{\} <br /> |
| `operationOverTime` _string_ | OperationOverTime specifies the operation to aggregate metrics over time.<br />Valid values: "last_one", "avg", "max", "min", "rate", "count"<br />Default: "last_one" |  | Optional: \{\} <br /> |


#### AIMServicePodMetricSource



AIMServicePodMetricSource defines pod-level metrics configuration.
Specifies the metric identification and target values for pod-based autoscaling.



_Appears in:_
- [AIMServiceMetricsSpec](#aimservicemetricsspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMServicePodMetric](#aimservicepodmetric)_ | Metric contains the metric identification and backend configuration.<br />Defines which metrics to collect and how to query them. |  |  |
| `target` _[AIMServiceMetricTarget](#aimservicemetrictarget)_ | Target specifies the target value for the metric.<br />The autoscaler will scale to maintain this target value. |  |  |


#### AIMServiceProfileConfig



AIMServiceProfileConfig contains profile selection configuration for AIMService v1alpha2.
When set, the service uses a profile-based reconciliation path instead of the template path.

Exactly one of Name and Selector must be set. Name resolves an AIMProfile /
AIMClusterProfile directly; Selector lists candidates by provenance and spec
fields (typically combined with `spec.model.name`, which the controller
treats as a shortcut for `selector.modelRef.name`).

An empty `name` ("") is treated as unset, so the rules below are value-based
(`size(self.name) > 0`) rather than presence-based (`has(self.name)`).



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the AIMProfile or AIMClusterProfile to use.<br />The controller looks for a namespace-scoped AIMProfile first, then falls back to AIMClusterProfile.<br />Mutually exclusive with Selector. An empty string is treated as unset. |  | Optional: \{\} <br /> |
| `selector` _[ProfileSelector](#profileselector)_ | Selector narrows candidate AIMProfile / AIMClusterProfile objects via the<br />shared provenance labels (role, source-model, origin) and spec filters<br />(aimId, precision, acceleratorModel, ...). The controller forces<br />`selector.role = Deployable` at evaluation time; user-supplied values<br />for that field are rejected by CEL on v1alpha2.<br />For every selector-driven AIMService the controller requires at least<br />one of `selector.aimId` or `selector.modelRef.name` so the watch<br />fan-out can reach the service via an O(1) index lookup. The top-level<br />`spec.model.name` shortcut is treated as if the user had set<br />`selector.modelRef.name` to the same value when not explicit. |  | Optional: \{\} <br /> |


#### AIMServiceProfileOverrides



AIMServiceProfileOverrides allows overriding profile parameters at the service level.
When specified, the controller materialises a service-owned overlay AIMProfile
derived from the referenced profile with these overrides applied; the original
profile is not modified. The downstream AIMProfileCache and InferenceService are
then resolved from the overlay, so the override participates in cache key
computation as well as inference-pod env wiring.

This type is a SUBSET of `aimv1alpha1.ProfileOverrides` (the type
AIMProfileSet derivation uses). Both go through the same internal apply
primitive (`internal/v1alpha2/aimprofile.ApplyProfileCopyOverrides`) so
the merge semantics match, but the service-level overlay intentionally
omits the `Image` override that AIMProfileSet's overrides expose:
changing the runtime container image per-service belongs at the profile
level (via spec.profiles.overrides.image on the source AIMModel /
AIMProfileSet), not at the consumer. Restricting the field set here
keeps the per-service overlay focused on workload-shape changes
(weights, env, args, hardware count) where service-level overrides are
the right tool.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources replaces the referenced profile's modelSources entirely.<br />Use this to point a profile at user-supplied weights (e.g. a fine-tuned<br />checkpoint) without forking the profile itself. The first source's<br />modelId becomes the overlay profile's modelId. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel replaces the referenced profile's acceleratorModel<br />(e.g. "MI300X" -> "MI325X"). Validation against actual cluster<br />availability is left to the AIMServiceTemplate / runtime layers. |  | Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount replaces the referenced profile's acceleratorCount. |  | Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode replaces the referenced profile's<br />acceleratorPartitioningMode. Complete replacement, not a merge — single<br />string, no substruct ambiguity. Empty string means "no override" (the<br />resolved overlay inherits the base profile's mode). Use this to deploy a<br />profile written for whole GPUs onto a partition slice. Whenever this<br />override is set (to any value, including "unpartitioned"), the CEL rule on<br />AIMService also requires an acceleratorCount override: partition mode<br />changes the per-unit interpretation of acceleratorCount, and CEL cannot<br />read the base profile to tell whether the meaning actually changed, so it<br />conservatively requires the count be restated. See<br />AcceleratorPartitioningMode on AIMProfileSpecCommon for the reserved<br />values ("unpartitioned", "partitioned", "<C>-<M>"). |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv merges by env-var name on top of the profile's<br />containerEnv. Matching names override; new names are appended.<br />AIM framework variables (AIM_*) reserved for the controller are<br />applied after the overlay's containerEnv and cannot be overridden<br />here. |  | Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv merges by key on top of the profile's engineEnv. These<br />variables flow into the inference engine's runtime configuration. |  | Optional: \{\} <br /> |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs shallow-merges on top of the profile's engineArgs,<br />overriding matching top-level keys. Values are passed verbatim<br />to the inference engine CLI. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |


#### AIMServiceRoutingStatus



AIMServiceRoutingStatus captures observed routing details.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the HTTP path prefix used when routing is enabled.<br />Example: `/tenant/svc-uuid` |  | Optional: \{\} <br /> |


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
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} <br /> |


#### AIMServiceRuntimeStatus



AIMServiceRuntimeStatus captures runtime status including replica counts from HPA.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `currentReplicas` _integer_ | CurrentReplicas is the current number of replicas as reported by the HPA. |  |  |
| `desiredReplicas` _integer_ | DesiredReplicas is the desired number of replicas as determined by the HPA. |  |  |
| `minReplicas` _integer_ | MinReplicas is the minimum number of replicas configured for autoscaling. |  |  |
| `maxReplicas` _integer_ | MaxReplicas is the maximum number of replicas configured for autoscaling. |  |  |
| `replicas` _string_ | Replicas is a formatted display string for kubectl output.<br />Shows "current" for fixed replicas or "current/desired (min-max)" for autoscaling. |  | Optional: \{\} <br /> |


#### AIMServiceSpec



AIMServiceSpec defines the desired state of AIMService.

Binds a canonical model to an AIMServiceTemplate and configures replicas,
caching behavior, and optional overrides. The template governs the base
runtime selection knobs, while the overrides field allows service-specific
customization.

With v1alpha2, a Profile can be used instead of a Template. Template and Profile
are mutually exclusive — at least one resolution path must be specified.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[AIMServiceModel](#aimservicemodel)_ | Model specifies which model to deploy using one of the available reference methods.<br />Use `name` to reference an existing AIMModel/AIMClusterModel by name, or use `image`<br />to specify a container image URI directly (which will auto-create a model if needed).<br />Required for v1alpha1 (template path), not permitted for v1alpha2 (profile path). |  | Optional: \{\} <br /> |
| `template` _[AIMServiceTemplateConfig](#aimservicetemplateconfig)_ | Template contains template selection and configuration.<br />Use Template.Name to specify an explicit template, or omit to auto-select.<br />Mutually exclusive with Profile (v1alpha2). |  | Optional: \{\} <br /> |
| `profile` _[AIMServiceProfileConfig](#aimserviceprofileconfig)_ | Profile contains profile selection configuration (v1alpha2 only).<br />When set, the service uses a profile-based reconciliation path.<br />Mutually exclusive with Template. |  | Optional: \{\} <br /> |
| `profileOverrides` _[AIMServiceProfileOverrides](#aimserviceprofileoverrides)_ | ProfileOverrides allows overriding specific profile parameters for this service.<br />Only valid when Profile is set. |  | Optional: \{\} <br /> |
| `adapterMode` _[AIMAdapterMode](#aimadaptermode)_ | AdapterMode is the immutable adapter contract for the service, mapped<br />directly to the AIM_ADAPTER_MODE container env. static (default) freezes<br />spec.adapters and mounts the adapter disk only when adapters are declared;<br />dynamic allows editing spec.adapters and mounts the disk even at zero<br />adapters (so add/remove never restarts the pod). It is immutable after<br />creation. | static | Enum: [static dynamic] <br />Optional: \{\} <br /> |
| `adapters` _[AIMServiceAdapterReference](#aimserviceadapterreference) array_ | Adapters is the load-bearing list of LoRA adapters this service serves.<br />The list may only be edited after creation when adapterMode is dynamic<br />(static freezes it). Omitting the list serves no adapters.<br />Supported on both the template (v1alpha1) and profile (v1alpha2) pipelines:<br />the base model the adapters attach to is resolved from the service's<br />template cache or profile cache respectively. In dynamic mode the list may<br />be edited after creation: adding an adapter stages it into the service's<br />subtree and the aim-runtime hot-loads it; removing one lets the runtime<br />unload it (subtree cleanup is reclaimed out-of-band). The InferenceService<br />is never modified for adapter changes — it mounts the whole per-service<br />subtree read-only. Entries are pure references; (kind, name) pairs must be<br />unique. Omitting the list serves no adapters. |  | MaxItems: 64 <br />Optional: \{\} <br /> |
| `caching` _[AIMServiceCachingConfig](#aimservicecachingconfig)_ | Caching controls caching behavior for this service.<br />When nil, defaults to Shared mode. |  | Optional: \{\} <br /> |
| `cacheModel` _boolean_ | DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version.<br />This field is no longer honored by the controller. |  | Optional: \{\} <br /> |
| `replicas` _integer_ | Replicas specifies the number of replicas for this service.<br />When not specified, defaults to 1 replica.<br />This value overrides any replica settings from the template.<br />For autoscaling, use MinReplicas and MaxReplicas instead. | 1 | Optional: \{\} <br /> |
| `minReplicas` _integer_ | MinReplicas specifies the minimum number of replicas for autoscaling.<br />Defaults to 1. Set to 0 to enable scale-to-zero: KEDA idles the predictor<br />to zero replicas when idle and brings it back up on the next request.<br />When specified with MaxReplicas, enables autoscaling for the service. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `maxReplicas` _integer_ | MaxReplicas specifies the maximum number of replicas for autoscaling.<br />Required when MinReplicas is set or when AutoScaling configuration is provided. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `autoScaling` _[AIMServiceAutoScaling](#aimserviceautoscaling)_ | AutoScaling configures advanced autoscaling behavior using KEDA.<br />Supports custom metrics from OpenTelemetry backend.<br />When specified, MinReplicas and MaxReplicas should also be set. |  | Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service.<br />When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />When set on AIMService, these take highest precedence in the merge hierarchy.<br />When set on RuntimeConfig, these provide namespace/cluster-level defaults.<br />Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources overrides the container resource requirements for this service.<br />When specified, these values take precedence over the template and image defaults. |  | Optional: \{\} <br /> |
| `overrides` _[AIMServiceOverrides](#aimserviceoverrides)_ | Overrides allows overriding specific template parameters for this service.<br />When specified, these values take precedence over the template values. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for the inference workload.<br />This service account is used by the deployed inference pods.<br />If empty, the default service account for the namespace is used. |  | Optional: \{\} <br /> |
| `priorityClassName` _string_ | PriorityClassName specifies the priority class for the inference pods.<br />This maps directly to the Kubernetes PriorityClassName field on the pod spec.<br />If empty, no priority class is set. |  | Optional: \{\} <br /> |


#### AIMServiceStatus



AIMServiceStatus defines the observed state of AIMService.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} <br /> |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  | Optional: \{\} <br /> |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the service lifecycle.<br />Values: `Pending`, `Starting`, `Running`, `Degraded`, `Failed`. | Pending | Enum: [Pending Starting Running Degraded Failed] <br /> |
| `routing` _[AIMServiceRoutingStatus](#aimserviceroutingstatus)_ | Routing surfaces information about the configured HTTP routing, when enabled. |  | Optional: \{\} <br /> |
| `resolvedTemplate` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedTemplate captures metadata about the template that satisfied the reference. |  |  |
| `resolvedProfile` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedProfile captures metadata about the profile that satisfied the reference.<br />Set when the service uses a profile-based reconciliation path (v1alpha2). |  | Optional: \{\} <br /> |
| `cache` _[AIMServiceCacheStatus](#aimservicecachestatus)_ | Cache captures cache-related status for this service. |  | Optional: \{\} <br /> |
| `runtime` _[AIMServiceRuntimeStatus](#aimserviceruntimestatus)_ | Runtime captures runtime status including replica counts. |  | Optional: \{\} <br /> |
| `adapters` _[AIMServiceAdapterStatus](#aimserviceadapterstatus) array_ | Adapters reports the per-adapter disk-side status for services that declare<br />spec.adapters. One entry per declared adapter. Observation is<br />best-effort/eventual; the disk state the controller wrote is authoritative. |  | Optional: \{\} <br /> |
| `adapterSubtreeSyncKey` _string_ | AdapterSubtreeSyncKey records the declared adapter set most recently<br />reconciled onto the service's adapter subtree by the subtree-sync Job<br />(a hash of the sorted spec.adapters names). The controller re-runs the<br />sync Job — which creates the subtree and prunes adapter directories no<br />longer declared — whenever this drifts from the current desired set, so<br />editing spec.adapters reclaims removed adapters without re-run loops. |  | Optional: \{\} <br /> |
| `adapterDiskPersistentVolumeClaim` _string_ | AdapterDiskPersistentVolumeClaim is the resolved shared adapter-disk PVC<br />(from the base model artifact's status). It is recorded here once resolved<br />and reused when a transient parent-resolution gap would otherwise leave it<br />empty, so a blip never re-renders the InferenceService without its adapter<br />mount and restarts a running predictor. Never cleared once set. |  | Optional: \{\} <br /> |




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
| `name` _string_ | Name is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use.<br />The template selects the runtime profile and GPU parameters.<br />When not specified, a template will be automatically selected based on the model. |  | Optional: \{\} <br /> |
| `allowUnoptimized` _boolean_ | AllowUnoptimized, if true, will allow automatic selection of templates<br />that resolve to an unoptimized profile. |  | Optional: \{\} <br /> |


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
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica.<br />For GPU models, defines the GPU count and model types required for deployment.<br />For CPU-only models, defines CPU resource requirements.<br />This field is immutable after creation. |  | Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B").<br />Required when customProfile is set; used to assemble the profile YAML aim_id field<br />and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8").<br />Required when customProfile is set; used for profile YAML model_id field<br />and for weight pre-caching via the discovery job. |  | Optional: \{\} <br /> |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine.<br />When set, the controller assembles a profile YAML from this data and template metadata,<br />creates a ConfigMap, and mounts it into discovery and inference containers.<br />Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  | Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />These variables are passed to the inference runtime and can be used<br />to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} <br /> |
| `caching` _[AIMTemplateCachingConfig](#aimtemplatecachingconfig)_ | Caching configures model caching behavior for this namespace-scoped template.<br />When enabled, models will be cached using the specified environment variables<br />during download. |  | Optional: \{\} <br /> |


#### AIMServiceTemplateSpecCommon







_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable.<br />Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica.<br />For GPU models, defines the GPU count and model types required for deployment.<br />For CPU-only models, defines CPU resource requirements.<br />This field is immutable after creation. |  | Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B").<br />Required when customProfile is set; used to assemble the profile YAML aim_id field<br />and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8").<br />Required when customProfile is set; used for profile YAML model_id field<br />and for weight pre-caching via the discovery job. |  | Optional: \{\} <br /> |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine.<br />When set, the controller assembles a profile YAML from this data and template metadata,<br />creates a ConfigMap, and mounts it into discovery and inference containers.<br />Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images.<br />These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults.<br />For namespace-scoped templates, secrets must exist in the same namespace.<br />For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.<br />This includes discovery dry-run jobs and inference services created from this template.<br />If empty, the default service account for the namespace is used. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template.<br />Service-specific values override the template defaults. |  | Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template.<br />When provided, the discovery dry-run will be skipped and these sources will be used directly.<br />This allows users to explicitly declare model dependencies without requiring a discovery job.<br />If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use.<br />When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for inference containers.<br />These variables are passed to the inference runtime and can be used<br />to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} <br /> |


#### AIMServiceTemplateStatus



AIMServiceTemplateStatus defines the observed state of AIMServiceTemplate.



_Appears in:_
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)
- [AIMServiceTemplate](#aimservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} <br /> |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  | Optional: \{\} <br /> |
| `resolvedCache` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedCache captures metadata about which cache is used for this template |  | Optional: \{\} <br /> |
| `resolvedHardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | ResolvedHardware contains the resolved hardware requirements for this template.<br />These values are computed from discovery results and spec defaults, and represent<br />what will actually be used when creating InferenceServices.<br />Resolution order: discovery output > spec values > defaults. |  | Optional: \{\} <br /> |
| `resolvedNodeAffinity` _[NodeAffinity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#nodeaffinity-v1-core)_ | ResolvedNodeAffinity contains the computed node affinity rules for GPU scheduling.<br />This is derived from GPU model and minVRAM requirements, merged with any user-specified<br />affinity from the spec. The service controller uses this directly when creating InferenceServices. |  | Optional: \{\} <br /> |
| `hardwareSummary` _string_ | HardwareSummary is a human-readable display string for the hardware requirements.<br />Format: "\{count\} x \{model\}" for GPU (e.g., "2 x MI300X") or "CPU" for CPU-only.<br />This is a computed field for display purposes only. |  | Optional: \{\} <br /> |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the template lifecycle.<br />Values: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`. | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources list the models that this template requires to run. These are the models that will be<br />cached, if this template is cached. |  |  |
| `version` _string_ | Version is the AIM version extracted from the owning model's image tag<br />(e.g., "0.8.5" from "aim-base:0.8.5"). Set during discovery.<br />Used for version-based template matching with fine-tuned models. |  | Optional: \{\} <br /> |
| `profile` _[AIMDiscoveredProfile](#aimdiscoveredprofile)_ | Profile contains the full discovery result profile as a free-form JSON object.<br />This includes metadata, engine args, environment variables, and model details. |  |  |
| `discoveryJob` _[AIMResolvedReference](#aimresolvedreference)_ | DiscoveryJob is a reference to the job that was run for discovery |  |  |
| `discovery` _[DiscoveryState](#discoverystate)_ | Discovery contains state tracking for the discovery process, including<br />retry attempts and backoff timing for the circuit breaker pattern. |  | Optional: \{\} <br /> |


#### AIMStorageConfig



AIMStorageConfig configures storage defaults for artifacts and PVCs.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `defaultStorageClassName` _string_ | DefaultStorageClassName specifies the storage class to use for artifacts and PVCs<br />when the consuming resource (AIMArtifact, AIMTemplateCache, AIMServiceTemplate) does not<br />specify a storage class. If this field is empty, the cluster's default storage class is used. |  | Optional: \{\} <br /> |
| `pvcHeadroomPercent` _integer_ | PVCHeadroomPercent specifies the percentage of extra space to add to PVCs<br />for model storage. This accounts for filesystem overhead and temporary files<br />during model loading. The value represents a percentage (e.g., 10 means 10% extra space).<br />If not specified, defaults to 10%. | 10 | Minimum: 0 <br />Optional: \{\} <br /> |
| `downloadFilter` _[AIMDownloadFilter](#aimdownloadfilter)_ | DownloadFilter controls which files are included or excluded during artifact downloads.<br />When set here, applies as the default for all artifacts using this runtime config.<br />Individual artifacts can override this with their own downloadFilter.<br />When no filter is configured at any level, subdirectory files are excluded by default.<br />Set to an empty object (downloadFilter: \{\}) to explicitly allow all files. |  | Optional: \{\} <br /> |
| `adapterDiskStorageClassName` _string_ | AdapterDiskStorageClassName is the storage class for the shared<br />ReadWriteMany adapter disk. It must be RWX-capable (e.g. longhorn, NFS) and<br />is resolved before the (typically RWO) DefaultStorageClassName. |  | Optional: \{\} <br /> |
| `adapterDiskSize` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | AdapterDiskSize is the cluster default size for the shared adapter disk PVC<br />(built-in default when unset). An artifact's adapterDisk.size wins over it. |  | Optional: \{\} <br /> |


#### AIMTemplateCache



AIMTemplateCache pre-warms artifacts for a specified template.



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

AIMTemplateCacheMode controls the ownership behavior of artifacts created by a template cache.

_Validation:_
- Enum: [Dedicated Shared]

_Appears in:_
- [AIMTemplateCacheSpec](#aimtemplatecachespec)

| Field | Description |
| --- | --- |
| `Dedicated` | TemplateCacheModeDedicated means artifacts have owner references to the template cache.<br />When the template cache is deleted, all its artifacts are garbage collected.<br />Use this mode for service-specific caches that should be cleaned up with the service.<br /> |
| `Shared` | TemplateCacheModeShared means artifacts have no owner references.<br />artifacts persist independently of template cache lifecycle and can be shared.<br />This is the default mode for long-lived, reusable caches.<br /> |


#### AIMTemplateCacheSpec



AIMTemplateCacheSpec defines the desired state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateName` _string_ | TemplateName is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to cache.<br />The controller will first look for a namespace-scoped AIMServiceTemplate in the same namespace.<br />If not found, it will look for a cluster-scoped AIMClusterServiceTemplate with the same name.<br />Namespace-scoped templates take priority over cluster-scoped templates. |  | MinLength: 1 <br /> |
| `templateScope` _[AIMServiceTemplateScope](#aimservicetemplatescope)_ | TemplateScope indicates whether the template is namespace-scoped or cluster-scoped.<br />This field is set by the controller during template resolution. |  | Enum: [Namespace Cluster Unknown] <br />Required: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables to use for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} <br /> |
| `storageClassName` _string_ | StorageClassName specifies the storage class for cache volumes.<br />When not specified, uses the cluster default storage class. |  | Optional: \{\} <br /> |
| `downloadImage` _string_ | DownloadImage specifies the container image used to download and initialize artifacts.<br />When not specified, the controller uses the default model download image. |  | Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to cache for this template.<br />These sources are typically copied from the resolved template's model sources. |  | Optional: \{\} <br /> |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |
| `mode` _[AIMTemplateCacheMode](#aimtemplatecachemode)_ | Mode controls the ownership behavior of artifacts created by this template cache.<br />- Dedicated: artifacts are owned by this template cache and garbage collected when it's deleted.<br />- Shared (default): artifacts have no owner references and persist independently.<br />When a Shared template cache encounters artifacts with owner references, it promotes them<br />to shared by removing the owner references, ensuring they persist for long-term use. | Shared | Enum: [Dedicated Shared] <br />Optional: \{\} <br /> |


#### AIMTemplateCacheStatus



AIMTemplateCacheStatus defines the observed state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of the template cache state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} <br /> |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high-level status of the template cache. | Pending | Enum: [Pending Progressing Ready Failed Degraded NotAvailable] <br /> |
| `resolvedTemplateKind` _string_ | ResolvedTemplateKind indicates whether the template resolved to a namespace-scoped<br />AIMServiceTemplate or cluster-scoped AIMClusterServiceTemplate.<br />Values: "AIMServiceTemplate", "AIMClusterServiceTemplate" |  |  |
| `artifacts` _object (keys:string, values:[AIMResolvedArtifact](#aimresolvedartifact))_ | Artifacts maps model names to their resolved AIMArtifact resources. |  | Optional: \{\} <br /> |


#### AIMTemplateCachingConfig



AIMTemplateCachingConfig configures model caching behavior for namespace-scoped templates.



_Appears in:_
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether caching is enabled for this template.<br />Defaults to `false`. | false |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables to use when downloading the model for caching.<br />These variables are available to the model download process and can be used<br />to configure download behavior, authentication, proxies, etc.<br />If not set, falls back to the template's top-level Env field. |  | Optional: \{\} <br /> |




#### AIMTemplateProfile



AIMTemplateProfile declares profile variables for template selection.
Used in AIMCustomTemplate to specify optimization targets.



_Appears in:_
- [AIMCustomTemplate](#aimcustomtemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric specifies the optimization target (e.g., latency, throughput). |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numerical precision (e.g., fp8, fp16, bf16). |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |


#### AIMVersionPolicy

_Underlying type:_ _string_

AIMVersionPolicy controls how template versions are filtered during aimId-based matching.

_Validation:_
- Enum: [pinned latest any all]

_Appears in:_
- [AIMCustomModelSpec](#aimcustommodelspec)

| Field | Description |
| --- | --- |
| `pinned` | AIMVersionPolicyPinned matches templates whose status.version equals the model's image tag.<br /> |
| `latest` | AIMVersionPolicyLatest matches only templates at the newest available status.version.<br /> |
| `all` | AIMVersionPolicyAll matches templates at any version. This is the<br />canonical spelling, aligned with v1alpha2 ProfileVersionPolicy.<br /> |
| `any` | AIMVersionPolicyAny is a deprecated alias of AIMVersionPolicyAll, kept<br />for backward compatibility with existing v1alpha1 objects. Prefer "all".<br /> |


#### AcceleratorType

_Underlying type:_ _string_

AcceleratorType distinguishes CPU from GPU accelerators.
Used by AIM Engine to determine the resource derivation strategy
(e.g., gpu → amd.com/gpu, cpu → cpu).

_Validation:_
- Enum: [gpu cpu]

_Appears in:_
- [ProfileHardwareGroup](#profilehardwaregroup)
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `cpu` |  |
| `gpu` |  |


#### ArtifactCacheConfig



ArtifactCacheConfig configures the S3-backed artifact cache.
When enabled, the controller checks internal S3 for cached models before
downloading from HuggingFace. On cache hit, the download source is rewritten
to s3:// so the download job pulls from the local cache instead.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether the S3 artifact cache is active. |  | Optional: \{\} <br /> |
| `s3Uri` _string_ | S3URI is the base S3 path for cached artifacts (e.g. s3://aim-cache/artifacts). |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env provides S3 endpoint configuration for the cache bucket.<br />Injected into download jobs when the source is rewritten to s3://.<br />Typical vars: AWS_ENDPOINT_URL, S3_NO_SSL. Credentials optional (anonymous access). |  | Optional: \{\} <br /> |


#### DiscoveredProfileCounts



DiscoveredProfileCounts summarizes the profiles found during image discovery.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ | Total is the number of profiles discovered from the image. |  |  |
| `supported` _integer_ | Supported is the number of discovered profiles currently supported by the cluster. |  |  |
| `unsupported` _integer_ | Unsupported is the number of discovered profiles currently not supported by the cluster. |  |  |
| `byHardware` _[ProfileHardwareGroup](#profilehardwaregroup) array_ | ByHardware groups discovered profiles by their hardware footprint and<br />reports whether each group is currently supported by the cluster. The<br />groups are stable across reconciles (sorted by acceleratorType,<br />acceleratorModel, acceleratorCount) so kubectl/jq queries are cheap.<br />Useful when debugging "why doesn't the \{metric, precision\} profile I<br />expect appear in my cluster?" — the breakdown surfaces every shape the<br />image emits, not only the ones materialised as AIMProfile objects. |  | Optional: \{\} <br /> |


#### DiscoveryCacheReference



DiscoveryCacheReference identifies the cached discovery catalog produced from image inspection.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the ConfigMap name. |  |  |
| `namespace` _string_ | Namespace is the ConfigMap namespace. |  |  |


#### DiscoveryState



DiscoveryState tracks the discovery process state for circuit breaker logic.
This enables exponential backoff and prevents infinite retry loops when
discovery jobs fail persistently.



_Appears in:_
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `attempts` _integer_ | Attempts is the number of discovery job attempts that have been made.<br />This counter increments each time a new discovery job is created after a failure. |  | Optional: \{\} <br /> |
| `lastAttemptTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastAttemptTime is the timestamp of the most recent discovery job creation.<br />Used to calculate exponential backoff before the next retry. |  | Optional: \{\} <br /> |
| `lastFailureReason` _string_ | LastFailureReason captures the reason for the most recent discovery failure.<br />Used to classify failures as terminal vs transient. |  | Optional: \{\} <br /> |
| `specHash` _string_ | SpecHash is a hash of the template spec fields that affect discovery.<br />When the spec changes, the circuit breaker resets to allow fresh attempts. |  | Optional: \{\} <br /> |
| `identityCheckHash` _string_ | IdentityCheckHash records the hash from the most recent identity<br />rediscovery attempt for a Ready template. When the current hash matches<br />this value, the controller will not invalidate a Ready template just to<br />retry identity (aimId/modelId) extraction. Bumping the operator-internal<br />hash version forces a one-shot revisit across all eligible templates<br />without operator intervention. |  | Optional: \{\} <br /> |
| `lastIdentityCheckTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastIdentityCheckTime is the timestamp of the most recent identity<br />rediscovery attempt initiation. Acts as a hard floor against rediscovery<br />thrashing in the presence of bugs that prevent IdentityCheckHash from<br />being recorded. |  | Optional: \{\} <br /> |


#### DownloadProgress



DownloadProgress represents the download progress for a artifact



_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `totalBytes` _integer_ | TotalBytes is the expected total size of the download in bytes |  | Optional: \{\} <br /> |
| `downloadedBytes` _integer_ | DownloadedBytes is the number of bytes downloaded so far |  | Optional: \{\} <br /> |
| `percentage` _integer_ | Percentage is the download progress as a percentage (0-100) |  | Maximum: 100 <br />Minimum: 0 <br />Optional: \{\} <br /> |
| `displayPercentage` _string_ | DisplayPercentage is a human-readable progress string (e.g., "45 %")<br />This field is automatically populated from Progress.Percentage |  | Optional: \{\} <br /> |


#### DownloadState



DownloadState represents the current download attempt state, updated by the downloader pod



_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `protocol` _string_ | Protocol is the download protocol currently in use (e.g., "XET", "HF_TRANSFER", "HTTP") |  | Optional: \{\} <br /> |
| `attempt` _integer_ | Attempt is the current attempt number (1-based) |  | Optional: \{\} <br /> |
| `totalAttempts` _integer_ | TotalAttempts is the total number of attempts configured via AIM_DOWNLOADER_PROTOCOL |  | Optional: \{\} <br /> |
| `protocolSequence` _string_ | ProtocolSequence is the configured protocol sequence (e.g., "HF_TRANSFER,XET") |  | Optional: \{\} <br /> |
| `message` _string_ | Message is a human-readable status message from the downloader |  | Optional: \{\} <br /> |


#### ImageMetadata



ImageMetadata contains metadata extracted from or provided for a container image.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[ModelMetadata](#modelmetadata)_ | Model contains AMD Silogen model-specific metadata. |  | Optional: \{\} <br /> |
| `oci` _[OCIMetadata](#ocimetadata)_ | OCI contains standard OCI image metadata. |  | Optional: \{\} <br /> |
| `originalLabels` _object (keys:string, values:string)_ | OriginalLabels contains the raw OCI image labels as a JSON object.<br />This preserves all labels from the image, including those not mapped to structured fields. |  | Optional: \{\} <br /> |
| `baseImageRef` _string_ | BaseImageRef is the value of the AIM_BASE_IMAGE_REF environment variable<br />baked into the image's OCI config at build time. For AIM model images this<br />records the base image (e.g. "ghcr.io/silogen/aim-base:0.8.5") that the<br />model image was built from. Used by the AIMModel controller to resolve<br />the deployment image for fine-tuned models whose spec.image is omitted<br />(versionPolicy=latest or any). |  | Optional: \{\} <br /> |


#### ManagedProfileCounts



ManagedProfileCounts summarizes managed derivative profiles. The count
fields intentionally omit `omitempty` so that a zero count serializes as
an explicit `0` rather than dropping the field. This keeps the status
shape stable for both kubectl printcolumns and chainsaw assertions —
callers can rely on `.status.managedProfiles.{total,ready,...}` always
being present once the controller has observed the resource.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)
- [AIMProfileSetStatus](#aimprofilesetstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ | Total is the number of derivative profiles the controller currently manages or intends to manage. |  |  |
| `ready` _integer_ | Ready is the number of derivative profiles whose own status is Ready. |  |  |
| `notAvailable` _integer_ | NotAvailable is the number of derivative profiles whose own status is NotAvailable. |  |  |
| `deployable` _integer_ | Deployable is the number of managed profiles whose spec is materialised<br />enough to back an AIMService (carries aimId and modelSources). For a<br />normal officially-discovered AIMModel this equals Total. For a<br />base-image AIMModel used as a source for custom-model derivation<br />this is 0 — the model only emits base profiles that callers must<br />derive into deployable profiles. |  |  |
| `base` _integer_ | Base is the number of managed profiles whose spec is structurally<br />incomplete (missing aimId or modelSources). Base profiles cannot back<br />an AIMService directly and exist purely as source material for<br />derivation via AIMModel.spec.profiles.derivedFrom with<br />selector.role=base. A non-zero value is the canonical operational<br />signal that this model is a base-image model (the source of<br />custom-model derivation). |  |  |


#### ModelDiscoveryState



ModelDiscoveryState tracks the Kubernetes Job that inspects an AIM image and
writes its profile YAMLs into the discovery cache ConfigMap. Mirrors the
v1alpha1 AIMServiceTemplate DiscoveryState but scoped to AIMModel semantics.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `attempts` _integer_ | Attempts is the number of discovery job attempts that have been made.<br />Increments each time a new discovery job is created after a failure. |  | Optional: \{\} <br /> |
| `lastAttemptTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#time-v1-meta)_ | LastAttemptTime is the timestamp of the most recent discovery job creation.<br />Used to calculate exponential backoff before the next retry. |  | Optional: \{\} <br /> |
| `lastFailureReason` _string_ | LastFailureReason captures the reason for the most recent discovery failure. |  | Optional: \{\} <br /> |
| `specHash` _string_ | SpecHash is a hash of the model spec fields that invalidate cached discovery<br />(image, imagePullSecrets, serviceAccountName, discoveryCommandVersion).<br />When it changes, the operator drops the cache and re-runs the discovery Job. |  | Optional: \{\} <br /> |


#### ModelMetadata



ModelMetadata contains AMD Silogen model-specific metadata extracted from image labels.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `canonicalName` _string_ | CanonicalName is the canonical model identifier (e.g., mistralai/Mixtral-8x22B-Instruct-v0.1).<br />Extracted from: org.amd.silogen.model.canonicalName |  | Optional: \{\} <br /> |
| `source` _string_ | Source is the URL where the model can be found.<br />Extracted from: org.amd.silogen.model.source |  | Optional: \{\} <br /> |
| `tags` _string array_ | Tags are descriptive tags (e.g., ["text-generation", "chat", "instruction"]).<br />Extracted from: org.amd.silogen.model.tags (comma-separated) |  | Optional: \{\} <br /> |
| `versions` _string array_ | Versions lists available versions.<br />Extracted from: org.amd.silogen.model.versions (comma-separated) |  | Optional: \{\} <br /> |
| `variants` _string array_ | Variants lists model variants.<br />Extracted from: org.amd.silogen.model.variants (comma-separated) |  | Optional: \{\} <br /> |
| `hfTokenRequired` _boolean_ | HFTokenRequired indicates if a HuggingFace token is required.<br />Extracted from: org.amd.silogen.hfToken.required |  | Optional: \{\} <br /> |
| `title` _string_ | Title is the Silogen-specific title for the model.<br />Extracted from: org.amd.silogen.title |  | Optional: \{\} <br /> |
| `descriptionFull` _string_ | DescriptionFull is the full description.<br />Extracted from: org.amd.silogen.description.full |  | Optional: \{\} <br /> |
| `releaseNotes` _string_ | ReleaseNotes contains release notes for this version.<br />Extracted from: org.amd.silogen.release.notes |  | Optional: \{\} <br /> |
| `recommendedDeployments` _[RecommendedDeployment](#recommendeddeployment) array_ | RecommendedDeployments contains recommended deployment configurations.<br />Extracted from: org.amd.silogen.model.recommendedDeployments (parsed from JSON array) |  | Optional: \{\} <br /> |


#### ModelSourceFilter



ModelSourceFilter defines an explicit image selector for discovery.
Supports multiple formats:
- Repository name: "org/repo" - exact repository match
- Repository with tag: "org/repo:1.0.0" - exact tag match
- Full URI: "ghcr.io/org/repo:1.0.0" - overrides registry and tag
- Full URI: "ghcr.io/org/repo" - exact repository on a specific registry



_Appears in:_
- [AIMClusterModelSourceSpec](#aimclustermodelsourcespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image with explicit repository matching and full URI support.<br />Supported formats:<br />- Repository name: "amdenterpriseai/aim-qwen-qwen3-32b"<br />- Repository with tag: "silogen/aim-llama:1.0.0" (overrides versions field)<br />- Full URI: "ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1" (overrides spec.registry and versions)<br />- Full URI without tag: "ghcr.io/silogen/aim-google-gemma-3-1b-it" (overrides spec.registry)<br />When a full URI is specified (including registry like ghcr.io), only images from that<br />registry will match. When a tag is included, it takes precedence over the versions field.<br />Note: wildcard syntax (e.g., "*") is not supported; use explicit image list entries. |  | MaxLength: 512 <br /> |
| `exclude` _string array_ | Exclude lists specific repository names to skip (exact match on repository name only, not registry).<br />Useful for excluding base images or experimental versions.<br />Examples:<br />- ["amdenterpriseai/aim-base", "amdenterpriseai/aim-experimental"]<br />- ["silogen/aim-base"] - works with "ghcr.io/silogen/aim-*" (registry is not checked in exclusion)<br />Note: Exclusions match against repository names (e.g., "silogen/aim-base"), not full URIs. |  | Optional: \{\} <br /> |
| `versions` _string array_ | Versions specifies semantic version constraints for this filter.<br />If specified, overrides the global Versions field.<br />Only tags that parse as valid semver are considered (including prereleases like 0.8.1-rc1).<br />Ignored if the Image field includes an explicit tag (e.g., "repo:1.0.0").<br />Examples: ">=1.0.0", "<2.0.0", "~1.2.0" (patch updates), "^1.0.0" (minor updates)<br />Prerelease versions (e.g., 0.8.1-rc1) are supported and follow semver rules:<br />- 0.8.1-rc1 matches ">=0.8.0" (prerelease is part of version 0.8.1)<br />- Use ">=0.8.1-rc1" to match only that prerelease or higher<br />- Leave empty to match all tags (including prereleases and non-semver tags) |  | Optional: \{\} <br /> |


#### OCIMetadata



OCIMetadata contains standard OCI image metadata extracted from image labels.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `title` _string_ | Title is the human-readable title.<br />Extracted from: org.opencontainers.image.title |  | Optional: \{\} <br /> |
| `description` _string_ | Description is a brief description.<br />Extracted from: org.opencontainers.image.description |  | Optional: \{\} <br /> |
| `licenses` _string_ | Licenses is the SPDX license identifier(s).<br />Extracted from: org.opencontainers.image.licenses |  | Optional: \{\} <br /> |
| `vendor` _string_ | Vendor is the organization that produced the image.<br />Extracted from: org.opencontainers.image.vendor |  | Optional: \{\} <br /> |
| `authors` _string_ | Authors is contact details of the authors.<br />Extracted from: org.opencontainers.image.authors |  | Optional: \{\} <br /> |
| `source` _string_ | Source is the URL to the source code repository.<br />Extracted from: org.opencontainers.image.source |  | Optional: \{\} <br /> |
| `documentation` _string_ | Documentation is the URL to documentation.<br />Extracted from: org.opencontainers.image.documentation |  | Optional: \{\} <br /> |
| `created` _string_ | Created is the creation timestamp.<br />Extracted from: org.opencontainers.image.created |  | Optional: \{\} <br /> |
| `revision` _string_ | Revision is the source control revision.<br />Extracted from: org.opencontainers.image.revision |  | Optional: \{\} <br /> |
| `version` _string_ | Version is the image version.<br />Extracted from: org.opencontainers.image.version |  | Optional: \{\} <br /> |




#### ProfileHardwareGroup



ProfileHardwareGroup is one accelerator footprint within a model's
discovery catalog, with the metric/precision combos shipped under it.



_Appears in:_
- [DiscoveredProfileCounts](#discoveredprofilecounts)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType is the resource family (gpu, cpu). |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier (e.g., "MI300X",<br />"EPYC_ZEN5"). Empty for profiles with no accelerator requirement. |  | Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units the profile<br />requests. For gpu: device count (e.g., 1, 2, 4, 8 for tensor-parallel<br />sizes). For cpu: number of CPU cores (e.g., 128 for EPYC_ZEN5,<br />192 for EPYC_9965). |  | Optional: \{\} <br /> |
| `supported` _boolean_ | Supported reports whether this hardware footprint is currently<br />satisfied by at least one cluster node. When false, all profiles in<br />this group are skipped during materialisation. |  |  |
| `profiles` _[ProfileHardwareGroupEntry](#profilehardwaregroupentry) array_ | Profiles lists the \{metric, precision\} combinations discovered under<br />this hardware footprint. Reported even when the group is unsupported<br />so users can see what they're missing. |  | Optional: \{\} <br /> |


#### ProfileHardwareGroupEntry



ProfileHardwareGroupEntry identifies one profile within a hardware group
by its {metric, precision} pair. Sufficient for users to spot whether the
optimization variant they want is shipped at all.



_Appears in:_
- [ProfileHardwareGroup](#profilehardwaregroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target (latency, throughput). |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision (fp4, fp8, bf16, …). |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |


#### ProfileOrigin

_Underlying type:_ _string_

ProfileOrigin classifies how an AIMProfile / AIMClusterProfile was produced.
Stamped by AIMProfile reconcilers via the `aim.eai.amd.com/profile-origin`
label and the AIMProfile `status.origin` field. Iteration-1 producers
stamp `discovered` (image discovery) and `derived` (AIMProfileSet /
AIMModel.spec.profiles.derivedFrom); user-authored profiles are backfilled
to `user-authored` by the AIMProfile reconciler when no AIM controller
owns them.

_Validation:_
- Enum: [discovered derived user-authored]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `discovered` | ProfileOriginDiscovered indicates the profile was produced by image<br />discovery (today: AIMModel.spec.image native discovery path).<br /> |
| `derived` | ProfileOriginDerived indicates the profile was produced by a derivation<br />flow (AIMModel.spec.profiles.derivedFrom or an AIMProfileSet).<br /> |
| `user-authored` | ProfileOriginUserAuthored indicates the profile was created<br />independently by a user (no AIM controller owner reference).<br /> |


#### ProfileOverrides



ProfileOverrides mutates selected source profiles when creating derived copies.

Identity fields (`aimId`, `modelId`, `profileId`) and behavioural fields
(`image`, `acceleratorModel`, `acceleratorCount`, env, args, modelSources)
always WRITE onto the derived profile. They are the "stamp on the output"
half of the derivation contract — the `selector` half FILTERS source
candidates and never mutates anything. Keeping these halves separated is
what lets a single YAML mean exactly one thing.

For `selector.role=base` derivations, `overrides.aimId` and
`overrides.modelId` are REQUIRED (enforced by CEL on the enclosing spec):
base profiles ship with empty identity fields by design, so the derived
profile would otherwise have no identity to bind against.



_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)
- [AIMProfileSetSpec](#aimprofilesetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId stamps the derived profile's spec.aimId. When set, wins over<br />the source profile's aimId. REQUIRED when the enclosing selector has<br />role=base (the source base profile carries no aimId of its own). |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId stamps the derived profile's spec.modelId. When set, wins<br />over both the source profile's modelId AND the auto-derivation from<br />modelSources[0].modelId. REQUIRED when the enclosing selector has<br />role=base. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId stamps the derived profile's spec.profileId. Optional —<br />most callers leave this empty and let the source profile's profileId<br />carry through (or the reconciler synthesise one). |  | Optional: \{\} <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources replaces the copied profile's modelSources. When<br />modelSources[0].modelId is set and overrides.modelId is unset, the<br />derived profile's modelId is auto-derived from modelSources[0]; an<br />explicit overrides.modelId always wins. |  | Optional: \{\} <br /> |
| `image` _string_ | Image overrides the runtime container image used by the derived<br />profiles. When empty the deployment image is rebased onto the source<br />profile's base-image (status.baseImage) so private mirrors stay<br />self-contained. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel replaces the copied profile's acceleratorModel. |  | Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount replaces the copied profile's acceleratorCount. |  | Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode replaces the copied profile's<br />acceleratorPartitioningMode. Complete replacement, not a merge; an empty<br />override string leaves the source profile's mode untouched. Same reserved<br />values as AIMProfileSpecCommon.AcceleratorPartitioningMode. Whenever this<br />override is set (to any value, including "unpartitioned"), the CEL rule on the<br />enclosing spec also requires AcceleratorCount to be set: partition mode<br />changes the per-unit interpretation of acceleratorCount, and CEL cannot<br />read the base profile to tell whether the meaning actually changed, so it<br />conservatively requires the count be restated. |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv merges by env var name, overriding matching source entries. |  | Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv merges by key, overriding matching source entries. |  | Optional: \{\} <br /> |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs shallow-merges on top of the source engineArgs, overriding matching keys. |  | Type: object <br />Optional: \{\} <br /> |


#### ProfileSelector



ProfileSelector narrows the source profiles selected for derivation.



_Appears in:_
- [AIMModelProfilesDerivedFrom](#aimmodelprofilesderivedfrom)
- [AIMProfileSetSpec](#aimprofilesetspec)
- [AIMServiceProfileConfig](#aimserviceprofileconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId filters by model architecture identifier (e.g., "qwen/qwen3-32b"). |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId filters by the source profile's specific model identifier. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId filters by the source profile's profile identifier. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine filters by inference engine. |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric filters by optimization target. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision filters by numeric precision. |  | Enum: [auto fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type filters by optimization level (exact match). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `minimumType` _[AIMProfileTypeFloor](#aimprofiletypefloor)_ | MinimumType filters by a minimum optimization level: candidates whose<br />type is this tier OR BETTER are accepted (hierarchy: optimized > general<br />> preview > unoptimized). This is the floor counterpart to the exact-match<br />Type field; the two AND together when both are set.<br />The sentinel "any" disables the floor (accept every tier). When this<br />field is empty the AIMService resolver applies a default floor of<br />"optimized" so auto-selection prefers production-grade profiles and never<br />silently picks an unoptimized one; to opt a service into lower tiers<br />(e.g. CPU/EPYC profiles published as unoptimized) set minimumType<br />explicitly to "unoptimized" or "any". Derivation selectors<br />(AIMProfileSet / AIMModel.profiles) treat empty as "any" so copying is<br />never tier-restricted by default. |  | Enum: [optimized general preview unoptimized any] <br />Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel filters by accelerator identifier. |  | Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode filters candidates by their declared<br />partitioning mode. Partial-order match (NOT strict equality):<br />  ""              - no filter on this field.<br />  "unpartitioned" - matches profiles with mode "" or "unpartitioned".<br />  "partitioned"   - matches profiles whose mode is non-trivial (anything<br />                    other than "" / "unpartitioned").<br />  "<C>"           - selector-only convenience: matches profiles with mode<br />                    "<C>-*" (prefix on the scheme). Not a valid profile-spec<br />                    value (e.g. selector "CPX" matches "CPX-NPS1", "CPX-NPS4").<br />  "<C>-<M>"       - exact-string match on the scheme. |  | Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType filters by accelerator resource type. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount filters by accelerator unit count. |  | Optional: \{\} <br /> |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs partially matches source engineArgs: every provided top-level key must<br />exist in the source object with an equal value. |  | Type: object <br />Optional: \{\} <br /> |
| `modelRef` _[ProfileSelectorModelRef](#profileselectormodelref)_ | ModelRef narrows candidates to those produced by a specific<br />AIM(Cluster)Model, matched via the `aim.eai.amd.com/source-model[-scope]`<br />labels stamped by the AIMModel reconcilers. Iteration 1 (v1alpha2 only). |  | Optional: \{\} <br /> |
| `role` _[ProfileSelectorRole](#profileselectorrole)_ | Role filters by the `aim.eai.amd.com/profile-role` label. Defaults to<br />`deployable`. `base` filters to base profiles emitted by base-image<br />discovery (custom-model derivation source material). | deployable | Enum: [base deployable] <br />Optional: \{\} <br /> |
| `origin` _[ProfileOrigin](#profileorigin)_ | Origin filters by the `aim.eai.amd.com/profile-origin` label. When unset<br />(empty) the selector does not filter by origin. Iteration 1 (v1alpha2<br />only). |  | Enum: [discovered derived user-authored] <br />Optional: \{\} <br /> |


#### ProfileSelectorModelRef



ProfileSelectorModelRef narrows derivation candidates by their owning
AIMModel / AIMClusterModel. The v1alpha2 AIMProfileSet reconciler matches
candidates by the `aim.eai.amd.com/source-model[-scope]` labels stamped by
AIMModel reconcilers.



_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the owning AIM(Cluster)Model name. Required. |  | MinLength: 1 <br /> |
| `scope` _[ProfileSelectorScope](#profileselectorscope)_ | Scope controls how Name is resolved against AIMModel vs AIMClusterModel. | Auto | Enum: [Auto Namespace Cluster] <br />Optional: \{\} <br /> |


#### ProfileSelectorRole

_Underlying type:_ _string_

ProfileSelectorRole filters source profiles by their role label
(`aim.eai.amd.com/profile-role`). Discovery of a deployable AIM image
stamps the `deployable` role; base-image discovery stamps `base` on
profiles that carry no aimId/modelSources, which custom-model AIMModels
derive into deployable copies.

_Validation:_
- Enum: [base deployable]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `base` | ProfileSelectorRoleBase filters down to base profiles that are not yet<br />deployable (no aimId / modelSources, only image + base-image so they can<br />serve as derivation source material for custom-model AIMModels).<br /> |
| `deployable` | ProfileSelectorRoleDeployable filters down to fully deployable profiles<br />(with aimId and modelSources). This is the default.<br /> |


#### ProfileSelectorScope

_Underlying type:_ _string_

ProfileSelectorScope determines how a ProfileSelector.ModelRef resolves the
source AIMModel scope. The v1alpha2 AIMProfileSet reconciler honours this
value when filtering source profiles by `aim.eai.amd.com/source-model[-scope]`
labels.

_Validation:_
- Enum: [Auto Namespace Cluster]

_Appears in:_
- [ProfileSelectorModelRef](#profileselectormodelref)

| Field | Description |
| --- | --- |
| `Auto` | ProfileSelectorScopeAuto tries the namespace AIMModel first and then<br />falls back to the cluster-scoped AIMClusterModel with the same name.<br /> |
| `Namespace` | ProfileSelectorScopeNamespace requires the source profile to come from<br />an AIMModel in the same namespace as the selecting AIMProfileSet.<br /> |
| `Cluster` | ProfileSelectorScopeCluster requires the source profile to come from a<br />cluster-scoped AIMClusterModel.<br /> |


#### ProfileSetReference



ProfileSetReference identifies the profile set synthesized by a model for derivation flows.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the profile set name. |  |  |
| `namespace` _string_ | Namespace is the profile set namespace. |  |  |


#### ProfileSourceRef



ProfileSourceRef identifies an alternate discovery cache source for derivation.



_Appears in:_
- [AIMModelProfilesDerivedFrom](#aimmodelprofilesderivedfrom)
- [AIMProfileSetSpec](#aimprofilesetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the discovery cache ConfigMap name. |  |  |


#### ProfileVersionPolicy

_Underlying type:_ _string_

ProfileVersionPolicy controls which matched profile versions a derivation request may copy.

_Validation:_
- Enum: [pinned latest all]

_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)
- [AIMProfileSetSpec](#aimprofilesetspec)

| Field | Description |
| --- | --- |
| `pinned` |  |
| `latest` |  |
| `all` |  |


#### RecommendedDeployment



RecommendedDeployment describes a recommended deployment configuration for a model.



_Appears in:_
- [ModelMetadata](#modelmetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuModel` _string_ | GPUModel is the GPU model name (e.g., MI300X, MI325X).<br />The legacy v1alpha1 schema only models GPU accelerators; CPU profiles<br />emitted via OCI labels (gpuModel="CPU") are not first-class here and<br />are handled exclusively by the v1alpha2 native discovery pipeline. |  | Optional: \{\} <br /> |
| `gpuCount` _integer_ | GPUCount is the number of GPUs required |  | Optional: \{\} <br /> |
| `precision` _string_ | Precision is the recommended precision (e.g., fp8, fp16, bf16) |  | Optional: \{\} <br /> |
| `metric` _string_ | Metric is the optimization target (e.g., latency, throughput) |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the unique identifier of the AIM profile for this deployment.<br />When set, templates created from this deployment will use this profile ID<br />to deterministically select the correct runtime profile in the AIM container. |  | Optional: \{\} <br /> |
| `description` _string_ | Description provides additional context about this deployment configuration |  | Optional: \{\} <br /> |


#### RuntimeConfigRef







_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMModelSpec](#aimmodelspec)
- [AIMServiceSpec](#aimservicespec)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [AIMTemplateCacheSpec](#aimtemplatecachespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both<br />as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority<br />over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster<br />runtime config with the name `default` is used, if it exists. |  | Optional: \{\} <br /> |


