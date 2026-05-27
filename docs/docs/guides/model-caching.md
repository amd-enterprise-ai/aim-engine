# Model Caching

Model caching pre-downloads model artifacts to shared persistent volumes, reducing startup time and bandwidth usage across service replicas and restarts.

!!! info "v1alpha2"
    Examples on this page use `aim.eai.amd.com/v1alpha2`. Caching is keyed by the **resolved profile** (via `AIMProfileCache`) instead of the v1alpha1 template (`AIMTemplateCache`). The `spec.caching.mode` field and its semantics are unchanged across versions. For the legacy template-based cache, see [Legacy AIMService](../legacy/aimservice-v1alpha1.md).

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
spec:
  model:
    name: qwen-qwen3-32b
  caching:
    mode: Shared
```

!!! note
    The caching mode is immutable after creation. Legacy values `Always`, `Auto`, and `Never` are accepted for backward compatibility (`Always`/`Auto` map to `Shared`, `Never` maps to `Dedicated`).

## How Caching Works

When caching is active, AIM Engine creates a hierarchy of resources:

1. **AIMProfileCache** (v1alpha2) — Groups all model artifacts for a specific resolved profile on a shared PVC. (v1alpha1 used `AIMTemplateCache`, keyed by template.)
2. **AIMArtifact** — Manages the download of individual model sources
3. **PVC + Download Job** — The actual storage and download execution

The profile cache is owned by the profile (not the service), so multiple services sharing the same profile reuse the same cache. With `caching.mode: Dedicated`, the cache is owned by the service and is garbage-collected with it.

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
