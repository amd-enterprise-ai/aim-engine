# Composition CRD contract

This reference defines composition authoring and observed state. See
[Deploy a composition](guide.md) for complete manifests and
[Administrator configuration](runtime-config.md) for worked defaulting examples.

## Resources

| Kind | API version | Scope | Change |
| --- | --- | --- | --- |
| `AIMProfileComposition` | `aim.eai.amd.com/v1alpha2` | Namespace | New reusable recipe. |
| `AIMClusterProfileComposition` | `aim.eai.amd.com/v1alpha2` | Cluster | Same recipe contract, cluster publication. |
| `AIMService` | `aim.eai.amd.com/v1alpha2` | Namespace | Add `spec.composition` and `status.composition`. |
| `AIMRuntimeConfig` | `aim.eai.amd.com/v1alpha1` | Namespace | Add composition defaults and transport presets. |
| `AIMClusterRuntimeConfig` | `aim.eai.amd.com/v1alpha1` | Cluster | Same additions. |

The new recipe resources have a status subresource and ordinary Kubernetes
metadata. Their plurals are `aimprofilecompositions` and
`aimclusterprofilecompositions`. Lists use `AIMProfileCompositionList` and
`AIMClusterProfileCompositionList`. Print columns are `Mode`
(`status.resolvedMode`), `Model Binding` (`status.modelBindingType`), and `Age`.
Recipe publication starts no workload.

On AIMService, add a `Mode` column (`status.composition.mode`). Existing
single-profile and predictor replica columns remain empty for compositions;
inspect `status.composition.roles` for role-specific counts.

No new profile kind, public resolved-plan CRD, or changes to mandatory profile
fields are required. Runtime configs do not move to v1alpha2.

## Recipe specification

`AIMProfileComposition.spec` and `AIMClusterProfileComposition.spec` share these fields.
All are optional individually; mode must be supplied or inferable.

| Field | Type | Meaning |
| --- | --- | --- |
| `mode` | `Aggregated` or `PrefillDecode` | Serving topology. Inferred from roles if omitted. |
| `placement` | Placement | Optional minimum placement intent. Omission uses the selected transport's requirement. |
| `transport` | TransportReference | P/D transfer preset. Omit to use the runtime config's default. |
| `profileSelector` | ProfileSelector | Authored restrictions on every primary and worker binding. |
| `main` | Role | Aggregated role. |
| `prefill` | Role | Prompt-processing role. |
| `decode` | Role | Token-generation role. |
| `inheritSelectorDefaults` | boolean | Whether instances use runtime selector defaults. Effective default `true`; consumers may override. |

Recipes cannot contain `ref`, `refreshToken`, raw inline profiles, a nested model
declaration, or nested compositions.

### Mode and role validation

| Authored shape | Effective mode / result |
| --- | --- |
| `main: {}` without mode | `Aggregated`. |
| `prefill: {}` or `decode: {}` without mode | `PrefillDecode`; expand both roles. |
| `mode: Aggregated` without roles | Expand `main` with defaults. |
| `mode: PrefillDecode` without roles | Expand `prefill` and `decode` with defaults. |
| Only `profileSelector` or an empty inline object | Reject: mode cannot be inferred. |
| `main` together with `prefill` or `decode` | Reject. |
| Mode inconsistent with authored roles | Reject. |
| `Disaggregated`, `Auto`, or `serving.mode` | Not part of this contract. |

`Placement` contains optional `scope: AnyNode` or `SameNode`. An empty placement
object is the same as omission. The selected transport integration contributes
its own minimum: TCP compatibility can permit `AnyNode`, while a local GPU-access
integration can require `SameNode`. Authored placement may narrow that result
but cannot weaken it.

`TransportReference` requires `preset`, a non-empty name in the effective runtime
config's `composition.transportPresets`. Transport is forbidden for aggregated
recipes.

## AIMService composition

`spec.composition` selects the composition execution path. It is a union:

- **Inline:** the recipe fields above, plus service-only `refreshToken`.
- **Named:** the fields in the following table. `ref` is required.

