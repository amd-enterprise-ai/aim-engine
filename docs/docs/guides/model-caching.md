# Model Caching

Model caching pre-downloads model artifacts to shared persistent volumes, reducing startup time and bandwidth usage across service replicas and restarts.

:::{admonition} v1alpha2
:class: note

Examples on this page use `aim.eai.amd.com/v1alpha2`. Caching is keyed by the **resolved profile** (via `AIMProfileCache`) instead of the v1alpha1 template (`AIMTemplateCache`). The `spec.caching.mode` field and its semantics are unchanged across versions. For the legacy template-based cache, see [Legacy AIMService](../legacy/aimservice-v1alpha1.md).
:::
## Caching Modes

Control caching behavior with `spec.caching.mode`:

| Mode | Behavior |
|------|----------|
| `Shared` | Reuses shared cache assets across services that resolve to the same profile. This is the **default**. |
| `Dedicated` | Creates service-owned dedicated cache assets, isolated from other services. |

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    name: qwen-qwen3-32b
  caching:
    mode: Shared
```

:::{note}
The caching mode is immutable after creation. Legacy values `Always`, `Auto`, and `Never` are accepted for backward compatibility (`Always`/`Auto` map to `Shared`, `Never` maps to `Dedicated`).
:::
## How Caching Works

When caching is active, AIM Engine creates a hierarchy of resources:

1. **AIMProfileCache** (v1alpha2) — Groups all model artifacts for a specific resolved profile on a shared PVC. (v1alpha1 used `AIMTemplateCache`, keyed by template.)
2. **AIMArtifact** — Manages the download of individual model sources
3. **PVC + Download Job** — The actual storage and download execution

When `AIMProfile.spec.caching.enabled` is set, the shared profile cache is owned
by the profile, so multiple services using that profile reuse it. A dedicated
cache is owned by its service and is garbage-collected with the service.

A profile-owned cache resolves the namespace or cluster RuntimeConfig named
`default`; `AIMProfile.spec.caching` has no `runtimeConfigName` selector.
Consequently, selecting a different RuntimeConfig on an `AIMService` does not
retarget that shared cache. Service-owned caches use the service's selected
RuntimeConfig.

## Download Protocols

For HuggingFace models (`hf://` sources), AIM Engine tries download protocols in sequence:

| Protocol | Description |
|----------|-------------|
| `XET` | XetHub protocol (fastest for large models) |
| `HF_TRANSFER` | HuggingFace Transfer (optimized multi-part download) |
| `HTTP` | Standard HTTP download (slowest, most compatible) |

The default order is `XET,HF_TRANSFER`. If a protocol fails, the downloader cleans up incomplete files and tries the next one.

Override the protocol order via runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "HF_TRANSFER,HTTP"
```

## Storage Sizing

AIM Engine automatically sizes PVCs based on discovered model sizes plus a headroom percentage (default 10%). Configure the headroom via cluster runtime config:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  storage:
    pvcHeadroomPercent: 15
    defaultStorageClassName: longhorn
```

## Repositories with required subdirectories

When no download filter is configured, AIM Engine excludes files in
subdirectories (equivalent to `exclude: ["*/*"]`). This is suitable for many
flat model repositories, but repository layouts such as Diffusers commonly
store required transformer, VAE, text-encoder, scheduler, and tokenizer files
under nested directories.

Set an explicitly empty filter to download all files:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: diffusers-model
  namespace: ml-team
spec:
  storage:
    downloadFilter: {}
```

For a service-owned cache, reference that RuntimeConfig from the relevant
`AIMService`:

```yaml
spec:
  runtimeConfigName: diffusers-model
```

For a profile-owned shared cache, configure the download filter on the
namespace or cluster RuntimeConfig named `default` instead. Avoid changing the
cluster-wide default unless every affected model needs nested files:
downloading them can increase transfer time and storage usage for unrelated
models.

This setting is based on repository layout, not inference engine. For example,
a Diffusers model served by vLLM-Omni needs it, while a flat repository served
by the same engine may not.

`AIMArtifact.spec.downloadFilter` is immutable. If an artifact was created with
the wrong filter, delete and recreate that artifact after changing the runtime
configuration.

## Download Verification

After each download completes, AIM Engine automatically verifies that all expected files are present on disk and persisted to storage. If verification fails, the download job retries with a clean state. No configuration is required — verification runs by default for all HuggingFace downloads.

For details on how verification works, see [Download Verification](../concepts/caching.md#download-verification).

## Monitoring Cache Status

Check the status of profile caches and artifacts:

```bash
# List profile caches (v1alpha2)
kubectl get aimprofilecache -n <namespace>

# List artifacts
kubectl get aimartifact -n <namespace>

# Check artifact download progress
kubectl get aimartifact <name> -n <namespace> -o jsonpath='{.status}' | jq
```

For services still managed by the v1alpha1 template pipeline, list caches with `kubectl get aimtemplatecache -n <namespace>` instead.

## Next Steps

- [Model Caching Concepts](../concepts/caching.md) — Cache hierarchy, ownership, and deletion behavior
- [Storage Configuration](../admin/storage-configuration.md) — PVC and storage class setup
- [Environment Variables](../reference/environment-variables.md) — Downloader configuration
