# Installation Reference

This page covers advanced installation options for AIM Engine. For a basic install, see the [Getting Started guide](../getting-started/installation.md).

## Helm Chart Configuration

All configuration is done through Helm values. See [Helm Chart Values](../reference/helm-values.md) for the complete reference.

### Operator Resources

Adjust operator resource limits for larger clusters:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --create-namespace \
  --set manager.resources.limits.memory=8Gi \
  --set manager.resources.requests.memory=512Mi
```

### Leader Election

Leader election is enabled by default (`--leader-elect` in `manager.args`). This ensures only one operator instance is active when running multiple replicas for high availability.

### Metrics

The metrics endpoint is enabled by default on port 8443 with TLS. To disable TLS for the metrics endpoint:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --set 'manager.args={--leader-elect,--metrics-secure=false}'
```

### Accelerator Detection

The Helm chart deploys an [AcceleratorDetector](../concepts/accelerator-detection.md) as DaemonSets on cluster nodes. It detects GPUs and CPUs and publishes the results as node labels via NFD, which AIM Engine uses for workload scheduling. [Node Feature Discovery](https://nfd.sigs.k8s.io/) must be installed on the cluster (included with the AMD GPU Operator).

### CRD Management

CRDs are distributed as a separate Helm chart and should be installed before the operator. See [Installation](../getting-started/installation.md#1-install-crds).

### Scale-from-zero gateway metrics

AIM Engine adds a gateway activation trigger to services with
`spec.minReplicas: 0`. The chart defaults `scaleFromZero.gatewayProvider` to
`none`, which renders no collector and rejects scale-from-zero services until
a provider is selected. Set it to
`envoyGateway` or `kgateway`; the chart derives the controller metric contract
and renders the matching collector. For another implementation, select
`custom`, use external collector management, and configure
`scaleFromZero.activationMetricQueryTemplate` through a RuntimeConfig or the
individual AIMService.

With Envoy Gateway, the shared `EnvoyExtensionPolicy` and the `EnvoyProxy`
source-side delta sink remain platform-owned because they configure a specific
Gateway. Set
`scaleFromZero.gatewayMetricsCollector.management=external` only when the
matching collector is also managed as platform infrastructure. See
[Optional Envoy Gateway scale-from-zero](envoy-gateway-scale-from-zero.md)
for installation, customization, and security guidance.

The chart also exposes these scale-from-zero controller-config values:

| Value | Default | Purpose |
|---|---|---|
| `scaleFromZero.scalerAddress` | `keda-otel-scaler.keda.svc:4318` | gRPC endpoint the controller writes into every external KEDA trigger it authors. |
| `scaleFromZero.receiverEndpoint` | `keda-otel-scaler.keda.svc:4317` | OTLP gRPC endpoint that the metrics collector sidecars of services with `spec.autoScaling.metrics` send the pod metrics to. Point it at the same keda-otel-add-on instance as `scalerAddress`. |
| `scaleFromZero.cooldownSecondsPerGiMemory` | `5` | Seconds-per-GiB multiplier used to derive each service's `cooldownPeriod` from the predictor's memory request (`cooldownPeriod = clamp(300 + memGiB × this, 300, 1200)`). Default budgets for ~1.6 GB/s warm-cache throughput; tune lower for faster storage (local NVMe, hugepages) or higher for slower storage (NFS, network PVC). The 300 s floor ensures every predictor gets enough cooldown to absorb a real warmup; the 1200 s ceiling caps the worst-case overcooling on multi-GPU services. See the comment in `values.yaml` for worked examples per model size. |

## Next Steps

- [Helm Chart Values](../reference/helm-values.md) — Full values reference
- [KServe Configuration](kserve-configuration.md) — Configure the KServe dependency
- [Security](security.md) — RBAC and pod security configuration
