# Models

Models are the foundation of AIM's model catalog system. They represent container images that package AI/ML models for inference, and serve as the entry point for deploying models on Kubernetes.

## Overview

An AIMModel (or AIMClusterModel for cluster-wide access) references a container image and manages the lifecycle of extracting metadata and generating deployment configurations. When you create a Model, the operator:

1. Inspects the container image to extract metadata (model name, recommended deployments, licensing info)
2. Auto-generates AIMServiceTemplates based on the image's recommended deployment configurations
3. Makes the model available for use by AIMServices

## Namespace vs Cluster Scope

AIM provides two Model resources to support different access patterns:

| Resource | Scope | Use Case |
|----------|-------|----------|
| `AIMModel` | Namespace | Team-specific models, development/testing, access control via namespace RBAC |
| `AIMClusterModel` | Cluster | Shared models across teams, organization-wide model catalog |

When both exist for the same image, namespace-scoped Models take precedence within their namespace.

## Basic Example

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: llama-3-8b
  namespace: ml-team
spec:
  image: ghcr.io/amd/llama-3-8b:v1.0.0
```

This creates a Model that references the specified container image. The operator will extract metadata and generate ServiceTemplates automatically.

## Private Registries

For images in private registries, provide image pull secrets:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: llama-3-8b
  namespace: ml-team
spec:
  image: private-registry.example.com/models/llama-3-8b:v1.0.0
  imagePullSecrets:
    - name: registry-credentials
```

## Explicit Model Sources

For models that download weights from external sources (e.g., HuggingFace), you can specify environment variables for authentication:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: llama-3-8b
  namespace: ml-team
spec:
  image: ghcr.io/amd/llama-3-8b:v1.0.0
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-credentials
          key: token
```

You can also explicitly specify model sources instead of relying on auto-discovery:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: llama-3-8b
  namespace: ml-team
spec:
  image: ghcr.io/amd/aim-base:v1.0.0
  modelSources:
    - sourceUri: hf://meta-llama/Llama-3-8B
```

## Discovery and Template Generation

By default, Models run a discovery process that:

1. Extracts metadata from container image labels (OCI and AMD-specific labels)
2. Identifies recommended deployment configurations (GPU type, count, precision, optimization metric)
3. Creates AIMServiceTemplates for each recommended deployment

You can control this behavior:

```yaml
spec:
  discovery:
    extractMetadata: true   # Fetch metadata from image (default: uses runtime config)
    createTemplates: true   # Auto-create templates from metadata
```

## Providing Metadata Directly

For air-gapped environments or custom images without baked-in labels, you can provide metadata directly in the spec:

```yaml
spec:
  image: registry.internal/models/llama-3-8b:v1.0.0

  imageMetadata:
    model:
      canonicalName: meta-llama/Llama-3-8B
      recommendedDeployments:
        - gpuModel: MI300X
          gpuCount: 8
          precision: fp8
          metric: latency
    oci:
      title: Llama 3 8B
      vendor: Meta
      licenses: llama3

  discovery:
    createTemplates: true  # Still auto-create templates from the provided metadata
```

When `imageMetadata` is provided, the operator uses it directly instead of fetching from the container registry. This enables deployments in environments without external network access.

## How Models Fit Into the Workflow

```
AIMModel ──generates──> AIMServiceTemplate ──used by──> AIMService
    │                         │                              │
    │                         │                              │
 (metadata)            (runtime profile)              (running inference)
```

1. **Model**: References a container image, extracts metadata
2. **ServiceTemplate**: Defines how to run the model (GPU requirements, precision, optimization goal)
3. **Service**: Deploys the model as a running inference endpoint

## Status

Model status reflects the state of metadata extraction and child templates:

| Status | Meaning |
|--------|---------|
| `Pending` | Initial state, waiting for processing |
| `Progressing` | Metadata extraction or template creation in progress |
| `Ready` | All templates are ready |
| `Degraded` | Some templates are not ready |
| `Failed` | Metadata extraction or all templates failed |
| `NotAvailable` | Required hardware not available in cluster |

