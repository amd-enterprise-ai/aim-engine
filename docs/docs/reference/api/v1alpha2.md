# API Reference

## Packages
- [aim.eai.amd.com/v1alpha2](#aimeaiamdcomv1alpha2)


## aim.eai.amd.com/v1alpha2

Package v1alpha2 contains API Schema definitions for the aim v1alpha2 API group.

### Resource Types
- [AIMClusterModel](#aimclustermodel)
- [AIMClusterModelList](#aimclustermodellist)
- [AIMClusterProfile](#aimclusterprofile)
- [AIMClusterProfileList](#aimclusterprofilelist)
- [AIMClusterProfileSet](#aimclusterprofileset)
- [AIMClusterProfileSetList](#aimclusterprofilesetlist)
- [AIMModel](#aimmodel)
- [AIMModelList](#aimmodellist)
- [AIMProfile](#aimprofile)
- [AIMProfileCache](#aimprofilecache)
- [AIMProfileCacheList](#aimprofilecachelist)
- [AIMProfileList](#aimprofilelist)
- [AIMProfileSet](#aimprofileset)
- [AIMProfileSetList](#aimprofilesetlist)
- [AIMService](#aimservice)
- [AIMServiceList](#aimservicelist)



#### AIMClusterModel



AIMClusterModel is the Schema for cluster-scoped v1alpha2 AIM model resources.
See AIMModel (api/v1alpha2/aimmodel_types.go) for the rationale of the
CEL rules below — AIMClusterModel mirrors the namespace-scoped contract.



_Appears in:_
- [AIMClusterModelList](#aimclustermodellist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterModel` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMClusterModelList



AIMClusterModelList contains a list of AIMClusterModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterModel](#aimclustermodel) array_ |  |  |  |


#### AIMClusterProfile



AIMClusterProfile is the Schema for cluster-scoped AIM profiles.
Cluster profiles are visible across all namespaces. They can be created manually
or, in the future, automatically during model discovery by a v1alpha2 model controller.
Unlike namespace-scoped AIMProfiles, cluster profiles do not support caching
configuration since caches are namespace-scoped.

Deployable profiles have both aimId and modelSources populated; base
profiles (custom-model derivation source material) have neither. Mixed
(one of the two set) is rejected to keep status.deployable derivable from
spec.



_Appears in:_
- [AIMClusterProfileList](#aimclusterprofilelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMClusterProfileSpec](#aimclusterprofilespec)_ |  |  |  |
| `status` _[AIMProfileStatus](#aimprofilestatus)_ |  |  |  |


#### AIMClusterProfileList



AIMClusterProfileList contains a list of AIMClusterProfile.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterProfileList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterProfile](#aimclusterprofile) array_ |  |  |  |


#### AIMClusterProfileSet



AIMClusterProfileSet is the Schema for cluster-scoped profile derivation resources.



_Appears in:_
- [AIMClusterProfileSetList](#aimclusterprofilesetlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterProfileSet` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMProfileSetSpec](#aimprofilesetspec)_ |  |  |  |
| `status` _[AIMProfileSetStatus](#aimprofilesetstatus)_ |  |  |  |


#### AIMClusterProfileSetList



AIMClusterProfileSetList contains a list of AIMClusterProfileSet.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMClusterProfileSetList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMClusterProfileSet](#aimclusterprofileset) array_ |  |  |  |


#### AIMClusterProfileSpec



AIMClusterProfileSpec defines the desired state of a cluster-scoped AIMClusterProfile.



_Appears in:_
- [AIMClusterProfile](#aimclusterprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding.<br />AimId is required for deployable profiles. Iteration 1 producers always<br />emit deployable profiles, so AimId is effectively required there. Empty<br />AimId is reserved for base profiles emitted by base-image discovery<br />(custom-model derivation source material), which are not deployable<br />until derived.<br />Once set, AimId is immutable. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `manualSelectionOnly` _boolean_ | ManualSelectionOnly excludes this profile from automatic AIMService selection.<br />It remains addressable by explicit name and is preserved from aim-build profile YAMLs. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "EPYC_ZEN5") — the AcceleratorDetector labels nodes<br />with all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required.<br />For AcceleratorType=gpu, this is the device count (e.g., 1, 2, 4, 8<br />for tensor-parallel sizes). For AcceleratorType=cpu, this is the<br />number of CPU cores (e.g., 128 for EPYC_ZEN5, 192 for EPYC_9965).<br />Combined with cluster-level configuration to compute default<br />resource requests in status.resources.<br />For AcceleratorType=gpu, the per-unit interpretation depends on<br />AcceleratorPartitioningMode: under "unpartitioned" (default) one unit is<br />one whole GPU; under "partitioned" or a specific scheme one unit is one<br />partition slice (e.g. CPX-NPS4 = 1/8 of a GPU). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode declares the GPU partition state the profile<br />requires. Free-form string with reserved values:<br />  ""              - omitted; CRD-defaulted to "unpartitioned".<br />  "unpartitioned" - hardware-default partition state. Matches unpartitioned<br />                    MI300X (canonical SPX-NPS1) AND non-partitionable<br />                    hardware (Radeon, etc.) — any node whose detector<br />                    stamped aim-accelerator.partitioning-scheme.default.<br />  "partitioned"   - any actively partitioned mode. Excludes both<br />                    unpartitioned partitionable hardware and<br />                    non-partitionable hardware.<br />  "<C>-<M>"       - specific compute+memory scheme, e.g. "CPX-NPS4". Matches<br />                    only nodes carrying that exact scheme label; does not<br />                    match non-partitionable hardware.<br />Other values (e.g. "CPX" alone, or typos) are accepted but fail-safe to<br />zero matching nodes under this iteration's label schema — compute-only /<br />memory-only matching is not supported here. AIM Engine does NOT validate<br />against AMD's hardware compatibility matrix; invalid combinations simply<br />report MatchingNodes == 0.<br />Resolves to a single Exists or DoesNotExist node-affinity term on the<br />aim-accelerator.partitioning-scheme.* labels published by the<br />AcceleratorDetector, AND-ed with the acceleratorModel term. | unpartitioned | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources is an optional override for K8s resource requests/limits.<br />When set, merged on top of the defaults that AIM Engine computes from<br />AcceleratorType, AcceleratorCount, and cluster-level configuration.<br />The resolved result is written to status.resources. |  | Optional: \{\} <br /> |
| `image` _string_ | Image is the deployment container image. Required.<br />For purpose-built profiles: the full AIM image.<br />For custom weight profiles: the base image (e.g., aim-base:0.8.5). |  | MinLength: 1 <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies model artifact sources for this profile.<br />Populated during discovery or set by user. |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv specifies container-level env vars for the AIM runtime process (K8s pod spec). |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets for pulling container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the service account for workloads. |  | Optional: \{\} <br /> |




#### AIMModel



AIMModel is the Schema for the v1alpha2 AIMModel API.

AIMModel mirrors AIMService: the canonical Spec/Status types live in
v1alpha1 and the v1alpha2 wrapper is a thin re-export so JSON wire format
is identical between versions and the None conversion strategy works
without a webhook.

v1alpha2 onboarding contract: exactly one of spec.image (official
discovery) or spec.profiles (fine-tune / custom-model derivation); legacy
v1alpha1 onboarding fields and the deprecated flat spec.derivedFrom shape
are forbidden on NEW v1alpha2 objects. Each "forbidden" rule uses
optionalOldSelf so that objects originally created via v1alpha1 (which
legally carry spec.custom, spec.modelSources, spec.customTemplates,
spec.profileCopy) and the early-iteration v1alpha2 objects (carrying
spec.derivedFrom) can still be updated through the v1alpha2 surface — the
reconciler must be able to add finalizers and patch the spec of
legacy-shaped objects without being blocked by the v1alpha2 schema. Adding
a legacy/deprecated field to an object that did not previously have it is
still rejected.



_Appears in:_
- [AIMModelList](#aimmodellist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMModel` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMModelSpec](#aimmodelspec)_ |  |  |  |
| `status` _[AIMModelStatus](#aimmodelstatus)_ |  |  |  |


#### AIMModelList



AIMModelList contains a list of AIMModel.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMModelList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMModel](#aimmodel) array_ |  |  |  |






#### AIMProfile



AIMProfile is the Schema for namespace-scoped AIM profiles.
A profile is a self-contained runtime configuration that answers five questions without
consulting any other resource: model architecture, accelerator, K8s resources, runtime
config, and container image.

Deployable profiles have both aimId and modelSources populated; base
profiles (custom-model derivation source material) have neither. Mixed
(one of the two set) is rejected to keep status.deployable derivable from
spec.



_Appears in:_
- [AIMProfileList](#aimprofilelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMProfileSpec](#aimprofilespec)_ |  |  |  |
| `status` _[AIMProfileStatus](#aimprofilestatus)_ |  |  |  |


#### AIMProfileCache



AIMProfileCache pre-warms model artifacts for a specified profile's model sources.



_Appears in:_
- [AIMProfileCacheList](#aimprofilecachelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfileCache` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMProfileCacheSpec](#aimprofilecachespec)_ |  |  |  |
| `status` _[AIMProfileCacheStatus](#aimprofilecachestatus)_ |  |  |  |


#### AIMProfileCacheList



AIMProfileCacheList contains a list of AIMProfileCache.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfileCacheList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMProfileCache](#aimprofilecache) array_ |  |  |  |


#### AIMProfileCacheMode

_Underlying type:_ _string_

AIMProfileCacheMode controls the ownership behavior of artifacts created by a profile cache.

_Validation:_
- Enum: [Dedicated Shared]

_Appears in:_
- [AIMProfileCacheSpec](#aimprofilecachespec)

| Field | Description |
| --- | --- |
| `Dedicated` | ProfileCacheModeDedicated means artifacts are owned by this profile cache and<br />garbage collected when it is deleted.<br /> |
| `Shared` | ProfileCacheModeShared means artifacts have no owner references and persist<br />independently of the profile cache lifecycle. This is the default mode.<br /> |


#### AIMProfileCacheSpec



AIMProfileCacheSpec defines the desired state of AIMProfileCache.



_Appears in:_
- [AIMProfileCache](#aimprofilecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `profileName` _string_ | ProfileName is the name of the AIMProfile or AIMClusterProfile to cache.<br />The controller resolves model sources from the referenced profile's spec.modelSources. |  | MinLength: 1 <br /> |
| `profileScope` _[AIMResolutionScope](#aimresolutionscope)_ | ProfileScope indicates whether the profile is namespace-scoped or cluster-scoped. |  | Enum: [Namespace Cluster] <br />Required: \{\} <br /> |
| `storageClassName` _string_ | StorageClassName specifies the storage class for cache volumes.<br />When not specified, uses the cluster default storage class. |  | Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for authentication when downloading models.<br />These variables are used for authentication with model registries (e.g., HuggingFace tokens). |  | Optional: \{\} <br /> |
| `mode` _[AIMProfileCacheMode](#aimprofilecachemode)_ | Mode controls the ownership behavior of artifacts created by this profile cache.<br />- Dedicated: artifacts are owned by this profile cache and garbage collected when it's deleted.<br />- Shared (default): artifacts have no owner references and persist independently. | Shared | Enum: [Dedicated Shared] <br />Optional: \{\} <br /> |


#### AIMProfileCacheStatus



AIMProfileCacheStatus defines the observed state of AIMProfileCache.



_Appears in:_
- [AIMProfileCache](#aimprofilecache)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of the profile cache state. |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high-level status of the profile cache. | Pending | Enum: [Pending Progressing Ready Failed Degraded NotAvailable] <br /> |
| `artifacts` _object (keys:string, values:AIMResolvedArtifact)_ | Artifacts maps artifact names to their resolved AIMArtifact resources. |  | Optional: \{\} <br /> |


#### AIMProfileCachingConfig



AIMProfileCachingConfig configures model caching behavior for namespace-scoped profiles.



_Appears in:_
- [AIMProfileSpec](#aimprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled controls whether caching is enabled for this profile. | false |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies environment variables for model download during caching.<br />If not set, falls back to the profile's ContainerEnv. |  | Optional: \{\} <br /> |


#### AIMProfileList



AIMProfileList contains a list of AIMProfile.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfileList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMProfile](#aimprofile) array_ |  |  |  |


#### AIMProfileSet



AIMProfileSet is the Schema for namespace-scoped profile derivation resources.



_Appears in:_
- [AIMProfileSetList](#aimprofilesetlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfileSet` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMProfileSetSpec](#aimprofilesetspec)_ |  |  |  |
| `status` _[AIMProfileSetStatus](#aimprofilesetstatus)_ |  |  |  |


#### AIMProfileSetList



AIMProfileSetList contains a list of AIMProfileSet.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfileSetList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMProfileSet](#aimprofileset) array_ |  |  |  |






#### AIMProfileSpec



AIMProfileSpec defines the desired state of a namespace-scoped AIMProfile.



_Appears in:_
- [AIMProfile](#aimprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding.<br />AimId is required for deployable profiles. Iteration 1 producers always<br />emit deployable profiles, so AimId is effectively required there. Empty<br />AimId is reserved for base profiles emitted by base-image discovery<br />(custom-model derivation source material), which are not deployable<br />until derived.<br />Once set, AimId is immutable. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `manualSelectionOnly` _boolean_ | ManualSelectionOnly excludes this profile from automatic AIMService selection.<br />It remains addressable by explicit name and is preserved from aim-build profile YAMLs. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "EPYC_ZEN5") — the AcceleratorDetector labels nodes<br />with all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required.<br />For AcceleratorType=gpu, this is the device count (e.g., 1, 2, 4, 8<br />for tensor-parallel sizes). For AcceleratorType=cpu, this is the<br />number of CPU cores (e.g., 128 for EPYC_ZEN5, 192 for EPYC_9965).<br />Combined with cluster-level configuration to compute default<br />resource requests in status.resources.<br />For AcceleratorType=gpu, the per-unit interpretation depends on<br />AcceleratorPartitioningMode: under "unpartitioned" (default) one unit is<br />one whole GPU; under "partitioned" or a specific scheme one unit is one<br />partition slice (e.g. CPX-NPS4 = 1/8 of a GPU). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode declares the GPU partition state the profile<br />requires. Free-form string with reserved values:<br />  ""              - omitted; CRD-defaulted to "unpartitioned".<br />  "unpartitioned" - hardware-default partition state. Matches unpartitioned<br />                    MI300X (canonical SPX-NPS1) AND non-partitionable<br />                    hardware (Radeon, etc.) — any node whose detector<br />                    stamped aim-accelerator.partitioning-scheme.default.<br />  "partitioned"   - any actively partitioned mode. Excludes both<br />                    unpartitioned partitionable hardware and<br />                    non-partitionable hardware.<br />  "<C>-<M>"       - specific compute+memory scheme, e.g. "CPX-NPS4". Matches<br />                    only nodes carrying that exact scheme label; does not<br />                    match non-partitionable hardware.<br />Other values (e.g. "CPX" alone, or typos) are accepted but fail-safe to<br />zero matching nodes under this iteration's label schema — compute-only /<br />memory-only matching is not supported here. AIM Engine does NOT validate<br />against AMD's hardware compatibility matrix; invalid combinations simply<br />report MatchingNodes == 0.<br />Resolves to a single Exists or DoesNotExist node-affinity term on the<br />aim-accelerator.partitioning-scheme.* labels published by the<br />AcceleratorDetector, AND-ed with the acceleratorModel term. | unpartitioned | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources is an optional override for K8s resource requests/limits.<br />When set, merged on top of the defaults that AIM Engine computes from<br />AcceleratorType, AcceleratorCount, and cluster-level configuration.<br />The resolved result is written to status.resources. |  | Optional: \{\} <br /> |
| `image` _string_ | Image is the deployment container image. Required.<br />For purpose-built profiles: the full AIM image.<br />For custom weight profiles: the base image (e.g., aim-base:0.8.5). |  | MinLength: 1 <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies model artifact sources for this profile.<br />Populated during discovery or set by user. |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv specifies container-level env vars for the AIM runtime process (K8s pod spec). |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets for pulling container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the service account for workloads. |  | Optional: \{\} <br /> |
| `caching` _[AIMProfileCachingConfig](#aimprofilecachingconfig)_ | Caching configures model caching behavior for this namespace-scoped profile. |  | Optional: \{\} <br /> |


#### AIMProfileSpecCommon



AIMProfileSpecCommon contains spec fields shared between AIMProfile and AIMClusterProfile.
A profile answers five questions without consulting any other resource: model architecture
(aimId), accelerator (acceleratorModel/Type/Count), K8s resources (status.resources),
runtime config (engineArgs, engineEnv), and container image (image).



_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMProfileSpec](#aimprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding.<br />AimId is required for deployable profiles. Iteration 1 producers always<br />emit deployable profiles, so AimId is effectively required there. Empty<br />AimId is reserved for base profiles emitted by base-image discovery<br />(custom-model derivation source material), which are not deployable<br />until derived.<br />Once set, AimId is immutable. |  | Optional: \{\} <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `manualSelectionOnly` _boolean_ | ManualSelectionOnly excludes this profile from automatic AIMService selection.<br />It remains addressable by explicit name and is preserved from aim-build profile YAMLs. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "EPYC_ZEN5") — the AcceleratorDetector labels nodes<br />with all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required.<br />For AcceleratorType=gpu, this is the device count (e.g., 1, 2, 4, 8<br />for tensor-parallel sizes). For AcceleratorType=cpu, this is the<br />number of CPU cores (e.g., 128 for EPYC_ZEN5, 192 for EPYC_9965).<br />Combined with cluster-level configuration to compute default<br />resource requests in status.resources.<br />For AcceleratorType=gpu, the per-unit interpretation depends on<br />AcceleratorPartitioningMode: under "unpartitioned" (default) one unit is<br />one whole GPU; under "partitioned" or a specific scheme one unit is one<br />partition slice (e.g. CPX-NPS4 = 1/8 of a GPU). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `acceleratorPartitioningMode` _string_ | AcceleratorPartitioningMode declares the GPU partition state the profile<br />requires. Free-form string with reserved values:<br />  ""              - omitted; CRD-defaulted to "unpartitioned".<br />  "unpartitioned" - hardware-default partition state. Matches unpartitioned<br />                    MI300X (canonical SPX-NPS1) AND non-partitionable<br />                    hardware (Radeon, etc.) — any node whose detector<br />                    stamped aim-accelerator.partitioning-scheme.default.<br />  "partitioned"   - any actively partitioned mode. Excludes both<br />                    unpartitioned partitionable hardware and<br />                    non-partitionable hardware.<br />  "<C>-<M>"       - specific compute+memory scheme, e.g. "CPX-NPS4". Matches<br />                    only nodes carrying that exact scheme label; does not<br />                    match non-partitionable hardware.<br />Other values (e.g. "CPX" alone, or typos) are accepted but fail-safe to<br />zero matching nodes under this iteration's label schema — compute-only /<br />memory-only matching is not supported here. AIM Engine does NOT validate<br />against AMD's hardware compatibility matrix; invalid combinations simply<br />report MatchingNodes == 0.<br />Resolves to a single Exists or DoesNotExist node-affinity term on the<br />aim-accelerator.partitioning-scheme.* labels published by the<br />AcceleratorDetector, AND-ed with the acceleratorModel term. | unpartitioned | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources is an optional override for K8s resource requests/limits.<br />When set, merged on top of the defaults that AIM Engine computes from<br />AcceleratorType, AcceleratorCount, and cluster-level configuration.<br />The resolved result is written to status.resources. |  | Optional: \{\} <br /> |
| `image` _string_ | Image is the deployment container image. Required.<br />For purpose-built profiles: the full AIM image.<br />For custom weight profiles: the base image (e.g., aim-base:0.8.5). |  | MinLength: 1 <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies model artifact sources for this profile.<br />Populated during discovery or set by user. |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv specifies container-level env vars for the AIM runtime process (K8s pod spec). |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets for pulling container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the service account for workloads. |  | Optional: \{\} <br /> |


#### AIMProfileStatus



AIMProfileStatus defines the observed state of AIMProfile / AIMClusterProfile.



_Appears in:_
- [AIMClusterProfile](#aimclusterprofile)
- [AIMProfile](#aimprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  |  |
| `status` _[AIMStatus](#aimstatus)_ | Status represents the current high-level status of this profile.<br />Ready: at least one cluster node matches the profile's accelerator labels and resource requests.<br />NotAvailable: no matching nodes found. | Pending | Enum: [Pending Progressing Ready Degraded Failed NotAvailable] <br /> |
| `deployable` _boolean_ | Deployable reports whether the profile is materialised enough to back an<br />AIMService: true when spec.aimId and spec.modelSources are both<br />populated, false for base profiles awaiting derivation.<br />Image discovery of a deployable AIM image emits deployable profiles;<br />base-image discovery emits base profiles (no aimId/modelSources) that<br />a custom-model AIMModel derives into deployable copies. | false |  |
| `sourceModel` _[ProfileSourceModel](#profilesourcemodel)_ | SourceModel identifies the producing AIM(Cluster)Model for profiles<br />owned by AIMModel reconcilers. Empty for user-authored profiles. |  | Optional: \{\} <br /> |
| `origin` _[ProfileOrigin](#profileorigin)_ | Origin classifies how this profile was produced:<br />  - discovered: emitted by image discovery (AIMModel.spec.image).<br />  - derived: emitted by an AIMProfileSet or<br />    AIMModel.spec.profiles.derivedFrom.<br />  - user-authored: created independently by a user.<br />Backfilled by the AIMProfile reconciler when not stamped at creation<br />time; user-authored profiles default to `user-authored`. |  | Enum: [discovered derived user-authored] <br />Optional: \{\} <br /> |
| `version` _string_ | Version is extracted from the spec.image tag during reconciliation (e.g., "0.8.5"). |  | Optional: \{\} <br /> |
| `baseImage` _string_ | BaseImage is the AIM_BASE_IMAGE_REF the inspector extracted from the<br />source image when this profile was materialised by AIMModel discovery.<br />Used by derivation flows (AIMService overlays, AIMProfileSet) to rebase<br />the deployment image onto the source's base when overriding model<br />sources, so private mirrors stay self-contained. Empty for<br />user-authored profiles. |  | Optional: \{\} <br /> |
| `matchingNodes` _integer_ | MatchingNodes is the count of cluster nodes matching both the accelerator<br />model label and status.resources requests. Zero means NotAvailable. |  | Optional: \{\} <br /> |
| `hardwareSummary` _string_ | HardwareSummary is a human-readable string describing the hardware requirements.<br />Format: "\{count\} x \{model\}" for GPU (e.g., "1 x MI300X") or "CPU" for CPU-only. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources contains the definitive K8s resource requests/limits used for deployment.<br />Computed by AIM Engine from AcceleratorType, AcceleratorCount, and cluster-level<br />configuration, then merged with any spec.resources override. |  | Optional: \{\} <br /> |
| `resolvedNodeAffinity` _[NodeAffinity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#nodeaffinity-v1-core)_ | ResolvedNodeAffinity contains the computed node affinity rules derived from<br />spec.acceleratorModel. Used by AIMService when building InferenceService pods. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of profile state. |  |  |




#### AIMService



AIMService manages a KServe-based AIM inference service for the selected model and template.
Note: KServe uses {name}-{namespace} format which must not exceed 63 characters.
This constraint is validated at runtime since CEL cannot access metadata.namespace.



_Appears in:_
- [AIMServiceList](#aimservicelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMService` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMServiceSpec](#aimservicespec)_ |  |  |  |
| `status` _[AIMServiceStatus](#aimservicestatus)_ |  |  |  |


#### AIMServiceList



AIMServiceList contains a list of AIMService.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMServiceList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AIMService](#aimservice) array_ |  |  |  |


















#### ProfileSourceModel



ProfileSourceModel identifies the producing AIM(Cluster)Model for a
reconciler-produced profile. Stamped from owner references during
reconciliation; left unset for user-authored profiles.



_Appears in:_
- [AIMProfileStatus](#aimprofilestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the producing model's name. |  |  |
| `kind` _[ProfileSourceModelKind](#profilesourcemodelkind)_ | Kind is the producing model's kind ("AIMModel" or "AIMClusterModel"). |  | Enum: [AIMModel AIMClusterModel] <br /> |
| `namespace` _string_ | Namespace is the producing model's namespace. Empty when Kind is<br />AIMClusterModel (cluster-scoped). |  | Optional: \{\} <br /> |


#### ProfileSourceModelKind

_Underlying type:_ _string_

ProfileSourceModelKind identifies whether a profile's source model is
namespace-scoped (AIMModel) or cluster-scoped (AIMClusterModel).

_Validation:_
- Enum: [AIMModel AIMClusterModel]

_Appears in:_
- [ProfileSourceModel](#profilesourcemodel)

| Field | Description |
| --- | --- |
| `AIMModel` |  |
| `AIMClusterModel` |  |






