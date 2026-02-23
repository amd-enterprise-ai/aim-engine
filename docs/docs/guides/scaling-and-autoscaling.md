# Scaling and Autoscaling

AIM Engine supports static replica scaling and KEDA-based autoscaling with OpenTelemetry metrics.

## Static Scaling

Set a fixed number of replicas:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  replicas: 3
```

## Autoscaling with KEDA

For demand-based scaling, use `minReplicas` and `maxReplicas` instead of `replicas`. AIM Engine configures KServe to use KEDA as the autoscaler.

### Prerequisites

Install KEDA and the OpenTelemetry integration:

- [KEDA](https://keda.sh/) v2.18+
- [OpenTelemetry Operator](https://github.com/open-telemetry/opentelemetry-operator)
- KEDA OpenTelemetry scaler (`keda-otel-scaler`)

### Basic Autoscaling

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  minReplicas: 1
  maxReplicas: 4
```

AIM Engine automatically:

1. Sets the KServe autoscaler class to `keda`
2. Injects an OpenTelemetry sidecar for metrics collection
3. KEDA creates an HPA (`keda-hpa-{isvc-name}-predictor`, based on the derived InferenceService name)

### Custom Metrics

Override the default scaling behavior with custom metrics:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  minReplicas: 1
  maxReplicas: 8
  autoScaling:
    metrics:
      - type: PodMetric
        podmetric:
          metric:
            backend: opentelemetry
            metricNames:
              - vllm:num_requests_running
            query: "vllm:num_requests_running"
            operationOverTime: "avg"
          target:
            type: Value
            value: "1"
```

### Available Metrics

Common vLLM metrics for scaling decisions:

| Metric | Description | Use Case |
|--------|-------------|----------|
| `vllm:num_requests_running` | Currently processing requests | Scale on active load |
| `vllm:num_requests_waiting` | Queued requests | Scale on queue depth |

### Metric Configuration

| Field | Description |
|-------|-------------|
| `backend` | Metrics backend (`opentelemetry`) |
| `serverAddress` | KEDA OTel scaler address (default: `keda-otel-scaler.keda.svc:4317`) |
| `metricNames` | Metric names to query |
| `query` | Query expression |
| `operationOverTime` | Aggregation: `last_one`, `avg`, `max`, `min`, `rate`, `count` |

### Target Types

| Type | Field | Description |
|------|-------|-------------|
| `Value` | `value` | Scale when metric exceeds this absolute value |
| `AverageValue` | `averageValue` | Scale when per-pod average exceeds this value |
| `Utilization` | `averageUtilization` | Scale on percentage utilization |

## Monitoring Scaling

Check the current scaling state:

```bash
# AIMService status
kubectl get aimservice qwen-chat -o jsonpath='{.status.runtime}' | jq

# KEDA HPA status
kubectl get hpa -n <namespace> -l aim.eai.amd.com/service.name=qwen-chat
```

## Next Steps

- [Deploying Services](deploying-services.md) — Full service configuration reference
- [Monitoring](../admin/monitoring.md) — Metrics and observability
