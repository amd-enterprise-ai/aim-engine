# Model Caching

AIM Engine pre-downloads model artifacts to persistent volumes so inference services can start without waiting on registry pulls. The caching system is hierarchical, deduplicates downloads within a namespace, and is driven from each `AIMService`'s resolved profile.

!!! info "v1alpha2"
    This page documents the `AIMProfileCache` flow used by v1alpha2 services. For the v1alpha1 `AIMTemplateCache` shape, see [Service Templates (v1alpha1)](../legacy/service-templates.md). Both flows coexist in the same cluster.

## Resources

The cache hierarchy uses three v1alpha2 resources plus the underlying Kubernetes objects:

| Resource | Scope | Role |
|---|---|---|
| `AIMService` | Namespace | Triggers cache creation via `spec.caching.mode`. Resolves a profile, then creates or reuses an `AIMProfileCache` for it. |
| `AIMProfileCache` | Namespace | Groups one `AIMArtifact` per `modelSources[]` entry on the referenced profile. |
| `AIMArtifact` | Namespace | Manages the actual download — a `Job` writing to a `PVC`. |

```
AIMService
   └── AIMProfileCache    (created or reused)
          └── AIMArtifact(s)    (created or reused)
                 └── PVC + Download Job
```

`AIMProfileCache.spec.profileName` + `profileScope` identifies which profile's `modelSources` to materialise. The cache controller looks up the namespace `AIMProfile` first when `profileScope: Namespace`, or directly the cluster `AIMClusterProfile` when `profileScope: Cluster`.

## Caching modes

`AIMProfileCache` runs in one of two modes, controlling artifact ownership:

| Mode | Artifact ownerReferences | Sharing | Set by |
|---|---|---|---|
| `Shared` (default) | None (orphan) | Reused across services and caches in the namespace | `AIMService.spec.caching.mode: Shared` (default) |
| `Dedicated` | Owned by the `AIMProfileCache` | Exclusive to this service | `AIMService.spec.caching.mode: Dedicated` |

### Shared mode

The cache and its artifacts persist independently of any single service. When a service is deleted, the shared cache stays around so subsequent services that resolve to the same profile reuse it immediately.

The deduplication boundary is per-namespace:

- The cache controller finds an existing shared `AIMProfileCache` for the same profile and reuses it (no new cache resource).
- If a new cache is created, the artifact controller finds existing shared `AIMArtifact`s for the same `sourceUri` (+ storage class + downloader env) and reuses them.

Result: multiple services that share a profile fan in onto a single set of `AIMArtifact`s, one download per artifact per namespace.

### Dedicated mode

The cache is owner-referenced by the service; its artifacts are owner-referenced by the cache. Deleting the service triggers Kubernetes garbage collection of both the cache and its artifacts (including their PVCs).

Useful when:

- You want a guaranteed, exclusive copy of the model — no risk of a quota-eviction round freeing a PVC out from under a long-running workload.
- You want all storage cleaned up automatically when the service is deleted.

Dedicated caches never share artifacts across caches. Two dedicated services on the same profile each get their own full download.

## Creation flow

When an `AIMService` reaches the cache step in its reconcile pipeline:

1. **Resolve profile.** The service has already produced `status.resolvedProfile`. The cache controller reads `spec.modelSources` from that profile.
2. **Lookup or create cache.**
    - `Shared` mode: list shared `AIMProfileCache` objects in the namespace by profile name + scope. Reuse if one exists; otherwise create a new one with `mode: Shared` and no owner.
    - `Dedicated` mode: create a new `AIMProfileCache` owned by the service, with `mode: Dedicated`.
3. **Lookup or create artifacts.** For each `modelSources[]` entry, the cache builds an `AIMArtifact` keyed on the source URI (and env / storage class). In shared mode the artifact controller reuses existing shared artifacts; in dedicated mode it always creates its own.
4. **Wait for `Ready`.** The cache's `ArtifactsReady` condition flips `True` when every constituent artifact is `Ready`.
5. **Mount.** The service mounts the underlying PVCs into the `InferenceService` pod spec.

The service's `ProfileCacheReady` condition mirrors the cache's `ArtifactsReady` until everything's downloaded.

## Profile-level caching

Namespace-scoped `AIMProfile` resources can themselves request caching:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: qwen-qwen3-32b-mi300x-fp8-latency
  namespace: ml-team
spec:
  # ...
  caching:
    enabled: true
