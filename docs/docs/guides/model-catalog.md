# Model Catalog

AIM Engine maintains a catalog of available models as `AIMModel` and `AIMClusterModel` resources. This guide covers browsing, applying, and auto-discovering models.

:::{admonition} v1alpha2
:class: note

All examples use `aim.eai.amd.com/v1alpha2`. For the deprecated v1alpha1 `AIMModel` shape with `spec.custom` / `spec.modelSources`, see [Legacy AIMModel](../legacy/aimmodel-v1alpha1.md).
:::
## Browsing models

```bash
# Cluster-scoped models (visible to all namespaces)
kubectl get aimclustermodels

# Namespace-scoped models
kubectl get aimmodels -n <namespace>

# Across all namespaces
kubectl get aimmodels --all-namespaces
```

The default kubectl printcolumns show status and key counts:

```
NAME                    STATUS   AIMID                                   MANAGED   READY   BASE   AGE
qwen-qwen3-32b          Ready    qwen/qwen3-32b                          5         5       0      3d
llama-3-8b-official     Ready    meta-llama/Llama-3-8B-Instruct          7         7       0      3d
aim-base-vllm           Ready                                            8         8       8      1d
```

`BASE > 0` identifies a base-image model. See [AIM Models](../concepts/models.md) for the three model flows.

### View model details

```bash
kubectl get aimmodel qwen-qwen3-32b -o yaml
```

Key fields:

| Field | Purpose |
|---|---|
| `spec.image` | Discovery image (Official flow) |
| `spec.profiles` | Derivation spec (Fine-tuned / Custom flow) |
| `status.aimId` | Resolved architecture identifier |
| `status.managedProfiles` | Counts: `total`, `ready`, `deployable`, `base` |
| `status.discoveryCacheRef` | Reference to the discovery cache `ConfigMap` |
| `status.profileSetRef` | Child profile set (derivation flows only) |

## Applying models manually

### Official flow

Apply an AMD-published AIM image directly:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen-qwen3-32b
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

Or cluster-scoped so all namespaces can resolve to it:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterModel
metadata:
  name: qwen-qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

### Fine-tuned flow

Derive deployable profiles from an `AIMModel` / `AIMClusterModel` that is already applied to the cluster and has produced deployable profiles (verify with `kubectl get aimmodel <name> -o jsonpath='{.status.managedProfiles.deployable}'`). The fine-tune resource selects those in-cluster profiles by `aimId` — it does not pull from a registry by itself.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen-finetune-acme
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        aimId: qwen/qwen3-32b
    versionPolicy: pinned
    version: "0.8.5"
    overrides:
      modelSources:
        - modelId: acme/qwen3-32b-finetune
          sourceUri: hf://acme/qwen3-32b-finetune
```

See [Fine-Tuned Models](fine-tuned-models.md) for the full walkthrough.

### Custom flow

Apply a base-image model, then derive deployable profiles for your own architecture:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: aim-base-vllm
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-base:0.11
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: acme-custom-transformer
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        role: base
        aimId: acme/custom-transformer
        modelRef:
          name: aim-base-vllm
    versionPolicy: all
    overrides:
      modelSources:
        - modelId: acme/custom-transformer
          sourceUri: s3://acme-models/custom-transformer
```

See [Custom Models](custom-models.md) for the full walkthrough.

## Automatic discovery from a registry

`AIMClusterModelSource` discovers official AIM images from a container registry and creates an `AIMClusterModel` for each match.

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

The discovered `AIMClusterModel` resources are then reconciled the same way as manually-applied ones. They use the v1alpha2 spec shape (`spec.image` set).

:::{admonition} v1alpha1 source resource
:class: note

`AIMClusterModelSource` itself remains under `v1alpha1` while the resources it creates use the v1alpha2 `AIMModel` shape. This intentionally avoids gating discovery on a migration.
:::
### Selecting images

Use `images` for simple explicit lists, or `filters` for per-image controls:

```yaml
spec:
  images:
    - "amdenterpriseai/aim-qwen-qwen3-32b"
    - "amdenterpriseai/aim-deepseek-deepseek-r1"
  versions:
    - ">=1.0.0"
    - "<2.0.0"
```

Advanced filters:

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

`spec.images` and `spec.filters` are mutually exclusive (set exactly one).

### Private registries

```yaml
spec:
  registry: ghcr.io
  imagePullSecrets:
    - name: ghcr-pull-secret
  images:
    - "my-org/private-model:1.2.3"
```

The secret must exist in the operator namespace (typically `aim-system`).

### Monitoring sync

```bash
kubectl get aimclustermodelsource amd-models -o jsonpath='{.status}' | jq
```

See [Model Sources concept](../concepts/model-sources.md) for the full discovery lifecycle.

## Model resolution

When an `AIMService` references a model by name (`spec.model.name`), the resolver checks:

1. Namespace-scoped `AIMModel` with that name in the service's namespace.
2. Cluster-scoped `AIMClusterModel` with that name.

Namespace wins, letting teams override a cluster default by name.

The resolved scope is recorded in the service's `status.resolvedModel.scope`.

## Identifying base-image models

```bash
kubectl get aimmodel -A -o json | jq -r '
  .items[]
  | select(.status.managedProfiles.base > 0 and .status.managedProfiles.deployable == 0)
  | "\(.metadata.namespace)/\(.metadata.name)"
'
```

These are the models that produce base profiles for [custom-model derivation](custom-models.md).

## Next steps

- [Deploying Services](deploying-services.md) — Deploy a model once it's in the catalog
- [Fine-Tuned Models](fine-tuned-models.md) — Derive a fine-tune AIMModel from a catalog entry
- [Custom Models](custom-models.md) — Derive a custom AIMModel from a base image
- [AIM Models](../concepts/models.md) — Full lifecycle and discovery mechanics
- [Model Sources](../concepts/model-sources.md) — Deep dive into `AIMClusterModelSource`
