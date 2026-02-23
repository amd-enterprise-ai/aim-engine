# GPU Management

AIM Engine detects available GPUs in the cluster and uses this information for template selection and node scheduling.

## GPU Detection

AIM Engine detects GPUs through node labels set by the AMD GPU device plugin.

### AMD GPU Labels

| Label | Description | Example |
|-------|-------------|---------|
| `amd.com/gpu.device-id` | PCI device ID | `74a1` |
| `amd.com/gpu.family` | GPU family | `MI300X` |
| `amd.com/gpu.vram` | VRAM in MiB | `196608` |

Legacy labels with `beta.amd.com/` prefix are also supported.

## Template Selection and GPUs

During [template auto-selection](../concepts/services.md#auto-selection), AIM Engine filters templates to only those whose required GPU is available in the cluster. A template requiring MI325X GPUs is excluded if no MI325X nodes exist.

GPU preference scoring (highest to lowest): MI325X > MI300X > MI250X > MI210.

## GPU Resource Requests

Templates specify GPU requirements that translate to Kubernetes resource requests:

```yaml
# In an AIMServiceTemplate profile
hardware:
  gpu:
    model: MI300X
    requests: 4
```

This results in the inference pod requesting `amd.com/gpu: 4`.

## Node Affinity

AIM Engine automatically configures node affinity on inference pods to schedule them on nodes with the correct GPU. It matches the `amd.com/gpu.device-id` label against the device IDs for the required GPU model.

## Verifying GPU Availability

Check which GPU labels are present on your nodes:

```bash
kubectl get nodes -o custom-columns='NAME:.metadata.name,DEVICE_ID:.metadata.labels.amd\.com/gpu\.device-id,FAMILY:.metadata.labels.amd\.com/gpu\.family,VRAM:.metadata.labels.amd\.com/gpu\.vram'
```

## Next Steps

- [AIM Services](../concepts/services.md) — Template selection algorithm
- [Service Templates](../concepts/templates.md) — Runtime profiles and GPU requirements
