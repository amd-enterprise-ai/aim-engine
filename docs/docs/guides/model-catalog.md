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
NAME                    STATUS   KIND    AIMID                                   VERSION   MANAGED   READY   BASE   AGE
qwen-qwen3-32b          Ready    Image   qwen/qwen3-32b                          0.8.5     5         5       0      3d
llama-3-8b-official     Ready    Image   meta-llama/Llama-3-8B-Instruct          0.9.0     7         7       0      3d
aim-base-vllm           Ready    Image                                           0.11      8         8       8      1d
```

`KIND` identifies the model onboarding flow: `Image`, `Generated`, `Derived`, or `Custom`. `VERSION` is the effective image tag and can be empty for flows without an image. `BASE > 0` identifies a base-image model (which also has `KIND=Image`). See [AIM Models](../concepts/models.md) for the model flows and status-field details.

### View model details

```bash
kubectl get aimmodel qwen-qwen3-32b -o yaml
```

Key fields:

| Field | Purpose |
|---|---|
| `spec.image` | Discovery image (Official flow) |
| `spec.modelId` | Canonical model identity used to generate a profile from RuntimeConfig fallbacks (Generated flow) |
| `spec.profiles` | Derivation spec (Fine-tuned / Custom flow) |
| `status.aimId` | Resolved architecture identifier |
| `status.managedProfiles` | Counts: `total`, `ready`, `deployable`, `base` |
| `status.discoveryCacheRef` | Reference to the discovery cache `ConfigMap` |
| `status.profileSetRef` | Child profile set (derivation flows only) |
| `status.profileGeneration` | Resolution strategy and matched RuntimeConfig fallbacks (Generated flow only) |

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

### Generated flow

Apply a canonical model ID when the platform provides a generic runtime for the cluster hardware:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen3-5-0-8b
  namespace: ml-team
spec:
  modelId: Qwen/Qwen3.5-0.8B
```

By default, `aimId` is the same as `modelId`, the source URI is `hf://<modelId>`, and one accelerator is requested. Override those fields when the weights or hardware shape differ:

```yaml
spec:
  modelId: acme/qwen-finetune
  aimId: Qwen/Qwen3.5-0.8B
  source:
    uri: s3://customer-models/qwen-finetune
  accelerator:
    vendor: nvidia
    model: H100
    count: 4
```

The model becomes ready when a matching `AIMRuntimeConfig`/`AIMClusterRuntimeConfig` profile-generation fallback and compatible hardware are available. The default Helm installation includes a generic NVIDIA vLLM fallback; platform administrators can replace or extend it for their fleet.

See [AIM Models](../concepts/models.md#flow-2-generated-runtime-profile) for status and resolution details, and [Runtime Configuration](../concepts/runtime-config.md#model-profile-generation-fallbacks) for fallback configuration.

### Fine-tuned flow

Derive deployable profiles from an image-backed `AIMModel` / `AIMClusterModel` with complete discovery. Verify its `DiscoveryReady` condition and `status.discoveredProfiles`. The fine-tune selects published definitions by `aimId`, including definitions without materialised profile resources.

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
        modelRef:
          name: aim-base-vllm
    versionPolicy: all
    overrides:
      aimId: acme/custom-transformer
      modelId: acme/custom-transformer
      modelSources:
        - modelId: acme/custom-transformer
          sourceUri: s3://acme-models/custom-transformer
```

See [Custom Models](custom-models.md) for the full walkthrough.

### Inline image overrides

Use a separate profile set or derived model to select and transform an image model's catalog definitions. See [Deriving hardware variants](../concepts/models.md#deriving-hardware-variants) for GPU remapping and [profile sets](../concepts/profilesets.md#examples) for partitioning examples.

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

The discovered `AIMClusterModel` resources are then reconciled the same way as manually applied image-based models. Registry-discovered images are written through the v1alpha1 API with `spec.image` set.

:::{admonition} v1alpha1 source resource
:class: note

`AIMClusterModelSource` itself remains under `v1alpha1`. Registry-discovered images are also created through v1alpha1, while entries declared under `spec.models` are created through v1alpha2 because `spec.modelId` is v1alpha2-only. Both API versions address the same stored v1alpha2 object.
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

This query lists models with materialised base profiles. Models with zero supported base profiles still publish their discovered base definitions for [custom-model derivation](custom-models.md). Check their discovery status and select them with `role: base`.

## Next steps

- [Deploying Services](deploying-services.md) — Deploy a model once it's in the catalog
- [Fine-Tuned Models](fine-tuned-models.md) — Derive a fine-tune AIMModel from a catalog entry
- [Custom Models](custom-models.md) — Derive a custom AIMModel from a base image
- [AIM Models](../concepts/models.md) — Full lifecycle and discovery mechanics
- [Runtime Configuration](../concepts/runtime-config.md) — Configure generated-profile fallbacks
- [Model Sources](../concepts/model-sources.md) — Deep dive into `AIMClusterModelSource`
