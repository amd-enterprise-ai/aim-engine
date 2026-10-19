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
| `False` | `RoutingRequiredForScaleToZero` | `minReplicas: 0` is set but routing is not enabled. See [ScaleToZeroConfig](#scaletozeroconfig). |
| `False` | `ActivationMetricQueryInvalid` | The scale-from-zero activation metric query cannot be resolved. See [ScaleToZeroConfig](#scaletozeroconfig). |
| `False` | `AutoscalingRequiresMetrics` | Autoscaling is configured but no scaling trigger resolves. See [AutoscalingConfig](#autoscalingconfig). |
| `False` | `ReferenceNotFound` | A referenced resource does not exist |

### Ready

Overall readiness — the aggregate of all other conditions and component health.

| Status | Reason | Description |
|---|---|---|
| `True` | `AllComponentsReady` | All components are ready |
| `False` | `ComponentsNotReady` | One or more components are not ready |
| `False` | `Progressing` | Waiting for components to become ready |

## Migration condition

### MigrationRequired

`MigrationRequired=True` with reason `TemplateAPIDeprecated` identifies a live resource that still uses the deprecated template API or produces template outputs. It appears on template-pipeline `AIMService` objects, legacy-shaped `AIMModel` / `AIMClusterModel` objects (including deprecated `profileCopy` and flat `derivedFrom` shapes, plus image models that still emit templates), `AIMServiceTemplate`, `AIMClusterServiceTemplate`, and `AIMTemplateCache`. Follow the [bounded template migration runbook](../admin/template-migration.md) before removing the v1alpha1 template APIs.

This condition is informational and does not participate in the `Ready` rollup. It is removed when an overlapping service uses the profile pipeline or a model uses only native profile inputs and no longer emits templates. `AIMArtifact`, `AIMRuntimeConfig`, `AIMClusterRuntimeConfig`, and `AIMClusterModelSource` do not report it because native profile pipelines still use those APIs or their outputs.

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

### ProfileRuntimeReady

Tracks the namespaced profile `ServingRuntime` and its same-name ConfigMap before AIM Engine creates or updates the InferenceService.

| Status | Reason | Description |
|---|---|---|
| `True` | `ProfileRuntimeReady` | Both projection siblings match the current profile and cache content hash |
| `False` | `ProfileRuntimeMaterializing` | One or both projection siblings do not exist yet |
| `False` | `ProfileRuntimeConverging` | Both siblings exist, but one or both have not converged to the expected content hash |
| `False` | `FetchError` | AIM Engine could not read one of the projection siblings |

`ProfileRuntimeConverging` can be transient because the two resources are updated independently. If it persists, check for unsupported out-of-band changes to generated `aim-*` resources. See [Consistency and rollout guarantees](../guides/bring-your-own-kserve.md#consistency-and-rollout-guarantees).

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

### ActivationMetricAvailable

Reports whether the HPA has read KEDA's zero-to-one activation trigger. AIM Engine first verifies that scale-to-zero routing and the selected gateway provider's activation query are valid. It then checks that the HPA spec contains the expected number of external metrics—one activation trigger plus every resolved user scaling trigger—before resolving the exact `s0-*` metric and requiring the same name with a current value in `HPA.status.currentMetrics`. This prevents a stale user-only HPA from being mistaken for activation evidence during configuration changes. A reported value of zero is valid evidence that the query is readable.

The aggregate HPA `ScalingActive` condition is diagnostic context only because any `s1-*` or later user metric can make it `True`. When `s0-*` is unavailable, the condition message lists any other external metrics that the HPA did report, but those metrics do not affect the activation verdict.

This condition is **informational only** — unlike the `*Ready` conditions it is deliberately excluded from the `Ready` rollup, so it never changes the service's status or its scaling behaviour. AIM Engine reports the signal; acting on it is the cluster administrator's call.

Set only when **all** of the following hold: `spec.minReplicas: 0`, routing is enabled, the provider-neutral activation query resolves, the KEDA-managed HPA is observable, and the predictor is currently running at least one replica. Invalid routing or activation-query configuration is reported by `ScaleToZeroConfig` instead, avoiding duplicate or misleading conditions. The running-replica constraint makes runtime availability unambiguous — KEDA normally retains replicas when a trigger cannot be evaluated. At zero replicas an absent series can be the gateway counter's normal lazy resting state, so the condition is removed rather than reported.

| Status | Reason | Description |
|---|---|---|
| `True` | `MetricAvailable` | The HPA reported the exact `s0-*` zero-to-one metric in `currentMetrics`; the activation query is readable |
| `Unknown` | `AwaitingActivationMetric` | Waiting for the HPA to report that exact metric, within the grace period covering HPA creation and transient collector restarts |
| `False` | `ActivationMetricUnavailable` | The HPA has not reported that exact metric for longer than the grace period; the service may not wake after scaling to zero. Emits a warning event. |

When `ActivationMetricUnavailable` is reported, compare the HPA's `s0-*` spec metric with its `currentMetrics` entries, then verify the selected gateway provider's activation counter and scale-from-zero collector path. For Envoy Gateway, also verify that the Lua `EnvoyExtensionPolicy` is accepted and that the gateway has served at least one request for this service's `HTTPRoute` (the counter is created lazily on first request). See [Optional Envoy Gateway scale-from-zero](../admin/envoy-gateway-scale-from-zero.md) for policy installation and verification.

### ScaleToZeroConfig

Validates that routing is enabled when `spec.minReplicas: 0` is set. The scale-from-zero trigger depends on a gateway metric associated with an `HTTPRoute`; without routing, the service could not wake from zero replicas.

| Status | Reason | Description |
|---|---|---|
| `False` | `RoutingRequiredForScaleToZero` | `minReplicas: 0` with routing disabled. Drives `ConfigValid=False` (`InvalidSpec` category) and emits an `InvalidSpec` event. Fix by enabling routing (`spec.routing.enabled: true` or `runtimeConfig.routing.enabled`) or setting `minReplicas >= 1`. |
| `False` | `ActivationMetricQueryInvalid` | Gateway activation is disabled with `gatewayProvider=none`, the selected custom provider has no `scaleFromZero.activationMetricQueryTemplate`, the template contains an unsupported placeholder, or its resource names cannot be derived. Drives `ConfigValid=False` and prevents creation of a scale-to-zero configuration that cannot wake. |

When valid, this check is silent — no condition or component-health entry is emitted.

### AutoscalingConfig

Validates that whenever autoscaling is configured (`spec.minReplicas`/`spec.maxReplicas`/`spec.autoScaling`) at least one KEDA scaling trigger resolves. The controller stamps `autoscalerClass=external` on the InferenceService and owns the KEDA `ScaledObject` directly, but that `ScaledObject` is only created when a trigger exists — the scale-from-zero activation trigger (`minReplicas: 0`) or a user-defined `spec.autoScaling.metrics` entry. Configuring autoscaling with neither leaves the predictor under external scaling control with nothing to drive it, so the declared replica bounds are never enforced (e.g. `maxReplicas` is set but the deployment never scales). AIM Engine rejects the spec instead of creating it.

| Status | Reason | Description |
|---|---|---|
| `False` | `AutoscalingRequiresMetrics` | Autoscaling configured but no trigger resolves. Drives `ConfigValid=False` (`InvalidSpec` category) and emits an `InvalidSpec` event. Fix by adding a metric (`spec.autoScaling.metrics`), setting `minReplicas: 0` for scale-from-zero, or using `spec.replicas` for a fixed replica count. |

When valid, this check is silent — no condition or component-health entry is emitted.

## AIMModel / AIMClusterModel conditions (v1alpha2)

### DiscoveryReady

Set for image-backed models (`spec.image` set), including models with inline `spec.profiles`. Tracks the in-cluster discovery Job.

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

Set when `spec.profiles` is configured, including image-backed inline overrides. Mirrors the child `AIMProfileSet`'s `DerivationReady`.

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All derived profiles are ready |
| `False` | `Progressing` | Derivation in progress |
| `False` | `NoMatchingProfiles` | Selector matched zero source candidates |
| `False` | `NoEligibleProfiles` | Profiles matched the selector and version policy, but none were published as copyable |
| `False` | `Degraded` | One or more derived profiles failed |

### Ready (managed profile rollup)

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All managed profiles are ready |
| `True` | `ManagedProfilesPartiallyReady` | Some profiles ready, others `NotAvailable` (typically missing hardware) |
| `False` | `ManagedProfilesNotAvailable` | No managed profile is ready and at least one is `NotAvailable` |
| `False` | `NoSupportedProfiles` | Discovery completed but no supported profiles found |
| `False` | `NoCompatibleRuntime` | A model-ID declaration has no configured fallback matching available hardware; the message identifies rejected fallbacks and node-level causes such as missing labels or insufficient allocatable resources |
| `False` | `HardwareNotAvailable` | Generated profiles exist, but their matching hardware is no longer available |
| `False` | `BuildFailed` | Building the desired profile set failed |
| `False` | `NodeInventoryAvailable` | Node-inventory snapshot built (intermediate state) |

With inline `spec.profiles`, readiness follows the selected transformed outputs. `discoveredProfiles` continues to report raw catalog hardware support; source hardware availability is independent of derivation eligibility. A valid current catalog is available to selectors even when the source model's overall `Ready` condition is false.

## AIMProfileSet / AIMClusterProfileSet conditions

### DerivationReady

| Status | Reason | Description |
|---|---|---|
| `True` | `ManagedProfilesReady` | All managed derived profiles are ready |
| `False` | `Progressing` | Derivation in progress |
| `False` | `NoMatchingProfiles` | Selector matched zero source candidates |
| `False` | `NoEligibleProfiles` | Profiles matched the selector and version policy, but none were published as copyable |
| `False` | `Degraded` | One or more derived profiles failed |

`status.managedProfiles.{total,ready,deployable,base,notAvailable}` are always populated (zero values serialised explicitly).

`NoMatchingProfiles` describes a completed source evaluation with no selector/version matches. `NoEligibleProfiles` means matches exist but copyability policy excluded all of them. Pending discovery and source-read or parsing failures are reported separately. Outputs for an unresolved source remain in place while healthy sources continue to reconcile.

## AIMProfile / AIMClusterProfile conditions

### HardwareAvailable

Reports whether the cluster has nodes matching the profile's accelerator labels and resource requests.

| Status | Reason | Description |
|---|---|---|
| `True` | `HardwareAvailable` | Matching nodes found in cluster |
| `True` | `NoAcceleratorSpecified` | No accelerator requirements — profile is always available |
| `False` | `HardwareNotAvailable` | No cluster nodes match accelerator labels and resource requests |

The profile controller watches node events and re-evaluates hardware availability whenever node labels change.

### RuntimeProjected

Reports whether the profile is currently projecting a native KServe runtime (`ServingRuntime` / `ClusterServingRuntime`). This is informational — it does **not** gate the profile's aggregate `Ready`. See [Bring Your Own KServe](../guides/bring-your-own-kserve.md) for the consumer-facing workflow.

| Status | Reason | Description |
|---|---|---|
| `True` | `RuntimeProjected` | A runtime is projected for this profile |
| `False` | `RuntimeDegraded` | The projection gate is no longer satisfied (e.g. matching nodes drained, or the profile is no longer deployable), but a previously-projected runtime is **kept, not deleted** |
| `False` | `RuntimeProjectionFailed` | AIM Engine could not construct a valid projected runtime from the profile |

The condition is silent (absent) when the profile has never projected a runtime. Transient node-list or shared-cache lookup failures preserve its prior value; those outages are reported through framework dependency health instead of being mislabeled as runtime build failures.

:::{admonition} Degraded without garbage collection
:class: note

Projection is additive with **asymmetric teardown**: a runtime is created only while the profile is projectable, but is never deleted merely because the gate later flips — only when the profile itself is deleted (owner-reference GC). So a runtime object can persist while its profile reports `RuntimeProjected=False / RuntimeDegraded`. This deliberately avoids yanking a runtime out from under a native `InferenceService` that references it. The `aim.eai.amd.com/runtime-projection-state` label on the runtime object marks provenance (`projected`) and does not currently flip to `degraded`, so this condition on the profile is the authoritative degraded signal; the runtime's ownerRef / `aim.eai.amd.com/projected.profile` annotation is the way back to it.
:::

### RuntimeProjectionReady

Appears only while runtime construction or its shared-cache lookup is failing, and gates aggregate `Ready`. It is removed after recovery.

| Status | Reason | Description |
|---|---|---|
| `False` | `RuntimeProjectionFailed` | The profile could not be converted into a valid projected runtime |
| `False` | `ProfileCacheLookupFailed` | Shared-cache readiness is temporarily unknown; AIM Engine leaves the last-known runtime untouched and retries |

### RuntimeProjectionElectionReady

Appears only when AIM Engine cannot list the profiles needed to elect a model-slug runtime winner, and gates aggregate `Ready`. The reason reflects the categorized dependency error. It is removed after election succeeds.

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

Readiness and health conditions follow positive polarity — `status: True` means healthy. `MigrationRequired` is the explicit non-health exception: `True` means administrator action is still required, and it never gates readiness. When building dashboards or alerting:

- **Green** — condition `status: True`
- **Yellow** — condition `status: False` with reason containing `Progressing`, `Creating`, `Awaiting`, `Started`
- **Red** — condition `status: False` with reason containing `Failed`, `Error`, `NotFound`, `Invalid`, `NotAvailable`
