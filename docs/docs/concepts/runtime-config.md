# Runtime Configuration

Runtime configurations provide storage defaults, routing parameters,
scale-from-zero activation queries, environment variables, additional CA
trust, the aim-base registry, and label-propagation rules that apply to AIM
workloads. They're optional — workloads run without them — but most production
deployments set at least a cluster-scoped `default`.

## Resources

| Resource | Scope | Typical contents |
|---|---|---|
| `AIMClusterRuntimeConfig` | Cluster | Non-secret defaults shared across all namespaces |
| `AIMRuntimeConfig` | Namespace | Namespace overrides, registry credentials, additional trust, routing policy |

Both resources are part of `aim.eai.amd.com/v1alpha1` and continue unchanged in
v1alpha2. Services, models, profile caches, and artifacts that expose
`runtimeConfigName` use the same resolution rules. Profiles do not expose that
field; a profile-owned shared cache resolves the `default` RuntimeConfig.

## Resolution algorithm

When a workload references `runtimeConfigName: my-config`:

1. The controller looks for `AIMRuntimeConfig` named `my-config` in the workload's namespace.
2. If found, it also looks for `AIMClusterRuntimeConfig` with the same name. If both exist, they are **merged** — namespace values override cluster values field-by-field.
3. If no namespace config exists, the controller falls back to the cluster config alone.
4. The resolved configuration is published in `status.resolvedRuntimeConfig`.

When `runtimeConfigName` is omitted, the controller resolves a config named `default`. If `default` doesn't exist, **no error is raised** and reconciliation continues without runtime-config overrides. By contrast, an explicitly-referenced name that doesn't exist is a hard error.

### Inline runtime config on AIMService

`AIMService` accepts an inline `runtimeConfig` block on its spec. Inline values take precedence over any referenced runtime config:

```
inline (spec.runtimeConfig)  >  namespace AIMRuntimeConfig  >  cluster AIMClusterRuntimeConfig  >  operator defaults
```

This lets a service override one or two fields without copying the whole config.

## Status tracking

The resolved runtime config is published in `status.resolvedRuntimeConfig` with a typed reference:

```yaml
status:
  resolvedRuntimeConfig:
    kind: AIMRuntimeConfig
    name: default
    namespace: ml-team
    scope: Namespace
    uid: abc123-def456-...
```

For cluster-scope resolutions:

```yaml
status:
  resolvedRuntimeConfig:
    kind: AIMClusterRuntimeConfig
    name: default
    namespace: ""
    scope: Cluster
    uid: xyz123-uvw123-...
```

Only one reference is present — namespace or cluster, never both. When the two are merged, `scope: Namespace` and the namespace ref is recorded (it's the more specific source).

## Resources that consume runtime config

| Resource | v1alpha2 path | v1alpha1 path |
|---|---|---|
| `AIMService` | Yes (resolved per service) | Yes |
| `AIMModel` / `AIMClusterModel` | Yes (used by discovery Job) | Yes |
| `AIMProfile` | Indirectly: profile-owned caching creates a cache that resolves `default` | n/a |
| `AIMClusterProfile` | No | n/a |
| `AIMProfileCache` | Yes (downloader env, storage defaults) | n/a |
| `AIMServiceTemplate` / `AIMClusterServiceTemplate` | n/a | Yes (legacy) |
| `AIMTemplateCache` | n/a | Yes (legacy) |
| `AIMArtifact` | Yes (downloader env) | Yes |

Each consuming resource independently resolves its RuntimeConfig and publishes
the result. A named RuntimeConfig selected by an `AIMService` does not retarget
a shared cache already owned by an `AIMProfile`; use `default` for policy that
must apply to both.

## aim-base registry for derived images

When a derivation replaces a model's weights (a fine-tuned model, a profile set
with `overrides.modelSources`, or an `AIMService` with
`profileOverrides.modelSources`), the derived profile deploys the clean aim-base
runtime recorded in the source image's `AIM_BASE_IMAGE_REF` instead of the
model-optimized image. AIM Engine keeps the image name and tag from that
reference and takes the registry and org from the source model image, so a
mirror that hosts both the model images and aim-base needs no configuration.

