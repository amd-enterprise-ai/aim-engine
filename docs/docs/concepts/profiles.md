# Profiles

Profiles are self-contained runtime configurations for AI inference workloads. A profile carries everything needed to deploy a model — accelerator requirements, resource requests, engine arguments, and the container image — without referencing any other resource.

!!! info "API Version"
    Profiles are part of the `aim.eai.amd.com/v1alpha2` API. They replace [Service Templates](templates.md) (`v1alpha1`), which are deprecated and will be removed in a future release. New deployments should use profiles.

## Overview

An AIMProfile answers five questions about a deployment without consulting any other resource:

1. **Model architecture** — What model family does this serve? (`aimId`)
2. **Accelerator** — What hardware is required? (`accelerator`: type, model, count)
3. **Engine configuration** — How should the inference engine be configured? (`engineArgs`, `engineEnv`)
4. **Container image** — What image runs the workload? (`image`)
5. **Optimization target** — Latency or throughput? At what precision? (`metric`, `precision`, `type`)

## Cluster vs Namespace Scope

### AIMClusterProfile

Cluster-scoped profiles are installed by administrators, typically created during model discovery from AIM container images. They are visible across all namespaces.

**Key characteristics:**

- Shared across all namespaces
- Provide validated, production-ready runtime configurations
- Can be created manually or, in the future, automatically during model discovery by a v1alpha2 model controller

### AIMProfile

Namespace-scoped profiles are created by ML engineers for custom configurations, fine-tuned models, or team-specific overrides.

**Key characteristics:**

- Visible only within their namespace
- Support namespace-specific secrets and authentication
- Can enable model caching via `spec.caching.enabled`
- Used for custom weight deployments and per-team overrides

When both a namespace-scoped and cluster-scoped profile match, the namespace-scoped profile takes precedence.

## Profile Specification

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterProfile
metadata:
  name: qwen-qwen3-32b-mi300x-lat-fp8
spec:
  aimId: qwen/qwen3-32b
  modelId: qwen/qwen3-32b-fp8
  engine: vllm
  metric: latency
  precision: fp8
  type: optimized
  primary: true

  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1
  resources:                          # optional override: cpu/memory requests
    requests:
      cpu: "4"
      memory: 32Gi

  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  engineArgs:
    distributed_executor_backend: mp
    gpu-memory-utilization: "0.95"
  engineEnv:
    VLLM_DO_NOT_TRACK: "1"

  modelSources:
    - modelId: qwen/qwen3-32b-fp8
      sourceUri: hf://qwen/qwen3-32b-fp8
