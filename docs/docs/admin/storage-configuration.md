# Storage Configuration

AIM Engine uses persistent volumes for model caching. This guide covers storage setup and sizing.

:::{admonition} v1alpha2 vs v1alpha1
:class: note

`AIMRuntimeConfig` / `AIMClusterRuntimeConfig` remain `aim.eai.amd.com/v1alpha1` resources — they are consumed by both the v1alpha2 profile pipeline and the v1alpha1 template pipeline unchanged. Storage fields (`spec.storage`, `spec.artifactStorageQuota`, `spec.artifact.defaultRetentionPriority`) are the same regardless of which pipeline reads them.
:::
## Requirements

Model caching requires `ReadWriteMany` (RWX) persistent volumes so that multiple pods can mount the same cached model data. You need a CSI driver that supports RWX access mode, such as:

- [Longhorn](https://longhorn.io/)
- [CephFS](https://docs.ceph.com/en/latest/cephfs/)
- [NFS-based provisioners](https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner)

## Default Storage Class

Set the default storage class for all AIM PVCs via cluster runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  storage:
    defaultStorageClassName: longhorn
```

Without this setting, AIM Engine uses the cluster's default storage class.

## PVC Headroom

AIM Engine sizes PVCs based on discovered model sizes plus a configurable headroom percentage. This accounts for filesystem overhead and temporary files during downloads.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  storage:
    pvcHeadroomPercent: 15
```

The default headroom is 10%. The final PVC size is rounded up to the nearest GiB.

## Storage Sizing Guidelines

Model storage requirements vary significantly:

| Model Size Category | Approximate Storage | Example |
|-------------------|-------------------|---------|
| Small (7-8B params) | 15-20 GiB | Qwen3 8B |
| Medium (30-70B params) | 60-140 GiB | Qwen3 32B, DeepSeek R1 70B |
| Large (100B+ params) | 200+ GiB | Mixtral 8x22B |

These are per-model estimates. A profile cache PVC (v1alpha2 `AIMProfileCache`, or v1alpha1 `AIMTemplateCache`) holds all model sources for the resolved profile / template.

## Monitoring Storage

Check PVC usage:

```bash
# List AIM-related PVCs
kubectl get pvc -l aim.eai.amd.com/artifact -n <namespace>

# Check artifact download status
kubectl get aimartifact -n <namespace>
```

## Storage Quotas

AIM Engine can enforce storage limits on artifact PVCs to prevent unbounded growth. Quotas are evaluated before creating PVCs -- when a new artifact would exceed the limit, it is either blocked or existing artifacts are evicted to make room.

### Namespace Quota

Set a per-namespace limit via annotation:

```bash
kubectl annotate namespace ml-team aim.eai.amd.com/artifact-storage-quota=100Gi
```

This limits the total allocated PVC storage for all AIMArtifacts in that namespace.

### Cluster-Wide Defaults

Configure default namespace limits and a cluster-wide cap via `AIMClusterRuntimeConfig`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  artifactStorageQuota:
    clusterLimit: 500Gi
    defaultNamespaceLimit: 100Gi
```

- **`clusterLimit`**: Maximum total PVC storage across all namespaces.
- **`defaultNamespaceLimit`**: Applied to namespaces that don't have the annotation. The namespace annotation takes precedence when both are set.

### Eviction Policy

When quota is exceeded and evictable artifacts exist, AIM Engine automatically deletes the lowest-priority artifacts to make room. Eviction eligibility requires:

1. The artifact has a `retentionPriority` (set explicitly or via `defaultRetentionPriority` in runtime config)
2. The artifact is `Shared` and `Ready`
3. The artifact is not in use by any `AIMProfileCache` (v1alpha2) or `AIMTemplateCache` (v1alpha1)
4. The artifact is not annotated with `aim.eai.amd.com/eviction-protected: "true"`

Lower `retentionPriority` values are evicted first. Among equal priorities, the oldest artifact is evicted first.

#### Default Retention Priority

To make all artifacts evictable by default without setting `retentionPriority` on each one:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  artifact:
    defaultRetentionPriority: 10
```

Artifacts with an explicit `spec.retentionPriority` override this default. Artifacts without either remain non-evictable.

#### Protecting Specific Artifacts

To exempt an artifact from eviction even when a default priority is configured:

```bash
kubectl annotate aimartifact important-model aim.eai.amd.com/eviction-protected=true
```

### Quota Status

When an artifact is blocked by quota, its status shows the reason:

```bash
kubectl get aimartifact -n ml-team
# STATUS column shows "Failed" for blocked artifacts

kubectl get aimartifact blocked-model -n ml-team -o yaml
# status.conditions includes:
#   - type: StorageQuotaExceeded
#     status: "True"
#     reason: NamespaceQuotaExceeded
#     message: "Namespace quota exceeded: 90Gi used + 20Gi needed > 100Gi limit"
```

This condition propagates up through `AIMProfileCache` / `AIMTemplateCache` and `AIMService`, so `kubectl get aimservice` shows the quota reason when a service is waiting for a blocked artifact.

## Cleanup

Profile cache PVCs are owned by `AIMProfileCache` resources (v1alpha2), which are in turn owned by the resolved `AIMProfile`. When a profile is deleted, its caches and PVCs are cleaned up automatically. For services that still use the v1alpha1 template pipeline, the equivalent owner is `AIMTemplateCache` (owned by the template).

To manually reclaim storage:

```bash
# v1alpha2: delete a profile cache (also deletes its PVCs and artifacts)
kubectl delete aimprofilecache <name> -n <namespace>

# v1alpha1: delete a template cache
kubectl delete aimtemplatecache <name> -n <namespace>
```

## Next Steps

- [Model Caching Guide](../guides/model-caching.md) — Caching modes and configuration
- [Model Caching Concepts](../concepts/caching.md) — Cache hierarchy and ownership
