# Accelerator Detection

The AcceleratorDetector automatically detects hardware accelerators on cluster nodes and publishes the results as Kubernetes node labels via [Node Feature Discovery (NFD)](https://nfd.sigs.k8s.io/). AIM Engine uses these labels to schedule inference workloads onto appropriate hardware. The AcceleratorDetector is enabled by default.

## How It Works

Two DaemonSets run `aim-runtime detect-hardware` on each node, write the results to NFD's local feature file directory, and NFD publishes them as node labels.

```
Node boots
  → AcceleratorDetector pod starts
  → Runs aim-runtime detect-hardware
  → Writes feature file to /etc/kubernetes/node-feature-discovery/features.d/
  → NFD publishes node labels:
      feature.node.kubernetes.io/aim-accelerator.MI300X=8
  → AIM Engine matches profiles to nodes via label affinity
```

Detection runs periodically (default: every 5 minutes) to ensure labels stay current.

## Node Labels

Each detected accelerator model produces a label under `feature.node.kubernetes.io/aim-accelerator.`. The key encodes the model identifier; the value is the accelerator count.

**GPU node:**

```
feature.node.kubernetes.io/aim-accelerator.MI300X: "8"
```

**CPU-only node:**

```
feature.node.kubernetes.io/aim-accelerator.EPYC_9965: "128"
```

| Hardware | Example Label |
|----------|---------------|
| AMD Instinct MI300X (8 GPUs) | `aim-accelerator.MI300X=8` |
| AMD Instinct MI325X (8 GPUs) | `aim-accelerator.MI325X=8` |
| AMD EPYC 9965 (192 cores) | `aim-accelerator.EPYC_9965=192` |
| AMD EPYC 9575F (64 cores) | `aim-accelerator.EPYC_9575F=64` |

AIM Engine constructs node affinity from a profile's `accelerator_model` field using the `Exists` operator, without requiring any knowledge of hardware specifics.

!!! note
    Architecture-level labels for fallback profile matching (e.g. `aim-accelerator.CDNA3`, `aim-accelerator.EPYC_ZEN5`) will be supported once `aim-runtime` returns the full identifier hierarchy.

## DaemonSets

| DaemonSet | Image | Target Nodes | Detects |
|-----------|-------|--------------|---------|
| GPU | `aim-base` (includes ROCm) | Nodes with `feature.node.kubernetes.io/amd-gpu=true` | AMD Instinct GPUs via `amdsmi` |
| CPU | `aim-epyc-base` (no ROCm) | Nodes without the `amd-gpu` label | AMD EPYC CPUs via `/proc/cpuinfo` |

The GPU DaemonSet uses a `nodeSelector` on the `amd-gpu` label (set by the AMD GPU Operator). The CPU DaemonSet uses a `nodeAffinity` rule to exclude nodes where that label is present. The two are mutually exclusive per node.

Both DaemonSets are independently configurable via Helm values.

### NFD Integration

Feature files are written to `/etc/kubernetes/node-feature-discovery/features.d/aim-accelerator`. NFD's [local source](https://nfd.sigs.k8s.io/usage/customization-guide#local-feature-source) picks up these files and publishes their contents as node labels. Writes use an atomic rename to avoid race conditions with the NFD worker.

## Prerequisites

- **Node Feature Discovery (NFD)** must be installed on the cluster. NFD is included with the AMD GPU Operator.
- **Container images** must be accessible. The GPU DaemonSet uses `aim-base`; the CPU DaemonSet uses `aim-epyc-base`.

## Configuration

The AcceleratorDetector is enabled by default. Configure it in your Helm `values.yaml`:

```yaml
acceleratorDetector:
  enable: true
  detectInterval: 300

  gpu:
    enable: true
    image:
      repository: "your-registry/aim-base"
      tag: "latest"
    imagePullSecrets:
      - name: your-pull-secret

  cpu:
    enable: true
    image:
      repository: "your-registry/aim-epyc-base"
      tag: "latest"
    imagePullSecrets:
      - name: your-pull-secret
```

To disable entirely:

```yaml
acceleratorDetector:
  enable: false
```

To disable CPU detection on a GPU-only cluster:

```yaml
acceleratorDetector:
  cpu:
    enable: false
```

## Relationship to k8s-device-plugin

The AMD [k8s-device-plugin Node Labeller](https://github.com/ROCm/k8s-device-plugin) writes GPU labels under `amd.com/gpu.*` (e.g. `amd.com/gpu.device-id=74a1`). The AcceleratorDetector writes a separate set of labels under `feature.node.kubernetes.io/aim-accelerator.*`.

AIM Engine supports both label sets. When AcceleratorDetector labels are present they take precedence, with fallback to `amd.com/gpu.device-id` for backward compatibility. See [GPU Management](../admin/gpu-management.md) for details.

## Verifying Labels

```bash
kubectl get nodes -o json | jq -r '
  .items[] |
  .metadata.name as $name |
  [.metadata.labels | to_entries[] | select(.key | startswith("feature.node.kubernetes.io/aim-accelerator."))] |
  if length > 0 then "\($name): \(map("\(.key | split(".") | last)=\(.value)") | join(", "))" else empty end'
```

Or:

```bash
kubectl get nodes --show-labels | grep aim-accelerator
```

## See Also

- [Naming and Labels](../reference/naming-and-labels.md) — Label reference
- [GPU Management](../admin/gpu-management.md) — Existing GPU label system