Set `spec.baseImageRegistry` when aim-base lives somewhere other than the model
images. The value is a registry and org, without a tag:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  baseImageRegistry: registry.example.com/amdenterpriseai
```

| `AIM_BASE_IMAGE_REF` | Source model image | `baseImageRegistry` | Deployed image |
|---|---|---|---|
| `build.example.com/aim/aim-base:0.13.1` | `docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.13.0` | unset | `docker.io/amdenterpriseai/aim-base:0.13.1` |
| `build.example.com/aim/aim-base:0.13.1` | `registry.example.com/mirror/aim-qwen-qwen3-32b:0.13.0` | unset | `registry.example.com/mirror/aim-base:0.13.1` |
| `build.example.com/aim/aim-base:0.13.1` | `registry.example.com/models/aim-qwen-qwen3-32b:0.13.0` | `registry.example.com/runtimes` | `registry.example.com/runtimes/aim-base:0.13.1` |
| `build.example.com/aim/aim-base:2026.11.0-rc11` | `docker.io/amdenterpriseai/aim-gemma4:2026.11.0-rc1` | unset | `docker.io/amdenterpriseai/aim-base:2026.11` |

Release tags and digests are kept exactly. A pre-release tag such as
`2026.11.0-rc11` is replaced by its `MAJOR.MINOR` rolling tag, because model
builds record pre-release aim-base tags that are not published.
`spec.baseImageRef` and `status.baseImage` keep the recorded reference either
way, so the substitution stays visible.

The RuntimeConfig that supplies `baseImageRegistry` depends on the derivation
owner:

| Path | RuntimeConfig used |
|---|---|
| `AIMProfileSet`, `AIMClusterProfileSet`, and v1alpha2 `AIMModel.spec.profiles.derivedFrom` | Merged `default` configuration for the applicable scope |
| v1alpha2 `AIMService.spec.profileOverrides` | The RuntimeConfig selected by that service |
| v1alpha1 fine-tuned `AIMModel` / `AIMClusterModel` template copies | The RuntimeConfig selected by that model |

A namespace `baseImageRegistry` replaces the cluster value. A named
configuration does not inherit `default`, so set `baseImageRegistry` on each
named configuration that needs it; without it, the source model image's
registry is used. Explicit derivation image overrides are used as given.

If an image does not contain `AIM_BASE_IMAGE_REF`, derivation reports a
controlled resolution error and retains existing managed profiles rather than
guessing a runtime image. Hand-authored deployable profiles can supply the same
lineage explicitly with `spec.baseImageRef`.

## Additional CA trust

`spec.additionalCABundleRef` selects namespace-local PEM certificates to append
to the public roots used by inference, artifact download, size-check, and
adapter staging workloads. AIM Engine adds the trust init container only when
the referenced ConfigMap and key are present during reconciliation. A missing
bundle is skipped, so the workload continues with public roots. The projection
does not add or change a pod-level `fsGroup`.

CA contents are a workload-creation snapshot. AIM Engine reads administrator
CA ConfigMaps directly from the API server, but does not cache or watch them
and does not replace workloads when their contents change. Recreate affected
inference pods or unfinished transfer/staging Jobs after rotating a CA.

Profile-owned caches resolve `default`, so namespace-wide private endpoint
trust should normally be configured on `AIMRuntimeConfig/default` (or
`AIMClusterRuntimeConfig/default` with the referenced ConfigMap copied into
each workload namespace). See
[Private Registries](../guides/private-registries.md#self-hosted-hugging-face-mirrors)
for configuration, image requirements, snapshot behavior, and discovery
limitations.

## Model profile-generation fallbacks

`spec.model.profileGeneration.fallbacks` defines generic serving runtimes for
v1alpha2 models declared with `spec.modelId`. The Helm chart installs a
chart-managed `AIMClusterRuntimeConfig/default` with the NVIDIA vLLM fallback
shown below, so NVIDIA model-ID onboarding works after a default install.
Set `clusterRuntimeConfig.enable=false` when the cluster already has a
platform-managed config named `default`, or override
`clusterRuntimeConfig.spec.model.profileGeneration.fallbacks` to enforce a
different runtime policy.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  model:
    profileGeneration:
      fallbacks:
        - name: nvidia-vllm
          priority: 10
          type: unoptimized       # default; see "Optimization tier" below
          autoSelectionPolicy: any
          match:
            acceleratorVendor: nvidia
            acceleratorType: gpu
          runtime:
            image: vllm/vllm-openai:v0.17.1-cu130
            engine: vllm
            engineArgs:
              max-model-len: 8192
              gpu-memory-utilization: 0.9
```

