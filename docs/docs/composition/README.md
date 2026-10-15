# Composition contract review

This is the review surface for ADR-0012, **not a feature release**. The following
pages deliberately use final-product language so reviewers can evaluate the
interface users will actually see.

The first implementation slice now adds installable `AIMComposition` and
`AIMClusterComposition` CRDs plus runtime-config selector defaults and transport
preset fields. It does **not** add `AIMService.spec.composition`, recipe
reconciliation, profile resolution, or workload projection. No composition can
start an inference workload in this slice.

The proposal is based on `main` at
`3f585b07c3d7fa215d6ceed20190a809e82dbe11` and the October 15, 2026
amendment of ADR-0012 in
`EnterpriseAI-specs/AIM-Engine/adrs/0012-llm-d-kserve-integration/`.
It incorporates the subsequent authoring/defaults discussion. It does not modify
that separate specifications repository.

## Read in this order

1. [Deploy a composition](guide.md) — the practical authoring experience.
2. [Composition concepts](concepts.md) — roles, bindings, selection, and ownership.
3. [CRD contract](api.md) — new field shapes, inheritance, validation,
   status, and changes to existing CRDs.
4. [Administrator configuration](runtime-config.md) — selector defaults and
   KV-transfer presets.
5. [Operations and troubleshooting](operations.md) — revisions, refresh,
   scaling, readiness, and failure cases.
6. [Implementation outline](implementation.md) — code seams and acceptance work.

The [authoring schema](schema.yaml) and [example index](examples.md) accompany
the prose. The schema remains a **review-only JSON Schema** for the complete
contract, including the service fields that are not implemented yet. Production
recipe and runtime-config admission is generated from Go types and covered by
generated-CRD/CEL tests. Service transition rules and cross-object resolution
remain implementation work.

## Decisions captured from the discussion

| Concern | Contract |
| --- | --- |
| Authoring location | Keep `spec.composition`; do not introduce `spec.serving`. |
| Primary execution | Put `profile`, `launch`, and `resources` directly on each role; remove `template`. |
| Binding | Keep the `profile.ref` / `profile.selector` union. Omit `profile` for automatic selection. |
| Mode | Use `PrefillDecode`, not `Disaggregated`; infer mode from inline roles when possible. |
| Model shortcut | Allow only `spec.model.name` alongside composition; it adds a shared provenance restriction. |
| Defaults | Put common and role selector defaults in runtime configs; apply them only to unconstrained fields. |
| Opt-out | `composition.inheritSelectorDefaults: false` skips selector defaults, not platform requirements. |
| Placement | Derive the minimum from the selected transport. TCP does not become `SameNode` merely because the service uses P/D. |
| Earlier proposal | ADR-0012 supersedes ADR-0008's `spec.llmServing`, per-profile projection, and `AIMInferenceFabric` composition surface. |
| Legacy isolation | Composition presence is immutable. No changes to non-composition behavior. |

Allowing the model-name shortcut amends the ADR's blanket rejection of top-level
`model`. The final contract must be synchronized back to the ADR before
implementation is considered approved. The reviewed `PrefillDecode` wire name
also replaces the design contract's earlier `Disaggregated` spelling.

## Concrete choices proposed for review

These details make the examples and reference self-consistent; they are proposals
for team review, not claims of earlier agreement:

- Exact references require `kind` and `name`, without cross-namespace access.
  Cluster recipes can bind exact cluster profiles or use selectors evaluated in
  the consumer namespace; they cannot hold exact namespace-profile references.
- Named consumers cannot override mode or transport, or add a worker absent from
  the recipe. They can narrow placement, narrow profile selection, and customize
  existing roles. Changing the referenced recipe is an explicit execution update.
- `refreshToken` is a service-only, edge-triggered refresh request. Leaving a
  token in place does not continually reselect profiles.
- Consumer capacity replaces the recipe's entire `replicas`/`scaling` choice.
  Selector fields do not use replacement semantics.
- Initial parallelism fields are `tensor` and `pipeline`. Worker counts and
  asymmetric native prefill/decode fields are derived by the qualified bundle
  adapter, not a second user replica count.
- Transport presets name a registered integration contract; they do not embed
  arbitrary pod templates or user-selected native `baseRefs`.
- Top-level inference `env` and LoRA adapter configuration are excluded from
  composition in this first contract. Role launch settings remain explicit.
- Status distinguishes desired intent, prepared projection, applied projection,
  and observed health. A rejected refresh can coexist with a healthy applied
  service.

## Not established by this package

There is no qualified controller/CRD/preset/Gateway/engine/sidecar/connector
bundle or demonstrated AMD KV transfer in this change. The guide's profile,
transport, and integration names are example catalog entries, not published
images or a supported-version matrix.

Platform work must establish the first concrete transport contract, its minimum
placement, role mapping, security requirements, fallback behavior, and runtime
signals. TCP compatibility must remain schedulable without false co-location.
A transport that requires `SameNode` must enforce it through scaling,
replacement, surge strategy, and node loss rather than downgrade it to preferred
placement. Neither matching YAML nor schema validation proves runtime
interoperability, actual KV movement, or an advantage over tuned aggregated
serving.

## Reviewing and checking the package

From the repository root:

```bash
python3 -m unittest discover -s docs/contracts -p 'test_*.py' -v
mise exec -- make docs-build
```

The contract tests need PyYAML and jsonschema, listed in
`docs/contracts/requirements.txt`. They validate examples, strict new field
shapes, local bounds/unique-name rules, and representative invalid objects. They
do not simulate a resolver, admission server, or KServe; unchanged embedded
Kubernetes/service types are checked only at their outer shape.

Review the user-facing vocabulary and behavior before enabling service
composition. In particular, approve the remaining choices above and the
[admission boundaries](api.md#admission-and-reconciliation).