| Named field | Type | Meaning |
| --- | --- | --- |
| `ref` | CompositionReference | Exact namespace or cluster recipe. |
| `placement` | Placement | Additional minimum placement; cannot weaken the recipe or selected transport. |
| `profileSelector` | ProfileSelector | Additional shared restrictions. |
| `main`, `prefill`, `decode` | Role | Customization of roles in the recipe's effective topology. |
| `inheritSelectorDefaults` | boolean | Overrides the recipe's default-inheritance choice. |
| `refreshToken` | string, 1–128 characters | Change to request a fresh resolution. |

Named form cannot include `mode` or `transport`. Reference lookup does not fall
back to a same-named object of another kind. A named placement is an additional
minimum and is rejected during resolution if it would weaken the recipe or
transport requirement. Adding a role outside the recipe's mode, or adding a
worker absent from that recipe, is also rejected during resolution. A P/D
recipe's omitted counterpart is still an existing role.

`CompositionReference` has exactly these required fields:

| Field | Type |
| --- | --- |
| `kind` | `AIMProfileComposition` or `AIMClusterProfileComposition` |
| `name` | Non-empty Kubernetes object name |

`AIMProfileComposition` resolves in the service namespace. Cross-namespace reference
fields are not accepted. Cluster publication does not confer tenant-data access.

### Top-level service fields

| Field | Composition behavior |
| --- | --- |
| `model.name` | Optional shared `modelRef.name` restriction, using `Auto` provenance scope. Intersects with all other restrictions. |
| `runtimeConfigName` | Existing name-based namespace/cluster merge. Omission selects `default`. |
| `routing` | Existing public route/security intent; rendered against the composition's native routing path. |
| `caching`, `storage` | Existing cache intent and storage defaults, applied to required artifacts across all positions. |
| `imagePullSecrets` | Namespace-local credentials available to all inference positions. |
| `serviceAccountName`, `priorityClassName` | Applied consistently to the composition's inference pods. |
| `adapterMode: static` | Accepted as the existing default, without adapters. |

The following top-level fields are not accepted with composition:

- `model.image`, or any model fields other than `name`;
- `profile`, `profileOverrides`, `template`, `overrides`;
- `replicas`, `minReplicas`, `maxReplicas`, `autoScaling`, `resources`;
- inference `env` and `scaleFromZero` (use role-aware launch/scaling);
- non-empty `adapters`, `adapterRuntime`, or `adapterMode: dynamic`.

The deprecated `cacheModel` field remains ignored. Empty `adapters: []` has no
effect. Do not rely on either when writing new services.

Composition takes precedence over migration-era pipeline annotations.
`status.reconcilerPipeline` is `composition`; `resolvedProfile`, legacy
predictor replica status, and `InferenceServiceReady` are not populated as though
one role represented the whole composition.

### Creation and update rules

The presence of `spec.composition` is immutable. Its contents are not generally
immutable: changing inline mode, the recipe reference, bindings, launch, resources,
or other execution inputs requests a new desired revision and undergoes transition
validation. A named consumer cannot override the referenced topology in place.

Changing `refreshToken` requests a new source resolution even if no other
execution fields changed. It is an opaque token, not a timestamp format or an
instruction to refresh on every reconciliation. The last processed value is
reported in status. Removing a token acknowledges nothing and does not request
refresh; change it to a new non-empty value for another request.

## Role and Worker

| Role field | Type | Effective default / semantics |
| --- | --- | --- |
| `profile` | ProfileBinding | Automatic binding from applicable shared restrictions. |
| `launch` | Launch | Inherit selected profile and applicable runtime environment. |
| `resources` | Kubernetes ResourceRequirements | Inherit selected profile's resources; overlay requests/limits per resource key. |
| `replicas` | integer ≥ 1 | One complete group if no inherited or explicit capacity is set. |
| `scaling` | Scaling | Mutually exclusive with `replicas`. |
| `parallelism` | Parallelism | Inherit a supported mapping of the selected profile's execution settings. |
| `worker` | Worker | Absent: no separately authored worker. Present: include the supported cooperating-worker shape. |

