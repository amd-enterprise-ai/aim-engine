# Installation Reference

This page covers advanced installation options for AIM Engine. For a basic install, see the [Getting Started guide](../getting-started/installation.md).

## Helm Chart Configuration

All configuration is done through Helm values. See [Helm Chart Values](../reference/helm-values.md) for the complete reference.

### Operator Resources

Adjust operator resource limits for larger clusters:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --create-namespace \
  --set manager.resources.limits.memory=8Gi \
  --set manager.resources.requests.memory=512Mi
```

### Leader Election

Leader election is enabled by default (`--leader-elect` in `manager.args`). This ensures only one operator instance is active when running multiple replicas for high availability.

### Metrics

The metrics endpoint is enabled by default on port 8443 with TLS. To disable TLS for the metrics endpoint:

```bash
helm install aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <version> \
  --namespace aim-system \
  --set 'manager.args={--leader-elect,--metrics-secure=false}'
```

### CRD Management

CRDs are distributed as a separate Helm chart and should be installed before the operator. See [Installation](../getting-started/installation.md#1-install-crds).

## Next Steps

- [Helm Chart Values](../reference/helm-values.md) — Full values reference
- [KServe Configuration](kserve-configuration.md) — Configure the KServe dependency
- [Security](security.md) — RBAC and pod security configuration
