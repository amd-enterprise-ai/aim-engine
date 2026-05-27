# Runtime Configuration

Runtime configurations provide storage defaults, routing parameters, environment variables, and label-propagation rules that apply to AIM workloads. They're optional — workloads run without them — but most production deployments set at least a cluster-scoped `default`.

## Resources

| Resource | Scope | Typical contents |
|---|---|---|
| `AIMClusterRuntimeConfig` | Cluster | Non-secret defaults shared across all namespaces |
| `AIMRuntimeConfig` | Namespace | Namespace overrides, registry credentials, routing policy |

Both resources are part of `aim.eai.amd.com/v1alpha1`. They continue unchanged in v1alpha2 — services, profiles, models, and caches all consume them through the same `runtimeConfigName` field.

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
| `AIMProfile` / `AIMClusterProfile` | Yes (used when caching is enabled) | n/a |
| `AIMProfileCache` | Yes (downloader env, storage defaults) | n/a |
| `AIMServiceTemplate` / `AIMClusterServiceTemplate` | n/a | Yes (legacy) |
| `AIMTemplateCache` | n/a | Yes (legacy) |
| `AIMArtifact` | Yes (downloader env) | Yes |

Each resource independently resolves its runtime config and publishes the result.

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

## Environment variable overrides

`spec.env` injects environment variables into managed workloads. Most commonly used to set the HuggingFace downloader fallback chain.

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
