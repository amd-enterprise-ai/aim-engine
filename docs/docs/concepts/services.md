# Services

An AIMService is the primary resource for deploying AI/ML models as inference endpoints on Kubernetes. It brings together a Model and a ServiceTemplate to create a running KServe InferenceService.

## Overview

When you create an AIMService, the operator:

1. Resolves the model (by name reference or image URI), creating an AIMModel if needed
2. Resolves the template (explicit reference or auto-selection)
3. Configures caching if enabled (creates AIMTemplateCache and AIMArtifact resources)
4. Creates a KServe InferenceService with the appropriate configuration
5. Optionally configures routing via Gateway API

## Basic Examples

### Deploy by Model Name

Reference an existing model and let AIM Engine auto-select the best template:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-service
  namespace: ml-team
spec:
  model:
    name: qwen-qwen3-32b
```

### Deploy by Image URI

Specify a container image directly. AIM Engine will find or create a matching AIMModel:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-service
  namespace: ml-team
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

When using `model.image`, AIM Engine searches for existing models with that image URI. If none exist, it creates an AIMModel automatically (without owner references, so it persists for reuse by other services).

### Deploy with Explicit Template

Specify both the model and template explicitly:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-service
  namespace: ml-team
spec:
  model:
    name: qwen-qwen3-32b
  template:
    name: qwen3-32b-mi300x-fp16-latency
```

## Model Resolution

AIMService supports three ways to specify the model:

| Mode | Spec Field | Behavior |
|------|------------|----------|
| **Reference** | `model.name` | Looks up existing AIMModel or AIMClusterModel by name |
| **Image** | `model.image` | Finds or creates a model matching the image URI |
| **Custom** | `model.custom` | Creates or reuses a namespace-scoped custom AIMModel from inline model sources and hardware requirements |

### Resolution Order

When resolving by name, AIM Engine checks:

1. Namespace-scoped `AIMModel` with that name
2. Cluster-scoped `AIMClusterModel` with that name

Namespace-scoped resources take precedence.

When resolving by image URI, AIM Engine searches both namespace and cluster-scoped models for a matching `spec.image`. If no match exists, AIM Engine creates an AIMModel automatically. If multiple matches exist, resolution fails with an error to prevent ambiguity.

## Template Resolution

Templates define how to run a model: GPU requirements, precision, optimization metric, environment variables, and more.

### Explicit Template

When you specify `template.name`, AIM Engine looks up that template directly:

```yaml
spec:
  template:
    name: my-template
```

Resolution order:
1. Namespace-scoped `AIMServiceTemplate`
2. Cluster-scoped `AIMClusterServiceTemplate`

### Auto-Selection

When no template name is specified, AIM Engine automatically selects the best template for the model. This is the recommended approach for most deployments.

Auto-selection uses a multi-stage filtering and scoring algorithm:

#### Stage 1: Availability Filter

Only templates with `status: Ready` are considered. Templates that are `Pending`, `Progressing`, `Failed`, or `NotAvailable` are excluded.

#### Stage 2: Optimization Filter

By default, only **optimized** templates are considered. Templates with profile type `unoptimized` or `preview` are excluded unless you explicitly allow them:

```yaml
spec:
  template:
    allowUnoptimized: true
```

This prevents accidentally deploying unoptimized configurations in production. Set `allowUnoptimized: true` during development or when optimized templates aren't available for your hardware.

#### Stage 3: GPU Availability

Templates are filtered to only those whose required GPU is available in the cluster. GPU availability is detected via node labels (based on GPU product ID).

If a template requires MI300X GPUs but none are available in the cluster, that template is excluded.

#### Stage 4: Scope Preference

When both namespace-scoped and cluster-scoped templates match, namespace-scoped templates take precedence. This allows teams to customize model deployments without affecting other namespaces.

#### Stage 5: Preference Scoring

If multiple templates remain after filtering, AIM Engine scores them using this preference hierarchy (highest to lowest priority):

1. **Profile Type**: optimized > preview > unoptimized
2. **GPU Tier**: MI325X > MI300X > MI250X > MI210
3. **Metric**: latency > throughput
4. **Precision**: Primary ordering by bit-width (smaller preferred). Secondary ordering by type: fp > bf > int. Full order: fp4 > int4 > fp8 > int8 > fp16 > bf16 > fp32

The template with the best score is selected.

### Ambiguous Selection

