# Runtime Configuration Architecture

Runtime configurations provide storage defaults and routing parameters. This document explains the resolution algorithm, inheritance model, and status tracking.

## Resolution Model

The AIM operator resolves runtime settings from two Custom Resource Definitions:

- **`AIMClusterRuntimeConfig`**: Cluster-wide defaults that apply across namespaces, useful for single-tenant clusters
- **`AIMRuntimeConfig`**: Namespace-scoped configuration including authentication secrets, useful for multi-tenant clusters

### Resolution Algorithm

When a workload references `runtimeConfigName: my-config`:

1. The controller first looks for `AIMRuntimeConfig` named `my-config` in the workload's namespace
2. If both namespace and cluster configs exist, they are **merged** (namespace values take precedence). Note also that any runtimeconfig embedded in AIMService takes precedence over namespaced runtimeconfig values.
3. If not found, the controller falls back to `AIMClusterRuntimeConfig` named `my-config`
4. The resolved configuration is published in the consumer's `status.resolvedRuntimeConfig`

When `runtimeConfigName` is omitted, the controller resolves a config named `default`. If this is not found, no error is raised. However, if a config that is not named `default` is specified, it must exist, otherwise an error is raised.

## Resolved Runtime Config Tracking

The resolved configuration is published in `status.resolvedRuntimeConfig` with:
- Reference to the source object (namespace or cluster scope)
- UID of the resolved config for identity tracking

### Namespace Config Status

```yaml
status:
  resolvedRuntimeConfig:
    kind: AIMRuntimeConfig
    name: default
    namespace: ml-team
    scope: Namespace
    uid: abc123-def456-...
```

### Cluster Config Status

```yaml
status:
  resolvedRuntimeConfig:
    kind: AIMClusterRuntimeConfig
    name: default
    namespace: ""
    scope: Cluster
    uid: xyz123-uvw123-...
```

Only one ref (namespace or cluster) is present, never both.

## Resources Supporting Runtime Config

The following AIM resources accept `runtimeConfigName`:

- `AIMModel` / `AIMClusterModel`
- `AIMServiceTemplate` / `AIMClusterServiceTemplate`
- `AIMService`
- `AIMTemplateCache`

Each resource independently resolves its runtime config and publishes the result in status.

## Configuration Scoping

### Cluster Runtime Configuration

`AIMClusterRuntimeConfig` captures non-secret defaults shared across namespaces:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  defaultStorageClassName: fast-nvme
```

**Use cases**:
- Platform-wide storage class defaults
- Shared routing configurations for clusters without multi-tenancy

**Limitations**:
- Cannot enforce namespace-specific policies

### Namespace Runtime Configuration

`AIMRuntimeConfig` provides namespace-specific configuration including authentication:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  defaultStorageClassName: team-ssd
  routing:
    enabled: true
    gatewayRef:
      name: kserve-gateway
      namespace: kgateway-system
    pathTemplate: "/{.metadata.namespace}/{.metadata.labels['team']}"
```

**Use cases**:
- Namespace-level routing policies
- Custom storage classes per team

## Routing Templates

Runtime configs can supply a reusable HTTP route template via `spec.routing.pathTemplate`. The template is rendered against the `AIMService` object using JSONPath expressions.

### Template Syntax

```yaml
spec:
  routing:
    pathTemplate: "/{.metadata.namespace}/{.metadata.labels['team']}/{.spec.aimImageName}/"
```

### Rendering Process

During reconciliation:

1. **Evaluation**: Each placeholder (e.g., `{.metadata.namespace}`) is evaluated with JSONPath
2. **Validation**: Missing fields, invalid expressions, or multi-value results fail the render
3. **Normalization**: Each path segment is:
   - Lowercased
   - RFC 3986 encoded
   - De-duplicated (multiple slashes collapsed)
4. **Length Check**: Final path must be ≤ 200 characters
5. **Trailing Slash**: Removed

### Rendering Failures

A rendered path that:

- Exceeds 200 characters
- Contains invalid JSONPath
- References missing labels/fields

...degrades the `AIMService` with reason `PathTemplateInvalid` and skips HTTPRoute creation. The InferenceService remains intact.

### Precedence

Services evaluate path templates in this order:

1. `AIMService.spec.routing.pathTemplate` (highest precedence)
2. Runtime config's `spec.routing.pathTemplate`
3. Default: `/<namespace>/<service-uid>`

This allows:

- **Runtime configs**: Set namespace-wide path conventions
- **Services**: Override with specific paths when needed

### Example

Runtime config with path template:

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

Service using template:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  labels:
    project: conversational-ai
