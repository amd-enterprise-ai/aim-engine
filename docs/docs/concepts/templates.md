# Service Templates

Service Templates define runtime configurations for models and serve as a discovery cache. This document explains the template architecture, discovery mechanism, and lifecycle management.

## Overview

Templates fulfill two roles:

1. **Runtime Configuration**: Define optimization goals (latency vs throughput), numeric precision, and GPU requirements
2. **Discovery Cache**: Store model artifact metadata to avoid repeated discovery operations

The discovery cache function is critical. When a template is created, the operator runs the container with dry-run argument and inspects the result to determine which model artifacts must be downloaded. This information is stored in `status.modelSources[]` and reused by services and caching mechanisms.

## Cluster vs Namespace Scope

### AIMClusterServiceTemplate

Cluster-scoped templates are typically installed by administrators as part of model catalog bundles. They arrive through GitOps workflows, Helm installations, or operator bundles.

**Key characteristics**:

- Cannot enable caching directly (caching is namespace-specific)
- Can be cached into namespaces using `AIMTemplateCache` resources
- Discovery runs in the operator namespace (default: `aim-system`)
- Provide baseline runtime profiles maintained by platform teams

### AIMServiceTemplate

Namespace-scoped templates are created by ML engineers and data scientists for custom runtime profiles.

**Key characteristics**:

- Can enable model caching via `spec.caching.enabled`
- Support namespace-specific secrets and authentication
- Discovery runs in the template's namespace
- Allow teams to customize configurations beyond cluster baselines

## Template Specification

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: qwen3-32b-throughput
  namespace: ml-research
spec:
  modelName: qwen-qwen3-32b
  runtimeConfigName: ml-research
  metric: throughput
  precision: fp8
  hardware:
    gpu:
      requests: 2
      model: MI300X
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: huggingface-creds
          key: token
  imagePullSecrets:
    - name: registry-credentials
```

### Common Fields

| Field | Description |
| ----- | ----------- |
| `modelName` | Model identifier referencing an `AIMModel` or `AIMClusterModel` by `metadata.name`. **Immutable** after creation. |
| `runtimeConfigName` | Runtime configuration for storage defaults and discovery settings. Defaults to `default`. |
| `metric` | Optimization goal: `latency` (interactive) or `throughput` (batch processing). **Immutable** after creation. |
| `precision` | Numeric precision: `auto`, `fp4`, `fp8`, `fp16`, `fp32`, `bf16`, `int4`, `int8`. **Immutable** after creation. |
| `hardware.gpu.requests` | Number of GPUs per replica. **Immutable** after creation. |
| `hardware.gpu.model` | GPU type (e.g., `MI300X`, `MI325X`). **Immutable** after creation. |
| `hardware.cpu` | CPU requirements (optional). For CPU-only models, use `hardware.cpu` without `hardware.gpu`. **Immutable** after creation. |
| `imagePullSecrets` | Secrets for pulling container images during discovery and inference. Must exist in the same namespace (or operator namespace for cluster templates). |
| `serviceAccountName` | Service account for discovery jobs and inference pods. If empty, uses the default service account. |
| `resources` | Container resource requirements. These override model defaults. |
| `modelSources` | Static model sources (optional). When provided, discovery is skipped and these sources are used directly. See [Static Model Sources](#static-model-sources) below. |

### Hardware propagation and node affinity

The `hardware` field specifies GPU and CPU requirements. It is part of the shared runtime parameters (`AIMRuntimeParameters`) and flows as follows:

- **AIMModel**: For custom models, `spec.custom.hardware` defines default requirements; `spec.customTemplates[].hardware` can override per template. The model controller merges these when creating or updating templates.
- **AIMServiceTemplate / AIMClusterServiceTemplate**: `spec.hardware` is the source of truth for the template. The template controller resolves it (with discovery when applicable) and writes **`status.resolvedHardware`**, which is used by the service controller when creating the inference workload.

**Node affinity**: From `spec.hardware.gpu` (or `status.resolvedHardware.gpu`), the operator builds node affinity rules so that inference pods schedule only on nodes that have the required GPU type (and, when specified, sufficient VRAM). GPU availability is detected via node labels (e.g. GPU product ID). If the required GPU is not present in the cluster, the template status becomes `NotAvailable`.

### Namespace-Specific Fields

| Field | Description |
| ----- | ----------- |
| `env` | Environment variables for model downloads (typically authentication tokens). |
| `caching` | Caching configuration for namespace-scoped templates. When enabled, models are cached on startup. |


### Discovery Process

When a template is created or its spec changes:

1. **Job Creation**: The controller creates a Kubernetes Job using the container image referenced by `modelName` (resolved via `AIMModel` or `AIMClusterModel`)

2. **Dry-Run Inspection**: The job runs the container in dry-run mode, examining model requirements without downloading large files

3. **Metadata Extraction**: The job outputs:

    - Model source URIs (often Hugging Face Hub references)
    - Expected sizes in bytes
    - Engine arguments and environment variables

4. **Status Update**: Discovered information is written to `status.modelSources[]` and `status.profile`

Discovery completes in seconds. The cached metadata remains available for all services referencing this template.

### Discovery Location

- **Cluster templates**: Discovery runs in the operator namespace (default: `aim-system`)
- **Namespace templates**: Discovery runs in the template's namespace

This allows namespace templates to access namespace-specific secrets during discovery.

### Model Sources

The `status.modelSources[]` array is the primary discovery output:

```yaml
status:
  modelSources:
    - name: Qwen/Qwen3-32B
      source: hf://Qwen/Qwen3-32B
      sizeBytes: 17179869184
    - name: tokenizer
      source: hf://Qwen/Qwen3-32B/tokenizer.json
      sizeBytes: 2097152
