# Composition concepts

A composition joins existing pod-local profiles into a complete model-serving
deployment. Use it when you need separate prefill and decode roles, or when you
want to serve a single profile through the same revisioned serving path.

`AIMProfileComposition` publishes a reusable recipe within a namespace.
`AIMClusterProfileComposition` publishes the same recipe for reuse across
namespaces. Each kind composes profiles into serving roles; it is not itself a
profile. Neither resource starts inference. An `AIMService` instantiates a named
recipe or supplies one inline.

## Roles and execution groups

An aggregated composition has a `main` role that handles the complete request.
A `PrefillDecode` composition has `prefill` and `decode` roles. Prefill computes
prompt state; decode uses that state to generate tokens.

Each role has a primary execution and optionally a cooperating `worker` shape.
The role's `profile`, `launch`, and `resources` configure the primary.
`worker.profile`, `worker.launch`, and `worker.resources` configure workers.
`replicas`, `scaling`, and `parallelism` describe complete execution groups.

A role replica is not a GPU count or a count of worker pods. Two decode replicas
mean two complete decode groups, each with all primary and worker executions
required by its supported parallelism mapping.

Profiles remain pod-local. They are not recipes and do not contain nested
compositions. The catalog classification `role: deployable` is unrelated to the
serving roles `main`, `prefill`, and `decode`.

## One interface, two authoring styles

Use inline composition for a service-specific shape. Use a named recipe to share
selection restrictions and role settings across services. Both forms resolve and
operate the same way.

Inline mode is inferred from roles: `main` means `Aggregated`; either `prefill`
or `decode` means `PrefillDecode`. The absent P/D counterpart still exists with
default settings. When neither role is authored, supply `mode`. An explicit mode
must agree with its roles.

A named reference supplies the topology. Consumer role fields customize that
topology; they cannot turn an aggregated recipe into a P/D deployment.

See [Deploy a composition](guide.md) for complete manifests.

## Selection restrictions and defaults

A primary or worker binding can name an exact profile or provide selector
criteria. Omitting the binding selects automatically using the applicable shared
criteria. It does not disable that execution position.

All authored restrictions apply together:

1. The recipe's shared selector.
2. Its selector for the particular primary or worker.
3. The service's shared selector, including `spec.model.name`.
4. The service's selector for that position.

These are intersections, not an override stack. Recipe `precision: fp8` and
consumer `precision: bf16` conflict. The consumer cannot broaden the recipe.
A worker does not inherit its primary's binding or primary-only selector.

Runtime-config selector defaults fill fields left unconstrained by all these
layers. They are defaults rather than mandatory policy: an explicit criterion
wins over a default, but cannot override a recipe restriction. Role defaults
apply to the primary and any worker in that role. See
[Administrator configuration](runtime-config.md).

An exact reference skips discovery and runtime selector defaults for that
position. It still must satisfy every authored restriction, deployable-profile
eligibility, and joint validation.

### Identity is not provenance

`modelId` filters the model identity declared by the profile. `modelRef` filters
the AIM model that published it. Supplying both requires both to match.

`spec.model.name: qwen3-32b` is a shortcut for a shared
`modelRef: {name: qwen3-32b}` restriction. It neither changes the selected weights
nor creates a model. A composition can instead use `modelId` or exact profile
references without requiring an AIMModel.

Matching model IDs alone does not establish equality of artifact revisions,
tokenizers, runtime behavior, or KV-transfer protocols. Those are checked where
the applicable integration contract supplies enough information.

## Joint resolution

AIM orders candidates with the existing profile-selection policy, including
namespace behavior, eligibility, optimization tier, ranking, and deterministic
tie-breaking. It searches in this position order:

- Aggregated: `main`, then `main.worker`.
- P/D: `decode`, `decode.worker`, `prefill`, then `prefill.worker`.

The first individually preferred profiles may not work together. AIM applies
launch settings and checks each complete combination, backtracking to the next
candidate when necessary. It selects the first jointly feasible result, not a
globally optimal throughput/latency combination.

