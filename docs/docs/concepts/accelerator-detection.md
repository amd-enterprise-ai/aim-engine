# Accelerator Detection

The AcceleratorDetector automatically detects hardware accelerators on cluster nodes and publishes the results as Kubernetes node labels via [Node Feature Discovery (NFD)](https://nfd.sigs.k8s.io/). AIM Engine uses these labels to schedule inference workloads onto appropriate hardware. The AcceleratorDetector is enabled by default.

The labels feed two different consumers depending on API version:

- **v1alpha2** — `AIMProfile.spec.acceleratorModel` is matched against the `feature.node.kubernetes.io/aim-accelerator.<model>` labels using the `Exists` operator. The match drives both profile `HardwareAvailable` reporting and inference-pod node affinity.
- **v1alpha1** — `AIMServiceTemplate.spec.hardware.gpu.model` feeds the same label system. Existing templates continue to work.

## How It Works

Two DaemonSets detect hardware on each node, write the results to NFD's local feature file directory, and NFD publishes them as node labels. The GPU detector runs `aim-runtime detect-hardware` for the accelerator model and, independently, `amd-smi partition` for the GPU partition state; the CPU detector reads `/proc/cpuinfo`.

```
Node boots
  → AcceleratorDetector pod starts
  → GPU: runs `aim-runtime detect-hardware` (model) + `amd-smi partition` (partition state)
    CPU: reads /proc/cpuinfo
  → Writes feature file(s) to /etc/kubernetes/node-feature-discovery/features.d/
  → NFD publishes node labels:
      feature.node.kubernetes.io/aim-accelerator.MI300X=8
      feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.CPX-NPS4=64
  → AIM Engine matches profiles (or v1alpha1 templates) to nodes via label affinity
```

Detection runs periodically (default: every 10 seconds) to keep labels — including GPU partition state — current. The end-to-end latency floor is NFD's own scan interval, not `detectInterval`.

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

AIM Engine constructs node affinity from `AIMProfile.spec.acceleratorModel` (or v1alpha1 `AIMServiceTemplate.spec.hardware.gpu.model`) using the `Exists` operator, without requiring any knowledge of hardware specifics. The label value (accelerator count) is informational only; actual capacity is enforced via the computed device resource request — see [Profiles — Accelerator and node affinity](profiles.md#accelerator-and-node-affinity).

:::{note}
Architecture-level labels for fallback profile matching (e.g. `aim-accelerator.CDNA3`, `aim-accelerator.EPYC_ZEN5`) will be supported once `aim-runtime` returns the full identifier hierarchy.
:::
## GPU Partition Scheme Labels

On GPU nodes the detector also reads the current partition state from `amd-smi partition --current --json` and publishes it on a single partition axis, `feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.*`. The kernel-applied state reported by `amd-smi` is the single source of truth — AMD GPU Operator / DCM labels are **not** consulted, because they can lag or disagree with the kernel.

Two kinds of value appear on this axis:

- A hardware-agnostic **`default`** sentinel meaning "canonical unpartitioned state, or hardware that doesn't expose partitioning at all".
- Explicit **`<Compute>-<Memory>`** scheme names (e.g. `CPX-NPS4`) for actively partitioned states, and `SPX-NPS1` for the canonical unpartitioned state on partitionable hardware.

| Node state | Labels |
|------------|--------|
| 8-GPU MI300X, unpartitioned (`SPX+NPS1`) | `aim-accelerator.MI300X=8`, `partitioning-scheme.default=8`, `partitioning-scheme.SPX-NPS1=8` |
| 8-GPU MI300X, `CPX+NPS4` (64 partitions) | `aim-accelerator.MI300X=8`, `partitioning-scheme.CPX-NPS4=64` |
| Non-partitionable hardware (e.g. Radeon) | `aim-accelerator.RadeonW7900=4`, `partitioning-scheme.default=4` |

The label *value* is the number of schedulable units in that bucket and is informational only. `AIMProfile.spec.acceleratorPartitioningMode` matches on the label *key* via `Exists` / `DoesNotExist`; see [Profiles — Accelerator and node affinity](profiles.md#accelerator-and-node-affinity).

Partition detection runs **independently of `detect-hardware`**: the scheme comes straight from `amd-smi partition --current --json`, so partition labels are published even on accelerator images that don't ship the `detect-hardware` command — and, conversely, a partition failure never suppresses the model/family labels (the two axes live in separate feature files; see [NFD Integration](#nfd-integration)). It is best-effort: when `amd-smi` is missing/old, times out, or returns unparseable output, the detector falls back to `partitioning-scheme.default` if it otherwise knows GPUs are present, and otherwise preserves the last-good partition labels rather than dropping them. The GPU detector image must ship an `amd-smi` that supports `partition --current --json`.

:::{note}
This iteration assumes the AMD GPU Operator's `resource_naming_strategy: single` (every GPU/partition advertised as `amd.com/gpu`). The partition labels themselves are strategy-independent (the detector reads `amd-smi`, not the device plugin), but partition-aware scheduling is only supported under `single` today.
:::
## DaemonSets

| DaemonSet | Image | Target Nodes | Detects |
|-----------|-------|--------------|---------|
| GPU | `aim-base` (includes ROCm) | Nodes with `feature.node.kubernetes.io/amd-gpu=true` | AMD Instinct GPUs via `amdsmi` |
| CPU | `aim-epyc-base` (no ROCm) | Nodes without the `amd-gpu` label | AMD EPYC CPUs via `/proc/cpuinfo` |

The GPU DaemonSet uses a `nodeSelector` on the `amd-gpu` label (set by the AMD GPU Operator). The CPU DaemonSet uses a `nodeAffinity` rule to exclude nodes where that label is present. The two are mutually exclusive per node.

Both DaemonSets are independently configurable via Helm values.

### NFD Integration

Feature files are written to `/etc/kubernetes/node-feature-discovery/features.d/`. The CPU detector writes `aim-accelerator-cpu`; the GPU detector writes **two** files — `aim-accelerator-gpu` for the model/family labels (from `detect-hardware`) and `aim-accelerator-gpu-partition` for the partition axis (from `amd-smi`). NFD's [local source](https://nfd.sigs.k8s.io/usage/customization-guide#local-feature-source) picks up every file and merges the resulting labels onto the node. Splitting the model and partition axes into separate files lets the CPU and GPU detectors co-exist on heterogeneous nodes without clobbering each other, and gives each axis an independent lifecycle so a failure of one never overwrites the other's labels. Writes use an atomic rename to avoid race conditions with the NFD worker.

Labels are **sticky across transient failures**: a detection failure (the underlying tool is missing, exits non-zero, times out, or returns unparseable output) is treated as distinct from a confident "no accelerators here". On failure the detector leaves the relevant last-good feature file untouched and retries on the next cycle, rather than truncating it to empty. Because the model and partition axes are separate files, this applies to each independently — a transient `amd-smi` failure can't drop the model labels, and an absent `detect-hardware` can't drop the partition labels. It prevents a single transient hiccup from flapping a node's `aim-accelerator.*` labels and every consumer's profile/model availability.

## Prerequisites

- **Node Feature Discovery (NFD)** must be installed on the cluster. NFD is included with the AMD GPU Operator.
- **Container images** must be accessible. The GPU DaemonSet uses `aim-base`; the CPU DaemonSet uses `aim-epyc-base`.

## Configuration

The AcceleratorDetector is enabled by default. Configure it in your Helm `values.yaml`:

```yaml
acceleratorDetector:
  enable: true
  detectInterval: 10

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

- [Profiles — Accelerator and node affinity](profiles.md#accelerator-and-node-affinity) — How profiles consume detector labels
- [Service Templates (v1alpha1)](../legacy/service-templates.md) — Hardware-resolution path for legacy templates
- [Naming and Labels](../reference/naming-and-labels.md) — Label reference
- [GPU Management](../admin/gpu-management.md) — Existing GPU label system
