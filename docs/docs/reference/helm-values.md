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

## clusterModelSource

Cluster-wide AIMClusterModelSource for automatic model discovery. Creates an AIMClusterModelSource CR when enabled. The kubebuilder helm/v2-alpha plugin always wraps the generated template in `{{- if .Values.clusterModelSource.enable }}`, so this block must exist even when the resource is not wanted -- otherwise `helm install` fails with `nil pointer evaluating interface {}.enable`.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `clusterModelSource.enable` | Enable creation of the AIMClusterModelSource resource. Off by default so a fresh `helm install` succeeds without needing a registry pull secret in the operator namespace. | `false` |
| `clusterModelSource.name` | Name of the AIMClusterModelSource resource | `default` |
| `clusterModelSource.spec` | Spec fields for the AIMClusterModelSource. See [AIMClusterModelSource](../../concepts/model-discovery.md). | `{}` (see examples below) |

## scaleFromZero

Controller-side tuning for scale-from-zero. The feature itself is always on; these knobs adjust how the controller authors KEDA cluster-wide activation collector ships with the chart. See `docs/docs/guides/scaling-and-autoscaling.md` for the full design.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `scaleFromZero.scalerAddress` | gRPC endpoint of the keda-otel-add-on scaler. Used for both gateway-rate activation metrics and in-pod vLLM metrics. Default matches a stock keda-otel-add-on install in the `keda` namespace. | `keda-otel-scaler.keda.svc:4318` |
| `scaleFromZero.cooldownSecondsPerGiMemory` | Seconds-per-GiB multiplier in the cooldown heuristic  cooldownPeriod = clamp(300 + memGiB * cooldownSecondsPerGiMemory, 300, 1200)  The default (5) budgets for ~1.6 GB/s warm-cache throughput plus faster (NVMe/hugepages) -> 2-3; slower (NFS/network PVC) -> 8-12; CPU-only inference -> 0 to disable the memory contribution. Per-service `spec.autoScaling.cooldownPeriod` always wins. | `5` |
| `scaleFromZero.gatewayMetricsCollector` | Cluster-singleton OpenTelemetry collector that scrapes kgateway data-plane Envoy stats and forwards the gateway request-rate counter to keda-otel-scaler. This is the only activation signal an AIMService with scale-from-zero to ever wake a service back up. Deployed into the release namespace. Requires the OpenTelemetry Operator CRDs on the cluster. |  |
| `scaleFromZero.gatewayMetricsCollector.enable` | Ship and manage the gateway-metrics collector with the chart. Disable if you install it out-of-band (see `config/prereqs/scale-from-zero/`) or the OpenTelemetry Operator is not present, in which case `helm install` would fail on the OpenTelemetryCollector CR. | `true` |
| `scaleFromZero.gatewayMetricsCollector.otlpEndpoint` | OTLP gRPC endpoint of the keda-otel-add-on receiver the collector pushes the gateway-rate metric to. Must resolve from the collector's (release) namespace. | `keda-otel-scaler.keda.svc:4317` |
| `scaleFromZero.gatewayMetricsCollector.gatewayName` | Value of the `gateway.networking.k8s.io/gateway-name` label on your kgateway data-plane pods; selects which gateways are scraped. | `kserve-ingress-gateway` |
| `scaleFromZero.gatewayMetricsCollector.scrapeInterval` | Collector scrape interval. Keep well below the controller's scaler pollingInterval so a single request between polls stays visible. | `1s` |
| `scaleFromZero.gatewayMetricsCollector.replicas` | Number of collector replicas | `1` |
| `scaleFromZero.gatewayMetricsCollector.resources` | Resource requests/limits for the collector pod |  |

## acceleratorDetector

AcceleratorDetector DaemonSets for hardware detection via NFD. Detects GPU and CPU accelerators on cluster nodes and writes NFD feature files so that AIM profiles can target specific hardware. Requires NFD (Node Feature Discovery) to be installed on the cluster.  GPU nodes additionally publish current partition state under feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.* by reading `amd-smi partition --current --json`. The GPU detector image must therefore ship an amd-smi build that supports `partition --current --json`.  changing is dominated by NFD's own scan interval, NOT detectInterval below. For timely partition labels, lower NFD's local-source scan interval to match (e.g. nfd-worker core.sleepInterval / -sleep-interval ~10s). NFD is an external prerequisite of this chart and is configured in the NFD release.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `acceleratorDetector.enable` | Enable the AcceleratorDetector DaemonSets | `true` |
| `acceleratorDetector.detectInterval` | Seconds between re-detection cycles. Lowered to 10s for low-latency partition-state labels; `amd-smi partition --current --json` is ~0.6s so 10s polling is essentially free. The effective floor is NFD's scan interval. | `10` |
| `acceleratorDetector.gpu` | GPU node detection (uses aim-base image with ROCm/amdsmi). Detects AMD Instinct GPUs and writes NFD labels like feature.node.kubernetes.io/aim-accelerator.MI300X=8 Only scheduled on nodes with feature.node.kubernetes.io/amd-gpu=true (set by the AMD GPU Operator NFD rule). |  |
| `acceleratorDetector.gpu.enable` | Enable GPU accelerator detection DaemonSet | `true` |
| `acceleratorDetector.gpu.image.repository` | GPU detector image repository (aim-base) | `docker.io/amdenterpriseai/aim-base` |
| `acceleratorDetector.gpu.image.tag` | GPU detector image tag | `0.12` |
| `acceleratorDetector.gpu.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `acceleratorDetector.gpu.imagePullSecrets` | Secrets for pulling the GPU detector image from private registries | `[]` |
| `acceleratorDetector.gpu.nodeSelector` | Node selector to target GPU nodes (requires AMD GPU Operator NFD rule) | `{feature.node.kubernetes.io/amd-gpu: "true"}` |
| `acceleratorDetector.gpu.tolerations` | Tolerations for GPU nodes (defaults to tolerate all taints) | `[{operator: Exists}]` |
| `acceleratorDetector.gpu.resources` | Resource limits and requests for GPU detector pods |  |
| `acceleratorDetector.cpu` | CPU node detection (uses aim-epyc-base image, lighter, no ROCm). Detects AMD EPYC CPUs and writes NFD labels like feature.node.kubernetes.io/aim-accelerator.EPYC_9965=128 Only scheduled on nodes WITHOUT feature.node.kubernetes.io/amd-gpu label (i.e. CPU-only nodes). |  |
| `acceleratorDetector.cpu.enable` | Enable CPU accelerator detection DaemonSet | `true` |
| `acceleratorDetector.cpu.image.repository` | CPU detector image repository (aim-epyc-base) | `docker.io/amdenterpriseai/aim-epyc-base` |
| `acceleratorDetector.cpu.image.tag` | CPU detector image tag is released. The detector's runtime fallback (detect-and-label.py) already handles the 0.12 packaging change when that image lands. | `0.11` |
| `acceleratorDetector.cpu.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `acceleratorDetector.cpu.imagePullSecrets` | Secrets for pulling the CPU detector image from private registries | `[]` |
| `acceleratorDetector.cpu.nodeSelector` | Node selector for CPU-only nodes (no additional selector needed; the DaemonSet uses nodeAffinity DoesNotExist on the amd-gpu label) | `{}` |
| `acceleratorDetector.cpu.tolerations` | Tolerations for CPU detector pods (defaults to tolerate all taints) | `[{operator: Exists}]` |
| `acceleratorDetector.cpu.resources` | Resource limits and requests for CPU detector pods |  |

