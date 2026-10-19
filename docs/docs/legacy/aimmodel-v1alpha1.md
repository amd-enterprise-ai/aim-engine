# AIMModel (v1alpha1)

:::{admonition} Deprecated
:class: warning

The v1alpha1 `AIMModel` shape (`spec.custom`, `spec.modelSources`, `spec.customTemplates`, `spec.profileCopy`, ...) is deprecated. New deployments should use [v1alpha2 AIMModel](../concepts/models.md), which supports official, generated, fine-tuned, and custom flows. See [Migrating to v1alpha2](migrating.md) for conversion recipes.
:::
This page documents the legacy `AIMModel` spec fields and their replacements.

## Overview

v1alpha1 `AIMModel` had a wide spec that supported several overlapping flows:

- **Image-based** — `spec.image` for AMD-published AIM images.
- **Custom-templates** — `spec.custom` + `spec.customTemplates` for bringing your own model architecture.
- **Profile-copy** — `spec.profileCopy` for replicating a published profile onto custom weights.
- **Defaults** — `spec.defaultServiceTemplate`, `spec.runtimeConfigName`, `spec.env` to influence downstream resources.

v1alpha2 supports image discovery through `spec.image`, external derivation through `spec.profiles.derivedFrom`, and generated onboarding through `spec.modelId`. These flows are mutually exclusive.

## v1alpha1 spec fields

| Field | Purpose | v1alpha2 equivalent |
|---|---|---|
| `spec.image` | AIM container image to discover | `spec.image` (unchanged for official flow) |
| `spec.aimId` | Model architecture identifier | Resolved from image discovery, supplied by `profiles.overrides.aimId`, or set with `spec.modelId` for generated onboarding |
| `spec.modelSources` | Static model artifact sources | Lives on `AIMProfile.spec.modelSources` |
| `spec.custom.image` | Base image for a custom model | `spec.image` on a shared base-image AIMModel |
| `spec.custom.engineArgs` | Engine arguments for the custom model | `spec.profiles.overrides.engineArgs` (or pre-baked into base-image profiles) |
| `spec.customTemplates` | Inline runtime profiles for a custom model | `AIMProfileSet` derivation (or `AIMModel.spec.profiles`) |
| `spec.profileCopy.selector` | Select source profiles to copy | `spec.profiles.derivedFrom.selector` |
| `spec.profileCopy.overrides` | Overrides applied to copies | `spec.profiles.overrides` |
| `spec.profileCopy.version` | Version pin | `spec.profiles.version` (`versionPolicy: pinned`) |
| `spec.discovery.disabled` | Disable image discovery | Replaced by setting `spec.profiles` instead |
| `spec.defaultServiceTemplate` | Default template name | Profile `spec.primary: true` |
| `spec.runtimeConfigName` | Default runtime config | `AIMService.spec.runtimeConfigRef.name` |
| `spec.env` | Default env vars | Profile `spec.containerEnv` / `spec.engineEnv` |
| `spec.imageMetadata` | Internal hints | Removed — internal |

## Custom-templates pattern (v1alpha1)

The v1alpha1 way to deploy your own model:

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
    - name: acme-transformer-mi300x-fp8
      acceleratorModel: MI300X
      acceleratorType: gpu
      acceleratorCount: 1
      precision: fp8
      metric: latency
      engine: vllm
```

The v1alpha2 replacement uses a base-image AIMModel + custom-model derivation:

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

The base image's profile YAMLs replace the hand-authored `customTemplates` entries. See [Custom Models](../guides/custom-models.md) for the full walkthrough.

## Profile-copy pattern (v1alpha1)

The v1alpha1 way to fine-tune a published model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMModel
metadata:
  name: qwen-finetune
spec:
  profileCopy:
    selector:
      aimId: qwen/qwen3-32b
    version: "0.8.5"
    overrides:
      modelSources:
        - modelId: acme/qwen3-32b-finetune
          sourceUri: hf://acme/qwen3-32b-finetune
```

The v1alpha2 replacement uses `spec.profiles`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: qwen-finetune
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

The selector and overrides shape is unchanged — only the wrapping field name. See [Fine-Tuned Models](../guides/fine-tuned-models.md) for the full walkthrough.

## v1alpha2 admission rules

v1alpha2 explicitly forbids the legacy fields. Admission rejects:

- `spec.aimId` with `spec.profiles` (use `profiles.overrides.aimId`; top-level `aimId` is valid with generated `spec.modelId` onboarding).
- `spec.modelSources` (now on `AIMProfile.spec.modelSources`).
- `spec.custom` and `spec.customTemplates` (replaced by base-image + derivation).
- `spec.profileCopy` (replaced by `derivedFrom`).
- `spec.discovery`, `spec.defaultServiceTemplate`, `spec.runtimeConfigName`, `spec.env`, `spec.imageMetadata`.

Each rejection produces a targeted error message naming the v1alpha2 equivalent.

## Coexistence

Both API versions share one v1alpha2-rooted controller and may be applied to the same cluster simultaneously. Output follows the retained spec shape, not the endpoint used to read or write the object: legacy inputs continue to produce `AIMServiceTemplate` resources, native inputs produce `AIMProfile` resources, and a grandfathered image-backed object may retain both outputs when both paths apply. See [Admin → Upgrading → Migration window](../admin/upgrading.md#migration-window) for readiness and ownership behavior. The bridge does not authorize automatic template cleanup.

## Related documentation

- [Migrating to v1alpha2](migrating.md)
- [AIM Models (v1alpha2)](../concepts/models.md) — Migration target
- [Fine-Tuned Models](../guides/fine-tuned-models.md) — Replacement for `spec.profileCopy`
- [Custom Models](../guides/custom-models.md) — Replacement for `spec.custom`/`customTemplates`
- [Service Templates (v1alpha1)](service-templates.md) — Legacy runtime profiles
