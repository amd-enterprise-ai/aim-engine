# LoRA Adapters

AIM Engine can serve LoRA adapters alongside a base model. A service declares
adapters in `spec.adapters`; `spec.adapterMode` selects the contract (`static`,
the default, or `dynamic`). The declared adapters are staged into an isolated,
per-service subtree of a shared adapter disk and mounted read-only into the
inference container at `/adapters`.

`spec.adapterMode` is an **immutable, startup-time** property of the pod (it maps
directly to the `AIM_ADAPTER_MODE` container env):

| `adapterMode` | Adapter disk mounted? | `spec.adapters` editable? |
|---------------|-----------------------|---------------------------|
| `static` (default) | When ≥ 1 adapter is declared | No — frozen at creation |
| `dynamic` | Yes (even at zero adapters) | Yes — add/remove anytime |

Because adapter add/remove is a pure disk operation, the mount is decoupled from
the list length: in `dynamic` mode the subtree is mounted even at **zero
adapters**, so editing the list — including dropping to zero — never restarts the
pod or modifies the InferenceService, and the runtime loads/unloads adapters from
the mounted subtree at will. In `static` mode the set is fixed at creation and the
disk is mounted only when at least one adapter is declared (a static service with
no adapters serves nothing and needs no mount).

:::{admonition} Both pipelines
:class: note

`spec.adapters` is supported on both the **profile** pipeline
(`aim.eai.amd.com/v1alpha2`, via `spec.profile`) and the **template**
pipeline (`aim.eai.amd.com/v1alpha1`, via `spec.template` / `spec.model`).
The staging mechanics are identical; only base-model resolution differs —
the profile pipeline resolves the parent through the `AIMProfileCache`, the
template pipeline through the `AIMTemplateCache`. On `v1alpha2`, adapters
still require `spec.profile`.
:::

:::{admonition} Minimal MVP scope
:class: warning

This release ships **reference-only** adapter support: adapters are plain
references to existing `AIMArtifact` objects, and status reflects disk-side
staging only (`Pending` / `Downloading` / `Downloaded`). The list is editable
— adding an adapter stages it and the runtime hot-loads it. Removing an entry
is reconciled: a controller-managed subtree-sync Job prunes the removed
adapter's directory from the service subtree (the model-artifact reaper still
reclaims the *whole* subtree when the service is deleted). Note the in-pod
effect of a removal depends on the image running in dynamic mode (watcher);
in static mode the bytes are removed from disk but the running pod keeps the
adapter until restart. The controller already sets the `AIM_ADAPTER_*`
container env (`AIM_ADAPTER_SOURCE`, `AIM_ADAPTER_MODE`, the `MAX_*` caps, and
a dynamic-mode refresh interval), but the image honouring them (the in-pod
watcher), inline self-healing (`sourceUri` on the service), engine-reported
`Loaded` / `LoadRejected` states, dynamic namespace opt-in, and
`AIMProfileCache → adapterDisk` auto-propagation are deferred.
:::

## Concepts

| Object | Role |
|--------|------|
| `AIMArtifact` `type: model` with `adapterDisk` | The base model. Provisions **two** PVCs: the model cache PVC and a shared `ReadWriteMany` adapter disk. |
| `AIMArtifact` `type: adapter` | A LoRA adapter bound to a parent model via `spec.parentArtifact`. Defines `sourceUri`, `modelId`, and optional `rank`. |
| `AIMService` `spec.adapters[]` | The list of adapters this service serves. Editable after creation; entries are pure references with unique `(kind, name)` pairs. |

### Storage layout

The shared adapter disk is one RWX PVC owned by the base model artifact. Each
service that serves adapters gets its own subtree keyed by the service UID:

```
<adapter-disk>/
  <service-uid>/            # mounted read-only into the pod via subPath
    <adapter-path>/         # one directory per adapter
  .staging/                 # download scratch (PVC root, outside the pod mount)
  .aside/                   # pre-promote swap area
```

A per-`(service, adapter)` staging Job downloads the adapter into `.staging/`,
verifies it, and atomically promotes it into the live subtree with `rename(2)`.
The staging Job is the single logical writer to the live tree — the controller
never mounts the PVC. The pod mounts only `<service-uid>/` (read-only, via
`subPath`), so a service can only ever see the adapters it declares.

Because the controller never mounts the PVC, a fast, AIMService-owned
**subtree-sync** Job reconciles the per-service directory: it creates
`<service-uid>/` (so the read-only `subPath` mount binds — the aim-runtime
errors on startup if its mounted `subPath` is missing) and prunes any adapter
directory no longer in `spec.adapters`. The InferenceService is gated only on
the subtree existing — not on downloads. The Job's name encodes the declared
set and `status.adapterSubtreeSyncKey` records the last synced set, so editing
`spec.adapters` re-runs the sync (to prune removed adapters) without re-run
loops. Staging Jobs run asynchronously and the runtime hot-loads each adapter
as it lands. The controller sets `AIM_ADAPTER_SOURCE` to the mount path so the
image finds the subtree.

## MVP bootstrap contract

