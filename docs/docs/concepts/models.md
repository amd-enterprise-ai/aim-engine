# AIM Models

AIM Model resources form a catalog that maps model identifiers to specific container images. This document explains the model resource types, discovery mechanism, and lifecycle.

## Overview

Model resources serve two purposes:

1. **Registry**: Translate abstract model references into concrete container images
2. **Version control**: Update which container serves a model without changing service configurations

## Cluster vs Namespace Scope

### AIMClusterModel

Cluster-scoped models are typically installed by administrators through GitOps workflows or Helm charts. They represent curated model catalogs maintained by platform teams or model publishers.

Cluster models provide a consistent baseline across all namespaces. Any namespace can reference a cluster model unless it defines a namespace-scoped model with the same name, which takes precedence.

**Discovery for cluster models** runs in the operator namespace (default: `aim-system`). Auto-generated templates are created as cluster-scoped resources.

### AIMModel

Namespace-scoped models allow teams to:

- Define team-specific model variants
- Override cluster-level definitions for testing
- Control model access at the namespace level

When both cluster and namespace models exist with the same `metadata.name`, the namespace resource takes precedence within that namespace.

**Discovery for namespace models** runs in the model's namespace. Auto-generated templates are created as namespace-scoped resources.

## Model Specification

An AIM Model uses `metadata.name` as the canonical model identifier:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModel
metadata:
  name: qwen-qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  discovery:
    extractMetadata: true
    createServiceTemplates: true
  resources:
    limits:
      cpu: "8"
      memory: 64Gi
    requests:
      cpu: "4"
      memory: 32Gi
```

### Fields

| Field | Purpose                                                                                                                                                           |
| ----- |-------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `image` | Container image URI implementing this model. The operator inspects this image during discovery.                                                                   |
| `discovery` | Controls metadata extraction and automatic template generation. Discovery is attempted automatically.                                                             |
| `discovery.createServiceTemplates` | When true (default), creates ServiceTemplates from recommended deployments published by the image.                                                                |
| `defaultServiceTemplate` | Default template name to use when services reference this model without specifying a template. Optional.                                                          |
| `imagePullSecrets` | Secrets for pulling the container image during discovery and inference. Must exist in the same namespace as the model (or operator namespace for cluster models). |
| `serviceAccountName` | Service account to use for discovery jobs and metadata extraction. If empty, uses the default service account.                                                    |
| `resources` | Default resource requirements. These serve as baseline values that templates and services can override.                                                           |

## Discovery Mechanism

Discovery is an automatic process that extracts metadata from container images and creates templates.

### Discovery Process

When discovery is enabled:

1. **Registry Inspection**: The controller directly queries the container registry
   using the operator's network context and any configured imagePullSecrets

2. **Image Metadata Fetch**: Using go-containerregistry, the controller pulls
   image metadata (labels) without downloading the full image

3. **Metadata Storage**: Extracted metadata is written to `status.imageMetadata`

4. **Template Generation**: If `createServiceTemplates: true`, the controller examines the image's recommended deployments and creates corresponding ServiceTemplate resources

### Expected Labels

AIM discovery looks for container image labels with the following prefix:
- `com.amd.aim.model.canonicalName`
- `com.amd.aim.model.deployments`
Images without these labels will have minimal metadata. If `createServiceTemplates: true`
but no `recommendedDeployments` are found, no templates are created.

## Lifecycle and Status

### Status Field

The `status` field tracks discovery progress:

| Field | Description |
| ----- | ----------- |
| `status` | Enum: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed` |
| `conditions` | Detailed conditions including `RuntimeConfigReady`, `ImageMetadataReady`, and `ServiceTemplatesReady` |
| `resolvedRuntimeConfig` | Metadata about the runtime config that was resolved (name, namespace, scope, UID) |
| `imageMetadata` | Extracted metadata from the container image including model and OCI metadata |

### Status Values

- **Pending**: Initial state, waiting for reconciliation
- **Progressing**: Discovery job running or templates being created
- **Ready**: Discovery succeeded and all auto-generated templates are healthy
- **Degraded**: Discovery succeeded but some templates have issues
- **Failed**: Discovery failed or required labels missing

### Conditions

