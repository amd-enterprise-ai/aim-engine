# Storage Configuration

AIM Engine uses persistent volumes for model caching. This guide covers storage setup and sizing.

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

These are per-model estimates. A template cache PVC holds all model sources for that template.

## Monitoring Storage

Check PVC usage:

```bash
# List AIM-related PVCs
kubectl get pvc -l aim.eai.amd.com/artifact -n <namespace>

# Check artifact download status
kubectl get aimartifact -n <namespace>
```

## Cleanup

Template cache PVCs are owned by `AIMTemplateCache` resources, which are owned by templates. When a template is deleted, its caches and PVCs are cleaned up automatically.

To manually reclaim storage:

```bash
# Delete a template cache (also deletes its PVCs and artifacts)
kubectl delete aimtemplatecache <name> -n <namespace>
```

## Next Steps

- [Model Caching Guide](../guides/model-caching.md) — Caching modes and configuration
- [Model Caching Concepts](../concepts/caching.md) — Cache hierarchy and ownership