## Cluster-Scoped Models

For organization-wide model catalogs, use AIMClusterModel:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModel
metadata:
  name: llama-3-8b  # No namespace - cluster-scoped
spec:
  image: ghcr.io/amd/llama-3-8b:v1.0.0
  imagePullSecrets:
    - name: registry-credentials  # Must exist in operator namespace
```

Cluster models generate AIMClusterServiceTemplates, which can be used by AIMServices in any namespace.

**Important**: For cluster-scoped models, any secrets or ConfigMaps referenced by the model (image pull secrets, environment variable references, etc.) must exist in the operator namespace (`aim-system` by default). This is because discovery jobs for cluster-scoped resources run in the operator namespace.

## Runtime Configuration

Models inherit default settings from AIMRuntimeConfig (namespace-scoped) or AIMClusterRuntimeConfig (cluster-scoped) resources. The `default` config is used when no explicit reference is provided:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  model:
    autoDiscovery: true  # Enable/disable discovery cluster-wide
```

To use a named runtime config:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: llama-3-8b
  namespace: ml-team
spec:
  image: ghcr.io/amd/llama-3-8b:v1.0.0
  runtimeConfigName: high-security  # Uses AIMRuntimeConfig named "high-security"
```

## Image Labels for Discovery

For auto-discovery to generate ServiceTemplates, container images should include the `aim.eai.amd.com/model.recommended-deployments` label with a JSON array of deployment configurations:

```dockerfile
LABEL aim.eai.amd.com/model.recommended-deployments='[{"gpuModel": "MI300X", "gpuCount": 4, "precision": "fp16", "metric": "throughput"}]'
```

Each deployment object supports:

| Field | Description |
|-------|-------------|
| `gpuModel` | GPU model identifier (e.g., `MI300X`, `MI250`) |
| `gpuCount` | Number of GPUs required |
| `precision` | Model precision (`fp32`, `fp16`, `bf16`, `fp8`) |
| `metric` | Optimization target (`throughput`, `latency`) |

Other optional labels (`aim.eai.amd.com/model.name`, `aim.eai.amd.com/model.family`, OCI labels like `org.opencontainers.image.title`) are extracted and stored in status but are not required. These may be useful for clients displaying model information.

## Troubleshooting

### Model stuck in "Progressing"

Check the conditions to understand what's happening:

```bash
kubectl get aimmodel <name> -o jsonpath='{.status.conditions}' | jq
```

Common causes:
- **MetadataExtracted=False**: Image inspection failed. Check imagePullSecrets and registry connectivity.
- **Templates still being created**: Wait for child templates to become ready.

### Templates not being created

1. Verify discovery is enabled:
   ```bash
   kubectl get aimmodel <name> -o jsonpath='{.spec.discovery}'
   ```
   If `createServiceTemplates: false`, templates won't be auto-generated.

2. Check if the image has recommended deployments:
   ```bash
   kubectl get aimmodel <name> -o jsonpath='{.status.imageMetadata.model.recommendedDeployments}'
   ```
   If empty, the image lacks the required label or metadata wasn't extracted.

### Image pull errors for cluster models

For AIMClusterModel, secrets must exist in the operator namespace:

```bash
# Check if secret exists in operator namespace
kubectl get secret registry-credentials -n aim-system

# If missing, copy from another namespace
kubectl get secret registry-credentials -n source-ns -o yaml | \
  sed 's/namespace: source-ns/namespace: aim-system/' | \
  kubectl apply -f -
```

### Model shows "Degraded" or "Failed"

This indicates problems with child ServiceTemplates:

```bash
# List templates owned by this model
kubectl get aimservicetemplates -l aim.eai.amd.com/model=<model-name>

# Check individual template status
kubectl describe aimservicetemplate <template-name>
```
