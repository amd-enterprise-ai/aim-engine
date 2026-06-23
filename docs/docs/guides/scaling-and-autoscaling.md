# Scaling and Autoscaling

AIM Engine supports static replica scaling and KEDA-based autoscaling with OpenTelemetry metrics.

:::{admonition} v1alpha2
:class: note

Examples on this page use `aim.eai.amd.com/v1alpha2`. The `spec.replicas`, `spec.minReplicas`, `spec.maxReplicas`, and `spec.autoScaling` fields are identical across versions — only the resolution shape differs (`spec.profile` and `spec.model` instead of `spec.template`). For the legacy template-shaped service, see [Legacy AIMService](../legacy/aimservice-v1alpha1.md).
:::
## Static Scaling

Set a fixed number of replicas:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  annotations:
    # Migration window: spec.model.image alone routes to the legacy
    # template pipeline by default. The annotation opts in to the
    # v1alpha2 profile pipeline, which auto-creates a dedicated AIMModel
    # for this image. See admin/upgrading.md#migration-window.
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  replicas: 3
```

:::{admonition} Migration window
:class: note

Until v1alpha1 is removed, the `aim.eai.amd.com/reconciler-pipeline: profile` annotation is required on `spec.model.image` services that should be reconciled by the v1alpha2 profile pipeline. See [Migration window](../admin/upgrading.md#migration-window). To skip the annotation, reference an existing AIMProfile (`spec.profile.name`) or AIMModel (`spec.model.name`) instead.
:::
## Autoscaling with KEDA

For demand-based scaling, use `minReplicas` and `maxReplicas` instead of `replicas`. AIM Engine stamps the InferenceService with `autoscalerClass=external` and creates a controller-owned KEDA `ScaledObject` that manages scaling. At least one scaling trigger is required: a custom metric (`autoScaling.metrics`) or scale-from-zero (`minReplicas: 0`, which supplies a gateway activation trigger). Configuring `minReplicas`/`maxReplicas` with `minReplicas >= 1` and no metric is rejected with `ConfigValid=False` (reason `AutoscalingRequiresMetrics`).

### Prerequisites

Install KEDA and the OpenTelemetry integration:

- [KEDA](https://keda.sh/) v2.18+
- [OpenTelemetry Operator](https://github.com/open-telemetry/opentelemetry-operator)
- KEDA OpenTelemetry scaler (`keda-otel-scaler`)
- **`kgateway-metrics-collector`** — cluster-singleton OpenTelemetryCollector
  that scrapes the kgateway data plane and forwards the gateway-rate counter
  to `keda-otel-scaler`. Required for any service configured with
  `minReplicas: 0`; without it, KEDA's activation trigger has no metric series
  and the predictor will never scale up from zero. Keep this collector running
  at all times — it is the sole activation path for idle services, so while it
  is down no `minReplicas: 0` service can wake from zero. This collector ships
  with the AIM Engine Helm chart and is enabled by default
  (`scaleFromZero.gatewayMetricsCollector.enable=true`), so a standard install
  already includes it. To manage it out-of-band instead, apply it from this repo:

  ```bash
  kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/main/config/prereqs/scale-from-zero/kgateway-metrics-collector.yaml
  kubectl -n keda rollout status deploy/kgateway-metrics-collector --timeout=120s
  ```

  Or, from a clone, `make install-scale-from-zero-prereq`. See
  [installation](../getting-started/installation.md#2-install-the-operator)
  for customization knobs (gateway label, keda namespace).

### Basic Autoscaling

`minReplicas`/`maxReplicas` define the scaling bounds; a metric tells KEDA when
to scale within them. Below, the predictor scales between 1 and 4 replicas on
the number of in-flight vLLM requests:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
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

:::{admonition} Routing must be enabled for scale-to-zero
:class: warning

`minReplicas: 0` **requires routing to be enabled** on the service
(`spec.routing.enabled: true`, or a cluster-wide default via
`runtimeConfig.routing.enabled`). The 0->1 activation trigger queries
gateway-side Envoy metrics that only exist once an `HTTPRoute` is wired up,
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
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  minReplicas: 0
  maxReplicas: 1
  routing: # can also be injected by the runtime config
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: kgateway-system
    pathTemplate: "/{.metadata.namespace}/{.metadata.name}"
```

:::{admonition} The first request seeds the activation metric
:class: warning

On a fresh cluster the gateway activation series (`envoy_cluster_external_upstream_rq_completed`) does not exist until a request has passed through kgateway, so the **first** `minReplicas: 0` service reports `Ready=False reason=TriggerError` and stays at its initial replica instead of idling to zero. Send a single request (even a cold-start `503` counts) to seed the metric — afterwards scale-to-zero works normally for that service and later ones inherit the now-known metric.
:::
Notes:

- Routing (`spec.routing.enabled`, or a cluster-wide
  `runtimeConfig.routing.enabled` default) **must** be enabled. A `minReplicas: 0`
  service with routing disabled fails validation with `ConfigValid=False` /
  `RoutingRequiredForScaleToZero` — it is never created rather than idling into a
  state it can never wake from.
- The `kgateway-metrics-collector` cluster prereq from
  [Prerequisites](#prerequisites) **must** be installed for `minReplicas: 0` to
  ever activate from a cold predictor. Without it, KEDA's activation trigger
  has no metric series, the ScaledObject reports `Ready=False reason=TriggerError`,
  and the AIMService stalls with the HPA showing `desiredReplicas=0`
  (the int32 zero value, not a real decision).
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

### Custom Metrics

Scale on a different metric, a higher replica ceiling, or multiple metrics at once:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
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