`Worker` contains only `profile`, `launch`, and `resources`, with the same types.
There is no independent worker replica count. An empty worker object requests
automatic worker selection; it is not removal syntax. If the bundle cannot map
the requested worker/parallelism shape, resolution fails before publication.

`Parallelism` contains optional positive integers `tensor` and `pipeline`.
These describe execution within each role group, not extra independent replicas.
Omitted values are derived from the profile and supported native mapping; AIM
does not rewrite a profile's tensor parallelism to `1` merely because this block
is absent. Authored values must agree with resources and effective launch.
Data/expert parallelism fields and multi-node execution are not exposed in this
initial contract.

### ProfileBinding

If `profile` is present, it contains **exactly one** of:

| Field | Type | Meaning |
| --- | --- | --- |
| `ref` | ProfileReference | Fix this binding to an exact resource. |
| `selector` | ProfileSelector | Add binding-local restrictions. An empty selector is valid. |

`profile: {}` is invalid; omit `profile` for automatic selection.
`ProfileReference` requires `kind: AIMProfile` or `AIMClusterProfile` and `name`.
No namespace field is allowed. Namespace refs in a cluster recipe are invalid;
a cluster recipe may use selectors that resolve tenant-local profiles when
instantiated, but it cannot publish an exact tenant-local reference.

A recipe's exact reference cannot be replaced with a different reference by a
consumer. A consumer selector can further constrain it; a consumer exact reference
can narrow a recipe selector. Every reference remains subject to all applicable
authored restrictions.

### ProfileSelector

The existing selector vocabulary is retained:

| Fields | Type / matching |
| --- | --- |
| `aimId`, `modelId`, `profileId` | String equality on the corresponding source identity. |
| `modelRef` | `{name, scope?}`; provenance scope is `Auto`, `Namespace`, or `Cluster`. |
| `engine` | String equality. |
| `metric` | `latency` or `throughput`. |
| `precision` | `auto`, `fp4`, `fp8`, `fp16`, `fp32`, `fp64`, `bf16`, `int4`, `int8`. |
| `type` | Exact tier: `optimized`, `general`, `preview`, `unoptimized`. |
| `minimumType` | Minimum tier, or `any` to disable the floor. |
| `acceleratorType` | `cpu` or `gpu`. |
| `acceleratorVendor` | `amd` or `nvidia`. |
| `acceleratorModel` | Accelerator identifier. |
| `acceleratorCount` | Integer resource-unit count. |
| `acceleratorPartitioningMode` | Existing partition scheme/partial-order match. |
| `engineArgs` | Object; supplied top-level keys must match source values. |
| `origin` | `discovered`, `derived`, `generated`, or `user-authored`. |
| `role` | Omitted or `deployable`; `base` is rejected for composition. |

`modelRef.name` is required when `modelRef` is present. `Auto` uses the existing
fully matching namespace-first, cluster-fallback policy. A cluster recipe may
express a namespace provenance restriction evaluated in each consumer's namespace.

Composition does not require the legacy service selector's `aimId`/`modelRef`
index anchor: `modelId`-only and model-agnostic recipes are supported. Broad
selectors still must resolve to a jointly consistent artifact identity.

Intersections are semantic predicates, not simply field overwrites. Floors combine
by taking the stricter bound; exact type and floor both apply. Partition restrictions
retain partial-order semantics. `engineArgs` criteria union distinct keys and
conflict on unequal values for the same key. Compatible `Auto` and explicit scope
restrictions narrow provenance rather than replacing the named model.

### Launch

| Field | Type | Merge |
| --- | --- | --- |
| `containerEnv` | List of Kubernetes EnvVar | By variable name; supplied entry replaces the entire inherited entry. |
| `engineEnv` | Map of string to string | By key. |
| `engineArgs` | JSON object | Shallow by top-level key; a supplied nested value replaces the whole nested value. |

Start with the selected profile, apply the effective runtime config's inference
`env`, then recipe launch, then service role launch. Only `containerEnv` receives
the common runtime `env`; engine fields follow profile → recipe → service.
Runtime env applies to inference positions, not blindly to connector sidecars.