**RuntimeConfigReady**: Reports runtime config resolution status. Common reasons:

- `ConfigFound`: Runtime configuration was successfully resolved
- `DefaultConfigNotFound`: No default runtime config found (non-fatal)
- `ConfigNotFound`: Explicitly referenced runtime config not found

**ImageMetadataReady**: Reports image inspection status. Common reasons:

- `ImageMetadataFound`: Metadata extraction succeeded
- `ImageFound`: Image is reachable, but metadata labels are missing
- `MetadataExtractionFailed`: Failed to extract metadata from the image

### Toggling Discovery

You can enable discovery after image creation:

```bash
kubectl edit aimclustermodel qwen-qwen3-32b
# Set spec.discovery.extractMetadata: true
```

The controller runs extraction on the next reconciliation and updates status accordingly.

Disabling discovery after templates exist leaves templates in place. Existing templates are not deleted automatically.

## Resource Resolution

When services reference a model, the controller merges resources from multiple sources:

1. Service-level: `AIMService.spec.resources` (highest precedence)
2. Template-level: `AIMServiceTemplate.spec.resources`
3. Model-level: `AIMModel.spec.resources` (baseline)

If GPU quantities remain unset after merging, the controller copies them from discovery metadata recorded on the template (`status.profile.metadata.gpu_count`).

## Model Lookup

For namespace-scoped lookups (from templates or services in a namespace):

1. Check for `AIMModel` in the same namespace
2. Fall back to `AIMClusterModel` with the same name

This allows namespace models to override cluster baselines.

## Examples

### Cluster Model with Discovery

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModel
metadata:
  name: qwen-qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  runtimeConfigName: platform-default
  discovery:
    extractMetadata: true
    createServiceTemplates: true
  resources:
    limits:
      cpu: "8"
      memory: 64Gi
    requests:
      cpu: "4"
      memory: 32Gi
```

### Namespace Model Without Discovery

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: qwen-qwen3-32b-dev
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  runtimeConfigName: ml-team
  defaultServiceTemplate: custom-template-name
  discovery:
    extractMetadata: false  # skip image metadata extraction
    createServiceTemplates: false
  resources:
    limits:
      cpu: "6"
      memory: 48Gi
```

### Enabling Discovery for Private Container Images

```yaml
# Secret in namespace
apiVersion: v1
kind: Secret
metadata:
  name: private-registry
  namespace: ml-team
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: BASE64_CONFIG
---
# Runtime config in namespace
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  serviceAccountName: aim-runtime
  imagePullSecrets:
    - name: private-registry
---
# Model with discovery
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: proprietary-model
  namespace: ml-team
spec:
  image: private.registry/models/proprietary:v1
  runtimeConfigName: default  # uses config above
  discovery:
    extractMetadata: true
    createServiceTemplates: true
```

## Troubleshooting

### Discovery Fails

Check the operator logs for registry access errors:

```bash
kubectl -n aim-system logs -l app.kubernetes.io/name=aim-engine --tail=100 | grep -i "<model-name>"
```

Common causes:
- Missing or invalid imagePullSecrets (secrets must exist in operator namespace for cluster models)
- Image doesn't exist or tag is invalid
- Network connectivity issues to the registry

### Templates Not Auto-Created

Check the model status:

```bash
kubectl get aimclustermodel <name> -o yaml
# or
kubectl -n <namespace> get aimmodel <name> -o yaml
```

Look for:

- `discovery.extractMetadata: false` - metadata extraction is disabled
- `discovery.createServiceTemplates: false` - auto-template creation is disabled
- Model condition reasons such as `NoTemplatesExpected` or `CreatingTemplates`

### ImageMetadataReady Condition False

The container image is missing required labels or the discovery job failed. Check:

```bash
kubectl get aimclustermodel <name> -o jsonpath='{.status.conditions[?(@.type=="ImageMetadataReady")]}'
```

Inspect the container image labels:

```bash
docker pull <image>
docker inspect <image> --format='{{json .Config.Labels}}'
```

## Auto-Creation from Services

When a service uses `spec.model.image` directly (instead of `spec.model.name`), AIM automatically creates a model resource if one doesn't already exist with that image URI. Auto-created models are namespace-scoped.

### Discovery for Auto-Created Models