Each matching fallback creates a self-contained `AIMProfile` or
`AIMClusterProfile`. Multiple fallbacks may match; the controller generates all
of them and selects one primary by current hardware availability, priority,
hardware-match specificity, then fallback name. Profiles are first created only
while matching hardware exists. Once created, they are retained if that hardware
temporarily disappears and become unavailable instead of being deleted.

Namespace and cluster fallback lists merge by `name`. A namespace fallback
replaces the complete same-named cluster entry, adds namespace-only entries,
and preserves unrelated cluster entries.

The runtime template may set `engineArgs`, `engineEnv`, `containerEnv`,
`resources`, `imagePullSecrets`, and `serviceAccountName`. Model caching remains
an AIMService/artifact concern and is not configured on the fallback.

The chart default deliberately omits CPU and memory requests. The generated
profile still requests the model's accelerator count (one `nvidia.com/gpu` by
default), while platform operators remain free to add host-resource requests
appropriate for their nodes and model sizes.

### Optimization tier and auto-selection policy

A fallback is cluster policy — a generic runtime for a class of hardware, not a
configuration tuned for any particular model. Generated profiles therefore
default to `type: unoptimized`, which is **below** the AIMService selection
floor (`minimumType` defaults to `optimized`).

`autoSelectionPolicy` controls which optimization tiers may participate in
automatic selection when a service leaves `minimumType` implicit:

```yaml
        - name: nvidia-vllm
          type: unoptimized
          autoSelectionPolicy: any
```

The default policy is `optimized`, which applies the implicit
`minimumType: optimized` floor. With `any`, the generated profile is considered
in a second pass only when:

- the service did not explicitly set `spec.profile.selector.minimumType`, and
- no namespace or cluster profile matched the optimized selection pass.

An explicitly authored `minimumType` always wins. For example,
`minimumType: optimized` prevents a lower-tier `any` profile from being
selected,
while `minimumType: unoptimized` directly admits all profiles at that tier or
better without requiring `autoSelectionPolicy: any`.

The chart-provided NVIDIA runtime uses `type: unoptimized` with
`autoSelectionPolicy: any`. This preserves the runtime's honest optimization
tier while making model-ID onboarding work without per-service selector
configuration. AMD preview and unoptimized profiles remain protected by the
default `optimized` policy unless their publisher or platform operator
explicitly opts them into any-tier auto-selection.

### Hardware shape

The fallback declares which hardware *class* it serves; it does not declare how
many devices a model needs. That is per-model and lives on
`AIM(Cluster)Model.spec.accelerator`:

```yaml
# AIMModel
spec:
  modelId: Qwen/Qwen3.5-0.8B
  accelerator:
    count: 4              # default 1
    vendor: nvidia        # optional; narrows which fallbacks apply
    model: H100           # optional; narrows further, and is stamped on the profile
    partitioningMode: unpartitioned
```

`count` sets the generated profile's `acceleratorCount`, which drives both the
device resource request and the engine's tensor-parallel size. A fallback
applies when each axis the model requests is either unconstrained on the
fallback's `match` or equal to it; axes the model leaves empty inherit the
matched fallback's value.

The same block is available on `AIMClusterModelSource.spec.models[]` so declared
models can carry their hardware shape.

## Storage defaults

The most common reason to apply a runtime config: pin the storage class used for cache PVCs.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  defaultStorageClassName: fast-nvme
```

Override per namespace:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  defaultStorageClassName: team-ssd
```

Profiles, caches, and artifacts pick this up unless they set `spec.storageClassName` directly.

## Routing defaults

`spec.routing` carries default routing parameters that `AIMService` inherits when its own `spec.routing` is unset (or partially set).

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: gateways
    pathTemplate: "/{.metadata.namespace}/{.metadata.labels['team']}"
```

### Path templates

The runtime config (and any service) can supply an HTTP path template. The template is rendered against the `AIMService` object using JSONPath expressions.

#### Syntax

```yaml
spec:
  routing:
    pathTemplate: "/{.metadata.namespace}/{.metadata.labels['team']}/{.metadata.name}"
