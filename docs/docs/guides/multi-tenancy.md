# Multi-Tenancy

AIM Engine supports multi-tenant deployments through a combination of cluster-scoped and namespace-scoped resources.

## Resource Scoping

| Resource | Cluster-Scoped | Namespace-Scoped |
|----------|---------------|-----------------|
| Models | `AIMClusterModel` | `AIMModel` |
| Profiles | `AIMClusterProfile` | `AIMProfile` |
| Runtime Config | `AIMClusterRuntimeConfig` | `AIMRuntimeConfig` |
| Model Sources | `AIMClusterModelSource` | — |
| Services | — | `AIMService` |
| Artifacts | — | `AIMArtifact` |

Namespace-scoped resources always take precedence over cluster-scoped ones during resolution.

## Team Isolation

A typical multi-tenant setup:

1. **Cluster admin** creates cluster-scoped resources shared by all teams:
   - `AIMClusterModelSource` for model discovery
   - `AIMClusterRuntimeConfig` for default routing, storage, and policies
   - `AIMClusterProfile` for validated runtime profiles

2. **Teams** work in their own namespaces with:
   - `AIMService` resources for their inference endpoints
   - `AIMRuntimeConfig` for team-specific credentials and overrides
   - `AIMModel` for custom models only their team needs

### Example: Namespace Configuration

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team-a
spec:
  caching:
    env:
      - name: HF_TOKEN
        valueFrom:
          secretKeyRef:
            name: team-a-hf-token
            key: token
  routing:
    pathTemplate: "/team-a/{.metadata.name}"
```

## Override Hierarchy

Configuration is resolved with the most specific scope winning:

| Setting | Resolution Order |
|---------|-----------------|
| Model | `AIMModel` (namespace) → `AIMClusterModel` (cluster) |
| Profile | `AIMProfile` (namespace) → `AIMClusterProfile` (cluster) |
| Runtime config | `AIMRuntimeConfig` (namespace) → `AIMClusterRuntimeConfig` (cluster) |
| Inference container env | Service `spec.env` → Profile `containerEnv` → RuntimeConfig `spec.env` (namespace) → ClusterRuntimeConfig `spec.env` ([full order](../concepts/runtime-config.md#inference-container-env)) |
| Download Job env | Service `spec.caching.env` → Profile `spec.caching.env` → RuntimeConfig `spec.caching.env` (namespace) → ClusterRuntimeConfig `spec.caching.env` ([full order](../concepts/runtime-config.md#download-job-env)) |

## Label Propagation

AIM Engine can propagate labels from `AIMService` resources to managed child resources (InferenceService, HTTPRoute, PVCs). This is useful for cost tracking, team attribution, and policy enforcement.

Enable via runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  labelPropagation:
    enabled: true
    match:
      - "aim.eai.amd.com/*"
      - "team-*"
      - "cost-center"
```

Labels on the `AIMService` matching these patterns are automatically applied to all child resources. Labels matching `aim.eai.amd.com/*` are always propagated regardless of this setting.

## RBAC

AIM Engine creates helper ClusterRoles for each CRD when `rbacHelpers.enable` is true (default):

| Role | Permissions |
|------|-------------|
| `aimservice-admin` | Full access to AIMService resources |
| `aimservice-editor` | Create, update, delete AIMService resources |
| `aimservice-viewer` | Read-only access to AIMService resources |

Similar roles exist for all CRDs. Bind these to team groups or service accounts:

```bash
kubectl create rolebinding team-a-aimservice \
  --clusterrole=aimservice-editor \
  --group=team-a \
  --namespace=ml-team-a
```

AIMService authors control the security context of their inference pods. For what AIM Engine refuses and what it does not, see the [warning in Team RBAC](../admin/security.md#team-rbac-security-contexts).

## Next Steps

- [Runtime Configuration](../concepts/runtime-config.md) — Configuration resolution details
- [Security](../admin/security.md) — RBAC and security configuration
- [Private Registries](private-registries.md) — Per-team credential management
