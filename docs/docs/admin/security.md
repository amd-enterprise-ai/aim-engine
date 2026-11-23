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

Available for: `aimservice`, `aimmodel`, `aimclustermodel`, `aimartifact`, `aimtemplatecache`, `aimprofile`, `aimclusterprofile`, `aimprofileset`, `aimclusterprofileset`, `aimprofilecache`, `aimservicetemplate`, `aimclusterservicetemplate`, `aimruntimeconfig`, `aimclusterruntimeconfig`.

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

(team-rbac-security-contexts)=
:::{warning}
An AIMService author controls the security context of the inference Pod (`spec.podSecurityContext` and `spec.securityContext`) and, with `spec.model.image`, its image. AIM Engine creates the Pod, so the author does not need permission to create Pods.

AIM Engine refuses the values in these fields that the Pod Security Standard `baseline` forbids, for example `privileged: true` and added capabilities. For the full list, see [Security contexts](../concepts/runtime-config.md#security-contexts). A user with `aimservice-editor` or `aimservice-admin` can still run the inference container as root. AIM images run as root when the security context does not select a user, and the AIMService value replaces a runtime config value that selects a non-root user. AIM Engine does not apply the rules of `restricted`. If the inference pods in a namespace must meet `restricted`, for example to run as non-root, enforce it in that namespace:

```bash
kubectl label namespace ml-team-a pod-security.kubernetes.io/enforce=restricted
```

To run inference pods in such a namespace, see [Restricted Pod Security](../guides/restricted-pod-security.md).

Users with `aimruntimeconfig-editor` or `aimruntimeconfig-admin` have the same control as AIMService authors. A namespace AIMRuntimeConfig sets the security contexts of the services in its namespace that use it and do not set their own. `aimclusterruntimeconfig-editor` and `aimclusterruntimeconfig-admin` do the same for all namespaces.
:::

## Inference Route Hostnames

AIM Engine exposes inference services through Gateway API `HTTPRoute` resources. A route with no hostname attaches to **every** listener on its parent Gateway. On a Gateway that fronts multiple hostnames (for example `workloads.*`, `api.*`, and `ui.*`), an unpinned route is therefore reachable on listeners that may not enforce the same authentication, which can expose a model endpoint without auth.

To prevent this, pin routes to the intended hostname via `spec.routing.hostnames` (or a runtime config default). When the parent Gateway has more than one listener, a hostname is **required**: a routing-enabled service without one is refused (no `HTTPRoute` is created) and reports `ConfigValid=False` / `RouteHostnameRequired`. See [Routing and Ingress](../guides/routing-and-ingress.md#hostnames).

Authentication itself is enforced at the gateway layer (for example via `cluster-auth/*` annotations propagated onto the route); hostname pinning ensures the route only attaches to the listener where that enforcement applies.

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