If multiple templates have identical scores after all filtering and scoring, AIM Engine reports an ambiguous selection error. Resolve this by:

- Specifying `template.name` explicitly
- Removing duplicate templates

## Caching

AIMService supports model caching to avoid downloading model weights on every pod startup. Caching is configured via `spec.caching.mode`.

### Caching Modes

| Mode | Behavior |
|------|----------|
| `Shared` (default) | Reuses or creates shared cache assets. The template cache and artifacts persist independently of the service and can be reused by other services referencing the same template. |
| `Dedicated` | Creates service-owned cache assets. The template cache and artifacts are owned by the service and garbage-collected when the service is deleted. |

```yaml
spec:
  caching:
    mode: Shared  # default; use Dedicated for service-owned caches
```

When `caching` is omitted, mode defaults to `Shared`.

### How Caching Works

1. **Template Cache**: An `AIMTemplateCache` pre-downloads all model sources for a template to PVCs
2. **Model Caches**: Individual `AIMArtifact` resources manage per-model downloads
3. **Cache ownership**: In `Shared` mode, the template cache has no owner references and persists after the service is deleted, available for reuse. In `Dedicated` mode, the cache is owned by the service and deleted with it.

## Resource Configuration

Configure compute resources for the inference container:

```yaml
spec:
  resources:
    requests:
      memory: "64Gi"
      cpu: "16"
    limits:
      memory: "128Gi"
      amd.com/gpu: "4"
```

## Image Pull Secrets

For private registries:

```yaml
spec:
  imagePullSecrets:
    - name: registry-credentials
```

## Status

Service status reflects the health of all components:

| Status | Meaning |
|--------|---------|
| `Pending` | Waiting for upstream dependencies (model, template) |
| `Starting` | Creating downstream resources (InferenceService, cache) |
| `Running` | InferenceService is ready and serving traffic |
| `Degraded` | Partially functional (e.g., cache failed but service running) |
| `Failed` | Critical failure preventing deployment |

### Component Health

The status includes health for each component:

- **Model**: Resolution and readiness of the AIMModel
- **Template**: Resolution and readiness of the AIMServiceTemplate
- **InferenceService**: KServe InferenceService status
- **Cache**: Template cache or service PVC status

Check conditions for detailed diagnostics:

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions}' | jq
```

## Troubleshooting

### Service stuck in "Pending"

The service is waiting for upstream dependencies:

```bash
# Check which component is blocking
kubectl get aimservice <name> -o jsonpath='{.status.conditions}' | jq
```

Common causes:
- **Model not found**: Check `model.name` spelling, or ensure `model.image` is accessible
- **Template not found**: Check `template.name` or verify templates exist for the model
- **Template not ready**: The template's model sources may still be resolving

### Service stuck in "Starting"

Downstream resources are being created:

```bash
# Check InferenceService status
kubectl get inferenceservice -l aim.eai.amd.com/service.name=<name> -n <namespace>

# Check pod status
kubectl get pods -l serving.kserve.io/inferenceservice=<isvc-name> -n <namespace>
```

Use the InferenceService name returned by the first command as `<isvc-name>`.

Common causes:
- **Image pull errors**: Check imagePullSecrets
- **Resource constraints**: Insufficient GPU, memory, or CPU
- **PVC not binding**: Check storage class availability

### Template selection fails with "no templates found"

```bash
# List templates for the model
kubectl get aimservicetemplates -l aim.eai.amd.com/model=<model-name>

# Check if templates are Ready
kubectl get aimservicetemplates -o custom-columns=NAME:.metadata.name,STATUS:.status.status
```

If templates exist but aren't selected:
- Templates may be `NotAvailable` (GPU not in cluster)
- Templates may be unoptimized (set `allowUnoptimized: true`)

### Template selection is ambiguous

Multiple templates have identical preference scores:

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
```

Resolution:
- Specify `template.name` explicitly
- Remove duplicate templates

### Cache errors

```bash
# Check template cache status
kubectl get aimtemplatecache -l aim.eai.amd.com/service.name=<name>

# Check artifact status
kubectl get aimartifact -l aim.eai.amd.com/template=<template-name>
```

If cache is failing:
- Check storage class supports ReadWriteMany
- Verify PVC headroom is sufficient for model size
- Check model source URLs are accessible

### Storage size error

If you see `StorageSizeError` in the cache health, the template's model sources don't have size information yet. This typically resolves automatically as the template controller discovers model sizes. If it persists, check the template's model source configuration.