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

:::{admonition} Adapters require a profile
:class: note

`spec.adapters` requires `spec.profile`. The service resolves the parent base
model through its `AIMProfileCache`.
:::

:::{admonition} The resolved profile must advertise adapter capability
:class: warning

`metadata.features` in the runtime profile YAML is what the image gates LoRA
loading on. `spec.adapters` declares what the service should serve; the profile
feature declares that the selected image can honour the adapter contract. AIM
Engine does not infer image capability from service intent.

Discovered adapter-capable profiles advertise `features: ["adapters"]`
automatically. A legacy or custom profile whose image is known to support
adapters can be asserted explicitly on a service-owned overlay:

```yaml
spec:
  profile:
    name: custom-qwen-profile
  profileOverrides:
    features:
      - adapters
  adapters:
    - name: medical-lora
      kind: AIMArtifact
```

Without either the profile feature or this explicit override, the service is
rejected with `ConfigValid=False`. Feature overrides are additive: they cannot
remove capabilities advertised by the source profile.
:::

:::{admonition} Image prerequisite
:class: warning

Serving adapters still requires an inference image that honours the
`AIM_ADAPTER_*` container contract — **AIM images from 0.13 onwards**. Older
images ignore the adapter env vars entirely: the disk is mounted and the bytes
are staged, but no adapter is ever loaded. AIM Engine cannot inspect an image to
verify this, so the container remains the final authority.
:::

:::{admonition} Minimal MVP scope
:class: warning

This release ships **reference-only** adapter support: adapters are plain
references to existing `AIMArtifact` objects, and status reflects disk-side
staging only (`Pending` / `Downloading` / `Downloaded` / `Failed`). The list is editable
— adding an adapter stages it and the runtime hot-loads it. Removing an entry
is reconciled: a controller-managed subtree-sync Job atomically moves the
removed adapter's directory out of the service subtree, then deletes it (the
model-artifact reaper retries any leftover cleanup and still reclaims the
*whole* subtree when the service is deleted). Note the in-pod effect of a
removal depends on the image running in dynamic mode (watcher);
in static mode the bytes are removed from disk but the running pod keeps the
adapter until restart. The controller sets the `AIM_ADAPTER_*` container env
(`AIM_ADAPTER_SOURCE`, `AIM_ADAPTER_MODE`, the `MAX_*` caps, and a dynamic-mode
refresh interval), which images from 0.13 onwards honour. Inline self-healing
(`sourceUri` on the service), engine-reported `Loaded` / `LoadRejected` states,
and dynamic namespace opt-in are deferred.
:::

## Concepts

| Object | Role |
|--------|------|
| `AIMArtifact` `type: model` with `adapterDisk` | The base model. Provisions **two** PVCs: the model cache PVC and a shared `ReadWriteMany` adapter disk. |
| `AIMArtifact` `type: adapter` | A LoRA adapter using either logical compatibility (`spec.compatibleWith` model IDs) or an exact legacy binding (`spec.parentArtifact`). Defines `sourceUri`, its own `modelId`, and optional `rank`. |
| `AIMService` `spec.adapters[]` | The list of adapters this service serves. Editable after creation; entries are pure references with unique `(kind, name)` pairs. |

## Rank capacity

The controller writes the resolved LoRA rank capacity to
`AIM_ADAPTER_MAX_RANK`:

- In `static` mode, the adapter set is immutable, so the controller infers the
  value by rounding the largest declared `AIMArtifact.spec.rank` up to a
  runtime-supported ceiling, recomputing it whenever those artifacts change —
  including downwards. Ranks above `512` are rejected.
- In `dynamic` mode, future adapters are unknown. The controller resolves a
  startup ceiling from the service, namespace RuntimeConfig, cluster
  RuntimeConfig, then the built-in default of `32`.

```yaml
spec:
  adapterMode: dynamic
  adapterRuntime:
    maxRank: 64
```

`spec.adapterRuntime.maxRank` is only valid when `adapterMode` is `dynamic`.
Static services infer their ceiling from the declared artifacts.

The same default can be configured for a namespace or cluster:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMRuntimeConfig
metadata:
  name: default
spec:
  adapterRuntime:
    maxRank: 64