spec:
  model:
    name: qwen-qwen3-32b
  # routing.pathTemplate omitted - uses runtime config template
```

Rendered path: `/ml/ml-team/conversational-ai`

Service with override:

```yaml
spec:
  model:
    ref: qwen-qwen3-32b
  routing:
    pathTemplate: "/custom/{.metadata.name}"
```

Rendered path: `/custom/qwen-chat` (runtime config template ignored)

## Error and Warning Behavior

### Missing Explicit Config

When a workload explicitly references a non-existent config:

```yaml
spec:
  runtimeConfigName: non-existent
```

Result:
- Reconciliation fails
- Workload enters `Failed` or `Degraded` state with reason `ConfigNotFound`
- Reconciliation retries until the config appears

### Missing Default Config

When the implicit `default` config doesn't exist:

- A `RuntimeConfigReady` condition is set to `True` with reason `DefaultConfigNotFound`
- A Normal event is emitted on the first reconcile with reason `DefaultConfigNotFound`
- Reconciliation continues without runtime config overrides
- Workloads relying on private registries may fail later unless a namespace config supplies credentials
This allows workloads without special requirements to proceed even when no default config exists.

## Label Propagation

Runtime configurations support automatic label propagation from parent AIM resources to their child Kubernetes resources. This feature helps maintain consistent metadata across the resource hierarchy for tracking, cost allocation, and compliance purposes.

### Configuration

Label propagation is configured in the runtime config's `labelPropagation` section:

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
      - "compliance.example/*"  # Wildcard matches any label with this prefix
```

### Propagation Behavior

When enabled, labels matching the specified patterns are automatically copied from parent resources to child resources:

- **AIMService** → InferenceService, HTTPRoute, PVCs, auto-created AIMModel
- **AIMTemplateCache** → AIMArtifact resources
- **AIMArtifact** → PVCs, download Jobs
- **AIMModel/AIMClusterModel** → auto-created AIMServiceTemplates
- **AIMServiceTemplate** → AIMTemplateCache
- **AIMClusterModelSource** → auto-created AIMClusterModel resources

### Pattern Matching

The `match` field accepts exact label keys or wildcard patterns:

- `"org.example/team"` - Matches exactly this label key
- `"org.example/*"` - Matches any label with the prefix `org.example/`
- `"compliance.*/severity"` - Matches labels like `compliance.sec/severity`, `compliance.audit/severity`

### Special Handling

For Job resources, propagated labels are applied to both:
1. The Job's metadata labels
2. The Job's PodTemplateSpec labels (enabling pod-level tracking)

### Example Use Case

A typical configuration for multi-tenant cost tracking:

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

When users create an AIMService with these labels:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
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
    ref: qwen-qwen3-32b
```

The operator propagates these labels to the InferenceService, HTTPRoute, and any PVCs created for the service, enabling cost tracking and chargeback at the infrastructure level.

## Environment Variable Overrides

Runtime configurations can inject environment variables into managed workloads via `spec.env`. This is useful for setting defaults across an entire namespace or cluster, such as the download protocol strategy for model artifacts.

### Download Protocol Strategy

The `AIM_DOWNLOADER_PROTOCOL` environment variable controls the sequence of protocols tried when downloading HuggingFace models. See [Model Caching – Download Protocol Strategy](caching.md#download-protocol-strategy) for full details.

#### Example: Cluster default for environments where XET is unreliable

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

#### Example: Namespace override preferring plain HTTP

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

### Merge Precedence

Environment variables are merged with the following precedence (highest first):

1. `AIMArtifact.spec.env` (per-artifact)
2. `AIMRuntimeConfig.spec.env` (namespace-scoped)
3. `AIMClusterRuntimeConfig.spec.env` (cluster-scoped)
4. Operator defaults (e.g., `AIM_DOWNLOADER_PROTOCOL=XET,HF_TRANSFER`)

This means an individual artifact can always override any runtime config setting when needed.

## Operator Namespace

The AIM controllers determine the operator namespace from the `AIM_SYSTEM_NAMESPACE` environment variable (default: `aim-system`).

Cluster-scoped workflows such as:
- Cluster template discovery
- Cluster image inspection
- Auto-generated cluster templates

...run auxiliary pods in this namespace and resolve namespaced runtime configs there.

## Related Documentation

- [Models](models.md) - How models use runtime configs for discovery and auto-creation
- [Templates](templates.md) - Template discovery and runtime config resolution
- [Services Usage](../guides/deploying-services.md) - Practical service configuration
- [Model Caching](caching.md) - Download protocol strategy and cache architecture