```

Services reference this array when determining runtime requirements.

### Static Model Sources

Templates can optionally provide static model sources in `spec.modelSources` instead of relying on discovery. When static sources are provided:

1. **Discovery is skipped**: No discovery job is created
2. **Sources are used directly**: The provided sources are copied to `status.modelSources[]`
3. **Faster startup**: Templates become `Ready` immediately without waiting for discovery
4. **Manual maintenance**: Sources must be updated manually when the model changes

This is useful when:

- Discovery is not available or not needed
- Model sources are already known and stable
- You want to avoid the discovery job overhead
- Working with custom or non-standard container images

**Example with static sources:**

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: qwen3-32b-static
  namespace: ml-research
spec:
  modelName: qwen-qwen3-32b
  metric: latency
  precision: fp16
  hardware:
    gpu:
      requests: 1
      model: MI300X
  modelSources:
    - name: Qwen/Qwen3-32B
      sourceURI: hf://Qwen/Qwen3-32B
      size: 16Gi
    - name: tokenizer
      sourceURI: hf://Qwen/Qwen3-32B/tokenizer.json
      size: 2Mi
```

When `spec.modelSources` is provided, the template moves directly to `Ready` status without running a discovery job.

### Discovery Job Limits

The AIM operator enforces a global limit of **10 concurrent discovery jobs** across the entire cluster. This prevents resource exhaustion when many templates are created simultaneously.

When this limit is reached:

- New templates wait in `Pending` status with reason `AwaitingDiscovery`
- Discovery jobs are queued and run as existing jobs complete
- Services referencing waiting templates remain in `Starting` status

To avoid delays:

- Use static model sources when discovery is not needed
- Stagger template creation when deploying many models at once
- Consider whether cluster-scoped templates can be shared across namespaces

## Template Status

### Status Fields

| Field | Type | Description |
| ----- | ---- | ----------- |
| `observedGeneration` | int64 | Most recent generation observed |
| `status` | enum | `Pending`, `Progressing`, `NotAvailable`, `Ready`, `Degraded`, `Failed` |
| `conditions` | []Condition | Detailed conditions: `Discovered`, `CacheReady`, `RuntimeConfigReady`, `ModelFound`, `Ready` |
| `resolvedRuntimeConfig` | object | Metadata about the runtime config that was resolved (name, namespace, scope, UID) |
| `resolvedModel` | object | Metadata about the model image that was resolved (name, namespace, scope, UID) |
| `resolvedHardware` | object | Resolved GPU/CPU requirements (from discovery + spec). Used by the service controller for resource requests and node affinity. |
| `hardwareSummary` | string | Human-readable summary of the hardware requirements (e.g. GPU model and count). |
| `modelSources` | []ModelSource | Discovered or static model artifacts with URIs and sizes |
| `profile` | JSON | Complete discovery result with engine arguments and metadata |

