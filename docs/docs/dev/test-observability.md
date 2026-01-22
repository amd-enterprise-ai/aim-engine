# Test Observability

This document describes the observability architecture for debugging Chainsaw test failures across local development and CI environments.

## Overview

When Chainsaw tests fail, debugging requires correlating multiple data sources:

- **Chainsaw test reports** - Which tests failed and why
- **Operator logs** - Reconciliation activity and errors
- **Kubernetes events** - Resource lifecycle (created, updated, scheduled)
- **Dependency pod logs** - KServe controller, inference pods, etc.
- **Test pod logs** - Pods created during test execution

The goal is a unified view where you can:

1. List all test runs (local and CI)
2. See which tests failed in each run
3. Drill into the timeline of what happened for a specific test namespace

## Architecture

All environments push logs to Grafana Cloud, providing a central location for analysis:

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  Local Dev       │     │  GH Actions CI   │     │  Dev vcluster    │
│  (kind + watch)  │     │  (kind cluster)  │     │  (GPU tests)     │
└────────┬─────────┘     └────────┬─────────┘     └────────┬─────────┘
         │                        │                        │
         └────────────────────────┼────────────────────────┘
                                  ▼
                      ┌──────────────────────┐
                      │   Grafana Cloud      │
                      │   (free tier)        │
                      │   - Loki for logs    │
                      │   - Dashboards       │
                      └──────────────────────┘
```

### Why Grafana Cloud?

| Consideration | Decision |
|---------------|----------|
| CI network access | GH Actions cannot reach our vclusters, so we need an externally accessible endpoint |
| Infrastructure cost | Free tier provides 50GB/month logs, 14-day retention |
| Maintenance | Fully managed, no infrastructure to maintain |
| Data sensitivity | Test logs are not sensitive |

### Grafana Cloud Free Tier Limits

| Resource | Limit | Notes |
|----------|-------|-------|
| Logs | 50 GB/month | Sufficient for test runs |
| Metrics | 10,000 series | More than enough |
| Traces | 50 GB/month | Available if we add OpenTelemetry later |
| Retention | 14 days | Sufficient for debugging recent failures |

## Data Model

Loki uses labels for indexing (must be low cardinality) and structured metadata for higher cardinality fields.

### Label Schema

```yaml
# Low cardinality labels - used for fast filtering
labels:
  env: "ci" | "local" | "vcluster"
  source: "github-actions" | "local-dev"
  component: "chainsaw" | "operator" | "kserve" | "pod" | "events"
  cluster: "kind-local" | "vcluster-gpu-dev"

# Medium cardinality - structured metadata for drill-down
metadata:
  run_id: "gh-run-12345" | "local-2024-01-21-143022"
  branch: "main" | "feature/service-migration"
  commit_sha: "ba45480"
  test_name: "aimservice/frozen"                    # chainsaw logs only
  test_namespace: "chainsaw-profound-cowbird"       # chainsaw logs only
