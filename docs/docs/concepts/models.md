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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMClusterModel
metadata:
  name: meta-llama-3-8b-instruct
spec:
  image: ghcr.io/silogen/aim-meta-llama-llama-3-1-8b-instruct:0.7.0
  discovery:
    enabled: true
    autoCreateTemplates: true
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
| `discovery.autoCreateTemplates` | When true (default), creates ServiceTemplates from recommended deployments published by the image.                                                                |
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

4. **Template Generation**: If `autoCreateTemplates: true`, the controller examines the image's recommended deployments and creates corresponding ServiceTemplate resources

### Expected Labels

AIM discovery looks for container image labels with either the new or legacy prefix:
- `com.amd.aim.model.canonicalName` or `org.amd.silogen.model.canonicalName`
- `com.amd.aim.model.deployments` or `org.amd.silogen.model.deployments`
Images without these labels will have minimal metadata. If `autoCreateTemplates: true`
but no `recommendedDeployments` are found, no templates are created.

## Lifecycle and Status

### Status Field

The `status` field tracks discovery progress:

| Field | Description |
| ----- | ----------- |
| `status` | Enum: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed` |
| `conditions` | Detailed conditions including `RuntimeResolved` and `MetadataExtracted` |
| `resolvedRuntimeConfig` | Metadata about the runtime config that was resolved (name, namespace, scope, UID) |
| `imageMetadata` | Extracted metadata from the container image including model and OCI metadata |

### Status Values

- **Pending**: Initial state, waiting for reconciliation
- **Progressing**: Discovery job running or templates being created
- **Ready**: Discovery succeeded and all auto-generated templates are healthy
- **Degraded**: Discovery succeeded but some templates have issues
- **Failed**: Discovery failed or required labels missing

### Conditions

**RuntimeResolved**: Reports whether runtime config resolution succeeded. Reasons:

- `RuntimeResolved`: Runtime configuration was successfully resolved
- `RuntimeConfigMissing`: The explicitly referenced runtime config was not found
- `DefaultRuntimeConfigMissing`: The implicit default runtime config was not found (warning, allows reconciliation to continue)

**MetadataExtracted**: Reports whether image inspection succeeded. Reasons:

- `MetadataExtracted`: Discovery completed successfully
- `MetadataExtractionFailed`: Discovery job failed or required labels missing from image

### Toggling Discovery

You can enable discovery after image creation:

```bash
kubectl edit aimclustermodel meta-llama-3-8b-instruct
# Set spec.discovery.enabled: true
```

The controller runs extraction on the next reconciliation and updates status accordingly.

Disabling discovery after templates exist leaves templates in place. The `TemplatesAutoGenerated` condition remains `True`.

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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMClusterModel
metadata:
  name: meta-llama-3-8b-instruct
spec:
  image: ghcr.io/example/llama-3.1-8b-instruct:v1.2.0
  runtimeConfigName: platform-default
  discovery:
    enabled: true
    autoCreateTemplates: true
  resources:
    limits:
      cpu: "8"
      memory: 64Gi
      nvidia.com/gpu: "1"
    requests:
      cpu: "4"
      memory: 32Gi
      nvidia.com/gpu: "1"
```

### Namespace Model Without Discovery

```yaml
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMModel
metadata:
  name: meta-llama-3-8b-dev
  namespace: ml-team
spec:
  image: ghcr.io/ml-team/llama-dev:latest
  runtimeConfigName: ml-team
  defaultServiceTemplate: custom-template-name
  discovery:
    enabled: false  # skip discovery and auto-templates
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
apiVersion: aim.silogen.ai/v1alpha1
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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMModel
metadata:
  name: proprietary-model
  namespace: ml-team
spec:
  image: private.registry/models/proprietary:v1
  runtimeConfigName: default  # uses config above
  discovery:
    enabled: true
```

## Troubleshooting

### Discovery Fails

Check the operator logs for registry access errors:
kubectl -n aim-system logs -l app.kubernetes.io/name=aim-operator --tail=100 | grep -i "model-name"Common causes:
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

