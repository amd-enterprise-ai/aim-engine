# Scaling and Autoscaling

AIM Engine supports static replica scaling and KEDA-based autoscaling with OpenTelemetry metrics.

:::{admonition} v1alpha2
:class: note

Examples on this page use `aim.eai.amd.com/v1alpha2`. The `spec.replicas`, `spec.minReplicas`, `spec.maxReplicas`, and `spec.autoScaling` fields are identical across versions — only the resolution shape differs (`spec.profile` and `spec.model` instead of `spec.template`).
:::
## Static Scaling

Set a fixed number of replicas:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  replicas: 3
```

## Autoscaling with KEDA

For demand-based scaling, use `minReplicas` and `maxReplicas` instead of `replicas`. AIM Engine stamps the InferenceService with `autoscalerClass=external` and creates a controller-owned KEDA `ScaledObject` that manages scaling. At least one scaling trigger is required: a custom metric (`autoScaling.metrics`) or scale-from-zero (`minReplicas: 0`, which supplies a gateway activation trigger). Configuring `minReplicas`/`maxReplicas` with `minReplicas >= 1` and no metric is rejected with `ConfigValid=False` (reason `AutoscalingRequiresMetrics`).

### Prerequisites

Install KEDA and the OpenTelemetry integration:

- [KEDA](https://keda.sh/) v2.18+
- [OpenTelemetry Operator](https://github.com/open-telemetry/opentelemetry-operator)
- KEDA OpenTelemetry scaler (`keda-otel-scaler`)
- For Envoy Gateway, one shared activation metrics policy per Gateway
- A non-`none` `scaleFromZero.gatewayProvider`. For built-in providers, the
  matching collector is installed by the AIM Engine chart by default.

See [Optional Envoy Gateway scale-from-zero](../admin/envoy-gateway-scale-from-zero.md)
for provider, Gateway policy, and collector configuration.

### Basic Autoscaling

`minReplicas`/`maxReplicas` define the scaling bounds; a metric tells KEDA when
to scale within them. Below, the predictor scales between 1 and 4 replicas on
the number of in-flight vLLM requests:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  minReplicas: 1
  maxReplicas: 4
  autoScaling:
    metrics:
      - type: PodMetric
        podmetric:
          metric:
            backend: opentelemetry
            metricNames:
              - vllm:num_requests_running
            query: "vllm:num_requests_running"
            operationOverTime: avg
          target:
            type: Value
            value: "1"
```

AIM Engine automatically:

1. Stamps the InferenceService with `autoscalerClass=external` so KServe writes no autoscaler of its own
2. Injects an OpenTelemetry sidecar for metrics collection
3. Creates a controller-owned KEDA `ScaledObject` targeting the predictor `Deployment`; KEDA in turn manages the HPA (`keda-hpa-{isvc-name}-predictor`, based on the derived InferenceService name)

### Scale to Zero

Set `minReplicas: 0` to let KEDA idle the predictor down to zero replicas when no
traffic is observed and bring it back up on the next request. Note: without `autoScaling.metrics`, the service activates from 0 -> 1 but will not scale from 1 -> N.

The Helm chart defaults `scaleFromZero.gatewayProvider` to `none`; select a
built-in or custom provider before creating scale-from-zero services.

:::{admonition} Routing must be enabled for scale-to-zero
:class: warning

`minReplicas: 0` **requires routing to be enabled** on the service
(`spec.routing.enabled: true`, or a cluster-wide default via
`runtimeConfig.routing.enabled`). The 0->1 activation trigger queries
gateway-side metrics associated with an `HTTPRoute`,
so with routing disabled the service can never wake from zero. AIM Engine
rejects this combination at validation time: the AIMService reports
`ConfigValid=False` with reason
[`RoutingRequiredForScaleToZero`](../reference/conditions.md#scaletozeroconfig)
and emits an `InvalidSpec` event. Either enable routing (below) or set
`minReplicas >= 1`.
:::
```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  minReplicas: 0
  maxReplicas: 1
  routing: # can also be injected by the runtime config
    enabled: true
    gatewayRef:
      name: <gateway-name>
      namespace: <gateway-namespace>
    pathTemplate: "/{.metadata.namespace}/{.metadata.name}"
```

Notes:

- Routing (`spec.routing.enabled`, or a cluster-wide
  `runtimeConfig.routing.enabled` default) **must** be enabled. A `minReplicas: 0`
  service with routing disabled fails validation with `ConfigValid=False` /
  `RoutingRequiredForScaleToZero` — it is never created rather than idling into a
  state it can never wake from.
- The Gateway metrics policy and collector from [Prerequisites](#prerequisites)
  must be running before a service can wake from zero replicas.
- `maxReplicas` must still be set to at least `1` so the service can scale back up.
- The first request after the pod has been scaled to zero pays the full cold-start
  cost (image pull, model load, accelerator allocation). For large LLMs this can be
  multiple minutes; combine with a cache (`caching.mode: Shared` or `Dedicated`) so
  weights are already on a PVC when the pod restarts.
- While the predictor is idle (zero replicas) or still warming up, requests through
  the gateway return `503` (`no healthy upstream`). AIM Engine does **not** retry
  these for you — the request that wakes the service is the one that gets the `503`.
  Clients are expected to retry on `503`; the OpenAI SDKs (`openai-python`,
  `openai-node`) do this by default, and raw HTTP clients should implement
  retry-with-backoff against a cold service.
- KEDA decides scale-to-zero based on the configured trigger (the default load-based
  trigger, or your custom `autoScaling.metrics`). The metric you scale on must
  legitimately reach `0` on idle, otherwise the pod will not be scaled down.

### Custom Gateway Activation Metrics

For another gateway implementation, install an external collector and configure
the chart without a bundled provider collector:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --namespace aim-system \
  --reuse-values \
  --set scaleFromZero.gatewayProvider=custom \
  --set scaleFromZero.gatewayMetricsCollector.management=external
```

Provide the activation query through the default RuntimeConfig to apply it to
all AIMServices that do not select another `runtimeConfigName`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  scaleFromZero:
    activationMetricQueryTemplate: >-
      sum(custom_gateway_requests{namespace="${namespace}",route="${httpRouteName}"})
```

Supported placeholders are `${namespace}`, `${serviceName}`,
`${httpRouteName}`, and `${predictorDeployment}`. A query set directly under an
AIMService's `spec.scaleFromZero` takes precedence over namespace and cluster
RuntimeConfigs. A named RuntimeConfig can provide a different query to selected
services through `spec.runtimeConfigName`.

The external pipeline must forward delta metrics, not cumulative counter
totals, to keda-otel-add-on. AIM Engine keeps the activation trigger's `targetValue` and
`operationOverTime=avg` fixed so a single gateway request produces the 0 → 1
activation signal. Missing queries for the `custom` provider and unsupported
placeholders report `ConfigValid=False` with reason
`ActivationMetricQueryInvalid`.

### Custom Metrics

Scale on a different metric, a higher replica ceiling, or multiple metrics at once:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
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
