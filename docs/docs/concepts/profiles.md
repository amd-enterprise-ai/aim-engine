# AIM Profiles

An **AIM profile** is a self-contained runtime configuration for an inference workload. It answers five questions about a deployment without consulting any other resource:

1. **Model architecture** — What model family does this serve? (`aimId`)
2. **Accelerator** — What hardware is required? (`acceleratorVendor`, `acceleratorType`, `acceleratorModel`, `acceleratorCount`)
3. **Engine** — How is the inference engine configured? (`engineArgs`, `engineEnv`)
4. **Container image** — What image runs the workload? (`image`)
5. **Optimization target** — What was this profile tuned for? (`metric`, `precision`, `type`)

`AIMProfile` and `AIMClusterProfile` carry this configuration. An `AIMService` resolves to exactly one of them and deploys it.

:::{admonition} v1alpha2
:class: note

Profiles are part of `aim.eai.amd.com/v1alpha2`. They replace v1alpha1 [Service Templates](../legacy/service-templates.md), which are deprecated.
:::
## Where profiles come from

Most profiles aren't hand-authored. They're produced by model discovery, model-ID profile generation, or profile derivation:

| Source | Profile origin | Typical labels |
|---|---|---|
| Image discovery on an official AIM image | `origin: discovered`, `role: deployable` | `source-model=<official-model>` |
| Image discovery on a base AIM image | `origin: discovered`, `role: base` | `source-model=<base-model>` |
| `AIMModel.spec.modelId` plus a RuntimeConfig fallback | `origin: generated`, `role: deployable` | `source-model=<generated-model>` |
| `AIMProfileSet` derivation | `origin: derived`, `role: deployable` | `source-model=<owning-model>` for a model-owned set |
| Hand-authored by a user | `origin: user-authored`, `role: deployable` | (none of the `source-model*` labels) |

See [AIM Models](models.md) for the four model flows that produce these profiles.

### Definitions and materialised profiles

Image-backed models publish complete profile definitions for derivation. Hardware-supported definitions become `AIMProfile` resources, while unsupported definitions remain in the model's catalog. Separate profile sets or derived models select definitions and apply overrides.

