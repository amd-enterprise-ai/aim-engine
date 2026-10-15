# AIM Profile Sets

`AIMProfileSet` and `AIMClusterProfileSet` select profile definitions, apply overrides, and produce derived `AIMProfile` / `AIMClusterProfile` resources. Selection spans image-backed model catalogs and copyable profile resources, including definitions whose original hardware is absent from the cluster.

You'll encounter profile sets in two ways:

1. **Implicitly**, as children of `AIMModel.spec.profiles` — externally derived models synthesise an `AIMProfileSet` named `<model>-profiles-<hash>` and summarise its output.
2. **Directly**, as standalone resources you author yourself when you want to derive profiles without creating an owning `AIMModel`.

The same internal primitive handles both — `AIMModel.spec.profiles` is translated into an `AIMProfileSetSpec` for the synthesised child set.

:::{admonition} v1alpha2
:class: note

Profile sets use the `aim.eai.amd.com/v1alpha2` API. Models configure derivation through `spec.profiles`; standalone sets expose selection and overrides directly.
:::
## Cluster vs namespace scope

| Resource | Scope | Produces |
|---|---|---|
| `AIMProfileSet` | Namespace | `AIMProfile` in the same namespace |
| `AIMClusterProfileSet` | Cluster | `AIMClusterProfile` across the cluster |

For sourcing:

- `AIMProfileSet` evaluates model definitions and copyable profiles in its own namespace first. It falls back to cluster-scoped sources when no local candidate satisfies the complete selector, eligibility rules, and version policy.
- `AIMClusterProfileSet` selects only cluster-scoped model definitions and profiles.
- `selector.modelRef.scope: Namespace` restricts selection to the set's namespace; `Cluster` selects only cluster sources. `Auto` uses namespace-first fallback.
- Namespace-first selection returns one scope's matches. Other namespaces are outside the source pool.
- Explicit catalog sources (`spec.sourceRef`) are read from the same namespace for `AIMProfileSet` and from the operator namespace for `AIMClusterProfileSet`.

For example, a local MI325X definition does not suppress a cluster MI300X match for `selector.acceleratorModel: MI300X`. A fully matching local definition takes precedence even when its source hardware is unavailable.

## Source selection

With `spec.sourceRef` omitted, a selector evaluates a combined logical pool:

| Source | Available definitions |
|---|---|
| Image-backed `AIMModel` / `AIMClusterModel` | All definitions in the current discovered catalog, including hardware-unsupported entries. Discovery-disabled and legacy custom/derivation models continue to use resource-backed sources. |
| Direct `AIMProfile` / `AIMClusterProfile` | Existing resources marked `aim.eai.amd.com/profile-copyable: "true"`, including hand-authored profiles. |
| Generated or other derived profiles | Existing copyable resources, subject to the same selector and eligibility rules. Profile-set outputs are copyable when their producer sets `outputPolicy.copyable: true`. |

Use any combination of source filters: accelerator model, partitioning mode, counts, `modelId`, `aimId`, engine, precision, and the other selector fields below. `selector.modelRef` is optional and narrows the selection to one producing model. A selector without it can derive every matching profile across models in the selected scope.

Each model definition and its corresponding materialised resource represent one logical source. Independent model definitions and direct profiles remain distinct even when they share a `profileId` or identical runtime settings. Hardware changes and source materialisation leave logical source identity and derived names stable.

Derived names preserve the previous release's naming inputs. The controller carries an internal `aim.eai.amd.com/legacy-catalog-naming` annotation through catalog-derived profiles so newly decoded vendor metadata does not rename downstream, explicitly opted-in derivations. Explicit vendor overrides retain their existing naming behavior. This marker is regenerated from source lineage if children are recreated; it is not an adopted-name map and should not be edited by users.

Current, valid discovery makes image definitions available independently of the source model's overall `Ready` status. Selected outputs are materialised with the requested overrides and evaluated against their target hardware. Source hardware availability does not filter the derivation pool.

Image models publish discovered definitions without transformation. Apply overrides in a separate profile set or derived model; `image` and `profiles` remain mutually exclusive on new v1alpha2 models.

### Explicit catalog source