The implicit optimized-tier floor remains. The existing explicit lower-tier
selection and eligible `autoSelectionPolicy: any` fallback remain available.
No performance scorer, benchmark gate, or ordered fallback-rule language is
introduced. Runtime defaults become filters once applied; AIM never silently
drops one to make a candidate match.

## Validation does not certify interoperability

| Finding | Outcome |
| --- | --- |
| Known contradiction under an applicable rule | Reject that tuple; try alternatives. |
| Missing information needed to construct the workload | Block and identify the missing input. |
| Constructible but not statically established interoperability | Accept with a visible unverified finding. |
| Observed runtime or KV-transfer failure | Report failure on the serving revision. |

A common engine, GPU, image tag, or precision is not proof of interoperability.
Generic precision does not imply KV dtype. Differences in tensor parallelism,
image tags, or block sizes are rejected only when a bounded rule establishes
incompatibility. There is no `allowUntested` switch: unverified interoperability
is already allowed when the workload is constructible and has no known
contradiction.

Initial bounded rules include the tested NIXL contradiction where
`VLLM_ROCM_USE_AITER=1` changes the expected KV layout, and the tested MoRI rule
requiring equal tensor parallelism across prefill and decode. These are
integration-specific rules, not universal P/D admission. A role launch overlay
can replace an inherited environment value with an empty string when the
qualified engine contract defines empty as disabled.

## Transport-driven placement

Placement is a requirement of the effective transport contract, not an inherent
property of P/D. TCP compatibility can permit `AnyNode`; an integration that
depends on local GPU access can require `SameNode`. Authored placement may narrow
the transport requirement but cannot weaken it. Omission uses the transport's
minimum.

When the effective scope is `SameNode`, it covers all groups and workers in a
service, including overlapping serving revisions. At least one common eligible
node must have enough allocatable capacity for all fixed replicas and, for each
autoscaled role, its minimum or one activation group, whichever is larger.

For example, one prefill group requesting two GPUs plus two decode groups
requesting one GPU each needs a common node with at least four compatible GPUs,
plus the combined CPU, memory, and supporting-container requirements.

This is not a reservation. A compatible but busy node can leave the service
waiting. Scaling maxima are ceilings, not reservations or a promise that all
maxima fit together. Pods wait rather than spill onto another node.

Hard co-location continues through independent role scaling, replacement,
scale-to-zero, and node loss. A transport that exposes pinned GPUs or cannot
safely admit old and new revisions together must use a non-surge strategy such
as `Recreate`, or reject the update. A platform mapping that cannot enforce the
selected transport's lifecycle is unsupported.

## Ownership and revision artifacts

```text
recipe + service + profiles + runtime configuration
                     ↓
           accepted content snapshot
                     ↓
     namespace-local revision dependencies
                     ↓
       LLMInferenceServiceConfig/revision
                     ↓
        LLMInferenceService.spec.baseRefs
                     ↓
              KServe workloads
```

AIM owns resolution, cache preparation, the snapshot and revision dependencies,
the generated config, the referencing native service, and the public
HTTPRoute/security contract. KServe owns inference workloads, endpoint-selection
resources, and role autoscalers.

Every accepted projection has one complete service-owned, namespace-local
`LLMInferenceServiceConfig`. The native service selects it through `baseRefs`;
AIM does not duplicate its managed workload settings inline.

Runtime profile YAML is materialized separately into namespace-local,
revision-addressed ConfigMaps, with the runtime's required filenames, mounts,
and environment. Cluster profiles do not authorize cross-namespace mounts.

## Boundaries

Composition is an explicit opt-in, fixed at service creation. Services without
`spec.composition` keep their existing `InferenceService` behavior. Moving between
paths requires a separate service.

The initial P/D contract does not qualify RDMA, gang scheduling, Grove, Dynamo,
atomic role rollout, automatic rollback, or guaranteed zero downtime. TCP
compatibility can place roles on different nodes, but it is not a high-performance
cross-node claim. KV transfer is distinct from intra-role collectives and request
or control traffic. Same-node placement or sharing `/dev/shm` alone does not
establish GPU IPC.