```

#### Rendering

1. **Evaluation** — each placeholder is evaluated with JSONPath against the service object.
2. **Validation** — missing fields, invalid expressions, or multi-value results fail the render.
3. **Normalisation** — each path segment is lowercased, RFC 3986 URL-encoded, and consecutive slashes are collapsed.
4. **Length check** — the final path must be ≤ 200 characters.
5. **Trailing slash** — removed.

A path that exceeds 200 characters, contains invalid JSONPath, or references missing labels/fields degrades the service with reason `PathTemplateInvalid` and skips `HTTPRoute` creation. The `InferenceService` remains intact.

#### Precedence

```
AIMService.spec.routing.pathTemplate  >  Runtime config spec.routing.pathTemplate  >  Default "/<namespace>/<service-uid>"
```

#### Example

Runtime config:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: gateways
    pathTemplate: "/ml/{.metadata.namespace}/{.metadata.labels['project']}"
```

Service that inherits it (v1alpha2):

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  labels:
    project: conversational-ai
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    name: qwen-qwen3-32b
```

Rendered path: `/ml/ml-team/conversational-ai`.

Service overriding the template:

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  routing:
    pathTemplate: "/custom/{.metadata.name}"
```

Rendered path: `/custom/qwen-chat` — the runtime config template is ignored.

## Scale-from-zero activation query

`spec.scaleFromZero.activationMetricQueryTemplate` overrides the metric query
used by the synthetic KEDA trigger that wakes a service from zero replicas. A
cluster-scoped config named `default` supplies the query to every AIMService
that does not select a different `runtimeConfigName`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  scaleFromZero:
    activationMetricQueryTemplate: >-
      sum(gateway_requests{namespace="${namespace}",route="${httpRouteName}"})
```

Resolution follows the normal RuntimeConfig hierarchy:

```
AIMService.spec.scaleFromZero  >  namespace AIMRuntimeConfig  >  cluster AIMClusterRuntimeConfig  >  provider default
```

The available placeholders are `${namespace}`, `${serviceName}`,
`${httpRouteName}`, and `${predictorDeployment}`. Unknown placeholders make the
AIMService configuration invalid. The external collector must expose metrics
with the query's expected names and labels and must emit delta values rather
than cumulative counter totals, because the activation trigger uses a fixed
`operationOverTime=avg`.

## Tolerations

`spec.tolerations` adds tolerations to the pods of AIMService inference workloads and to the Jobs that prepare a model, so they can be scheduled onto tainted nodes, such as GPU nodes reserved for inference:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  tolerations:
    - key: amd.com/gpu
      operator: Exists
      effect: NoSchedule
```

Each level replaces the list of the level below it as a whole; lists are not merged:

```
AIMService.spec.tolerations  >  namespace AIMRuntimeConfig  >  cluster AIMClusterRuntimeConfig
```

An empty list (`tolerations: []`) removes the inherited tolerations: on an AIMService it removes the runtime config's for that service, and on a namespace AIMRuntimeConfig it removes the cluster config's for that namespace. Clusters that enable the `ExtendedResourceToleration` admission plugin already give pods requesting `amd.com/gpu` or `nvidia.com/gpu` a toleration for a taint with that key, whatever this field says.

The size check, download and discovery Jobs use the runtime config's list. One of these Jobs can serve several services, so an AIMService's list does not apply to them. The adapter staging Jobs use the same list as the service's inference pods, because they write to the adapter disk that the inference pods mount. If no node without the taint can run these Jobs, for example when all nodes are GPU nodes or the cache uses node-local storage, the model starts only when the runtime config tolerates the taint.

A Job keeps the tolerations that it was created with. After you change the list, delete a `Pending` Job, and AIM Engine creates it again with the new list.

Changing a runtime config's list restarts the inference pods of every service that inherits it, even when no pod would move to another node: the tolerations are part of each predictor's pod template, and a Deployment replaces its pods whenever that template changes. Services with their own `spec.tolerations` are not affected. If the rollout starts new pods before it stops the old ones, GPU services need free GPUs to complete it.

## Security contexts

`spec.podSecurityContext` sets the Pod security context of AIMService inference pods. `spec.securityContext` sets the security context of their inference container, `kserve-container`. If no level sets a value, AIM Engine does not set a security context, and the pods run as the image and the cluster defaults specify.

