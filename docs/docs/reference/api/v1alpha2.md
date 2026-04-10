# API Reference

## Packages
- [aim.eai.amd.com/v1alpha2](#aimeaiamdcomv1alpha2)


## aim.eai.amd.com/v1alpha2

Package v1alpha2 contains API Schema definitions for the aim v1alpha2 API group.

### Resource Types
- [AIMClusterProfile](#aimclusterprofile)
- [AIMClusterProfileList](#aimclusterprofilelist)
- [AIMProfile](#aimprofile)
- [AIMProfileList](#aimprofilelist)



#### AIMClusterProfile



AIMClusterProfile is the Schema for cluster-scoped AIM profiles.
Cluster profiles are visible across all namespaces. They can be created manually
or, in the future, automatically during model discovery by a v1alpha2 model controller.
Unlike namespace-scoped AIMProfiles, cluster profiles do not support caching
configuration since caches are namespace-scoped.



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


#### AIMClusterProfileSpec



AIMClusterProfileSpec defines the desired state of a cluster-scoped AIMClusterProfile.



_Appears in:_
- [AIMClusterProfile](#aimclusterprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding. Immutable. |  | MinLength: 1 <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator-model.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "CDNA3") — the AcceleratorDetector labels nodes with<br />all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required (e.g., GPU count).<br />Combined with AcceleratorType and cluster-level configuration to compute<br />default resource requests in status.resources. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources is an optional override for K8s resource requests/limits.<br />When set, merged on top of the defaults that AIM Engine computes from<br />AcceleratorType, AcceleratorCount, and cluster-level configuration.<br />The resolved result is written to status.resources. |  | Optional: \{\} <br /> |
| `image` _string_ | Image is the deployment container image. Required.<br />For purpose-built profiles: the full AIM image.<br />For custom weight profiles: the base image (e.g., aim-base:0.8.5). |  | MinLength: 1 <br /> |
| `modelSources` _[AIMModelSource](#aimmodelsource) array_ | ModelSources specifies model artifact sources for this profile.<br />Populated during discovery or set by user. |  | Optional: \{\} <br /> |
| `containerEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | ContainerEnv specifies container-level env vars for the AIM runtime process (K8s pod spec). |  | Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#localobjectreference-v1-core) array_ | ImagePullSecrets lists secrets for pulling container images. |  | Optional: \{\} <br /> |
| `serviceAccountName` _string_ | ServiceAccountName specifies the service account for workloads. |  | Optional: \{\} <br /> |


#### AIMMetric

_Underlying type:_ _string_

AIMMetric enumerates supported optimization targets.

_Validation:_
- Enum: [latency throughput]

_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMProfileSpec](#aimprofilespec)
- [AIMProfileSpecCommon](#aimprofilespeccommon)

| Field | Description |
| --- | --- |
| `latency` |  |
| `throughput` |  |


#### AIMModelSource



AIMModelSource describes a downloadable model artifact with optional credentials.



_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMProfileSpec](#aimprofilespec)
- [AIMProfileSpecCommon](#aimprofilespeccommon)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `modelId` _string_ | ModelID is the canonical identifier in \{org\}/\{name\} format.<br />Determines the cache mount path: /workspace/cache/\{modelId\} |  | Pattern: `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$` <br />Required: \{\} <br /> |
| `sourceUri` _string_ | SourceURI is the location from which the model should be downloaded.<br />Supported schemes: hf:// (Hugging Face Hub), s3:// (S3-compatible storage). |  | Pattern: `^(hf\|s3)://[^ \t\r\n]+$` <br /> |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#quantity-resource-api)_ | Size is the expected storage space required for this model artifact.<br />Optional — if not specified, the download job discovers the size automatically. |  | Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision describes the runtime precision this source is compatible with.<br />Used to match model sources to profiles during custom weight onboarding. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#envvar-v1-core) array_ | Env specifies per-source credential overrides.<br />Takes precedence over base-level env for the same variable name. |  | Optional: \{\} <br /> |


#### AIMPrecision

_Underlying type:_ _string_

AIMPrecision enumerates supported numeric precisions.

_Validation:_
- Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8]

_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMModelSource](#aimmodelsource)
- [AIMProfileSpec](#aimprofilespec)
- [AIMProfileSpecCommon](#aimprofilespeccommon)

| Field | Description |
| --- | --- |
| `fp4` |  |
| `fp8` |  |
| `fp16` |  |
| `fp32` |  |
| `bf16` |  |
| `int4` |  |
| `int8` |  |


#### AIMProfile



AIMProfile is the Schema for namespace-scoped AIM profiles.
A profile is a self-contained runtime configuration that answers five questions without
consulting any other resource: model architecture, accelerator, K8s resources, runtime
config, and container image.



_Appears in:_
- [AIMProfileList](#aimprofilelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `aim.eai.amd.com/v1alpha2` | | |
| `kind` _string_ | `AIMProfile` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AIMProfileSpec](#aimprofilespec)_ |  |  |  |
| `status` _[AIMProfileStatus](#aimprofilestatus)_ |  |  |  |


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


#### AIMProfileSpec



AIMProfileSpec defines the desired state of a namespace-scoped AIMProfile.



_Appears in:_
- [AIMProfile](#aimprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding. Immutable. |  | MinLength: 1 <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator-model.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "CDNA3") — the AcceleratorDetector labels nodes with<br />all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required (e.g., GPU count).<br />Combined with AcceleratorType and cluster-level configuration to compute<br />default resource requests in status.resources. |  | Minimum: 0 <br />Optional: \{\} <br /> |
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
| `aimId` _string_ | AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").<br />Primary matching axis for profile selection and custom weight onboarding. Immutable. |  | MinLength: 1 <br /> |
| `modelId` _string_ | ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").<br />Determines the cache path (/workspace/cache/\{modelId\}) and serves as a secondary<br />discriminator for custom weight matching. |  | Optional: \{\} <br /> |
| `profileId` _string_ | ProfileId is the on-disk profile identifier from the AIM image<br />(e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this<br />CRD back to the profile YAML inside the container. Not required for manually<br />created profiles. |  | Optional: \{\} <br /> |
| `engine` _string_ | Engine identifies the inference engine (e.g., "vllm", "tgi"). |  | Optional: \{\} <br /> |
| `metric` _[AIMMetric](#aimmetric)_ | Metric is the optimization target for this profile. |  | Enum: [latency throughput] <br />Optional: \{\} <br /> |
| `precision` _[AIMPrecision](#aimprecision)_ | Precision is the numeric precision used by this profile. |  | Enum: [fp4 fp8 fp16 fp32 bf16 int4 int8] <br />Optional: \{\} <br /> |
| `type` _[AIMProfileType](#aimprofiletype)_ | Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized. |  | Enum: [optimized general preview unoptimized] <br />Optional: \{\} <br /> |
| `primary` _boolean_ | Primary marks this as a default/recommended profile. When true, the profile is<br />advertised for standard deployment and copied automatically for custom weight models.<br />Defaults to false when not specified. | false |  |
| `engineArgs` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#json-v1-apiextensions-k8s-io)_ | EngineArgs contains inference engine CLI arguments as a free-form JSON object.<br />Passed to the inference engine (e.g., vLLM) at startup. |  | Schemaless: \{\} <br />Optional: \{\} <br /> |
| `engineEnv` _object (keys:string, values:string)_ | EngineEnv contains environment variables for the inference engine subprocess.<br />Applied via os.execv, distinct from container-level ContainerEnv. |  | Optional: \{\} <br /> |
| `acceleratorModel` _string_ | AcceleratorModel is the accelerator identifier for node selection.<br />Maps to a node label key using the Exists operator:<br />  feature.node.kubernetes.io/aim-accelerator-model.\{value\}: Exists<br />Supports both specific models (e.g., "MI300X") and architecture-level<br />fallbacks (e.g., "CDNA3") — the AcceleratorDetector labels nodes with<br />all applicable identifiers. |  | MaxLength: 63 <br />Pattern: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$` <br />Optional: \{\} <br /> |
| `acceleratorType` _[AcceleratorType](#acceleratortype)_ | AcceleratorType determines the resource derivation strategy: gpu or cpu.<br />AIM Engine computes default resource requests from this field combined<br />with AcceleratorCount and cluster-level configuration. |  | Enum: [gpu cpu] <br />Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount is the number of accelerator units required (e.g., GPU count).<br />Combined with AcceleratorType and cluster-level configuration to compute<br />default resource requests in status.resources. |  | Minimum: 0 <br />Optional: \{\} <br /> |
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
| `version` _string_ | Version is extracted from the spec.image tag during reconciliation (e.g., "0.8.5"). |  | Optional: \{\} <br /> |
| `matchingNodes` _integer_ | MatchingNodes is the count of cluster nodes matching both the accelerator<br />model label and status.resources requests. Zero means NotAvailable. |  | Optional: \{\} <br /> |
| `hardwareSummary` _string_ | HardwareSummary is a human-readable string describing the hardware requirements.<br />Format: "\{count\} x \{model\}" for GPU (e.g., "1 x MI300X") or "CPU" for CPU-only. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#resourcerequirements-v1-core)_ | Resources contains the definitive K8s resource requests/limits used for deployment.<br />Computed by AIM Engine from AcceleratorType, AcceleratorCount, and cluster-level<br />configuration, then merged with any spec.resources override. |  | Optional: \{\} <br /> |
| `resolvedNodeAffinity` _[NodeAffinity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#nodeaffinity-v1-core)_ | ResolvedNodeAffinity contains the computed node affinity rules derived from<br />spec.acceleratorModel. Used by AIMService when building InferenceService pods. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.22/#condition-v1-meta) array_ | Conditions represent the latest observations of profile state. |  |  |


#### AIMProfileType

_Underlying type:_ _string_

AIMProfileType indicates the optimization level of a profile.
Hierarchy: optimized > general > preview > unoptimized.

_Validation:_
- Enum: [optimized general preview unoptimized]

_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMProfileSpec](#aimprofilespec)
- [AIMProfileSpecCommon](#aimprofilespeccommon)

| Field | Description |
| --- | --- |
| `optimized` |  |
| `general` |  |
| `preview` |  |
| `unoptimized` |  |


#### AcceleratorType

_Underlying type:_ _string_

AcceleratorType distinguishes CPU from GPU accelerators.
Used by AIM Engine to determine the resource derivation strategy
(e.g., gpu → amd.com/gpu, cpu → cpu).

_Validation:_
- Enum: [gpu cpu]

_Appears in:_
- [AIMClusterProfileSpec](#aimclusterprofilespec)
- [AIMProfileSpec](#aimprofilespec)
- [AIMProfileSpecCommon](#aimprofilespeccommon)

| Field | Description |
| --- | --- |
| `cpu` |  |
| `gpu` |  |


