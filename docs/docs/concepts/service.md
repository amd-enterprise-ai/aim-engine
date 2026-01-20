# Services

An AIMService is the primary resource for deploying AI/ML models as inference endpoints on Kubernetes. It brings together a Model and a ServiceTemplate to create a running KServe InferenceService.

## Overview

When you create an AIMService, the operator:

1. Resolves the model (by name reference or image URI), creating an AIMModel if needed
2. Resolves the template (explicit reference or auto-selection), creating derived templates if overrides are specified
3. Configures caching if enabled (creates AIMTemplateCache and AIMModelCache resources)
4. Creates a KServe InferenceService with the appropriate configuration
5. Optionally configures routing via Gateway API

## Basic Examples

### Deploy by Model Name

Reference an existing model and let AIM Engine auto-select the best template:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: llama-service
  namespace: ml-team
spec:
  model:
    name: llama-3-8b
```

### Deploy by Image URI

Specify a container image directly. AIM Engine will find or create a matching AIMModel:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: llama-service
  namespace: ml-team
spec:
  model:
    image: ghcr.io/amd/llama-3-8b:v1.0.0
```

When using `model.image`, AIM Engine searches for existing models with that image URI. If none exist, it creates an AIMModel automatically (without owner references, so it persists for reuse by other services).

### Deploy with Explicit Template

Specify both the model and template explicitly:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: llama-service
  namespace: ml-team
spec:
  model:
    name: llama-3-8b
  template:
    name: llama-3-8b-mi300x-fp16-latency
```

## Model Resolution

AIMService supports three ways to specify the model:

| Mode | Spec Field | Behavior |
|------|------------|----------|
| **Reference** | `model.name` | Looks up existing AIMModel or AIMClusterModel by name |
| **Image** | `model.image` | Finds or creates a model matching the image URI |
| **Custom** | `model.custom` | Advanced: bypass model/template logic entirely (coming soon) |

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

#### Stage 3: Override Matching

If you specify overrides (metric, precision, GPU), only templates matching those constraints are considered:

```yaml
spec:
  overrides:
    metric: latency           # Only templates optimized for latency
    precision: fp16           # Only fp16 precision
    gpuSelector:
      model: MI300X           # Only templates for MI300X
      count: 4                # Only 4-GPU configurations
```

#### Stage 4: GPU Availability

Templates are filtered to only those whose required GPU is available in the cluster. GPU availability is detected via node labels (based on GPU product ID).

If a template requires MI300X GPUs but none are available in the cluster, that template is excluded.

#### Stage 5: Scope Preference

When both namespace-scoped and cluster-scoped templates match, namespace-scoped templates take precedence. This allows teams to customize model deployments without affecting other namespaces.

#### Stage 6: Preference Scoring

If multiple templates remain after filtering, AIM Engine scores them using this preference hierarchy (highest to lowest priority):

1. **Profile Type**: optimized > preview > unoptimized
2. **GPU Tier**: MI325X > MI300X > MI250X > MI210 > A100 > H100
3. **Metric**: latency > throughput
4. **Precision**: Smaller precision types are preferred. Primary ordering is fp, bf, int. Within each type: fp4 > fp8 > fp16 > fp32, bf16, int4 > int8

The template with the best score is selected.

### Ambiguous Selection

If multiple templates have identical scores after all filtering and scoring, AIM Engine reports an ambiguous selection error. Resolve this by:

- Specifying `template.name` explicitly
- Adding overrides to narrow the selection
- Removing duplicate templates

## Derived Templates

When you specify overrides on a service that uses an explicit template, AIM Engine creates a **derived template**. This is a namespace-scoped copy of the base template with your overrides applied.

```yaml
spec:
  template:
    name: base-template
  overrides:
    precision: fp8
    metric: latency
```

This creates a derived template with a descriptive name like `base-template-ovr-mi325x-fp16-a1b2`. The name includes the override values for readability, plus a short hash for uniqueness.

Derived templates:
- Are namespace-scoped (even if the base template is cluster-scoped)
- Have no owner references (intentionally orphaned for reuse)
- Can be shared across services with identical overrides
- Must be manually cleaned up if no longer needed

## Caching

AIMService supports model caching to avoid downloading model weights on every pod startup.

### Caching Modes

| Mode | Behavior |
|------|----------|
| `Always` | Requires cache to be ready before deploying. Service waits for AIMTemplateCache. |
| `Auto` | Uses cache if available, falls back to download mode if not. Default behavior. |
| `Never` | Always downloads models; creates a temporary PVC for the service. |

```yaml
spec:
  caching: Always  # or Auto, Never
```

### How Caching Works

1. **Template Cache**: An `AIMTemplateCache` pre-downloads all model sources for a template to a shared PVC
2. **Model Caches**: Individual `AIMModelCache` resources manage per-model downloads
3. **Service PVC**: When no template cache exists, a temporary PVC is created for the service

With `caching: Auto`, services can start immediately while model caches populate in the background.

## Overrides

Overrides let you customize template behavior without manually creating a new template (a derived template is created automatically):

```yaml
spec:
  overrides:
    metric: latency           # Optimization target
    precision: fp16           # Model precision
    gpuSelector:
      model: MI300X           # GPU model
      count: 4                # Number of GPUs
```

Overrides affect:
- **Template selection**: Filters auto-selection to matching templates
- **Derived templates**: Creates customized template when using explicit template name

## Resource Configuration

Override compute resources for the inference container:

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
| `Progressing` | Creating downstream resources (InferenceService, cache) |
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
kubectl get aimservice <name> -o jsonpath='{.status.componentHealth}' | jq
```

Common causes:
- **Model not found**: Check `model.name` spelling, or ensure `model.image` is accessible
- **Template not found**: Check `template.name` or verify templates exist for the model
- **Template not ready**: The template's model sources may still be resolving

### Service stuck in "Progressing"

Downstream resources are being created:

```bash
# Check InferenceService status
kubectl get inferenceservice <name> -n <namespace>

# Check pod status
kubectl get pods -l serving.kserve.io/inferenceservice=<name>
```

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
- Overrides may be too restrictive

### Template selection is ambiguous

Multiple templates have identical preference scores:

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
```

Resolution:
- Specify `template.name` explicitly
- Add overrides to narrow selection
- Remove duplicate templates

### Cache errors

```bash
# Check template cache status
kubectl get aimtemplatecache -l aim.eai.amd.com/service=<name>

# Check model cache status
kubectl get aimmodelcache -l aim.eai.amd.com/template=<template-name>
```

If cache is failing:
- Check storage class supports ReadWriteMany
- Verify PVC headroom is sufficient for model size
- Check model source URLs are accessible

### Storage size error

If you see `StorageSizeError` in the cache health, the template's model sources don't have size information yet. This typically resolves automatically as the template controller discovers model sizes. If it persists, check the template's model source configuration.
