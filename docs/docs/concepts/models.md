# AIM Models

`AIMModel` and `AIMClusterModel` are the entry point for getting a model onto the cluster. Each resource describes one model and produces a set of `AIMProfile` resources that `AIMService` can deploy. Image-backed models also publish their complete profile definitions for selection by [AIM Profile Sets](profilesets.md), including definitions whose original hardware is absent from the cluster.

:::{admonition} v1alpha2
:class: note

This page documents the `aim.eai.amd.com/v1alpha2` API. For the v1alpha1 `AIMModel` shape (with `spec.custom`, `spec.modelSources`, `spec.discovery`, etc.) see [Legacy AIMModel](../legacy/aimmodel-v1alpha1.md).
:::
## Four flows

Every v1alpha2 AIMModel uses one of four flows. The flow is determined by which field you populate on the spec:

| Flow | When to use | Spec shape | Source of profiles | `status.kind` |
|---|---|---|---|---|
| **Image-backed** | Onboarding an AIM image | `spec.image` | The AIM container image itself (discovery) | `Image` |
| **Generated** | Deploying model weights with a generic runtime configured for cluster hardware | `spec.modelId` set | RuntimeConfig `model.profileGeneration.fallbacks` | `Generated` |
| **Fine-tuned** | Deploying a fine-tune of a published architecture | `spec.profiles.derivedFrom` selecting deployable profiles | A previously-applied official AIMModel | `Derived` |
| **Custom** | Deploying a model whose architecture isn't in the catalog | `spec.profiles.derivedFrom` selecting base-image base profiles | A previously-applied base-image AIMModel | `Custom` |

The onboarding shapes are:

| Spec shape | Profile selection |
|---|---|
| `image` | All definitions discovered in the image; supported entries are materialised. |
| `profiles` without `image` | External sources selected by the required `profiles.derivedFrom`. |
| `modelId` | Generated profiles from RuntimeConfig fallbacks; exclusive with `image` and `profiles`. |

