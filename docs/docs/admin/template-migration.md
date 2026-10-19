# Bounded v1alpha1 template migration

This runbook describes the local `aim-template-migration` Plan/Apply utility.
It is a controlled-development tool for disposable, non-production resources;
it is not release authorization or evidence that a workload serves inference.

## Prerequisites

Install the CRDs and operator from the same bridge build, in that order, as
described in [Upgrading](upgrading.md#migration-window). Before planning,
verify that the selected legacy services still use the template pipeline and
that their existing KServe workloads are Ready:

```bash
kubectl get aimservice -A
kubectl get inferenceservice -A
kubectl describe aimservice -n <namespace> <service>
```

Record the service, `InferenceService`, template cache, artifact, and PVC UIDs
outside the repository. Stop if predecessor serving is not healthy or the
bridge reports compatibility warnings.

Build the utility without creating a repository-root binary:

```bash
go build -o /tmp/aim-template-migration ./cmd/aim-template-migration
```

## Plan and review

Select every namespace explicitly. Plan does not infer a cluster-wide scope,
and the output directory must not already exist:

```bash
/tmp/aim-template-migration plan \
  --namespace team-a \
  --namespace team-b \
  --runtime-projection-mode Both \
  --output-dir ./migration-plan
```

Set `--runtime-projection-mode` to the controller deployment's exact
`Exhaustive`, `Reduced`, or `Both` value. The flag is required because the
utility cannot safely discover a process flag through the Kubernetes API.
`Reduced` plans every unit as manual because it does not publish the
per-profile runtime required by the pre-cutover fence.

The directory is mode `0700`; `backup.yaml`, `plan.json`, and any Dedicated
handoff evidence JSON files are mode `0600`. Keep every file private. Plan
prints each evidence path and the stable manual-handoff section reference only
when Dedicated evidence exists. The backup is best-effort predecessor desired
state: it does not contain Secret objects, Secret values, artifact bytes, UIDs,
resource versions, status, or restorable owner identities. Back up referenced
credentials separately using the organization-approved Secret process.
Secret-looking literals make a unit manual, but sanitization is not a
credential-discovery guarantee.

Review every unit, warning, imputation, pinned identity, and proposed
operation in `plan.json`. Automatic handling is intentionally narrow. Edit a
unit only when the administrator owns and understands the resulting
semantics; Apply validates structural safety and current identity, not the
equivalence of arbitrary edits. Leave custom, ambiguous, override-bearing,
externally managed, or otherwise unsupported units manual.
Adapter-enabled services, including dynamic mode with no current adapters, are
manual until target-profile adapter support and adapter storage preservation
are implemented.

## Dry-run and commit

Apply defaults to Kubernetes server-side dry-run. It re-reads and revalidates
each unit independently and asks admission to validate each planned write:

```bash
/tmp/aim-template-migration apply --plan ./migration-plan/plan.json
```

Inspect the terminal counts and private `apply-result.json`. The result
separates attempted and completed operations, records current identities and
the last attempted and completed handoff step, and distinguishes succeeded,
failed, manual, and skipped units. It also records the exact plan-file digest
and a unique run ID. Apply durably records an incomplete attempt before each
Kubernetes mutation and records API acceptance or failure before later
readiness verification.

Persistent execution requires explicit approval and `--commit`:

```bash
/tmp/aim-template-migration apply \
  --plan ./migration-plan/plan.json \
  --commit
```

Automatic cutover is no-rollout: the AIMService changes to the profile
pipeline, but the existing InferenceService UID, generation, desired spec, and
desired metadata must remain unchanged. Before any Kubernetes write, Apply
requires that InferenceService to be current and Ready, carry a nonempty legacy
template label, and carry no profile label. The service patch binds
preservation to both the cutover AIMService generation and the captured
InferenceService UID. While both pins match, the profile pipeline observes
that exact legacy-template InferenceService without replacing its inline
predictor with a ServingRuntime reference. An active pin with a missing or
different InferenceService or invalid legacy labels fails closed without
rendering a profile-based replacement. A later AIMService spec change advances
generation, expires the pin, and resumes normal profile rendering; that later
change can roll the workload.

Revalidation tolerates resource-version-only controller status churn for
status-bearing resources after checking UID, generation, relevant content, and
the current source closure. Statusless AIMRuntimeConfig inputs remain pinned to
their exact resource version.

During controlled development, approve commit only for disposable,
non-production resources in isolated namespaces containing no real
credentials. A rerun always starts by reading and validating current state.
Apply refuses to start while its exclusive lock exists or when the selected
result path already exists. Archive the prior `apply-result.json` before
starting a fresh run; remove a stale lock only after proving no Apply process is active.
A rerun is not an automatic resume and does not promise rollback,
compensation, repair, or restoration of predecessor UIDs or owner references.

## Cache behavior

The first runnable automatic cohort is the narrow Shared path. It requires one
unique service-driven target cache; a target profile that would create its own
cache, another matching cache, or an owned pre-existing target is manual.
Apply requires the target cache to report the exact predecessor artifact UID
and PVC name, and separately re-reads the pinned live PVC UID, before
protecting the ownerless artifact and again before cutover. The legacy Shared
template cache stays present. These identity checks do not prove cached bytes
or storage-backend identity.

Dedicated caching is always manual. Apply rejects an edited automatic
Dedicated unit before any Kubernetes mutation, in dry-run and commit modes.

## Manual Dedicated handoff

Plan writes one private `dedicated-handoff-*.json` evidence file for each unit
with the `DedicatedHandoffRequiresQuiescence` outcome. The JSON is a
non-executable planning-time snapshot, not a manifest: never apply it. Compare
its pinned identities, exact owner references, pause state, readiness,
observed generations, artifact reports, and PVC phase with live state at every
phase below. A difference is a reason to stop and reassess, not to edit the
evidence.

This procedure requires platform-specific review and approval. The product
does not currently provide an acknowledged legacy-writer quiescence handshake;
the reconciliation-pause annotation requests that a controller stop, but does
not prove that an in-flight writer drained. Informer ordering also cannot prove
that the target cache observed staged ownership before acting.

Prerequisites:

- keep the existing AIMService on the template pipeline and verify its
  InferenceService is Ready;
- obtain maintenance approval, a platform-specific method to establish and
  verify legacy-writer quiescence, and named owners for each resource;
- record live service, InferenceService, source cache, artifact, and PVC UIDs,
  generations, status, and owner references, then compare them with the
  evidence file; and
- arrange independent observation of controller logs, Kubernetes events,
  storage health, and serving health throughout the transfer.

Stop immediately if any pinned UID, desired-state digest, artifact/PVC
association, owner reference, observed generation, or readiness state differs;
if a resource is terminating; if external desired-state management races the
maintenance; if quiescence cannot be positively verified; or if serving health
degrades. The pause annotation alone is insufficient evidence of quiescence.

Perform the approved transfer conceptually in this order. The exact commands
and persistence checks are platform-specific and must not be copied from the
evidence file:

1. **Hold the source pipeline.** Confirm the AIMService still selects its
   legacy template and record the InferenceService UID, generation, desired
   spec, desired metadata, observed generation, and Ready condition. Stop if
   any value is already changing.
2. **Quiesce the legacy writer externally.** Establish and independently
   verify that no source cache or artifact writer remains active. Recheck
   storage and serving health before continuing.
3. **Pause the source cache.** Add the reconciliation-pause request without
   changing its UID, spec, finalizers, or owner references. Re-read the source
   cache, artifact, and PVC; verify the external quiescence condition still
   holds and that all identities and readiness facts match the approved
   snapshot.
4. **Prepare the target paused.** Create or verify the exact target profile and
   a controller-compatible Dedicated target cache while that cache is paused.
   Confirm the target profile/cache identities, desired state, service
   ownership, and pause state before exposing the artifact to it.
5. **Stage secondary ownership.** Preserve the source cache as the artifact's
   controller owner and add the target cache only as the approved,
   non-controller secondary owner. Persist and re-read the artifact; verify
   both exact owner references and flags, the artifact UID/status/PVC, and the
   live PVC UID and Bound phase. Do not continue on an admission rewrite or
   ambiguous owner order.
6. **Activate and verify the target.** Remove the target cache pause only after
   the staged ownership is durably observed. Require the target cache's current
   generation to report Ready with the exact source artifact UID and PVC, and
   independently re-read that PVC. A same-name artifact or PVC is not
   sufficient.
7. **Transfer controller ownership.** Only after target adoption is proven,
   replace the artifact's source controller owner with the exact target cache
   controller owner, preserving the approved deletion-protection flag.
   Re-read the artifact and PVC and verify the transfer persisted exactly.
8. **Revalidate the full closure.** Recheck the paused source cache, target
   profile/cache, artifact, PVC, AIMService dependencies, controller events,
   and serving health. Stop if either pipeline reports a different artifact or
   PVC, or if any generation/readiness fence regresses.
9. **Cut over the existing AIMService.** Change only the existing service from
   template selection to the approved profile selection. Verify that the
   existing InferenceService retains the same UID, generation, desired spec,
   and desired metadata, and that its status observes that unchanged
   generation and remains Ready.
10. **Leave the predecessor in place.** Keep the old Dedicated template cache
    paused and present. Its cleanup requires a separate approval after the
    target service and storage have remained healthy for the required
    observation period.

After any partial failure, make no compensating or cleanup change merely to
reach the next listed state. Freeze administrator changes, preserve every
surviving object, capture live YAML/status/events/logs, compare live state with
the evidence and last verified phase, and involve the source controller,
target controller, storage, and service owners. Resume, reconstruct the
predecessor, or reverse an ownership change only under a new plan that accounts
for current UIDs, finalizers, controller reactions, and storage state.

## Partial recovery

Units run independently in deterministic order, and one failure does not stop
unrelated units. Manual units perform no Kubernetes writes. For a failed unit:

1. stop further administrator changes to that unit;
2. inspect its `apply-result.json` entry, current UIDs, owner references,
   pause state, and last attempted/completed step;
3. compare live resources with `plan.json`, `backup.yaml`, and the separately
   secured credential backup; and
4. choose a manual correction or predecessor reconstruction with the resource
   owners.

The utility never performs rollback, compensation, or cleanup. After separate
cluster validation and owner approval, administrators may later address
paused legacy Dedicated caches, legacy Shared caches, and Shared artifact
eviction protection. Cleanup must account for finalizers, creator labels,
controller reactions, and the fact that backup files cannot restore object
identity or cached bytes.

## Controlled-development limitations

The following validation and delivery gaps remain open. Neither a successful
dry-run, commit, unit test, nor bridge-contract check establishes production
safety:

- **Artifact contents:** the selected PVC identity contract does not establish
  preserved bytes or storage-backend identity.
- **Direct-vs-triggered Delete boundary:** direct utility Delete calls are
  prohibited, but controller-triggered Delete effects after writes are not
  bounded by that recording-client proof.
- **Dedicated synchronization:** there is no acknowledged writer-quiescence
  and adoption-ordering contract, so Dedicated units remain manual.
- **Live integration:** cross-version API-server CRUD, informer delivery,
  owner-reference admission, collision timing, and annotation-size boundaries
  are not proven by fake-client tests. The controlled rehearsal twice required
  an operator restart after an AIMService update was not dispatched; treat a
  cutover that does not promptly advance `status.observedGeneration` as an
  unresolved infrastructure failure, not permission to weaken no-rollout
  validation.
- **Bridge-contract scope:** the stage-aware bridge contract asserts only the
  bounded Stage 2 source shape and retains a broader Stage 2
  semantic-verification exclusion.

The utility does not rewrite storage, mutate CRDs, send inference requests,
install RBAC, integrate with GitOps, or authorize v1alpha1 removal.

## Deferred live validation

GPU readiness, predictor rollout observation, and a real inference request
belong to the later administrator-owned local GPU validation script. That
script must also confirm the expected artifact/PVC identities and capture any
unsupported concrete case before automatic behavior is expanded.
