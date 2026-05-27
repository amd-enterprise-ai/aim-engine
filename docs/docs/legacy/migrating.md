# Migrating to v1alpha2

This guide maps v1alpha1 concepts to their v1alpha2 equivalents and provides conversion recipes for the common deployment patterns.

!!! note
    Read the [Legacy Overview](index.md) first for context on coexistence rules and the deprecation timeline.

## Resource mapping

| v1alpha1 | v1alpha2 | Migration |
|---|---|---|
| `AIMServiceTemplate.spec` | `AIMProfile.spec` | Templates → profiles, one profile per template profile. |
| `AIMClusterServiceTemplate.spec` | `AIMClusterProfile.spec` | Same, cluster-scoped. |
| `AIMService.spec.template` | `AIMService.spec.profile` | Reference a profile instead of a template. |
| `AIMService.spec.overrides` | `AIMService.spec.profileOverrides` | Override shape changed (see below). |
| `AIMService.spec.model.image` | `AIMModel.spec.image` (then `AIMService.spec.model.name`) | Image-based services should onboard an explicit AIMModel. |
| `AIMModel.spec.custom` | `AIMModel.spec.image` (base) + `AIMModel.spec.profiles` | Two-step custom-model flow. |
| `AIMModel.spec.customTemplates` | `AIMProfileSet` | Standalone derivation resource. |
| `AIMModel.spec.profileCopy` | `AIMModel.spec.profiles` | Profile-copy is now first-class (groups derivedFrom + version + overrides). |
| `AIMModel.spec.modelSources` | `AIMProfile.spec.modelSources` | Sources live on each profile. |
| `AIMTemplateCache` | `AIMProfileCache` | Created automatically from `AIMProfile.spec.caching`. |

## Resource-by-resource walkthrough

### AIMService

**Before (v1alpha1):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    name: qwen-qwen3-32b
  template:
    selectionCriteria:
      precision: fp8
      acceleratorModel: MI300X
      metric: latency
```

**After (v1alpha2):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    name: qwen-qwen3-32b
  profile:
    selector:
      precision: fp8
      acceleratorModel: MI300X
      metric: latency
```

The selector lives under `spec.profile.selector` (not `spec.template.selectionCriteria`). The same field names are reused — `precision`, `acceleratorModel`, `acceleratorCount`, `metric`, `engine`, `type`.

For services that reference a template by name:

**Before:**

```yaml
spec:
  template:
    name: qwen-qwen3-32b-mi300x-fp8-latency
```

**After:**

```yaml
spec:
  profile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
```

The profile names produced by v1alpha2 discovery match the v1alpha1 template profile names in most cases.

### AIMService.spec.overrides → AIMService.spec.profileOverrides

The override shape changes substantively in v1alpha2:

| v1alpha1 (`spec.overrides`) | v1alpha2 (`spec.profileOverrides`) |
|---|---|
| Flat `AIMRuntimeParameters` (engineArgs, env, ...) | Structured `ProfileOverrides` (modelSources, containerEnv, engineEnv, engineArgs, acceleratorModel, acceleratorCount) |
| Modifies template selection in place | Materialises a service-owned overlay AIMProfile |
| No effect on cache key | Participates in cache-key computation |

**Before:**

```yaml
spec:
  template:
    name: qwen-qwen3-32b-mi300x-fp8-latency
  overrides:
    engineArgs:
      max-model-len: "32768"
```

**After:**

```yaml
spec:
  profile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
  profileOverrides:
    engineArgs:
      max-model-len: 32768
```

Notice the JSON value is now a typed integer rather than a string — `engineArgs` accepts typed JSON in v1alpha2.

### Custom models

The v1alpha1 `AIMModel.spec.custom + spec.customTemplates` pattern is replaced by a two-step flow:

**Before (v1alpha1):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: acme-transformer
spec:
  aimId: acme/custom-transformer
  custom:
    image: amdenterpriseai/aim-base:0.8.5
  modelSources:
    - modelId: acme/custom-transformer
      sourceUri: s3://acme-models/custom-transformer
  customTemplates:
    - name: acme-transformer-mi300x-fp16
      acceleratorModel: MI300X
      acceleratorType: gpu
      acceleratorCount: 1
      precision: fp16
      metric: throughput
      engine: vllm
```

**After (v1alpha2):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: aim-base-vllm
spec:
  image: amdenterpriseai/aim-base:0.11
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: acme-transformer
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

The base-image AIMModel produces base profiles (one per accelerator/precision/metric combination the base image supports). The custom-model AIMModel's derivation promotes those base profiles to deployable profiles by overriding `modelSources` and supplying the target `aimId`.

See [Custom Models](../guides/custom-models.md) for the full walkthrough.

### Fine-tunes

v1alpha1 had no first-class fine-tune flow — you'd either fork an `AIMServiceTemplate` and edit its model sources, or use `AIMService.spec.overrides`. v1alpha2 makes fine-tunes a discrete AIMModel:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen-finetune-acme
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

See [Fine-Tuned Models](../guides/fine-tuned-models.md) for the full walkthrough.

### AIMServiceTemplate → AIMProfile

Hand-authored v1alpha1 service templates become AIMProfile resources. The fields map almost 1:1:

**Before (v1alpha1):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: qwen3-32b-throughput
  namespace: ml-team
spec:
  modelName: qwen-qwen3-32b
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1
  precision: fp8
  metric: throughput
  engine: vllm
  engineArgs:
    gpu-memory-utilization: "0.95"
```