```

### Spec Fields

| Field | Description |
| ----- | ----------- |
| `aimId` | Model architecture identifier (e.g., `qwen/qwen3-32b`). Used to match profiles to models during automatic selection. **Immutable** after creation. |
| `modelId` | Specific model variant or HuggingFace URI (e.g., `qwen/qwen3-32b-fp8`). Determines the cache path and is used to match profiles to specific model weights. |
| `profileId` | Unique identifier for the source profile within an AIM container image (e.g., `vllm-mi300x-fp8-tp1-latency`). Set during discovery to trace this profile back to its origin in the container. Not required for manually created profiles. |
| `engine` | Inference engine identifier (e.g., `vllm`, `tgi`). |
| `metric` | Optimization target: `latency` (interactive) or `throughput` (batch processing). |
| `precision` | Numeric precision: `fp4`, `fp8`, `fp16`, `fp32`, `bf16`, `int4`, `int8`. |
| `type` | Optimization level. Hierarchy: `optimized` > `general` > `preview` > `unoptimized`. |
| `primary` | Marks this as the recommended profile for its model and configuration. See [Primary Profiles](#primary-profiles). Defaults to `false`. |
| `acceleratorModel` | Accelerator identifier for node selection (e.g., `MI300X`, `CDNA3`, `EPYC_9965`). See [Accelerator and Node Affinity](#accelerator-and-node-affinity). |
| `acceleratorType` | `gpu` or `cpu`. Determines resource derivation strategy. AIM Engine computes default resource requests from this field combined with `acceleratorCount` and cluster-level configuration. |
| `acceleratorCount` | Number of accelerator units required. Combined with `acceleratorType` and cluster config to compute default resource requests in `status.resources`. |
| `resources` | Optional override for Kubernetes [`ResourceRequirements`](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/). When set, merged on top of the defaults that AIM Engine computes. The resolved result is in `status.resources`. |
| `image` | **Required.** Deployment container image. For purpose-built profiles: the full AIM image. For custom weight profiles: the base image. |
| `engineArgs` | Inference engine CLI arguments as a free-form JSON object. Supports typed values (integers, floats, booleans, strings). Converted to `--key value` flags by the AIM runtime. |
| `engineEnv` | Environment variables passed directly to the inference engine process. These are distinct from `containerEnv`, which sets variables on the outer container. |
| `modelSources` | Model artifact sources with download URIs. Populated during discovery or set manually. |
| `containerEnv` | Container-level environment variables for the AIM runtime process (K8s pod spec). |
| `imagePullSecrets` | Secrets for pulling container images. |
| `serviceAccountName` | Service account for workloads. |

### Namespace-Specific Fields

These fields are only available on namespace-scoped `AIMProfile`, not on `AIMClusterProfile`.

| Field | Description |
| ----- | ----------- |
| `caching` | Caching configuration. When `caching.enabled` is `true`, model artifacts are pre-downloaded to a PVC on startup. |

!!! note "Caching for cluster profiles"
    Cluster-scoped profiles do not have a `caching` field. Caching support for `AIMClusterProfile` via a dedicated `AIMProfileCache` resource is planned.

## Accelerator and Node Affinity

Three flat spec fields describe the hardware accelerator:

| Field | Description |
|-------|-------------|
| `acceleratorModel` | Accelerator identifier for node selection (e.g., `MI300X`, `CDNA3`, `EPYC_9965`). Maps to a node label key with an `Exists` selector. |
| `acceleratorType` | `gpu` or `cpu`. Determines how AIM Engine derives Kubernetes resource requests. |
| `acceleratorCount` | Number of accelerator units required (e.g., GPU count or CPU core count). |

### Node selection

The profile specifies a single `acceleratorModel` string. AIM Engine constructs one label key and uses the `Exists` operator for node affinity:

```
acceleratorModel: MI300X  →  feature.node.kubernetes.io/aim-accelerator-model.MI300X  (Exists)
```

The **AcceleratorDetector** (DaemonSet) labels each node with all applicable identifiers. For example, a node with an MI300X GPU gets:

```
feature.node.kubernetes.io/aim-accelerator-model.MI300X: ""    # GPU model
feature.node.kubernetes.io/aim-accelerator-model.CDNA3: ""     # GPU architecture
```

A profile targeting `MI300X` matches that node (exact). A fallback profile targeting `CDNA3` also matches (any CDNA3 GPU). AIM Engine is fully blind — it constructs one label key from `acceleratorModel` and sets `operator: Exists`.

### Resource derivation

AIM Engine computes default `ResourceRequirements` from `acceleratorType`, `acceleratorCount`, and cluster-level configuration (e.g., DCM ConfigMap for GPU partitioning modes, vendor-specific resource names), then merges any `spec.resources` override on top. The result is written to `status.resources` — the definitive resource requirements used for deployment.

```yaml
spec:
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1        # engine computes: amd.com/gpu: "1"
  resources:                  # optional override: cpu/memory
    requests:
      cpu: "4"
      memory: 32Gi
```

The operator checks cluster nodes against both the accelerator model label and `status.resources` capacity. If no node matches, the profile status becomes `NotAvailable`.

### Partitioned GPUs

For partitioned GPU configurations (e.g., CPX-NPS4, MIG), override the derived device resource in `spec.resources`:

```yaml
spec:
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 0             # suppresses default amd.com/gpu derivation
  resources:
    requests:
      amd.com/cpx-nps4: "4"      # partition-specific device resource
```

## Profile Status

### Status Fields

| Field | Type | Description |
| ----- | ---- | ----------- |
| `status` | enum | `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`, `NotAvailable` |
| `version` | string | Extracted from `spec.image` tag (e.g., `0.8.5`) |
| `matchingNodes` | int32 | Count of cluster nodes matching accelerator labels and resource requests |
| `hardwareSummary` | string | Human-readable summary (e.g., `1 x MI300X`, `CPU`) |
| `resources` | ResourceRequirements | Definitive resource requirements used for deployment: defaults computed from accelerator fields + cluster config, merged with any spec.resources override |
| `resolvedNodeAffinity` | NodeAffinity | Computed node affinity rules for pod scheduling |
| `conditions` | []Condition | Standard Kubernetes conditions |

### Status Lifecycle

- **Pending** — Profile created, not yet reconciled
- **Ready** — At least one cluster node matches the accelerator labels and has sufficient resource capacity. For profiles without accelerator requirements (CPU-only), the profile is always `Ready`.
- **Degraded** — The controller encountered a transient error (e.g., failed to list cluster nodes). The profile will be re-evaluated automatically.
- **NotAvailable** — No cluster nodes match the profile's hardware requirements. The profile becomes `Ready` automatically when matching nodes are added to the cluster.

### Conditions

**HardwareAvailable**: Reports whether the cluster has nodes matching the profile's requirements.

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `HardwareAvailable` | Matching nodes found in cluster |
| `True` | `NoAcceleratorSpecified` | No accelerator requirements — always available |
| `False` | `HardwareNotAvailable` | No cluster nodes match accelerator labels and resource requests |

The profile controller watches node events and re-evaluates hardware availability when nodes are added, removed, or their GPU labels change.

## Primary Profiles

AIM container images are built by **profile authors** — the team that benchmarks models on specific hardware and publishes validated runtime configurations. A single image may contain many profiles covering different precisions, optimization targets, and GPU configurations. The `primary` field marks the profile that the authors recommend as the default for a given model and hardware combination.

When `primary: true`:

- The profile is selected by default when [deploying a service](services.md) without an explicit profile or template reference
- The profile is used as the base configuration when onboarding custom model weights that match the same `aimId` and `precision`
- The profile is preferred when multiple candidates match during automatic selection

Non-primary profiles remain available for explicit selection but are not considered during automatic selection by default.

## Examples

### Cluster Profile — Latency Optimized

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterProfile
metadata:
  name: qwen-qwen3-32b-mi300x-lat-fp8
spec:
  aimId: qwen/qwen3-32b
  modelId: qwen/qwen3-32b-fp8
  engine: vllm
  metric: latency
  precision: fp8
  type: optimized
  primary: true
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  engineArgs:
    gpu-memory-utilization: "0.95"
```