`image`, `profiles`, and `modelId` are mutually exclusive. The flow is surfaced as `status.kind` and shown as the `KIND` column in `kubectl get aimmodel`. Image-backed models retain `kind: Image` for generic base images. See [Base-image models](#base-image-models) for base-profile selection.

End-to-end guides for the two derivation flows:

- [Fine-Tuned Models](../guides/fine-tuned-models.md)
- [Custom Models](../guides/custom-models.md)

## Cluster vs namespace scope

| Resource | Scope | Typical owner | Produces |
|---|---|---|---|
| `AIMModel` | Namespace | Application / ML team | `AIMProfile` in the same namespace |
| `AIMClusterModel` | Cluster | Platform team | `AIMClusterProfile` across the cluster |

When an `AIMService` references a model by name, AIM Engine looks for a namespace-scoped `AIMModel` first, then falls back to a cluster-scoped `AIMClusterModel` with the same name. This lets teams override a cluster default without affecting other namespaces.

The two kinds share the same `spec` and `status` shape — every spec example below applies to both unless noted.

## Flow 1 — Official AIM model

Use this when AMD publishes an AIM image for the exact model you want to deploy.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: llama-3-8b-official
  namespace: ml-team
spec:
  image: quay.io/amd/aim-llama-3-8b-instruct:0.9.0
  imagePullSecrets:
    - name: registry-creds
  serviceAccountName: aim-runtime
```

The controller:

1. Runs an in-cluster **discovery Job** that inspects the image's profile YAMLs (`/workspace/aim-runtime/profiles/<aim_id>/*.yaml`) and writes the raw documents and discovery metadata into a `ConfigMap` (`status.discoveryCacheRef`).
2. Normalises the complete catalog into profile definitions available to profile-set selectors.
3. Evaluates each discovered profile against the cluster's nodes — GPU model, partitioning mode, accelerator count, and resource capacity.
4. Materialises one `AIMProfile` per supported entry. Unsupported entries are counted in `status.discoveredProfiles.unsupported` and remain available as derivation sources.
5. Watches node events and re-evaluates hardware support when nodes are added or removed.

The resulting profiles are **deployable** (carry `aimId` and `modelSources`) and labelled `aim.eai.amd.com/profile-role=deployable`, `aim.eai.amd.com/profile-origin=discovered`.

Profile sets select these definitions using ordinary filters such as `acceleratorModel`, `acceleratorCount`, `modelId`, and `engineArgs`. A model with successful discovery and zero materialised profiles can supply every matching definition. Source eligibility depends on current discovery, independently of the model's overall readiness or source hardware availability.

### Deriving hardware variants

Keep the source image model separate from its variants. For example, a standalone profile set maps the source model's definitions to a different GPU:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: llama-mi325x
  namespace: ml-team
spec:
  selector:
    modelRef:
      name: llama-3-8b-official
  versionPolicy: all
  overrides:
    acceleratorModel: MI325X
```

The source model's catalog remains unchanged, including when its hardware is unavailable. For a separately addressable derived model, put `profiles.derivedFrom` on a second AIMModel instead. Combining `image` and `profiles` is not supported.

See [Partitioning and topology](profilesets.md#partitioning-and-topology) for explicit source/target counts and tensor-parallel settings. Overrides do not automatically retune the runtime or guarantee model fit.

### Status signal

```bash
kubectl get aimmodel llama-3-8b-official -o jsonpath='{.status}' | jq
```

```json
{
  "status": "Ready",
  "aimId": "meta-llama/Llama-3-8B-Instruct",
  "baseImage": "ghcr.io/silogen/aim-base:0.11",
  "discoveryCacheRef": { "name": "llama-3-8b-official-discovery-5e8ad928" },
  "discoveredProfiles": { "total": 7, "supported": 5, "unsupported": 2 },
  "managedProfiles": { "total": 5, "ready": 5, "deployable": 5, "base": 0, "notAvailable": 0 }
}
```

## Flow 2 — Generated runtime profile

Use this when model weights are identified by a model ID and the platform has configured a generic runtime for the available hardware:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen3-5-0-8b
  namespace: ml-team
spec:
  modelId: Qwen/Qwen3.5-0.8B
```

The controller defaults `aimId` to `modelId` and the source URI to `hf://<modelId>`. Both may be overridden for fine-tuned or S3-hosted weights:

```yaml
spec:
  modelId: acme/qwen-finetune
  aimId: Qwen/Qwen3.5-0.8B
  source:
    uri: s3://customer-models/qwen-finetune
```

The hardware shape is declared per-model. `spec.accelerator` sets how many devices the model needs and optionally narrows which fallback serves it:

```yaml
spec:
  modelId: Qwen/Qwen3.5-0.8B
  accelerator:
    count: 4              # default 1; also becomes the tensor-parallel size
    vendor: nvidia        # optional
    model: H100           # optional
```

Generated profiles are created only when a matching RuntimeConfig fallback and hardware are available. They are labelled `profile-origin=generated`. Existing profiles are retained and become unavailable if the matching nodes temporarily disappear.

:::{admonition} Generated profile selection is platform policy
:class: important

Generated profiles default to `type: unoptimized` — a generic runtime template is not tuned for any particular model. That is **below** the AIMService selection floor. RuntimeConfig fallbacks default to `autoSelectionPolicy: optimized`, which keeps that floor in force. A platform may set `autoSelectionPolicy: any` to admit the generated profile only when no optimized profile matches and the service did not explicitly set `minimumType`. The chart-provided NVIDIA fallback uses this policy for out-of-the-box model-ID onboarding. See [Optimization tier and auto-selection policy](runtime-config.md#optimization-tier-and-auto-selection-policy).
:::

### Resolution strategy

`status.profileGeneration.strategy` reports the resolution mechanism, and `status.profileGeneration.matchedFallbacks` lists the fallbacks that produced a profile:

```yaml
status:
  kind: Generated
  profileGeneration:
    strategy: RuntimeFallback
    matchedFallbacks: [nvidia-vllm]
```

`RuntimeFallback` resolves generated profiles from RuntimeConfig fallbacks. Existing copyable generated profiles also participate in profile-set source selection. The generated source pool consists of materialised profiles; RuntimeConfig fallback definitions belong to the generation flow.

## Flow 3 — Fine-tuned model

Use this when you have your own fine-tune of a model whose architecture **is** in the catalog. The official AIMModel's profiles already carry the tuned engine arguments and accelerator pairings — you reuse them and override only the model sources.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: llama-3-8b-finetune-acme
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        aimId: meta-llama/Llama-3-8B-Instruct
        acceleratorModel: MI300X
    versionPolicy: pinned
    version: "0.9.0"
    overrides:
      modelSources:
        - modelId: acme/llama-3-8b-finetune
          sourceUri: hf://acme/llama-3-8b-finetune
```

`selector.aimId` matches source profiles by their existing `spec.aimId`. The derived profile inherits `aimId` from the source and only `modelSources` and `modelId` change.

See [Fine-Tuned Models](../guides/fine-tuned-models.md) for the full walkthrough, version-policy semantics, and override merge rules.

## Flow 4 — Custom model

Use this when your model's architecture isn't in the catalog. Apply a **base-image AIMModel** to publish base definitions (generic runtime configs with no model identity), then a custom-model AIMModel that overlays your weights and identity.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: aim-base-vllm
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-base:0.11
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: acme-custom-transformer
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        role: base
        modelRef:
          name: aim-base-vllm
          scope: Namespace
    versionPolicy: all
    overrides:
      aimId: acme/custom-transformer
      modelId: acme/custom-transformer
      modelSources:
        - modelId: acme/custom-transformer
          sourceUri: s3://acme-models/custom-transformer
```

The shape differs from fine-tunes in two ways:

- `selector.role: base` matches only base profiles; `selector.modelRef.name` pins to a specific base-image AIMModel.
- Target identity for the derived profile lives on `overrides.{aimId,modelId}` (required by CEL when `role: base` — base profiles ship with no identity to inherit).

See [Custom Models](../guides/custom-models.md) for the full walkthrough, the identity-stamping semantics, and base-image requirements.

## Base-image models

A **base-image AIMModel** uses a generic AIM base image whose profile YAMLs declare runtime/engine/accelerator settings and leave model identity and sources empty. Its complete catalog supplies **base** definitions for derivation. When `spec.profiles` is omitted, hardware-supported entries are materialised as base AIMProfiles:

- `status.deployable: false`
- `aim.eai.amd.com/profile-role: base`
- `aim.eai.amd.com/profile-origin: discovered`

Base profiles cannot back an `AIMService` directly. They exist as source material for a custom-model derivation (see [Flow 4](#flow-4-custom-model)).

`status.kind` is `Image`. A non-zero `status.managedProfiles.base` count identifies materialised base profiles; zero materialised profiles can still accompany a complete base catalog. Use `selector.role: base` on a separate derived model or profile set to select those definitions.

## Discovery cache

For image-backed models, the discovery Job writes its output into a `ConfigMap`. The cache stores:

- Raw profile YAML documents under `profiles.*.yaml` keys
- Discovery metadata in `metadata.json`, including the base image extracted from `AIM_BASE_IMAGE_REF` when available

The operator parses and normalises these documents in a shared in-process catalog cache. Profile-set selection resolves the owning model's current catalog automatically. The ConfigMap reference is useful for inspection; ordinary selectors operate across model definitions and copyable profile resources.

`status.discoveryCacheRef` points at this `ConfigMap`. `status.discovery` tracks the job lifecycle:

| Field | Description |
|---|---|
| `discovery.attempts` | Number of discovery Job attempts so far |
| `discovery.lastAttemptTime` | Timestamp of the most recent attempt |
| `discovery.lastFailureReason` | Reason the most recent attempt failed (e.g. `LogParseFailed`, `ImagePullBackOff`) |
| `discovery.specHash` | Hash of the inputs that invalidate the cache (image, imagePullSecrets, serviceAccountName, discovery command version) |

When `specHash` changes — for example, you rotate `serviceAccountName` or upgrade the image tag — the operator invalidates the catalog and re-runs discovery. During discovery or a source-read failure, existing derived outputs are retained. Current model ownership and discovery inputs determine which catalog is valid, including after an operator restart or model recreation.

## Profile-set delegation

For every model with `spec.profiles`, the controller synthesises a child `AIMProfileSet` (or `AIMClusterProfileSet`) named `<model>-profiles-<hash>` and lets it select external sources and produce derived profiles. The model summarises its child profile-set output back onto its own status; see [AIM Profile Sets](profilesets.md) for the mechanics.

`status.profileSetRef` points at the child resource:

```bash
kubectl get aimmodel acme-custom-transformer \
  -o jsonpath='{.status.profileSetRef.name}'
# acme-custom-transformer-profiles-2be25a47
```

Image-backed models without `spec.profiles` materialise supported discoveries directly; `status.profileSetRef` is empty. Standalone profile sets own their outputs independently, so their output counts and readiness belong to the set, not to the source model.

## Spec fields

The unified `spec` shape used by all four flows:

| Field | Used by | Description |
|---|---|---|
| `image` | Image-backed, Base | Source image to inspect for discovery. Exclusive with `profiles` and `modelId`. |
| `modelId` | Generated | Canonical model identity used to generate profiles from RuntimeConfig fallbacks. |
| `aimId` | Generated | Optional architecture identity override; defaults to `modelId`. |
| `source` | Generated | Optional source URI and metadata override; URI defaults to `hf://<modelId>`. |
| `accelerator` | Generated | Optional hardware request — `count` (default 1), `vendor`, `model`, `partitioningMode`. Narrows which RuntimeConfig fallback applies and is stamped on the generated profile. Rejected without `modelId`. |
| `runtimeConfigName` | Generated | Optional RuntimeConfig name; defaults to `default`. |
| `profiles` | Image-backed, Fine-tuned, Custom | Profile selection and overrides. With `image`, `derivedFrom` is optional and selection is local to the image. Without `image`, `derivedFrom` is required and selects external sources using the version policy. Exclusive with `modelId`. |
| `imagePullSecrets` | Official, Fine-tuned, Custom | Secrets used for image inspection and propagated to derived runtime profiles. `AIMModel` discovery resolves them in the model namespace; `AIMClusterModel` discovery resolves them in the operator namespace. Consumers of a cluster profile need corresponding credentials in their service namespace. Generated-flow pull secrets belong on the RuntimeConfig fallback. See [Private Registries](../guides/private-registries.md). |
| `serviceAccountName` | Official, Fine-tuned, Custom | Service account propagated to managed child resources. Generated-flow service accounts belong on the RuntimeConfig fallback. |

The v1alpha2 CRD explicitly forbids legacy fields such as `spec.modelSources`, `spec.custom`, `spec.customTemplates`, and `spec.profileCopy`. `spec.aimId`, `spec.source`, and `spec.runtimeConfigName` are valid for the generated flow.

## Status fields

| Field | Description |
|---|---|
| `status` | Overall status (`Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`, `NotAvailable`) |
| `conditions` | Detailed reconciliation conditions (see [Conditions Reference](../reference/conditions.md)) |
| `kind` | Discriminator for the onboarding flow: `Image`, `Generated`, `Derived`, or `Custom`. |
| `profileGeneration` | `Generated` models only: `strategy` (the resolution mechanism that ran — `RuntimeFallback` today) and `matchedFallbacks` (the RuntimeConfig fallbacks that produced a profile). |
| `aimId` | Resolved architecture identifier — populated by discovery for `Image` models, defaulted from `modelId` for `Generated` models, and inherited or overridden for `Custom`/`Derived` models |
| `baseImage` | Extracted `AIM_BASE_IMAGE_REF` when available |
| `discoveryCacheRef` | Reference to the raw discovery cache `ConfigMap` (image-backed flows) |
| `discoveredProfiles` | Raw catalog counts: `total`, `supported`, `unsupported`, plus `byHardware[]` listing each discovered hardware footprint with its `{metric, precision}` profiles. Inline overrides leave these discovery counts unchanged. |
| `discovery` | Discovery Job state: `attempts`, `lastAttemptTime`, `lastFailureReason`, `specHash` |
| `profileSetRef` | Reference to the synthesised child profile set when `spec.profiles` is set |
| `managedProfiles` | Actual managed output counts: `total`, `ready`, `notAvailable`, `deployable`, `base`. |

The `managedProfiles` counts always serialise zero values explicitly (no `omitempty`), so `kubectl` printcolumns and assertions on `.status.managedProfiles.{total,ready,...}` are stable.

For models using `spec.profiles`, the child profile set provides alternative profiles, not a requirement that every hardware variant be available. A valid, current child set with at least one ready profile can make the model `Ready`, even if other alternatives are unavailable, failed, or still reconciling. The child must be controlled by this model, match the model's current requested profile-set spec, and have current observed status. Errors establishing that desired state still block readiness; individual alternative diagnostics remain on the profiles.

Availability does not relax replacement safety: pruning predecessor profiles during a transition to derivation still waits for the complete current replacement set to be ready.

## Resolution and precedence

When an `AIMService` references a model by name, AIM Engine resolves it in this order:

1. Namespace-scoped `AIMModel` with that name.
2. Cluster-scoped `AIMClusterModel` with that name.

Namespace wins. The resolved scope is recorded in the service's `status.resolvedModel.scope`.

## Troubleshooting

### Discovery fails

Check operator logs for image-inspection errors:

```bash
kubectl -n aim-system logs -l app.kubernetes.io/name=aim-engine --tail=100 \
  | grep -i "<model-name>"
```

Inspect the discovery Job directly:

```bash
kubectl -n <namespace> get jobs -l aim.eai.amd.com/model=<model-name>
kubectl -n <namespace> logs job/<discovery-job>
```

Common causes:

- Missing or invalid `imagePullSecrets`
- Image reference not found at the registry
- `LogParseFailed` — the container is not an AIM-compatible image
- Registry authentication or connectivity failures

`status.discovery.lastFailureReason` captures the most recent reason, and `status.conditions[?(@.type=='DiscoveryReady')]` carries the human-readable message including the next retry interval.

### Discovery succeeds but no profiles materialise

With `DiscoveryReady=True`, `status.managedProfiles.total == 0` and `status.discoveredProfiles.unsupported > 0` indicate that discovery found definitions whose hardware is unavailable. Compare the cluster's accelerator labels:

```bash
kubectl get nodes -o jsonpath='{.items[*].metadata.labels}' \
  | tr ',' '\n' | grep aim-accelerator
```

against the accelerator requirements in `status.discoveredProfiles.byHardware` or the discovery cache. The complete definitions remain selectable by profile sets. Use a standalone set to derive hardware variants across models, or a separate derived model to expose the variant under its own model name.

### Derivation produces no profiles

`status.profileSetRef` points at the child `AIMProfileSet`. Inspect it:

```bash
kubectl get aimprofileset $(kubectl get aimmodel <name> \
  -o jsonpath='{.status.profileSetRef.name}') -o yaml
```

`DerivationReady=False / NoMatchingProfiles` means the selector matched zero source candidates. See the troubleshooting sections in [Fine-Tuned Models](../guides/fine-tuned-models.md#troubleshooting) and [Custom Models](../guides/custom-models.md#troubleshooting).

`DerivationReady=False / NoEligibleProfiles` means matching profile resources exist but are not published as copyable. Annotate a hand-authored source profile with `aim.eai.amd.com/profile-copyable: "true"`. If the source is generated by another derivation and the chain is intentional, enable `outputPolicy.copyable` on that producer rather than annotating its generated profiles directly.

## Related documentation

- [Fine-Tuned Models](../guides/fine-tuned-models.md) — End-to-end walkthrough for flow 3
- [Custom Models](../guides/custom-models.md) — End-to-end walkthrough for flow 4
- [Profiles](profiles.md) — Base vs deployable, provenance labels, status fields
- [AIM Profile Sets](profilesets.md) — Derivation machinery underneath `spec.profiles`
- [Services](services.md) — How `AIMService` resolves models and profiles
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Legacy AIMModel (v1alpha1)](../legacy/aimmodel-v1alpha1.md) — The deprecated v1alpha1 shape
