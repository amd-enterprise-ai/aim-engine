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



AIMAdapterDisk configures the shared, RWX adapter disk provisioned alongside a model artifact. When present on a type=model artifact, the controller provisions a second PersistentVolumeClaim (ReadWriteMany) owned by the model artifact and shared by every AIMService that serves adapters on this base model.



_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Size is the requested size of the adapter disk PVC. Defaults to 50Gi when unset (a cascade default may override it). |  | Optional: \{\} |
| `storageClassName` _string_ | StorageClassName specifies the storage class for the adapter disk. When empty, the cluster default storage class is used. The access mode is fixed at ReadWriteMany by the controller. |  | Optional: \{\} |


#### AIMAdapterMode

_Underlying type:_ _string_

AIMAdapterMode selects the adapter contract for the service. Its values are the lowercase tokens the inference container reads via the AIM_ADAPTER_MODE env, and the field is immutable after creation.
  - static (default): the served set is fixed at creation — spec.adapters is CEL-immutable. The adapter disk is mounted read-only only when the service declares at least one adapter.
  - dynamic: spec.adapters may be edited after creation; the runtime hot-loads/unloads from the mounted subtree. The adapter disk is mounted (immutably) whenever the service is in dynamic mode — even at zero adapters — so add/remove never restarts the pod.

A service serves no adapters by simply declaring none: the default-static, no-adapters case mounts nothing.

_Validation:_
- Enum: [static dynamic]

_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description |
| --- | --- |
| `static` | AdapterModeStatic fixes the adapter set at creation; the disk is mounted only when adapters are declared. |
| `dynamic` | AdapterModeDynamic permits editing spec.adapters and mounts the disk even at zero adapters. |


#### AIMAdapterRuntimeConfig



AIMAdapterRuntimeConfig configures the inference runtime's LoRA adapter capacity. It is shared by AIMService and namespace/cluster RuntimeConfigs so services can override administrator-provided defaults.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `maxRank` _integer_ | MaxRank is the startup-time LoRA rank ceiling for dynamic adapter mode. A service-level value takes precedence over namespace and cluster RuntimeConfig values. When unset, the controller uses a conservative built-in default. Static adapter services infer the required maximum rank from their declared AIMArtifact objects. A service-level value is rejected in static mode; inherited RuntimeConfig values are ignored because one RuntimeConfig may serve both static and dynamic services. Allowed values mirror vLLM's MaxLoRARanks: 1, 8, 16, 32, 64, 128, 256, 320, and 512. Changing this value changes the inference container contract and rolls the serving workload. Increases take effect immediately. A decrease is deferred while any declared adapter artifact's rank is unresolved; the controller retains the last resolved ceiling until every rank can be checked against the lower value. |  | Minimum: 1 <br />Optional: \{\} |


#### AIMAdapterState

_Underlying type:_ _string_

AIMAdapterState is the disk-side lifecycle state of an adapter within a service's subtree. The controller tracks staging and removal directly; the engine-reported states (Loaded/LoadRejected) are reserved until the inference container exposes a per-adapter load-status surface.

_Validation:_
- Enum: [Pending Downloading Downloaded Failed Deleting Loaded LoadRejected]

