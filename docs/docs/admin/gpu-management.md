# GPU Management

:::{admonition} AcceleratorDetector
:class: note

AIM Engine includes an
[AcceleratorDetector](../concepts/accelerator-detection.md) that detects AMD
GPUs, NVIDIA GPUs, and CPUs through NFD. It writes normalized labels under
`feature.node.kubernetes.io/aim-accelerator.*` and is enabled by default.
:::
AIM Engine uses detected accelerators for profile resolution, resource
requests, and inference-pod scheduling.

## GPU Detection

AIM Engine's detector DaemonSets publish a common label contract regardless of
GPU vendor:

| Label | Description | Example |
|---|---|---|
| `feature.node.kubernetes.io/aim-accelerator.<model>` | Accelerator model and count | `feature.node.kubernetes.io/aim-accelerator.MI300X=8` |
| `feature.node.kubernetes.io/aim-accelerator.vendor.GPU.<vendor>` | GPU vendor and count | `feature.node.kubernetes.io/aim-accelerator.vendor.GPU.NVIDIA=8` |
| `feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.<mode>` | GPU partition state | `feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.default=8` |
| `feature.node.kubernetes.io/aim-accelerator.mixed-models.GPU` | Number of distinct GPU models, present only when the node has more than one. GPU workloads are not scheduled onto these nodes; see [Nodes with mixed GPU models](../concepts/accelerator-detection.md#nodes-with-mixed-gpu-models) | `feature.node.kubernetes.io/aim-accelerator.mixed-models.GPU=2` |

AMD detection uses `aim-runtime detect-hardware` and `amd-smi`; NVIDIA
detection uses `nvidia-smi`. Product names are normalized to profile-compatible
tokens such as `MI300X`, `H100`, and `RTX4090`.

The device plugins separately advertise schedulable resources:

| Vendor | Kubernetes resource | Supporting operator |
|---|---|---|
| AMD | `amd.com/gpu` | AMD GPU Operator |
| NVIDIA | `nvidia.com/gpu` | NVIDIA GPU Operator |

The AMD device plugin may also publish compatibility metadata such as
`amd.com/gpu.device-id`, `amd.com/gpu.family`, and `amd.com/gpu.vram`.
Legacy labels with the `beta.amd.com/` prefix remain relevant to the deprecated
template pipeline; v1alpha2 profile matching uses the normalized
`aim-accelerator.*` labels.

## GPU-Aware Resolution

During [profile resolution](../concepts/services.md#resolution-shapes), AIM
Engine filters profiles to those whose required vendor, model, partition state,
and resource capacity are available. For example, an H100 profile is excluded
when only AMD nodes are present, and an MI325X profile is excluded when no
MI325X node exists.

When otherwise-equivalent profiles remain, AMD GPU preference scoring is
MI325X > MI300X > MI250X > MI210 > R9700 > W7900. Models outside this list,
including NVIDIA models, tie at the bottom and are resolved by the remaining
profile-ranking and deterministic name rules.

## GPU Resource Requests

`AIMProfile` and `AIMClusterProfile` use flat accelerator fields. AIM Engine
derives the vendor-specific device request from those fields:

::::{tab-set}
:::{tab-item} AMD profile
```yaml
# In an AIMProfile / AIMClusterProfile
spec:
  acceleratorVendor: amd
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 4
```
:::

:::{tab-item} NVIDIA profile
```yaml
# In an AIMProfile / AIMClusterProfile
spec:
  acceleratorVendor: nvidia
  acceleratorModel: H100
  acceleratorType: gpu
  acceleratorCount: 4
```
:::
::::

The AMD profile requests and limits `amd.com/gpu: 4`; the NVIDIA profile
requests and limits `nvidia.com/gpu: 4`. If `acceleratorVendor` is omitted, the
backward-compatible default is AMD. Explicit `spec.resources` entries are
merged over the derived defaults and win for the same resource key.

## Node Affinity

AIM Engine writes required node affinity from the profile's accelerator fields.
`acceleratorVendor` selects the normalized vendor label,
`acceleratorModel` selects the model label, and
`acceleratorPartitioningMode` selects the partition label. These requirements
are combined so a pod cannot cross vendors or land on an incompatible GPU
shape.

## Verifying GPU Availability

Check the normalized accelerator labels:

```bash
kubectl get nodes --show-labels | grep aim-accelerator
```

Check the GPU resources advertised by each device plugin:

```bash
kubectl get nodes \
  -o custom-columns='NAME:.metadata.name,AMD_GPU:.status.allocatable.amd\.com/gpu,NVIDIA_GPU:.status.allocatable.nvidia\.com/gpu'
```

If the expected detector labels are absent, inspect the matching AMD or NVIDIA
AcceleratorDetector DaemonSet and verify that NFD is running.

## Next Steps

- [Accelerator Detection](../concepts/accelerator-detection.md) — Unified hardware detection and NFD labels
- [AIM Services](../concepts/services.md) — Profile resolution and ranking
- [Profiles](../concepts/profiles.md) — Self-contained runtime configurations and GPU requirements
- [Installation](../getting-started/installation.md) — GPU operator prerequisites
