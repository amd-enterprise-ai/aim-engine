# Conditions Reference

Every AIM resource reports its state through standard Kubernetes conditions. This page catalogs all conditions, their reasons, and what triggers them.

## Reading conditions

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions}' | jq
```

Each condition has:

- **type** — The condition name (e.g., `Ready`)
- **status** — `True`, `False`, or `Unknown`
- **reason** — Machine-readable cause
- **message** — Human-readable description
- **lastTransitionTime** — When the status last changed

## Framework conditions

These conditions are managed by the reconciliation framework and appear on **all** AIM resources.

### DependenciesReachable

Whether upstream dependencies (referenced models, profiles, configs) can be fetched.

| Status | Reason | Description |
|---|---|---|
| `True` | `Reachable` | All dependencies are reachable |
| `False` | `InfrastructureError` | Cannot reach one or more dependencies |

### AuthValid

Whether authentication and authorization for referenced secrets and registries are valid.

| Status | Reason | Description |
|---|---|---|
| `True` | `AuthenticationValid` | Authentication and authorization successful |
| `False` | `AuthError` | Authentication or authorization failure |

### ConfigValid

Whether the resource's spec is valid and all referenced resources exist.

| Status | Reason | Description |
|---|---|---|
| `True` | `ConfigurationValid` | Configuration is valid |
| `False` | `InvalidSpec` | Configuration validation failed |
| `False` | `ReferenceNotFound` | A referenced resource does not exist |

### Ready

Overall readiness — the aggregate of all other conditions and component health.

| Status | Reason | Description |
|---|---|---|
| `True` | `AllComponentsReady` | All components are ready |
| `False` | `ComponentsNotReady` | One or more components are not ready |
| `False` | `Progressing` | Waiting for components to become ready |

## AIMService conditions (v1alpha2)

`AIMService` reports per-component conditions in addition to the framework conditions. The condition catalog covers profile resolution, cache, KServe runtime, routing, and autoscaling.

### ProfileReady

| Status | Reason | Description |
|---|---|---|
| `True` | `ProfileResolved` | Profile found and is deployable |
| `False` | `ProfileNotFound` | No matching `AIMProfile` / `AIMClusterProfile`. For the image shape (`spec.model.image` + reconciler-pipeline annotation), this also covers the transient "auto-creating AIMModel; waiting for discovery" window — the message text disambiguates. |
| `False` | `ProfileNotReady` | Profile exists but its own `Ready` condition is false |
| `False` | `BaseProfile` | `spec.profile.name` resolved to a base profile (not deployable) |
| `False` | `ProfileSelectorAmbiguous` | Selector returned multiple equally-scored candidates |

When `ProfileReady=False`, the controller suppresses downstream component conditions (`InferenceServiceReady`, `HTTPRouteReady`) — the resources aren't being created until profile resolution succeeds.

### ModelReady

Set when `spec.model` is used (by-model, model+selector, or model.image with the v1alpha2 profile-pipeline annotation).

| Status | Reason | Description |
|---|---|---|
| `True` | `ModelResolved` | Model found and ready |
| `False` | `ModelNotFound` | Referenced model does not exist |
| `False` | `ModelNotReady` | Model exists but is not ready |
| `False` | `CreatingModel` | Auto-creating a model from `spec.model.image` (v1alpha1 template pipeline). v1alpha2's image-shape auto-create does not currently set this reason — the transient state is surfaced through `ProfileReady=False / ProfileNotFound` with a message that explains the auto-create. |

### RuntimeConfigReady

| Status | Reason | Description |
|---|---|---|
| `True` | `RuntimeConfigResolved` | Runtime config found (or no runtime config required) |
| `False` | `ReferenceNotFound` | Referenced runtime config does not exist |

### ProfileCacheReady

Replaces v1alpha1's `CacheReady`. Tracks the `AIMProfileCache` lifecycle.

| Status | Reason | Description |
|---|---|---|
| `True` | `CacheReady` | All cache artifacts are downloaded and verified |
| `False` | `CacheCreating` | Creating the profile cache |
| `False` | `CacheNotReady` | Cache exists but download is incomplete |
| `False` | `CacheFailed` | Cache download failed |
| `False` | `CacheLost` | Previously-ready cache is no longer available |

### InferenceServiceReady

| Status | Reason | Description |
|---|---|---|
| `True` | `RuntimeReady` | KServe `InferenceService` is serving |
| `False` | `CreatingRuntime` | Creating or updating the InferenceService |
| `False` | `RuntimeScaling` | Replica scaling in progress |

### InferenceServicePodsReady

Tracks whether the predictor pods are running and ready.

### HTTPRouteReady

Set only when `spec.routing.enabled: true`.

| Status | Reason | Description |
|---|---|---|
| `True` | `HTTPRouteAccepted` | `HTTPRoute` accepted by the Gateway |
| `False` | `HTTPRoutePending` | `HTTPRoute` exists but is still pending acceptance |
| `False` | `PathTemplateInvalid` | `spec.routing.pathTemplate` failed to resolve |
| `False` | `GatewayNotConfigured` | Routing enabled but no `gatewayRef` configured |

### HPAReady

Set only when `spec.minReplicas` / `spec.maxReplicas` are configured.

| Status | Reason | Description |
|---|---|---|
| `True` | `HPAOperational` | HPA is active and metrics are available |
| `False` | `HPANotFound` | Waiting for KEDA to create HPA |
| `False` | `WaitingForMetrics` | InferenceService not ready yet; metrics unavailable |

## AIMModel / AIMClusterModel conditions (v1alpha2)

### DiscoveryReady

Set only for the official and base-image flows (`spec.image` set). Tracks the in-cluster discovery Job.

| Status | Reason | Description |
|---|---|---|
| `True` | `DiscoveryCacheValid` | Discovery cache is populated and the spec hash matches |
| `True` | `DiscoverySucceeded` | Discovery Job completed and produced a valid cache |
| `False` | `DiscoveryStarted` | Discovery Job has been launched |
| `False` | `DiscoveryInProgress` | Discovery Job is running |
| `False` | `LogParseFailed` | Discovery completed but logs could not be parsed |
| `False` | `CacheParseFailed` | Discovery cache `ConfigMap` could not be parsed |
| `False` | `DiscoveryFailed` | Discovery Job failed (image pull failure, registry auth, etc.) |
| `False` | `DiscoveryCountFailed` | Could not count current attempts |
| `False` | `DiscoveryCapReached` | Concurrent discovery cap reached; waiting for slot |

`status.discovery.lastFailureReason` records the most recent failure reason. The message includes the retry backoff window.

### DerivationReady

Set only for the fine-tuned and custom flows (`spec.profiles` set). Mirrors the child `AIMProfileSet`'s `DerivationReady`.

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All derived profiles are ready |
| `False` | `Progressing` | Derivation in progress |
| `False` | `NoMatchingProfiles` | Selector matched zero source candidates |
| `False` | `Degraded` | One or more derived profiles failed |

### Ready (managed profile rollup)

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All managed profiles are ready |
| `True` | `ManagedProfilesPartiallyReady` | Some profiles ready, others `NotAvailable` (typically missing hardware) |
| `False` | `ManagedProfilesNotAvailable` | No managed profile is ready and at least one is `NotAvailable` |
| `False` | `NoSupportedProfiles` | Discovery completed but no supported profiles found |
| `False` | `BuildFailed` | Building the desired profile set failed |
| `False` | `NodeInventoryAvailable` | Node-inventory snapshot built (intermediate state) |

## AIMProfileSet / AIMClusterProfileSet conditions

### DerivationReady

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All managed derived profiles are ready |
| `False` | `Progressing` | Derivation in progress |
| `False` | `NoMatchingProfiles` | Selector matched zero source candidates |
| `False` | `Degraded` | One or more derived profiles failed |

`status.managedProfiles.{total,ready,deployable,base,notAvailable}` are always populated (zero values serialised explicitly).

## AIMProfile / AIMClusterProfile conditions

### HardwareAvailable

Reports whether the cluster has nodes matching the profile's accelerator labels and resource requests.

| Status | Reason | Description |
|---|---|---|
| `True` | `HardwareAvailable` | Matching nodes found in cluster |
| `True` | `NoAcceleratorSpecified` | No accelerator requirements — profile is always available |
| `False` | `HardwareNotAvailable` | No cluster nodes match accelerator labels and resource requests |

The profile controller watches node events and re-evaluates hardware availability whenever node labels change.

## AIMProfileCache conditions

### ArtifactsReady

| Status | Reason | Description |
|---|---|---|
| `True` | `AllCachesReady` | All artifacts downloaded |
| `False` | `Creating` | Creating artifact resources |
| `False` | `CachesNotReady` | Some artifacts not ready |

## AIMArtifact conditions

### Ready

| Status | Reason | Description |
|---|---|---|
| `True` | `Verified` | Download complete and verified |
| `False` | `Downloading` | Download in progress |
| `False` | `Verifying` | Verifying downloaded data |
| `False` | `RetryBackoff` | Waiting before retrying a failed download |
| `False` | `Failed` | Terminal failure |

## v1alpha1 conditions

Legacy v1alpha1 controllers (template-based AIMService, AIMServiceTemplate, AIMTemplateCache) still emit their own condition catalog during the deprecation window. See [Legacy Conditions](../legacy/conditions-v1alpha1.md) for the full reference.

### v1alpha1 AIMService (template path)

When an AIMService uses `spec.template` (v1alpha1) the controller emits `TemplateReady` and `CacheReady` instead of `ProfileReady` and `ProfileCacheReady`:

| Condition | Reasons |
|---|---|
| `TemplateReady` | `Resolved`, `TemplateNotFound`, `TemplateNotReady`, `TemplateSelectionAmbiguous` |
| `CacheReady` | `CacheReady`, `CacheCreating`, `CacheNotReady`, `CacheFailed`, `CacheLost` |

### v1alpha1 AIMModel (template-emitting path)

When an AIMModel still emits `AIMServiceTemplate` resources, its `Ready` rollup uses the v1alpha1 reason catalog (`AllTemplatesReady`, `SomeTemplatesReady`, etc.). See the legacy reference for the full list.

## Condition polarity

All AIM conditions follow positive polarity — `status: True` means healthy. When building dashboards or alerting:

- **Green** — condition `status: True`
- **Yellow** — condition `status: False` with reason containing `Progressing`, `Creating`, `Awaiting`, `Started`
- **Red** — condition `status: False` with reason containing `Failed`, `Error`, `NotFound`, `Invalid`, `NotAvailable`
