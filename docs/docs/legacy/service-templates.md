# Service Templates (v1alpha1)

:::{admonition} Deprecated — use Profiles in v1alpha2
:class: warning

`AIMServiceTemplate` / `AIMClusterServiceTemplate` are part of `aim.eai.amd.com/v1alpha1` and will be removed in a future release. New deployments should use [Profiles](../concepts/profiles.md) (`v1alpha2`), which carry self-contained runtime configurations (image + accelerator + engine + model sources) without requiring an intermediate template lookup.

See [Migrating to v1alpha2](migrating.md) for field-by-field conversion recipes and [Admin → Upgrading → Migration window](../admin/upgrading.md#migration-window) for how the two API versions coexist.
:::
This page is the operational reference for the legacy template shape. The advanced walkthroughs that used to live here (`customProfile`, `customTemplates`, `profileCopy`, fine-tuned template copies, auto-creation from model discovery) have v1alpha2 replacements documented in the [user guides](../guides/deploying-services.md) and the [migration recipes](migrating.md).

## Overview

For v1alpha1, templates fulfil two roles:

1. **Runtime configuration** — optimisation goals (`metric`), numeric precision, and GPU/CPU requirements.
2. **Discovery cache** — the controller runs the model's container image in dry-run mode and stores the discovered model sources on `status.modelSources[]`.

A service then references a template via `AIMService.spec.template.name` (or relies on automatic selection by the v1alpha1 template pipeline).

## Cluster vs namespace scope

| Resource | Scope | Notes |
|---|---|---|
| `AIMClusterServiceTemplate` | Cluster | Created by platform admins. Discovery runs in the operator namespace. Cannot enable caching directly — caches are created per-namespace via `AIMTemplateCache`. |
| `AIMServiceTemplate` | Namespace | Created by teams. Can enable caching via `spec.caching.enabled`. Discovery runs in the template's namespace, so namespace-scoped secrets are available. |

## Spec reference

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: qwen3-32b-throughput
  namespace: ml-research
spec:
  modelName: qwen-qwen3-32b
  runtimeConfigName: ml-research
  metric: throughput
  precision: fp8
  hardware:
    gpu:
      requests: 2
      model: MI300X
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: huggingface-creds
          key: token
  imagePullSecrets:
    - name: registry-credentials
```

| Field | Description | Immutable |
|---|---|---|
| `modelName` | `AIMModel` / `AIMClusterModel` name. | Yes |
| `runtimeConfigName` | Runtime configuration for storage defaults / discovery settings. Defaults to `default`. | No |
| `metric` | `latency` or `throughput`. | Yes |
| `precision` | `auto`, `fp4`, `fp8`, `fp16`, `fp32`, `bf16`, `int4`, `int8`. | Yes |
| `hardware.gpu.requests` | GPUs per replica. | Yes |
| `hardware.gpu.model` | GPU model (e.g. `MI300X`). | Yes |
| `hardware.cpu` | CPU requirements (alternative to `hardware.gpu`). | Yes |
| `imagePullSecrets` | Secrets for pulling discovery and inference images. | No |
| `serviceAccountName` | Discovery / inference pod service account. | No |
| `resources` | Container `ResourceRequirements`. Overrides model defaults. | No |
| `env` | Container env vars (typically download tokens). Namespace-scoped only. | No |
| `caching` | `caching.enabled: true` pre-warms model artifacts. Namespace-scoped only. | No |
| `modelSources` | Static sources skipping discovery. See [Static model sources](#static-model-sources). | — |

The v1alpha2 equivalent fields live on `AIMProfile` — see [Migrating to v1alpha2 → AIMServiceTemplate](migrating.md#aimservicetemplate-aimprofile).

## Discovery lifecycle

When a template is created or its spec changes the controller runs a Kubernetes Job using the model's image with a dry-run flag, extracts model source URIs / sizes / engine arguments, and writes the result to `status.modelSources[]` and `status.profile`. Cluster-scoped templates run discovery in the operator namespace; namespace-scoped templates run in their own namespace.

A global cap of **10 concurrent discovery jobs** is enforced. Templates over the cap wait in `Pending` with reason `AwaitingDiscovery`.

### Static model sources

Setting `spec.modelSources` skips discovery — the controller copies the static list to `status.modelSources[]` and the template transitions directly to `Ready`. Useful for non-standard images, environments without registry access, or templates whose sources are already known and stable.

```yaml
spec:
  modelSources:
    - name: Qwen/Qwen3-32B
      sourceURI: hf://Qwen/Qwen3-32B
      size: 16Gi
```

## Status reference

| Field | Description |
|---|---|
| `status` | `Pending`, `Progressing`, `NotAvailable`, `Ready`, `Degraded`, `Failed` |
| `conditions` | `Discovered`, `CacheReady`, `RuntimeConfigReady`, `ModelFound`, `Ready` |
| `resolvedRuntimeConfig` | Resolved runtime config metadata |
| `resolvedModel` | Resolved model metadata |
| `resolvedHardware` | Final GPU/CPU requirements after merging discovery + spec |
| `modelSources` | Discovered or static sources used by the cache layer |
| `profile` | Complete discovery output |
| `version` | Image-tag version (e.g. `0.8.5`) used by version-policy matching |

`NotAvailable` means the cluster has no nodes matching `hardware.gpu.model` (or sufficient CPU). The template controller watches node events and re-evaluates when nodes change.

## Template selection (legacy)

When an `AIMService` references a model without naming a specific template (`spec.template.name` empty), the v1alpha1 template pipeline:

1. Enumerates templates referencing the model.
2. Excludes templates not in `Ready` status.
3. Excludes templates whose GPU isn't present in the cluster.
4. Selects the single remaining candidate or surfaces an ambiguity / not-found condition.

For the v1alpha2 equivalent (ranking deployable profiles by `primary > type > version`), see [Services → Resolution shapes](../concepts/services.md#resolution-shapes).

## v1alpha2 replacements at a glance

| Legacy concept | v1alpha2 replacement |
|---|---|
| `AIMServiceTemplate` (runtime + discovery wrapper) | `AIMProfile` (self-contained), produced by `AIMModel` discovery or `AIMProfileSet` derivation |
| `AIMClusterServiceTemplate` | `AIMClusterProfile` |
| `spec.customProfile` (inline engine args / env) | `AIMService.spec.profileOverrides` + per-service overlay profile |
| `spec.customTemplates[]` on `AIMModel` | `AIMModel.spec.profiles.derivedFrom` (custom-model flow) — see [Custom Models](../guides/custom-models.md) |
| Auto-creation from model discovery (`spec.discovery.createServiceTemplates`) | Always-on — v1alpha2 image-discovery materialises `AIMProfile`s by default |
| Fine-tuned template copies (`spec.aimId` + `spec.modelSources` on `AIMModel`) | `AIMModel.spec.profiles.derivedFrom` (fine-tune flow) — see [Fine-Tuned Models](../guides/fine-tuned-models.md) |
| `AIMTemplateCache` | `AIMProfileCache` |

## Troubleshooting

### Template stuck in `Progressing`

Check the discovery job:

```bash
kubectl -n <namespace> get job -l aim.eai.amd.com/template=<template-name>
kubectl -n <namespace> logs job/<job-name>
```

Common causes: image pull failure (missing `imagePullSecrets`), container crash during dry-run, missing referenced runtime config.

### `status.modelSources` empty after discovery

```bash
kubectl -n <namespace> get aimservicetemplate <name> \
  -o jsonpath='{.status.conditions[?(@.type=="Discovered")]}'
```

The image may not be a valid AIM image or may not publish model sources in the discovery output.

### Template `NotAvailable`

The cluster has no nodes matching `hardware.gpu.model`. Either install the matching GPU hardware, update the operator's accelerator detection, or relax the template's `hardware.gpu.model`.

## Related documentation

- [Profiles](../concepts/profiles.md) — v1alpha2 replacement (recommended)
- [Models](../concepts/models.md) — Model catalog and discovery in v1alpha2
- [Runtime Config](../concepts/runtime-config.md) — Resolution algorithm shared by both versions
- [Model Caching](../concepts/caching.md) — Cache lifecycle (`AIMProfileCache` / `AIMTemplateCache`)
- [Deploying Services](../guides/deploying-services.md) — v1alpha2 service patterns
- [Migrating to v1alpha2](migrating.md) — Field-by-field migration recipes
- [Admin → Upgrading → Migration window](../admin/upgrading.md#migration-window) — Coexistence and dispatch rules