Each level replaces the value of the level below it as a whole. The fields of the two values are not merged:

```
AIMService.spec  >  namespace AIMRuntimeConfig  >  cluster AIMClusterRuntimeConfig
```

An empty object (`{}`) also replaces the inherited value. For example, `securityContext: {}` on an AIMService removes the runtime config's container security context for that service. The two fields are independent: a service can set `podSecurityContext` and inherit `securityContext`.

These fields apply only to the inference pods. They do not apply to the Jobs that prepare a model or stage adapters.

AIM Engine refuses the values in these fields that the Pod Security Standard `baseline` forbids. The API server refuses an AIMService, AIMRuntimeConfig or AIMClusterRuntimeConfig that has one of these values:

- `podSecurityContext.sysctls` with an entry. AIM Engine refuses all sysctls, also those that `baseline` allows.
- `securityContext.privileged: true`.
- `securityContext.capabilities.add` with an entry. AIM Engine refuses all added capabilities, also those that `baseline` allows.
- `securityContext.procMount` with a value other than `Default`.
- In both fields:
  - `seccompProfile.type` or `appArmorProfile.type` with a value other than `RuntimeDefault` or `Localhost`.
  - `windowsOptions.hostProcess: true`.
  - `seLinuxOptions.type` with a value other than empty, `container_t`, `container_init_t`, `container_kvm_t` or `container_engine_t`.
  - `seLinuxOptions.user` or `seLinuxOptions.role` with a value.

