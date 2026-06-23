# GPU Management

:::{admonition} AcceleratorDetector
:class: note

AIM Engine includes an [AcceleratorDetector](../concepts/accelerator-detection.md) that detects GPUs and CPUs via NFD, writing labels under `feature.node.kubernetes.io/aim-accelerator.*`. It is enabled by default. The k8s-device-plugin labels documented below remain supported as a fallback.
:::
AIM Engine detects available GPUs in the cluster and uses this information for profile resolution (v1alpha2) / template selection (v1alpha1) and node scheduling.

## GPU Detection

AIM Engine detects GPUs through node labels set by the AMD GPU device plugin.

### AMD GPU Labels

| Label | Description | Example |
|-------|-------------|---------|
| `amd.com/gpu.device-id` | PCI device ID | `74a1` |
| `amd.com/gpu.family` | GPU family | `MI300X` |
| `amd.com/gpu.vram` | VRAM in MiB | `196608` |

Legacy labels with `beta.amd.com/` prefix are also supported.

## GPU-Aware Resolution

During [profile resolution](../concepts/services.md#resolution-shapes), AIM Engine filters profiles to only those whose required GPU is available in the cluster. A profile requiring MI325X GPUs is excluded if no MI325X nodes exist. The v1alpha1 template pipeline applies the same filter to candidate templates.

GPU preference scoring (highest to lowest): MI325X > MI300X > MI250X > MI210 > R9700 > W7900. Models outside this list tie at the bottom of the ranking.

## GPU Resource Requests

Profiles (v1alpha2) and templates (v1alpha1) specify GPU requirements that translate to Kubernetes resource requests:

::::{tab-set}
:::{tab-item} Profile (v1alpha2)
```yaml
# In an AIMProfile / AIMClusterProfile
spec:
  accelerator:
    model: MI300X
  resources:
    requests:
      amd.com/gpu: "4"
```
:::

:::{tab-item} Template (v1alpha1, deprecated)
```yaml
# In an AIMServiceTemplate
hardware:
  gpu:
    model: MI300X
    requests: 4
```
:::
::::

Both result in the inference pod requesting `amd.com/gpu: 4`. Profiles use standard Kubernetes `ResourceRequirements` directly, while templates use a simplified `hardware` abstraction.

## Node Affinity

AIM Engine automatically configures node affinity on inference pods to schedule them on nodes with the correct GPU. It matches the `amd.com/gpu.device-id` label against the device IDs for the required GPU model.

## Verifying GPU Availability

Check which GPU labels are present on your nodes:

```bash
kubectl get nodes -o custom-columns='NAME:.metadata.name,DEVICE_ID:.metadata.labels.amd\.com/gpu\.device-id,FAMILY:.metadata.labels.amd\.com/gpu\.family,VRAM:.metadata.labels.amd\.com/gpu\.vram'
```

If the AcceleratorDetector is deployed, also check for `aim-accelerator.*` labels:

```bash
kubectl get nodes --show-labels | grep aim-accelerator
```

## Next Steps

- [Accelerator Detection](../concepts/accelerator-detection.md) — Unified hardware detection and NFD labels
- [AIM Services](../concepts/services.md) — Profile resolution and ranking
- [Profiles](../concepts/profiles.md) — Self-contained runtime configurations and GPU requirements
- [Service Templates (v1alpha1)](../legacy/service-templates.md) — Legacy template pipeline reference
