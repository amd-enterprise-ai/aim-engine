# Model Caching

Model caching pre-downloads model artifacts to shared persistent volumes, reducing startup time and bandwidth usage across service replicas and restarts.

## Caching Modes

Control caching behavior with `spec.caching.mode`:

| Mode | Behavior |
|------|----------|
| `Shared` | Reuses shared cache assets across services that use the same template. This is the **default**. |
| `Dedicated` | Creates service-owned dedicated cache assets, isolated from other services. |

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  caching:
    mode: Shared
```

!!! note
    The caching mode is immutable after creation. Legacy values `Always`, `Auto`, and `Never` are accepted for backward compatibility (`Always`/`Auto` map to `Shared`, `Never` maps to `Dedicated`).

## How Caching Works

When caching is active, AIM Engine creates a hierarchy of resources:

1. **AIMTemplateCache** — Groups all model artifacts for a specific template on a shared PVC
2. **AIMArtifact** — Manages the download of individual model sources
3. **PVC + Download Job** — The actual storage and download execution

The template cache is owned by the template (not the service), so multiple services sharing the same template reuse the same cache.

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

## Monitoring Cache Status

Check the status of template caches and artifacts:

```bash
# List template caches
kubectl get aimtemplatecache -n <namespace>

# List artifacts
kubectl get aimartifact -n <namespace>

# Check artifact download progress
kubectl get aimartifact <name> -n <namespace> -o jsonpath='{.status}' | jq
```

## Next Steps

- [Model Caching Concepts](../concepts/caching.md) — Cache hierarchy, ownership, and deletion behavior
- [Storage Configuration](../admin/storage-configuration.md) — PVC and storage class setup
- [Environment Variables](../reference/environment-variables.md) — Downloader configuration
