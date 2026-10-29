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
| `manager.runtimeProjectionMode` | Eager KServe `ServingRuntime` / `ClusterServingRuntime` projection mode for the profile reconcilers, rendered as the operator's `--runtime-projection-mode` argument. Exhaustive projects one scope-appropriate `ServingRuntime` or `ClusterServingRuntime` per projectable profile. Reduced projects one model-slug primary `ServingRuntime` or `ClusterServingRuntime` per model that native KServe references by name. Both runs the two together. All projected `ServingRuntime` and `ClusterServingRuntime` objects keep `autoSelect` off (all share one model format, so `autoSelect` would collide across models). The lazy namespaced `ServingRuntime`-keyed projection is always on and is not governed by this value. See the runtime-projection ADR (docs/adr). | `Both` |
| `manager.artifactDownloaderImage` | Override the artifact-downloader image the operator spawns for model download and size-check Jobs. Empty uses the public mirror image baked into the operator binary at build time. Set this to install from a private downloader mirror instead. Per-resource (spec.modelDownloadImage) and runtime-config overrides still take precedence over this install-time default. | `` |
| `manager.env` | Additional environment variables for the controller. Variables managed by dedicated chart values cannot be overridden here. | `[]` |
| `manager.extraVolumeMounts` | Additional volume mounts for the controller | `[]` |
| `manager.extraVolumes` | Additional volumes for the controller pod | `[]` |
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

Cluster-wide runtime configuration for AIM resources. By default the chart creates `AIMClusterRuntimeConfig/default` with public AIM base-image compatibility mappings and a generic NVIDIA vLLM fallback, so derived runtime images remain pullable and v1alpha2 `spec.modelId` onboarding can generate profiles immediately on clusters that advertise NVIDIA GPU resources. Disable this when the cluster already has a platform-managed config with the same name.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `clusterRuntimeConfig.enable` | Enable creation of the chart-managed AIMClusterRuntimeConfig resource | `true` |
| `clusterRuntimeConfig.name` | Name of the AIMClusterRuntimeConfig resource | `default` |
| `clusterRuntimeConfig.spec` | Spec fields for the AIMClusterRuntimeConfig. See [Runtime Configuration](../concepts/runtime-config.md) for details. The default fallback intentionally omits CPU and memory requests so profile generation is gated by the NVIDIA device resource, not a one-size-fits-all host-resource policy. Override the fallback to match your fleet and model sizes. Generated profiles remain truthfully `unoptimized`, while autoSelectionPolicy=any lets AIMServices use this runtime automatically only when no optimized profile matches. | Public AIM base-image compatibility mappings and an NVIDIA vLLM fallback for any NVIDIA GPU |

## clusterModelSource

Cluster-wide AIMClusterModelSource for automatic model discovery. Creates an AIMClusterModelSource CR when enabled. The kubebuilder helm/v2-alpha plugin always wraps the generated template in `{{- if .Values.clusterModelSource.enable }}`, so this block must exist even when the resource is not wanted -- otherwise `helm install` fails with `nil pointer evaluating interface {}.enable`.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `clusterModelSource.enable` | Enable creation of the AIMClusterModelSource resource. Off by default so a fresh `helm install` succeeds without needing a registry pull secret in the operator namespace. | `false` |
| `clusterModelSource.name` | Name of the AIMClusterModelSource resource | `default` |
| `clusterModelSource.spec` | Spec fields for the AIMClusterModelSource. See [AIMClusterModelSource](../concepts/model-sources.md). | `{}` (see examples below) |

## scaleFromZero

