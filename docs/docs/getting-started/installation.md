# Installation

This guide covers installing AIM Engine on a Kubernetes cluster.

## Prerequisites

| Component | Minimum Version | Notes |
|-----------|----------------|-------|
| Kubernetes | 1.32+ | Cluster with AMD GPU nodes |
| [AMD GPU Operator](https://github.com/ROCm/gpu-operator) | — | Advertises `amd.com/gpu` and the GPU node labels used for template selection |
| KServe | v0.16.1 | See [KServe Configuration](../admin/kserve-configuration.md) |
| Gateway API | v1.3.0 | Required for HTTP routing |
| kgateway | v2.0+ | Gateway API data plane that AIM Engine targets for routing and the scale-from-zero activation signal |
| cert-manager | v1.16+ | Required by KServe and optional metrics TLS |
| KEDA | 2.18+ | Autoscaling; required because scale-from-zero is a default-on feature |
| keda-otel-add-on | latest | gRPC scaler that bridges OpenTelemetry metrics to KEDA. Installed alongside KEDA |
| OpenTelemetry Operator | 0.101+ | Reconciles the `OpenTelemetryCollector` CR for the bundled scale-from-zero collector (see note below) |

The scale-from-zero collector (`kgateway-metrics-collector`) is **not** a
separate prerequisite to apply — it ships with the AIM Engine Helm chart and
`dist/install.yaml` and is enabled by default
(`scaleFromZero.gatewayMetricsCollector.enable=true`), deployed into the release
namespace. You only apply it yourself when opting out; see
[the collector README](https://github.com/amd-enterprise-ai/aim-engine/tree/main/config/prereqs/scale-from-zero).

Optional components:

| Component | Version | Purpose |
|-----------|---------|---------|
| Longhorn or similar CSI | — | ReadWriteMany storage for model caching |

## Install with Helm

### 1. Install CRDs

CRDs are distributed separately from the Helm chart and must be installed first:

```bash
helm install aim-engine-crds oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version <version> \
  --namespace aim-system \
  --create-namespace
```

Or from a local file:

```bash
kubectl apply -f crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s
```

### 2. Install the Operator

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --create-namespace
```

The chart deploys the scale-from-zero `kgateway-metrics-collector` into the
release namespace by default (`scaleFromZero.gatewayMetricsCollector.enable=true`);
it needs the OpenTelemetry Operator CRDs already present. To run it standalone
instead — for a different namespace, a non-default gateway label, or because you
manage cluster infra separately — set that value to `false` and apply the
manifest yourself:

```bash
kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/main/config/prereqs/scale-from-zero/kgateway-metrics-collector.yaml
kubectl -n keda rollout status deploy/kgateway-metrics-collector --timeout=120s
```

See [`config/prereqs/scale-from-zero/README.md`](https://github.com/amd-enterprise-ai/aim-engine/tree/main/config/prereqs/scale-from-zero)
for the customization points.

See [Helm Chart Values](../reference/helm-values.md) for all configurable values (replicas, resources, metrics, CRD management, etc.).

### 3. Enable model discovery (optional)

The Helm chart does not create an `AIMClusterModelSource`. To populate cluster models from a registry, apply an `AIMClusterModelSource` manifest yourself (for example from the samples under `config/samples/` in this repository). See [Model Catalog](../guides/model-catalog.md) for details.

## Install from Source

Build and install from the repository:

```bash
git clone https://github.com/amd-enterprise-ai/aim-engine.git
cd aim-engine

# Generate CRDs and Helm chart
make crds
make helm

# Install CRDs
kubectl apply -f dist/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Install the operator (bundles the scale-from-zero collector by default;
# requires the OpenTelemetry Operator CRDs to be present)
helm install aim-engine ./dist/chart \
  --namespace aim-system \
  --create-namespace
```

!!! tip
    This project uses [mise](https://mise.jdx.dev) to manage tool versions (Go, controller-gen, etc.). Run `mise install` and `eval "$(mise activate bash)"` to get the correct versions on your PATH. See [Development Setup](../contributing/development-setup.md) for details.

## Common Configuration

### Enable Cluster Runtime Defaults

Set up cluster-wide routing and storage defaults:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --namespace aim-system \
  --set clusterRuntimeConfig.enable=true \
  --set clusterRuntimeConfig.spec.routing.enabled=true \
  --set clusterRuntimeConfig.spec.routing.gatewayRef.name=aim-gateway \
  --set clusterRuntimeConfig.spec.routing.gatewayRef.namespace=kgateway-system
```

See [Helm Chart Values](../reference/helm-values.md) for all available options.

## Verify Installation

Check that the operator is running:

```bash
kubectl get pods -n aim-system
```

Expected output:

```
NAME                                              READY   STATUS    RESTARTS   AGE
aim-engine-controller-manager-xxxxx-yyyyy         1/1     Running   0          30s
```

Verify CRDs are installed:

```bash
kubectl get crds | grep aim.eai.amd.com
```

## Uninstalling

```bash
# Remove the operator
helm uninstall aim-engine -n aim-system

# Remove CRDs
helm uninstall aim-engine-crds -n aim-system
```

!!! warning
    Uninstalling the CRDs release deletes all AIM custom resources from the cluster. Remove the operator first, then the CRDs only if you want a full cleanup.

## Next Steps

- [Quickstart](quickstart.md) — Deploy your first inference service
- [KServe Configuration](../admin/kserve-configuration.md) — Configure KServe for AIM Engine
- [Helm Chart Values](../reference/helm-values.md) — Full reference for all chart values
