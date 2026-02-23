# Installation

This guide covers installing AIM Engine on a Kubernetes cluster.

## Prerequisites

| Component | Minimum Version | Notes |
|-----------|----------------|-------|
| Kubernetes | 1.28+ | Cluster with AMD GPU nodes |
| KServe | v0.16.1 | See [KServe Configuration](../admin/kserve-configuration.md) |
| Gateway API | v1.3.0 | Required for HTTP routing |
| cert-manager | v1.16+ | Required by KServe and optional metrics TLS |

Optional components:

| Component | Version | Purpose |
|-----------|---------|---------|
| KEDA | 2.18+ | Autoscaling with OpenTelemetry metrics |
| OpenTelemetry Operator | 0.101+ | Custom metrics collection for autoscaling |
| Longhorn or similar CSI | — | ReadWriteMany storage for model caching |

## Install with Helm

### 1. Install CRDs

CRDs are distributed separately from the Helm chart and must be installed first:

```bash
helm install aim-engine-crds oci://docker.io/amdenterpriseai/charts/aim-engine-crds \
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
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --create-namespace
```

See [Helm Chart Values](../reference/helm-values.md) for all configurable values (replicas, resources, metrics, CRD management, etc.).

### 3. Enable Model Discovery (Recommended)

Automatically populate the model catalog from AMD's published AIM container images:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --set clusterModelSource.enable=true
```

This creates an `AIMClusterModelSource` that discovers `amdenterpriseai/aim-*` images and registers them as `AIMClusterModel` resources. See [Model Catalog](../guides/model-catalog.md) for more details.

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

# Install the operator
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
helm upgrade aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
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