```

RuntimeConfig may carry this default for mixed service modes; static services
ignore the inherited value.

A dynamic adapter whose declared rank exceeds the resolved ceiling is rejected
before staging. Changing the ceiling changes the inference container contract
and rolls the serving workload. Increases take effect immediately. A decrease
is deferred while any declared adapter artifact's rank is unresolved: the
controller retains the previous higher ceiling until every rank can be checked
against the lower value. An adapter artifact without a rank retains the
compatibility default of `16`.

The valid resolved startup ceiling is reported on `status.adapterMaxRank`. A
rejected configuration leaves the last valid value in place. This field reports
the controller's resolved container contract; it is not runtime-reported state.
During a deferred dynamic decrease, it therefore continues to report the
previous higher ceiling until rank resolution completes.

### Storage layout

The shared adapter disk is one RWX PVC owned by the base model artifact. Each
service that serves adapters gets its own subtree keyed by the service UID:

```
<adapter-disk>/
  <service-uid>/            # mounted read-only into the pod via subPath
    <adapter-path>/         # one directory per adapter
  .staging/                 # download scratch (PVC root, outside the pod mount)
  .aside/                   # pre-promote swap area
  .unload-tmp/              # atomically removed adapters awaiting cleanup
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
the subtree existing — not on downloads. The Job's name encodes both the
declared set and resolved adapter-disk PVC, and
`status.adapterSubtreeSyncKey` records the last synced binding. Editing
`spec.adapters` re-runs the sync to prune removed adapters; replacing a base
artifact with a new UID-derived adapter PVC re-runs the sync and staging on the
new disk. The InferenceService keeps mounting the previous disk until every
adapter staged there is also staged on the new one, unless the previous PVC is
gone. Until then:

- `status.adapters` describes the previous disk, which the service still
  serves from.
- The `AdaptersReady` message names both PVCs, counts the adapters staged on
  the new disk, and names any adapter whose staging failed there. An adapter
  that fails on the new disk keeps the service on the previous one. To move
  without it, remove it from `spec.adapters` or delete the previous base model
  `AIMArtifact`.
- An adapter added to `spec.adapters` is staged on both disks.
- An adapter removed from `spec.adapters` stays on the previous disk, and the
  runtime can still load it, until the move completes.

While the new base model `AIMArtifact`
has no adapter disk PVC yet, the service keeps the previous disk mounted, and
the `AdaptersReady` message says what that `AIMArtifact` is waiting for,
including its `DependenciesReachable` message, such as a `ResourceQuota`
rejecting the PVC. Staging Jobs run asynchronously and
the runtime hot-loads each adapter as it lands. A staged adapter stays
`Downloaded` after its finished staging Job is cleaned up, so it is not staged
again. The controller sets `AIM_ADAPTER_SOURCE` to the mount path
so the image finds the subtree.

## Storage and compatibility contract

Before a service can serve adapters:

