# AIM Profiles

An **AIM profile** is a self-contained runtime configuration for an inference workload. It answers five questions about a deployment without consulting any other resource:

1. **Model architecture** — What model family does this serve? (`aimId`)
2. **Accelerator** — What hardware is required? (`acceleratorType`, `acceleratorModel`, `acceleratorCount`)
3. **Engine** — How is the inference engine configured? (`engineArgs`, `engineEnv`)
4. **Container image** — What image runs the workload? (`image`)
5. **Optimization target** — What was this profile tuned for? (`metric`, `precision`, `type`)

`AIMProfile` and `AIMClusterProfile` carry this configuration. An `AIMService` resolves to exactly one of them and deploys it.

!!! info "v1alpha2"
    Profiles are part of `aim.eai.amd.com/v1alpha2`. They replace v1alpha1 [Service Templates](../legacy/service-templates.md), which are deprecated.

## Where profiles come from

Most profiles aren't hand-authored. They're produced by `AIMModel` reconcilers in one of three flows:

| Source | Profile origin | Typical labels |
|---|---|---|
| Image discovery on an official AIM image | `origin: Discovered`, `role: deployable` | `source-model=<official-model>` |
| Image discovery on a base AIM image | `origin: Discovered`, `role: base` | `source-model=<base-model>` |
| `AIMProfileSet` derivation (fine-tune or custom-model) | `origin: Derived`, `role: deployable` | `source-model=<derivation-model>` |
| Hand-authored by a user | `origin: UserAuthored`, `role: deployable` | (none of the `source-model*` labels) |

See [AIM Models](models.md) for the three model flows that produce these profiles.

## Cluster vs namespace scope

| Resource | Scope | Caching support |
|---|---|---|
| `AIMClusterProfile` | Cluster | No `spec.caching` field (cluster-scoped caches not yet implemented) |
| `AIMProfile` | Namespace | `spec.caching.enabled` triggers an `AIMProfileCache` |

When both a namespace-scoped and cluster-scoped profile match a service's selector, the namespace-scoped profile takes precedence.

## Deployable vs base

A profile is **deployable** when it carries both `spec.aimId` and a non-empty `spec.modelSources`. The model controller stamps this onto `status.deployable: true` and labels the profile `aim.eai.amd.com/profile-role=deployable`.

A profile is a **base profile** when both `aimId` and `modelSources` are empty. Base profiles carry `status.deployable: false` and `profile-role=base`. They exist only as derivation sources for custom-model AIMModels — they cannot back an `AIMService` directly.

Mixed spec (one of `aimId` / `modelSources` set, the other empty) is rejected at admission so `status.deployable` is always derivable from spec.

## Provenance labels

v1alpha2 stamps a small set of canonical labels on every operator-produced profile. These are the labels selectors key off, both inside the AIMService resolver and inside AIMProfileSet derivation.

| Label | Values |
|---|---|
| `aim.eai.amd.com/profile-role` | `base`, `deployable` |
| `aim.eai.amd.com/profile-origin` | `discovered`, `derived`, `user-authored` |
| `aim.eai.amd.com/source-model` | Name of the owning `AIMModel` / `AIMClusterModel` |
| `aim.eai.amd.com/source-model-scope` | `namespace`, `cluster` |
| `aim.eai.amd.com/profile-source` | `copy` (AIMProfileSet derivation marker) |
| `aim.eai.amd.com/profile-copyable` | `"true"` (eligible to be a derivation source) |

The matching `status` fields mirror the labels for kubectl-friendly access:

- `status.origin` mirrors `profile-origin`
- `status.sourceModel.{name,kind,namespace}` mirrors `source-model` + `source-model-scope`
- `status.deployable` derives from spec (also implicit from `profile-role`)

