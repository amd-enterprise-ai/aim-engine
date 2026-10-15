# Composition contract review

This is the review surface for ADR-0012, **not a feature release**. The following
pages deliberately use final-product language so reviewers can evaluate the
interface users will actually see. The operator, installable CRDs, and Helm chart
in this branch do not implement these additions.

The proposal is based on `main` at
`3f585b07c3d7fa215d6ceed20190a809e82dbe11` and the October 12, 2026
ADR-0012 design contract in
`EnterpriseAI-specs/AIM-Engine/adrs/0012-llm-d-kserve-integration/`.
It incorporates the subsequent authoring/defaults discussion. It does not modify
that separate specifications repository.

## Read in this order

1. [Deploy a composition](guide.md) — the practical authoring experience.
2. [Composition concepts](concepts.md) — roles, bindings, selection, and ownership.
3. [CRD contract](api.md) — new field shapes, inheritance, validation,
   status, and changes to existing CRDs.
4. [Administrator configuration](runtime-config.md) — selector defaults and
   local transport presets.
5. [Operations and troubleshooting](operations.md) — revisions, refresh,
   scaling, readiness, and failure cases.
6. [Implementation outline](implementation.md) — code seams and acceptance work.

The [authoring schema](schema.yaml) and [example index](examples.md) accompany
the prose. The schema is a **review-only JSON Schema**, not an installable
Kubernetes CRD. It checks the new authoring shapes and examples without enabling
an unsupported execution path in the production CRDs. Kubernetes admission,
transition rules, and cross-object resolution need their own tests during
implementation.

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
| Legacy isolation | Composition presence is immutable. No changes to non-composition behavior. |

Allowing the model-name shortcut amends the ADR's blanket rejection of top-level
`model`. The final contract must be synchronized back to the ADR before
implementation is considered approved.

## Concrete choices proposed for review

These details make the examples and reference self-consistent; they are proposals
for team review, not claims of earlier agreement:

- Exact references require `kind` and `name`, without cross-namespace access.
  Cluster recipes can bind exact cluster profiles or use selectors evaluated in
  the consumer namespace; they cannot hold exact namespace-profile references.
- Named consumers cannot override mode, placement, or transport, or add a worker
  absent from the recipe. They can narrow profile selection and customize existing
  roles. Changing the referenced recipe is an explicit execution update.
- `refreshToken` is a service-only, edge-triggered refresh request. Leaving a
  token in place does not continually reselect profiles.
- Consumer capacity replaces the recipe's entire `replicas`/`scaling` choice.
  Selector fields do not use replacement semantics.
- Initial parallelism fields are `tensor` and `pipeline`. Worker counts are
  derived by the supported native mapping, not a second user replica count.
- Transport presets name a registered integration contract; they do not embed
  arbitrary pod templates or user-selected native `baseRefs`.
- Top-level inference `env` and LoRA adapter configuration are excluded from
  composition in this first contract. Role launch settings remain explicit.
- Status distinguishes desired intent, prepared projection, applied projection,
  and observed health. A rejected refresh can coexist with a healthy applied
  service.

## Not established by this package

There is no qualified controller/CRD/preset/Gateway/engine/sidecar/connector
bundle or demonstrated local AMD KV transfer in this change. The guide's profile,
transport, and integration names are example catalog entries, not published
images or a supported-version matrix.

Platform work must establish the first concrete transport contract and hard
same-node mapping, including scaling, replacement, surge, and node loss. A
supported shape must enforce those requirements; it cannot downgrade them to
preferred placement. Neither matching YAML nor schema validation proves runtime
interoperability.

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

Review the user-facing vocabulary and behavior before translating this schema
into Go types and generated CRDs. In particular, approve the remaining choices
above and the [admission boundaries](api.md#admission-and-reconciliation).
