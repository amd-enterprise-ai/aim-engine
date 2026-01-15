# KServe Configuration

This document describes the recommended KServe configuration for use with AIM Engine.

## Installation

AIM Engine requires KServe v0.16.0 or later. Install KServe using the official Helm chart:

```bash
helm install kserve-crd oci://ghcr.io/kserve/charts/kserve-crd \
  --namespace kserve-system \
  --create-namespace \
  --version v0.16.0

helm install kserve oci://ghcr.io/kserve/charts/kserve \
  --namespace kserve-system \
  --version v0.16.0 \
  --values kserve-values.yaml
```

## Recommended Configuration

### Deployment Mode

AIM Engine uses KServe in RawDeployment mode (without Knative):

```yaml
# kserve-values.yaml
kserve:
  controller:
    deploymentMode: RawDeployment
    gateway:
      ingressGateway:
        enableGatewayApi: false
```

### Resource Limits

By default, KServe applies resource limits to all InferenceService containers:

```yaml
# KServe defaults (not recommended)
kserve:
  inferenceService:
    resource:
      cpuLimit: "1"
      memoryLimit: "2Gi"
      cpuRequest: "200m"
      memoryRequest: "512Mi"
```

These defaults can cause issues with GPU workloads where AIM Engine sets higher CPU requests based on GPU count (4 CPUs per GPU). When the default CPU limit (1) is lower than the calculated request (4), Kubernetes rejects the deployment.

**Recommended**: Remove default limits to allow workloads to set their own resource requirements:

```yaml
# kserve-values.yaml
kserve:
  inferenceService:
    resource:
      cpuLimit: ""
      memoryLimit: ""
```

This follows Kubernetes best practices where limits are set intentionally per workload rather than globally.

### Local Model Cache

AIM Engine manages model caching independently. Disable KServe's local model feature:

```yaml
# kserve-values.yaml
kserve:
  localmodel:
    enabled: false
```

## Complete Example

```yaml
# kserve-values.yaml
kserve:
  controller:
    deploymentMode: RawDeployment
    gateway:
      ingressGateway:
        enableGatewayApi: false
  localmodel:
    enabled: false
  inferenceService:
    resource:
      cpuLimit: ""
      memoryLimit: ""
```

## Verifying Configuration

After installation, verify the configuration is applied:

```bash
kubectl get configmap -n kserve-system inferenceservice-config -o jsonpath='{.data.inferenceService}'
```

The output should show empty or missing `cpuLimit` and `memoryLimit` values.
