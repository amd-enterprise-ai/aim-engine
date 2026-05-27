# Conditions Reference (v1alpha1)

!!! warning "Deprecated"
    These conditions are emitted by v1alpha1 controllers. See [Conditions](../reference/conditions.md) for the v1alpha2 catalog.

This page catalogs the conditions emitted by v1alpha1 controllers — template-based `AIMService`, `AIMServiceTemplate`, `AIMTemplateCache`, and template-emitting `AIMModel`.

The framework-level conditions (`DependenciesReachable`, `AuthValid`, `ConfigValid`, `Ready`) are unchanged from v1alpha2 — see the [main conditions reference](../reference/conditions.md#framework-conditions).

## AIMService conditions (template path)

When an `AIMService` uses `spec.template` (instead of `spec.profile`), the controller emits this condition catalog:

### ModelReady

| Status | Reason | Description |
|---|---|---|
| `True` | `ModelResolved` | Model found and ready |
| `False` | `ModelNotFound` | Referenced model does not exist |
| `False` | `ModelNotReady` | Model exists but is not ready |
| `False` | `CreatingModel` | Auto-creating a model from `spec.model.image` |
| `False` | `InvalidImageReference` | `spec.model.image` is not a valid image URI |

### TemplateReady

| Status | Reason | Description |
|---|---|---|
| `True` | `Resolved` | Template found and ready |
| `False` | `TemplateNotFound` | No matching template found |
| `False` | `TemplateNotReady` | Template exists but is not ready |
| `False` | `TemplateSelectionAmbiguous` | Multiple templates scored equally |

### CacheReady

| Status | Reason | Description |
|---|---|---|
| `True` | `CacheReady` | Model cache is populated |
| `False` | `CacheCreating` | Creating template cache |
| `False` | `CacheNotReady` | Cache exists but download is incomplete |
| `False` | `CacheFailed` | Cache download failed |
| `False` | `CacheLost` | Previously-ready cache is no longer available |

### InferenceServiceReady

| Status | Reason | Description |
|---|---|---|
| `True` | `RuntimeReady` | KServe `InferenceService` is serving |
| `False` | `CreatingRuntime` | Creating or updating InferenceService |
| `False` | `RuntimeScaling` | Replica scaling in progress |

### HTTPRouteReady

Identical to the v1alpha2 catalog — see [HTTPRouteReady](../reference/conditions.md#httprouteready).

### HPAReady

Identical to the v1alpha2 catalog — see [HPAReady](../reference/conditions.md#hpaready).

## AIMServiceTemplate / AIMClusterServiceTemplate conditions

### Discovered

| Status | Reason | Description |
|---|---|---|
| `True` | `DiscoveryComplete` | Profiles successfully discovered |
| `True` | `InlineModelSources` | Template has inline model sources (no discovery needed) |
| `False` | `AwaitingDiscovery` | Discovery job not yet complete |
| `False` | `DiscoveryFailed` | Discovery job failed |

### CacheReady

| Status | Reason | Description |
|---|---|---|
| `True` | `AllCachesReady` | All template caches are ready |
| `False` | `CreatingCaches` | Creating caches |
| `False` | `CachesNotReady` | Some caches are not ready |
| `False` | `NoCaches` | No caches exist |

### ModelFound

Whether the referenced model exists and is accessible.

## AIMModel conditions (template-emitting path)

When an `AIMModel` still emits `AIMServiceTemplate` resources (v1alpha1 path), the `Ready` condition uses the template-based rollup:

| Status | Reason | Description |
|---|---|---|
| `True` | `AllTemplatesReady` | All discovered templates are ready |
| `True` | `SomeTemplatesReady` | At least one template is ready |
| `True` | `NoTemplatesExpected` | Model has no templates (by design) |
| `False` | `SomeTemplatesDegraded` | Some templates are degraded |
| `False` | `TemplatesProgressing` | Templates are still being discovered |
| `False` | `AllTemplatesFailed` | All templates failed |
| `False` | `NoTemplatesAvailable` | No templates available for this model |
| `False` | `AwaitingMetadata` | Waiting for model metadata extraction |
| `False` | `CreatingTemplates` | Creating service templates |
| `False` | `MetadataExtractionFailed` | Failed to extract model metadata |

## AIMTemplateCache conditions

### TemplateFound

Whether the parent template exists and is accessible.

### ArtifactsReady

| Status | Reason | Description |
|---|---|---|
| `True` | `AllCachesReady` | All artifacts downloaded |
| `False` | `CreatingCaches` | Creating artifact resources |
| `False` | `CachesNotReady` | Some artifacts not ready |

## Migration

The v1alpha2 condition catalog reuses many of the same reason strings (e.g. `CacheReady`, `RuntimeReady`, `HTTPRouteAccepted`). The differences:

- `TemplateReady` → `ProfileReady` (`TemplateNotFound` → `ProfileNotFound`, etc.).
- `CacheReady` on AIMService is now `ProfileCacheReady`.
- The model-level template rollup (`AllTemplatesReady`, etc.) becomes the profile rollup (`ManagedProfilesReady`, `ManagedProfilesPartiallyReady`, etc.).

See [Migrating to v1alpha2](migrating.md) for the full migration story.