1. **Configure RWX adapter storage.** An adapter-enabled `AIMService` adds
   `spec.adapterDisk` to the base model
   `AIMArtifact` that its `AIMProfileCache` resolves, once its adapter
   configuration is valid. This also applies to an `AIMArtifact` first cached for
   services without adapters: the model is not downloaded again, the
   `AIMProfileCache` is not changed, and services already mounting the model PVC
   are not restarted. A Normal `AdapterDiskRequested` event on the `AIMArtifact`
   names the service.

   The disk's size and storage class come from `spec.adapterDisk` when set there,
   otherwise from `spec.storage.adapterDiskSize` and
   `spec.storage.adapterDiskStorageClassName` of the runtime config the
   `AIMArtifact` uses (its `spec.runtimeConfigName`, merged with the
   `AIMClusterRuntimeConfig`), not of the service that added the disk. Without an
   adapter disk storage class, the runtime config's
   `spec.storage.defaultStorageClassName` is used, then the cluster's default
   storage class; the model's own storage class is not considered. The class must
   support `ReadWriteMany`, so set `adapterDiskStorageClassName` on the
   `AIMClusterRuntimeConfig` when the default class does not. The PVC is 50Gi by
   default, including for a `dynamic` service with no adapters, so budget for it
   on any `AIMArtifact` an adapter-enabled service may share. AIM's
   [artifact storage quotas](../admin/storage-configuration.md#storage-quotas) do
   not count it; a Kubernetes `ResourceQuota` on storage does. While the disk
   exists, the `AIMArtifact` runs a short [reaper Job](#reclaim) every 15
   minutes.

   The disk stays for the `AIMArtifact`'s lifetime; see
   [Removing the adapter disk](#removing-the-adapter-disk). The field is owned by
   the `aim-service-controller-adapter-disk` field manager. GitOps tools that apply
   the `AIMArtifact` with server-side or client-side apply leave it in place; with
   replace-style syncs, ignore `/spec/adapterDisk` (for example through Argo CD
   `ignoreDifferences` with `managedFieldsManagers` and
   `RespectIgnoreDifferences=true`).
2. **Use an exact `sourceUri` match.** The service resolves its parent base model
   by matching the resolved base model id against the resolved artifacts of the
   service's `AIMProfileCache`. With `Shared` caching, the adapter disk is shared
   by every adapter-enabled service on that `AIMArtifact`. With `Dedicated`
   caching, the service gets its own base model `AIMArtifact`, and the adapter
   disk is deleted with it.
3. **Use a single model source.** Adapters bind to one resolved base model.
   Profiles with multiple model sources are rejected as ambiguous.
4. **Choose exactly one compatibility mode per adapter.**
   `compatibleWith` is the recommended logical mode: the resolved base
   artifact's canonical `modelId` must appear in the list. `parentArtifact` is
   the exact legacy mode and must equal the resolved base artifact's Kubernetes
   name. A mismatch is reported as a configuration error.

A logical adapter is valid and `Ready` even when no compatible base currently
exists. It has no parent owner reference and survives deletion or replacement
of compatible base artifacts. Compatibility is evaluated only when an
`AIMService` resolves a concrete base.

## Worked example

```yaml
# 1. Optional pre-created parent base model with an adapter disk. Normally the
#    adapter-enabled service adds this disk automatically; it is shown
#    explicitly so the example is standalone.
apiVersion: aim.eai.amd.com/v1alpha2
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
# 2. A shared, pre-authored adapter logically compatible with the model.
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMArtifact
metadata:
  name: cs-tone-v3
spec:
  type: adapter
  modelId: acme/cs-tone-v3
  compatibleWith:
    - google/gemma-3-27b-it
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
[`config/samples/aim_v1alpha2_lora_adapters.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/aim_v1alpha2_lora_adapters.yaml).

## Lifecycle and status

The InferenceService is gated until the base model is `Ready` **and** the
per-service adapter subtree exists (the subtree-sync Job has succeeded). The
service checks each logical adapter's `compatibleWith` list against the
resolved base artifact's `modelId`; exact adapters are checked against the
resolved artifact name. Compatible adapters then stage asynchronously. In
dynamic mode the runtime loads them as they land without holding back base-model
serving; in static mode the InferenceService waits until every declared adapter
is `Downloaded`. The service exposes a single aggregate `Adapters` condition
(rather than one condition per adapter) and per-adapter disk-side states under
`status.adapters[]`. Once the subtree exists the aggregate condition is `Ready`
even if an individual adapter is still downloading or has failed. A `Failed`
entry carries the staging Job's reason and message in `lastError`. Configuration
errors block both modes; in static mode any adapter not yet `Downloaded` also
blocks InferenceService creation:

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
| `ParentLacksAdapterDisk` | The base model `AIMArtifact` has not resolved yet, has no `spec.adapterDisk` yet, or has not created its adapter disk PVC yet. Once the `AIMArtifact` resolves, the message names it and includes its `DependenciesReachable` message when that is `False` (`Progressing`). |
| `AdapterSubtreeProvisioning` | The per-service subtree is being created; the ISVC is gated on this. The message says when the adapter disk PVC is still `Pending` (`Progressing`). |
| `AdapterConfigInvalid` | Multiple model sources, an incompatible model ID, an exact-parent mismatch, a duplicate adapter path, or a non-adapter artifact was referenced (`Failed`, blocks the ISVC). |
| `AdaptersStaging` | The subtree is ready but one or more adapters are not `Downloaded` (`Ready` aggregate condition). Dynamic serving continues; static ISVC creation remains gated. Also reported while the service moves to a new adapter disk PVC and keeps the previous one mounted; the message names the PVC still mounted. |
| `AdaptersStaged` | All declared adapters are staged (`Ready`). |

### Service stays at `AdaptersReady=False`

Follow the chain from the service to the adapter disk PVC:

```bash
# The AIMProfileCache the service uses (set once that cache is Ready).
kubectl get aimservice <service-name> -n <namespace> -o jsonpath='{.status.cache.profileCacheRef.name}'
# The base model AIMArtifact the service's AIMProfileCache resolved (v1alpha2).
kubectl get aimprofilecache <cache-name> -n <namespace> -o jsonpath='{.status.artifacts}'
# Its adapter disk, the PVC it created, and why it could not apply resources.
kubectl get aimartifact <artifact-name> -n <namespace> \
  -o jsonpath='{.spec.adapterDisk}{"\n"}{.status.adapterPersistentVolumeClaim}{"\n"}'
kubectl describe aimartifact <artifact-name> -n <namespace>
kubectl get pvc <adapter-pvc-name> -n <namespace>
```

- **`ParentLacksAdapterDisk`, and the `AIMArtifact` has no `spec.adapterDisk`:**
  the service has not added it. Check the service's `ConfigValid` and
  `DependenciesReachable` conditions; a failed apply shows on the latter.
- **`ParentLacksAdapterDisk`, and the `AIMArtifact` has `spec.adapterDisk` but no
  `status.adapterPersistentVolumeClaim`:** the adapter disk PVC could not be
  created, for example because a `ResourceQuota` rejects it. The service's message
  includes the `AIMArtifact`'s `DependenciesReachable` message; check the
  `AIMArtifact`'s events.
- **`AdapterSubtreeProvisioning` with a `Pending` PVC:** the storage class cannot
  provision `ReadWriteMany` volumes, so the Job that creates the service's
  subtree cannot start. Set `spec.storage.adapterDiskStorageClassName` to a class
  that supports `ReadWriteMany` on the runtime config the `AIMArtifact` uses,
  then delete the `Pending` PVC. The `AIMArtifact` recreates it with the new
  class; the base model is not downloaded again.

A service moving from a previous adapter disk stays at `AdaptersReady=True`
with `AdaptersStaging` in these cases instead: its message names the PVC it
keeps mounted, then the same causes, including a new PVC that is still
`Pending`.

## Reclaim

Adapter subtrees are not Kubernetes objects, so owner-reference garbage
collection cannot reclaim them. Cleanup is two-tier, split by ownership:

- **Adapter descriptor lifecycle** — logical adapters have no base-model owner
  and persist independently. Exact `parentArtifact` adapters retain the legacy
  owner reference and are cascade-deleted with that concrete parent artifact.

- **Per-adapter unload (service alive)** — the AIMService-owned **subtree-sync**
  Job atomically moves adapter directories no longer in `spec.adapters` to
  `.unload-tmp`, then deletes them as its final, best-effort step. It is owned by
  the service (garbage-collected with it) and runs whenever the declared set
  changes.
- **Whole-subtree reclaim (service deleted)** — the base model artifact
  periodically launches a **reaper** Job that removes subtrees whose owning
  `AIMService` no longer exists, crash-orphaned `.staging` / `.aside`
  directories, and old `.unload-tmp` entries even for live services. This is
  owned by the model artifact (not the service) precisely so it can run *after*
  the service — and its subtree-sync Job — are gone. Deleting an adapter-serving
  service tears down its pods immediately; its on-disk subtree is reclaimed by
  the next sweep.

### Removing the adapter disk

With `Shared` caching, the adapter disk stays on the base model `AIMArtifact`
after its last adapter-enabled service is deleted. With `Dedicated` caching, it
is deleted with the service. To reclaim a shared disk without downloading the
model again:

1. Find the PVC and check that nothing still uses it. Any adapter-enabled
   `AIMService` whose base model resolves to this `AIMArtifact` mounts it,
   whichever profile it uses, and adds the disk back after you remove it.

   ```bash
   PVC=$(kubectl get aimartifact <artifact> -n <namespace> \
     -o jsonpath='{.status.adapterPersistentVolumeClaim}')
   kubectl get aimservices -n <namespace> \
     -o jsonpath="{range .items[?(@.status.adapterDiskPersistentVolumeClaim==\"$PVC\")]}{.metadata.name}{'\n'}{end}"
   ```

2. Remove the disk from the `AIMArtifact`, then delete its reaper Job and the
   PVC. The reaper Job's finished pod is kept for 5 minutes and holds the PVC
   until it is deleted:

   ```bash
   kubectl patch aimartifact <artifact> -n <namespace> --type json \
     -p '[{"op":"remove","path":"/spec/adapterDisk"}]'
   kubectl delete job -n <namespace> \
     -l aim.eai.amd.com/component=adapter-reap,aim.eai.amd.com/cache.name=<artifact>
   kubectl delete pvc -n <namespace> "$PVC"
   ```

An adapter-enabled v1alpha2 service that arrives later adds the disk back.

## Serving contract

The container is expected to load every adapter directory it finds under
`/adapters`. The controller sets the `AIM_ADAPTER_*` environment variables on the
inference container (`AIM_ADAPTER_SOURCE`, `AIM_ADAPTER_MODE`, the `MAX_*` caps,
and a dynamic-mode refresh interval). Static maximum rank is inferred from the
declared artifacts; dynamic maximum rank is resolved from service/runtime
configuration. Engine-reported load state remains to be completed alongside the
inference container.
