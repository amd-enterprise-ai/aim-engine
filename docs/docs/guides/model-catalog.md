# Model Catalog

AIM Engine maintains a catalog of available AI models as Kubernetes custom resources. This guide covers browsing, discovering, and managing models.

## Browsing Models

List all available models:

```bash
# Cluster-scoped models (available to all namespaces)
kubectl get aimclustermodels

# Namespace-scoped models
kubectl get aimmodels -n <namespace>
```

View model details:

```bash
kubectl get aimclustermodel qwen3-32b -o yaml
```

Key fields to look for:

- `spec.image` — The container image for this model
- `status.status` — Current state (`Ready`, `Pending`, etc.)
- `metadata.labels` — Model metadata (hardware, precision, etc.)

## Automatic Model Discovery

`AIMClusterModelSource` automatically discovers models from container registries and creates `AIMClusterModel` resources.

### Setting Up Discovery

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: amd-models
spec:
  registry: docker.io
  images:
    - "amdenterpriseai/aim-qwen-qwen3-32b"
    - "amdenterpriseai/aim-deepseek-deepseek-r1"
  versions:
    - ">=0.8.4"
  syncInterval: 1h
  maxModels: 500
```

### Selecting Images

Use `images` for simple explicit lists, or `filters` for advanced per-filter controls.

```yaml
spec:
  images:
    - "amdenterpriseai/aim-qwen-qwen3-32b"
    - "amdenterpriseai/aim-deepseek-deepseek-r1"
  versions:
    - ">=1.0.0"
    - "<2.0.0"
```

Advanced example using `filters`:

```yaml
spec:
  filters:
    - image: "amdenterpriseai/aim-qwen-qwen3-32b"
      versions:
        - ">=1.0.0"
        - "<2.0.0"
      exclude:
        - "amdenterpriseai/aim-experimental"
```

`exclude` values are exact repository matches.
`spec.images` and `spec.filters` are mutually exclusive (set exactly one).

### Private Registries

Authenticate to private registries using image pull secrets:

```yaml
spec:
  registry: ghcr.io
  imagePullSecrets:
    - name: ghcr-pull-secret
  images:
    - "my-org/private-model:1.2.3"
```

The secret must exist in the operator namespace (typically `aim-system`).

### Monitoring Sync Status

```bash
kubectl get aimclustermodelsource amd-models -o jsonpath='{.status}' | jq
```

## Creating Models Manually

Create a namespace-scoped model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: qwen3-32b
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

Or a cluster-scoped model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModel
metadata:
  name: qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

## Model Resolution

When an `AIMService` references a model by name, AIM Engine resolves it in this order:

1. Namespace-scoped `AIMModel` with that name
2. Cluster-scoped `AIMClusterModel` with that name

Namespace-scoped resources take precedence, allowing teams to override cluster models.

When using `model.image` instead of `model.name`, AIM Engine searches for any model matching that image URI. If none exists, it creates an `AIMModel` automatically.

## Next Steps

- [Deploying Services](deploying-services.md) — Use models in inference services
- [Model Sources](../concepts/model-sources.md) — Deep dive into AIMClusterModelSource
- [AIM Models](../concepts/models.md) — Full model lifecycle and discovery mechanics