Configure scale-from-zero triggers and the gateway-specific activation collector.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `scaleFromZero.scalerAddress` | gRPC endpoint written to KEDA external metric triggers | `keda-otel-scaler.keda.svc:4318` |
| `scaleFromZero.cooldownSecondsPerGiMemory` | Seconds per GiB added to the default cooldown period  cooldownPeriod = clamp(300 + memGiB * cooldownSecondsPerGiMemory, 300, 1200)  Per-service `spec.autoScaling.cooldownPeriod` always wins. | `5` |
| `scaleFromZero.gatewayProvider` | Gateway implementation that supplies scale-from-zero activation metrics. Supported values are "none", "envoyGateway", "kgateway", and "custom". The default "none" installs no collector and makes minReplicas=0 invalid until a provider is selected. Known providers select both the controller's default metric contract and the collector rendered by Helm. "custom" requires external collector management and an activationMetricQueryTemplate on a service or its RuntimeConfig. | `none` |
| `scaleFromZero.gatewayMetricsCollector` | OpenTelemetry collector that forwards gateway activation metrics to keda-otel-scaler. Envoy Gateway's EnvoyExtensionPolicy and source-side delta OTLP sink are managed separately on the platform-owned Gateway. |  |
| `scaleFromZero.gatewayMetricsCollector.management` | Collector ownership. Use "helm" to render the collector selected by gatewayProvider, or "external" when platform infrastructure manages it. | `helm` |
| `scaleFromZero.gatewayMetricsCollector.otlpEndpoint` | OTLP gRPC endpoint for keda-otel-add-on | `keda-otel-scaler.keda.svc:4317` |
| `scaleFromZero.gatewayMetricsCollector.scrapeInterval` | Metrics scrape interval used only by the kgateway collector. Envoy Gateway pushes source-side deltas over OTLP instead of being scraped. | `1s` |
| `scaleFromZero.gatewayMetricsCollector.replicas` | Number of collector replicas. Envoy Gateway can use multiple OTLP receivers for availability. Keep this at 1 for kgateway because its Prometheus receivers would scrape and export the same series repeatedly. | `1` |
| `scaleFromZero.gatewayMetricsCollector.kgateway.gatewayName` | Value of the gateway.networking.k8s.io/gateway-name label on the kgateway proxy pods. Used only when gatewayProvider is "kgateway". | `kserve-ingress-gateway` |
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
| `acceleratorDetector.nvidia` | NVIDIA GPU node detection. Uses a thin Python image; nvidia-smi and the driver libraries are injected at runtime by the nvidia-container-toolkit (NVIDIA_VISIBLE_DEVICES=all + NVIDIA_DRIVER_CAPABILITIES=utility under the nvidia runtime), so no CUDA/driver is baked into the image. Writes NFD labels like feature.node.kubernetes.io/aim-accelerator.H100=8 and feature.node.kubernetes.io/aim-accelerator.vendor.GPU.NVIDIA=8. Only scheduled on nodes labelled feature.node.kubernetes.io/pci-10de.present (set by the NVIDIA GPU Operator's NFD config). It won't deploy anywhere on a GPU-free or AMD-only cluster, so it is safe to leave enabled by default. |  |
| `acceleratorDetector.nvidia.enable` | Enable NVIDIA accelerator detection DaemonSet | `true` |
| `acceleratorDetector.nvidia.image.repository` | NVIDIA detector image repository (thin Python image) | `docker.io/library/python` |
| `acceleratorDetector.nvidia.image.tag` | NVIDIA detector image tag | `3-slim` |
| `acceleratorDetector.nvidia.image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `acceleratorDetector.nvidia.imagePullSecrets` | Secrets for pulling the NVIDIA detector image from private registries | `[]` |
| `acceleratorDetector.nvidia.runtimeClassName` | RuntimeClass that routes the pod through the nvidia-container-toolkit so nvidia-smi/libnvidia-ml are injected. The NVIDIA GPU Operator creates a RuntimeClass named "nvidia". Set to empty ("") to rely on the node default runtime instead. | `nvidia` |
| `acceleratorDetector.nvidia.nodeSelector` | Node selector to target NVIDIA GPU nodes. Default matches the NVIDIA GPU Operator's vendor-only PCI label; override for vanilla-NFD clusters that emit class+vendor keys (e.g. pci-0302_10de.present). | `{feature.node.kubernetes.io/pci-10de.present: "true"}` |
| `acceleratorDetector.nvidia.tolerations` | Tolerations for NVIDIA GPU nodes (defaults to tolerate all taints) | `[{operator: Exists}]` |
| `acceleratorDetector.nvidia.resources` | Resource limits and requests for NVIDIA detector pods |  |

