# Installation

This guide covers installing AIM Engine on a Kubernetes cluster.

## Prerequisites

| Component | Minimum Version | Notes |
|-----------|----------------|-------|
| Kubernetes | 1.32+ | Cluster with supported AMD or NVIDIA GPU nodes; CPU-only profiles are also supported |
| [AMD GPU Operator](https://github.com/ROCm/gpu-operator) | — | AMD GPU stack; advertises `amd.com/gpu`, labels AMD nodes, and installs NFD |
| [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) | — | NVIDIA GPU stack; advertises `nvidia.com/gpu`, installs NFD, and provides the NVIDIA runtime |
| KServe | v0.16.1 | See [KServe Configuration](../admin/kserve-configuration.md) |
| Gateway API | v1.5.1+ | Required only when HTTP routing is enabled |
| Envoy Gateway | v1.8.2 | Optional Gateway API data plane. Required only when that platform integration is selected. |
| cert-manager | v1.16+ | Required by KServe and optional metrics TLS |
| KEDA | 2.18+ | Required when using AIMService autoscaling |
| keda-otel-add-on | latest | gRPC scaler that bridges OpenTelemetry metrics to KEDA. Installed alongside KEDA |
| OpenTelemetry Operator | 0.101+ | Required when Helm manages a bundled scale-from-zero collector (see note below) |

Gateway activation is disabled by default (`scaleFromZero.gatewayProvider=none`).
Selecting `envoyGateway` or `kgateway` makes the chart include the matching
collector by default. Envoy Gateway also requires one shared metrics policy per
Gateway and a source-side delta OTLP sink on its platform-owned `EnvoyProxy`.
See
[Optional Envoy Gateway scale-from-zero](../admin/envoy-gateway-scale-from-zero.md).

Optional components:

| Component | Version | Purpose |
|-----------|---------|---------|
| Longhorn or similar CSI | — | ReadWriteMany storage for model caching |

Install the GPU operator that matches the cluster hardware, or provide an
equivalent device-plugin, NFD, and runtime setup. AIM Engine's
[AcceleratorDetector](../concepts/accelerator-detection.md) uses NFD to publish
normalized model, vendor, and partition labels for profile resolution.

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
kubectl apply --server-side -f crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s
```

### 2. Install the Operator

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --create-namespace
```

This base installation is gateway-neutral. Gateway activation remains disabled
because `scaleFromZero.gatewayProvider` defaults to `none`, and the chart
renders no gateway metrics collector.

### 3. Enable a scale-from-zero gateway integration (optional)

To use Envoy Gateway, the platform administrator installs and configures Envoy
Gateway and one shared `EnvoyExtensionPolicy` per Gateway. AIM Engine does not
own those resources. Then enable the matching controller contract and
Helm-managed collector:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --set scaleFromZero.gatewayProvider=envoyGateway
```

Follow [Optional Envoy Gateway scale-from-zero](../admin/envoy-gateway-scale-from-zero.md)
for the complete platform setup, policy installation, and verification.

For kgateway, use the [kgateway setup](../admin/kgateway-setup.md). Custom
gateways use `scaleFromZero.gatewayProvider=custom` together with
`scaleFromZero.gatewayMetricsCollector.management=external`; they must provide
an activation metric query through a RuntimeConfig.

Existing kgateway installations should follow
[Migrate from kgateway to Envoy Gateway](../admin/migrating-kgateway-to-envoy-gateway.md)
instead of switching the provider and Gateway reference without a warm-replica
safety window.

See
[Custom gateway activation metrics](../guides/scaling-and-autoscaling.md#custom-gateway-activation-metrics).

See [Helm Chart Values](../reference/helm-values.md) for all configurable values (replicas, resources, metrics, CRD management, etc.).

### 4. Enable model discovery (optional)

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
kubectl apply --server-side -f dist/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Install the operator with gateway activation disabled by default.
helm install aim-engine ./dist/chart \
  --namespace aim-system \
  --create-namespace
```

:::{tip}
This project uses [mise](https://mise.jdx.dev) to manage tool versions (Go, controller-gen, etc.). Run `mise install` and `eval "$(mise activate bash)"` to get the correct versions on your PATH. See [Development Setup](../contributing/development-setup.md) for details.
:::
## Common Configuration

### Customize Cluster Runtime Defaults

The chart creates `AIMClusterRuntimeConfig/default` by default, including a
generic NVIDIA vLLM fallback for model-ID onboarding and compatibility mappings
from the private producer prefixes recorded by released AIM images to the
public `docker.io/amdenterpriseai` runtime repository. Add cluster-wide routing
and storage defaults by overriding its spec:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --namespace aim-system \
  --set clusterRuntimeConfig.spec.routing.enabled=true \
  --set clusterRuntimeConfig.spec.routing.gatewayRef.name=<gateway-name> \
  --set clusterRuntimeConfig.spec.routing.gatewayRef.namespace=<gateway-namespace>
```

If the cluster already has a platform-managed `AIMClusterRuntimeConfig/default`,
install or upgrade with `--set clusterRuntimeConfig.enable=false` to avoid a
Helm ownership conflict.

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

:::{warning}
Uninstalling the CRDs release deletes all AIM custom resources from the cluster. Remove the operator first, then the CRDs only if you want a full cleanup.
:::
## Next Steps

- [Quickstart](quickstart.md) — Deploy your first inference service
- [KServe Configuration](../admin/kserve-configuration.md) — Configure KServe for AIM Engine
- [Helm Chart Values](../reference/helm-values.md) — Full reference for all chart values