Arrays of environment variables have map-list semantics keyed by `name`.
Duplicate names within one authored list are invalid. No overlay deletion syntax
is provided: omission inherits; empty maps/lists do not clear inherited entries;
an empty string is an actual value, not deletion. Null is not removal syntax.

Launch cannot change profile identity, weights, image, or hardware identity.
Controller-owned connector and platform requirements are constraints; conflicting
user settings are rejected, not silently overwritten.

### Scaling

| Field | Type | Effective default |
| --- | --- | --- |
| `minReplicas` | integer ≥ 0 | `1`. |
| `maxReplicas` | integer ≥ 1, required | No default. Must be ≥ effective minimum. |
| `metrics` | List of existing `AIMServiceMetricsSpec` | No implicit utilization metric. |
| `pollingInterval` | integer seconds ≥ 1 | Supported native integration default. |
| `cooldownPeriod` | integer seconds ≥ 0 | Supported native integration default. |

At least one non-empty user metric is required unless `minReplicas: 0` enables
the supported request-activation path. Zero minimum still needs a valid public
route and a role-aware activation mapping. If the installed integration cannot
activate all required roles, it rejects the configuration.

Metrics retain the existing `PodMetric`/`podmetric` shape and OpenTelemetry
backend. Targets and queries must be valid for the selected role. The
implementation translates them to KServe-owned native KEDA scaling, not an
AIM-owned predictor ScaledObject.

The unit is complete execution groups. A zero minimum does not remove that role
from topology or admission-footprint checks.

## Inheritance and defaulting

| Setting | Rule |
| --- | --- |
| Authored selectors | Intersect across all applicable layers. |
| Exact references | Fixed candidate, further constrained by other layers. |
| Role capacity | Consumer `replicas` or `scaling` replaces the recipe's entire capacity choice. Neither supplied means inherit. |
| `scaling` | A supplied block replaces, not partially merges, inherited scaling. Its own omissions use effective defaults. |
| `parallelism` | Consumer supplied dimensions override recipe dimensions; omissions inherit. |
| Launch | Profile → runtime env → recipe → service, using the field-specific merges above. |
| Resources | Profile → recipe → service, per request/limit resource key; validate final shape. |
| `inheritSelectorDefaults` | Service value → recipe value → `true`. |
| Runtime selector defaults | Fill unconstrained fields only, after authored restrictions. |
| Transport | Inline/recipe preset, otherwise effective runtime-config default for P/D. |

Do not store inheritance-sensitive defaults in service or recipe specs via
unconditional API-server defaults. In particular, a named consumer must not
acquire `replicas: 1`, `mode`, `inheritSelectorDefaults: true`, or selector values
before the recipe is resolved. Effective defaults are recorded in the accepted
revision and status.

## Runtime-config additions

Both runtime-config kinds add `spec.composition`:

| Field | Type | Semantics |
| --- | --- | --- |
| `selectorDefaults` | SelectorDefaults | Gap-filling criteria for automatic bindings. |
| `defaultTransportPreset` | Non-empty string | Default P/D transport preset name. |
| `transportPresets` | List of TransportPreset | Map-list keyed by `name`, with at most 64 entries; namespace entry replaces a same-named cluster entry as a whole. |

`SelectorDefaults` contains optional `common`, `main`, `prefill`, and `decode`
objects. Each accepts only `engine`, `metric`, `precision`, `acceleratorType`,
`acceleratorVendor`, `acceleratorModel`, `acceleratorPartitioningMode`, and
`acceleratorCount`, using the selector types above.

Identity, provenance, `engineArgs`, eligibility tier, and catalog role are not
runtime-default fields. This keeps defaults distinct from mandatory model policy,
launch overlays, and fallback generation.

`TransportPreset` requires `name` and `integration`, both non-empty strings.
`integration` identifies a registered, versioned KV-transfer integration
contract in the installed bundle. The contract supplies its connector, protocol,
images, launch inputs, permissions, placement requirements, and bounded
validation rules. Presets do not accept arbitrary native pod/config overlays.

No new runtime-config status fields are needed. Existing config health remains;
effective contributions and transport resolution are reported on each service.

## Recipe status