For what these rules do not prevent, see the [warning in Team RBAC](../admin/security.md#team-rbac-security-contexts).

Changing a runtime config's security context restarts the inference pods of every service that inherits it, because the security contexts are part of each predictor's pod template. Services that set their own value are not affected.

To run inference pods in a namespace that enforces Pod Security `restricted`, see [Restricted Pod Security](../guides/restricted-pod-security.md).

## Environment variable overrides

`spec.env` injects environment variables into managed workloads, for example the inference containers of AIMServices and the model download Jobs. Most commonly used to set the HuggingFace downloader fallback chain. An `AIMService` can also set `spec.env` for its own inference container.

### Inference container env

The runtime config `spec.env` and the AIMService `spec.env` reach the inference container in both AIMService pipelines, but the two pipelines use a different order.

For a service with `spec.profile` (the profile pipeline):

1. The profile env wins. This is the profile `containerEnv`, also through `spec.profileOverrides.containerEnv`, and the framework-owned variables such as `AIM_PROFILE_ID` (see [Inference Engines](inference-engines.md)).
2. The AIMService `spec.env` and the runtime config `spec.env` only add variables that the profile does not set. The AIMService value wins over the namespace runtime config value, and the namespace runtime config value wins over the cluster runtime config value.

To change a variable that the profile sets, use `spec.profileOverrides.containerEnv`. This creates an overlay profile with its own cache. The AIMService and runtime config env go on the service's InferenceService. They do not change the profile, the profile cache or the profile's ServingRuntime, so a new env does not download the model again.

For a service without `spec.profile` (the template pipeline), the AIMService `spec.env` wins over the template env, and the template env wins over the runtime config env.

`AIM_ENGINE_ARGS` values are merged as JSON objects across the cluster runtime config, the namespace runtime config and the AIMService, and the more specific value wins for each key. In the profile pipeline, if the profile sets `AIM_ENGINE_ARGS` in `containerEnv`, the container gets only the profile value. The template pipeline also merges the template value.

Changing a runtime config's `spec.env` restarts the inference pods of every service that gets the changed variables, because the env is part of each predictor's pod template.

### Download protocol strategy

`AIM_DOWNLOADER_PROTOCOL` controls the sequence of protocols tried when downloading HuggingFace models. See [Model Caching — Download Protocol Strategy](caching.md#download-protocol-strategy) for the full mechanism.

Cluster default for environments where XET is unreliable:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "HTTP,XET"
```

Namespace override preferring plain HTTP:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "HTTP"
```

### Merge precedence

```
AIMArtifact.spec.env (per-artifact)
  > AIMProfile.spec.containerEnv / engineEnv  (per-profile, where applicable)
  > AIMRuntimeConfig.spec.env  (namespace)
  > AIMClusterRuntimeConfig.spec.env  (cluster)
  > Operator defaults  (e.g. AIM_DOWNLOADER_PROTOCOL=XET,HF_TRANSFER)
```

An individual artifact or profile can always override an org-wide default when needed.

## Label propagation

Runtime configs can propagate labels from parent AIM resources to their child Kubernetes resources. Useful for cost allocation, ownership tracking, and compliance.

### Configuration

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  labelPropagation:
    enabled: true
    match:
      - "org.example/cost-center"
      - "org.example/team"
      - "compliance.example/*"
```

### Propagation graph

When enabled, labels matching the `match` patterns are automatically copied:

- **AIMService** → `InferenceService`, `HTTPRoute`, PVCs, auto-created `AIMModel` (v1alpha1 image path), `AIMProfileCache`
- **AIMProfileCache** → `AIMArtifact` resources
- **AIMArtifact** → PVCs, download `Job`s
- **AIMModel** / **AIMClusterModel** → auto-created `AIMServiceTemplate` (v1alpha1), `AIMProfile` (v1alpha2 discovery), child `AIMProfileSet` (v1alpha2 derivation)
- **AIMProfileSet** → derived `AIMProfile`s
- **AIMServiceTemplate** → `AIMTemplateCache` (legacy)
- **AIMTemplateCache** → `AIMArtifact` (legacy)
- **AIMClusterModelSource** → auto-discovered `AIMClusterModel`s

### Pattern matching

| Pattern | Matches |
|---|---|
| `"org.example/team"` | Exactly this label key |
| `"org.example/*"` | Any label whose key starts with `org.example/` |
| `"compliance.*/severity"` | Labels like `compliance.sec/severity`, `compliance.audit/severity` |

### Job-specific handling

For `Job` resources, propagated labels are applied to **both**:

1. The `Job`'s metadata labels.
2. The `Job`'s `PodTemplateSpec` labels.

This enables pod-level tracking, so discovery and download pods inherit the same cost-allocation labels as the parent service.

### Example: multi-tenant cost tracking

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  labelPropagation:
    enabled: true
    match:
      - "org.example/cost-center"
      - "org.example/department"
      - "org.example/project"
```

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  labels:
    org.example/cost-center: "eng-ml"
    org.example/department: "engineering"
    org.example/project: "chatbot-v2"
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    name: qwen-qwen3-32b
```

The operator propagates these labels to the `InferenceService`, `HTTPRoute`, `AIMProfileCache`, `AIMArtifact`s, PVCs, and download Jobs, enabling cost tracking and chargeback at the infrastructure level.

## Error and warning behaviour

### Missing explicit config

A workload that explicitly references a non-existent config:

```yaml
spec:
  runtimeConfigName: non-existent
```

Results in:

- Reconciliation fails.
- `ConfigValid=False / ReferenceNotFound`.
- Status goes to `Failed` or `Degraded`.
- Reconciliation retries until the config appears.

### Missing default config

When the implicit `default` config doesn't exist:

- `RuntimeConfigReady=True / DefaultConfigNotFound`.
- A `Normal` event is emitted on the first reconcile with reason `DefaultConfigNotFound`.
- Reconciliation continues without runtime-config overrides.
- Workloads relying on private registries may fail later unless a namespace config supplies credentials.

This lets workloads without special requirements run on a fresh cluster without a `default` config.

## Operator namespace

The AIM controllers determine the operator namespace from the `AIM_SYSTEM_NAMESPACE` environment variable (default: `aim-system`). Cluster-scoped workflows — cluster template discovery, cluster image inspection, auto-generated cluster templates — run auxiliary pods in this namespace and resolve namespaced runtime configs there.

## Related documentation

- [Models](models.md) — How models use runtime configs for discovery
- [Profiles](profiles.md) — Self-contained runtime configurations (v1alpha2)
- [Services](services.md) — How services consume runtime config
- [Service Templates (v1alpha1)](../legacy/service-templates.md) — Legacy template-driven path
- [Deploying Services](../guides/deploying-services.md) — Practical service configuration
- [Model Caching](caching.md) — Download protocol strategy and cache architecture
- [Storage Configuration](../admin/storage-configuration.md) — Storage classes, PVCs, quotas