See [Naming and Labels → Profile labels](../reference/naming-and-labels.md#profile-labels) for the canonical list (including label setters and selector recipes).

## Filtering profiles by `aimId` (field selector)

`AIMProfile` and `AIMClusterProfile` expose `spec.aimId` as a **selectable field**, so clients can filter profiles by model architecture server-side with a field selector instead of listing everything and filtering client-side:

```bash
# Cluster profiles for one model architecture
kubectl get aimclusterprofile --field-selector spec.aimId=qwen/qwen3-32b

# Namespace profiles for one model architecture
kubectl get aimprofile -n ml-team --field-selector spec.aimId=qwen/qwen3-32b
```

This is the recommended way for the management UI (and any API consumer) to scope a profile listing to a single model — it pushes the filter to the API server, avoiding a full list + client-side scan. Selectable fields on custom resources require Kubernetes 1.32+ (the `CustomResourceFieldSelectors` feature), which matches the project's minimum supported version (see [Prerequisites](../getting-started/installation.md#prerequisites)).

Only `spec.aimId` is wired as a selectable field today. Other axes (`spec.engine`, `spec.precision`, `spec.acceleratorModel`, `status.deployable`, ...) are natural candidates and can be added the same way (Kubernetes allows up to 8 selectable fields per CRD version).

### Hand-authored profiles cannot fake provenance

Hand-stamping `aim.eai.amd.com/source-model` (or `-scope`) labels on a user-authored AIMProfile does **not** work — the operator strips them on every reconcile. The labels are derived from the profile's controller ownerReferences (or from propagated labels for AIMProfileSet-owned profiles), so a profile with no AIM-controller owner ends up labeled `profile-origin: user-authored` with no `source-model` labels.

This is intentional: it protects against stale labels when a user-authored profile loses its owning model. The user-facing consequence is:

- A hand-authored profile is **not reachable** through `selector.modelRef.name` (which queries by `source-model`).
- To make a hand-authored profile reachable, either match via `aimId`/`modelId` (spec fields, not labels), or onboard it through an `AIMModel`/`AIMProfileSet` that becomes the profile's owner.

## Profile specification

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
  resources:
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

### Spec fields

| Field | Description |
|---|---|
| `aimId` | Model architecture identifier (e.g. `qwen/qwen3-32b`). Primary matching axis for selection. **Immutable** once set. |
| `modelId` | Specific model variant or weights identifier (e.g. `qwen/qwen3-32b-fp8`). Determines cache path. |
| `profileId` | On-disk profile identifier from the AIM image (e.g. `vllm-mi300x-fp8-tp1-latency`). Set during discovery; not required for hand-authored profiles. |
| `engine` | Inference engine (`vllm`, `tgi`, ...). |
| `metric` | Optimization target: `latency` or `throughput`. |
| `precision` | Numeric precision: `fp4`, `fp8`, `fp16`, `fp32`, `bf16`, `int4`, `int8`. |
| `type` | Optimization level. Hierarchy: `optimized > general > preview > unoptimized`. |
| `primary` | Marks the recommended default for this model + hardware combination. Boosts ranking during automatic selection. Default `false`. |
| `manualSelectionOnly` | Excludes this profile from automatic selection. Still addressable by explicit `spec.profile.name`. Used by aim-build for preview / experimental profiles. Default `false`. |
| `acceleratorModel` | Accelerator identifier for node selection (e.g. `MI300X`, `CDNA3`, `EPYC_ZEN5`). Maps to a `feature.node.kubernetes.io/aim-accelerator.<value>` node label with the `Exists` operator. |
| `acceleratorType` | `gpu` or `cpu`. Determines resource derivation strategy. |
| `acceleratorCount` | Number of accelerator units required. Combined with `acceleratorType` and cluster config to compute default resource requests. |
| `resources` | Optional override for K8s `ResourceRequirements`. Merged on top of computed defaults; result lands in `status.resources`. |
| `image` | **Required.** Deployment container image. For purpose-built profiles: the full AIM image. For overlay-produced custom-model profiles: the base image. |
| `engineArgs` | Inference engine CLI arguments as a free-form JSON object. Typed values (ints, floats, booleans, strings) preserved; converted to `--key value` flags by the runtime. |
| `engineEnv` | Environment variables passed to the inference engine subprocess (distinct from `containerEnv`). |
| `modelSources` | Model artifact sources with download URIs. |
| `containerEnv` | Container-level env vars on the pod spec. |
| `imagePullSecrets` | Secrets for pulling the deployment image. |
| `serviceAccountName` | Workload service account. |

Namespace-scoped `AIMProfile` adds one more field:

| Field | Description |
|---|---|
| `caching` | When `caching.enabled: true`, an `AIMProfileCache` pre-downloads `modelSources` to a PVC on profile creation. |

## Accelerator and node affinity

The three accelerator fields jointly describe the hardware AIM Engine schedules onto.

```
spec.acceleratorModel: MI300X
  → feature.node.kubernetes.io/aim-accelerator.MI300X  (Exists)

spec.acceleratorType: gpu
spec.acceleratorCount: 1
  → AIM Engine computes the default device request (e.g. amd.com/gpu: "1")
```

The [AcceleratorDetector](accelerator-detection.md) DaemonSet labels each node with all applicable identifiers (specific GPU model, CPU architecture). A profile with `acceleratorModel: MI300X` matches exactly that GPU model; a fallback profile with `acceleratorModel: EPYC_ZEN5` matches any Zen5 EPYC node.

The label value (count) is informational only — the selector operator is `Exists`. Actual capacity is enforced via the computed device resource request.

### Hardware support

Different AIM images support different AMD hardware families:

| Family | Example `acceleratorModel` |
|---|---|
| AMD Instinct | `MI300X` |
| AMD Radeon | `RadeonW7900` |
| AMD EPYC | `EPYC_9965` |

The hardware a profile targets is declared on the **profile** itself, via `AIMProfile.spec.acceleratorModel`:

```yaml
# AIMProfile (or AIMClusterProfile)
spec:
  acceleratorModel: MI300X
```

You don't normally author these profiles by hand — the `AIMModel` discovers the AIM image and publishes one profile per supported (hardware, precision, metric) combination, each stamped with the `spec.acceleratorModel` it was built for. See [Where profiles come from](#where-profiles-come-from). To target specific hardware you therefore pick among the *already-discovered* profiles rather than inventing a new accelerator value.

An `AIMService` selects a profile with that value through `spec.profile.selector.acceleratorModel` — the selector matches against the profiles' `spec.acceleratorModel` and resolves the service to a profile carrying it:

```yaml
# AIMService
spec:
  model:
    name: qwen-qwen3-32b
  profile:
    selector:
      acceleratorModel: MI300X
```

Make sure the chosen model image actually supports the target hardware — an image built for AMD Instinct GPUs will not run on Radeon or EPYC. Profile resolution only matches an image to a node whose detected labels satisfy the profile's `spec.acceleratorModel`; it does not transcode an unsupported image onto incompatible hardware. See [Deploying Services — Model + selector](../guides/deploying-services.md#model--selector) for the full selector resolution flow.

### Partitioned GPUs

For partitioned GPU configurations (CPX-NPS4, MIG, etc.), override the derived device resource in `spec.resources`:

```yaml
spec:
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 0
  resources:
    requests:
      amd.com/cpx-nps4: "4"
```

`acceleratorCount: 0` suppresses the default `amd.com/gpu` derivation so the override stands alone.

### CPU-only profiles

Profiles without accelerator fields are treated as CPU-only and are always `Ready` (no node-affinity gating).

```yaml
spec:
  aimId: microsoft/phi-2
  image: amdenterpriseai/aim-phi-2:0.8.5
  engine: vllm
  metric: latency
  precision: fp32
```

## Profile status

| Field | Description |
|---|---|
| `status` | `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`, `NotAvailable` |
| `deployable` | `true` when `spec.aimId` and `spec.modelSources` are both populated; `false` for base profiles |
| `origin` | `discovered`, `derived`, `user-authored` (mirrors the `profile-origin` label) |
| `baseImage` | Base image reference extracted from the discovery-cache metadata (`AIM_BASE_IMAGE_REF`). Empty for image-derived profiles whose own `spec.image` is the runtime image. |
| `sourceModel` | `{name, kind, namespace}` of the producing AIM(Cluster)Model. Empty for user-authored profiles. |
| `version` | Extracted from `spec.image` tag (e.g. `0.8.5`) |
| `matchingNodes` | Count of cluster nodes matching the accelerator label and resource requests |
| `hardwareSummary` | Human-readable summary (`1 x MI300X`, `CPU`) |
| `resources` | Definitive `ResourceRequirements` used for deployment — defaults plus any `spec.resources` override |
| `resolvedNodeAffinity` | Computed node affinity rules |
| `conditions` | Standard Kubernetes conditions |

### Status lifecycle

- **Pending** — profile created, not yet reconciled
- **Ready** — at least one cluster node matches the accelerator label and has sufficient resource capacity. CPU-only profiles are always `Ready`.
- **Degraded** — transient error (e.g. failed to list nodes); will be retried.
- **NotAvailable** — no cluster nodes match the requirements. Auto-recovers when matching nodes are added.

### Conditions

`HardwareAvailable` reports whether the cluster has nodes matching the profile's requirements:

| Status | Reason | Description |
|---|---|---|
| `True` | `HardwareAvailable` | Matching nodes present |
| `True` | `NoAcceleratorSpecified` | No accelerator requirements — always available |
| `False` | `HardwareNotAvailable` | No matching nodes |

The profile controller watches node events and re-evaluates hardware availability whenever node labels change.

## Primary profiles

`spec.primary: true` marks a profile as the **recommended default** for its (model + accelerator + precision + metric) combination. AIM image authors stamp this on the profile they consider the production sweet spot for the hardware.

When a service uses `spec.model.name` resolution (which produces multiple candidate profiles), primaries are ranked above non-primaries during selection. Non-primary profiles remain addressable by explicit name.

## `manualSelectionOnly` profiles

`spec.manualSelectionOnly: true` excludes a profile from automatic selection entirely. The profile remains addressable by explicit `spec.profile.name` but is never returned by the model-driven ranker. aim-build uses this for preview / experimental tunings that ship in the image but shouldn't be picked by default.

## Examples

### Cluster profile — latency-tuned

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

### Namespace profile — custom weights with caching

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
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 2
  resources:
    requests:
      cpu: "8"
      memory: 64Gi
  image: amdenterpriseai/aim-base:0.8.5
  engineArgs:
    tensor-parallel-size: "2"
  modelSources:
    - modelId: my-org/qwen-finetuned-fp8
      sourceUri: s3://my-bucket/fp8-weights/
  caching:
    enabled: true
```

## Troubleshooting

### Profile stuck in `NotAvailable`

The profile's accelerator requirements don't match any cluster node.

```bash
kubectl get aimclusterprofile <name> -o jsonpath='{.status.resolvedNodeAffinity}' | jq
kubectl get aimclusterprofile <name> -o jsonpath='{.status.resources}' | jq
kubectl get aimclusterprofile <name> -o jsonpath='{.status.matchingNodes}'

# Nodes carrying the expected accelerator label
kubectl get nodes -l feature.node.kubernetes.io/aim-accelerator.MI300X
```

Common causes: GPU nodes not yet added, AcceleratorDetector not labeling nodes, wrong `acceleratorModel` value.

### `source-model` label disappears after reconcile

Expected for user-authored profiles. See [Hand-authored profiles cannot fake provenance](#hand-authored-profiles-cannot-fake-provenance) above.

### Profile is `Ready` but a service can't resolve to it

If the service uses `spec.profile.selector` or `spec.model.name`, the selector may not match this profile. Check:

- The profile's `status.deployable` is `true` (base profiles are never returned).
- The profile carries `profile-role: deployable`.
- For `modelRef.name` selectors, the profile carries `source-model: <model-name>`. User-authored profiles don't carry this label — they're reachable only by `aimId` / `modelId`.

## Related documentation

- [AIM Models](models.md) — Three model flows that produce profiles
- [AIM Profile Sets](profilesets.md) — Derivation machinery
- [Services](services.md) — How services resolve and rank profiles
- [Model Caching](caching.md) — Cache lifecycle and configuration
- [Naming and Labels](../reference/naming-and-labels.md) — Full label catalogue
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Conditions Reference](../reference/conditions.md) — Condition catalogue
- [Legacy Service Templates](../legacy/service-templates.md) — The deprecated v1alpha1 equivalent
