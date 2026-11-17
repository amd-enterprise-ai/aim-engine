# KServe Configuration

AIM Engine requires KServe v0.16.1 or later with specific configuration. Use the values file below when installing KServe.

## Required Values

```yaml
# kserve-values.yaml
kserve:
  controller:
    deploymentMode: Standard
    gateway:
      ingressGateway:
        enableGatewayApi: false
  localmodel:
    enabled: false
  inferenceservice:
    resources:
      limits:
        cpu: ""
        memory: ""
      requests:
        cpu: ""
        memory: ""
```

## Installation

```bash
helm install kserve-crd oci://ghcr.io/kserve/charts/kserve-crd \
  --namespace kserve-system \
  --create-namespace \
  --version v0.16.1

helm install kserve oci://ghcr.io/kserve/charts/kserve \
  --namespace kserve-system \
  --version v0.16.1 \
  --values kserve-values.yaml
```

## Why These Values Matter

### Resource Limits (Critical)

:::{warning}
Without clearing the default resource limits, AIM Engine deployments will fail.
:::
KServe applies default CPU and memory resources (`cpu: 1`, `memory: 2Gi`) to InferenceService containers when those keys are omitted. AIM Engine sets CPU requests based on GPU count (4 CPUs per GPU) but intentionally does not set CPU limits, allowing inference workloads to burst and fully utilize available CPU for optimal throughput. KServe's default CPU limit of `1` conflicts with larger calculated requests, while its `2Gi` memory limit can OOM-kill model servers whose profile does not override memory.

For GPU workloads, profiles set defaults per GPU (`requests.cpu: 4`, `requests.memory: 32Gi`, `limits.memory: 48Gi`), unless overridden by profile or service resources. Clearing KServe defaults avoids accidental request/limit mismatches and hidden caps when resources are omitted or partially overridden.

Setting limits and requests to `""` removes the defaults so AIM Engine controls per-workload CPU/memory behavior.

### Deployment Mode

AIM Engine uses KServe in Standard mode (without Knative) to support KEDA-based autoscaling with OpenTelemetry metrics.

### Local Model Cache

AIM Engine manages model caching independently via `AIMArtifact` and `AIMProfileCache` resources. KServe's built-in local model feature must be disabled to avoid conflicts.

This is already configured in the values snippet above via `kserve.localmodel.enabled: false` (and in the repository's local dependency Helmfile).

## Verifying Configuration

After installation, verify the resource limits are removed:

```bash
kubectl get configmap -n kserve-system inferenceservice-config -o jsonpath='{.data.inferenceService}'
```

The output should show empty or missing `cpuLimit` and `memoryLimit` values.
