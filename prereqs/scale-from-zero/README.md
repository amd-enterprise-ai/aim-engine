# Scale-from-zero cluster prerequisites

AIM Engine uses a gateway-side request counter to wake an `AIMService` from
zero replicas. An Envoy Gateway installation needs:

1. One shared `EnvoyExtensionPolicy` on each Gateway used by AIM Engine.
2. A source-side delta OTLP metrics sink on the Gateway's platform-owned
   `EnvoyProxy`.
3. One OpenTelemetry Collector that receives those deltas and forwards the
   activation counters to keda-otel-add-on.

The policy runs before Envoy returns a zero-endpoint `503`, so the request that
wakes a service is counted even when no backend pod is available. Each
HTTPRoute has a distinct metric.

For the recommended Helm-managed installation, follow
[Optional Envoy Gateway scale-from-zero](https://github.com/amd-enterprise-ai/aim-engine/blob/main/docs/docs/admin/envoy-gateway-scale-from-zero.md).
This README documents the manifests in this directory and the distinction
between Helm-managed and externally managed collectors.

## Files

| File | Purpose |
|---|---|
| `envoy-gateway-route-metrics.yaml` | Shared Gateway-scoped metrics policy. |
| `envoy-gateway-metrics-collector.yaml` | Standalone, RBAC-free Envoy OTLP receiver for external management. |
| `kgateway-metrics-collector.yaml` | Standalone collector for an externally managed [kgateway installation](https://github.com/amd-enterprise-ai/aim-engine/blob/main/docs/docs/admin/kgateway-setup.md). |
| `kustomization.yaml` | Convenience bundle for explicitly installing the standalone Envoy collector. |

## Requirements

- Envoy Gateway v1.8+ / Envoy Proxy v1.38+ (`handle:stats()` is required).
- KEDA 2.18+ and keda-otel-add-on.
- OpenTelemetry Operator.

Allow the Envoy proxy pods to reach the collector Service on TCP 4317 and the
collector to reach keda-otel-add-on when the cluster enforces NetworkPolicies.
The Envoy collector does not need Kubernetes API discovery access.

Envoy Gateway v1.8's `Strict` Lua validator does not yet model Envoy 1.38's
`handle:stats()` API. Configure the Gateway's `EnvoyProxy` with both the Lua
setting and an OTLP sink that calculates deltas at the source:

```yaml
spec:
  luaValidation: InsecureSyntax
  telemetry:
    metrics:
      sinks:
        - type: OpenTelemetry
          openTelemetry:
            host: <collector-service>.<collector-namespace>.svc.cluster.local
            port: 4317
            reportCountersAsDeltas: true
```

For a Helm-managed collector installed as release `aim-engine` in `aim-system`,
use
`aim-engine-envoy-gateway-metrics-collector.aim-system.svc.cluster.local`.
For the standalone manifest defaults, use
`envoy-gateway-metrics-collector.keda.svc.cluster.local`.

Restrict `EnvoyExtensionPolicy` write access to platform administrators. A
route-level policy can override the Gateway-level policy.

## Helm-managed collector (recommended)

When `scaleFromZero.gatewayProvider=envoyGateway`, the AIM Engine Helm chart can
manage prerequisite 3 (the collector). It never modifies prerequisites 1 or 2
because they belong to the platform-owned Gateway. Install the policy and
configure its `EnvoyProxy` independently for every Gateway used by AIMServices.

For Envoy Gateway, edit the target Gateway name and namespace in
`envoy-gateway-route-metrics.yaml`, then apply the policy:

```bash
kubectl apply -f prereqs/scale-from-zero/envoy-gateway-route-metrics.yaml
```

The AIM Engine Helm chart selects the controller metric contract and bundled
collector from one provider value. Gateway activation is disabled by default:

```yaml
scaleFromZero:
  gatewayProvider: none
  gatewayMetricsCollector:
    management: helm
```

With this default, Helm renders no collector and AIMServices that request
`minReplicas: 0` report `ConfigValid=False`. To enable Envoy Gateway activation,
set `gatewayProvider: envoyGateway`; the existing `management: helm` default
then renders its collector.

Use `gatewayProvider: kgateway` to render the kgateway collector instead; see
[kgateway setup](../../../docs/docs/admin/kgateway-setup.md) for its Gateway
configuration.

For another gateway implementation, use `gatewayProvider: custom` with
`gatewayMetricsCollector.management: external`. The external collector must
forward delta metrics to keda-otel-add-on, and a default, named, or service-level
RuntimeConfig must provide
`scaleFromZero.activationMetricQueryTemplate`.

## Externally managed collector

Use external management only when platform infrastructure, rather than the AIM
Engine Helm release, owns the collector. Select the provider but disable Helm
ownership:

```yaml
scaleFromZero:
  gatewayProvider: envoyGateway
  gatewayMetricsCollector:
    management: external
```

Edit the standalone manifest's namespace and destination OTLP endpoint, update
the `EnvoyProxy` sink host to match that namespace, then apply it:

```bash
kubectl apply -f prereqs/scale-from-zero/envoy-gateway-metrics-collector.yaml
kubectl -n keda rollout status deploy/envoy-gateway-metrics-collector
```

Exactly one collector management mode should be used. Do not apply a
standalone collector while Helm management is enabled.

## Verify

Confirm that the policy is accepted:

```bash
kubectl get envoyextensionpolicy -n <gateway-namespace> \
  route-activation-metrics -o yaml
```

For the Helm-managed collector, inspect resources in the AIM Engine release
namespace (normally `aim-system`):

```bash
kubectl get opentelemetrycollector \
  aim-engine-envoy-gateway-metrics \
  -n aim-system
kubectl rollout status \
  deploy/aim-engine-envoy-gateway-metrics-collector \
  -n aim-system \
  --timeout=180s
kubectl logs \
  deploy/aim-engine-envoy-gateway-metrics-collector \
  -n aim-system \
  --tail=50
```

For the standalone manifest defaults, inspect the externally managed collector
in `keda`:

```bash
kubectl get opentelemetrycollector envoy-gateway-metrics -n keda
kubectl rollout status deploy/envoy-gateway-metrics-collector \
  -n keda \
  --timeout=180s
kubectl logs deploy/envoy-gateway-metrics-collector \
  -n keda \
  --tail=50
```

After sending a request through an AIM Engine HTTPRoute, the selected
collector's logs should contain an
`envoy_http_lua_aim_activation_requests_*` metric for the route.