```

### Log Line Format

All logs should be JSON formatted for easy parsing:

```json
{
  "level": "error",
  "msg": "reconciliation failed",
  "controller": "aimservice",
  "namespace": "chainsaw-profound-cowbird",
  "name": "test-service",
  "error": "timeout waiting for InferenceService ready"
}
```

### Test Run Summary Log

At the end of each Chainsaw run, emit a summary log entry to enable "list all test runs" queries:

```json
{
  "type": "test_run_summary",
  "run_id": "gh-run-12345",
  "started_at": "2024-01-21T14:30:00Z",
  "finished_at": "2024-01-21T14:35:22Z",
  "total_tests": 15,
  "passed": 12,
  "failed": 3,
  "failed_tests": [
    {"name": "aimservice/frozen", "namespace": "chainsaw-profound-cowbird"},
    {"name": "aimservice/scaling", "namespace": "chainsaw-happy-elephant"}
  ],
  "source": "github-actions",
  "branch": "feature/service-migration",
  "commit": "ba45480",
  "pr_number": 142
}
```

This enables querying for test runs: `{component="chainsaw"} | json | type="test_run_summary"`

## Log Sources and Collection

### What Gets Shipped

| Source | Collection Method | Labels |
|--------|-------------------|--------|
| Operator logs | Promtail (stdout scraping) | `component=operator` |
| KServe controller | Promtail | `component=kserve` |
| Test pod logs | Promtail | `component=pod` |
| Kubernetes events | kube-event-exporter | `component=events` |
| Chainsaw reports | Post-test script | `component=chainsaw` |

### Operator Logs

When running locally via `make watch`, logs are written to `.tmp/logs/`. For shipped logs, the Promtail agent scrapes stdout from the operator pod (or reads the log files when running locally).

Labels added:
- `component=operator`
- `controller=<controller-name>` (from log content)

### KServe Controller Logs

Promtail scrapes the KServe controller manager pod in the `kserve` namespace.

Labels added:
- `component=kserve`

### Kubernetes Events

Use [kubernetes-event-exporter](https://github.com/resmoio/kubernetes-event-exporter) to ship events as log entries.

Events are particularly useful for:
- Pod scheduling decisions
- Resource creation/deletion timestamps
- Error conditions (ImagePullBackOff, OOMKilled, etc.)

Labels added:
- `component=events`
- Event details in log content (kind, name, namespace, reason, message)

### Chainsaw Reports

After each Chainsaw run, a script parses the JSON report and ships:
1. Individual test results as log entries
2. A summary log entry (for test run listing)

Labels added:
- `component=chainsaw`
- `test_name=<test-path>`
- `test_namespace=<chainsaw-namespace>`

## Dashboard Design

### Test Run Explorer Dashboard

The primary dashboard for investigating test failures:

```
┌─────────────────────────────────────────────────────────────────────┐
│  Test Run Explorer                            [env ▼] [branch ▼]   │
├─────────────────────────────────────────────────────────────────────┤
│  Recent Test Runs                                                   │
│  ┌────────────┬────────┬────────┬─────────┬────────┬─────────────┐  │
│  │ Run ID     │ Source │ Branch │ Commit  │ Result │ Time        │  │
│  ├────────────┼────────┼────────┼─────────┼────────┼─────────────┤  │
│  │ gh-12345   │ CI     │ main   │ ba45480 │ 12/15 ✓│ 2 hours ago │  │
│  │ gh-12344   │ CI     │ feat/x │ 8979a0d │ 14/15 ✓│ 3 hours ago │  │
│  │ local-xxx  │ local  │ feat/y │ 059c705 │ 10/15 ✗│ 5 hours ago │  │
│  └────────────┴────────┴────────┴─────────┴────────┴─────────────┘  │
├─────────────────────────────────────────────────────────────────────┤
│  Run: local-xxx | Failed Tests                                      │
│  ┌─────────────────────────┬─────────────────────────────────────┐  │
│  │ Test Name               │ Namespace                           │  │
│  ├─────────────────────────┼─────────────────────────────────────┤  │
│  │ aimservice/frozen       │ chainsaw-profound-cowbird           │  │
│  │ aimservice/scaling      │ chainsaw-happy-elephant             │  │
│  └─────────────────────────┴─────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────────────────┤
│  Timeline: chainsaw-profound-cowbird                [component ▼]  │
│                                                                     │
│  14:30:00 ─┬─ [operator] Starting reconciliation for test-service   │
│            ├─ [kserve]   InferenceService created                   │
│            ├─ [events]   Pod test-service-pred-0 scheduled          │
│  14:30:15 ─┼─ [pod]      Pulling image...                           │
│  14:31:02 ─┼─ [pod]      Container started                          │
│            ├─ [operator] Error: timeout waiting for ready           │
│  14:32:00 ─┴─ [chainsaw] Assertion failed: status.ready != true     │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Dashboard Variables

| Variable | Query | Purpose |
|----------|-------|---------|
| `$env` | `label_values(env)` | Filter by environment |
| `$branch` | `label_values(branch)` | Filter by branch |
| `$run_id` | Selected from table | Current test run |
| `$test_namespace` | Selected from failed tests | Current test namespace |
| `$component` | `chainsaw`, `operator`, `kserve`, `pod`, `events` | Filter timeline |

### Key Queries

**List test runs:**
```logql
{component="chainsaw"} | json | type="test_run_summary"
```

**Failed tests for a run:**
```logql
{component="chainsaw", run_id="$run_id"} | json | status="failed"
```

**Timeline for a test namespace:**
```logql
{test_namespace="$test_namespace"} | json
```

**Errors only:**
```logql
{test_namespace="$test_namespace"} | json | level="error"
```

## Implementation

### Phase 1: Grafana Cloud Setup

1. Create Grafana Cloud account (free tier)
2. Note the Loki push endpoint and credentials
3. Store credentials as repository secrets for CI

### Phase 2: Local Development Integration

1. **Promtail configuration** for local Kind cluster:

```yaml
# promtail-config.yaml
server:
  http_listen_port: 9080

positions:
  filename: /tmp/positions.yaml

clients:
  - url: https://<user>:<token>@logs-prod-us-central1.grafana.net/loki/api/v1/push

scrape_configs:
  # Operator logs (when running in-cluster)
  - job_name: operator
    static_configs:
      - targets:
          - localhost
        labels:
          component: operator
          env: local
          cluster: kind-local
          __path__: /var/log/pods/aim-system_aim-engine-*/*/*.log

  # All pods in chainsaw namespaces
  - job_name: chainsaw-pods
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_namespace]
        regex: chainsaw-.*
        action: keep
      - source_labels: [__meta_kubernetes_namespace]
        target_label: test_namespace
    pipeline_stages:
      - json:
          expressions:
            level: level
            controller: controller
```