- `discovery.enabled: false` - discovery is disabled
- `discovery.autoCreateTemplates: false` - auto-creation disabled
- `TemplatesAutoGenerated` condition with reason `NoRecommendedTemplates`

### MetadataExtracted Condition False

The container image is missing required labels or the discovery job failed. Check:

```bash
kubectl get aimclustermodel <name> -o jsonpath='{.status.conditions[?(@.type=="MetadataExtracted")]}'
```

Inspect the container image labels:

```bash
docker pull <image>
docker inspect <image> --format='{{json .Config.Labels}}'
```

## Auto-Creation from Services

When a service uses `spec.model.image` directly (instead of `spec.model.ref`), AIM automatically creates a model resource if one doesn't already exist with that image URI.

### Creation Scope

The runtime config's `spec.model.creationScope` field controls whether the auto-created model is cluster-scoped or namespace-scoped. The default is namespace-scoped:

```yaml
# In runtime config
spec:
  model:
    creationScope: Cluster  # creates AIMClusterModel
    # OR
    creationScope: Namespace  # creates AIMModel in service's namespace
```

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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMService
metadata:
  name: my-service
  namespace: ml-team
spec:
  model:
    image: ghcr.io/example/my-model:v1.0.0
  runtimeConfigName: default
```

If the runtime config has `creationScope: Cluster` and `autoDiscovery: true`, AIM creates:

```yaml
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMClusterModel
metadata:
  name: auto-<hash-of-image>
spec:
  image: ghcr.io/example/my-model:v1.0.0
  discovery:
    enabled: true
    autoCreateTemplates: true
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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMModel
metadata:
  name: my-custom-llama
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-base:latest
  modelSources:
    - modelId: meta-llama/Llama-3-8B
      sourceUri: s3://my-bucket/models/llama-3-8b
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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMService
metadata:
  name: my-llama-service
  namespace: ml-team
spec:
  model:
    custom:
      modelSources:
        - modelId: meta-llama/Llama-3-8B
          sourceUri: hf://meta-llama/Llama-3-8B
          # size is optional - auto-discovered by download job
      hardware:
        gpu:
          requests: 1
```

The service automatically creates a namespace-scoped AIMModel owned by the service. When the service is deleted, the model is garbage collected.

### Model Sources

Each model source specifies:

| Field | Required | Description |
|-------|----------|-------------|
| `modelId` | Yes | Canonical identifier in `{org}/{name}` format. Determines the cache mount path. |
| `sourceUri` | Yes | Download location. Schemes: `hf://org/model` (HuggingFace) or `s3://bucket/key` (S3). For S3, use the bucket name directly without the service hostname (e.g., `s3://my-bucket/models/llama`). |
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

```yaml
spec:
  modelSources:
    - modelId: meta-llama/Llama-3-8B
      sourceUri: s3://bucket/model
      size: 16Gi
  custom:
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
        # Inherits hardware from custom.hardware
```

### Authentication

Configure credentials for private sources:

#### HuggingFace

```yaml
spec:
  modelSources:
    - modelId: meta-llama/Llama-3-8B
      sourceUri: hf://meta-llama/Llama-3-8B
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
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMService
metadata:
  name: llama-custom
  namespace: ml-team
spec:
  model:
    custom:
      modelSources:
        - modelId: meta-llama/Llama-3.1-8B-Instruct
          sourceUri: hf://meta-llama/Llama-3.1-8B-Instruct
          size: 16Gi
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
  replicas: 1
```

## Related Documentation

- [Templates](templates.md) - Understanding ServiceTemplates and discovery
- [Runtime Config Concepts](runtime-config.md) - Resolution details including model creation
- [Services Usage](../usage/services.md) - Deploying services
- [Caching](caching.md) - Model caching and download architecture

## Note on Terminology

AIM Model resources (`AIMModel` and `AIMClusterModel`) define the mapping between model identifiers and container images. While we sometimes refer to the "model catalog" conceptually, the Kubernetes resources are always `AIMModel` and `AIMClusterModel`.