[AIM Profile Sets](profilesets.md#source-selection) select across these definitions and existing copyable profiles. The catalog and resource representations of a model definition count as one source. A hand-authored profile participates when annotated `aim.eai.amd.com/profile-copyable: "true"`; generated and standalone derived resources use the same opt-in rule.

`AIMService` resolves actual profile resources. Logical catalog definitions supply derivation inputs, and resource listings such as `kubectl get aimprofile` show only materialised profiles.

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
| `aim.eai.amd.com/profile-origin` | `discovered`, `generated`, `derived`, `user-authored` |
| `aim.eai.amd.com/source-model` | Name of the owning `AIMModel` / `AIMClusterModel` |
| `aim.eai.amd.com/source-model-scope` | `namespace`, `cluster` |

Two provenance markers are **annotations**, not labels — nothing selects on them, so a label selector will never match:

| Annotation | Values |
|---|---|
| `aim.eai.amd.com/profile-source` | `image` (discovery), `copy` (AIMProfileSet derivation), `generated` (modelId + RuntimeConfig fallback) |
| `aim.eai.amd.com/profile-copyable` | `"true"` makes a profile resource eligible for derivation; an explicit `"false"` on a model profile also excludes its corresponding logical definition |

The matching `status` fields mirror the labels for kubectl-friendly access:

- `status.origin` mirrors `profile-origin`
- `status.sourceModel.{name,kind,namespace}` mirrors `source-model` + `source-model-scope`
- `status.deployable` derives from spec (also implicit from `profile-role`)

See [Naming and Labels → Profile labels](../reference/naming-and-labels.md#profile-labels) for the canonical list (including label setters and selector recipes).

## Projected profile YAML compatibility

`AIMProfile.spec` is the canonical control-plane model used for profile selection, scheduling, derivation, and overrides. The YAML mounted into an AIM runtime is a versioned wire format. AIM Engine decodes source YAML into the canonical CRD and later uses the recorded source codec to encode it back into a format accepted by that runtime. Detection comes from the **source profile document**, never from the image repository or tag.

The current `aim-profile/v1` codec covers the legacy, transitional, and strict accelerator field variants:

| Source YAML field presence | Projected metadata |
|---|---|
| `gpu`, `gpu_count`, `manual_selection_only` | `gpu`, `gpu_count`, `manual_selection_only` |
| `accelerator_model`, `accelerator_type`, `accelerator_count`, `manual_selection_only` | `accelerator_model`, `accelerator_type`, `accelerator_count`, `manual_selection_only` |
| `accelerator_model`, `accelerator_type`, `accelerator_count` | `accelerator_model`, `accelerator_type`, `accelerator_count` |

Discovery records one controller-owned `aim.eai.amd.com/profile-yaml-contract` JSON annotation containing:

- the codec identifier;
- the source presence of fields AIM Engine models; and
- source-owned metadata or top-level extensions AIM Engine does not model.

Profile sets, custom-model derivation, and AIMService overlays inherit the complete contract, so eager and lazy runtime projection reproduce the same field family and retain runtime-only additions such as `metadata.capabilities`. AIM Engine's normalized CRD values remain authoritative: opaque extensions cannot replace identity, hardware, engine, metric, precision, type, `engine_args`, or `env_vars`.

`metadata.features` and `metadata.capabilities` are intentionally distinct. Features advertise runtime/container integration contracts, currently LoRA adapter support (`adapters` or `adapters-scale-only`). AIM Engine models features in `AIMProfile.spec.features` because service composition depends on them. Capabilities describe model/API behavior such as tool calling, structured outputs, and reasoning. AIM runtime validation consumes those flags, while AIM Engine currently preserves the complete capabilities block as an opaque source-owned extension. A profile may declare either field, both, or neither.

If a later AIM Engine release starts modeling a field that an older contract stored as an extension, the existing opaque value remains authoritative until the source profile is inspected again. Re-inspection moves the value into the canonical CRD and records modeled field presence. This avoids replacing preserved future data with a zero-valued typed field during an upgrade.

Source profiles may eventually declare a top-level `profile_schema_version`. Version `1` selects the current codec; profiles that omit it remain compatible with the established v1 document shape. Unknown explicit versions and unknown persisted codec identifiers fail explicitly rather than silently dropping fields. New structural formats can therefore add new codecs while old profiles continue using the codec recorded when they were discovered.

Generated and hand-authored profiles that have no source YAML provenance use the strict canonical `accelerator_*` v1 format. Discovered and derived profiles must carry their inspected contract; projection waits for producer backfill rather than guessing a source schema from annotation absence or an image tag. The annotation is diagnostic controller state, not a normal user-facing configuration knob.

Contracts are bounded to 64 KiB so they cannot consume Kubernetes' aggregate annotation budget. Unknown contract-envelope fields, oversized contracts, unknown codecs, and malformed source metadata fail explicitly. Opaque extension keys remain unrestricted within the bounded `extensions` maps.

`manual_selection_only` is preserved only when the source schema contained that field. This is runtime-schema compatibility, not resolver behavior: `spec.manualSelectionOnly` remains deprecated and ignored for profile selection.

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

  acceleratorVendor: amd
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
| `profileId` | On-disk profile identifier from the AIM image (e.g. `vllm_omni-mi300x-fp16-tp4-latency-usp4`). Set during discovery and retained as the projected profile filename; not required for hand-authored profiles. |
| `engine` | Inference engine (`vllm`, `vllm_omni`, ...). Projected onto the inference container as the framework-owned `AIM_ENGINE` variable. |
| `variant` | Optional runtime recipe suffix for profiles that otherwise share the same engine/hardware/precision/count/metric tuple (for example `usp4`). Discovery preserves `metadata.variant` from the image. It is identity metadata, not currently a selector or ranking axis. |
| `metric` | Optimization target: `latency` or `throughput`. |
| `precision` | Numeric precision: `fp4`, `fp8`, `fp16`, `fp32`, `fp64`, `bf16`, `int4`, `int8`. |
| `type` | Optimization level. Hierarchy: `optimized > general > preview > unoptimized`. Auto-selection compares this against the selector's `minimumType` floor (default `optimized` for AIMServices), so lower tiers are opt-in. An empty/unset `type` is treated as `unoptimized` (conservative: an undeclared tier is excluded by the default floor unless the selector opts down). |
| `autoSelectionPolicy` | Automatic-selection policy: `optimized` (default) applies the implicit optimized floor; `any` admits this profile regardless of optimization tier in a second pass only when the service did not explicitly set `minimumType` and no optimized namespace or cluster profile matched. |
| `primary` | Marks the recommended default for this model + hardware combination. Boosts ranking during automatic selection. Default `false`. |
| `manualSelectionOnly` | **Deprecated and ignored by the resolver.** Use `type` + the selector's `minimumType` floor for service-authored consent and `autoSelectionPolicy` for platform-authored automatic eligibility. Retained for backward compatibility but has no effect. Default `false`. |
| `acceleratorVendor` | Accelerator device-plugin contract: `amd` or `nvidia`. Selects the vendor node label and default GPU resource name. Empty preserves the legacy AMD behavior. |
| `acceleratorModel` | Accelerator identifier for node selection (e.g. `MI300X`, `CDNA3`, `EPYC_ZEN5`). Maps to a `feature.node.kubernetes.io/aim-accelerator.<value>` node label with the `Exists` operator. |
| `acceleratorType` | `gpu` or `cpu`. Determines resource derivation strategy. |
| `acceleratorCount` | Number of accelerator units required. Combined with `acceleratorVendor` and `acceleratorType` to compute default resource requests. |
| `resources` | Optional override for K8s `ResourceRequirements`. Merged on top of computed defaults; result lands in `status.resources`. |
| `image` | **Required.** Deployment container image. For purpose-built profiles: the full AIM image. For overlay-produced custom-model profiles: the base image. |
| `engineArgs` | Optional inference-engine tuning and overrides as a free-form JSON object. Typed values (ints, floats, booleans, strings) are preserved and projected into engine-specific CLI flags. |
| `engineEnv` | Environment variables passed to the inference engine subprocess (distinct from `containerEnv`). |
| `modelSources` | Model artifact sources with download URIs. |
| `containerEnv` | Container-level env vars on the pod spec. |
| `imagePullSecrets` | Secrets for pulling the deployment image. |
| `serviceAccountName` | Workload service account. |

For engine-specific behavior—including shared-memory sizing and vLLM-Omni USP variants—see [Inference Engines](inference-engines.md). To require a specific variant, select the profile by `spec.profile.name`; automatic selection does not filter or rank on `variant`.

Namespace-scoped `AIMProfile` adds one more field:

| Field | Description |
|---|---|
| `caching` | When `caching.enabled: true`, an `AIMProfileCache` pre-downloads `modelSources` to a PVC on profile creation. |

## Accelerator and node affinity

The accelerator fields jointly describe the hardware AIM Engine schedules onto. `acceleratorVendor` is sufficient to target any GPU from one vendor; `acceleratorModel` is an optional additional constraint for profiles that require a specific card.

```
spec.acceleratorVendor: amd
  → feature.node.kubernetes.io/aim-accelerator.vendor.GPU.AMD  (Exists)

spec.acceleratorModel: MI300X
  → feature.node.kubernetes.io/aim-accelerator.MI300X  (Exists)

spec.acceleratorType: gpu
spec.acceleratorCount: 1
  → AIM Engine computes amd.com/gpu: "1"

spec.acceleratorVendor: nvidia
spec.acceleratorType: gpu
spec.acceleratorCount: 1
  → AIM Engine computes nvidia.com/gpu: "1"
```

The [AcceleratorDetector](accelerator-detection.md) DaemonSet labels each node with its vendor and applicable identifiers (specific GPU model, CPU architecture). A generic profile with `acceleratorVendor: nvidia` matches NVIDIA GPU nodes of any model. Adding `acceleratorModel: H100` narrows that profile to H100 nodes.

The label value (count) is informational only — the selector operator is `Exists`. Actual capacity is enforced via the computed device resource request.

### Hardware support

Optimized AIM images declare the concrete AMD hardware family they support:

| Family | Example `acceleratorModel` |
|---|---|
| AMD Instinct | `MI300X` |
| AMD Radeon | `RadeonW7900` |
| AMD EPYC | `EPYC_9965` |

The hardware a profile targets is declared on the **profile** itself, via `AIMProfile.spec.acceleratorVendor` and, when required, `AIMProfile.spec.acceleratorModel`:

```yaml
# AIMProfile (or AIMClusterProfile)
spec:
  acceleratorVendor: amd
  acceleratorModel: MI300X
```

The `AIMModel` discovers the AIM image and retains every profile definition. It materialises hardware-supported entries with their declared `spec.acceleratorModel`. To map definitions to another accelerator or partitioning mode, use [profile sets](profilesets.md#examples) or a separate derived model, then validate the workload on the target hardware.

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

### Generic NVIDIA vLLM profiles

A hand-authored profile can run an upstream vLLM image on any NVIDIA GPU model without pre-creating a profile per card:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: qwen2-5-0-5b-nvidia-vllm
spec:
  aimId: Qwen/Qwen2.5-0.5B-Instruct
  modelId: Qwen/Qwen2.5-0.5B-Instruct
  engine: vllm
  type: unoptimized
  acceleratorVendor: nvidia
  acceleratorType: gpu
  acceleratorCount: 1
  image: vllm/vllm-openai:v0.16.0
  modelSources:
    - modelId: Qwen/Qwen2.5-0.5B-Instruct
      sourceUri: hf://Qwen/Qwen2.5-0.5B-Instruct
```

For this direct NVIDIA vLLM contract, AIM Engine generates `vllm serve`, points it at the locally mounted model artifact, sets the served model name, host, port, and tensor-parallel size, and forces Hugging Face offline mode. `engineArgs` is optional and is only needed for additional vLLM tuning or to override generated tuning values such as `served-model-name` and `tensor-parallel-size`; the model path, host, and port remain controller-managed.

The profile itself does not need `spec.caching`. An `AIMService` selecting the profile creates or reuses the cache according to the service's caching mode and does not create the InferenceService until the model artifact is ready. Because `type: unoptimized` is below the default profile-selection floor, a selector using this reference profile must explicitly set `minimumType: unoptimized` (or `any`).

### Partitioned GPUs

The vendor resolver is also the extension point for partition-specific device-plugin resources. In the current implementation, AMD partition modes still derive `amd.com/gpu`; a later resource-naming strategy can map a concrete mode such as `CPX-NPS4` to `amd.com/cpx_nps4` without changing profile selection or runtime projection.

For recognized AMD compute partition modes, generated host CPU and memory defaults scale with slice density: SPX=1, DPX=2, QPX=4, and CPX=8 slices per physical GPU. For example, eight `CPX-NPS4` accelerator units receive the same aggregate host defaults as one unpartitioned GPU. The geometry-agnostic `partitioned` value and unknown schemes do not receive host defaults; specify `resources` explicitly for those profiles.

Until partition-specific resource mapping is enabled, a cluster that advertises such a resource can override the derived device resource explicitly:

```yaml
spec:
  acceleratorVendor: amd
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 0
  resources:
    requests:
      amd.com/cpx_nps4: "4"
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

## Optimization tier and the `minimumType` floor

A profile's `spec.type` (`optimized > general > preview > unoptimized`) is both a ranking signal and a selection gate. Automatic selection (`spec.model.name` / selector resolution) first considers profiles whose `type` is at or above the selector's `spec.profile.selector.minimumType` floor. For AIMServices that floor **defaults to `optimized`**, so lower-tier profiles are excluded from the optimized pass unless the service opts in:

```yaml
# AIMService — accept lower tiers (e.g. an EPYC unoptimized profile)
spec:
  model:
    name: aim-epyc-llama-3-2-1b-zen4-lowmem
  profile:
    selector:
      minimumType: any   # or "unoptimized"; default would be "optimized"
```

Any profile — including lower tiers — also remains addressable by explicit `spec.profile.name`, which bypasses the floor entirely.

`spec.autoSelectionPolicy: any` is a platform/profile-publisher opt-in for automatic selection regardless of optimization tier. If the service did not explicitly set `minimumType` and no optimized profile matched in either scope, the resolver performs a second pass containing only `any` profiles. This allows a generic runtime such as the chart-provided NVIDIA vLLM profile to remain truthfully `unoptimized` without requiring every service to lower its floor. The default policy is `optimized`, so existing AMD preview and unoptimized profiles remain excluded.

:::{warning}
`manualSelectionOnly` is deprecated

`spec.manualSelectionOnly` is no longer honored. Use `type` + `minimumType` for service-authored optimization-tier consent and `autoSelectionPolicy` for platform-authored automatic eligibility. The field is still accepted for backward compatibility but has no effect and will be removed in a future API version.
:::

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

- [AIM Models](models.md) — Four model flows that produce profiles
- [AIM Profile Sets](profilesets.md) — Derivation machinery
- [Services](services.md) — How services resolve and rank profiles
- [Model Caching](caching.md) — Cache lifecycle and configuration
- [Naming and Labels](../reference/naming-and-labels.md) — Full label catalogue
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Conditions Reference](../reference/conditions.md) — Condition catalogue
- [Legacy Service Templates](../legacy/service-templates.md) — The deprecated v1alpha1 equivalent
