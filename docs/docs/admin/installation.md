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

### Scale-from-zero collector

AIM Engine's scale-from-zero feature is enabled by default and there is no Helm value to toggle it on or off — the controller always authors a gateway-rate activation trigger on ScaledObjects for services with `spec.minReplicas: 0`. For that trigger to fire, the cluster must run the `kgateway-metrics-collector` (a cluster-singleton OpenTelemetryCollector that scrapes the kgateway data-plane envoys and forwards the request counter to keda-otel-scaler). Keep this collector running at all times — it is the sole activation path for idle services, so while it is down no `minReplicas: 0` service can wake from zero.

That collector **ships with the AIM Engine Helm chart** and is enabled by default (`scaleFromZero.gatewayMetricsCollector.enable=true`), deployed into the release namespace, so a standard install already includes it. It requires the OpenTelemetry Operator CRDs to be present on the cluster. To manage it yourself instead — for example to run it in another namespace, or because the OpenTelemetry Operator is not installed — set that value to `false` and apply the standalone manifest:

```bash
kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/main/config/prereqs/scale-from-zero/kgateway-metrics-collector.yaml
```

If your cluster does not match the defaults (kgateway data-plane labeled `gateway.networking.k8s.io/gateway-name=kserve-ingress-gateway`, keda-otel-add-on in the `keda` namespace), override the chart values (or, for the standalone manifest, edit the three `EDIT FOR YOUR CLUSTER` comments inline before applying). The full rationale and per-knob explanation lives in [`config/prereqs/scale-from-zero/README.md`](https://github.com/amd-enterprise-ai/aim-engine/tree/main/config/prereqs/scale-from-zero).

The chart also exposes these scale-from-zero controller-config values, independent of the collector:

| Value | Default | Purpose |
|---|---|---|
| `scaleFromZero.scalerAddress` | `keda-otel-scaler.keda.svc:4318` | gRPC endpoint the controller writes into every external KEDA trigger it authors. |
| `scaleFromZero.cooldownSecondsPerGiMemory` | `5` | Seconds-per-GiB multiplier used to derive each service's `cooldownPeriod` from the predictor's memory request (`cooldownPeriod = clamp(300 + memGiB × this, 300, 1200)`). Default budgets for ~1.6 GB/s warm-cache throughput; tune lower for faster storage (local NVMe, hugepages) or higher for slower storage (NFS, network PVC). The 300 s floor ensures every predictor gets enough cooldown to absorb a real warmup; the 1200 s ceiling caps the worst-case overcooling on multi-GPU services. See the comment in `values.yaml` for worked examples per model size. |

## Next Steps

- [Helm Chart Values](../reference/helm-values.md) — Full values reference
- [KServe Configuration](kserve-configuration.md) — Configure the KServe dependency
- [Security](security.md) — RBAC and pod security configuration
