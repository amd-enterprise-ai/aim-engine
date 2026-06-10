# Helm Chart Values

Reference for all configurable values in the AIM Engine Helm chart.

<!-- Auto-generated from config/helm/values.yaml by hack/generate-helm-values-docs.go. Do not edit manually. -->

## Controller Manager

Controller manager configuration

| Parameter | Description | Default |
|-----------|-------------|----------|
| `manager.replicas` | Number of operator replicas | `1` |
| `manager.image.repository` | Operator container image repository | `docker.io/amdenterpriseai/aim-engine` |
| `manager.image.tag` | Operator container image tag | `latest` |
| `manager.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `manager.imagePullSecrets` | Secrets for pulling the operator image from private registries | `[]` |
| `manager.args` | Controller command-line arguments | `["--leader-elect"]` |
| `manager.env` | Additional environment variables for the controller | `[]` |
| `manager.podSecurityContext.runAsNonRoot` | Require non-root user | `true` |
| `manager.podSecurityContext.seccompProfile.type` | Seccomp profile type | `RuntimeDefault` |
| `manager.securityContext.allowPrivilegeEscalation` | Prevent privilege escalation | `false` |
| `manager.securityContext.capabilities.drop` | Dropped Linux capabilities | `["ALL"]` |
| `manager.securityContext.readOnlyRootFilesystem` | Read-only root filesystem | `true` |
| `manager.resources.limits.memory` | Memory limit | `4Gi` |
| `manager.resources.requests.cpu` | CPU request | `100m` |
| `manager.resources.requests.memory` | Memory request | `256Mi` |

## RBAC Helpers

Create admin/editor/viewer ClusterRoles for each CRD

| Parameter | Description | Default |
|-----------|-------------|----------|
| `rbacHelpers.enable` | Enable RBAC helper roles | `true` |

## CRDs

Custom Resource Definitions

| Parameter | Description | Default |
|-----------|-------------|----------|
| `crd.enable` | Install CRDs with the chart | `true` |
| `crd.keep` | Keep CRDs when uninstalling (prevents data loss) | `true` |

## Metrics

Controller metrics endpoint

| Parameter | Description | Default |
|-----------|-------------|----------|
| `metrics.enable` | Enable metrics endpoint | `true` |
| `metrics.port` | Metrics endpoint port | `8443` |

## Cert-Manager

Cert-manager integration for TLS certificates

| Parameter | Description | Default |
|-----------|-------------|----------|
| `certManager.enable` | Enable cert-manager integration | `false` |

## Prometheus

Prometheus ServiceMonitor for metrics scraping

| Parameter | Description | Default |
|-----------|-------------|----------|
| `prometheus.enable` | Create a Prometheus ServiceMonitor resource | `false` |

## Cluster Runtime Configuration

Cluster-wide runtime configuration for AIM resources. Creates an AIMClusterRuntimeConfig CR when enabled.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `clusterRuntimeConfig.enable` | Enable creation of the AIMClusterRuntimeConfig resource | `false` |
| `clusterRuntimeConfig.name` | Name of the AIMClusterRuntimeConfig resource | `default` |

## acceleratorDetector

AcceleratorDetector DaemonSets for hardware detection via NFD. Detects GPU and CPU accelerators on cluster nodes and writes NFD feature files so that AIM profiles can target specific hardware. Requires NFD (Node Feature Discovery) to be installed on the cluster.  GPU nodes additionally publish current partition state under feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.* by reading `amd-smi partition --current --json`. The GPU detector image must therefore ship an amd-smi build that supports `partition --current --json`.  changing is dominated by NFD's own scan interval, NOT detectInterval below. For timely partition labels, lower NFD's local-source scan interval to match (e.g. nfd-worker core.sleepInterval / -sleep-interval ~10s). NFD is an external prerequisite of this chart and is configured in the NFD release.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `acceleratorDetector.enable` | Enable the AcceleratorDetector DaemonSets | `true` |
| `acceleratorDetector.detectInterval` | Seconds between re-detection cycles. Lowered to 10s for low-latency partition-state labels; `amd-smi partition --current --json` is ~0.6s so 10s polling is essentially free. The effective floor is NFD's scan interval. | `10` |
| `acceleratorDetector.gpu` | GPU node detection (uses aim-base image with ROCm/amdsmi). Detects AMD Instinct GPUs and writes NFD labels like feature.node.kubernetes.io/aim-accelerator.MI300X=8 Only scheduled on nodes with feature.node.kubernetes.io/amd-gpu=true (set by the AMD GPU Operator NFD rule). |  |
| `acceleratorDetector.gpu.enable` | Enable GPU accelerator detection DaemonSet | `true` |
| `acceleratorDetector.gpu.image.repository` | GPU detector image repository (aim-base) | `docker.io/amdenterpriseai/aim-base` |
| `acceleratorDetector.gpu.image.tag` | GPU detector image tag | `0.11` |
| `acceleratorDetector.gpu.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `acceleratorDetector.gpu.imagePullSecrets` | Secrets for pulling the GPU detector image from private registries | `[]` |
| `acceleratorDetector.gpu.nodeSelector` | Node selector to target GPU nodes (requires AMD GPU Operator NFD rule) | `{feature.node.kubernetes.io/amd-gpu: "true"}` |
| `acceleratorDetector.gpu.tolerations` | Tolerations for GPU nodes (defaults to tolerate all taints) | `[{operator: Exists}]` |
| `acceleratorDetector.gpu.resources` | Resource limits and requests for GPU detector pods |  |
| `acceleratorDetector.cpu` | CPU node detection (uses aim-epyc-base image, lighter, no ROCm). Detects AMD EPYC CPUs and writes NFD labels like feature.node.kubernetes.io/aim-accelerator.EPYC_9965=128 Only scheduled on nodes WITHOUT feature.node.kubernetes.io/amd-gpu label (i.e. CPU-only nodes). |  |
| `acceleratorDetector.cpu.enable` | Enable CPU accelerator detection DaemonSet | `true` |
| `acceleratorDetector.cpu.image.repository` | CPU detector image repository (aim-epyc-base) | `docker.io/amdenterpriseai/aim-epyc-base` |
| `acceleratorDetector.cpu.image.tag` | CPU detector image tag | `0.11` |
| `acceleratorDetector.cpu.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `acceleratorDetector.cpu.imagePullSecrets` | Secrets for pulling the CPU detector image from private registries | `[]` |
| `acceleratorDetector.cpu.nodeSelector` | Node selector for CPU-only nodes (no additional selector needed; the DaemonSet uses nodeAffinity DoesNotExist on the amd-gpu label) | `{}` |
| `acceleratorDetector.cpu.tolerations` | Tolerations for CPU detector pods (defaults to tolerate all taints) | `[{operator: Exists}]` |
| `acceleratorDetector.cpu.resources` | Resource limits and requests for CPU detector pods |  |