```

When `spec.caching.enabled: true`, the profile controller creates a shared `AIMProfileCache` for that profile on its own — no service required. This is the standalone pre-warm pattern: the cache is in place before any service references the profile.

Cluster-scoped `AIMClusterProfile` resources cannot enable caching directly (cluster-scoped caches not yet implemented). They get cached only when a service in some namespace resolves to them.

## Cache status

`AIMProfileCache` uses the standard AIM status values:

| Status | Meaning |
|---|---|
| `Pending` | Cache created; lookup or reconciliation pending |
| `Progressing` | One or more artifacts are still downloading |
| `Ready` | All artifacts are `Ready` |
| `Degraded` | At least one artifact failed but others are usable |
| `NotAvailable` | Referenced profile not found or not ready |
| `Failed` | Cache build failed (config error, missing dependency) |

### Conditions

| Condition | Reasons |
|---|---|
| `ProfileFound` | `ProfileResolved`, `ProfileNotFound` |
| `ArtifactsReady` | `AllCachesReady`, `CreatingCaches`, `CachesNotReady`, `NoCaches` |

`status.artifacts` is a map from logical artifact name to the resolved `AIMArtifact` reference (`name`, `uid`).

## Deletion behaviour

Deletion follows Kubernetes ownership. AIM finalizers also delete **non-Ready** caches and artifacts so a Failed/Pending cache never blocks recreation.

### When an AIMService is deleted

- **Dedicated cache** owned by the service: garbage-collected with the service. Artifacts and PVCs go too.
- **Shared cache** referenced by the service: untouched. Other services may still use it.
- **Service finalizer**: deletes any caches the service created (by label) that are **not Ready**, so stuck Pending/Progressing/Failed caches don't block a future service from creating a new one.

### When an AIMProfile is deleted

- Profile-owned caches (created via `spec.caching.enabled: true`) are garbage-collected with the profile.
- Service-created shared caches that referenced this profile go `NotAvailable` (`ProfileFound=False / ProfileNotFound`) but are not deleted automatically; clean them up manually if you want to free the PVCs.

### When an AIMProfileCache is deleted

- **Dedicated**: artifacts are GC'd with the cache.
- **Shared**: the cache finalizer deletes any **not-Ready** artifacts it created (by label). **Ready** shared artifacts persist so other caches can reuse them.

### When an AIMArtifact is deleted

- Its PVC and any download `Job` are garbage-collected (they're owned by the artifact).
- Any inference pod still mounted on the PVC keeps it bound until the pod terminates.

## Storage quota and eviction

AIM Engine supports storage quotas that cap total PVC space consumed by AIMArtifacts. When a new artifact would exceed the configured limit, the controller either evicts lower-priority artifacts to free space or blocks the new artifact until capacity is available.

### How eviction works

Only **Shared**, **Ready** artifacts with a `retentionPriority` (set on the artifact or via `defaultRetentionPriority` in runtime config) are evictable. The controller evicts the minimum needed, starting with the lowest priority. Artifacts referenced by an active `AIMProfileCache` are never evicted.

```
Eviction order:
  1. Lowest retentionPriority value first
  2. Among equal priorities, oldest creationTimestamp first
```

When nothing evictable can free enough space, the new artifact is blocked with a `StorageQuotaExceeded` condition. That condition propagates up through `AIMProfileCache` and `AIMService` so users see the root cause at the layer they look at.

### Dedicated vs shared

Only **Shared** artifacts are eligible for eviction. **Dedicated** artifacts belong to a single service — removing them would break it, so the eviction routine skips them.

For configuration details, see [Storage Quotas](../admin/storage-configuration.md#storage-quotas).

## Manual cache management

Most caching is implicit (services drive it via `spec.caching`). Manual entry points:

- **Pre-warm a profile** by setting `AIMProfile.spec.caching.enabled: true` and applying the profile before any service.
- **Create an `AIMArtifact` directly** to materialise a single source URI; it's deduplicated against existing shared artifacts.
- **Cleanup**: Ready shared artifacts have no owner and are not GC'd. Delete them manually to free space.

### Pre-warm with `AIMProfile.spec.caching.enabled`

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: qwen-qwen3-32b-mi300x-fp8-latency
  namespace: ml-team
spec:
  aimId: qwen/qwen3-32b
  modelId: qwen/qwen3-32b-fp8
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1
  precision: fp8
  metric: latency
  engine: vllm
  modelSources:
    - modelId: qwen/qwen3-32b-fp8
      sourceUri: hf://qwen/qwen3-32b-fp8
  caching:
    enabled: true
```

### Direct `AIMArtifact` (kserve downloader, XET disabled)

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: kserve-smollm2-135m
  namespace: ml-team
spec:
  modelDownloadImage: kserve/storage-initializer:v0.16.0
  env:
    - name: HF_HUB_DISABLE_XET
      value: "1"
  sourceUri: hf://HuggingFaceTB/SmolLM2-135M
  size: 500M
  storageClassName: rwx-nfs
