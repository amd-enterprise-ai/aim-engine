# Scale-from-zero cluster prerequisites

AIM Engine's scale-from-zero feature needs one cluster-wide OpenTelemetry
collector that turns the kgateway data-plane request counter into the
activation signal KEDA uses to wake a service from zero pods.

**It ships with AIM Engine by default** — in `dist/install.yaml` (via
`make build-installer`) and the Helm chart
(`scaleFromZero.gatewayMetricsCollector.enable`, on by default), deployed
into the release namespace. To manage it out-of-band instead, set that
value to `false` and apply the manifest here directly (see below).

## What this directory contains

| File | Purpose |
|---|---|
| `kgateway-metrics-collector.yaml` | The collector. Scrapes Envoy admin metrics from each kgateway data-plane pod and forwards the gateway-rate counter to keda-otel-scaler — the only activation signal available while a `spec.minReplicas: 0` service sits at zero pods. Source of truth: `config/default` pulls in this exact manifest, so the installer and chart ship it. |
| `kustomization.yaml` | Kustomize base that pulls the manifest into `config/default`. |

## Other prerequisites (NOT in this directory)

These are installed via their own upstream methods; we just note them
for completeness so a fresh cluster can be brought up end-to-end:

- **KEDA** (>= 2.18) — the operator that owns the `ScaledObject` →
  HPA conversion that drives the 0↔1 transition. Install per
  [KEDA's docs](https://keda.sh/docs/latest/deploy/).
- **keda-otel-add-on** — the gRPC scaler service that consumes both
  the gateway-rate metric (this manifest's output) and the in-pod
  vLLM metrics, exposing them to KEDA over the `external` trigger
  protocol. Install per the
  [kedify/otel-add-on README](https://github.com/kedify/otel-add-on).
  Defaults assume it runs in the `keda` namespace serving
  `keda-otel-scaler.keda.svc:4318` (gRPC scaler) and `:4317` (OTLP
  receiver).
- **OpenTelemetry Operator** — reconciles the
  `OpenTelemetryCollector` CR in `kgateway-metrics-collector.yaml`
  into a Deployment + Service.
- **kgateway** with at least one `Gateway` whose data-plane pods are
  labeled `gateway.networking.k8s.io/gateway-name=<name>`. The
  default selector value is `kserve-ingress-gateway`; edit the
  manifest if your cluster differs.

## Install

### Default: shipped with AIM Engine

For a standard install the collector is already deployed — nothing to apply
here. Tune it via Helm values:

```yaml
scaleFromZero:
  gatewayMetricsCollector:
    enable: true                 # set false to manage it standalone (below)
    gatewayName: kserve-ingress-gateway
    otlpEndpoint: keda-otel-scaler.keda.svc:4317
```

### Standalone (out-of-band) install

For clusters that manage this infrastructure separately, disable the
chart copy (`scaleFromZero.gatewayMetricsCollector.enable=false`) and
apply the manifest directly:

```bash
kubectl apply -f config/prereqs/scale-from-zero/kgateway-metrics-collector.yaml
```

Wait for the collector to become Ready:

```bash
kubectl -n keda rollout status deploy/kgateway-metrics-collector --timeout=120s
```

## Customize for your cluster

When **chart-managed**, set the values under
`scaleFromZero.gatewayMetricsCollector` (namespace follows the release;
`gatewayName` and `otlpEndpoint` map to the two cluster-specific knobs
below).

When applying the **standalone** manifest, the defaults match the
kaiwo-tw-1 baseline; three values commonly need to change for other
clusters, each flagged inline with an `EDIT FOR YOUR CLUSTER` comment:

1. **Namespace** (`keda`) — wherever your keda-otel-add-on lives.
2. **Gateway label selector** (`kserve-ingress-gateway`) — the value
   of the `gateway.networking.k8s.io/gateway-name` label on your
   kgateway data-plane pods.
3. **OTLP exporter endpoint** — must point at your keda-otel-scaler's
   OTLP gRPC port (default `:4317`).

Edit in place and re-apply; the collector reconciles within a few
seconds.

## Verify

After apply, send a single request through any HTTPRoute backed by a
scale-to-zero AIMService and tail the collector's debug exporter:

```bash
kubectl logs -n keda deploy/kgateway-metrics-collector --tail=20
```

You should see one (and only one) datapoint per kgateway pod per
second, with attributes
`envoy_cluster_name=kube_<ns>_<svc>_<port>`,
`namespace=<ns>`, `deployment=<svc>`. Values arrive as **per-scrape
deltas** (typically 0, with a spike of 1+ on the scrape that
captures a real request) rather than the raw cumulative counter —
the `cumulativetodelta` processor converts the series at the
collector boundary so the keda-otel-add-on scaler never sees a
counter reset. This matters when the Envoy upstream cluster for
the predictor restarts (predictor cycle, kgateway pod restart,
zero-endpoint eviction): without the delta conversion the scaler's
sliding-window `rate` aggregation would report a *negative* value
across the reset and would fail activation for one window.

Series with non-`kube_` cluster names (e.g. `admin_port_cluster`)
should NOT appear — the `filter/exclude_envoy_internal` processor
drops them at the collector boundary so the keda-otel-scaler's
metric store stays free of noise.

To keep scrape volume bounded as the cluster grows, the Prometheus
receiver filters at the Envoy admin endpoint itself
(`/stats/prometheus?filter=upstream_rq_completed&usedonly=`) instead
of pulling the full stats page (~10k lines/pod) and discarding it in
the collector. Envoy's `filter` regex matches the *internal* stat
name (`cluster.<name>.external.upstream_rq_completed`), not the
Prometheus-rendered name, so the broad `upstream_rq_completed`
substring is used and the `filter/metrics` processor keeps the exact
`envoy_cluster_external_upstream_rq_completed` series; `usedonly`
drops counters that have never been incremented.

## When to manage it yourself

The collector is a cluster-wide singleton. Prefer the standalone path
(`enable=false` + the manifest here) when:

- A platform team owns cluster infra (kgateway, KEDA, OTel Operator) on a
  separate lifecycle from the controller.
- You run multiple AIM Engine installs on one cluster — the chart's
  cluster-scoped collector RBAC is singleton-named and would collide.
- The OpenTelemetry Operator CRDs aren't present at install time, so the
  chart's `OpenTelemetryCollector` would fail to apply.