### Status Lifecycle

- **Pending**: Template created, discovery not yet started
- **Progressing**: Discovery job running or cache warming in progress
- **NotAvailable**: Template cannot run because required GPU resources are not present in the cluster
- **Ready**: Discovery succeeded (or static sources provided), template ready for use
- **Degraded**: Template is partially functional but has issues
- **Failed**: Discovery encountered terminal errors

Services wait for templates to reach `Ready` before deploying.

### Conditions

**Discovered**: Reports discovery status. Reasons:

- `DiscoveryComplete`: Discovery completed successfully and runtime profiles were extracted
- `InlineModelSources`: Template defines inline model sources, so no discovery job is needed
- `AwaitingDiscovery`: Discovery job has been created and is waiting to run
- `DiscoveryFailed`: Discovery job failed (check job logs for details)

**CacheReady**: Reports caching status (namespace-scoped templates only). Reasons:

- `Ready`: All model sources have been cached successfully
- `WaitingForCache`: Caching has been requested but cache is not yet ready
- `CacheDegraded`: Cache is partially available but has issues
- `CacheFailed`: Cache warming failed

 **Note:** The underlying `AIMTemplateCache` resource uses different reasons (`Warm`, `Warming`, `Failed`) which are translated to the above reasons at the template level.

**Ready**: Reports overall readiness based on all template components.

## Custom Profiles

Custom profiles allow users to tune inference engine behavior (engine args and environment variables) without building custom container images. Profile data is specified inline on service templates and materialized as a ConfigMap at deploy time.

### Overview

The AIM runtime starts as the container entrypoint, selects a profile, then replaces itself with the inference engine (vLLM) via `os.execv`. Custom profiles let you control the engine args and env vars that are applied during this handoff — without modifying the container image or managing raw ConfigMaps.

When `customProfile` is set on a template, the controller:

1. Assembles a complete profile YAML from the template's fields
2. Runs the standard discovery job with the profile mounted (validates compatibility and triggers model weight pre-caching)
3. At deploy time, creates an ephemeral ConfigMap owned by the AIMService and mounts it under the AIM runtime's custom profile path
4. Sets `AIM_PROFILE_ID` to explicitly select the custom profile, bypassing the runtime's normal profile selection logic

### Required Fields

When `customProfile` is set, a CEL validation rule requires all fields needed to assemble a valid profile YAML:

| Field | Description |
| ----- | ----------- |
| `aimId` | AIM product family identifier (e.g., `meta-llama/Llama-3-8B`). Populates the `aim_id` field in the assembled profile YAML. |
| `modelId` | HuggingFace model URI (e.g., `Qwen/Qwen3-32B-FP8`). Identifies which model weights the profile targets. |
| `hardware` | GPU requirements — `gpu.model` and `gpu.requests` map to the profile's `metadata.gpu` and `metadata.gpu_count`. |
| `metric` | Optimization goal (`latency` or `throughput`). |
| `precision` | Numeric precision (e.g., `fp16`, `fp8`). |

### Custom Profile Fields

The `customProfile` object contains two fields:

| Field | Type | Target | Description |
| ----- | ---- | ------ | ----------- |
| `engineArgs` | `map[string]JSON` | Inference engine CLI args | Converted to `--key value` flags on the engine process (e.g., `dtype: float16` becomes `--dtype float16`). Supports typed values — integers, floats, booleans, lists, and strings. |
| `envVars` | `map[string]string` | Inference engine process env | Set via `os.environ` before `os.execv` to the engine (e.g., `PYTORCH_TUNABLEOP_ENABLED: "1"`). Keys must match `^[A-Z0-9_]+$`. |

These are distinct from the existing `env` field on templates, which sets container-level environment variables affecting the AIM runtime process itself.

### Example

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: llama-3-8b-mi300x-custom
  namespace: ml-team
spec:
  aimId: meta-llama/Llama-3-8B
  modelId: meta-llama/Llama-3-8B
  modelName: my-llama-model
  metric: latency
  precision: fp16
  hardware:
    gpu:
      model: MI300X
      requests: 1
  customProfile:
    engineArgs:
      dtype: float16
      gpu-memory-utilization: 0.95
      tensor-parallel-size: 1
    envVars:
      HIP_FORCE_DEV_KERNARG: "1"
      PYTORCH_TUNABLEOP_ENABLED: "1"
