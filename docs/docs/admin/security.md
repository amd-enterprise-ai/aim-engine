# Security

Security configuration for AIM Engine deployments.

## Pod Security

The operator runs with restrictive security defaults:

| Setting | Value |
|---------|-------|
| `runAsNonRoot` | `true` |
| `readOnlyRootFilesystem` | `true` |
| `allowPrivilegeEscalation` | `false` |
| `capabilities.drop` | `ALL` |
| `seccompProfile.type` | `RuntimeDefault` |

These are configured in the Helm chart and can be adjusted via `manager.podSecurityContext` and `manager.securityContext` values.

## RBAC

### Operator Permissions

The operator runs with a ClusterRole (`aim-engine-manager-role`) that grants:

- Full access to all AIM CRDs (`aim.eai.amd.com`)
- Read access to nodes, namespaces, pods, secrets
- Manage PVCs, jobs, and events
- Create/manage KServe InferenceServices
- Create/manage Gateway API HTTPRoutes
- Read storage classes

### Helper Roles

When `rbacHelpers.enable` is `true` (default), the chart creates admin/editor/viewer ClusterRoles for each CRD:

| Role Pattern | Permissions |
|-------------|-------------|
| `{crd}-admin` | Full access including status |
| `{crd}-editor` | Create, update, delete |
| `{crd}-viewer` | Read-only |

Available for: `aimservice`, `aimmodel`, `aimclustermodel`, `aimartifact`, `aimtemplatecache`, `aimservicetemplate`, `aimclusterservicetemplate`, `aimruntimeconfig`, `aimclusterruntimeconfig`.

### Example: Team RBAC

Grant a team editor access to services and viewer access to cluster resources:

```bash
# Edit services in their namespace
kubectl create rolebinding team-a-services \
  --clusterrole=aimservice-editor \
  --group=team-a \
  --namespace=ml-team-a

# View cluster models (read-only)
kubectl create clusterrolebinding team-a-models \
  --clusterrole=aimclustermodel-viewer \
  --group=team-a
```

## TLS

### Metrics Endpoint

The metrics endpoint serves over HTTPS by default (port 8443). Provide certificates via:

- **cert-manager** — Set `certManager.enable: true` in Helm values
- **Manual** — Mount certificates and set `--metrics-cert-path`

To disable TLS for metrics (not recommended for production):

```bash
--set 'manager.args={--leader-elect,--metrics-secure=false}'
```

## Secrets Management

Sensitive credentials (registry tokens, S3 keys) are managed through Kubernetes Secrets referenced in runtime configurations:

```yaml
spec:
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-credentials
          key: token
```

The operator reads these references but never stores credential values in CRD status or logs.

## Next Steps

- [Multi-Tenancy](../guides/multi-tenancy.md) — Team isolation patterns
- [Installation Reference](installation.md) — Helm security values
- [Private Registries](../guides/private-registries.md) — Credential configuration