Because `AIMProfileCache → adapterDisk` propagation is deferred, the operator
must satisfy the following before a service can serve adapters:

1. **Pre-create the parent model artifact with `adapterDisk`.** A service that
   declares adapters against a parent without an adapter disk is gated with
   `ParentLacksAdapterDisk`.
2. **Use `Shared` caching and an exact `sourceUri` match.** The service resolves
   its parent base model by matching the resolved base model id against the
   cache's resolved artifacts — the `AIMProfileCache` on the profile pipeline,
   the `AIMTemplateCache` on the template pipeline. `Dedicated` caching mints a
   per-service parent and is not supported with adapters.
3. **First model source wins.** Adapters bind to a single base model. If the
   resolved profile or template carries more than one model source, the first is
   targeted deterministically.
4. **Each adapter's `parentArtifact` must equal the resolved base model.** A
   mismatch is reported as a configuration error.

## Worked example

```yaml
# 1. Parent base model with an adapter disk (operator-created bootstrap).
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: gemma-3-27b-it-cache
spec:
  type: model
  modelId: google/gemma-3-27b-it
  sourceUri: hf://google/gemma-3-27b-it
  size: 60Gi
  adapterDisk:
    size: 50Gi
---
# 2. A shared, pre-authored adapter bound to the parent.
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: cs-tone-v3
spec:
  type: adapter
  modelId: acme/cs-tone-v3
  parentArtifact: gemma-3-27b-it-cache
  sourceUri: s3://eai-artifacts/adapters/cs-tone-v3/
  rank: 16
---
# 3. A service that serves the adapter.
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: gemma-cs
spec:
  profile:
    name: gemma-3-27b-it-mi300x-bf16
  adapterMode: dynamic
  adapters:
    - name: cs-tone-v3
      kind: AIMArtifact
```

The full set of objects is also available under
[`config/samples/aim_v1alpha2_lora_adapters.yaml`](https://github.com/silogen/aim-engine/blob/main/config/samples/aim_v1alpha2_lora_adapters.yaml).

The same adapters can be served from the **template pipeline** by declaring
`spec.adapters` on a `v1alpha1` service (the parent base model is then resolved
through the `AIMTemplateCache`):

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: gemma-cs
spec:
  model:
    name: gemma-3-27b-it
  adapterMode: dynamic
  adapters:
    - name: cs-tone-v3
      kind: AIMArtifact
```

## Lifecycle and status

The InferenceService is gated until the base model is `Ready` **and** the
per-service adapter subtree exists (the subtree-sync Job has succeeded).
Adapters then stage asynchronously and the runtime loads them as they land —
downloads never hold back serving. The service exposes a single aggregate
`Adapters` condition (rather than one condition per adapter) and per-adapter
disk-side states under `status.adapters[]`. Once the subtree exists the
`Adapters` condition is `Ready` even while individual adapters are still
downloading; a configuration error is the only adapter state that blocks the
InferenceService:

```yaml
status:
  adapters:
    - name: cs-tone-v3
      adapterPath: cs-tone-v3
      modelId: acme/cs-tone-v3
      state: Downloaded
```

| Reason | Meaning |
|--------|---------|
| `ParentLacksAdapterDisk` | The resolved base model has no adapter disk yet (`Progressing`). |
| `AdapterSubtreeProvisioning` | The per-service subtree is being created; the ISVC is gated on this (`Progressing`). |
| `AdapterConfigInvalid` | Multiple model sources, a parent mismatch, a duplicate adapter path, or a non-adapter artifact was referenced (`Failed`, blocks the ISVC). |
| `AdaptersStaging` | The subtree is ready and serving; one or more adapters are still downloading asynchronously (`Ready`). |
| `AdaptersStaged` | All declared adapters are staged (`Ready`). |

## Reclaim

Adapter subtrees are not Kubernetes objects, so owner-reference garbage
collection cannot reclaim them. Cleanup is two-tier, split by ownership:

- **Per-adapter unload (service alive)** — the AIMService-owned **subtree-sync**
  Job prunes adapter directories no longer in `spec.adapters`. It is owned by the
  service (garbage-collected with it) and runs whenever the declared set changes.
- **Whole-subtree reclaim (service deleted)** — the base model artifact
  periodically launches a **reaper** Job that removes subtrees whose owning
  `AIMService` no longer exists, plus crash-orphaned `.staging` / `.aside`
  directories. This is owned by the model artifact (not the service) precisely so
  it can run *after* the service — and its subtree-sync Job — are gone. Deleting
  an adapter-serving service tears down its pods immediately; its on-disk subtree
  is reclaimed by the next sweep.

## Serving contract

The container is expected to load every adapter directory it finds under
`/adapters`. The controller sets the `AIM_ADAPTER_*` environment variables on the
inference container (`AIM_ADAPTER_SOURCE`, `AIM_ADAPTER_MODE`, the `MAX_*` caps,
and a dynamic-mode refresh interval); the `MAX_*` caps are currently uniform
built-in defaults. The image honouring these variables, a profile
LoRA-support gate, max-rank validation, and engine-reported load state remain to
be completed alongside the inference container.
