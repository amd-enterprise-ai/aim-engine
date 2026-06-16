# AIM Profile Sets

`AIMProfileSet` and `AIMClusterProfileSet` are the **derivation engine** of v1alpha2. They select source profiles (by label and spec criteria), apply overrides, and produce derived `AIMProfile` / `AIMClusterProfile` resources.

You'll encounter profile sets in two ways:

1. **Implicitly**, as children of `AIMModel.spec.profiles` — every fine-tuned or custom-model `AIMModel` synthesises an `AIMProfileSet` named `<model>-profiles-<hash>` and summarises its output.
2. **Directly**, as standalone resources you author yourself when you want to derive profiles without creating an owning `AIMModel`.

The same internal primitive handles both — `AIMModel.spec.profiles` is translated into an `AIMProfileSetSpec` for the synthesised child set.

!!! info "v1alpha2"
    `AIMProfileSet`/`AIMClusterProfileSet` are new CRDs in `aim.eai.amd.com/v1alpha2` — v1alpha1 had no first-class derivation resource. The closest legacy primitive is `AIMModel.spec.profileCopy`, which performed an inline copy step against existing AIMServiceTemplates; v1alpha2 reshapes that into `AIMModel.spec.profiles` (which synthesises a child profile set), and `spec.profileCopy` is forbidden on v1alpha2. See [Migrating to v1alpha2 → AIMModel mapping](../legacy/migrating.md#aimmodel-mapping) for the full field-by-field translation.

## Cluster vs namespace scope

| Resource | Scope | Produces |
|---|---|---|
| `AIMProfileSet` | Namespace | `AIMProfile` in the same namespace |
| `AIMClusterProfileSet` | Cluster | `AIMClusterProfile` across the cluster |

For sourcing:

- `AIMProfileSet` selects from namespace-scoped `AIMProfile` objects in its own namespace.
- `AIMClusterProfileSet` selects from cluster-scoped `AIMClusterProfile` objects.
- Cache-backed sources (`spec.sourceRef`) are read from the same namespace for `AIMProfileSet` and from the operator namespace for `AIMClusterProfileSet`.

## Two source modes

A profile set selects sources in one of two modes — set by whether `spec.sourceRef` is populated.

| Mode | `spec.sourceRef` | Source pool |
|---|---|---|
| **Real-profile-backed** | omitted | Existing `AIMProfile` / `AIMClusterProfile` objects in the set's scope |
| **Cache-backed** | set | Entries from a discovery cache `ConfigMap` (typically published by an `AIMModel`) |

The two modes are mutually exclusive — the controller doesn't mix them in a single reconcile.

Cache-backed mode is mainly used by `AIMModel.spec.profiles.derivedFrom.sourceRef` when the source is an image's discovery output rather than already-materialised profiles. For standalone use, you'll almost always omit `sourceRef`.

## Selector

`spec.selector` chooses candidates from the source pool. At least one matching field must be set (validated by CRD CEL).

| Field | Description |
|---|---|
| `aimId` | Match by source `spec.aimId` (e.g. `meta-llama/Llama-3-8B-Instruct`). Rejected by CEL when `role: base` — target identity for derived profiles lives on `overrides.aimId` instead (see [Identity stamping](#identity-stamping) below). |
| `modelId` | Match by source `spec.modelId`. Rejected by CEL when `role: base` — use `overrides.modelId`. |
| `profileId` | Match by source `spec.profileId`. Rejected by CEL when `role: base` — use `overrides.profileId`. |
| `engine` | Match by inference engine (`vllm`, `tgi`). |
| `metric` | Match `latency` or `throughput`. |
| `precision` | Match by numeric precision (`fp8`, `fp16`, `bf16`, ...). |
| `type` | Match optimization level exactly (`optimized`, `general`, `preview`, `unoptimized`). |
| `minimumType` | Match optimization level as a floor — that tier or better (`optimized > general > preview > unoptimized`), or `any` for no floor. Empty means no floor for derivation selectors (AIMService auto-selection defaults this floor to `optimized` instead). ANDs with `type` when both are set. |
| `acceleratorModel` | Match by accelerator identifier (`MI300X`, `MI325X`, ...). |
| `acceleratorType` | Match `gpu` or `cpu`. |
| `acceleratorCount` | Match by accelerator unit count. |
| `engineArgs` | Partial match on engine args — every provided top-level key must exist on the source with an equal value. |
| `modelRef.name` | Narrow to profiles produced by a specific AIM(Cluster)Model (matched via the `source-model` label). |
| `modelRef.scope` | `Auto` (try namespace first, then cluster), `Namespace`, or `Cluster`. Default `Auto`. |
| `role` | `base` or `deployable`. Default `deployable`. |
| `origin` | `discovered`, `derived`, or `user-authored`. When unset, doesn't filter by origin. |

### Identity stamping

Identity for the derived profile is set via `overrides.{aimId,modelId,profileId}`. The selector never writes identity onto the derived profile — its identity fields are always source-side filters (strict equality, empty = wildcard).

When `selector.role: base`, CEL on `AIMProfileSetSpec` and `AIMModelProfilesSpec` enforces this split:

| Rule | Effect |
|---|---|
| `selector.aimId/modelId/profileId` rejected | Base profiles have no source identity to filter on. The fields would silently do nothing useful. |
| `overrides.aimId` required | Base profiles ship with empty `spec.aimId`. The derived profile would otherwise have no identity to bind against from an `AIMService.spec.profile.selector.aimId`. |
| `overrides.modelId` required | Same reasoning for the model-level identifier. (`modelSources[0].modelId` auto-derivation is bypassed for `role: base` — explicit is required.) |
| `overrides.profileId` optional | Most callers don't pin a profileId; the AIMProfile reconciler synthesises one if unset. |

For `role: deployable` derivations (the AIMProfileSet remix case where you're re-deriving an already-deployable profile into accelerator/engine variants), all three identity overrides are optional — the source profile's existing identity carries through unchanged unless you explicitly override it.

The same shape applies to [`AIMModel.spec.profiles.derivedFrom`](../guides/custom-models.md): selector filters source candidates, overrides stamp the target identity onto the derived profile. CEL enforces the split on both surfaces identically.

## Version policy

`spec.versionPolicy` controls how multiple versions of matching source profiles are filtered.

| Policy | Behavior |
|---|---|
| `pinned` (default) | Only match profiles whose version (extracted from `spec.image` tag) equals `spec.version`. Requires `version` to be set. |
| `latest` | Within each (accelerator, precision, metric) group, only match the highest-version profile. |
| `all` | Match every profile that satisfies the selector, regardless of version. |

`pinned` is the typical choice for reproducibility. `latest` auto-tracks upstream improvements. `all` is common for custom-model derivation (where each version represents a different accelerator/precision combo rather than a temporal sequence).

## Overrides

`spec.overrides` mutates selected sources before writing them out as derived profiles.

| Field | Merge semantics |
|---|---|
| `modelSources` | Replaces source `modelSources` entirely. The first source's `modelId` is auto-copied into the derived profile's `spec.modelId`. |
| `acceleratorModel` | Replaces source `acceleratorModel`. |
| `acceleratorCount` | Replaces source `acceleratorCount`. |
| `containerEnv` | Merges by env-var name; matching names override, new names append. |
| `engineEnv` | Merges by key; matching keys override, new keys append. |
| `engineArgs` | Shallow merge on top of source `engineArgs`; matching top-level keys override, new keys append. |
| `image` | Replaces the derived profile's container image entirely. When unset, the image is resolved from the source profile and whether the override replaces the weights — see [Image resolution](#image-resolution) below. |

### Image resolution

When `overrides.image` is unset, the derived profile's image depends on the source profile's role and whether the override replaces the model weights (`overrides.modelSources`):

| Source profile | Override replaces weights? | Resulting image |
|---|---|---|
| Deployable (`role=deployable`, a model-optimized image) | **Yes** (`modelSources` set) | Resolves back to the source's `status.baseImage` (`AIM_BASE_IMAGE_REF`), normalised to `MAJOR.MINOR` and rebased onto the source image's registry+org. The new weights load onto the clean aim-base runtime. |
| Deployable | **No** (partitioning-only / env-only / engineArgs-only override) | **Keeps the optimized source image.** The model optimizations and tuning still apply, so a re-partition or env tweak stays on the model-optimized image instead of silently downgrading to the base runtime. |
| Base (`role=base`, an aim-base image used for [Custom Models](../guides/custom-models.md)) | — | Keeps the source image unchanged. A base profile already *is* the runtime image; weights come from `overrides.modelSources`. |

An explicit `overrides.image` always wins outright, regardless of the above.

Derived profiles inherit the source profile's `status.baseImage` so subsequent derivations or overlays can rebase consistently.

## Spec shape

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: mi308x-variants
  namespace: ml-team
spec:
  selector:
    role: deployable
    modelRef:
      name: llama-3-8b-official
      scope: Auto
    origin: discovered
    acceleratorModel: MI300X
    engineArgs:
      tensor-parallel-size: 2
  versionPolicy: latest
  overrides:
    image: quay.io/acme/aim-base-vllm:0.9.0
    acceleratorModel: MI308X
    acceleratorCount: 4
    engineArgs:
      tensor-parallel-size: 4
      max-model-len: 32768
```

| Field | Description |
|---|---|
| `sourceRef` | Optional reference to a discovery-cache `ConfigMap`. If omitted, source selection uses same-scope real profiles. |
| `selector` | Match criteria — see [Selector](#selector). |
| `versionPolicy` | `pinned`, `latest`, `all` — see [Version policy](#version-policy). |
| `version` | Required when `versionPolicy: pinned`. |
| `image` | Optional image override applied to derived profiles. |
| `overrides` | Mutations applied to selected sources — see [Overrides](#overrides). |
| `imagePullSecrets` | Propagated to derived profiles. |
| `serviceAccountName` | Propagated to derived profiles. |

See the [CRD API Reference](../reference/api/v1alpha2.md#aimprofilesetspec) for the full schema.

## Examples

### Real-profile-backed derivation

Standalone profile set that derives MI308X variants from existing MI300X profiles owned by `llama-3-8b-official`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: mi308x-variants
  namespace: ml-team
spec:
  selector:
    modelRef:
      name: llama-3-8b-official
    acceleratorModel: MI300X
  versionPolicy: latest
  overrides:
    acceleratorModel: MI308X
    acceleratorCount: 4
```

### Cache-backed derivation

Profile set that derives directly from an `AIMModel`'s discovery cache without going through materialised profiles:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: qwen3-cache-derived
  namespace: ml-team
spec:
  sourceRef:
    name: qwen3-32b-custom-discovery
  selector:
    aimId: qwen/Qwen3-32B
    acceleratorModel: MI300X
  versionPolicy: pinned
  version: "0.9.0"
  overrides:
    image: quay.io/acme/aim-base-vllm:0.9.0
    modelSources:
      - modelId: acme/qwen3-32b-custom
        sourceUri: s3://models/qwen3-32b-custom/
```

### Custom-model derivation (role: base)

The derivation half of the [custom-model flow](../guides/custom-models.md). When authored by an `AIMModel.spec.profiles` it's synthesised automatically; here it's shown standalone for reference:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: custom-derivatives
  namespace: ml-team
spec:
  selector:
    role: base
    aimId: acme/custom-transformer
    modelRef:
      name: aim-base-vllm
      scope: Namespace
  versionPolicy: all
  overrides:
    modelSources:
      - modelId: acme/custom-transformer
        sourceUri: s3://acme-models/custom-transformer
```

## Status

| Field | Description |
|---|---|
| `status` | `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`, `NotAvailable` |
| `conditions` | Detailed conditions — see [Conditions Reference](../reference/conditions.md) |
| `managedProfiles.total` | Number of derived profiles the set intends to manage |
| `managedProfiles.ready` | Number of managed profiles whose own status is `Ready` |
| `managedProfiles.notAvailable` | Number of managed profiles whose own status is `NotAvailable` |
| `managedProfiles.deployable` | Number of managed profiles that carry `aimId` and `modelSources` (deployable). For typical fine-tune / custom-model derivation this equals `total`. |
| `managedProfiles.base` | Number of managed profiles that don't carry both fields. Non-zero typically means a base-image AIMModel was the source. |

Zero values serialise explicitly (no `omitempty`), so `kubectl` printcolumns and assertions on count fields are always present.

### Conditions

| Condition | Reasons |
|---|---|
| `DerivationReady` | `ManagedProfilesReady`, `NoMatchingProfiles`, `Progressing`, `Degraded` |
| `Ready` | `AllComponentsReady`, `ComponentsNotReady`, `Progressing` |

`DerivationReady=False / NoMatchingProfiles` means the selector matched zero source candidates. This is by far the most common failure mode; see troubleshooting below.

## Derived-profile labelling

Profiles produced by an `AIMProfileSet` carry these labels:

| Label | Value |
|---|---|
| `aim.eai.amd.com/profile-source` | `copy` |
| `aim.eai.amd.com/profile-role` | `deployable` (or `base` if the derivation was authored to produce base profiles, which is unusual) |
| `aim.eai.amd.com/profile-origin` | `derived` |
| `aim.eai.amd.com/source-model` | Name of the AIM(Cluster)Model that owns the profile set (when applicable) |
| `aim.eai.amd.com/source-model-scope` | `namespace` or `cluster` |

By default, derived profiles are **not** marked `profile-copyable`, which prevents accidental cascading copy chains. If you want a derived profile to itself be a valid derivation source, set the label explicitly.

## Source eligibility

For real-profile-backed mode, a source profile must carry `aim.eai.amd.com/profile-copyable="true"` to be considered. This separates provenance ("where did this come from") from source eligibility ("can this be copied"):

- Image-discovery-produced profiles owned by AIM(Cluster)Models are marked copyable by default.
- Derived profiles are not marked copyable by default — opt in explicitly if you want chained derivation.
- Hand-authored profiles need the label added explicitly to participate.

Cache-backed mode bypasses this — the entries are taken directly from the discovery catalog.

## Troubleshooting

### `DerivationReady=False / NoMatchingProfiles`

The selector matched zero source candidates. Diagnose by listing candidates and comparing:

```bash
# What did the selector look for?
kubectl get aimprofileset <name> -o jsonpath='{.spec.selector}'

# What profiles are in the pool?
kubectl get aimprofile -n <namespace> -o custom-columns=\
NAME:.metadata.name,\
AIMID:.spec.aimId,\
PRECISION:.spec.precision,\
ACCEL:.spec.acceleratorModel,\
ROLE:.metadata.labels.aim\\.eai\\.amd\\.com/profile-role,\
COPYABLE:.metadata.labels.aim\\.eai\\.amd\\.com/profile-copyable
```

Common causes:

- A selector field is too restrictive — relax `precision`, `acceleratorModel`, etc.
- `modelRef.name` typo or wrong scope.
- Source profiles lack the `profile-copyable: "true"` label.
- For cache-backed mode: `spec.sourceRef.name` doesn't reference an existing ConfigMap.

### `managedProfiles.total == 0` after applying

Either the set hasn't been reconciled yet (look at `status.conditions[?(@.type=='DerivationReady')]`) or every candidate was filtered out before the derived-profile build step. The selector is the most common culprit.

### Model-managed derivation is stuck

When an `AIMModel.spec.profiles` synthesises a profile set, the model's `status.profileSetRef` points at it. Inspect directly:

```bash
kubectl get aimmodel <name> -o jsonpath='{.status.profileSetRef.name}'
kubectl get aimprofileset <profileset-name> -o yaml
```

Conditions on the profile set carry the actionable detail; the model just summarises.

## Related documentation

- [AIM Models](models.md) — Three flows that synthesise profile sets
- [Profiles](profiles.md) — What derived profiles carry; provenance labels
- [Fine-Tuned Models](../guides/fine-tuned-models.md) — End-to-end derivation walkthrough (deployable sources)
- [Custom Models](../guides/custom-models.md) — End-to-end derivation walkthrough (base-profile sources)
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Conditions Reference](../reference/conditions.md) — Full condition catalogue