2. **Operator log shipping** when running locally via `make watch`:

```bash
# Ship local operator logs to Grafana Cloud
# Could be added to Makefile or run separately
tail -f "$(ls -t .tmp/logs/air-*.log | head -1)" | \
  promtail --stdin --client.url="$LOKI_URL" \
    --client.external-labels="component=operator,env=local,source=local-dev"
```

3. **Chainsaw report shipping** after test runs:

```bash
#!/bin/bash
# scripts/ship-chainsaw-report.sh

REPORT_FILE="${1:-.tmp/chainsaw-reports/chainsaw-report.json}"
RUN_ID="${RUN_ID:-local-$(date +%Y%m%d-%H%M%S)}"
BRANCH=$(git branch --show-current)
COMMIT=$(git rev-parse --short HEAD)

# Parse and ship individual test results
jq -c '.tests[]' "$REPORT_FILE" | while read -r test; do
  test_name=$(echo "$test" | jq -r '.name')
  # ... extract status, namespace, etc.
  # Ship to Loki with appropriate labels
done

# Ship summary
# ... generate and ship summary log entry
```

### Phase 3: CI Integration

1. **Deploy Promtail in CI Kind cluster:**

```yaml
# .github/workflows/test.yaml
- name: Deploy Promtail
  run: |
    helm repo add grafana https://grafana.github.io/helm-charts
    helm install promtail grafana/promtail \
      --set "config.clients[0].url=${{ secrets.LOKI_URL }}" \
      --set "config.clients[0].external_labels.env=ci" \
      --set "config.clients[0].external_labels.source=github-actions" \
      --set "config.clients[0].external_labels.run_id=gh-${{ github.run_id }}" \
      --set "config.clients[0].external_labels.branch=${{ github.head_ref }}" \
      --set "config.clients[0].external_labels.commit=${{ github.sha }}"
```

2. **Ship Chainsaw report after tests:**

```yaml
- name: Ship test report
  if: always()
  run: |
    ./scripts/ship-chainsaw-report.sh \
      --run-id "gh-${{ github.run_id }}" \
      --branch "${{ github.head_ref }}" \
      --commit "${{ github.sha }}" \
      --pr "${{ github.event.pull_request.number }}"
```

### Phase 4: vcluster Integration

For GPU tests on the dev vcluster:

1. Deploy Promtail DaemonSet with Grafana Cloud credentials
2. Configure external labels: `env=vcluster`, `cluster=<vcluster-name>`
3. Same Chainsaw report shipping as local/CI

## Chainsaw Configuration

Enhance Chainsaw to capture more context on failure:

```yaml
# chainsaw-configuration.yaml
apiVersion: chainsaw.kyverno.io/v1alpha2
kind: Configuration
metadata:
  name: default
spec:
  cleanup:
    delayBeforeCleanup: 5s  # Time to capture final state before cleanup
  catch:
    - podLogs: {}           # Collect all pod logs in namespace
    - events: {}            # Collect all events
    - describe:             # Describe key resources
        resource: pods
    - describe:
        apiVersion: serving.kserve.io/v1beta1
        kind: InferenceService
    - describe:
        apiVersion: aim.eai.amd.com/v1alpha1
        kind: AIMService
```

## Future Enhancements

### OpenTelemetry Integration

Add tracing to the operator for deeper correlation:

1. Instrument reconciliation loops with spans
2. Propagate trace context through resource annotations
3. Ship traces to Grafana Cloud Tempo
4. Correlate logs and traces by trace ID

### Test vcluster Infrastructure

For whole-stack GPU tests in CI:

| Approach | Pros | Cons |
|----------|------|------|
| Shared CI vcluster | Simple, one cluster | Contention, isolation issues |
| On-demand vclusters | Perfect isolation | Spin-up time, cost |
| Scheduled GPU tests | Low cost | Slower feedback |
| GPU tests on merge only | Fast PRs | Late failure discovery |

Recommendation: Tiered strategy with fast Kind tests on PRs and GPU tests on merge/nightly.

## Troubleshooting

### Common Issues

**Logs not appearing in Grafana:**
- Check Promtail is running: `kubectl get pods -l app=promtail`
- Verify Loki URL and credentials
- Check Promtail logs for push errors

**High cardinality warnings:**
- Ensure `run_id`, `test_name` are in structured metadata, not labels
- Review label values for unexpected variety

**Missing test namespaces:**
- Chainsaw namespace names are random; check the actual namespace in the report
- Search by `component=chainsaw` first to find the namespace

### Useful LogQL Queries

```logql
# All errors in the last hour
{env="ci"} | json | level="error"

# Reconciliation activity for a specific controller
{component="operator"} | json | controller="aimservice"

# Events for a specific resource
{component="events"} | json | name="my-service"

# Compare two test runs
{run_id=~"gh-123|gh-124", component="operator"} | json | level="error"
```