The runtime config's `spec.model.autoDiscovery` field controls whether auto-created models run discovery:

```yaml
spec:
  model:
    autoDiscovery: true  # auto-created models run discovery and create templates
```

### Example

Service using direct image reference:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: my-service
  namespace: ml-team
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  runtimeConfigName: default
```

If the runtime config has `autoDiscovery: true`, AIM creates a namespace-scoped model and discovery runs automatically:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: auto-<hash-of-image>
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  discovery:
    extractMetadata: true
    createServiceTemplates: true
```

## Custom Models

Custom models allow you to deploy models from external sources (S3, HuggingFace) without requiring a pre-built AIM container image. The AIM operator uses a generic base container that downloads model weights at runtime.

### Overview

Unlike image-based models where model weights are embedded in the container image, custom models:

- Download weights from external sources (S3 or HuggingFace)
- Use the `amdenterpriseai/aim-base` container for inference
- Skip discovery (no image metadata extraction needed)
- Require explicit hardware specifications

### Creating Custom Models

There are two ways to create custom models:

#### 1. Direct AIMModel with modelSources

Create an AIMModel or AIMClusterModel with `modelSources` instead of relying on image discovery:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: my-custom-qwen
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-base:latest
  modelSources:
    - modelId: Qwen/Qwen3-32B
      sourceUri: s3://my-bucket/models/qwen3-32b
      # size: 16Gi  # Optional - auto-discovered by download job if omitted
  custom:
    hardware:
      gpu:
        requests: 1
        models:
          - MI300X
```

#### 2. Inline Custom Model in AIMService

Create an AIMService with `spec.model.custom` to auto-create a custom model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: my-qwen-service
  namespace: ml-team
spec:
  model:
    custom:
      baseImage: amdenterpriseai/aim-base:latest
      modelSources:
        - modelId: Qwen/Qwen3-32B
          sourceUri: hf://Qwen/Qwen3-32B
          # size is optional - auto-discovered by download job
      hardware:
        gpu:
          requests: 1
  template:
    allowUnoptimized: true  # Required - custom models default to unoptimized
```

The service automatically creates a namespace-scoped AIMModel. Custom models are shared resources that persist independently of the service, allowing them to be reused by other services or manually managed.

### Model Sources

Each model source specifies:

| Field | Required | Description |
|-------|----------|-------------|
| `modelId` | Yes | Canonical identifier in `{org}/{name}` format. Determines the cache mount path. |
| `sourceUri` | Yes | Download location. Schemes: `hf://org/model` (HuggingFace) or `s3://bucket/key` (S3). For S3, use the bucket name directly without the service hostname (e.g., `s3://my-bucket/models/qwen3-32b`). |
| `size` | No | Storage size for PVC provisioning. If omitted, the download job automatically discovers the size. Can be set explicitly to pre-allocate storage. |
| `env` | No | Per-source credential overrides (e.g., `HF_TOKEN`, `AWS_ACCESS_KEY_ID`) |

### Hardware Requirements

Custom models require explicit hardware specifications since discovery doesn't run.
These go under `spec.custom.hardware` for AIMModel, or `spec.model.custom.hardware` for inline AIMService:

```yaml
# For AIMModel:
spec:
  custom:
    hardware:
      gpu:
        requests: 2          # Number of GPUs required
        models:              # Optional: specific GPU models for node affinity
          - MI300X
          - MI250
        minVram: 64Gi        # Optional: minimum VRAM per GPU for capacity planning
      cpu:
        requests: "4"        # Required if cpu field is specified: CPU requests
        limits: "8"          # Optional: CPU limits
```

If no `models` are specified, the workload can run on any available GPU. The `minVram` field is used for capacity planning when the model size is known.

### Template Generation

When `modelSources` is specified:

1. **Without custom.templates**: A single template is auto-generated using `custom.hardware`
2. **With custom.templates**: Templates are created per entry, each inheriting from `custom.hardware` unless overridden

Templates also inherit the `type` field from `spec.custom.type`, which defaults to `unoptimized`. This can be overridden per-template via `customTemplates[].type`.

