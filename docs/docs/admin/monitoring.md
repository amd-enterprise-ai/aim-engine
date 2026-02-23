# Monitoring and Observability

AIM Engine exposes metrics and structured logs for monitoring operator health and inference workloads.

## Metrics

### Endpoint

The controller exposes metrics on port 8443 (HTTPS by default). Configure via Helm:

| Value | Default | Description |
|-------|---------|-------------|
| `metrics.enable` | `true` | Enable metrics endpoint |
| `metrics.port` | `8443` | Metrics port |

### Prometheus ServiceMonitor

Enable automatic scraping with Prometheus:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --set prometheus.enable=true
```

This creates a `ServiceMonitor` resource that Prometheus Operator picks up automatically.

### Controller Runtime Metrics

AIM Engine exposes standard controller-runtime metrics:

- `controller_runtime_reconcile_total` — Total reconciliations by controller and result
- `controller_runtime_reconcile_errors_total` — Total reconciliation errors
- `controller_runtime_reconcile_time_seconds` — Reconciliation duration
- `workqueue_depth` — Current work queue depth per controller

## Logs

### Format

Operator logs are JSON-formatted with these key fields:

| Field | Description | Example |
|-------|-------------|---------|
| `level` | Log level | `info`, `error`, `debug` |
| `controller` | Controller name | `artifact`, `service`, `model` |
| `namespace` | Resource namespace | `ml-team` |
| `name` | Resource name | `qwen-chat` |
| `condition` | Condition being updated | `Ready` |
| `status` | Condition status | `True`, `False` |
| `reason` | Condition reason | `RuntimeReady` |

### Log Levels

Configure via operator flags:

| Flag | Values | Default |
|------|--------|---------|
| `--zap-log-level` | `debug`, `info`, `error`, or integer | `info` |
| `--zap-encoder` | `json`, `console` | `json` |
| `--zap-devel` | — | `false` (production mode) |

Enable debug logging in Helm:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --set 'manager.args={--leader-elect,--zap-log-level=debug}'
```

### Useful Log Queries

```bash
# View operator logs
kubectl logs -n aim-system deployment/aim-engine-controller-manager -f

# Filter for errors
kubectl logs -n aim-system deployment/aim-engine-controller-manager | \
  jq 'select(.level == "error")'

# Filter by controller
kubectl logs -n aim-system deployment/aim-engine-controller-manager | \
  jq 'select(.controller == "aimservice")'

# Filter by namespace
kubectl logs -n aim-system deployment/aim-engine-controller-manager | \
  jq 'select(.namespace == "ml-team")'
```

## Kubernetes Events

The operator emits Kubernetes Events on AIM resources when conditions change. Events provide a timeline of state transitions visible via `kubectl describe`.

### Event Types

| Type | When Emitted |
|------|-------------|
| `Normal` | Condition transitions to a healthy state |
| `Warning` | Condition transitions to an unhealthy state, or persists unhealthy on every reconcile |

### Event Reasons

Events use the condition's `reason` field as the event reason. Common event reasons:

**AIMService:**

| Reason | Type | Description |
|--------|------|-------------|
| `ModelResolved` | Normal | Model found and ready |
| `ModelNotFound` | Warning | Referenced model does not exist |
| `Resolved` | Normal | Template resolved successfully |
| `TemplateSelectionAmbiguous` | Warning | Multiple templates scored equally |
| `CacheReady` | Normal | Model cache is populated |
| `CacheFailed` | Warning | Cache download failed |
| `RuntimeReady` | Normal | InferenceService is serving |
| `InvalidImageReference` | Warning | Model image URI is invalid |
| `PathTemplateInvalid` | Warning | Routing path template failed to resolve |

**AIMModel:**

| Reason | Type | Description |
|--------|------|-------------|
| `AllTemplatesReady` | Normal | All discovered templates are ready |
| `AllTemplatesFailed` | Warning | All discovered templates failed |
| `MetadataExtractionFailed` | Warning | Failed to extract model metadata |

**AIMArtifact:**

| Reason | Type | Description |
|--------|------|-------------|
| `Verified` | Normal | Download complete and verified |
| `Downloading` | Normal | Download in progress |

### Viewing Events

```bash
# Events for a specific resource
kubectl describe aimservice qwen-chat -n <namespace>

# All AIM-related events in a namespace
kubectl get events -n <namespace> --field-selector involvedObject.apiVersion=aim.eai.amd.com/v1alpha1
```

### Recurring Events

Some warning events are emitted on **every reconcile** (not just on transitions) for critical conditions that remain unhealthy. These are useful for alerting — a persistent stream of warnings indicates a stuck or failing resource.

See [Conditions Reference](../reference/conditions.md) for the full catalog of conditions and reasons.

## Health Probes

The operator exposes health and readiness probes:

| Probe | Path | Port |
|-------|------|------|
| Liveness | `/healthz` | 8081 |
| Readiness | `/readyz` | 8081 |

These are configured automatically in the Helm chart deployment.

## Next Steps

- [Troubleshooting](troubleshooting.md) — Diagnosing common issues
- [CLI and Operator Flags](../reference/cli.md) — Full operator flag reference