`spec.sourceRef: {name: <configmap-name>}` selects an alternate catalog ConfigMap in place of the default source pool. The reference identifies a ConfigMap, not a model. Model-based narrowing uses `selector.modelRef`.

`sourceRef` and `selector.modelRef` are mutually exclusive, even if the catalog is owned by the named model. Other selector fields can filter the explicit catalog's contents, and the selector may be omitted. The same restriction applies under `AIMModel.spec.profiles.derivedFrom` and `AIMClusterModel.spec.profiles.derivedFrom`.

:::{note}
Managed catalog ConfigMap updates reconcile affected profile sets. User-maintained alternate catalogs are read on reconciliation; re-annotate the profile set to pick up an edit promptly.
:::

## Selector

`spec.selector` chooses candidates from the source pool. Default source selection requires at least one matching field. An explicit `sourceRef` scopes selection to that catalog and permits an omitted selector.

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
| `acceleratorVendor` | Match by accelerator vendor (`amd`, `nvidia`). |
| `acceleratorType` | Match `gpu` or `cpu`. |
| `acceleratorCount` | Match by accelerator unit count. |
| `acceleratorPartitioningMode` | Match source partitioning: `unpartitioned`, `partitioned`, or a specific scheme such as `CPX-NPS4`. |
| `engineArgs` | Partial match on engine args — every provided top-level key must exist on the source with an equal value. |
| `modelRef.name` | Narrow to definitions and profiles produced by a specific AIM(Cluster)Model. Mutually exclusive with `sourceRef`. |
| `modelRef.scope` | `Auto` (fully matching namespace candidates first, then cluster), `Namespace`, or `Cluster`. Default `Auto`. |
| `role` | `base` or `deployable`. Default `deployable`. |
| `origin` | `discovered`, `derived`, `generated`, or `user-authored`. When unset, doesn't filter by origin. |

All provided fields are ANDed together. Engine-argument matching uses literal keys and values: `tensor-parallel-size` and `tensor_parallel_size` are distinct keys, and a missing TP setting does not imply TP=1.

Derivation preserves the source's optimization type, including an unspecified value; it never promotes an unspecified type to `optimized`. An unspecified type ranks like `unoptimized` for `minimumType`, but does not satisfy an exact `type` filter. With neither tier filter set, derivation can copy these profiles; service auto-selection still follows its own optimization-tier policy.

### Identity stamping

Identity for the derived profile is set via `overrides.{aimId,modelId,profileId}`. The selector never writes identity onto the derived profile — its identity fields are always source-side filters (strict equality, empty = wildcard).

When `selector.role: base`, CEL on `AIMProfileSetSpec` and `AIMModelProfilesSpec` enforces this split:

| Rule | Effect |
|---|---|
| `selector.aimId/modelId/profileId` rejected | Base selection uses runtime and hardware filters; overrides supply target identity. |
| `overrides.aimId` required | Supplies the target architecture identity. |
| `overrides.modelId` required | Supplies the target model identity explicitly, including when `modelSources[0].modelId` is set. |
| `overrides.profileId` optional | Most callers don't pin a profileId; the AIMProfile reconciler synthesises one if unset. |

