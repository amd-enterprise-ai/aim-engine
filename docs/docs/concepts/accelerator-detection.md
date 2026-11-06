# Accelerator Detection

The AcceleratorDetector automatically detects hardware accelerators on cluster nodes and publishes the results as Kubernetes node labels via [Node Feature Discovery (NFD)](https://nfd.sigs.k8s.io/). AIM Engine uses these labels to schedule inference workloads onto appropriate hardware. The AcceleratorDetector is enabled by default.

The labels feed two different consumers depending on API version:

- **v1alpha2** — `AIMProfile.spec.acceleratorModel` is matched against the `feature.node.kubernetes.io/aim-accelerator.<model>` labels using the `Exists` operator. The match drives both profile `HardwareAvailable` reporting and inference-pod node affinity.
- **v1alpha1** — `AIMServiceTemplate.spec.hardware.gpu.model` feeds the same label system. Existing templates continue to work.

## How It Works

DaemonSets detect hardware on each node, write the results to NFD's local feature file directory, and NFD publishes them as node labels. The AMD GPU detector runs `aim-runtime detect-hardware` for the accelerator model and, independently, `amd-smi partition` for the GPU partition state; the NVIDIA GPU detector runs `nvidia-smi`; the CPU detector reads `/proc/cpuinfo`. Each detector also stamps a per-accelerator-type **vendor** label (see [Vendor Labels](#vendor-labels)).

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

Detection runs periodically (Helm default `detectInterval: 10` seconds) to keep labels — including GPU partition state — current. The end-to-end latency floor is NFD's own scan interval, not `detectInterval`. (The bare `kustomize` manifests under `config/accelerator-detector/` ship a more conservative `300`s default; the Helm chart is the supported install path.)

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
| NVIDIA H100 (8 GPUs) | `aim-accelerator.H100=8` |
| NVIDIA A100 (8 GPUs) | `aim-accelerator.A100=8` |
| AMD EPYC 9965 (192 cores) | `aim-accelerator.EPYC_9965=192` |
| AMD EPYC 9575F (64 cores) | `aim-accelerator.EPYC_9575F=64` |

NVIDIA product names reported by `nvidia-smi` (e.g. `NVIDIA H100 80GB HBM3`, `NVIDIA A100-SXM4-80GB`) are normalized to a canonical token (`H100`, `A100`) for the label key. Consumer and workstation RTX cards are normalized by pattern too — `NVIDIA GeForce RTX 4090` → `RTX4090`, `NVIDIA RTX A6000` → `RTXA6000`, `NVIDIA RTX 6000 Ada Generation` → `RTX6000Ada` — so evaluating on a workstation GPU produces clean labels. Unrecognized names fall back to a sanitized slug and are logged so the token table can be extended.

:::{admonition} Consumer GPUs are for evaluation
:class: note

Consumer/workstation NVIDIA cards are detected and labelled (model, `GPU` family, and `vendor.GPU.NVIDIA`) so you can try AIM Engine on them, but detection does not imply every profile will run: consumer cards differ from datacenter parts in VRAM, interconnect (no NVLink), and features (e.g. no FP8 on Ampere consumer). Whether a given model serves is governed by the profile's own requirements, not the detector. Consumer cards have no MIG, so they report `partitioning-scheme.default`.
:::

AIM Engine constructs node affinity from `AIMProfile.spec.acceleratorModel` (or v1alpha1 `AIMServiceTemplate.spec.hardware.gpu.model`) using the `Exists` operator, without requiring any knowledge of hardware specifics. The label value (accelerator count) is informational only; actual capacity is enforced via the computed device resource request — see [Profiles — Accelerator and node affinity](profiles.md#accelerator-and-node-affinity).

:::{note}
Architecture-level labels for fallback profile matching (e.g. `aim-accelerator.CDNA3`, `aim-accelerator.EPYC_ZEN5`) will be supported once `aim-runtime` returns the full identifier hierarchy.
:::

## Vendor Labels

Each detector also publishes a **vendor** label, keyed per accelerator type, on the `feature.node.kubernetes.io/aim-accelerator.vendor.<TYPE>.<VENDOR>` axis. The vendor comes from the DaemonSet itself (each detector is vendor-specific), not from detection output.

Scoping the vendor by accelerator type lets a cross-vendor node carry one vendor key per type without conflict — for example, an NVIDIA GPU on an AMD EPYC host:

```
feature.node.kubernetes.io/aim-accelerator.vendor.GPU.NVIDIA: "8"
feature.node.kubernetes.io/aim-accelerator.vendor.CPU.AMD: "1"
```

| Node | Vendor labels |
|------|---------------|
| AMD MI300X ×8 | `aim-accelerator.vendor.GPU.AMD=8` |
| NVIDIA H100 ×8 | `aim-accelerator.vendor.GPU.NVIDIA=8` |
| AMD EPYC CPU-only | `aim-accelerator.vendor.CPU.AMD=192` |

Like the other labels, the value is the accelerator count and is informational; consumers match on the key via `Exists`. This iteration assumes a single vendor per accelerator type on a given node. A node can carry several GPU models; see [Nodes with mixed GPU models](#nodes-with-mixed-gpu-models).

:::{note}
`AIMProfile.spec.acceleratorVendor` consumes this label when resolving nodes
and the device-plugin resource. `amd` selects `amd.com/gpu`; `nvidia` selects
`nvidia.com/gpu`. A generic NVIDIA profile can omit `acceleratorModel` and
match any detected NVIDIA GPU node.
:::

## Nodes with mixed GPU models

A node can have more than one GPU model, for example AMD Instinct MI350P and Radeon AI PRO R9700 cards in the same server. The detector labels each model with its own count and adds a marker label whose value is the number of distinct GPU models:

```
feature.node.kubernetes.io/aim-accelerator.MI350P: "4"
feature.node.kubernetes.io/aim-accelerator.R9700: "2"
feature.node.kubernetes.io/aim-accelerator.GPU: "6"
feature.node.kubernetes.io/aim-accelerator.mixed-models.GPU: "2"
```

GPUs whose device ID the detector doesn't recognise get no model labels, and don't count in the `GPU` family label. They still count toward the marker, one model per device ID: the device plugin offers them as `amd.com/gpu` like any other GPU. A node with seven MI300X GPUs and one unrecognised GPU is labelled `MI300X=7` and `mixed-models.GPU=2`.

AIM Engine does not schedule device-plugin GPU workloads onto nodes with the `mixed-models.GPU` marker. The device-plugin resources (`amd.com/gpu`, `nvidia.com/gpu`) are a single pool per node, so a pod there could be given any of the node's GPUs, whatever model its profile targets. Every GPU profile's node affinity therefore includes a `mixed-models.GPU` `DoesNotExist` term, and these nodes appear in the profile's hardware mismatch reasons. CPU profiles are not affected.

AMD GPU nodes with mixed models can serve AIMServices through DRA (Dynamic Resource Allocation): install the AMD GPU DRA driver on them, and the services' ResourceClaims select devices by model. With the default `Auto` [accelerator allocation mode](runtime-config.md#accelerator-allocation), a service uses DRA when no device-plugin node has its GPUs. NVIDIA GPUs have no DRA path in AIM Engine, so NVIDIA nodes with mixed models serve no GPU workloads.

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
| GPU (AMD) | `aim-base` (includes ROCm) | Nodes with `feature.node.kubernetes.io/amd-gpu=true` | AMD Instinct GPUs via `amdsmi` |
| NVIDIA | `python:3-slim` (thin) | Nodes with `feature.node.kubernetes.io/pci-10de.present=true` | NVIDIA GPUs via `nvidia-smi` |
| CPU | `aim-epyc-base` (no ROCm) | Nodes without the `amd-gpu` label | AMD EPYC CPUs via `/proc/cpuinfo` |

The AMD GPU DaemonSet uses a `nodeSelector` on the `amd-gpu` label (set by the AMD GPU Operator); the NVIDIA DaemonSet uses a `nodeSelector` on `pci-10de.present` (10de is NVIDIA's PCI vendor ID, set by the NVIDIA GPU Operator's NFD config). The CPU DaemonSet targets the remaining nodes.

:::{admonition} CPU detector and NVIDIA GPU nodes
:class: note

The CPU detector (`aim-epyc-base`) runs on nodes without the `amd-gpu` label, which includes NVIDIA GPU nodes. That is fine — and useful — on an AMD-EPYC host with NVIDIA GPUs (it labels the host CPU). On a **non-AMD host** (e.g. Intel) with NVIDIA GPUs, the EPYC-oriented CPU detection won't produce meaningful CPU labels; this is a known limitation. It does not affect the NVIDIA GPU labels, which come from the separate NVIDIA DaemonSet.
:::

The NVIDIA detector does **not** bake in any CUDA/driver: `nvidia-smi` and `libnvidia-ml` are injected from the host driver by the nvidia-container-toolkit. The pod therefore runs under the `nvidia` RuntimeClass with `NVIDIA_VISIBLE_DEVICES=all` and `NVIDIA_DRIVER_CAPABILITIES=utility`, and it does not request a `nvidia.com/gpu` resource (so it lands on every GPU node without consuming a GPU slot). On clusters whose NFD emits class+vendor PCI keys (e.g. `pci-0302_10de.present`) rather than the operator's vendor-only key, override the `nodeSelector` via Helm.

All DaemonSets are independently configurable via Helm values.

:::{note}
NVIDIA MIG geometry is not yet detected. NVIDIA GPU nodes currently report only the `partitioning-scheme.default` sentinel, so they match unpartitioned profiles. Per-instance MIG partition labels are a planned follow-up.
:::

### NFD Integration

Feature files are written to `/etc/kubernetes/node-feature-discovery/features.d/`. The CPU detector writes `aim-accelerator-cpu`; the GPU detector writes **two** files — `aim-accelerator-gpu` for the model/family labels (from `detect-hardware`) and `aim-accelerator-gpu-partition` for the partition axis (from `amd-smi`). NFD's [local source](https://nfd.sigs.k8s.io/usage/customization-guide#local-feature-source) picks up every file and merges the resulting labels onto the node. Splitting the model and partition axes into separate files lets the CPU and GPU detectors co-exist on heterogeneous nodes without clobbering each other, and gives each axis an independent lifecycle so a failure of one never overwrites the other's labels. Writes use an atomic rename to avoid race conditions with the NFD worker.

Labels are **sticky across transient failures**: a detection failure (the underlying tool is missing, exits non-zero, times out, or returns unparseable output) is treated as distinct from a confident "no accelerators here". On failure the detector leaves the relevant last-good feature file untouched and retries on the next cycle, rather than truncating it to empty. Because the model and partition axes are separate files, this applies to each independently — a transient `amd-smi` failure can't drop the model labels, and an absent `detect-hardware` can't drop the partition labels. It prevents a single transient hiccup from flapping a node's `aim-accelerator.*` labels and every consumer's profile/model availability.

## Prerequisites

- **Node Feature Discovery (NFD)** must be installed on the cluster. NFD is included with the AMD GPU Operator and the NVIDIA GPU Operator.
- **Container images** must be accessible. The AMD GPU DaemonSet uses `aim-base`; the CPU DaemonSet uses `aim-epyc-base`; the NVIDIA DaemonSet uses a thin `python:3-slim` image.
- **For NVIDIA detection:** the nvidia-container-toolkit must be installed (it injects `nvidia-smi`), and a `nvidia` RuntimeClass should exist (created by the NVIDIA GPU Operator). Both are present on any cluster already running NVIDIA GPU workloads.

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

  nvidia:
    enable: true
    image:
      repository: "your-registry/python"
      tag: "3-slim"
    # RuntimeClass that routes the pod through the nvidia-container-toolkit so
    # nvidia-smi is injected. Set to "" to use the node's default runtime.
    runtimeClassName: nvidia
    imagePullSecrets:
      - name: your-pull-secret
```

The NVIDIA detector is enabled by default but only schedules on NVIDIA GPU nodes, so it is a no-op on AMD-only or GPU-free clusters.

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

AIM Engine uses AcceleratorDetector model and vendor labels for profile
matching. AMD model matching retains fallback support for
`amd.com/gpu.device-id`; NVIDIA profiles use the detector's NVIDIA vendor/model
labels together with capacity advertised as `nvidia.com/gpu`. See
[GPU Management](../admin/gpu-management.md) for details.

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
