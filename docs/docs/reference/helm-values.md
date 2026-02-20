# Helm Chart Values

Reference for all configurable values in the AIM Engine Helm chart.

<!-- Auto-generated from config/helm/values.yaml by hack/generate-helm-values-docs.go. Do not edit manually. -->

## Controller Manager

Controller manager configuration

| Parameter | Description | Default |
|-----------|-------------|----------|
| `manager.replicas` | Number of operator replicas | `1` |
| `manager.image.repository` | Operator container image repository | `ghcr.io/silogen/aim-engine` |
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
| `manager.resources.limits.cpu` | CPU limit | `500m` |
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

## Cluster Model Source

Cluster-wide model source for automatic model discovery from container registries. Creates an AIMClusterModelSource CR when enabled, installing latest AIM Container Images.

| Parameter | Description | Default |
|-----------|-------------|----------|
| `clusterModelSource.enable` | Enable creation of the AIMClusterModelSource resource | `false` |
| `clusterModelSource.name` | Name of the AIMClusterModelSource resource | `amd-aim-model-source` |
| `clusterModelSource.spec` | Spec fields for the AIMClusterModelSource |  |
| `clusterModelSource.spec.registry` | Container registry to sync from (e.g., docker.io, ghcr.io, gcr.io) | `docker.io` |
| `clusterModelSource.spec.filters` | Filters define which images to discover and sync. Each filter specifies an image pattern with optional version constraints. |  |
| `clusterModelSource.spec.syncInterval` | How often to sync with the registry (minimum recommended: 15m) | `1h` |
| `clusterModelSource.spec.maxModels` | Maximum number of AIMClusterModel resources to create (prevents runaway creation) | `500` |