```

## Download protocol strategy

`AIMArtifact` downloads from HuggingFace support multiple protocols with automatic fallback. The operator tries each protocol in a configurable sequence, cleaning incomplete files between attempts. The motivation: some models require XET for parts of the download, but XET struggles with network instability in certain environments. A mixed-protocol fallback chain is the default to keep downloads robust.

### Supported protocols

| Protocol | Description |
|---|---|
| `XET` | HuggingFace content-addressable chunk protocol. Default in `huggingface_hub >= 0.32`. Efficient for large files with deduplication. |
| `HF_TRANSFER` | Rust-based parallel HTTP downloader (deprecated upstream in favour of XET). |
| `HTTP` | Plain HTTP range requests. Most compatible, no extra dependencies. |

### Configuration

Controlled by the `AIM_DOWNLOADER_PROTOCOL` environment variable — a comma-separated sequence of protocols to try in order.

**Default**: `XET,HF_TRANSFER`

Overrides apply at three layers (highest precedence first):

1. **Per-artifact** via `AIMArtifact.spec.env`.
2. **Per-namespace** via `AIMRuntimeConfig.spec.env`.
3. **Cluster-wide** via `AIMClusterRuntimeConfig.spec.env`.

#### Example: override per artifact

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: my-model
spec:
  sourceUri: hf://Qwen/Qwen3-32B
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "HTTP"
```

#### Example: cluster-wide default

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "XET,XET,HTTP"
```

### Observing download status

During downloads, `status.download` on the artifact is updated by the downloader pod with attempt metadata:

```yaml
status:
  download:
    protocol: HTTP
    attempt: 2
    totalAttempts: 3
    protocolSequence: "XET,XET,HTTP"
    message: Complete
```

```bash
kubectl get aimart -o wide
kubectl get aimart my-model -o yaml
```

### How protocol switching works

1. The downloader iterates through the protocol sequence left to right.
2. For each protocol, the appropriate HuggingFace environment variables are set (`HF_HUB_DISABLE_XET`, `HF_HUB_ENABLE_HF_TRANSFER`).
3. If a protocol fails, any `.incomplete` files are cleaned before switching to the next protocol.
4. Already-completed files are skipped regardless of protocol (metadata-based).
5. If all protocols are exhausted, the Job fails and Kubernetes retries via `backoffLimit`.

## Download verification

After each download, AIM Engine performs a two-stage verification:

1. **File presence check** — the downloader independently queries HuggingFace for the expected file list (honouring any download filters), then verifies every expected file exists on disk. Each verified file is explicitly `fsync`ed so it's been written to underlying storage — particularly important on network filesystems. Missing files fail the Job and trigger a retry.
2. **Integrity verification** — `hf cache verify` validates checksums against HuggingFace metadata. On failure, the local metadata cache is cleared by default so the retry performs a full fresh download rather than skipping files based on stale metadata.

Set `AIM_KEEP_METADATA_ON_FAILURE` to preserve the metadata cache on failure for debugging. See [Environment Variables](../reference/environment-variables.md).

## Troubleshooting

### Service stuck at `ProfileCacheReady=False / CachesNotReady`

One or more artifacts are still downloading or have failed. Drill down:

```bash
kubectl get aimprofilecache -l aim.eai.amd.com/service.name=<service-name> -n <namespace>
kubectl get aimartifact -l aim.eai.amd.com/profile-cache.name=<cache-name> -n <namespace>
kubectl get aimartifact <name> -o jsonpath='{.status.download}' | jq
```

Common causes:

- Registry authentication missing or invalid → check `spec.env` and any referenced secrets.
- Download size exceeds the artifact's declared `spec.size` → adjust.
- Network instability → the protocol fallback chain should handle it; check `status.download.protocolSequence`.

### Storage quota blocks a new artifact

```bash
kubectl get aimartifact <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq
```

A reason of `StorageQuotaExceeded` means no evictable artifacts could free enough space. Free space by deleting unused artifacts, raising the quota, or lowering retention priorities so existing artifacts become evictable.

### Cache stuck `NotAvailable / ProfileNotFound`

The profile the cache references has been deleted. If the service is also gone, delete the cache manually; otherwise check why the profile isn't reconciling.

## Related documentation

- [Profiles](profiles.md) — Self-contained runtime configurations (v1alpha2)
- [Services](services.md) — How `AIMService` triggers caching
- [Deploying Services](../guides/deploying-services.md) — Service-level caching configuration
- [Service Templates (v1alpha1)](../legacy/service-templates.md) — Legacy `AIMTemplateCache` flow
- [Runtime Configuration](runtime-config.md) — Cluster-wide downloader defaults
- [Storage Configuration](../admin/storage-configuration.md) — Storage classes, quotas, PVC sizing