For `role: deployable` derivations (the AIMProfileSet remix case where you're re-deriving an already-deployable profile into accelerator/engine variants), all three identity overrides are optional — the source profile's existing identity carries through unchanged unless you explicitly override it.

The same shape applies to [`AIMModel.spec.profiles.derivedFrom`](../guides/custom-models.md): selector filters source candidates, overrides stamp the target identity onto the derived profile. CEL enforces the split on both surfaces identically.

## Version policy

`spec.versionPolicy` controls how multiple versions of matching source profiles are filtered.

| Policy | Behavior |
|---|---|
| `pinned` (default) | Only match profiles whose version (extracted from `spec.image` tag) equals `spec.version`. Requires `version` to be set. |
| `latest` | Select the highest version across all matching sources; retain every match at that version. Narrow the selector to avoid comparing unrelated models. |
| `all` | Match every profile that satisfies the selector, including versionless profiles. |

`pinned` selects a specific source version. `latest` tracks the highest matching version. `all` retains all matching runtime and hardware variants across versions.

Versions describe the selected sources, before overrides. Use `all` to include versionless sources such as digest-only images.

## Overrides

`spec.overrides` modifies a copy of each selected definition before writing it as a derived profile. Source definitions and resources remain unchanged.

| Field | Merge semantics |
|---|---|
| `aimId`, `modelId`, `profileId` | Replace source identity fields. An explicit `modelId` wins over derivation from `modelSources`. |
| `modelSources` | Replaces source `modelSources` entirely. The first source's `modelId` supplies the derived profile's `spec.modelId` when no explicit `modelId` override is set. |
| `image` | Replaces the runtime image; takes precedence over top-level `spec.image`. |
| `acceleratorModel` | Replaces source `acceleratorModel`. |
| `acceleratorVendor` | Replaces source `acceleratorVendor`. |
| `acceleratorCount` | Replaces source `acceleratorCount`. |
| `acceleratorPartitioningMode` | Replaces source partitioning mode; standalone sets require an explicit target count. Explicit source and target counts are recommended. |
| `containerEnv` | Merges by env-var name; matching names override, new names append. |
| `engineEnv` | Merges by key; matching keys override, new keys append. |
| `engineArgs` | Shallow merge on top of source `engineArgs`; matching top-level keys override, new keys append. |
| `features` | Unions with source features. |

### Partitioning and topology

For a partitioning override, explicitly set both `selector.acceleratorCount` and `overrides.acceleratorCount`. The selector identifies the source device topology; the override states the number of target devices or partitions. Standalone profile sets require the target count; source-count selection is recommended rather than enforced. The existing model-derivation admission rules are unchanged.

For a single-partition workload, select a single-device TP=1 source and preserve its TP setting. Accelerator count, partitioning mode, and tensor-parallel size are independent values. Mapping profiles with TP greater than one requires a matching target topology and engine configuration.

Overrides express the requested configuration. GPU memory fit, model support on the target accelerator, and runtime compatibility require workload validation. The operator evaluates target hardware availability; it does not size models or automatically retune tensor parallelism.

### Image resolution

With neither `spec.image` nor `spec.overrides.image` set, the derived image depends on the source role and whether the override replaces model weights (`overrides.modelSources`):

| Source profile | Override replaces weights? | Resulting image |
|---|---|---|
| Deployable (`role=deployable`, a model-optimized image) | **Yes** (`modelSources` set) | Resolves back to the source's `status.baseImage` (`AIM_BASE_IMAGE_REF`) exactly, unless the merged `default` RuntimeConfig contains an explicit matching `imageRegistry.mappings` entry. The new weights load onto the clean AIM runtime selected by the image producer. |
| Deployable | **No** (partitioning-only / env-only / engineArgs-only override) | Keeps the optimized source image. |
| Base (`role=base`, an aim-base image used for [Custom Models](../guides/custom-models.md)) | — | Keeps the source image unchanged. A base profile already *is* the runtime image; weights come from `overrides.modelSources`. |

An explicit image override selects the deployment image; `overrides.image` takes precedence over top-level `spec.image`, preserving existing behavior.

When a derivation replaces model weights or explicitly selects an image, its resolved deployment image becomes the reusable base-image lineage for subsequent copies. Configuration-only copies retain the source profile's `status.baseImage`. This keeps chains such as base profile → custom model → fine-tune on the effective AIM runtime instead of descending to that runtime image's own upstream base.

If a deployable source does not expose `AIM_BASE_IMAGE_REF`, profile derivation
reports a controlled error and retains existing managed profiles instead of
guessing a runtime image from the source registry or tag.

### Profile YAML contract inheritance

Derived profiles inherit the source's complete runtime configuration: image, model sources, primary defaults, engine arguments, environment, pull credentials, service account, and runtime-YAML contract, with explicit overrides applied. A catalog definition and its corresponding profile resource supply equivalent inputs.

For resource-backed sources, the YAML contract comes from the `aim.eai.amd.com/profile-yaml-contract` annotation. For image definitions, it is decoded from the raw profile YAML. The derived profile carries the same codec, modeled-field presence, and opaque source extensions. Runtime projection uses that information to choose between legacy `gpu` fields, transitional accelerator fields, and strict canonical accelerator fields while retaining runtime-only additions that are not part of `AIMProfile.spec`. Referenced secrets and service accounts must exist in the consuming namespace.

Neither profile derivation nor runtime projection infers compatibility from an image name or tag. In particular, setting the profile set's top-level `spec.image` changes the container image but does **not** inspect that replacement image or choose a new YAML contract. The replacement image must accept the source profile's inherited format.

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
  image: quay.io/acme/aim-base-vllm:0.9.0
  overrides:
    acceleratorModel: MI308X
    acceleratorCount: 4
    engineArgs:
      tensor-parallel-size: 4
      max-model-len: 32768
```

| Field | Description |
|---|---|
| `sourceRef` | Optional alternate catalog `ConfigMap`, mutually exclusive with `selector.modelRef`. If omitted, selects from model definitions and copyable profile resources using namespace/cluster scope rules. |
| `selector` | Match criteria — see [Selector](#selector). |
| `versionPolicy` | `pinned`, `latest`, `all` — see [Version policy](#version-policy). |
| `version` | Required when `versionPolicy: pinned`. |
| `image` | Optional image override applied to derived profiles. |
| `overrides` | Mutations applied to selected sources — see [Overrides](#overrides). |
| `imagePullSecrets` | Propagated to derived profiles. |
| `serviceAccountName` | Propagated to derived profiles. |

See the [API reference for AIMProfileSetSpec](../reference/api/v1alpha1.md#aimprofilesetspec) for the shared spec fields.

## Examples

### Derive across models on a partitioned cluster

This cluster profile set maps every matching unpartitioned, single-device TP=1 definition to one CPX-NPS4 partition. It selects across all cluster models and copyable direct cluster profiles, including on a cluster with only partitioned GPUs:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterProfileSet
metadata:
  name: mi300x-cpx
spec:
  selector:
    engine: vllm
    acceleratorModel: MI300X
    acceleratorCount: 1
    acceleratorPartitioningMode: unpartitioned
    engineArgs:
      tensor-parallel-size: 1
  versionPolicy: all
  overrides:
    acceleratorCount: 1
    acceleratorPartitioningMode: CPX-NPS4
```

`role` defaults to `deployable`. Add `modelId`, `aimId`, or other selector fields to narrow the selection. The source definitions remain available even when none has a materialised unpartitioned profile.

### Narrow to one model

Derive MI308X variants from MI300X definitions published by `llama-3-8b-official`:

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

### Alternate catalog

Derive from an explicitly maintained catalog ConfigMap named `team-runtime-catalog`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfileSet
metadata:
  name: qwen3-cache-derived
  namespace: ml-team
spec:
  sourceRef:
    name: team-runtime-catalog
  selector:
    aimId: qwen/Qwen3-32B
    acceleratorModel: MI300X
  versionPolicy: pinned
  version: "0.9.0"
  image: quay.io/acme/aim-base-vllm:0.9.0
  overrides:
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

`Ready` means that source evaluation and derivation succeeded and at least one current derived profile is ready. Profiles are alternatives: other selected profiles may be unavailable on this cluster, still reconciling, or failed without blocking a ready alternative. Their individual statuses and conditions remain unchanged, and `managedProfiles.ready` need not equal `managedProfiles.total`. A partially ready set reports `ManagedProfilesPartiallyReady` with its ready/total counts.

A profile contributes to readiness only when it is not being deleted, its spec and YAML contract match the desired output, and its status observes its current generation. An unreadable source or profile inventory, or an invalid derivation, still prevents readiness even if an older output is ready. An empty selection or a fully observed set with no ready profiles remains `NotAvailable`; missing or stale outputs with no ready alternative remain `Progressing`.

### Conditions

| Condition | Reasons |
|---|---|
| `DerivationReady` | `ManagedProfilesReady`, `ManagedProfilesPartiallyReady`, `NoMatchingProfiles`, `NoEligibleProfiles`, `Progressing`, `Degraded` |
| `Ready` | `AllComponentsReady`, `ComponentsNotReady`, `Progressing` |

`DerivationReady=False / NoMatchingProfiles` means source evaluation completed and the selector matched zero candidates. Pending discovery or an unreadable source is reported separately from a confirmed empty selection.

`DerivationReady=False / NoEligibleProfiles` means one or more profiles matched the selector and version policy, but all matching resource-backed profiles were excluded because they were not published as copyable.

## Derived-profile labels and annotations

Profiles produced by an `AIMProfileSet` carry this metadata:

| Kind | Name | Value |
|---|---|---|
| Annotation | `aim.eai.amd.com/profile-source` | `copy` |
| Annotation | `aim.eai.amd.com/profile-yaml-contract` | Inherited source codec, field presence, and runtime extensions |
| Label | `aim.eai.amd.com/profile-role` | `deployable` (or `base` if the derivation was authored to produce base profiles, which is unusual) |
| Label | `aim.eai.amd.com/profile-origin` | `derived` |
| Label | `aim.eai.amd.com/source-model` | Name of the AIM(Cluster)Model that owns the profile set (when applicable) |
| Label | `aim.eai.amd.com/source-model-scope` | `namespace` or `cluster` |

Derived profiles are non-copyable by default. Set `spec.outputPolicy.copyable: true` on a standalone profile set, or `spec.profiles.outputPolicy.copyable: true` on a model-managed derivation, to publish its outputs as eligible sources for a later derivation:

```yaml
spec:
  outputPolicy:
    copyable: true
```

For the compatibility `AIMModel.spec.profileCopy` shape, use `spec.profileCopy.outputPolicy.copyable: true`.

Copyability is live source policy, not a one-time export. Turning it off, deleting the producing set or model, or narrowing the producer's selection makes those profiles disappear from downstream source pools. Downstream sets then prune the copies that depended on them, which can remove profiles currently selected by running services.

## Source eligibility

Source eligibility and provenance are separate:

- Image-backed models publish their discovered definitions. Image-discovery-produced profile resources are copyable by default.
- A materialised model profile's explicit copyability opt-out applies to its logical catalog source as well. An owned, opted-out profile is retained when matching hardware disappears, preserving that policy. Removing its catalog entry or explicitly deleting the profile removes the opt-out; there is no separate persistent policy store.
- Direct profiles, including hand-authored and generated profiles, participate when marked copyable.
- Derived profiles participate only with explicit copyability opt-in. Prefer the producer's declarative `outputPolicy.copyable: true` over manually annotating generated profiles. Profile-set lineage prevents a set from consuming a descendant that was previously derived from that same set.
- `origin` filters preserve source provenance across catalog and resource representations.

To include a hand-authored profile in ordinary selector-based derivation:

```bash
kubectl -n ml-team annotate aimprofile my-profile \
  aim.eai.amd.com/profile-copyable=true
```

An explicit `sourceRef` selects entries from that catalog. Eligibility for model-derived default selection is resolved through the owning model and any corresponding live-profile opt-out.

## Reconciliation and resource use

Model, managed-catalog, and direct-profile changes reconcile affected sets, including sets whose sources have no materialised profile resources. Catalog identity includes the current model ownership and discovery inputs, so model recreation and operator restarts resolve the current source.

If discovery is pending or any source cannot be read or parsed, the entire set retains its outputs without applying or pruning profiles. Healthy-source progress waits for a complete source snapshot. A missing explicit catalog is also an unresolved dependency, not an empty catalog. Confirmed model deletion, removed catalog entries, and selector narrowing prune outputs once all remaining sources resolve. Namespace fallback is evaluated from a complete source result; an unavailable local source is not treated as an empty scope.

Each operator process shares parsed, immutable catalogs across reconcilers. Cached resource reads and scope/model indexes narrow source enumeration. Catalogs are parsed once per revision and relevant input state; only selected output definitions are copied for overrides.

Broad selectors still require work proportional to the candidate definitions they examine. Creating and reconciling outputs scales with the number of matches per set, so overlapping sets each incur their own output-resource cost. Hardware-unsupported raw definitions occupy catalog memory, without creating profile CRs, projected runtimes, or per-profile reconcile work.

The source pool includes complete image-backed definitions and existing copyable resources. RuntimeConfig fallback expansion remains part of generated-model onboarding. Each set applies one selector and one overrides block; it evaluates published definitions and opted-in resources rather than traversing a recursive derivation graph.

Standalone sets own their outputs and report their readiness. Source-model provenance alone does not add those outputs to the source model's managed counts. A model's child set reports its outputs through the owning model. Services resolve materialised profile resources.

## Troubleshooting

### `DerivationReady=False / NoMatchingProfiles`

The selector matched zero source candidates. Diagnose by listing candidates and comparing:

```bash
# What did the selector look for?
kubectl get aimprofileset <name> -o jsonpath='{.spec.selector}'

# Which materialised profiles can participate?
kubectl get aimprofile -n <namespace> -o custom-columns=\
NAME:.metadata.name,\
AIMID:.spec.aimId,\
PRECISION:.spec.precision,\
ACCEL:.spec.acceleratorModel,\
ROLE:.metadata.labels.aim\\.eai\\.amd\\.com/profile-role,\
COPYABLE:.metadata.annotations.aim\\.eai\\.amd\\.com/profile-copyable
```

Common causes:

- A selector field is too restrictive — relax `precision`, `acceleratorModel`, etc.
- `modelRef.name` typo or wrong scope.
- The requested version does not match, or a versionless source needs `versionPolicy: all`.

Also inspect the source models' `DiscoveryReady` conditions and `status.discoveredProfiles`. Resource listings show only materialised profiles; a model can supply definitions when that listing is empty.

### `DerivationReady=False / NoEligibleProfiles`

The selector and version policy matched one or more profile resources, but none were published as copyable.

- For a hand-authored source profile, add `aim.eai.amd.com/profile-copyable: "true"` to that profile.
- If the source is produced by another profile set or derived model and the chain is intentional, set `outputPolicy.copyable: true` on that producer. This makes the policy durable across reconciliation:

```yaml
spec:
  profiles:
    outputPolicy:
      copyable: true
```

Use `spec.outputPolicy` for a standalone `AIMProfileSet`, or `spec.profileCopy.outputPolicy` for the compatibility model shape. Do not patch generated profiles directly; manual annotations can obscure whether the producer intended its hardware overrides to be reused.

If producer copyability is later disabled, downstream copies are pruned as soon as their source disappears from the eligible pool. Check affected services before changing this policy.

### Source discovery or catalog is unavailable

Inspect the affected model's discovery conditions and `status.discoveryCacheRef`. For an explicit alternate catalog, verify `spec.sourceRef.name` in the set's namespace (or operator namespace for a cluster set). All existing outputs of the set remain in place while reconciliation retries.

### `sourceRef and selector.modelRef are mutually exclusive`

New requests combining these fields are rejected at admission. If an existing profile set contains both fields, reconciliation reports an invalid configuration and retains all derived profiles without applying or pruning them. Remove `selector.modelRef` to continue using the same explicit catalog. For model-managed derivation, make the correction in the parent model's `spec.profiles.derivedFrom`.

### `managedProfiles.total == 0` after applying

Inspect `status.conditions[?(@.type=='DerivationReady')]` to distinguish pending source discovery, source evaluation errors, and a completed selection with zero matches.

### Model-managed derivation is stuck

When an `AIMModel.spec.profiles` synthesises a profile set, the model's `status.profileSetRef` points at it. Inspect directly:

```bash
kubectl get aimmodel <name> -o jsonpath='{.status.profileSetRef.name}'
kubectl get aimprofileset <profileset-name> -o yaml
```

Conditions on the profile set carry the actionable detail; the model just summarises.

## Related documentation

- [AIM Models](models.md) — Derivation flows that synthesise profile sets
- [Profiles](profiles.md) — What derived profiles carry; provenance labels
- [Fine-Tuned Models](../guides/fine-tuned-models.md) — End-to-end derivation walkthrough (deployable sources)
- [Custom Models](../guides/custom-models.md) — End-to-end derivation walkthrough (base-profile sources)
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Conditions Reference](../reference/conditions.md) — Full condition catalogue