| Field | Type / meaning |
| --- | --- |
| `observedGeneration` | Last observed recipe generation. |
| `observedSpecDigest` | Digest of authored recipe content. |
| `resolvedMode` | `Aggregated` or `PrefillDecode` after local shape validation. |
| `modelBindingType` | `ModelAgnostic`, `ModelSpecific`, or `Unknown`. |
| `conditions` | Standard metav1 conditions, map-list keyed by `type`. |
| `findings` | List of Finding records. |

Classification examines all authored restrictions and exact refs. A model
restriction or a reference to a model-bound profile makes a recipe model-specific.
Unresolved references can make it unknown. A catalog containing only one matching
model does not make a generic recipe specific. Consumer restrictions do not
modify recipe status.

`RecipeValid` reports local shape/restriction validity;
`ReferencesResolved` reports exact-reference availability. `Ready` means the
recipe is usable for instantiation, not that it has a feasible tuple in every
namespace or has started workloads. Classification is independent of validity.

## AIMService status additions

Existing top-level `observedGeneration`, `status`, `conditions`, and `routing`
remain. `status.composition` contains:

| Field | Type / meaning |
| --- | --- |
| `mode` | Effective applied mode, or desired mode before first apply. |
| `observedRefreshToken` | Last refresh request evaluated for the desired intent. |
| `desired` | RevisionAttempt for the latest evaluated service intent. |
| `prepared` | RevisionProjection whose snapshot, dependencies, and generated config are ready to publish. |
| `applied` | RevisionProjection currently selected by the native service. Not a claim of healthy rollout. |
| `bindings` | BindingStatus list for the applied revision, or prepared revision before first apply. |
| `roles` | RoleStatus list, map-list keyed by `name`. |
| `transport` | TransportStatus for the applied revision. |
| `findings` | Finding list; every entry identifies its relevant revision/intent. |

Before a stage exists its field is absent. A rejected desired revision never
replaces the applied binding records. Desired findings and attempt identity make
the rejected request separately inspectable.

### RevisionAttempt and RevisionProjection

| RevisionAttempt field | Meaning |
| --- | --- |
| `intentDigest` | Digest of authored service intent and refresh request; exists even if resolution fails. |
| `observedGeneration` | Service generation being evaluated. |
| `state` | `Resolving`, `Rejected`, or `Accepted`. |
| `revision` | Effective-content digest; present only after successful resolution. |
| `reason`, `message` | Diagnostic for this attempt. |

A rejected selection need not have an effective-content revision. Do not invent
one for nonexistent resolved content; compare its intent digest with the applied
revision instead.

| RevisionProjection field | Meaning |
| --- | --- |
| `revision` | Effective-content identity of this complete projection. |
| `intentDigest` | Originating desired intent. |
| `executionDigest` | Selected execution content excluding capacity-only projection changes. |
| `snapshotRef` | Namespace-local persisted snapshot reference. |
| `configRef` | Namespace-local generated `LLMInferenceServiceConfig` reference. |
| `configDigest` | Content digest of the generated config. |
| `nativeServiceRef` | Referencing `LLMInferenceService`; present once published. |
| `bundleDigest` | Installed controller/CRD/preset/runtime/connector contract identity. |
| `recipeSource` | Named recipe source reference and digest; absent for inline authoring. |
| `recipeDigest` | Effective recipe-content digest, also present for inline authoring. |
| `runtimeConfigSources` | References and content digests of all contributing runtime configs. |
| `dependencies` | Reference/digest list of generated profile YAML, cache/artifact, and external dependencies. |

Digests are algorithm-qualified strings, such as `sha256:<hex>`. Projection
references (`snapshotRef`, `configRef`, `nativeServiceRef`, `runtimeProfileRef`)
use an object containing required strings `apiVersion`, `kind`, `name`, `uid`,
and `namespace`. Each points into the service namespace.

A source record contains required strings `apiVersion`, `kind`, `name`, `uid`,
`resourceVersion`, and `contentDigest`, plus `namespace` for namespace-scoped
sources. A dependency record contains an object `ref` with that object's identity,
required booleans `managed` and `mutable`, and optional `contentDigest` when AIM
can safely establish it. Secret **contents are never copied into status or
snapshots**; record references and non-secret provenance only.

