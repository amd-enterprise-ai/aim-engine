# Implementation outline

This page is the engineering handoff for the contract review. Unlike the product
guides, it describes work still to implement. No controller, production API type,
installed CRD, or chart change is included in the review package.

## Keep three deep modules

| Module | Interface | Hidden implementation |
| --- | --- | --- |
| Composition resolution | Authored recipe/service, source catalog, runtime configuration, node/bundle capabilities → validated effective execution or structured findings | Inheritance, selector intersections/defaults, deterministic tuple search, overlays, identity and capacity checks, bounded interoperability rules. |
| Native projection | Accepted execution plus projection intent → ordered revision artifacts and native config/service | Profile YAML contract, caches/storage, native field mapping, preset interaction, placement, routing, scaling. |
| Revision lifecycle | Desired intent, persisted accepted content, and observations → publication/retention decisions and status | Refresh semantics, projection vs execution digests, crash recovery, source deletion, mixed-generation checks, garbage collection. |

The resolution interface is the main test surface. It must never return a
renderable tuple that has bypassed joint validation. Native KServe details belong
at the projection seam, not in the public selector or recipe vocabulary.

## 1. Translate the reviewed contract into CRDs

Add shared composition types where the current version dependency permits them,
then v1alpha2 `AIMComposition` / `AIMClusterComposition` root/list types. Extend the
shared service spec/status and runtime-config common types.

Relevant existing locations:

- `api/v1alpha1/aimservice_types.go`: shared service spec/status and legacy defaults.
- `api/v1alpha2/aimservice_types.go`: v1alpha2 wrapper and root admission rules.
- `api/v1alpha1/profile_derivation_shared.go`: existing selector vocabulary.
- `api/v1alpha1/aimruntimeconfig_shared.go`: runtime-config common fields.
- `api/v1alpha2/profile_admission_test.go`: generated-schema/CEL test pattern.
- `config/crd/kustomization.yaml`, API registration, RBAC, chart CRD packaging,
  and generated API references.

Do not add runtime-only defaults to `AIMServiceRuntimeConfig`: that type is
embedded in services and would leak administrative composition defaults into
the wrong authoring surface.

Replace unconditional top-level replica defaulting with path-aware behavior.
Preserve the existing non-composition stored/defaulted result. Mirror transition
safety across served API versions because AIMService currently shares a spec and
uses conversion strategy `None`.

Generate, do not hand-edit production CRDs:

```bash
mise exec -- make generate
mise exec -- make manifests
```

Acceptance: valid/invalid authoring, update transitions, field pruning/strict
validation, old API-version paths, and defaulting tested against generated CRDs.
Publishing a recipe creates no workload. Until the serving implementation is
enabled, composition services must remain explicitly unsupported/pending; they
must never accidentally dispatch through legacy serving.

## 2. Implement inheritance and resolution

Use `internal/v1alpha1/aimruntimeconfig/observe.go` for the selected-name merge.
Extend it deliberately for selector-default field merge and whole transport-entry
replacement.

Reuse profile selection/ranking from `internal/v1alpha2/aimprofile/rank.go` and
`internal/v1alpha2/aimservice/resolver.go`, but do not reuse sticky source refs as
a content-pinning implementation.

Build per-position authored predicates, then fill runtime defaults. Preserve
provenance of every constraint. Exact bindings skip defaults but not validation.
Rank candidates with the current policy and search tuples in the contracted order.

Acceptance cases include:

- Omitted profile, empty selector, exact ref, missing ref, namespace/cluster scope.
- Identity vs provenance, recipe/service contradictions, type floors and
  partition partial-order intersection.
- Common/role and cluster/namespace defaults; explicit override and opt-out.
- Exact bindings bypassing defaults but failing explicit restrictions.
- Unconstrained model-agnostic recipe classification independent of catalog size.
- First tuple fails; later tuple succeeds deterministically.
- Constructible/unverified vs contradictory vs missing-input outcomes.
- Whole-service admission footprint, busy capacity, and unsupported mapping.

## 3. Persist accepted content before serving

Implement effective-content snapshots with a versioned internal serialization,
content digests, bounded status summaries, and a way to handle large snapshots.
Record source content separately from customized runtime YAML and optimization
claims. Never copy Secret contents into snapshots/status.

Distinguish `executionDigest` from the complete projection revision. Capacity-only
changes reuse accepted execution content while emitting a new immutable native
config if needed. Scaling must work after source deletion without reselection.

Acceptance: restart at every publication boundary; missing/changed sources;
idempotent refresh tokens; rejected desired state with healthy applied state;
retention of dependencies used by old/replacement workloads.

## 4. Deliver aggregated serving first

Add composition-first dispatch in `internal/aimservice/pipeline.go` and the
canonical controller path in `internal/controller/aimservice_controller.go`.

Reuse the existing runtime-profile YAML contract in
`internal/v1alpha2/serving/profile_runtime.go`. Preserve filename, mount path,
environment, and entrypoint expectations. Materialize cluster profiles in the
service namespace; avoid mutating/reusing mutable shared YAML for pinned content.

Create one complete revisioned `LLMInferenceServiceConfig` and a thin
`LLMInferenceService` containing its managed `baseRefs` entry. Compare an
aggregated exact-ref service and selector service against equivalent legacy
services; legacy creates only `InferenceService`.

The apply utility in `internal/controller/utils/apply.go` sorts flat batches by
GVK and may aggregate errors. A flat batch is not the required dependency →
config → native-service publication protocol. Introduce explicit durable phases
and make each idempotent.

Acceptance: effective post-preset workload inspection, profile/config dependencies,
cache/credential mounts, authenticated routing, group scaling, owner collision
handling, and legacy regression.

## 5. Establish and add the local P/D integration

Choose a reproducible installed bundle, not just the API version inspected by
the ADR. Register one concrete local-transfer contract with exact versions,
obtainable artifacts, GPU topology, permissions, launch settings, and bounded
rules. The `integration` name in example presets is not a bundle choice.

Map decode to native top-level workload fields and prefill to native prefill
fields within the generated config. Verify AIM owns the public route/security
surface while KServe owns its endpoint-selection resources and role scalers.
Do not reuse predictor names, route backends, scaler ownership, or storage
initializer behavior by assumption.

Prove hard co-location through bootstrap, independent scaling, replacement,
surge, coexisting revisions, scale-to-zero/reactivation, and node loss.
An inability to enforce this blocks the shape; affinity syntax alone is not
acceptance evidence.

Check old/new cross-generation combinations before publishing a refresh.
Reject known-unsafe mixing if the native rollout cannot isolate it.

## 6. Exercise a reference recipe and promote the docs

Freeze one reference manifest, model/artifact revision, profiles, exact bundle,
hardware, prompts, and allowed differences. Compare generated config and
post-preset workloads to that reference.

Collect distinct evidence for render equivalence, inference correctness,
authorized routing/streaming/cancellation, and intended local KV movement.
Test replacement, scaling, source deletion, crash recovery, and dependency
retention. No performance, multi-node, or RDMA claim follows from this test.

After team approval, synchronize refinements to ADR-0012. After implementation
and conformance, move the product pages into normal concepts/guides/admin/reference
navigation, generate the real API reference, and remove the review-only entry
point/schema checker once production admission tests cover the contract.

## Review-package verification boundary

The supplied JSON Schema and Python tests check authoring examples and new local
field shapes. They deliberately do not implement selector matching, mutable
reference lookup, admission transition behavior, generated-CRD defaulting, or
native execution. Those are acceptance tests above, not functionality proved by
this documentation change.
