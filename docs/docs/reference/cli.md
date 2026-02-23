# CLI and Operator Flags

The AIM Engine operator binary accepts the following command-line flags.

## Controller Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--metrics-bind-address` | string | `"0"` | Address for the metrics endpoint. Use `:8443` for HTTPS via cert-manager, `:8080` for HTTP, or `0` to disable. |
| `--health-probe-bind-address` | string | `:8081` | Address for the health probe endpoint. |
| `--leader-elect` | bool | `false` | Enable leader election for high availability. Uses lease ID `3be10d2f.eai.amd.com`. |
| `--metrics-secure` | bool | `true` | Serve metrics over HTTPS. Set to `false` for HTTP. |
| `--enable-http2` | bool | `false` | Enable HTTP/2 for metrics and webhook servers. Disabled by default due to CVE-2023-44487. |

## TLS Certificate Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--webhook-cert-path` | string | `""` | Directory containing webhook server TLS certificate. |
| `--webhook-cert-name` | string | `tls.crt` | Webhook certificate file name. |
| `--webhook-cert-key` | string | `tls.key` | Webhook private key file name. |
| `--metrics-cert-path` | string | `""` | Directory containing metrics server TLS certificate. |
| `--metrics-cert-name` | string | `tls.crt` | Metrics certificate file name. |
| `--metrics-cert-key` | string | `tls.key` | Metrics private key file name. |

## Logging Flags (Zap)

The operator uses controller-runtime's Zap logging integration.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--zap-devel` | bool | `false` | Enable development mode (human-readable, debug level). |
| `--zap-encoder` | string | `json` | Log encoding format: `json` or `console`. |
| `--zap-log-level` | string | `info` | Log level: `debug`, `info`, `error`, or an integer. |
| `--zap-stacktrace-level` | string | `dpanic` | Minimum level for stack traces: `info`, `error`, or `dpanic`. |

## Health Endpoints

| Path | Port | Description |
|------|------|-------------|
| `/healthz` | 8081 | Liveness probe — returns 200 if the process is alive |
| `/readyz` | 8081 | Readiness probe — returns 200 if the operator is ready to reconcile |

## Configuring via Helm

Override flags in the Helm chart using `manager.args`:

```yaml
manager:
  args:
    - --leader-elect
    - --zap-log-level=debug
    - --metrics-secure=false
```

Or via command line:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --set 'manager.args={--leader-elect,--zap-log-level=debug}'
```

## Next Steps

- [Monitoring](../admin/monitoring.md) — Metrics and log analysis
- [Helm Chart Values](helm-values.md) — Full chart configuration