`runtimeConfigSources`, `dependencies`, `bindings`, `selectorContributions`,
and `findings` are atomic lists in the status schema. `roles` is a map-list keyed
by `name`, and condition lists are keyed by `type`. Replica counters and observed
generations are non-negative integers; timestamps use the Kubernetes date-time
format. `reason`, `message`, `sourcePath`, and `rule` are strings.

Snapshot storage is an internal implementation concern, not another public CRD.
It must persist full effective content and dependency identities, survive source
deletion, and handle API object-size limits without truncating accepted content.

### BindingStatus, RoleStatus, TransportStatus, Finding

| Record | Fields |
| --- | --- |
| BindingStatus | `position` (`main`, `main.worker`, `decode`, `decode.worker`, `prefill`, `prefill.worker`); `revision`; `profile` source record; `effectiveProfileDigest`; `effectiveSelector`; `selectorContributions`; optional `runtimeProfileRef`. |
| SelectorContribution | `path` of the effective selector field; `source` (`Recipe`, `Service`, `ModelShortcut`, `RuntimeCommon`, `RuntimeRole`, `BuiltinEligibility`); optional `sourceRef`; `sourcePath`; `value`. Record all applicable authored contributors, not only the final scalar. |
| RoleStatus | `name`; `revision`; `currentReplicas`, `desiredReplicas`, `readyReplicas`; optional `minReplicas`, `maxReplicas`. Counts are groups, not pods. |
| TransportStatus | `state` (`NotApplicable`, `Unobserved`, `Observed`, `Failed`); `revision`; optional `preset`, `integration`, `reason`, `message`, `lastObservedTime`. |
| Finding | `category` (`Contradiction`, `MissingInput`, `Unverified`, `Drift`, `RuntimeFailure`); `reason`; `message`; optional `position`, `rule`, `sourcePath`, `intentDigest`, `revision`. |

The persisted snapshot additionally records effective launch, images,
model/artifact identities, resources, placement, transport, applied rule versions,
and generated YAML. Status is a bounded summary; it need not duplicate that full
content. Truncated diagnostic lists must disclose truncation and retain a path to
the full non-secret record.

## Admission and reconciliation

| Boundary | Checks |
| --- | --- |
| Structural schema | Known fields, types/enums, non-empty refs/tokens, positive sizes, no inline raw profile definitions. |
| Admission/CEL | Profile ref XOR selector, role/mode shape, named vs inline union, replicas XOR scaling, cluster recipe exact-ref scope, metric structure, forbidden legacy combinations. |
| Update admission | Composition presence immutable; legacy-only defaults and old API-version rules preserved. |
| Recipe reconciliation | Reference availability, authored contradictions, classification. No workload creation. |
| Service resolution | Named recipe topology, semantic selector intersection, profile eligibility/ranking, effective defaults, artifact identity, resource/parallelism agreement, transport construction, storage access, and effective-placement feasibility. |
| Refresh validation | Known-unsafe mixed generations rejected unless the native rollout prevents mixing. |
| Bundle conformance | Effective post-preset native configuration, role-specific parallelism mapping, transport-required placement lifecycle, routing/security, scaling, profile YAML and storage mounts. |
| Runtime observation | Native health, routing, pending capacity, and observed transfer, fallback, lease failure, or recomputation. |

Unknown fields are excluded from the structural schema. Strict field validation
rejects them; clients must not depend on a server's permissive pruning behavior.
The review schema intentionally rejects unknown new fields to catch authoring
mistakes early.

For the existing multi-version AIMService CRD, composition admission cannot be
bypassed through v1alpha1. The shared spec/status representation and conversion
strategy require equivalent safety checks on both served versions. v1alpha2 is
the documented authoring version. Non-composition v1alpha1/v1alpha2 objects retain
their current behavior.

The unconditional legacy top-level `replicas: 1` structural default must be
replaced with composition-aware defaulting while preserving its existing
non-composition result. Merely removing the default or ignoring an injected
`replicas` field is not sufficient.
