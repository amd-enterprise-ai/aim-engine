# Legacy v1alpha1 API

`aim.eai.amd.com/v1alpha1` is the original AIM Engine API. It remains supported during the v1alpha2 deprecation window, but new deployments should use `v1alpha2`.

This section preserves the v1alpha1 reference material:

- [Migrating to v1alpha2](migrating.md) — Field-by-field mapping and conversion recipes
- [AIMService (v1alpha1)](aimservice-v1alpha1.md) — The template-based service shape
- [AIMModel (v1alpha1)](aimmodel-v1alpha1.md) — The `custom` / `customTemplates` model shape
- [Service Templates](service-templates.md) — Runtime configuration via `AIMServiceTemplate`
- [Conditions (v1alpha1)](conditions-v1alpha1.md) — Template-based condition catalog

## What changed

v1alpha2 reorganises model onboarding around image discovery, model-ID generation, and profile derivation flows, and introduces self-contained `AIMProfile` resources. The summary:

| v1alpha1 | v1alpha2 | Notes |
|---|---|---|
| `AIMService.spec.template` | `AIMService.spec.profile` | Profiles replace templates |
| `AIMService.spec.overrides` | `AIMService.spec.profileOverrides` | Override shape changed |
| `AIMModel.spec.custom` | `AIMModel.spec.image` (base) + `AIMModel.spec.profiles` | Inline on one model, or derived from a shared base-image model |
| `AIMModel.spec.customTemplates` | `AIMProfileSet` | Standalone derivation resource |
| `AIMModel.spec.profileCopy` | `AIMModel.spec.profiles` | Profile-copy semantics now first-class |
| `AIMModel.spec.modelSources` | `AIMProfile.spec.modelSources` (per profile) | Sources live on the profile |
| `AIMServiceTemplate` (discovery cache) | `AIMModel.status.discoveryCacheRef` | Discovery cache moved to model |
| `AIMServiceTemplate` (runtime config) | `AIMProfile` | Profiles are self-contained |
| `AIMTemplateCache` | `AIMProfileCache` | Caches now key off profiles |

## Coexistence

Both API versions live in the same cluster and may be applied simultaneously. The operator dispatches each `AIMService` to either the v1alpha1 template pipeline or the v1alpha2 profile pipeline based on its spec shape and the optional `aim.eai.amd.com/reconciler-pipeline` annotation.

See [Admin → Upgrading → Migration window](../admin/upgrading.md#migration-window) for the canonical bridge installation, served/storage, dispatch, and recovery contract.

## Migration and removal boundary

The bridge does not choose or authorize a v1alpha1 removal release. Deprecation warnings identify the older endpoint; they are not evidence that a cluster is ready for removal.

Migration execution belongs to the bounded administrator-run Plan/Apply design in [ADR-0002](https://github.com/amd-enterprise-ai/aim-engine/blob/main/ADR-0002-bounded-plan-apply-template-migration.md). The bridge itself does not inventory, convert, clean up, or qualify workloads.

## Where to migrate first

If you're preparing a v1alpha1 → v1alpha2 migration, install the bridge CRDs before the matching operator and verify existing serving first. Use [Migrating to v1alpha2](migrating.md) for field mapping, but use ADR-0002 for execution and manual-outcome handling; do not infer cleanup authorization from the mapping guide.

Profile derivation (`AIMProfileSet`, `AIMModel.spec.profiles`) is new in v1alpha2 — there's no v1alpha1 equivalent to migrate. New custom-model and fine-tune workflows use these resources directly.