**After (v1alpha2):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: qwen3-32b-throughput
  namespace: ml-team
spec:
  aimId: qwen/qwen3-32b
  modelId: qwen/qwen3-32b-fp8
  acceleratorModel: MI300X
  acceleratorType: gpu
  acceleratorCount: 1
  precision: fp8
  metric: throughput
  engine: vllm
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  engineArgs:
    gpu-memory-utilization: "0.95"
  modelSources:
    - modelId: qwen/qwen3-32b-fp8
      sourceUri: hf://qwen/qwen3-32b-fp8
```

Differences:

- `modelName` is gone — profiles aren't tied to a model name; they declare `aimId` and `modelId` directly.
- `image` is now required on the profile (templates inherited it from the parent model via discovery).
- `modelSources` lives on the profile (templates inherited them from the parent model).

The simpler path: re-create the model through v1alpha2 discovery and let the controller produce the equivalent profiles. Hand-authoring profiles is only necessary when you can't run discovery.

### AIMTemplateCache → AIMProfileCache

You don't typically apply `AIMProfileCache` directly. It's created automatically by the AIMService controller when `spec.caching` is enabled (or by `AIMProfile.spec.caching.enabled` for namespace-scoped profiles).

For pre-warming a model independent of any service:

**Before (v1alpha1):**

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMTemplateCache
metadata:
  name: qwen-cache
spec:
  templateName: qwen-qwen3-32b-mi300x-fp8-latency
```

**After (v1alpha2):**

Set `spec.caching.enabled: true` on the corresponding namespace `AIMProfile`. The controller then provisions an `AIMProfileCache` automatically:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: qwen-qwen3-32b-mi300x-fp8-latency
  namespace: ml-team
spec:
  # ... profile spec ...
  caching:
    enabled: true
```

Cluster-scoped profiles cannot enable caching directly (cluster-scoped caches not yet implemented).

## Migration order

The recommended sequence:

1. **Cluster admins first** — onboard cluster-scoped models via `AIMClusterModel` with `spec.image`. Verify deployable profiles materialise.
2. **Namespace admins** — migrate namespace-scoped `AIMServiceTemplate` to `AIMProfile` (or rebuild from a namespace `AIMModel`).
3. **Services last** — switch `AIMService.spec.template` to `spec.profile` (or `spec.model.name`).
4. **Verify** with `kubectl get aimservice <name> -o jsonpath='{.status.resolvedProfile}'`.
5. **Clean up** — delete v1alpha1 templates after confirming no service references remain.

## Validation traps

The v1alpha2 CRD rejects a handful of fields that were valid on v1alpha1:

| Rejected on v1alpha2 | Resource | Why |
|---|---|---|
| `spec.aimId` | `AIMModel` | Resolved automatically (image discovery) or supplied by `derivedFrom.overrides.aimId`. |
| `spec.modelSources` | `AIMModel` | Lives on `AIMProfile` instead. |
| `spec.custom` | `AIMModel` | Replaced by base-image + `derivedFrom`. |
| `spec.customTemplates` | `AIMModel` | Replaced by `AIMProfileSet` / `derivedFrom`. |
| `spec.discovery` | `AIMModel` | Discovery configuration now lives at the controller level. |
| `spec.defaultServiceTemplate` | `AIMModel` | Profiles select themselves via `primary: true`. |
| `spec.runtimeConfigName` | `AIMModel` | Runtime config is referenced on the service. |
| `spec.env` | `AIMModel` | Lives on the profile as `containerEnv` / `engineEnv`. |
| `spec.imageMetadata` | `AIMModel` | Internal; not user-facing. |
| `spec.profileCopy` | `AIMModel` | Replaced by `derivedFrom`. |
| Both `spec.image` and `spec.profiles` | `AIMModel` | Exactly one required. |
| Neither | `AIMModel` | Exactly one required. |
| `spec.template` | `AIMService` | Use `spec.profile`. |
| `spec.overrides` | `AIMService` | Use `spec.profileOverrides`. |
| `spec.profileOverrides` without `spec.profile.name` | `AIMService` | Overlay requires a named seed. |
| `spec.profile.selector.role: base` | `AIMService` | Services always resolve to deployable profiles. |
| `spec.profile.selector` without `aimId` or `modelRef` or paired `spec.model` | `AIMService` | Resolver needs an anchor for watch fan-out. |

If admission rejects a v1alpha1 manifest, the error message is targeted — it names the field and the v1alpha2 equivalent.

## After migration

Once a workload is migrated:

- `kubectl get aimservice <name>` shows the resolved profile in the printcolumn output (priority 1).
- `status.resolvedProfile` carries the chosen `AIMProfile` reference.
- The KServe `InferenceService` is named `<service-name>` (unchanged from v1alpha1) — your downstream consumers don't need to update endpoints.

## Related documentation

- [Legacy Overview](index.md)
- [AIM Models](../concepts/models.md) — Three model flows in detail
- [Services](../concepts/services.md) — v1alpha2 resolution shapes
- [Profiles](../concepts/profiles.md) — Self-contained runtime configurations
- [Fine-Tuned Models](../guides/fine-tuned-models.md) — Migration target for fine-tunes
- [Custom Models](../guides/custom-models.md) — Migration target for `spec.custom`/`customTemplates`