```

### Lifecycle

1. **Template creation**: The controller creates a ConfigMap with the assembled profile YAML and runs a discovery job with it mounted. The template enters `Pending` until discovery completes.
2. **Service deployment**: When an AIMService selects a custom profile template, the controller creates a new ConfigMap in the service's namespace (owned by the AIMService via ownerReference). The ConfigMap is mounted into the inference container and `AIM_PROFILE_ID` is set to select it.
3. **Service deletion**: The deploy-time ConfigMap is garbage-collected with the AIMService.

The deploy-time ConfigMap is a point-in-time snapshot. If the template's `customProfile` changes after deployment, the existing ConfigMap is not updated — recreate the AIMService to pick up changes.

### Via AIMModel Custom Templates

Custom profiles can also be specified on `AIMModel.spec.customTemplates[]`. The model controller creates an `AIMServiceTemplate` from each entry, copying `aimId`, `modelId`, and `customProfile` to the created template:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: my-finetuned-llama
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-vllm-base:0.10.0
  modelSources:
    - modelId: my-org/llama-finetuned
      sourceUri: s3://my-bucket/weights/
      size: 16Gi
  customTemplates:
    - name: llama-custom
      aimId: meta-llama/Llama-3-8B
      modelId: meta-llama/Llama-3-8B
      hardware:
        gpu:
          model: MI300X
          requests: 1
      profile:
        metric: latency
        precision: fp16
      customProfile:
        engineArgs:
          dtype: float16
          gpu-memory-utilization: 0.95
        envVars:
          HIP_FORCE_DEV_KERNARG: "1"
```

The same discovery and deploy-time flows apply to templates created this way.

## Auto-Creation from Model Discovery

When AIM Models have `spec.discovery.extractMetadata: true` and `spec.discovery.createServiceTemplates: true`, the controller creates templates from the model's recommended deployments.

These auto-created templates:

- Use naming from the recommended deployment metadata
- Include preset metric, precision, and GPU requirements
- Undergo discovery like manually created templates
- Are managed by the model controller

## Template Selection

When `AIMService.spec.template.name` is omitted, the controller automatically selects a template:

1. **Enumeration**: Find all templates referencing the model (either by `spec.model.name` or matching the auto-created model from `spec.model.image`)
2. **Filtering**: Exclude templates not in `Ready` status
3. **GPU Filtering**: Exclude templates requiring GPUs not present in the cluster
4. **Selection**: If exactly one candidate remains, select it

If zero or multiple candidates remain, the service reports a failure condition explaining the issue.

## Examples

### Cluster Template - Latency Optimized

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterServiceTemplate
metadata:
  name: qwen3-32b-latency
spec:
  modelName: qwen-qwen3-32b
  runtimeConfigName: platform-default
  metric: latency
  precision: fp16
  hardware:
    gpu:
      requests: 1
      model: MI300X
```

### Namespace Template

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: qwen3-32b-throughput
  namespace: ml-research
spec:
  modelName: qwen-qwen3-32b
  runtimeConfigName: ml-research
  metric: throughput
  precision: fp8
  hardware:
    gpu:
      requests: 2
      model: MI300X
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-creds
          key: token
```

## Troubleshooting

### Template Stuck in Progressing

Check discovery job status:

```bash
# Cluster template
kubectl -n aim-system get job -l aim.eai.amd.com/template=<template-name>

# Namespace template
kubectl -n <namespace> get job -l aim.eai.amd.com/template=<template-name>
```

View job logs:

```bash
kubectl -n <namespace> logs job/<job-name>
```

Common issues:

- Image pull failures (missing/invalid imagePullSecrets)
- Container crashes during dry-run
- Runtime config missing

### ModelSources Empty After Discovery

Check the template status conditions:

```bash
kubectl -n <namespace> get aimservicetemplate <name> -o jsonpath='{.status.conditions[?(@.type=="Discovered")]}'
```

The container image may not be a valid AIM container image or may not publish model sources correctly.

## Related Documentation

- [Models](models.md) - Understanding the model catalog and discovery
- [Runtime Config Concepts](runtime-config.md) - Resolution algorithm
- [Model Caching](caching.md) - Cache lifecycle and deletion behavior
- [Services Usage](../guides/deploying-services.md) - Deploying services with templates