```yaml
spec:
  modelSources:
    - modelId: Qwen/Qwen3-32B
      sourceUri: s3://bucket/model
  custom:
    type: unoptimized  # Default - can be omitted
    hardware:
      gpu:
        requests: 1
    templates:
      - name: high-memory  # Generated as {modelName}-custom-[{name}][-{precision}][-{gpu}]-{hash}
        hardware:
          gpu:
            requests: 2  # Override
        env:
          - name: VLLM_GPU_MEMORY_UTILIZATION
            value: "0.95"
      - name: standard
        # Inherits hardware and type from custom.*
```

#### Unoptimized Templates and allowUnoptimized

Custom models generate templates with `type: unoptimized` by default because no discovery job runs to validate performance characteristics. This has an important implication:

**Services will not auto-select unoptimized templates unless explicitly allowed.**

When creating an AIMService that uses a custom model, you must either:

1. **Set `allowUnoptimized: true`** on the service's template selector:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: my-service
spec:
  model:
    name: my-custom-model
  template:
    allowUnoptimized: true  # Required for custom model templates
```

2. **Explicitly specify the template name** to bypass auto-selection:

```yaml
spec:
  template:
    name: my-custom-model-custom-abc123  # Explicit template name
```

This safety mechanism prevents accidentally deploying unoptimized configurations in production. See [Template Resolution](services.md#template-resolution) for more details on how templates are selected and the role of optimization levels.

### Authentication

Configure credentials for private sources:

#### HuggingFace

```yaml
spec:
  modelSources:
    - modelId: Qwen/Qwen3-32B
      sourceUri: hf://Qwen/Qwen3-32B
      size: 16Gi
      env:
        - name: HF_TOKEN
          valueFrom:
            secretKeyRef:
              name: hf-credentials
              key: token
```

#### S3-Compatible Storage

```yaml
spec:
  modelSources:
    - modelId: my-org/custom-model
      sourceUri: s3://my-bucket/models/custom
      size: 32Gi
      env:
        - name: AWS_ACCESS_KEY_ID
          valueFrom:
            secretKeyRef:
              name: s3-credentials
              key: access-key
        - name: AWS_SECRET_ACCESS_KEY
          valueFrom:
            secretKeyRef:
              name: s3-credentials
              key: secret-key
        - name: AWS_ENDPOINT_URL
          value: "https://s3.my-provider.com"
```

### Lifecycle Differences

| Aspect | Image-Based Models | Custom Models |
|--------|-------------------|---------------|
| Model weights | source URI embedded in image | source URI in spec |
| Discovery | Runs to extract metadata | Skipped |
| Hardware | Optional (from discovery) | Required |
| Templates | Auto-generated from image labels | Auto-generated from spec |
| Caching | Uses shared template cache | Uses dedicated template cache |

### Status

Custom models report `sourceType: Custom` in their status:

```yaml
status:
  status: Ready
  sourceType: Custom
  conditions:
    - type: Ready
      status: "True"
```

### Example: Full Custom Model Deployment

```yaml
# Secret for HuggingFace access
apiVersion: v1
kind: Secret
metadata:
  name: hf-token
  namespace: ml-team
type: Opaque
stringData:
  token: hf_xxxxxxxxxxxxx
---
# Custom model service
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-custom
  namespace: ml-team
spec:
  model:
    custom:
      modelSources:
        - modelId: Qwen/Qwen3-32B
          sourceUri: hf://Qwen/Qwen3-32B
          # size is optional - auto-discovered by download job
          env:
            - name: HF_TOKEN
              valueFrom:
                secretKeyRef:
                  name: hf-token
                  key: token
      hardware:
        gpu:
          requests: 1
          models:
            - MI300X
  template:
    allowUnoptimized: true  # Required - custom models default to unoptimized
  replicas: 1
```

## Related Documentation

- [Templates](templates.md) - Understanding ServiceTemplates and discovery
- [Runtime Config Concepts](runtime-config.md) - Resolution details including model creation
- [Services Usage](../guides/deploying-services.md) - Deploying services
- [Caching](caching.md) - Model caching and download architecture

## Note on Terminology

AIM Model resources (`AIMModel` and `AIMClusterModel`) define the mapping between model identifiers and container images. While we sometimes refer to the "model catalog" conceptually, the Kubernetes resources are always `AIMModel` and `AIMClusterModel`.