### Namespace Profile — Custom Weights

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: my-finetuned-qwen-mi300x-lat-fp8
  namespace: ml-team
spec:
  aimId: qwen/qwen3-32b
  modelId: my-org/qwen-finetuned-fp8
  engine: vllm
  metric: latency
  precision: fp8
  type: general
  primary: false
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 2
  resources:
    requests:
      cpu: "8"
      memory: 64Gi
  image: amdenterpriseai/aim-base:0.8.5
  engineArgs:
    distributed_executor_backend: mp
    gpu-memory-utilization: "0.90"
    tensor-parallel-size: "2"
  modelSources:
    - modelId: my-org/qwen-finetuned-fp8
      sourceUri: s3://my-bucket/fp8-weights/
      precision: fp8
```

### CPU-Only Profile

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: small-model-cpu
  namespace: dev-team
spec:
  aimId: microsoft/phi-2
  engine: vllm
  metric: latency
  precision: fp32
  type: general
  primary: false
  image: amdenterpriseai/aim-phi-2:0.8.5
```

Profiles without accelerator fields (`acceleratorModel`, `acceleratorType`, `acceleratorCount`) and without extended device resource requests are treated as CPU-only and are always `Ready`.

## Troubleshooting

### Profile Stuck in NotAvailable

The profile's accelerator requirements don't match any cluster node:

```bash
# Check which nodes the profile expects
kubectl get aimclusterprofile <name> -o jsonpath='{.status.resolvedNodeAffinity}' | jq
# Check resolved resources
kubectl get aimclusterprofile <name> -o jsonpath='{.status.resources}' | jq

# Check matching node count
kubectl get aimclusterprofile <name> -o jsonpath='{.status.matchingNodes}'

# List nodes with accelerator labels
kubectl get nodes -l feature.node.kubernetes.io/aim-accelerator-model.MI300X
```

Common causes:

- GPU nodes not yet added to the cluster
- AcceleratorDetector not labeling nodes
- Wrong accelerator model name in the profile

### Profile Shows Wrong Hardware Summary

The hardware summary is derived from `acceleratorModel` and `acceleratorCount`. Verify the accelerator fields:

```bash
kubectl get aimclusterprofile <name> -o yaml | grep -E 'accelerator(Model|Type|Count)'
```

## Migration from Service Templates

Profiles (`v1alpha2`) replace Service Templates (`v1alpha1`). Both API versions coexist during the transition period — existing v1alpha1 Service Templates continue to work. New deployments should use profiles.

Key differences for migrating users:

- Profiles carry their own container image (`spec.image`), removing the dependency on model lookups
- Hardware requirements use flat accelerator fields (`acceleratorModel`, `acceleratorType`, `acceleratorCount`). The controller derives Kubernetes device resource requests from the accelerator count. Partitioned GPUs (e.g., `amd.com/cpx-nps4`) are supported via explicit `resources` overrides
- Precision is always explicit — there is no `auto` value
- A `general` optimization tier is available between `optimized` and `preview`

See the [Service Templates](templates.md) documentation for v1alpha1 usage.

## Related Documentation

- [Service Templates](templates.md) — v1alpha1 templates (deprecated)
- [AIM Services](services.md) — Deploying inference endpoints
- [Model Caching](caching.md) — Cache lifecycle and configuration
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full field reference
- [Conditions Reference](../reference/conditions.md) — All conditions and reasons
