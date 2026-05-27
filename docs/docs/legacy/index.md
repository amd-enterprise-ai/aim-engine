# Legacy v1alpha1 API

`aim.eai.amd.com/v1alpha1` is the original AIM Engine API. It remains supported during the v1alpha2 deprecation window, but new deployments should use `v1alpha2`.

This section preserves the v1alpha1 reference material:

- [Migrating to v1alpha2](migrating.md) — Field-by-field mapping and conversion recipes
- [AIMService (v1alpha1)](aimservice-v1alpha1.md) — The template-based service shape
- [AIMModel (v1alpha1)](aimmodel-v1alpha1.md) — The `custom` / `customTemplates` model shape
- [Service Templates](service-templates.md) — Runtime configuration via `AIMServiceTemplate`
- [Conditions (v1alpha1)](conditions-v1alpha1.md) — Template-based condition catalog

## What changed

v1alpha2 reorganises model onboarding around three flows and introduces self-contained `AIMProfile` resources. The summary:

| v1alpha1 | v1alpha2 | Notes |
|---|---|---|
| `AIMService.spec.template` | `AIMService.spec.profile` | Profiles replace templates |
| `AIMService.spec.overrides` | `AIMService.spec.profileOverrides` | Override shape changed |
| `AIMModel.spec.custom` | `AIMModel.spec.image` (base) + `AIMModel.spec.profiles` | Two-step custom-model flow |
| `AIMModel.spec.customTemplates` | `AIMProfileSet` | Standalone derivation resource |
| `AIMModel.spec.profileCopy` | `AIMModel.spec.profiles` | Profile-copy semantics now first-class |
| `AIMModel.spec.modelSources` | `AIMProfile.spec.modelSources` (per profile) | Sources live on the profile |
| `AIMServiceTemplate` (discovery cache) | `AIMModel.status.discoveryCacheRef` | Discovery cache moved to model |
| `AIMServiceTemplate` (runtime config) | `AIMProfile` | Profiles are self-contained |
| `AIMTemplateCache` | `AIMProfileCache` | Caches now key off profiles |

## Coexistence

Both API versions live in the same cluster and may be applied simultaneously. The operator dispatches each `AIMService` to either the v1alpha1 template pipeline or the v1alpha2 profile pipeline based on its spec shape and the optional `aim.eai.amd.com/reconciler-pipeline` annotation.

See [Admin → Upgrading → Migration window](../admin/upgrading.md#migration-window) for the canonical reference (spec-shape dispatch table, annotation values, recommended migration order, and when the annotation becomes unnecessary).

## Deprecation timeline

v1alpha1 will be removed in a future major release. The deprecation window is calibrated to give existing deployments time to migrate without disruption — track the [Changelog](../changelog.md) for the exact removal release.

Before removal, the project will:

1. Publish a per-resource migration tool (planned).
2. Announce the removal release in advance.
3. Continue emitting deprecation warnings on every v1alpha1 spec field.

## Where to migrate first

If you're starting a v1alpha1 → v1alpha2 migration:

1. Read [Migrating to v1alpha2](migrating.md) for the field-by-field mapping.
2. Migrate cluster admin resources first — `AIMClusterServiceTemplate` → `AIMClusterProfile` (or, more commonly, rebuild from `AIMClusterModel` discovery).
3. Migrate namespace `AIMServiceTemplate` → `AIMProfile` next.
4. Switch `AIMService.spec.template` → `AIMService.spec.profile` last.
5. Verify nothing references v1alpha1 templates, then delete them.

Profile derivation (`AIMProfileSet`, `AIMModel.spec.profiles`) is new in v1alpha2 — there's no v1alpha1 equivalent to migrate. New custom-model and fine-tune workflows use these resources directly.