_Appears in:_
- [AIMServiceAdapterStatus](#aimserviceadapterstatus)

| Field | Description |
| --- | --- |
| `Pending` | AdapterStatePending means the adapter is applied and waiting on a precondition. |
| `Downloading` | AdapterStateDownloading means a staging Job is running for this adapter. |
| `Downloaded` | AdapterStateDownloaded means the bytes are staged in this service's subtree. |
| `Failed` | AdapterStateFailed means the adapter's staging Job terminated unsuccessfully. LastError carries the Job failure reason and message. |
| `Deleting` | AdapterStateDeleting means the adapter was removed from spec.adapters and its bytes are being reclaimed from the service subtree by the subtree-sync Job. The entry is dropped from status once the prune completes. |
| `Loaded` | AdapterStateLoaded means the inference engine has the adapter in memory. RESERVED: engine-reported, not yet populated by the controller. |
| `LoadRejected` | AdapterStateLoadRejected means the bytes are present but the engine declined to load the adapter. RESERVED: engine-reported, not yet populated. |


#### AIMArtifact



AIMArtifact is the Schema for the artifacts API



_Appears in:_
- [AIMArtifactList](#aimartifactlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMArtifact` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMArtifactSpec](#aimartifactspec)_ |  |  |  |
| `status` _[AIMArtifactStatus](#aimartifactstatus)_ |  |  |  |


#### AIMArtifactConfig



AIMArtifactConfig controls artifact-level defaults that are not appropriate for individual services. These settings apply at namespace/cluster scope only.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `defaultRetentionPriority` _integer_ | DefaultRetentionPriority sets the default retention priority for AIMArtifacts that do not specify one in their spec. When set, artifacts without an explicit retentionPriority become eligible for automatic eviction at this priority level. Lower values are evicted first. If not set, artifacts without an explicit retentionPriority are never automatically evicted. |  | Minimum: 0 <br />Optional: \{\} |
| `modelDownloadImage` _string_ | ModelDownloadImage specifies the default container image for artifact download and size-check jobs. Applies when an AIMArtifact does not set spec.modelDownloadImage. When neither is set, the operator falls back to its build-time default (matching the release version). Direct S3 artifacts using typed S3 configuration always use the build-time image so administrator-managed credentials are not exposed to arbitrary images. |  | Optional: \{\} |
| `s3` _[S3ConnectionConfig](#s3connectionconfig)_ | S3 configures typed connection settings for AIMArtifacts whose sourceUri uses the s3:// scheme. RuntimeConfig env remains available for administrator-owned infrastructure settings, while artifact-level env is restricted to bounded downloader tuning. Typed fields take final precedence. |  | Optional: \{\} |


#### AIMArtifactList



AIMArtifactList contains a list of AIMArtifact





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMArtifactList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
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
| `Dedicated` | ArtifactModeDedicated indicates the cache has owner references and will be garbage collected when its owners are deleted. |
| `Shared` | ArtifactModeShared indicates the cache has no owner references and persists independently, available for sharing across services. |


#### AIMArtifactSpec



AIMArtifactSpec defines the desired state of AIMArtifact



_Appears in:_
- [AIMArtifact](#aimartifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[AIMArtifactType](#aimartifacttype)_ | Type discriminates a base model artifact (`model`) from a LoRA adapter definition (`adapter`). Defaults to `model`; immutable after creation. Adapter artifacts require modelId and exactly one of parentArtifact or compatibleWith, and do not get their own cache PVC. | model | Enum: [model adapter] <br />Optional: \{\} |
| `sourceUri` _string_ | SourceURI specifies the source location of the model to download. Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage). This field uniquely identifies the artifact and is immutable after creation. Example: hf://meta-llama/Llama-3-8B |  | MinLength: 1 <br />Pattern: `^(hf\|s3)://[^ \t\r\n]+$` |
| `parentArtifact` _string_ | ParentArtifact names the exact base model AIMArtifact (type=model) this adapter is bound to. It is an alternative to compatibleWith, is only allowed when type=adapter, and is immutable. The adapter is owned by (cascade-deleted with) the named parent. |  | MinLength: 1 <br />Optional: \{\} |
| `compatibleWith` _string array_ | CompatibleWith lists canonical base model IDs this adapter can be mounted with. It is an alternative to parentArtifact: compatibility is checked against the concrete base artifact resolved by each consuming AIMService. The adapter remains independent of the lifecycle and current availability of compatible model artifacts. |  | MinItems: 1 <br />items:Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Optional: \{\} |
| `rank` _integer_ | Rank is the LoRA rank of the adapter. Optional; only meaningful when type=adapter. |  | Minimum: 1 <br />Optional: \{\} |
| `adapterDisk` _[AIMAdapterDisk](#aimadapterdisk)_ | AdapterDisk, when set on a type=model artifact, provisions a shared ReadWriteMany adapter disk owned by this model artifact and partitioned per consuming service. Only allowed when type=model. |  | Optional: \{\} |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format. Determines the cache download path: /workspace/cache/\{modelId\} For HuggingFace sources, this is typically derived from the URI (e.g., "meta-llama/Llama-3-8B"). For S3 sources, this must be explicitly provided (e.g., "my-team/fine-tuned-llama"). When not specified, derived from SourceURI for HuggingFace sources. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Optional: \{\} |
| `storageClassName` _string_ | StorageClassName specifies the storage class for the cache volume. When not specified, uses the cluster default storage class. |  | Optional: \{\} |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Size specifies the size of the cache volume |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env lists environment variables used when downloading the artifact. For a typed S3 connection, artifact-level env is restricted to bounded downloader tuning; configure proxy, transport, trust, and credential providers through the administrator-owned RuntimeConfig. |  | Optional: \{\} |
| `modelDownloadImage` _string_ | ModelDownloadImage specifies the container image used to download and initialize the artifact. Direct S3 artifacts using typed S3 configuration always use the controller's built-in image so administrator-managed credentials and trust settings are not exposed to an arbitrary image. When not specified, the controller uses its built-in default. |  | Optional: \{\} |
| `downloadFilter` _[AIMDownloadFilter](#aimdownloadfilter)_ | DownloadFilter controls which files are included or excluded when downloading from HuggingFace. Overrides any filter set in the runtime config's storage.downloadFilter. When neither is set, subdirectory files are excluded by default (equivalent to exclude: ["*/*"]). To download all files including subdirectories, set this to an empty object: downloadFilter: \{\}. This field is immutable — to change the filter, recreate the artifact. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} |
| `retentionPriority` _integer_ | RetentionPriority marks this artifact as eligible for automatic eviction when storage quota is exceeded. Lower values are evicted first. Artifacts without this field are only evictable if a defaultRetentionPriority is configured in the runtime config. Use the aim.eai.amd.com/eviction-protected annotation to exempt an artifact from eviction entirely. |  | Minimum: 0 <br />Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |


#### AIMArtifactStatus



AIMArtifactStatus defines the observed state of AIMArtifact



_Appears in:_
- [AIMArtifact](#aimartifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest available observations of the artifact's state |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current status of the artifact | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] |
| `progress` _[DownloadProgress](#downloadprogress)_ | Progress represents the download progress when Status is Progressing |  | Optional: \{\} |
| `download` _[DownloadState](#downloadstate)_ | Download represents the current download attempt state, patched by the downloader pod. Shows which protocol is active, what attempt we're on, etc. |  | Optional: \{\} |
| `displaySize` _string_ | DisplaySize is the human-readable effective size (spec or discovered) |  | Optional: \{\} |
| `lastUsed` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastUsed represents the last time a model was deployed that used this cache |  |  |
| `persistentVolumeClaim` _string_ | PersistentVolumeClaim represents the name of the created PVC |  |  |
| `mode` _[AIMArtifactMode](#aimartifactmode)_ | Mode indicates the ownership mode of this artifact, derived from owner references.<br />- Dedicated: Has owner references, will be garbage collected when owners are deleted.<br />- Shared: No owner references, persists independently and can be shared. |  | Enum: [Dedicated Shared] <br />Optional: \{\} |
| `discoveredSizeBytes` _integer_ | DiscoveredSizeBytes is the model size discovered via check-size job. Populated when spec.size is not provided. |  | Optional: \{\} |
| `allocatedSize` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | AllocatedSize is the actual PVC size requested (including headroom). |  | Optional: \{\} |
| `headroomPercent` _integer_ | HeadroomPercent is the headroom percentage that was applied to the PVC size. |  | Optional: \{\} |
| `resolvedSourceUri` _string_ | DEPRECATED: ResolvedSourceURI was populated when the removed embedded Hugging Face-to-S3 cache rewrote a source URI. It is retained temporarily for API compatibility and is no longer populated. |  | Optional: \{\} |
| `adapterPersistentVolumeClaim` _string_ | AdapterPersistentVolumeClaim is the name of the shared adapter disk PVC provisioned for a type=model artifact that declares an adapterDisk. Empty otherwise. |  | Optional: \{\} |
| `adapterPath` _string_ | AdapterPath is the resolved on-disk directory name for a type=adapter artifact, frozen at first resolution (defaults to metadata.name). This is the canonical copy, mirrored into consuming services' status. |  | Optional: \{\} |
| `resolvedParent` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedParent captures the resolved parent model artifact for a type=adapter artifact, including its UID. |  | Optional: \{\} |
| `parentModelId` _string_ | ParentModelID is the parent model artifact's modelId, denormalized onto the adapter for convenience (refreshed each reconcile). |  | Optional: \{\} |


#### AIMArtifactStorageQuota



AIMArtifactStorageQuota configures storage limits for AIMArtifacts. These settings are only available on AIMClusterRuntimeConfig (cluster-scoped) because they enforce cluster-wide and cross-namespace policies.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `clusterLimit` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | ClusterLimit is the maximum total allocated storage for all AIMArtifacts cluster-wide. When the sum of all artifact PVC sizes across all namespaces would exceed this limit, new artifact PVCs are blocked until evictable artifacts are cleaned up or the limit is raised. |  | Optional: \{\} |
| `defaultNamespaceLimit` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | DefaultNamespaceLimit is the default maximum allocated storage for AIMArtifacts per namespace. Can be overridden for individual namespaces via the aim.eai.amd.com/artifact-storage-quota annotation. |  | Optional: \{\} |


#### AIMArtifactType

_Underlying type:_ _string_

AIMArtifactType discriminates a model artifact from a LoRA adapter artifact.

_Validation:_
- Enum: [model adapter]

_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)

| Field | Description |
| --- | --- |
| `model` | ArtifactTypeModel is a base model artifact backed by a cache PVC. This is the default and matches the behavior of artifacts created before adapters existed. |
| `adapter` | ArtifactTypeAdapter is a LoRA adapter definition. Adapter artifacts do not get their own cache PVC; their bytes are staged per-consuming-service into the parent model artifact's adapter disk. |


#### AIMCachingMode

_Underlying type:_ _string_

AIMCachingMode controls caching behavior for a service. Canonical values are Dedicated and Shared. Legacy values are accepted for backward compatibility:
- Always maps to Shared
- Auto maps to Shared
- Never maps to Dedicated

_Validation:_
- Enum: [Dedicated Shared Auto Always Never]

_Appears in:_
- [AIMServiceCachingConfig](#aimservicecachingconfig)

| Field | Description |
| --- | --- |
| `Dedicated` | CachingModeDedicated always creates service-owned dedicated caches/artifacts. |
| `Shared` | CachingModeShared reuses and creates shared caches/artifacts. |
| `Auto` | CachingModeAuto is deprecated legacy value that maps to Shared. |
| `Always` | CachingModeAlways is deprecated legacy value that maps to Shared. |
| `Never` | CachingModeNever is deprecated legacy value that maps to Dedicated. |


#### AIMClusterModel



AIMClusterModel is a cluster-scoped model catalog entry for AIM container images.

Cluster-scoped models can be referenced by AIMServices in any namespace, making them ideal for shared model deployments across teams and projects. Like namespace-scoped AIMModels, cluster models trigger discovery jobs to extract metadata and generate service templates.

When both cluster and namespace models exist for the same container image, services will preferentially use the namespace-scoped AIMModel when referenced by image URI.



_Appears in:_
- [AIMClusterModelList](#aimclustermodellist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModel` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMClusterModelList



AIMClusterModelList contains a list of AIMClusterModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterModel](#aimclustermodel) array_ |  |  |  |


#### AIMClusterModelSource



AIMClusterModelSource automatically discovers and syncs AI model images from container registries.



_Appears in:_
- [AIMClusterModelSourceList](#aimclustermodelsourcelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelSource` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterModelSourceSpec](#aimclustermodelsourcespec)_ |  |  |  |
| `status` _[AIMClusterModelSourceStatus](#aimclustermodelsourcestatus)_ |  |  |  |


#### AIMClusterModelSourceList



AIMClusterModelSourceList contains a list of AIMClusterModelSource.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterModelSourceList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterModelSource](#aimclustermodelsource) array_ |  |  |  |


#### AIMClusterModelSourceModel



AIMClusterModelSourceModel is a modelId-backed cluster model declaration.



_Appears in:_
- [AIMClusterModelSourceSpec](#aimclustermodelsourcespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelId` _string_ | ModelID is the canonical model identifier. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` |
| `aimId` _string_ | AimID optionally overrides the model architecture identifier. |  | Optional: \{\} |
| `source` _[AIMModelSourceLocation](#aimmodelsourcelocation)_ | Source optionally overrides the default hf://<modelId> source. |  | Optional: \{\} |
| `accelerator` _[AIMModelAcceleratorRequest](#aimmodelacceleratorrequest)_ | Accelerator optionally requests the hardware shape for the generated model. Mirrors AIMModelSpec.Accelerator. |  | Optional: \{\} |


#### AIMClusterModelSourceSpec



AIMClusterModelSourceSpec defines the desired state of AIMClusterModelSource.



_Appears in:_
- [AIMClusterModelSource](#aimclustermodelsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `registry` _string_ | Registry to sync from (e.g., docker.io, ghcr.io, gcr.io). Defaults to docker.io if not specified. | docker.io | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets contains references to secrets for authenticating to private registries. Secrets must exist in the operator namespace (typically aim-system). Used for both registry catalog listing and image metadata extraction. |  | Optional: \{\} |
| `filters` _[ModelSourceFilter](#modelsourcefilter) array_ | Filters define which images to discover and sync. Each filter specifies an image selector with optional version constraints and exclusions. Multiple filters are combined with OR logic (any match includes the image). Use this field for advanced matching options. For simple explicit image lists, use Images instead. |  | MaxItems: 100 <br />MinItems: 1 <br />Optional: \{\} |
| `images` _string array_ | Images defines a simple explicit list of images to discover and sync. Use this for straightforward static declarations without per-filter options. Supported image formats:<br />- Repository with tag: "amdenterpriseai/aim-qwen-qwen3-32b:0.8.4"<br />- Repository without tag: "amdenterpriseai/aim-qwen-qwen3-32b" (uses Versions if set)<br />- Full URI with tag: "docker.io/amdenterpriseai/aim-llama:1.0.0"<br />Must not be set together with Filters. |  | MaxItems: 100 <br />MinItems: 1 <br />Optional: \{\} |
| `models` _[AIMClusterModelSourceModel](#aimclustermodelsourcemodel) array_ | Models declares canonical model identifiers to materialize as v1alpha2 AIMClusterModel resources. Models may be combined with either Filters or Images. Lifecycle differs from registry discovery in one respect. Discovered images are a snapshot of an external registry, so they are only ever appended. A declaration is desired state the user edits in this spec, so models this source owns are kept in sync with their declaration — editing aimId, source, or accelerator updates the existing model in place. Neither kind is ever deleted: removing a declaration leaves its AIMClusterModel behind for an operator to remove deliberately. |  | MaxItems: 100 <br />MinItems: 1 <br />Optional: \{\} |
| `syncInterval` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#duration-v1-meta)_ | SyncInterval defines how often to sync with the registry. Defaults to 1h. Minimum recommended interval is 15m to avoid rate limiting. Format: duration string (e.g., "30m", "1h", "2h30m"). | 1h | Optional: \{\} |
| `versions` _string array_ | Versions specifies global semantic version constraints applied to all filters. Individual filters can override this with their own version constraints. Constraints use semver syntax: >=1.0.0, <2.0.0, ~1.2.0, ^1.0.0, etc. Non-semver tags (e.g., "latest", "dev") are silently skipped. Version ranges work on all registries (including ghcr.io, gcr.io) when combined with exact repository names (no wildcards). The controller uses the Tags List API to fetch all tags for the repository and filters them by the semver constraint. Example: registry=ghcr.io, filters=[\{image: "amdenterpriseai/aim-llama"\}], versions=[">=1.0.0"] will fetch all tags from docker.io/amdenterpriseai/aim-llama and include only those >=1.0.0. |  | Optional: \{\} |
| `maxModels` _integer_ | MaxModels is the maximum number of AIMClusterModel resources to create from this source. Once this limit is reached, no new models will be created, even if more matching images are discovered. Existing models are never deleted. This prevents runaway model creation from overly broad filters. | 100 | Maximum: 10000 <br />Minimum: 1 <br />Optional: \{\} |


#### AIMClusterModelSourceStatus



AIMClusterModelSourceStatus defines the observed state of AIMClusterModelSource.



_Appears in:_
- [AIMClusterModelSource](#aimclustermodelsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `status` _string_ | Status represents the overall state of the model source. |  | Enum: [Pending Starting Progressing Ready Running Degraded NotAvailable Failed] <br />Optional: \{\} |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastSyncTime is the timestamp of the last successful registry sync. Updated after each successful sync operation. |  | Optional: \{\} |
| `discoveredModels` _integer_ | DiscoveredModels is the count of AIMClusterModel resources managed by this source. Includes both existing and newly created models. |  | Optional: \{\} |
| `availableModels` _integer_ | AvailableModels is the total count of images discovered in the registry that match the filters. This may be higher than DiscoveredModels if maxModels limit was reached. |  | Optional: \{\} |
| `modelsLimitReached` _boolean_ | ModelsLimitReached indicates whether the maxModels limit has been reached. When true, no new models will be created even if more matching images are discovered. |  | Optional: \{\} |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest available observations of the source's state. Standard conditions: Ready, Syncing, RegistryReachable. |  | Optional: \{\} |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently observed spec. |  | Optional: \{\} |


#### AIMClusterRuntimeConfig



AIMClusterRuntimeConfig is a cluster-scoped runtime configuration for AIM services, models, and templates.

Cluster-scoped runtime configs provide platform-wide defaults that apply to all namespaces, making them ideal for organization-level policies such as storage classes, discovery behavior, model creation scope, and routing configuration.

When both cluster and namespace runtime configs exist with the same name, the configs are merged, and the namespace-scoped AIMRuntimeConfig takes precedence for any field that is set in both.



_Appears in:_
- [AIMClusterRuntimeConfigList](#aimclusterruntimeconfiglist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterRuntimeConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)_ |  |  |  |
| `status` _[AIMRuntimeConfigStatus](#aimruntimeconfigstatus)_ |  |  |  |


#### AIMClusterRuntimeConfigList



AIMClusterRuntimeConfigList contains a list of AIMClusterRuntimeConfig.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterRuntimeConfigList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterRuntimeConfig](#aimclusterruntimeconfig) array_ |  |  |  |


#### AIMClusterRuntimeConfigSpec



AIMClusterRuntimeConfigSpec defines cluster-wide defaults for AIM resources.



_Appears in:_
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `adapterRuntime` _[AIMAdapterRuntimeConfig](#aimadapterruntimeconfig)_ | AdapterRuntime configures LoRA adapter capacity for inference containers. Service values override namespace and cluster RuntimeConfig defaults. |  | Optional: \{\} |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `scaleFromZero` _[AIMScaleFromZeroConfig](#aimscalefromzeroconfig)_ | ScaleFromZero configures the activation metric query for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. When set on AIMService, these take highest precedence in the merge hierarchy. When set on RuntimeConfig, these provide namespace/cluster-level defaults. Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} |
| `additionalCABundleRef` _[AdditionalCABundleReference](#additionalcabundlereference)_ | AdditionalCABundleRef selects PEM CA certificates that AIM Engine adds to its standard public trust roots for outbound TLS from inference, artifact-download, size-check, and adapter-staging containers. The ConfigMap is resolved in each workload namespace. When configured on an AIMClusterRuntimeConfig, each consuming namespace needs a ConfigMap with the same name and key to receive the additional trust. AIM Engine checks the reference directly during reconciliation and omits the projection when the ConfigMap or key is absent. Certificate parsing occurs only in a workload init container. CA changes are consumed when workloads are recreated; the ConfigMap is not watched for rotation. |  | Optional: \{\} |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | DEPRECATED: The embedded Hugging Face-to-S3 artifact cache has been removed. This field is retained temporarily for API compatibility and is no longer honored by the controller. Use direct s3:// model sources with Artifact.S3 connection settings instead. |  | Optional: \{\} |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources. When enabled, labels matching the specified patterns are automatically copied from parent resources (e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs). This is useful for propagating organizational metadata like cost centers, team identifiers, or compliance labels through the resource hierarchy. |  | Optional: \{\} |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set, the value will be automatically migrated. |  | Optional: \{\} |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set, the value will be automatically migrated. |  | Optional: \{\} |
| `artifactStorageQuota` _[AIMArtifactStorageQuota](#aimartifactstoragequota)_ | ArtifactStorageQuota configures storage limits for AIMArtifacts. These limits control how much total PVC storage artifacts may consume, both cluster-wide and per-namespace. |  | Optional: \{\} |


#### AIMClusterServiceTemplate



AIMClusterServiceTemplate is a cluster-scoped template that defines runtime profiles for AIM services.

Cluster-scoped templates can be used by AIMServices in any namespace, making them ideal for platform-wide model configurations that should be shared across teams and projects. Unlike namespace-scoped AIMServiceTemplates, cluster templates do not support caching configuration and must be managed by cluster administrators, since caches themselves are namespace-scoped.

When both cluster and namespace templates exist with the same name, the namespace-scoped template takes precedence for services in that namespace.



_Appears in:_
- [AIMClusterServiceTemplateList](#aimclusterservicetemplatelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterServiceTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)_ |  |  |  |
| `status` _[AIMServiceTemplateStatus](#aimservicetemplatestatus)_ |  |  |  |


#### AIMClusterServiceTemplateList



AIMClusterServiceTemplateList contains a list of AIMClusterServiceTemplate.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMClusterServiceTemplateList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterServiceTemplate](#aimclusterservicetemplate) array_ |  |  |  |


#### AIMClusterServiceTemplateSpec



AIMClusterServiceTemplateSpec defines the desired state of AIMClusterServiceTemplate (cluster-scoped).

A cluster-scoped template that selects a runtime profile for a given AIM model.



_Appears in:_
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable. Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica. For GPU models, defines the GPU count and model types required for deployment. For CPU-only models, defines CPU resource requirements. This field is immutable after creation. |  | Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B"). Required when customProfile is set; used to assemble the profile YAML aim_id field and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8"). Required when customProfile is set; used for profile YAML model_id field and for weight pre-caching via the discovery job. |  | Optional: \{\} |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine. When set, the controller assembles a profile YAML from this data and template metadata, creates a ConfigMap, and mounts it into discovery and inference containers. Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images. These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults. For namespace-scoped templates, secrets must exist in the same namespace. For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template. This includes discovery dry-run jobs and inference services created from this template. If empty, the default service account for the namespace is used. |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template. Service-specific values override the template defaults. |  | Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template. When provided, the discovery dry-run will be skipped and these sources will be used directly. This allows users to explicitly declare model dependencies without requiring a discovery job. If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use. When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. These variables are passed to the inference runtime and can be used to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} |


#### AIMCpuRequirements



AIMCpuRequirements specifies CPU resource requirements.



_Appears in:_
- [AIMHardwareRequirements](#aimhardwarerequirements)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Requests is the number of CPU cores to request. Required and must be > 0. |  | Required: \{\} |
| `limits` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Limits is the maximum number of CPU cores to allow. |  | Optional: \{\} |


#### AIMCustomModelSpec



AIMCustomModelSpec contains configuration for custom models. These fields are only used when modelSources is specified (custom models). For image-based models, these settings come from discovery.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies default hardware requirements for all templates. Individual templates can override these defaults. Required when modelSources is set and customTemplates is empty (unless aimId is set). |  | Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies default type for all templates. Individual templates can override this default. When nil, templates default to "unoptimized". |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `versionPolicy` _[AIMVersionPolicy](#aimversionpolicy)_ | VersionPolicy controls how template versions are filtered during aimId-based matching.<br />- pinned (default): match templates whose status.version equals the model's image tag<br />- latest: match only templates at the newest available status.version<br />- all: match templates at any version<br />- any: deprecated alias of all, kept for backward compatibility<br />Only used when spec.aimId is set. | pinned | Enum: [pinned latest any all] <br />Optional: \{\} |


#### AIMCustomProfile



AIMCustomProfile defines inline custom profile data for user-provided inference engine configuration. When set on a template, the controller assembles a profile YAML, creates a ConfigMap, and mounts it into both the discovery job and the inference service container.



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine arguments as a free-form JSON object. These are passed as CLI arguments to the inference engine (e.g., vLLM). Do not include "model" — it is injected separately by the runtime. |  | Schemaless: \{\} <br />Optional: \{\} |
| `envVars` _object (keys:string, values:string)_ | EnvVars contains environment variables applied to the inference engine process. These are written into the profile YAML and applied by the AIM runtime via os.execv, distinct from container-level Env which targets the AIM runtime container itself. Keys must match ^[A-Z0-9_]+$ (uppercase with underscores). |  | Optional: \{\} |


#### AIMCustomTemplate



AIMCustomTemplate defines a custom template configuration for a model. When modelSources are specified directly on AIMModel, customTemplates allow defining explicit hardware requirements and profiles, skipping the discovery job. This is an existing struct (not a CRD); it appears as an element of AIMModel.spec.customTemplates[].



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the template name. If not provided, auto-generated from model name + profile. |  | MaxLength: 63 <br />Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization status of this template.<br />- optimized: Template has been tuned for performance<br />- general: General-purpose tuning between optimized and preview<br />- preview: Template is experimental/pre-release<br />- unoptimized: Default, no specific optimizations applied | unoptimized | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variable overrides when this template is selected. These are container-level env vars applied to the AIM runtime container. |  | MaxItems: 64 <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for this template. Optional when spec.hardware is set (inherits from spec). When both are set, values are merged field-by-field with template taking precedence. |  | Optional: \{\} |
| `profile` _[AIMTemplateProfile](#aimtemplateprofile)_ | Profile declares runtime profile variables for template selection. Used when multiple templates exist to select based on metric/precision. |  | Optional: \{\} |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B"). Required when customProfile is set. |  | Optional: \{\} |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8"). Required when customProfile is set. |  | Optional: \{\} |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine. When set, the resulting template will have a custom profile ConfigMap mounted. Requires aimId, modelId, hardware, profile.metric, and profile.precision. |  | Optional: \{\} |


#### AIMDiscoveredProfile



AIMDiscoveredProfile contains the cached discovery results for a template. This is the processed and validated version of AIMDiscoveryProfile that is stored in the template's status after successful discovery.

The profile serves as a cache of runtime configuration, eliminating the need to re-run discovery for each service that uses this template. Services and caching mechanisms reference this cached profile for deployment parameters and model sources.

See discovery.go for AIMDiscoveryProfile (the raw discovery output) and the relationship between these types.



_Appears in:_
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine_args` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains runtime-specific engine configuration as a free-form JSON object. The structure depends on the inference engine being used (e.g., vLLM, TGI). These arguments are passed to the runtime container to configure model loading and inference. |  | Schemaless: \{\} |
| `env_vars` _object (keys:string, values:string)_ | EnvVars contains environment variables required by the runtime for this profile. These may include engine-specific settings, optimization flags, or hardware configuration. |  | Optional: \{\} |
| `metadata` _[AIMProfileMetadata](#aimprofilemetadata)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `originalDiscoveryOutput` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | OriginalDiscoveryOutput contains the raw discovery job JSON output. This preserves the complete discovery result from the dry-run container, including all fields that may not be mapped to structured fields above. |  | Schemaless: \{\} <br />Optional: \{\} |




#### AIMDiscoveryProfileMetadata



AIMDiscoveryProfileMetadata describes the characteristics of a discovered deployment profile.



_Appears in:_
- [AIMDiscoveryProfile](#aimdiscoveryprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  | Optional: \{\} |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  | Optional: \{\} |
| `gpu_count` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  | Optional: \{\} |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type specifies the optimization level of this profile (optimized, unoptimized, preview). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |


#### AIMDownloadFilter



AIMDownloadFilter controls which files are included or excluded during artifact downloads. Patterns use fnmatch-style glob syntax applied against relative file paths in the repository. Both the size estimator and downloader apply the same filter, ensuring PVC sizing matches the actual download.

Filter order (matching huggingface_hub behavior):
 1. Include: if set, only files matching at least one include pattern are considered
 2. Exclude: files matching any exclude pattern are then removed

When no filter is configured (neither on the artifact nor in the runtime config), subdirectory files are excluded by default (equivalent to exclude: ["*/*"]). To download all files including subdirectories, set an empty filter: downloadFilter: {}.



_Appears in:_
- [AIMArtifactSpec](#aimartifactspec)
- [AIMStorageConfig](#aimstorageconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `include` _string array_ | Include specifies glob patterns for files to download. Only files matching at least one pattern are considered. If empty, all files pass the include check. Patterns use fnmatch syntax (e.g., ["*.safetensors", "config.json"]). |  | Optional: \{\} |
| `exclude` _string array_ | Exclude specifies glob patterns for files to skip. Files matching any exclude pattern are removed after include filtering. Patterns use fnmatch syntax (e.g., ["*/*", "*.bin"]). Use ["*/*"] to exclude all files in subdirectories (the default when no filter is set). |  | Optional: \{\} |


#### AIMGpuRequirements



AIMGpuRequirements specifies GPU resource requirements.



_Appears in:_
- [AIMHardwareRequirements](#aimhardwarerequirements)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requests` _integer_ | Requests is the number of GPUs to set as requests/limits. Set to 0 to target GPU nodes without consuming GPU resources (useful for testing). |  | Minimum: 0 <br />Optional: \{\} |
| `model` _string_ | Model limits deployment to a specific GPU model. Example: "MI300X" Cannot be combined with minVram. |  | MaxLength: 64 <br />Optional: \{\} |
| `minVram` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | MinVRAM limits deployment to GPUs having at least this much VRAM. Used for capacity planning when the model size is known but any GPU with sufficient VRAM is acceptable. Cannot be combined with model. |  | Optional: \{\} |
| `resourceName` _string_ | ResourceName is the Kubernetes resource name for GPU resources. Defaults to "amd.com/gpu" if not specified. | amd.com/gpu | Optional: \{\} |


#### AIMHardwareRequirements



AIMHardwareRequirements specifies compute resource requirements for custom models. Used in AIMModelSpec and AIMCustomTemplate to define GPU and CPU needs.



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
| `gpu` _[AIMGpuRequirements](#aimgpurequirements)_ | GPU specifies GPU requirements. If not set, no GPUs are requested (CPU-only model). |  | Optional: \{\} |
| `cpu` _[AIMCpuRequirements](#aimcpurequirements)_ | CPU specifies CPU requirements. |  | Optional: \{\} |


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
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMModelAcceleratorRequest



AIMModelAcceleratorRequest is the per-model hardware request for modelId-backed onboarding. RuntimeConfig fallbacks are cluster policy — they describe which runtime image serves which class of hardware — so the shape of one model's deployment (how many devices it needs, and optionally which card) belongs here rather than on the fallback.

Each field plays two roles: it narrows which fallbacks apply (a fallback matches when its own match.* is empty or equal to the request) and it is stamped onto the generated profile. Fields left empty inherit the matched fallback's value.

Only valid alongside spec.modelId. Image-backed models get their hardware from discovery, and narrowing which discovered profile a deployment picks is AIMService.spec.profile.selector's job.



_Appears in:_
- [AIMClusterModelSourceModel](#aimclustermodelsourcemodel)
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vendor` _[AcceleratorVendor](#acceleratorvendor)_ | Vendor requests a specific accelerator vendor. |  | Enum: [amd nvidia] <br />Optional: \{\} |
| `model` _string_ | Model requests an exact detector accelerator model (e.g. "MI300X", "H100"). Empty matches any model the fallback supports. |  | MaxLength: 64 <br />Optional: \{\} |
| `count` _integer_ | Count is the number of accelerator units the model needs. It drives the generated profile's acceleratorCount, and therefore both the device resource request and the engine's tensor-parallel size. Defaults to 1. |  | Minimum: 0 <br />Optional: \{\} |
| `partitioningMode` _string_ | PartitioningMode requests a GPU partitioning mode. See AcceleratorPartitioningMode on AIMProfileSpecCommon for the reserved values ("unpartitioned", "partitioned", "<C>-<M>"). |  | Optional: \{\} |


#### AIMModelConfig







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `autoDiscovery` _boolean_ | AutoDiscovery controls whether models run discovery by default. When true, models run discovery jobs to extract metadata and auto-create templates. When false, discovery is skipped. Discovery failures are non-fatal and reported via conditions. |  | Optional: \{\} |
| `profileGeneration` _[AIMProfileGenerationConfig](#aimprofilegenerationconfig)_ | ProfileGeneration configures generic runtime fallbacks used to generate profiles for v1alpha2 modelId-backed models. |  | Optional: \{\} |


#### AIMModelDiscoveryConfig



AIMModelDiscoveryConfig controls discovery behavior for a model.

The bool fields are pointers so the schema can distinguish "unset" from explicit false. With a plain bool + omitempty + default=true, the API server's OpenAPI defaulter cannot tell an explicit false apart from a missing field (both look like the Go zero value) and silently rewrites the explicit false to true. Pointers preserve the user's intent.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `extractMetadata` _boolean_ | ExtractMetadata controls whether metadata extraction runs for this model. During metadata extraction, the controller connects to the image registry and extracts the image's labels. | true | Optional: \{\} |
| `createServiceTemplates` _boolean_ | CreateServiceTemplates controls whether (cluster) service templates are auto-created from the image metadata. | true | Optional: \{\} |


#### AIMModelKind

_Underlying type:_ _string_

AIMModelKind classifies the v1alpha2 AIMModel onboarding flow that produced this model's profiles. Populated by the v1alpha2 controller during reconciliation from the model's spec shape.

The four kinds correspond 1:1 to the onboarding flows documented in concepts/models.md:

  - Image    — spec.image is set; discovery supplies the source definitions. Includes official AIM images, private images, and generic base images.
  - Derived  — spec.profiles.derivedFrom without spec.image, with selector.role unset or "deployable"; profiles are re-derived from another deployable model's profiles (e.g. fine-tunes that swap weights but keep the original model's architecture, runtime, and accelerator shapes).
  - Custom   — spec.profiles.derivedFrom without spec.image, with selector.role=base; profiles are derived by overlaying BYO weights + target identity onto a base image's generic base profiles.
  - Generated — spec.modelId is set; profiles are generated from matching RuntimeConfig fallbacks and observed cluster hardware.

Empty when the spec hasn't been classified yet (controller hasn't reconciled) or when the spec shape doesn't match any onboarding flow (a misconfigured spec the CEL validators didn't catch).

_Validation:_
- Enum: [Image Derived Custom Generated]

_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description |
| --- | --- |
| `Image` |  |
| `Derived` |  |
| `Custom` |  |
| `Generated` |  |


#### AIMModelList



AIMModelList contains a list of AIMModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMModel](#aimmodel) array_ |  |  |  |


#### AIMModelProfileGenerationStatus



AIMModelProfileGenerationStatus reports the resolution of a Generated model.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `strategy` _[ProfileGenerationStrategy](#profilegenerationstrategy)_ | Strategy is the mechanism that produced the profiles. |  | Enum: [RuntimeFallback] <br />Optional: \{\} |
| `matchedFallbacks` _string array_ | MatchedFallbacks lists the RuntimeConfig fallback names that produced a profile, in the order the profiles are named. Empty when no fallback matched the model's hardware request. |  | Optional: \{\} |


#### AIMModelProfilesDerivedFrom



AIMModelProfilesDerivedFrom selects source profile definitions. Selection uses published model definitions and existing copyable profiles, or an explicit alternate catalog.



_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `selector` _[ProfileSelector](#profileselector)_ | Selector filters source definitions before overrides. The default role is deployable; role=base selects base definitions. |  | Optional: \{\} |
| `sourceRef` _[ProfileSourceRef](#profilesourceref)_ | SourceRef selects an alternate catalog ConfigMap in place of the default model-definition and copyable-profile pool. Mutually exclusive with selector.modelRef; other selector fields may filter the catalog contents. |  | Optional: \{\} |


#### AIMModelProfilesSpec



AIMModelProfilesSpec selects and transforms profiles through a model-owned AIMProfileSet. derivedFrom is required and selects external definitions using versionPolicy and version. It cannot be combined with the model's spec.image.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `derivedFrom` _[AIMModelProfilesDerivedFrom](#aimmodelprofilesderivedfrom)_ | DerivedFrom selects source definitions and profiles. |  |  |
| `versionPolicy` _[ProfileVersionPolicy](#profileversionpolicy)_ | VersionPolicy controls matching source versions (defaults to pinned). | pinned | Enum: [pinned latest all] <br />Optional: \{\} |
| `version` _string_ | Version pins matching to a specific source profile version when VersionPolicy is `pinned`. |  | Optional: \{\} |
| `overrides` _[ProfileOverrides](#profileoverrides)_ | Overrides mutates the copied profile spec after selection and version filtering. Use overrides.image to override the deployment container image used by the derived profiles. |  | Optional: \{\} |
| `outputPolicy` _[ProfileOutputPolicy](#profileoutputpolicy)_ | OutputPolicy controls whether profiles produced for this model may be used as sources for subsequent derivations. |  | Optional: \{\} |


#### AIMModelSource



AIMModelSource describes a model artifact that must be downloaded for inference. Discovery extracts these from the container's configuration to enable caching and validation.



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
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format. Determines the cache mount path: /workspace/cache/\{modelId\} For HuggingFace sources, this typically mirrors the URI path (e.g., meta-llama/Llama-3-8B). For S3 sources, users define their own organizational structure. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Required: \{\} |
| `sourceUri` _string_ | SourceURI is the location from which the model should be downloaded. Supported schemes:<br />- hf://org/model - Hugging Face Hub model<br />- s3://bucket/key - S3-compatible storage |  | Pattern: `^(hf\|s3)://[^ \t\r\n]+$` |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Size is the expected storage space required for this model artifact. Used for PVC sizing and capacity planning during cache creation. Optional - if not specified, the download job will discover the size automatically. Can be set explicitly to pre-allocate storage or override auto-discovery. |  | Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision describes the runtime precision this source is compatible with. Used to match model sources to profiles during custom weight onboarding. |  | Enum: [fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies per-source credential overrides. These variables are used for authentication when downloading this specific source. Takes precedence over base-level env for the same variable name. |  | Optional: \{\} |


#### AIMModelSourceLocation



AIMModelSourceLocation describes the location and optional metadata for a v1alpha2 modelId-backed model. The modelId itself lives on AIMModelSpec so users do not have to repeat it in both the model and source declarations.



_Appears in:_
- [AIMClusterModelSourceModel](#aimclustermodelsourcemodel)
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `uri` _string_ | URI is the location from which model weights are downloaded. When omitted, modelId-backed models default to hf://<modelId>. |  | Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br />Optional: \{\} |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | Size is the expected model artifact size. |  | Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision describes the runtime precision of the source weights. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies source-specific credential overrides. |  | Optional: \{\} |


#### AIMModelSourceType

_Underlying type:_ _string_

AIMModelSourceType indicates how a model's artifacts are sourced.

Only set by the v1alpha1 controller, which lumps fine-tunes and custom models together as "Custom" — losing the distinction users actually care about. The v1alpha2 controller intentionally does not populate this field on v1alpha2-shaped specs; v1alpha2 consumers should read AIMModelStatus.Kind instead, which classifies Image, Generated, Derived, and Custom onboarding.

_Validation:_
- Enum: [Image Custom]

_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description |
| --- | --- |
| `Image` | AIMModelSourceTypeImage indicates the model is discovered from container image labels. |
| `Custom` | AIMModelSourceTypeCustom indicates the model uses explicit spec.modelSources. |


#### AIMModelSpec



AIMModelSpec defines the desired state of AIMModel.

v1alpha2 accepts exactly one of image, profiles, or modelId. v1alpha1 forbids derivedFrom and profiles. v1alpha2 forbids profileCopy/custom/customTemplates/top-level modelSources on new resources and rejects profiles mixed with legacy scalar fields.



_Appears in:_
- [AIMClusterModel](#aimclustermodel)
- [AIMModel](#aimmodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the container image URI for this AIM model. This image is inspected by the operator to select runtime profiles used by templates. Discovery behavior is controlled by the discovery field and runtime config's AutoDiscovery setting. Required unless modelId, an eligible aimId-only flow, or a profile derivation flow is set. |  | Optional: \{\} |
| `modelId` _string_ | ModelID is the canonical model identifier for generated-profile onboarding. The controller combines it with RuntimeConfig profile generation fallbacks and observed hardware to materialize profiles. |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Optional: \{\} |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "qwen/qwen3-32b"). When set together with modelSources, enables aimId-based template matching: the controller finds official templates by aimId, filters by versionPolicy, matches by modelId, and creates copies with the custom weight source. |  | Optional: \{\} |
| `source` _[AIMModelSourceLocation](#aimmodelsourcelocation)_ | Source optionally overrides the default hf://<modelId> model source for modelId-backed onboarding. |  | Optional: \{\} |
| `accelerator` _[AIMModelAcceleratorRequest](#aimmodelacceleratorrequest)_ | Accelerator requests the hardware shape for modelId-backed onboarding — how many accelerator units the model needs and, optionally, which vendor, card model, or partitioning mode. Narrows which RuntimeConfig fallbacks apply and is stamped onto the generated profile. Defaults to one unit of whatever the matched fallback targets. |  | Optional: \{\} |
| `profileCopy` _[AIMProfileSetSpec](#aimprofilesetspec)_ | ProfileCopy reuses the AIMProfileSet derivation shape so an AIMModel can publish derivative AIMProfiles directly. The controller may synthesize a child AIMProfileSet and fill SourceRef when image-backed discovery is involved. Mutually exclusive with all deprecated v1alpha1 fields. DEPRECATED on v1alpha2: use spec.profiles. v1alpha1 still accepts ProfileCopy. v1alpha2 CRD CEL forbids ProfileCopy. |  | Optional: \{\} |
| `derivedFrom` _[AIMProfileSetSpec](#aimprofilesetspec)_ | DerivedFrom is the legacy flat shape of v1alpha2's profile-derivation onboarding surface. New objects must use spec.profiles instead; the field is retained so existing v1alpha2 objects (and the v1alpha2 reconciler reading them) round-trip cleanly. DEPRECATED: prefer spec.profiles.derivedFrom on v1alpha2. |  | Optional: \{\} |
| `profiles` _[AIMModelProfilesSpec](#aimmodelprofilesspec)_ | Profiles selects definitions and applies overrides through a child AIMProfileSet. derivedFrom selects external sources and is required. Exclusive with spec.image and spec.modelId. v1alpha1 rejects spec.profiles. |  | Optional: \{\} |
| `discovery` _[AIMModelDiscoveryConfig](#aimmodeldiscoveryconfig)_ | Discovery controls discovery behavior for this model. When unset, uses runtime config defaults. |  | Optional: \{\} |
| `defaultServiceTemplate` _string_ | DefaultServiceTemplate specifies the default AIMServiceTemplate to use when creating services for this model. When set, services that reference this model will use this template if no template is explicitly specified. If this is not set, a template will be automatically selected. |  | Optional: \{\} |
| `custom` _[AIMCustomModelSpec](#aimcustommodelspec)_ | Custom contains configuration for custom models (models with inline modelSources). Only used when modelSources are specified; ignored for image-based models. |  | Optional: \{\} |
| `customTemplates` _[AIMCustomTemplate](#aimcustomtemplate) array_ | CustomTemplates defines explicit template configurations for this model. These templates are created directly without running a discovery job. Can be used with or without modelSources to define custom deployment configurations. If omitted when modelSources is set, a single template is auto-generated using the custom.hardware requirements. |  | MaxItems: 16 <br />Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use for this model. When specified, these sources are used instead of auto-discovery from the container image. This enables pre-creating custom models with explicit model sources. The size field is optional - if not specified, it will be discovered by the download job. AIM runtime currently supports only one model source. |  | MaxItems: 1 <br />Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling the model container image. These secrets are used for:<br />- OCI registry metadata extraction during discovery<br />- Pulling the image for inference services<br />The secrets are merged with any runtime config defaults. For namespace-scoped models, secrets must exist in the same namespace. For cluster-scoped models, secrets must exist in the operator namespace. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for authentication during model discovery and metadata extraction. These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model. This includes metadata extraction jobs and any other model-related operations. If empty, the default service account for the namespace is used. |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources defines the default resource requirements for services using this model. Template- or service-level values override these defaults. |  | Optional: \{\} |
| `imageMetadata` _[ImageMetadata](#imagemetadata)_ | ImageMetadata is the metadata that is used to determine which recommended service templates to create, and to drive clients with richer metadata regarding this particular model. For most cases the user does not need to set this field manually, for images that have the supported labels embedded in them the `AIM(Cluster)Model.status.imageMetadata` field is automatically filled from the container image labels. This field is intended to be used when there are network restrictions, or in other similar situations. If this field is set, the remote extraction will not be performed at all. |  |  |


#### AIMModelStatus



AIMModelStatus defines the observed state of AIMModel.



_Appears in:_
- [AIMClusterModel](#aimclustermodel)
- [AIMModel](#aimmodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the overall status of the image based on its templates | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest available observations of the model's state |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} |
| `imageMetadata` _[ImageMetadata](#imagemetadata)_ | ImageMetadata is the metadata extracted from an AIM image |  | Optional: \{\} |
| `sourceType` _[AIMModelSourceType](#aimmodelsourcetype)_ | SourceType indicates how this model's artifacts are sourced.<br />- "Image": Model discovered from container image labels<br />- "Custom": Model uses explicit spec.modelSources<br />Set by the controller based on whether spec.modelSources is populated. Note: only populated by the v1alpha1 controller; v1alpha2 consumers should read .status.kind instead, which distinguishes fine-tunes (Derived) from BYO base-image overlays (Custom). |  | Enum: [Image Custom] <br />Optional: \{\} |
| `kind` _[AIMModelKind](#aimmodelkind)_ | Kind classifies the v1alpha2 onboarding flow that produced this model's profiles (Image / Generated / Derived / Custom). See AIMModelKind for the per-value semantics. Populated by the v1alpha2 controller from the spec shape; left empty by the v1alpha1 controller. |  | Enum: [Image Derived Custom Generated] <br />Optional: \{\} |
| `profileGeneration` _[AIMModelProfileGenerationStatus](#aimmodelprofilegenerationstatus)_ | ProfileGeneration reports how a Generated model's profiles were resolved. Empty for every other kind. |  | Optional: \{\} |
| `aimId` _string_ | AimId is the resolved model architecture identifier for this model. Populated by the v1alpha2 controller from spec.aimId or discovered metadata. |  | Optional: \{\} |
| `baseImage` _string_ | BaseImage is the extracted AIM base image reference (AIM_BASE_IMAGE_REF) when known. Used when resolving deployment images for fine-tuned models that have no spec.image. |  | Optional: \{\} |
| `version` _string_ | Version is the effective image version of the model. Populated from the spec.image tag (`amdenterpriseai/aim-qwen-qwen3-32b:0.11.0` → `0.11.0`) during reconciliation. Empty for models that have no spec.image (e.g. fine-tuned models derived from a parent) or that reference an image by digest. This mirrors AIMProfileStatus.Version so the two surfaces stay in lock-step: a single image-tag extraction rule governs what users see in the kubectl printcolumn for both kinds. The image-author-declared version (i.e. the `org.opencontainers.image.version` OCI label) is preserved separately under .status.imageMetadata.oci.version for users that want to inspect what the image build pipeline stamped. The two values usually agree; when the OCI label is missing or empty, this field still surfaces a useful version from the tag itself. |  | Optional: \{\} |
| `discoveryCacheRef` _[DiscoveryCacheReference](#discoverycachereference)_ | DiscoveryCacheRef points to the raw profile YAML and metadata cache ConfigMap for image-backed flows. Populated after image inspection succeeds. |  | Optional: \{\} |
| `discoveredProfiles` _[DiscoveredProfileCounts](#discoveredprofilecounts)_ | DiscoveredProfiles summarizes raw discovered definitions and their hardware support. |  | Optional: \{\} |
| `profileSetRef` _[ProfileSetReference](#profilesetreference)_ | ProfileSetRef identifies the child profile set created when spec.profiles is configured. |  | Optional: \{\} |
| `managedProfiles` _[ManagedProfileCounts](#managedprofilecounts)_ | ManagedProfiles summarizes actual managed outputs. Standalone sets' outputs are counted by those sets. |  | Optional: \{\} |
| `discovery` _[ModelDiscoveryState](#modeldiscoverystate)_ | Discovery tracks the state of the image-discovery Job used to populate the discovery cache. |  | Optional: \{\} |


#### AIMPrecision

_Underlying type:_ _string_

AIMPrecision enumerates supported numeric precisions

_Validation:_
- Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMModelSource](#aimmodelsource)
- [AIMModelSourceLocation](#aimmodelsourcelocation)
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
| `fp64` |  |
| `bf16` |  |
| `int4` |  |
| `int8` |  |


#### AIMProfileAutoSelectionPolicy

_Underlying type:_ _string_

AIMProfileAutoSelectionPolicy controls which optimization tiers may participate in automatic selection when the AIMService does not explicitly set selector.minimumType.

_Validation:_
- Enum: [optimized any]

_Appears in:_
- [AIMProfileGenerationFallback](#aimprofilegenerationfallback)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileAutoSelectionPolicyOptimized applies the implicit optimized minimumType floor. |
| `any` | AIMProfileAutoSelectionPolicyAny permits a profile of any optimization tier to participate in a second selection pass when no optimized candidate matched and the AIMService did not explicitly set selector.minimumType. |


#### AIMProfileGenerationConfig



AIMProfileGenerationConfig controls profile generation for modelId-backed AIMModel and AIMClusterModel resources.



_Appears in:_
- [AIMModelConfig](#aimmodelconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `fallbacks` _[AIMProfileGenerationFallback](#aimprofilegenerationfallback) array_ | Fallbacks are runtime templates selected by observed cluster hardware. Namespace entries replace cluster entries with the same name. |  | Optional: \{\} |


#### AIMProfileGenerationFallback



AIMProfileGenerationFallback describes one hardware match and runtime template used to generate a self-contained AIMProfile.



_Appears in:_
- [AIMProfileGenerationConfig](#aimprofilegenerationconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the stable fallback identity and merge key. |  | MaxLength: 63 <br />MinLength: 1 |
| `priority` _integer_ | Priority selects the primary profile when multiple fallbacks match. Higher values win. |  | Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type is the optimization tier stamped on the generated profile. Generated profiles default to "unoptimized" because a generic runtime template is not tuned for any particular model. AutoSelectionPolicy controls whether a lower-tier generated profile can serve as an automatic fallback. | unoptimized | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `autoSelectionPolicy` _[AIMProfileAutoSelectionPolicy](#aimprofileautoselectionpolicy)_ | AutoSelectionPolicy is stamped on the generated profile. "optimized" keeps the profile subject to the implicit optimized minimumType floor. "any" permits it to participate in a second automatic-selection pass when no optimized candidate matched and the service did not explicitly set minimumType. | optimized | Enum: [optimized any] <br />Optional: \{\} |
| `match` _[AIMProfileGenerationMatch](#aimprofilegenerationmatch)_ | Match constrains the hardware on which this fallback is applicable. |  |  |
| `runtime` _[AIMProfileGenerationRuntime](#aimprofilegenerationruntime)_ | Runtime is copied into the generated profile. |  |  |


#### AIMProfileGenerationMatch



AIMProfileGenerationMatch identifies compatible accelerator hardware.



_Appears in:_
- [AIMProfileGenerationFallback](#aimprofilegenerationfallback)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `acceleratorVendor` _[AcceleratorVendor](#acceleratorvendor)_ | AcceleratorVendor selects the hardware vendor. |  | Enum: [amd nvidia] |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType selects GPU or CPU hardware. |  | Enum: [gpu cpu] |
| `acceleratorModel` _string_ | AcceleratorModel optionally requires an exact detector model label. |  | Optional: \{\} |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode optionally requires a partitioning mode. |  | Optional: \{\} |


#### AIMProfileGenerationRuntime



AIMProfileGenerationRuntime contains the profile runtime fields controlled by cluster or namespace policy.



_Appears in:_
- [AIMProfileGenerationFallback](#aimprofilegenerationfallback)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the serving container image. |  | MinLength: 1 |
| `engine` _string_ | Engine identifies the inference engine, for example vllm. |  | MinLength: 1 |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs overrides or extends generated engine CLI arguments. |  | Schemaless: \{\} <br />Optional: \{\} |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains inference-engine subprocess environment variables. |  | Optional: \{\} |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | ContainerEnv contains pod-level environment variables. |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources overrides generated Kubernetes resource requirements. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets contains credentials for pulling the serving image. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName selects the serving workload service account. |  | Optional: \{\} |


#### AIMProfileMetadata



AIMProfileMetadata describes the characteristics of a cached deployment profile. This is identical to AIMDiscoveryProfileMetadata but exists in the template status namespace.



_Appears in:_
- [AIMDiscoveredProfile](#aimdiscoveredprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimID is the model-family identifier from the profile YAML's top-level `aim_id`. Used for cross-scope fine-tune template matching (AIMModel.spec.aimId -> template.spec.aimId). |  | Optional: \{\} |
| `modelId` _string_ | ModelID is the per-profile model variant identifier from the profile YAML's top-level `model_id`. |  | Optional: \{\} |
| `engine` _string_ | Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi"). |  | Optional: \{\} |
| `gpu` _string_ | GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X"). |  | Optional: \{\} |
| `gpuCount` _integer_ | GPUCount indicates how many GPUs are required per replica for this profile. |  | Optional: \{\} |
| `metric` _[AIMMetric](#aimmetric)_ | Metric indicates the optimization goal for this profile ("latency" or "throughput"). |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8"). |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this profile (optimized, preview, unoptimized). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |


#### AIMProfileSetSpec



AIMProfileSetSpec defines the desired state of AIMProfileSet and is also reused by AIMModel.profileCopy.

The selector-non-empty rule accepts every documented narrowing field (including modelRef and origin) and lets sourceRef-only specs through: when sourceRef is set, the discovery cache itself acts as the source scope and a separate selector is not required.

Default selection combines image-backed model definitions with existing copyable profile resources. A model definition and its materialised profile represent one logical source. Namespace sets use fully matching local candidates, falling back to cluster sources only when no local match exists; cluster sets use cluster sources only.

Partitioning overrides require both selector.acceleratorCount and overrides.acceleratorCount.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceRef` _[ProfileSourceRef](#profilesourceref)_ | SourceRef points to an alternate discovery cache source. When omitted, derivation uses published image-model definitions and visible copyable AIMProfile and AIMClusterProfile objects, with namespace-first fallback. For AIMProfileSet, the referenced ConfigMap is read from the same namespace. For AIMClusterProfileSet, it is read from the operator namespace. Mutually exclusive with selector.modelRef; other selector fields may filter the catalog contents. |  | Optional: \{\} |
| `selector` _[ProfileSelector](#profileselector)_ | Selector chooses which source profiles to derive from. |  | Optional: \{\} |
| `versionPolicy` _[ProfileVersionPolicy](#profileversionpolicy)_ | VersionPolicy controls how matching profiles are filtered by version. | pinned | Enum: [pinned latest all] <br />Optional: \{\} |
| `version` _string_ | Version pins matching to a specific source profile version when VersionPolicy is pinned. |  | Optional: \{\} |
| `image` _string_ | Image overrides the runtime image used by the derived profiles. |  | Optional: \{\} |
| `overrides` _[ProfileOverrides](#profileoverrides)_ | Overrides mutates the copied profile spec after selection and version filtering. |  | Optional: \{\} |
| `outputPolicy` _[ProfileOutputPolicy](#profileoutputpolicy)_ | OutputPolicy controls whether profiles produced by this set may be used as sources for subsequent derivations. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets used for inspecting and pulling container images. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName is propagated to managed profiles for downstream workloads. |  | Optional: \{\} |




#### AIMProfileType

_Underlying type:_ _string_

AIMProfileType indicates the optimization level of a deployment profile. Hierarchy: optimized > general > preview > unoptimized.

_Validation:_
- Enum: [optimized general preview unoptimized]

_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMCustomModelSpec](#aimcustommodelspec)
- [AIMCustomTemplate](#aimcustomtemplate)
- [AIMDiscoveryProfileMetadata](#aimdiscoveryprofilemetadata)
- [AIMProfileGenerationFallback](#aimprofilegenerationfallback)
- [AIMProfileMetadata](#aimprofilemetadata)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileTypeOptimized indicates the profile has been fully optimized. |
| `general` | AIMProfileTypeGeneral indicates a general-purpose profile (between optimized and preview). |
| `preview` | AIMProfileTypePreview indicates the profile is in preview/beta state. |
| `unoptimized` | AIMProfileTypeUnoptimized indicates the profile has not been optimized. |
| `any` | AIMProfileTypeAny is a selector-only sentinel meaning "no optimization floor — accept every tier". It is never stamped on a profile's own spec.type; it is only valid as a ProfileSelector.minimumType value, where it disables the floor (equivalent to flooring at unoptimized, the lowest tier, but reads as intent rather than asking specifically for unoptimized). |


#### AIMProfileTypeFloor

_Underlying type:_ _string_

AIMProfileTypeFloor enumerates the values accepted by a profile selector's minimumType floor: the real optimization tiers plus the selector-only "any" sentinel that disables the floor.

It is a distinct type from AIMProfileType on purpose. MinimumType cannot just be an AIMProfileType: that type's own enum constrains a profile's spec.type to the four real tiers (a profile is never "any"), and a field whose type already declares an enum cannot widen it — controller-gen emits an allOf of the two enums, whose intersection silently drops "any" and makes the documented sentinel un-settable. A separate type carries the correct 5-value enum for the floor while leaving spec.type constrained to real tiers.

_Validation:_
- Enum: [optimized general preview unoptimized any]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `optimized` | AIMProfileTypeFloorOptimized floors selection at optimized (the default for AIMService auto-selection). |
| `general` | AIMProfileTypeFloorGeneral floors selection at general. |
| `preview` | AIMProfileTypeFloorPreview floors selection at preview. |
| `unoptimized` | AIMProfileTypeFloorUnoptimized floors selection at unoptimized (the lowest real tier — admits every typed profile). |
| `any` | AIMProfileTypeFloorAny disables the floor entirely (accept every tier, including untyped/unknown). |


#### AIMResolutionScope

_Underlying type:_ _string_

AIMResolutionScope describes the scope of a resolved reference.

_Validation:_
- Enum: [Namespace Cluster Merged Unknown]

_Appears in:_
- [AIMResolvedReference](#aimresolvedreference)

| Field | Description |
| --- | --- |
| `Namespace` | AIMResolutionScopeNamespace denotes a namespace-scoped resource. |
| `Cluster` | AIMResolutionScopeCluster denotes a cluster-scoped resource. |
| `Merged` | AIMResolutionScopeMerged denotes that both cluster and namespace configs were merged. |
| `Unknown` | AIMResolutionScopeUnknown denotes that the scope could not be determined. |


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
| `namespace` _string_ | Namespace identifies where the resource was found when namespace-scoped. Empty indicates a cluster-scoped resource. |  |  |
| `scope` _[AIMResolutionScope](#aimresolutionscope)_ | Scope indicates whether the resolved resource was namespace or cluster scoped. |  | Enum: [Namespace Cluster Merged Unknown] |
| `kind` _string_ | Kind is the fully-qualified kind of the resolved reference, when known. |  | Optional: \{\} |
| `uid` _[UID](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#uid-types-pkg)_ | UID captures the unique identifier of the resolved reference, when known. |  | Optional: \{\} |


#### AIMRuntimeConfig



AIMRuntimeConfig is the Schema for namespace-scoped AIM runtime configurations.



_Appears in:_
- [AIMRuntimeConfigList](#aimruntimeconfiglist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMRuntimeConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMRuntimeConfigSpec](#aimruntimeconfigspec)_ |  |  |  |
| `status` _[AIMRuntimeConfigStatus](#aimruntimeconfigstatus)_ |  |  |  |


#### AIMRuntimeConfigCommon



AIMRuntimeConfigCommon captures configuration fields shared across cluster and namespace scopes. These settings apply to both AIMRuntimeConfig (namespace-scoped) and AIMClusterRuntimeConfig (cluster-scoped). It embeds AIMServiceRuntimeConfig which contains fields that can also be overridden at the service level.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `adapterRuntime` _[AIMAdapterRuntimeConfig](#aimadapterruntimeconfig)_ | AdapterRuntime configures LoRA adapter capacity for inference containers. Service values override namespace and cluster RuntimeConfig defaults. |  | Optional: \{\} |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `scaleFromZero` _[AIMScaleFromZeroConfig](#aimscalefromzeroconfig)_ | ScaleFromZero configures the activation metric query for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. When set on AIMService, these take highest precedence in the merge hierarchy. When set on RuntimeConfig, these provide namespace/cluster-level defaults. Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} |
| `additionalCABundleRef` _[AdditionalCABundleReference](#additionalcabundlereference)_ | AdditionalCABundleRef selects PEM CA certificates that AIM Engine adds to its standard public trust roots for outbound TLS from inference, artifact-download, size-check, and adapter-staging containers. The ConfigMap is resolved in each workload namespace. When configured on an AIMClusterRuntimeConfig, each consuming namespace needs a ConfigMap with the same name and key to receive the additional trust. AIM Engine checks the reference directly during reconciliation and omits the projection when the ConfigMap or key is absent. Certificate parsing occurs only in a workload init container. CA changes are consumed when workloads are recreated; the ConfigMap is not watched for rotation. |  | Optional: \{\} |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | DEPRECATED: The embedded Hugging Face-to-S3 artifact cache has been removed. This field is retained temporarily for API compatibility and is no longer honored by the controller. Use direct s3:// model sources with Artifact.S3 connection settings instead. |  | Optional: \{\} |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources. When enabled, labels matching the specified patterns are automatically copied from parent resources (e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs). This is useful for propagating organizational metadata like cost centers, team identifiers, or compliance labels through the resource hierarchy. |  | Optional: \{\} |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set, the value will be automatically migrated. |  | Optional: \{\} |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set, the value will be automatically migrated. |  | Optional: \{\} |


#### AIMRuntimeConfigLabelPropagationSpec







_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled, if true, allows propagating parent labels to all child resources it creates directly Only label keys that match the ones in Match are propagated. | false | Optional: \{\} |
| `match` _string array_ | Match is a list of label keys that will be propagated to any child resources created. Wildcards are supported, so for example `org.my/my-key-*` would match any label with that prefix. |  | Optional: \{\} |


#### AIMRuntimeConfigList



AIMRuntimeConfigList contains a list of AIMRuntimeConfig.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMRuntimeConfigList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMRuntimeConfig](#aimruntimeconfig) array_ |  |  |  |


#### AIMRuntimeConfigSpec



AIMRuntimeConfigSpec defines namespace-scoped overrides for AIM resources.



_Appears in:_
- [AIMRuntimeConfig](#aimruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `adapterRuntime` _[AIMAdapterRuntimeConfig](#aimadapterruntimeconfig)_ | AdapterRuntime configures LoRA adapter capacity for inference containers. Service values override namespace and cluster RuntimeConfig defaults. |  | Optional: \{\} |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `scaleFromZero` _[AIMScaleFromZeroConfig](#aimscalefromzeroconfig)_ | ScaleFromZero configures the activation metric query for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. When set on AIMService, these take highest precedence in the merge hierarchy. When set on RuntimeConfig, these provide namespace/cluster-level defaults. Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} |
| `additionalCABundleRef` _[AdditionalCABundleReference](#additionalcabundlereference)_ | AdditionalCABundleRef selects PEM CA certificates that AIM Engine adds to its standard public trust roots for outbound TLS from inference, artifact-download, size-check, and adapter-staging containers. The ConfigMap is resolved in each workload namespace. When configured on an AIMClusterRuntimeConfig, each consuming namespace needs a ConfigMap with the same name and key to receive the additional trust. AIM Engine checks the reference directly during reconciliation and omits the projection when the ConfigMap or key is absent. Certificate parsing occurs only in a workload init container. CA changes are consumed when workloads are recreated; the ConfigMap is not watched for rotation. |  | Optional: \{\} |
| `model` _[AIMModelConfig](#aimmodelconfig)_ | Model controls model creation and discovery defaults. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifact` _[AIMArtifactConfig](#aimartifactconfig)_ | Artifact controls artifact-level defaults such as eviction policy. This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services. |  | Optional: \{\} |
| `artifactCache` _[ArtifactCacheConfig](#artifactcacheconfig)_ | DEPRECATED: The embedded Hugging Face-to-S3 artifact cache has been removed. This field is retained temporarily for API compatibility and is no longer honored by the controller. Use direct s3:// model sources with Artifact.S3 connection settings instead. |  | Optional: \{\} |
| `labelPropagation` _[AIMRuntimeConfigLabelPropagationSpec](#aimruntimeconfiglabelpropagationspec)_ | LabelPropagation controls how labels from parent AIM resources are propagated to child resources. When enabled, labels matching the specified patterns are automatically copied from parent resources (e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs). This is useful for propagating organizational metadata like cost centers, team identifiers, or compliance labels through the resource hierarchy. |  | Optional: \{\} |
| `defaultStorageClassName` _string_ | DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set, the value will be automatically migrated. |  | Optional: \{\} |
| `pvcHeadroomPercent` _integer_ | DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version. For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set, the value will be automatically migrated. |  | Optional: \{\} |


#### AIMRuntimeConfigStatus



AIMRuntimeConfigStatus records the resolved config reference surfaced to consumers.



_Appears in:_
- [AIMClusterRuntimeConfig](#aimclusterruntimeconfig)
- [AIMRuntimeConfig](#aimruntimeconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the last reconciled generation. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions communicate reconciliation progress. |  |  |


#### AIMRuntimeParameters



AIMRuntimeParameters contains the runtime configuration parameters shared across templates and services. Fields use pointers to allow optional usage in different contexts (required in templates, optional in service overrides).



_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMServiceOverrides](#aimserviceoverrides)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)
- [AIMServiceTemplateSpecCommon](#aimservicetemplatespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica. For GPU models, defines the GPU count and model types required for deployment. For CPU-only models, defines CPU resource requirements. This field is immutable after creation. |  | Optional: \{\} |


#### AIMRuntimeRoutingConfig



AIMRuntimeRoutingConfig configures HTTP routing defaults for inference services. These settings control how Gateway API HTTPRoutes are created and configured.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether HTTP routing is managed for inference services using this config. When true, the operator creates HTTPRoute resources for services that reference this config. When false or unset, routing must be explicitly enabled on each service. This provides a namespace or cluster-wide default that individual services can override. |  | Optional: \{\} |
| `gatewayRef` _[ParentReference](#parentreference)_ | GatewayRef specifies the Gateway API Gateway resource that should receive HTTPRoutes. This identifies the parent gateway for routing traffic to inference services. The gateway can be in any namespace (cross-namespace references are supported). If routing is enabled but GatewayRef is not specified, service reconciliation will fail with a validation error. |  | Optional: \{\} |
| `hostnames` _Hostname array_ | Hostnames pins generated HTTPRoutes to these hostnames so a route only attaches to the matching Gateway listener instead of every listener on the parent gateway. Without a hostname, an HTTPRoute matches all of the parent gateway's listener hostnames, which can expose a service on listeners that do not enforce the intended authentication. This field is required when the parent gateway exposes more than one listener: in that case a service with routing enabled but no hostnames configured will not get an HTTPRoute and reports ConfigValid=False with reason RouteHostnameRequired. When the parent gateway has a single listener, leaving this empty preserves the existing behavior (the route inherits that listener's hostnames). Individual services can override this list via spec.routing.hostnames. |  | Optional: \{\} |
| `pathTemplate` _string_ | PathTemplate defines the HTTP path template for routes, evaluated using JSONPath expressions. The template is rendered against the AIMService object to generate unique paths. Example templates:<br />- `/\{.metadata.namespace\}/\{.metadata.name\}` - namespace and service name<br />- `/\{.metadata.namespace\}/\{.metadata.labels['team']\}/inference` - with label<br />- `/models/\{.metadata.name\}` - based on service name<br />The template must:<br />- Use valid JSONPath expressions wrapped in \{...\}<br />- Reference fields that exist on the service<br />- Produce a path ≤ 200 characters after rendering<br />- Result in valid URL path segments (lowercase, RFC 1123 compliant)<br />If evaluation fails, the service enters Degraded state with PathTemplateInvalid reason. Individual services can override this template via spec.routing.pathTemplate. |  | Optional: \{\} |
| `requestTimeout` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#duration-v1-meta)_ | RequestTimeout defines the HTTP request timeout for routes. This sets the maximum duration for a request to complete before timing out. The timeout applies to the entire request/response cycle. If not specified, no timeout is set on the route. Individual services can override this value via spec.routing.requestTimeout. |  | Optional: \{\} |
| `annotations` _object (keys:string, values:string)_ | Annotations defines default annotations to add to all HTTPRoute resources. Services can add additional annotations or override these via spec.routing.annotations. When both are specified, service annotations take precedence for conflicting keys. Common use cases include ingress controller settings, rate limiting, monitoring labels, and security policies that should apply to all services using this config. |  | Optional: \{\} |


#### AIMScaleFromZeroConfig



AIMScaleFromZeroConfig configures the gateway activation metric used to wake a service from zero replicas.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceRuntimeConfig](#aimserviceruntimeconfig)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `activationMetricQueryTemplate` _string_ | ActivationMetricQueryTemplate overrides the provider-derived KEDA OpenTelemetry metric query. The controller expands these placeholders: $\{namespace\}, $\{serviceName\}, $\{httpRouteName\}, and $\{predictorDeployment\}. Unknown placeholders make the service configuration invalid. A value set directly on AIMService takes precedence over namespace and cluster RuntimeConfig values. |  | MaxLength: 4096 <br />MinLength: 1 <br />Optional: \{\} |


#### AIMService



AIMService manages a KServe-based AIM inference service for the selected model and template. Note: KServe uses {name}-{namespace} format which must not exceed 63 characters. This constraint is validated at runtime since CEL cannot access metadata.namespace.



_Appears in:_
- [AIMServiceList](#aimservicelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMService` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMServiceSpec](#aimservicespec)_ |  |  |  |
| `status` _[AIMServiceStatus](#aimservicestatus)_ |  |  |  |


#### AIMServiceAdapterKind

_Underlying type:_ _string_

AIMServiceAdapterKind enumerates the kinds an adapter reference may target. Restricted to AIMArtifact in v1; reserved to admit a future AIMAdapter catalog kind.

_Validation:_
- Enum: [AIMArtifact]

_Appears in:_
- [AIMServiceAdapterReference](#aimserviceadapterreference)

| Field | Description |
| --- | --- |
| `AIMArtifact` | AdapterKindAIMArtifact references an AIMArtifact (type=adapter). |


#### AIMServiceAdapterReference



AIMServiceAdapterReference is a typed reference to a LoRA adapter served by this service. Today it is resolved as a pure reference to an existing adapter artifact. The inline bootstrap fields (sourceUri/modelId/rank) are reserved: the schema accepts them, but create-if-missing self-healing is not yet wired, so a referenced adapter artifact must currently exist.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the metadata.name of the referenced adapter artifact. |  | MaxLength: 253 <br />MinLength: 1 |
| `kind` _[AIMServiceAdapterKind](#aimserviceadapterkind)_ | Kind is the kind of the referenced adapter. Required; restricted to AIMArtifact in v1. |  | Enum: [AIMArtifact] |
| `sourceUri` _string_ | SourceURI is an optional create-if-missing bootstrap source. When set and no adapter artifact named Name exists, a later release will create one from this source; a pre-existing artifact always wins. RESERVED: not yet acted on. |  | Optional: \{\} |
| `modelId` _string_ | ModelID is the adapter's canonical model id. Required when SourceURI is set. RESERVED: only meaningful alongside SourceURI. |  | Optional: \{\} |
| `rank` _integer_ | Rank is the optional LoRA rank for the bootstrapped adapter. RESERVED: only meaningful alongside SourceURI. |  | Minimum: 1 <br />Optional: \{\} |


#### AIMServiceAdapterStatus



AIMServiceAdapterStatus is the per-adapter status aggregated onto an AIMService.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the adapter reference name. |  |  |
| `adapterPath` _string_ | AdapterPath is the on-disk directory name (mirrored from the artifact). |  | Optional: \{\} |
| `modelId` _string_ | ModelID is the adapter's canonical model id (mirrored from the artifact). |  | Optional: \{\} |
| `state` _[AIMAdapterState](#aimadapterstate)_ | State is the disk-side state of the adapter for this service. |  | Enum: [Pending Downloading Downloaded Failed Deleting Loaded LoadRejected] <br />Optional: \{\} |
| `loadedReplicas` _string_ | LoadedReplicas reports how many serving replicas have the adapter loaded, as "loaded/total" (e.g. "3/3"). RESERVED: engine-reported, not yet populated. |  | Optional: \{\} |
| `lastObserved` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastObserved is when the controller last observed this adapter's state. |  | Optional: \{\} |
| `lastError` _string_ | LastError carries the most recent error for this adapter (e.g. a mirrored failing reason from the underlying artifact). |  | Optional: \{\} |


#### AIMServiceAutoScaling



AIMServiceAutoScaling configures KEDA-based autoscaling with custom metrics. This enables automatic scaling based on metrics collected from OpenTelemetry. A present autoScaling block must carry at least one real setting: a fully empty block is a partially-complete spec that yields no usable configuration, so it is rejected. Omit the whole block to use default scaling instead. Any single valid sub-field (metrics, pollingInterval, or cooldownPeriod) is enough.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metrics` _[AIMServiceMetricsSpec](#aimservicemetricsspec) array_ | Metrics is a list of metrics to be used for autoscaling. Each metric defines a source (PodMetric) and target values. |  | Optional: \{\} |
| `pollingInterval` _integer_ | PollingInterval is the KEDA polling interval in seconds. Defaults to 5 when spec.minReplicas == 0 (so a single request reliably activates the deployment within ~10s); otherwise unset (KEDA default of 30 applies). |  | Minimum: 1 <br />Optional: \{\} |
| `cooldownPeriod` _integer_ | CooldownPeriod is the seconds-of-inactivity budget KEDA waits before scaling back to minReplicaCount. Under scale-to-zero, defaults to a memory-derived value (300-1200s); otherwise unset (KEDA default of 300). |  | Minimum: 0 <br />Optional: \{\} |


#### AIMServiceCacheStatus



AIMServiceCacheStatus captures cache-related status for an AIMService.

Exactly one of TemplateCacheRef / ProfileCacheRef is populated, depending on which reconciliation path produced the cache:
  - TemplateCacheRef is set by the v1alpha1 (template-based) path and points to an AIMTemplateCache.
  - ProfileCacheRef is set by the v1alpha2 (profile-based) path and points to an AIMProfileCache.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateCacheRef` _[AIMResolvedReference](#aimresolvedreference)_ | TemplateCacheRef references the AIMTemplateCache being used, if any. Set by the v1alpha1 (template-based) reconciliation path. |  | Optional: \{\} |
| `profileCacheRef` _[AIMResolvedReference](#aimresolvedreference)_ | ProfileCacheRef references the AIMProfileCache being used, if any. Set by the v1alpha2 (profile-based) reconciliation path. |  | Optional: \{\} |
| `retryAttempts` _integer_ | RetryAttempts tracks how many times this service has attempted to retry a failed cache. Each service gets exactly one retry attempt. When a cache enters Failed state, this counter is incremented from 0 to 1 after deleting failed Artifacts. If the retry fails (cache enters Failed again with attempts == 1), the service degrades. |  | Optional: \{\} |


#### AIMServiceCachingConfig



AIMServiceCachingConfig controls caching behavior for a service.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[AIMCachingMode](#aimcachingmode)_ | Mode controls when to use caching. Canonical values:<br />- Shared (default): reuse/create shared cache assets<br />- Dedicated: create service-owned dedicated cache assets<br />Legacy values are accepted and normalized:<br />- Always -> Shared<br />- Auto -> Shared<br />- Never -> Dedicated | Shared | Enum: [Dedicated Shared Auto Always Never] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env supplies credentials for model downloads (for example a HuggingFace token via secretKeyRef). Unlike the inference container env, these variables reach only the model-download Job, so download-only secrets are never injected into the serving container. They are also reachable for cluster-scoped and overlay profiles, where the profile's own caching.env does not exist. Merged over the profile's caching.env (service wins). |  | Optional: \{\} |


#### AIMServiceList



AIMServiceList contains a list of AIMService.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMService](#aimservice) array_ |  |  |  |


#### AIMServiceMetricTarget



AIMServiceMetricTarget defines the target value for a metric. Specifies how the metric value should be interpreted and what target to maintain. The value field that matches the chosen type must be set, otherwise the target carries no threshold and the scaler cannot make a decision.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type specifies how to interpret the metric value.<br />"Value": absolute value target (use Value field)<br />"AverageValue": average value across all pods (use AverageValue field)<br />"Utilization": percentage utilization for resource metrics (use AverageUtilization field) |  | Enum: [Value AverageValue Utilization] |
| `value` _string_ | Value is the target value of the metric (as a quantity). Used when Type is "Value". Example: "1" for 1 request, "100m" for 100 millicores |  | Optional: \{\} |
| `averageValue` _string_ | AverageValue is the target value of the average of the metric across all relevant pods (as a quantity). Used when Type is "AverageValue". Example: "100m" for 100 millicores per pod |  | Optional: \{\} |
| `averageUtilization` _integer_ | AverageUtilization is the target value of the average of the resource metric across all relevant pods, represented as a percentage of the requested value of the resource for the pods. Used when Type is "Utilization". Only valid for Resource metric source type. Example: 80 for 80% utilization |  | Optional: \{\} |


#### AIMServiceMetricsSpec



AIMServiceMetricsSpec defines a single metric for autoscaling. Specifies the metric source type and configuration.



_Appears in:_
- [AIMServiceAutoScaling](#aimserviceautoscaling)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type is the type of metric source. Valid values: "PodMetric" (per-pod custom metrics). |  | Enum: [PodMetric] |
| `podmetric` _[AIMServicePodMetricSource](#aimservicepodmetricsource)_ | PodMetric refers to a metric describing each pod in the current scale target. Used when Type is "PodMetric". Supports backends like OpenTelemetry for custom metrics. |  | Optional: \{\} |


#### AIMServiceModel



AIMServiceModel specifies which model to deploy. Exactly one field must be set.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name references an existing AIMModel or AIMClusterModel by metadata.name. The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel. Example: `meta-llama-3-8b` |  | Optional: \{\} |
| `image` _string_ | Image specifies a container image URI directly. The controller searches for an existing model with this image, or creates one if none exists. Auto-created models are namespace-scoped and can be reused by other services. Example: `docker.io/amdenterpriseai/llama-3-8b:v1.2.0` |  | Optional: \{\} |
| `custom` _[AIMServiceModelCustom](#aimservicemodelcustom)_ | Custom specifies a custom model configuration with explicit base image, model sources, and hardware requirements. The controller will search for an existing matching AIMModel or auto-create one if not found. |  | Optional: \{\} |


#### AIMServiceModelCustom



AIMServiceModelCustom specifies a custom model configuration with explicit base image, model sources, and hardware requirements. Used for ad-hoc custom model deployments.



_Appears in:_
- [AIMServiceModel](#aimservicemodel)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `baseImage` _string_ | BaseImage is the container image URI for the AIM base image. This will be used as the image for the auto-created AIMModel. Example: `docker.io/amdenterpriseai/aim-base:0.7.0` |  | Required: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to use. The controller will search for or create an AIMModel with these sources. The size field is optional - if not specified, it will be discovered by the download job. AIM runtime currently supports only one model source. |  | MaxItems: 1 <br />MinItems: 1 <br />Required: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies the GPU and CPU requirements for this custom model. GPU is optional - if not set, no GPUs are requested (CPU-only model). |  | Required: \{\} |


#### AIMServiceOverrides



AIMServiceOverrides allows overriding template parameters at the service level. All fields are optional. When specified, they override the corresponding values from the referenced AIMServiceTemplate.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica. For GPU models, defines the GPU count and model types required for deployment. For CPU-only models, defines CPU resource requirements. This field is immutable after creation. |  | Optional: \{\} |


#### AIMServicePodMetric



AIMServicePodMetric identifies the pod metric and its backend. Supports multiple metrics backends including OpenTelemetry.



_Appears in:_
- [AIMServicePodMetricSource](#aimservicepodmetricsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `backend` _string_ | Backend defines the metrics backend to use. If not specified, defaults to "opentelemetry". | opentelemetry | Enum: [opentelemetry] <br />Optional: \{\} |
| `serverAddress` _string_ | ServerAddress specifies the address of the metrics backend server. If not specified, defaults to "keda-otel-scaler.keda.svc:4317" for OpenTelemetry backend. |  | Optional: \{\} |
| `metricNames` _string array_ | MetricNames specifies which metrics to collect from pods and send to ServerAddress. Example: ["vllm:num_requests_running"] |  | Optional: \{\} |
| `query` _string_ | Query specifies the query to run to retrieve metrics from the backend. The query syntax depends on the backend being used. Example: "vllm:num_requests_running" for OpenTelemetry. |  | Optional: \{\} |
| `operationOverTime` _string_ | OperationOverTime specifies the operation to aggregate metrics over time. Valid values: "last_one", "avg", "max", "min", "rate", "count" Default: "last_one" |  | Optional: \{\} |


#### AIMServicePodMetricSource



AIMServicePodMetricSource defines pod-level metrics configuration. Specifies the metric identification and target values for pod-based autoscaling.



_Appears in:_
- [AIMServiceMetricsSpec](#aimservicemetricsspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMServicePodMetric](#aimservicepodmetric)_ | Metric contains the metric identification and backend configuration. Defines which metrics to collect and how to query them. |  |  |
| `target` _[AIMServiceMetricTarget](#aimservicemetrictarget)_ | Target specifies the target value for the metric. The autoscaler will scale to maintain this target value. |  |  |


#### AIMServiceProfileConfig



AIMServiceProfileConfig contains profile selection configuration for AIMService v1alpha2. When set, the service uses a profile-based reconciliation path instead of the template path.

Exactly one of Name and Selector must be set. Name resolves an AIMProfile / AIMClusterProfile directly; Selector lists candidates by provenance and spec fields (typically combined with `spec.model.name`, which the controller treats as a shortcut for `selector.modelRef.name`).

An empty `name` ("") is treated as unset, so the rules below are value-based (`size(self.name) > 0`) rather than presence-based (`has(self.name)`).



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the AIMProfile or AIMClusterProfile to use. The controller looks for a namespace-scoped AIMProfile first, then falls back to AIMClusterProfile. Mutually exclusive with Selector. An empty string is treated as unset. |  | Optional: \{\} |
| `selector` _[ProfileSelector](#profileselector)_ | Selector narrows candidate AIMProfile / AIMClusterProfile objects via the shared provenance labels (role, source-model, origin) and spec filters (aimId, precision, acceleratorModel, ...). The controller forces `selector.role = Deployable` at evaluation time; user-supplied values for that field are rejected by CEL on v1alpha2. For every selector-driven AIMService the controller requires at least one of `selector.aimId` or `selector.modelRef.name` so the watch fan-out can reach the service via an O(1) index lookup. The top-level `spec.model.name` shortcut is treated as if the user had set `selector.modelRef.name` to the same value when not explicit. |  | Optional: \{\} |


#### AIMServiceProfileOverrides



AIMServiceProfileOverrides allows overriding profile parameters at the service level. When specified, the controller materialises a service-owned overlay AIMProfile derived from the referenced profile with these overrides applied; the original profile is not modified. The downstream AIMProfileCache and InferenceService are then resolved from the overlay, so the override participates in cache key computation as well as inference-pod env wiring.

This type is a SUBSET of `aimv1alpha1.ProfileOverrides` (the type AIMProfileSet derivation uses). Both go through the same internal apply primitive (`internal/v1alpha2/aimprofile.ApplyProfileCopyOverrides`) so the merge semantics match, but the service-level overlay intentionally omits the `Image` override that AIMProfileSet's overrides expose: changing the runtime container image per-service belongs at the profile level (via spec.profiles.overrides.image on the source AIMModel / AIMProfileSet), not at the consumer. Restricting the field set here keeps the per-service overlay focused on workload-shape changes (weights, env, args, hardware count) where service-level overrides are the right tool.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources replaces the referenced profile's modelSources entirely. Use this to point a profile at user-supplied weights (e.g. a fine-tuned checkpoint) without forking the profile itself. The first source's modelId becomes the overlay profile's modelId. |  | Optional: \{\} |
| `features` _string array_ | Features adds optional runtime capabilities to the service-owned overlay profile. Values are unioned with the referenced profile's features. Use ["adapters"] to explicitly assert that the selected image honours the LoRA adapter runtime contract when discovery metadata does not advertise it. |  | Optional: \{\} |
| `acceleratorModel` _string_ | AcceleratorModel replaces the referenced profile's acceleratorModel (e.g. "MI300X" -> "MI325X"). Validation against actual cluster availability is left to the AIMServiceTemplate / runtime layers. |  | Optional: \{\} |
| `acceleratorVendor` _[AcceleratorVendor](#acceleratorvendor)_ | AcceleratorVendor replaces the referenced profile's acceleratorVendor. |  | Enum: [amd nvidia] <br />Optional: \{\} |
| `acceleratorCount` _integer_ | AcceleratorCount replaces the referenced profile's acceleratorCount. |  | Optional: \{\} |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode replaces the referenced profile's acceleratorPartitioningMode. Complete replacement, not a merge — single string, no substruct ambiguity. Empty string means "no override" (the resolved overlay inherits the base profile's mode). Use this to deploy a profile written for whole GPUs onto a partition slice. Whenever this override is set (to any value, including "unpartitioned"), the CEL rule on AIMService also requires an acceleratorCount override: partition mode changes the per-unit interpretation of acceleratorCount, and CEL cannot read the base profile to tell whether the meaning actually changed, so it conservatively requires the count be restated. See AcceleratorPartitioningMode on AIMProfileSpecCommon for the reserved values ("unpartitioned", "partitioned", "<C>-<M>"). |  | Optional: \{\} |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | ContainerEnv merges by env-var name on top of the profile's containerEnv. Matching names override; new names are appended. AIM framework variables (AIM_*) reserved for the controller are applied after the overlay's containerEnv and cannot be overridden here. |  | Optional: \{\} |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv merges by key on top of the profile's engineEnv. These variables flow into the inference engine's runtime configuration. |  | Optional: \{\} |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs shallow-merges on top of the profile's engineArgs, overriding matching top-level keys. Values are passed verbatim to the inference engine CLI. |  | Schemaless: \{\} <br />Optional: \{\} |


#### AIMServiceRoutingStatus



AIMServiceRoutingStatus captures observed routing details.



_Appears in:_
- [AIMServiceStatus](#aimservicestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the HTTP path prefix used when routing is enabled. Example: `/tenant/svc-uuid` |  | Optional: \{\} |


#### AIMServiceRuntimeConfig



AIMServiceRuntimeConfig contains runtime configuration fields that apply to services. This struct is shared between AIMService.spec (inlined) and AIMRuntimeConfigCommon, allowing services to override these specific runtime settings while inheriting defaults from namespace/cluster RuntimeConfigs.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `adapterRuntime` _[AIMAdapterRuntimeConfig](#aimadapterruntimeconfig)_ | AdapterRuntime configures LoRA adapter capacity for inference containers. Service values override namespace and cluster RuntimeConfig defaults. |  | Optional: \{\} |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `scaleFromZero` _[AIMScaleFromZeroConfig](#aimscalefromzeroconfig)_ | ScaleFromZero configures the activation metric query for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. When set on AIMService, these take highest precedence in the merge hierarchy. When set on RuntimeConfig, these provide namespace/cluster-level defaults. Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} |


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
| `replicas` _string_ | Replicas is a formatted display string for kubectl output. Shows "current" for fixed replicas or "current/desired (min-max)" for autoscaling. |  | Optional: \{\} |


#### AIMServiceSpec



AIMServiceSpec defines the desired state of AIMService.

Binds a canonical model to an AIMServiceTemplate and configures replicas, caching behavior, and optional overrides. The template governs the base runtime selection knobs, while the overrides field allows service-specific customization.

With v1alpha2, a Profile can be used instead of a Template. Template and Profile are mutually exclusive — at least one resolution path must be specified.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[AIMServiceModel](#aimservicemodel)_ | Model specifies which model to deploy using one of the available reference methods. Use `name` to reference an existing AIMModel/AIMClusterModel by name, or use `image` to specify a container image URI directly (which will auto-create a model if needed). Required for v1alpha1 (template path), not permitted for v1alpha2 (profile path). |  | Optional: \{\} |
| `template` _[AIMServiceTemplateConfig](#aimservicetemplateconfig)_ | Template contains template selection and configuration. Use Template.Name to specify an explicit template, or omit to auto-select. Mutually exclusive with Profile (v1alpha2). |  | Optional: \{\} |
| `profile` _[AIMServiceProfileConfig](#aimserviceprofileconfig)_ | Profile contains profile selection configuration (v1alpha2 only). When set, the service uses a profile-based reconciliation path. Mutually exclusive with Template. |  | Optional: \{\} |
| `profileOverrides` _[AIMServiceProfileOverrides](#aimserviceprofileoverrides)_ | ProfileOverrides allows overriding specific profile parameters for this service. Only valid when Profile is set. |  | Optional: \{\} |
| `adapterMode` _[AIMAdapterMode](#aimadaptermode)_ | AdapterMode is the immutable adapter contract for the service, mapped directly to the AIM_ADAPTER_MODE container env. static (default) freezes spec.adapters and mounts the adapter disk only when adapters are declared; dynamic allows editing spec.adapters and mounts the disk even at zero adapters (so add/remove never restarts the pod). It is immutable after creation. | static | Enum: [static dynamic] <br />Optional: \{\} |
| `adapters` _[AIMServiceAdapterReference](#aimserviceadapterreference) array_ | Adapters is the load-bearing list of LoRA adapters this service serves. The list may only be edited after creation when adapterMode is dynamic (static freezes it). Omitting the list serves no adapters. Supported on both the template (v1alpha1) and profile (v1alpha2) pipelines: the base model the adapters attach to is resolved from the service's template cache or profile cache respectively. In dynamic mode the list may be edited after creation: adding an adapter stages it into the service's subtree and the aim-runtime hot-loads it; removing one lets the runtime unload it (subtree cleanup is reclaimed out-of-band). The InferenceService is never modified for adapter changes — it mounts the whole per-service subtree read-only. Entries are pure references; (kind, name) pairs must be unique. Omitting the list serves no adapters. |  | MaxItems: 64 <br />Optional: \{\} |
| `caching` _[AIMServiceCachingConfig](#aimservicecachingconfig)_ | Caching controls caching behavior for this service. When nil, defaults to Shared mode. |  | Optional: \{\} |
| `cacheModel` _boolean_ | DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version. This field is no longer honored by the controller. |  | Optional: \{\} |
| `replicas` _integer_ | Replicas specifies the number of replicas for this service. When not specified, defaults to 1 replica. This value overrides any replica settings from the template. For autoscaling, use MinReplicas and MaxReplicas instead. | 1 | Optional: \{\} |
| `minReplicas` _integer_ | MinReplicas specifies the minimum number of replicas for autoscaling. Defaults to 1. Set to 0 to enable scale-to-zero: KEDA idles the predictor to zero replicas when idle and brings it back up on the next request. When specified with MaxReplicas, enables autoscaling for the service. |  | Minimum: 0 <br />Optional: \{\} |
| `maxReplicas` _integer_ | MaxReplicas specifies the maximum number of replicas for autoscaling. Required when MinReplicas is set or when AutoScaling configuration is provided. |  | Minimum: 1 <br />Optional: \{\} |
| `autoScaling` _[AIMServiceAutoScaling](#aimserviceautoscaling)_ | AutoScaling configures advanced autoscaling behavior using KEDA. Supports custom metrics from OpenTelemetry backend. When specified, MinReplicas and MaxReplicas should also be set. |  | Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `adapterRuntime` _[AIMAdapterRuntimeConfig](#aimadapterruntimeconfig)_ | AdapterRuntime configures LoRA adapter capacity for inference containers. Service values override namespace and cluster RuntimeConfig defaults. |  | Optional: \{\} |
| `storage` _[AIMStorageConfig](#aimstorageconfig)_ | Storage configures storage defaults for this service's PVCs and caches. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `routing` _[AIMRuntimeRoutingConfig](#aimruntimeroutingconfig)_ | Routing controls HTTP routing configuration for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `scaleFromZero` _[AIMScaleFromZeroConfig](#aimscalefromzeroconfig)_ | ScaleFromZero configures the activation metric query for this service. When set, these values override namespace/cluster runtime config defaults. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. When set on AIMService, these take highest precedence in the merge hierarchy. When set on RuntimeConfig, these provide namespace/cluster-level defaults. Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources overrides the container resource requirements for this service. When specified, these values take precedence over the template and image defaults. |  | Optional: \{\} |
| `overrides` _[AIMServiceOverrides](#aimserviceoverrides)_ | Overrides allows overriding specific template parameters for this service. When specified, these values take precedence over the template values. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for the inference workload. This service account is used by the deployed inference pods. If empty, the default service account for the namespace is used. |  | Optional: \{\} |
| `priorityClassName` _string_ | PriorityClassName specifies the priority class for the inference pods. This maps directly to the Kubernetes PriorityClassName field on the pod spec. If empty, no priority class is set. |  | Optional: \{\} |


#### AIMServiceStatus



AIMServiceStatus defines the observed state of AIMService.



_Appears in:_
- [AIMService](#aimservice)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `reconcilerPipeline` _string_ | ReconcilerPipeline records which pipeline owns pipeline-specific status. The controller persists this before dispatch so transitions can retire old resolved references and conditions before the new pipeline fetches state. |  | Enum: [template profile] <br />Optional: \{\} |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  | Optional: \{\} |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the service lifecycle. Values: `Pending`, `Starting`, `Running`, `Degraded`, `Failed`. | Pending | Enum: [Pending Starting Running Degraded Failed] |
| `routing` _[AIMServiceRoutingStatus](#aimserviceroutingstatus)_ | Routing surfaces information about the configured HTTP routing, when enabled. |  | Optional: \{\} |
| `resolvedTemplate` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedTemplate captures metadata about the template that satisfied the reference. |  |  |
| `resolvedProfile` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedProfile captures metadata about the profile that satisfied the reference. Set when the service uses a profile-based reconciliation path (v1alpha2). |  | Optional: \{\} |
| `cache` _[AIMServiceCacheStatus](#aimservicecachestatus)_ | Cache captures cache-related status for this service. |  | Optional: \{\} |
| `runtime` _[AIMServiceRuntimeStatus](#aimserviceruntimestatus)_ | Runtime captures runtime status including replica counts. |  | Optional: \{\} |
| `adapters` _[AIMServiceAdapterStatus](#aimserviceadapterstatus) array_ | Adapters reports the per-adapter disk-side status for services that declare spec.adapters. One entry per declared adapter. Observation is best-effort/eventual; the disk state the controller wrote is authoritative. |  | Optional: \{\} |
| `adapterMaxRank` _integer_ | AdapterMaxRank is the resolved AIM_ADAPTER_MAX_RANK startup ceiling. It is only updated when the resolved configuration is valid; a rejected change leaves the last valid value in place. Static services recompute it from their declared adapter artifacts, preserving the last resolved value across transient lookup failures. Dynamic services report the configured/default startup ceiling, but preserve the previous higher ceiling while any declared adapter rank is unresolved before a decrease. |  | Optional: \{\} |
| `adapterSubtreeSyncKey` _string_ | AdapterSubtreeSyncKey records the adapter-disk PVC generation and declared adapter set most recently reconciled onto the service's adapter subtree by the subtree-sync Job. The controller re-runs the sync Job whenever the adapter set or storage binding changes. This both reclaims removed adapters and provisions a fresh subtree after a PVC or base artifact is recreated. |  | Optional: \{\} |
| `adapterDiskPersistentVolumeClaim` _string_ | AdapterDiskPersistentVolumeClaim is the shared adapter-disk PVC whose service subtree was most recently synchronized successfully. Together with AdapterDiskPersistentVolumeClaimUID it identifies the currently mountable storage binding and is reused through transient parent-resolution gaps. |  | Optional: \{\} |
| `adapterDiskPersistentVolumeClaimUid` _string_ | AdapterDiskPersistentVolumeClaimUID is the Kubernetes UID of AdapterDiskPersistentVolumeClaim. A same-name PVC recreation receives a new UID, forcing the controller to provision and stage the service subtree on the replacement storage before switching the InferenceService mount. |  | Optional: \{\} |




#### AIMServiceTemplate



AIMServiceTemplate is the Schema for namespace-scoped AIM service templates.



_Appears in:_
- [AIMServiceTemplateList](#aimservicetemplatelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMServiceTemplateSpec](#aimservicetemplatespec)_ |  |  |  |
| `status` _[AIMServiceTemplateStatus](#aimservicetemplatestatus)_ |  |  |  |


#### AIMServiceTemplateConfig



AIMServiceTemplateConfig contains template selection configuration for AIMService.



_Appears in:_
- [AIMServiceSpec](#aimservicespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use. The template selects the runtime profile and GPU parameters. When not specified, a template will be automatically selected based on the model. |  | Optional: \{\} |
| `allowUnoptimized` _boolean_ | AllowUnoptimized, if true, will allow automatic selection of templates that resolve to an unoptimized profile. |  | Optional: \{\} |


#### AIMServiceTemplateList



AIMServiceTemplateList contains a list of AIMServiceTemplate.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMServiceTemplateList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
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

A namespaced and versioned template that selects a runtime profile for a given AIM model (by canonical name). Templates are intentionally narrow: they describe runtime selection knobs for the AIM container and do not redefine the full Kubernetes deployment shape.



_Appears in:_
- [AIMServiceTemplate](#aimservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable. Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica. For GPU models, defines the GPU count and model types required for deployment. For CPU-only models, defines CPU resource requirements. This field is immutable after creation. |  | Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B"). Required when customProfile is set; used to assemble the profile YAML aim_id field and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8"). Required when customProfile is set; used for profile YAML model_id field and for weight pre-caching via the discovery job. |  | Optional: \{\} |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine. When set, the controller assembles a profile YAML from this data and template metadata, creates a ConfigMap, and mounts it into discovery and inference containers. Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images. These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults. For namespace-scoped templates, secrets must exist in the same namespace. For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template. This includes discovery dry-run jobs and inference services created from this template. If empty, the default service account for the namespace is used. |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template. Service-specific values override the template defaults. |  | Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template. When provided, the discovery dry-run will be skipped and these sources will be used directly. This allows users to explicitly declare model dependencies without requiring a discovery job. If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use. When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. These variables are passed to the inference runtime and can be used to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} |
| `caching` _[AIMTemplateCachingConfig](#aimtemplatecachingconfig)_ | Caching configures model caching behavior for this namespace-scoped template. When enabled, models will be cached using the specified environment variables during download. |  | Optional: \{\} |


#### AIMServiceTemplateSpecCommon







_Appears in:_
- [AIMClusterServiceTemplateSpec](#aimclusterservicetemplatespec)
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelName` _string_ | ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable. Example: `meta/llama-3-8b:1.1+20240915` |  | MinLength: 1 |
| `metric` _[AIMMetric](#aimmetric)_ | Metric selects the optimization goal.<br />- `latency`: prioritize low end‑to‑end latency<br />- `throughput`: prioritize sustained requests/second |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision selects the numeric precision used by the runtime. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `hardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | Hardware specifies GPU and CPU requirements for each replica. For GPU models, defines the GPU count and model types required for deployment. For CPU-only models, defines CPU resource requirements. This field is immutable after creation. |  | Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `aimId` _string_ | AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B"). Required when customProfile is set; used to assemble the profile YAML aim_id field and to compute the custom profile ID for AIM_PROFILE_ID. |  | Optional: \{\} |
| `modelId` _string_ | ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8"). Required when customProfile is set; used for profile YAML model_id field and for weight pre-caching via the discovery job. |  | Optional: \{\} |
| `customProfile` _[AIMCustomProfile](#aimcustomprofile)_ | CustomProfile defines inline custom profile data for the inference engine. When set, the controller assembles a profile YAML from this data and template metadata, creates a ConfigMap, and mounts it into discovery and inference containers. Requires aimId, modelId, hardware, metric, and precision to also be set. |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets containing credentials for pulling container images. These secrets are used for:<br />- Discovery dry-run jobs that inspect the model container<br />- Pulling the image for inference services<br />The secrets are merged with any model or runtime config defaults. For namespace-scoped templates, secrets must exist in the same namespace. For cluster-scoped templates, secrets must exist in the operator namespace. |  | Optional: \{\} |
| `serviceAccountName` _string_ | ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template. This includes discovery dry-run jobs and inference services created from this template. If empty, the default service account for the namespace is used. |  | Optional: \{\} |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core)_ | Resources defines the default container resource requirements applied to services derived from this template. Service-specific values override the template defaults. |  | Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources required to run this template. When provided, the discovery dry-run will be skipped and these sources will be used directly. This allows users to explicitly declare model dependencies without requiring a discovery job. If omitted, a discovery job will be run to automatically determine the required model sources. |  | Optional: \{\} |
| `profileId` _string_ | ProfileId is the specific AIM profile ID that this template should use. When set, the discovery job will be instructed to use this specific profile. |  | Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level of this template.<br />- optimized: Template has been tuned for performance<br />- preview: Template is experimental/pre-release<br />- general: General-purpose tuning between optimized and preview<br />- unoptimized: Default, no specific optimizations applied<br />When nil, the type is determined by discovery. When set, overrides discovery. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables for inference containers. These variables are passed to the inference runtime and can be used to configure runtime behavior, authentication, or other settings. |  | Optional: \{\} |


#### AIMServiceTemplateStatus



AIMServiceTemplateStatus defines the observed state of AIMServiceTemplate.



_Appears in:_
- [AIMClusterServiceTemplate](#aimclusterservicetemplate)
- [AIMServiceTemplate](#aimservicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest observations of template state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} |
| `resolvedModel` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedModel captures metadata about the image that was resolved. |  | Optional: \{\} |
| `resolvedCache` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedCache captures metadata about which cache is used for this template |  | Optional: \{\} |
| `resolvedHardware` _[AIMHardwareRequirements](#aimhardwarerequirements)_ | ResolvedHardware contains the resolved hardware requirements for this template. These values are computed from discovery results and spec defaults, and represent what will actually be used when creating InferenceServices. Resolution order: discovery output > spec values > defaults. |  | Optional: \{\} |
| `resolvedNodeAffinity` _[NodeAffinity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#nodeaffinity-v1-core)_ | ResolvedNodeAffinity contains the computed node affinity rules for GPU scheduling. This is derived from GPU model and minVRAM requirements, merged with any user-specified affinity from the spec. The service controller uses this directly when creating InferenceServices. |  | Optional: \{\} |
| `hardwareSummary` _string_ | HardwareSummary is a human-readable display string for the hardware requirements. Format: "\{count\} x \{model\}" for GPU (e.g., "2 x MI300X") or "CPU" for CPU-only. This is a computed field for display purposes only. |  | Optional: \{\} |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high‑level status of the template lifecycle. Values: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`. | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources list the models that this template requires to run. These are the models that will be cached, if this template is cached. |  |  |
| `version` _string_ | Version is the AIM version extracted from the owning model's image tag (e.g., "0.8.5" from "aim-base:0.8.5"). Set during discovery. Used for version-based template matching with fine-tuned models. |  | Optional: \{\} |
| `profile` _[AIMDiscoveredProfile](#aimdiscoveredprofile)_ | Profile contains the full discovery result profile as a free-form JSON object. This includes metadata, engine args, environment variables, and model details. |  |  |
| `discoveryJob` _[AIMResolvedReference](#aimresolvedreference)_ | DiscoveryJob is a reference to the job that was run for discovery |  |  |
| `discovery` _[DiscoveryState](#discoverystate)_ | Discovery contains state tracking for the discovery process, including retry attempts and backoff timing for the circuit breaker pattern. |  | Optional: \{\} |


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
| `defaultStorageClassName` _string_ | DefaultStorageClassName specifies the storage class to use for artifacts and PVCs when the consuming resource (AIMArtifact, AIMTemplateCache, AIMServiceTemplate) does not specify a storage class. If this field is empty, the cluster's default storage class is used. |  | Optional: \{\} |
| `pvcHeadroomPercent` _integer_ | PVCHeadroomPercent specifies the percentage of extra space to add to PVCs for model storage. This accounts for filesystem overhead and temporary files during model loading. The value represents a percentage (e.g., 10 means 10% extra space). If not specified, defaults to 10%. | 10 | Minimum: 0 <br />Optional: \{\} |
| `downloadFilter` _[AIMDownloadFilter](#aimdownloadfilter)_ | DownloadFilter controls which files are included or excluded during artifact downloads. When set here, applies as the default for all artifacts using this runtime config. Individual artifacts can override this with their own downloadFilter. When no filter is configured at any level, subdirectory files are excluded by default. Set to an empty object (downloadFilter: \{\}) to explicitly allow all files. |  | Optional: \{\} |
| `adapterDiskStorageClassName` _string_ | AdapterDiskStorageClassName is the storage class for the shared ReadWriteMany adapter disk. It must be RWX-capable (e.g. longhorn, NFS) and is resolved before the (typically RWO) DefaultStorageClassName. |  | Optional: \{\} |
| `adapterDiskSize` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#quantity-resource-api)_ | AdapterDiskSize is the cluster default size for the shared adapter disk PVC (built-in default when unset). An artifact's adapterDisk.size wins over it. |  | Optional: \{\} |


#### AIMTemplateCache



AIMTemplateCache pre-warms artifacts for a specified template.



_Appears in:_
- [AIMTemplateCacheList](#aimtemplatecachelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMTemplateCache` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMTemplateCacheSpec](#aimtemplatecachespec)_ |  |  |  |
| `status` _[AIMTemplateCacheStatus](#aimtemplatecachestatus)_ |  |  |  |


#### AIMTemplateCacheList



AIMTemplateCacheList contains a list of AIMTemplateCache.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha1` | | |
| `kind` _string_ | `AIMTemplateCacheList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
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
| `Dedicated` | TemplateCacheModeDedicated means artifacts have owner references to the template cache. When the template cache is deleted, all its artifacts are garbage collected. Use this mode for service-specific caches that should be cleaned up with the service. |
| `Shared` | TemplateCacheModeShared means artifacts have no owner references. artifacts persist independently of template cache lifecycle and can be shared. This is the default mode for long-lived, reusable caches. |


#### AIMTemplateCacheSpec



AIMTemplateCacheSpec defines the desired state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateName` _string_ | TemplateName is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to cache. The controller will first look for a namespace-scoped AIMServiceTemplate in the same namespace. If not found, it will look for a cluster-scoped AIMClusterServiceTemplate with the same name. Namespace-scoped templates take priority over cluster-scoped templates. |  | MinLength: 1 |
| `templateScope` _[AIMServiceTemplateScope](#aimservicetemplatescope)_ | TemplateScope indicates whether the template is namespace-scoped or cluster-scoped. This field is set by the controller during template resolution. |  | Enum: [Namespace Cluster Unknown] <br />Required: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables to use for authentication when downloading models. These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#localobjectreference-v1-core) array_ | ImagePullSecrets references secrets for pulling AIM container images. |  | Optional: \{\} |
| `storageClassName` _string_ | StorageClassName specifies the storage class for cache volumes. When not specified, uses the cluster default storage class. |  | Optional: \{\} |
| `downloadImage` _string_ | DownloadImage specifies the container image used to download and initialize artifacts. When not specified, the controller uses the default model download image. |  | Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies the model sources to cache for this template. These sources are typically copied from the resolved template's model sources. |  | Optional: \{\} |
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |
| `mode` _[AIMTemplateCacheMode](#aimtemplatecachemode)_ | Mode controls the ownership behavior of artifacts created by this template cache.<br />- Dedicated: artifacts are owned by this template cache and garbage collected when it's deleted.<br />- Shared (default): artifacts have no owner references and persist independently.<br />When a Shared template cache encounters artifacts with owner references, it promotes them to shared by removing the owner references, ensuring they persist for long-term use. | Shared | Enum: [Dedicated Shared] <br />Optional: \{\} |


#### AIMTemplateCacheStatus



AIMTemplateCacheStatus defines the observed state of AIMTemplateCache



_Appears in:_
- [AIMTemplateCache](#aimtemplatecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest observations of the template cache state. |  |  |
| `resolvedRuntimeConfig` _[AIMResolvedReference](#aimresolvedreference)_ | ResolvedRuntimeConfig captures metadata about the runtime config that was resolved. |  | Optional: \{\} |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high-level status of the template cache. | Pending | Enum: [Pending Progressing Ready Failed Degraded NotAvailable] |
| `resolvedTemplateKind` _string_ | ResolvedTemplateKind indicates whether the template resolved to a namespace-scoped AIMServiceTemplate or cluster-scoped AIMClusterServiceTemplate. Values: "AIMServiceTemplate", "AIMClusterServiceTemplate" |  |  |
| `artifacts` _object (keys:string, values:[AIMResolvedArtifact](#aimresolvedartifact))_ | Artifacts maps model names to their resolved AIMArtifact resources. |  | Optional: \{\} |


#### AIMTemplateCachingConfig



AIMTemplateCachingConfig configures model caching behavior for namespace-scoped templates.



_Appears in:_
- [AIMServiceTemplateSpec](#aimservicetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether caching is enabled for this template. Defaults to `false`. | false |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env specifies environment variables to use when downloading the model for caching. These variables are available to the model download process and can be used to configure download behavior, authentication, proxies, etc. If not set, falls back to the template's top-level Env field. |  | Optional: \{\} |




#### AIMTemplateProfile



AIMTemplateProfile declares profile variables for template selection. Used in AIMCustomTemplate to specify optimization targets.



_Appears in:_
- [AIMCustomTemplate](#aimcustomtemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric specifies the optimization target (e.g., latency, throughput). |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision specifies the numerical precision (e.g., fp8, fp16, bf16). |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |


#### AIMVersionPolicy

_Underlying type:_ _string_

AIMVersionPolicy controls how template versions are filtered during aimId-based matching.

_Validation:_
- Enum: [pinned latest any all]

_Appears in:_
- [AIMCustomModelSpec](#aimcustommodelspec)

| Field | Description |
| --- | --- |
| `pinned` | AIMVersionPolicyPinned matches templates whose status.version equals the model's image tag. |
| `latest` | AIMVersionPolicyLatest matches only templates at the newest available status.version. |
| `all` | AIMVersionPolicyAll matches templates at any version. This is the canonical spelling, aligned with v1alpha2 ProfileVersionPolicy. |
| `any` | AIMVersionPolicyAny is a deprecated alias of AIMVersionPolicyAll, kept for backward compatibility with existing v1alpha1 objects. Prefer "all". |


#### AcceleratorType

_Underlying type:_ _string_

AcceleratorType distinguishes CPU from GPU accelerators. Used by AIM Engine to determine the resource derivation strategy (e.g., gpu → a vendor-specific extended resource, cpu → cpu).

_Validation:_
- Enum: [gpu cpu]

_Appears in:_
- [AIMProfileGenerationMatch](#aimprofilegenerationmatch)
- [ProfileHardwareGroup](#profilehardwaregroup)
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `cpu` |  |
| `gpu` |  |


#### AcceleratorVendor

_Underlying type:_ _string_

AcceleratorVendor identifies the vendor-specific device-plugin contract for an accelerator. It is intentionally independent of AcceleratorModel so a generic profile can target any GPU from one vendor without enumerating every possible card model.

_Validation:_
- Enum: [amd nvidia]

_Appears in:_
- [AIMModelAcceleratorRequest](#aimmodelacceleratorrequest)
- [AIMProfileGenerationMatch](#aimprofilegenerationmatch)
- [AIMServiceProfileOverrides](#aimserviceprofileoverrides)
- [ProfileOverrides](#profileoverrides)
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `amd` |  |
| `nvidia` |  |


#### AdditionalCABundleReference



AdditionalCABundleReference identifies additional PEM CA certificates in a namespace-local ConfigMap.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the ConfigMap name in the workload namespace. |  | MaxLength: 253 <br />MinLength: 1 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$` |
| `key` _string_ | Key is the ConfigMap data key containing PEM CA certificates. |  | MaxLength: 253 <br />MinLength: 1 <br />Pattern: `^[-._a-zA-Z0-9]+$` |


#### ArtifactCacheConfig



ArtifactCacheConfig is the deprecated configuration for the removed embedded Hugging Face-to-S3 artifact cache. It is retained only so existing manifests remain valid while migrating to direct s3:// sources.

Deprecated: this configuration is ignored by the controller.



_Appears in:_
- [AIMClusterRuntimeConfigSpec](#aimclusterruntimeconfigspec)
- [AIMRuntimeConfigCommon](#aimruntimeconfigcommon)
- [AIMRuntimeConfigSpec](#aimruntimeconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled formerly controlled whether the embedded S3 artifact cache was active. It is retained for API compatibility and has no effect. |  | Optional: \{\} |
| `s3Uri` _string_ | S3URI formerly selected the base S3 path for cached artifacts. It is retained for API compatibility and has no effect. |  | Optional: \{\} |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | Env formerly provided S3 endpoint and credential configuration for the embedded cache. It is retained for API compatibility and has no effect. |  | Optional: \{\} |


#### DiscoveredProfileCounts



DiscoveredProfileCounts summarizes the profiles found during image discovery.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ | Total is the number of profiles discovered from the image. |  |  |
| `supported` _integer_ | Supported is the number of discovered profiles currently supported by the cluster. |  |  |
| `unsupported` _integer_ | Unsupported is the number of discovered profiles currently not supported by the cluster. |  |  |
| `byHardware` _[ProfileHardwareGroup](#profilehardwaregroup) array_ | ByHardware groups discovered profiles by their hardware footprint and reports whether each group is currently supported by the cluster. The groups are stable across reconciles (sorted by acceleratorType, acceleratorModel, acceleratorCount) so kubectl/jq queries are cheap. Useful when debugging "why doesn't the \{metric, precision\} profile I expect appear in my cluster?" — the breakdown surfaces every shape the image emits, not only the ones materialised as AIMProfile objects. |  | Optional: \{\} |


#### DiscoveryCacheReference



DiscoveryCacheReference identifies the cached discovery catalog produced from image inspection.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the ConfigMap name. |  |  |
| `namespace` _string_ | Namespace is the ConfigMap namespace. |  |  |


#### DiscoveryState



DiscoveryState tracks the discovery process state for circuit breaker logic. This enables exponential backoff and prevents infinite retry loops when discovery jobs fail persistently.



_Appears in:_
- [AIMServiceTemplateStatus](#aimservicetemplatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `attempts` _integer_ | Attempts is the number of discovery job attempts that have been made. This counter increments each time a new discovery job is created after a failure. |  | Optional: \{\} |
| `lastAttemptTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastAttemptTime is the timestamp of the most recent discovery job creation. Used to calculate exponential backoff before the next retry. |  | Optional: \{\} |
| `lastFailureReason` _string_ | LastFailureReason captures the reason for the most recent discovery failure. Used to classify failures as terminal vs transient. |  | Optional: \{\} |
| `specHash` _string_ | SpecHash is a hash of the template spec fields that affect discovery. When the spec changes, the circuit breaker resets to allow fresh attempts. |  | Optional: \{\} |
| `identityCheckHash` _string_ | IdentityCheckHash records the hash from the most recent identity rediscovery attempt for a Ready template. When the current hash matches this value, the controller will not invalidate a Ready template just to retry identity (aimId/modelId) extraction. Bumping the operator-internal hash version forces a one-shot revisit across all eligible templates without operator intervention. |  | Optional: \{\} |
| `lastIdentityCheckTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastIdentityCheckTime is the timestamp of the most recent identity rediscovery attempt initiation. Acts as a hard floor against rediscovery thrashing in the presence of bugs that prevent IdentityCheckHash from being recorded. |  | Optional: \{\} |


#### DownloadProgress



DownloadProgress represents the download progress for a artifact



_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `totalBytes` _integer_ | TotalBytes is the expected total size of the download in bytes |  | Optional: \{\} |
| `downloadedBytes` _integer_ | DownloadedBytes is the number of bytes downloaded so far |  | Optional: \{\} |
| `percentage` _integer_ | Percentage is the download progress as a percentage (0-100) |  | Maximum: 100 <br />Minimum: 0 <br />Optional: \{\} |
| `displayPercentage` _string_ | DisplayPercentage is a human-readable progress string (e.g., "45 %") This field is automatically populated from Progress.Percentage |  | Optional: \{\} |
| `message` _string_ | Message is an optional human-readable diagnostic from the progress monitor. Empty during normal progress; cleared once the download completes. |  | Optional: \{\} |


#### DownloadState



DownloadState represents the current download attempt state, updated by the downloader pod



_Appears in:_
- [AIMArtifactStatus](#aimartifactstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `protocol` _string_ | Protocol is the download protocol currently in use (e.g., "XET", "HF_TRANSFER", "HTTP") |  | Optional: \{\} |
| `attempt` _integer_ | Attempt is the current attempt number (1-based) |  | Optional: \{\} |
| `totalAttempts` _integer_ | TotalAttempts is the total number of attempts configured via AIM_DOWNLOADER_PROTOCOL |  | Optional: \{\} |
| `protocolSequence` _string_ | ProtocolSequence is the configured protocol sequence (e.g., "HF_TRANSFER,XET") |  | Optional: \{\} |
| `message` _string_ | Message is a human-readable status message from the downloader |  | Optional: \{\} |


#### ImageMetadata



ImageMetadata contains metadata extracted from or provided for a container image.



_Appears in:_
- [AIMModelSpec](#aimmodelspec)
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _[ModelMetadata](#modelmetadata)_ | Model contains AIM model-specific metadata. |  | Optional: \{\} |
| `oci` _[OCIMetadata](#ocimetadata)_ | OCI contains standard OCI image metadata. |  | Optional: \{\} |
| `originalLabels` _object (keys:string, values:string)_ | OriginalLabels contains the raw OCI image labels as a JSON object. This preserves all labels from the image, including those not mapped to structured fields. |  | Optional: \{\} |
| `baseImageRef` _string_ | BaseImageRef is the value of the AIM_BASE_IMAGE_REF environment variable baked into the image's OCI config at build time. For AIM model images this records the base image (e.g. "docker.io/amdenterpriseai/aim-base:0.8.5") that the model image was built from. Used by the AIMModel controller to resolve the deployment image for fine-tuned models whose spec.image is omitted (versionPolicy=latest or any). |  | Optional: \{\} |


#### ManagedProfileCounts



ManagedProfileCounts summarizes managed derivative profiles. The count fields intentionally omit `omitempty` so that a zero count serializes as an explicit `0` rather than dropping the field. This keeps the status shape stable for both kubectl printcolumns and chainsaw assertions — callers can rely on `.status.managedProfiles.{total,ready,...}` always being present once the controller has observed the resource.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)
- [AIMProfileSetStatus](#aimprofilesetstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ | Total is the number of derivative profiles the controller currently manages or intends to manage. |  |  |
| `ready` _integer_ | Ready is the number of derivative profiles whose own status is Ready. |  |  |
| `notAvailable` _integer_ | NotAvailable is the number of derivative profiles whose own status is NotAvailable. |  |  |
| `deployable` _integer_ | Deployable is the number of managed profiles whose spec is materialised enough to back an AIMService (carries aimId and modelSources). For a normal officially-discovered AIMModel this equals Total. For a base-image AIMModel used as a source for custom-model derivation this is 0 — the model only emits base profiles that callers must derive into deployable profiles. |  |  |
| `base` _integer_ | Base counts managed base profile resources with empty aimId and modelSources. They supply custom-model derivation with selector.role=base. Catalog base definitions remain selectable when this materialised count is zero. |  |  |


#### ModelDiscoveryState



ModelDiscoveryState tracks the Kubernetes Job that inspects an AIM image and writes its profile YAMLs into the discovery cache ConfigMap. Mirrors the v1alpha1 AIMServiceTemplate DiscoveryState but scoped to AIMModel semantics.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `attempts` _integer_ | Attempts is the number of discovery job attempts that have been made. Increments each time a new discovery job is created after a failure. |  | Optional: \{\} |
| `lastAttemptTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastAttemptTime is the timestamp of the most recent discovery job creation. Used to calculate exponential backoff before the next retry. |  | Optional: \{\} |
| `lastFailureReason` _string_ | LastFailureReason captures the reason for the most recent discovery failure. |  | Optional: \{\} |
| `specHash` _string_ | SpecHash is a hash of the model spec fields that invalidate cached discovery (image, imagePullSecrets, serviceAccountName, discoveryCommandVersion). When it changes, the operator drops the cache and re-runs the discovery Job. |  | Optional: \{\} |


#### ModelMetadata



ModelMetadata contains AIM model-specific metadata extracted from image labels.
Each field falls back to the legacy org.amd.silogen. label with the same suffix.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `canonicalName` _string_ | CanonicalName is the canonical model identifier (e.g., mistralai/Mixtral-8x22B-Instruct-v0.1). Extracted from: com.amd.aim.model.canonicalName |  | Optional: \{\} |
| `source` _string_ | Source is the URL where the model can be found. Extracted from: com.amd.aim.model.source |  | Optional: \{\} |
| `tags` _string array_ | Tags are descriptive tags (e.g., ["text-generation", "chat", "instruction"]). Extracted from: com.amd.aim.model.tags (comma-separated) |  | Optional: \{\} |
| `versions` _string array_ | Versions lists available versions. Extracted from: com.amd.aim.model.versions (comma-separated) |  | Optional: \{\} |
| `variants` _string array_ | Variants lists model variants. Extracted from: com.amd.aim.model.variants (comma-separated) |  | Optional: \{\} |
| `hfTokenRequired` _boolean_ | HFTokenRequired indicates if a HuggingFace token is required. Extracted from: com.amd.aim.hfToken.required |  | Optional: \{\} |
| `title` _string_ | Title is the AIM-specific title for the model. Extracted from: com.amd.aim.title |  | Optional: \{\} |
| `descriptionFull` _string_ | DescriptionFull is the full description. Extracted from: com.amd.aim.description.full |  | Optional: \{\} |
| `releaseNotes` _string_ | ReleaseNotes contains release notes for this version. Extracted from: com.amd.aim.release.notes |  | Optional: \{\} |
| `recommendedDeployments` _[RecommendedDeployment](#recommendeddeployment) array_ | RecommendedDeployments contains recommended deployment configurations. Extracted from: com.amd.aim.model.recommendedDeployments (parsed from JSON array) |  | Optional: \{\} |


#### ModelSourceFilter



ModelSourceFilter defines an explicit image selector for discovery. Supports multiple formats:
- Repository name: "org/repo" - exact repository match
- Repository with tag: "org/repo:1.0.0" - exact tag match
- Full URI: "ghcr.io/org/repo:1.0.0" - overrides registry and tag
- Full URI: "ghcr.io/org/repo" - exact repository on a specific registry



_Appears in:_
- [AIMClusterModelSourceSpec](#aimclustermodelsourcespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image with explicit repository matching and full URI support. Supported formats:<br />- Repository name: "amdenterpriseai/aim-qwen-qwen3-32b"<br />- Repository with tag: "amdenterpriseai/aim-llama:1.0.0" (overrides versions field)<br />- Full URI: "docker.io/amdenterpriseai/aim-google-gemma-3-1b-it:0.8.1-rc1" (overrides spec.registry and versions)<br />- Full URI without tag: "docker.io/amdenterpriseai/aim-google-gemma-3-1b-it" (overrides spec.registry)<br />When a full URI is specified (including registry like ghcr.io), only images from that registry will match. When a tag is included, it takes precedence over the versions field. Note: wildcard syntax (e.g., "*") is not supported; use explicit image list entries. |  | MaxLength: 512 |
| `exclude` _string array_ | Exclude lists specific repository names to skip (exact match on repository name only, not registry). Useful for excluding base images or experimental versions. Examples:<br />- ["amdenterpriseai/aim-base", "amdenterpriseai/aim-experimental"]<br />- ["amdenterpriseai/aim-base"] - works with "docker.io/amdenterpriseai/aim-*" (registry is not checked in exclusion)<br />Note: Exclusions match against repository names (e.g., "amdenterpriseai/aim-base"), not full URIs. |  | Optional: \{\} |
| `versions` _string array_ | Versions specifies semantic version constraints for this filter. If specified, overrides the global Versions field. Only tags that parse as valid semver are considered (including prereleases like 0.8.1-rc1). Ignored if the Image field includes an explicit tag (e.g., "repo:1.0.0"). Examples: ">=1.0.0", "<2.0.0", "~1.2.0" (patch updates), "^1.0.0" (minor updates) Prerelease versions (e.g., 0.8.1-rc1) are supported and follow semver rules:<br />- 0.8.1-rc1 matches ">=0.8.0" (prerelease is part of version 0.8.1)<br />- Use ">=0.8.1-rc1" to match only that prerelease or higher<br />- Leave empty to match all tags (including prereleases and non-semver tags) |  | Optional: \{\} |


#### OCIMetadata



OCIMetadata contains standard OCI image metadata extracted from image labels.



_Appears in:_
- [ImageMetadata](#imagemetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `title` _string_ | Title is the human-readable title. Extracted from: org.opencontainers.image.title |  | Optional: \{\} |
| `description` _string_ | Description is a brief description. Extracted from: org.opencontainers.image.description |  | Optional: \{\} |
| `licenses` _string_ | Licenses is the SPDX license identifier(s). Extracted from: org.opencontainers.image.licenses |  | Optional: \{\} |
| `vendor` _string_ | Vendor is the organization that produced the image. Extracted from: org.opencontainers.image.vendor |  | Optional: \{\} |
| `authors` _string_ | Authors is contact details of the authors. Extracted from: org.opencontainers.image.authors |  | Optional: \{\} |
| `source` _string_ | Source is the URL to the source code repository. Extracted from: org.opencontainers.image.source |  | Optional: \{\} |
| `documentation` _string_ | Documentation is the URL to documentation. Extracted from: org.opencontainers.image.documentation |  | Optional: \{\} |
| `created` _string_ | Created is the creation timestamp. Extracted from: org.opencontainers.image.created |  | Optional: \{\} |
| `revision` _string_ | Revision is the source control revision. Extracted from: org.opencontainers.image.revision |  | Optional: \{\} |
| `version` _string_ | Version is the image version. Extracted from: org.opencontainers.image.version |  | Optional: \{\} |




#### ProfileGenerationStrategy

_Underlying type:_ _string_

ProfileGenerationStrategy names the mechanism that resolved a Generated model's profiles.

spec.modelId is one onboarding flow with more than one possible resolution mechanism, so the mechanism is reported here rather than folded into AIMModelKind. Recording it separately means the planned AMD catalog lookup (match modelId against published optimized AIM images) can be added without flipping status.kind on an unchanged spec, and without a fifth kind.

RuntimeFallback is the only strategy implemented today.

_Validation:_
- Enum: [RuntimeFallback]

_Appears in:_
- [AIMModelProfileGenerationStatus](#aimmodelprofilegenerationstatus)

| Field | Description |
| --- | --- |
| `RuntimeFallback` | ProfileGenerationStrategyRuntimeFallback indicates profiles were built from RuntimeConfig model.profileGeneration.fallbacks matched against observed cluster hardware. |


#### ProfileHardwareGroup



ProfileHardwareGroup is one accelerator footprint within a model's discovery catalog, with the metric/precision combos shipped under it.



_Appears in:_
- [DiscoveredProfileCounts](#discoveredprofilecounts)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType is the resource family (gpu, cpu). |  | Enum: [gpu cpu] <br />Optional: \{\} |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier (e.g., "MI300X", "EPYC_ZEN5"). Empty for profiles with no accelerator requirement. |  | Optional: \{\} |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units the profile requests. For gpu: device count (e.g., 1, 2, 4, 8 for tensor-parallel sizes). For cpu: number of CPU cores (e.g., 128 for EPYC_ZEN5, 192 for EPYC_9965). |  | Optional: \{\} |
| `supported` _boolean_ | Supported reports whether this hardware footprint is currently satisfied by at least one cluster node. When false, all profiles in this group are skipped during materialisation. |  |  |
| `profiles` _[ProfileHardwareGroupEntry](#profilehardwaregroupentry) array_ | Profiles lists the \{metric, precision\} combinations discovered under this hardware footprint. Reported even when the group is unsupported so users can see what they're missing. |  | Optional: \{\} |


#### ProfileHardwareGroupEntry



ProfileHardwareGroupEntry identifies one profile within a hardware group by its {metric, precision} pair. Sufficient for users to spot whether the optimization variant they want is shipped at all.



_Appears in:_
- [ProfileHardwareGroup](#profilehardwaregroup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target (latency, throughput). |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision (fp4, fp8, bf16, …). |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |


#### ProfileOrigin

_Underlying type:_ _string_

ProfileOrigin classifies how an AIMProfile / AIMClusterProfile was produced. Stamped by AIMProfile reconcilers via the `aim.eai.amd.com/profile-origin` label and the AIMProfile `status.origin` field. Iteration-1 producers stamp `discovered` (image discovery) and `derived` (AIMProfileSet / AIMModel.spec.profiles.derivedFrom); user-authored profiles are backfilled to `user-authored` by the AIMProfile reconciler when no AIM controller owns them.

_Validation:_
- Enum: [discovered derived generated user-authored]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `discovered` | ProfileOriginDiscovered indicates the profile was produced by image discovery (today: AIMModel.spec.image native discovery path). |
| `derived` | ProfileOriginDerived indicates the profile was produced by a derivation flow (AIMModel.spec.profiles.derivedFrom or an AIMProfileSet). |
| `generated` | ProfileOriginGenerated indicates the profile was generated from a modelId declaration and a RuntimeConfig fallback. |
| `user-authored` | ProfileOriginUserAuthored indicates the profile was created independently by a user (no AIM controller owner reference). |


#### ProfileOutputPolicy



ProfileOutputPolicy controls whether profiles produced by a derivation may be used as sources for a later derivation.



_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)
- [AIMProfileSetSpec](#aimprofilesetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `copyable` _boolean_ | Copyable publishes derived profiles as eligible sources for subsequent profile derivations. It is disabled by default so each derivation boundary remains explicit. Disabling copyability, deleting the producer, or changing its selection can remove downstream derived profiles immediately. |  | Optional: \{\} |


#### ProfileOverrides



ProfileOverrides mutates selected source profiles when creating derived copies.

Identity fields (`aimId`, `modelId`, `profileId`) and behavioural fields (`image`, `features`, `acceleratorModel`, `acceleratorCount`, env, args, modelSources) always WRITE onto the derived profile. They are the "stamp on the output" half of the derivation contract — the `selector` half FILTERS source candidates and never mutates anything. Keeping these halves separated is what lets a single YAML mean exactly one thing.

For `selector.role=base` derivations, `overrides.aimId` and `overrides.modelId` are REQUIRED (enforced by CEL on the enclosing spec): base profiles ship with empty identity fields by design, so the derived profile would otherwise have no identity to bind against.



_Appears in:_
- [AIMModelProfilesSpec](#aimmodelprofilesspec)
- [AIMProfileSetSpec](#aimprofilesetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId stamps the derived profile's spec.aimId. When set, wins over the source profile's aimId. REQUIRED when the enclosing selector has role=base (the source base profile carries no aimId of its own). |  | Optional: \{\} |
| `modelId` _string_ | ModelId stamps the derived profile's spec.modelId. When set, wins over both the source profile's modelId AND the auto-derivation from modelSources[0].modelId. REQUIRED when the enclosing selector has role=base. |  | Optional: \{\} |
| `profileId` _string_ | ProfileId stamps the derived profile's spec.profileId. Optional — most callers leave this empty and let the source profile's profileId carry through (or the reconciler synthesise one). |  | Optional: \{\} |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources replaces the copied profile's modelSources. When modelSources[0].modelId is set and overrides.modelId is unset, the derived profile's modelId is auto-derived from modelSources[0]; an explicit overrides.modelId always wins. |  | Optional: \{\} |
| `features` _string array_ | Features adds optional runtime capabilities to the copied profile. Values are unioned with the source profile's features; an override cannot remove a capability advertised by the source. |  | Optional: \{\} |
| `image` _string_ | Image overrides the runtime container image. With no explicit image override, replacing modelSources on a deployable source resolves its base runtime; hardware-only and environment-only overrides retain the source image. This takes precedence over a profile set's top-level image. |  | Optional: \{\} |
| `acceleratorModel` _string_ | AcceleratorModel replaces the copied profile's acceleratorModel. |  | Optional: \{\} |
| `acceleratorVendor` _[AcceleratorVendor](#acceleratorvendor)_ | AcceleratorVendor replaces the copied profile's acceleratorVendor. |  | Enum: [amd nvidia] <br />Optional: \{\} |
| `acceleratorCount` _integer_ | AcceleratorCount replaces the copied profile's acceleratorCount. |  | Optional: \{\} |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode replaces the source partitioning mode. An empty override leaves the source mode unchanged. Uses the same values as AIMProfileSpecCommon.acceleratorPartitioningMode. When set, profile-set and model derivation require explicit source selector.acceleratorCount and target overrides.acceleratorCount. Tensor-parallel size remains an independent engine argument. |  | Optional: \{\} |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) array_ | ContainerEnv merges by env var name, overriding matching source entries. |  | Optional: \{\} |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv merges by key, overriding matching source entries. |  | Optional: \{\} |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs shallow-merges on top of the source engineArgs, overriding matching keys. |  | Type: object <br />Optional: \{\} |


#### ProfileSelector



ProfileSelector filters by source identity, runtime, hardware, and provenance. Derivation evaluates published model definitions and copyable profile resources; AIMService evaluates materialised deployable profile resources. All supplied criteria must match.



_Appears in:_
- [AIMModelProfilesDerivedFrom](#aimmodelprofilesderivedfrom)
- [AIMProfileSetSpec](#aimprofilesetspec)
- [AIMServiceProfileConfig](#aimserviceprofileconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId filters by model architecture identifier (e.g., "qwen/qwen3-32b"). |  | Optional: \{\} |
| `modelId` _string_ | ModelId filters by the source profile's specific model identifier. |  | Optional: \{\} |
| `profileId` _string_ | ProfileId filters by the source profile's profile identifier. |  | Optional: \{\} |
| `engine` _string_ | Engine filters by inference engine. |  | Optional: \{\} |
| `metric` _[AIMMetric](#aimmetric)_ | Metric filters by optimization target. |  | Enum: [latency throughput] <br />Optional: \{\} |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision filters by numeric precision. |  | Enum: [auto fp4 fp8 fp16 fp32 fp64 bf16 int4 int8] <br />Optional: \{\} |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type filters by optimization level (exact match). |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} |
| `minimumType` _[AIMProfileTypeFloor](#aimprofiletypefloor)_ | MinimumType filters by a minimum optimization level: candidates whose type is this tier OR BETTER are accepted (hierarchy: optimized > general > preview > unoptimized). This is the floor counterpart to the exact-match Type field; the two AND together when both are set. The sentinel "any" disables the floor (accept every tier). When this field is empty the AIMService resolver applies a default floor of "optimized" so auto-selection prefers production-grade profiles and never silently picks an unoptimized one; to opt a service into lower tiers (e.g. CPU/EPYC profiles published as unoptimized) set minimumType explicitly to "unoptimized" or "any". Derivation selectors (AIMProfileSet / AIMModel.profiles) treat empty as "any" so copying is never tier-restricted by default. |  | Enum: [optimized general preview unoptimized any] <br />Optional: \{\} |
| `acceleratorModel` _string_ | AcceleratorModel filters by accelerator identifier. |  | Optional: \{\} |
| `acceleratorVendor` _[AcceleratorVendor](#acceleratorvendor)_ | AcceleratorVendor filters by the vendor-specific device-plugin contract. |  | Enum: [amd nvidia] <br />Optional: \{\} |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode filters candidates by their declared partitioning mode. Partial-order match (NOT strict equality):<br />""              - no filter on this field.<br />"unpartitioned" - matches profiles with mode "" or "unpartitioned".<br />"partitioned"   - matches profiles whose mode is non-trivial (anything other than "" / "unpartitioned").<br />"<C>"           - selector-only convenience: matches profiles with mode "<C>-*" (prefix on the scheme). Not a valid profile-spec value (e.g. selector "CPX" matches "CPX-NPS1", "CPX-NPS4").<br />"<C>-<M>"       - exact-string match on the scheme. |  | Optional: \{\} |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType filters by accelerator resource type. |  | Enum: [gpu cpu] <br />Optional: \{\} |
| `acceleratorCount` _integer_ | AcceleratorCount filters by accelerator unit count. |  | Optional: \{\} |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#json-v1-apiextensions-k8s-io)_ | EngineArgs partially matches source engineArgs: every provided top-level key must exist in the source object with an equal value. |  | Type: object <br />Optional: \{\} |
| `modelRef` _[ProfileSelectorModelRef](#profileselectormodelref)_ | ModelRef optionally narrows candidates to definitions and profiles published by a specific AIM(Cluster)Model. Omit to select across models. |  | Optional: \{\} |
| `role` _[ProfileSelectorRole](#profileselectorrole)_ | Role filters by the `aim.eai.amd.com/profile-role` label. Defaults to `deployable`. `base` filters to base profiles emitted by base-image discovery (custom-model derivation source material). | deployable | Enum: [base deployable] <br />Optional: \{\} |
| `origin` _[ProfileOrigin](#profileorigin)_ | Origin filters source provenance across catalog and resource representations. Empty means no origin filter. |  | Enum: [discovered derived generated user-authored] <br />Optional: \{\} |


#### ProfileSelectorModelRef



ProfileSelectorModelRef narrows derivation candidates by their owning AIMModel / AIMClusterModel. Definitions carry model ownership before materialisation; profile resources expose that ownership through the `aim.eai.amd.com/source-model[-scope]` labels.



_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the owning AIM(Cluster)Model name. Required. |  | MinLength: 1 |
| `scope` _[ProfileSelectorScope](#profileselectorscope)_ | Scope controls how Name is resolved against AIMModel vs AIMClusterModel. | Auto | Enum: [Auto Namespace Cluster] <br />Optional: \{\} |


#### ProfileSelectorRole

_Underlying type:_ _string_

ProfileSelectorRole filters source profiles by their role label (`aim.eai.amd.com/profile-role`). Discovery of a deployable AIM image stamps the `deployable` role; base-image discovery stamps `base` on profiles that carry no aimId/modelSources, which custom-model AIMModels derive into deployable copies.

_Validation:_
- Enum: [base deployable]

_Appears in:_
- [ProfileSelector](#profileselector)

| Field | Description |
| --- | --- |
| `base` | ProfileSelectorRoleBase filters down to base profiles that are not yet deployable (no aimId / modelSources, only image + base-image so they can serve as derivation source material for custom-model AIMModels). |
| `deployable` | ProfileSelectorRoleDeployable filters down to fully deployable profiles (with aimId and modelSources). This is the default. |


#### ProfileSelectorScope

_Underlying type:_ _string_

ProfileSelectorScope determines how a ProfileSelector.ModelRef resolves the source AIMModel scope. The v1alpha2 AIMProfileSet reconciler honours this value when filtering source profiles by `aim.eai.amd.com/source-model[-scope]` labels.

_Validation:_
- Enum: [Auto Namespace Cluster]

_Appears in:_
- [ProfileSelectorModelRef](#profileselectormodelref)

| Field | Description |
| --- | --- |
| `Auto` | ProfileSelectorScopeAuto selects fully matching namespace candidates first, then falls back to cluster candidates with the same model name when no local match exists. Cluster sets select cluster sources only. |
| `Namespace` | ProfileSelectorScopeNamespace requires the source profile to come from an AIMModel in the same namespace as the selecting AIMProfileSet. |
| `Cluster` | ProfileSelectorScopeCluster requires the source profile to come from a cluster-scoped AIMClusterModel. |


#### ProfileSetReference



ProfileSetReference identifies the profile set synthesized by a model for derivation flows.



_Appears in:_
- [AIMModelStatus](#aimmodelstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the profile set name. |  |  |
| `namespace` _string_ | Namespace is the profile set namespace. |  |  |


#### ProfileSourceRef



ProfileSourceRef identifies an alternate catalog ConfigMap for derivation. It replaces default selection across model definitions and copyable profiles.



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
| `pinned` | ProfileVersionPolicyPinned matches the requested source version; requires version. |
| `latest` | ProfileVersionPolicyLatest selects the highest matching source version. |
| `all` | ProfileVersionPolicyAll selects all matching profiles, including versionless sources. |


#### RecommendedDeployment



RecommendedDeployment describes a recommended deployment configuration for a model.



_Appears in:_
- [ModelMetadata](#modelmetadata)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuModel` _string_ | GPUModel is the GPU model name (e.g., MI300X, MI325X). The legacy v1alpha1 schema only models GPU accelerators; CPU profiles emitted via OCI labels (gpuModel="CPU") are not first-class here and are handled exclusively by the v1alpha2 native discovery pipeline. |  | Optional: \{\} |
| `gpuCount` _integer_ | GPUCount is the number of GPUs required |  | Optional: \{\} |
| `precision` _string_ | Precision is the recommended precision (e.g., fp8, fp16, bf16) |  | Optional: \{\} |
| `metric` _string_ | Metric is the optimization target (e.g., latency, throughput) |  | Optional: \{\} |
| `profileId` _string_ | ProfileId is the unique identifier of the AIM profile for this deployment. When set, templates created from this deployment will use this profile ID to deterministically select the correct runtime profile in the AIM container. |  | Optional: \{\} |
| `description` _string_ | Description provides additional context about this deployment configuration |  | Optional: \{\} |


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
| `runtimeConfigName` _string_ | Name is the name of the runtime config to use for this resource. If a runtime config with this name exists both as a namespace and a cluster runtime config, the values are merged together, the namespace config taking priority over the cluster config when there are conflicts. If this field is empty or set to `default`, the namespace / cluster runtime config with the name `default` is used, if it exists. |  | Optional: \{\} |


#### S3AddressingStyle

_Underlying type:_ _string_

S3AddressingStyle controls how the bucket is encoded in S3 HTTP requests.

_Validation:_
- Enum: [auto path virtual]

_Appears in:_
- [S3ConnectionConfig](#s3connectionconfig)

| Field | Description |
| --- | --- |
| `auto` |  |
| `path` |  |
| `virtual` |  |


#### S3AuthConfig



S3AuthConfig configures S3 authentication.



_Appears in:_
- [S3ConnectionConfig](#s3connectionconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[S3AuthMode](#s3authmode)_ | Mode selects the authentication strategy. Omitted mode defaults to chain. | chain | Enum: [chain static anonymous] <br />Optional: \{\} |
| `credentialsSecretRef` _[S3CredentialsSecretReference](#s3credentialssecretreference)_ | CredentialsSecretRef is required for static authentication. |  | Optional: \{\} |


#### S3AuthMode

_Underlying type:_ _string_

S3AuthMode selects how the S3 client obtains credentials.

_Validation:_
- Enum: [chain static anonymous]

_Appears in:_
- [S3AuthConfig](#s3authconfig)

| Field | Description |
| --- | --- |
| `chain` | S3AuthModeChain uses the standard boto3 credential provider chain. |
| `static` | S3AuthModeStatic loads credentials from a namespace-local Secret. |
| `anonymous` | S3AuthModeAnonymous sends unsigned requests to a public bucket. |


#### S3CABundleReference

_Underlying type:_ _[struct{Kind string "json:\"kind\""; Name string "json:\"name\""; Key string "json:\"key\""}](#struct{kind-string-"json:\"kind\"";-name-string-"json:\"name\"";-key-string-"json:\"key\""})_

S3CABundleReference identifies a PEM CA bundle in a namespace-local ConfigMap or Secret.



_Appears in:_
- [S3TLSConfig](#s3tlsconfig)



#### S3ConnectionConfig



S3ConnectionConfig provides typed S3 endpoint, authentication, addressing, signing, and TLS settings.



_Appears in:_
- [AIMArtifactConfig](#aimartifactconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | Endpoint is an optional S3-compatible API endpoint. Leave empty for AWS S3. Custom endpoints must include an explicit http:// or https:// scheme. |  | Pattern: `^https?://[^ \t\r\n]+$` <br />Optional: \{\} |
| `region` _string_ | Region is the signing region. Custom endpoints default to us-east-1 when omitted; AWS S3 uses normal SDK region resolution. |  | Optional: \{\} |
| `addressingStyle` _[S3AddressingStyle](#s3addressingstyle)_ | AddressingStyle controls path-style versus virtual-hosted bucket routing. Custom endpoints default to path when omitted. |  | Enum: [auto path virtual] <br />Optional: \{\} |
| `signatureVersion` _[S3SignatureVersion](#s3signatureversion)_ | SignatureVersion controls authenticated request signing. |  | Enum: [auto s3v4] <br />Optional: \{\} |
| `auth` _[S3AuthConfig](#s3authconfig)_ | Auth configures credential-chain, static, or anonymous access. Omitted auth defaults to the standard SDK credential provider chain. |  | Optional: \{\} |
| `tls` _[S3TLSConfig](#s3tlsconfig)_ | TLS configures custom CA trust or the unsafe verification bypass. |  | Optional: \{\} |


#### S3CredentialsSecretReference

_Underlying type:_ _[struct{Name string "json:\"name\""; AccessKeyIDKey string "json:\"accessKeyIdKey,omitempty\""; SecretAccessKeyKey string "json:\"secretAccessKeyKey,omitempty\""; SessionTokenKey string "json:\"sessionTokenKey,omitempty\""}](#struct{name-string-"json:\"name\"";-accesskeyidkey-string-"json:\"accesskeyidkey,omitempty\"";-secretaccesskeykey-string-"json:\"secretaccesskeykey,omitempty\"";-sessiontokenkey-string-"json:\"sessiontokenkey,omitempty\""})_

S3CredentialsSecretReference identifies credential keys in a namespace-local Secret.



_Appears in:_
- [S3AuthConfig](#s3authconfig)



#### S3SignatureVersion

_Underlying type:_ _string_

S3SignatureVersion controls request signing for authenticated S3 requests.

_Validation:_
- Enum: [auto s3v4]

_Appears in:_
- [S3ConnectionConfig](#s3connectionconfig)

| Field | Description |
| --- | --- |
| `auto` |  |
| `s3v4` |  |


#### S3TLSConfig



S3TLSConfig controls TLS certificate and hostname verification.



_Appears in:_
- [S3ConnectionConfig](#s3connectionconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `caBundleRef` _[S3CABundleReference](#s3cabundlereference)_ | CABundleRef selects a namespace-local PEM CA bundle for S3 TLS verification. |  | Optional: \{\} |
| `insecureSkipVerify` _boolean_ | InsecureSkipVerify disables certificate-chain and hostname verification. This is unsafe and intended only as a temporary diagnostic escape hatch. |  | Optional: \{\} |
