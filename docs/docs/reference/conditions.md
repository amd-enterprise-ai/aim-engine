# Conditions Reference

Every AIM resource reports its state through standard Kubernetes conditions. This page catalogs all conditions, their reasons, and what triggers them.

## Reading Conditions

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions}' | jq
```

Each condition has:

- **type** — The condition name (e.g., `Ready`)
- **status** — `True`, `False`, or `Unknown`
- **reason** — Machine-readable cause
- **message** — Human-readable description
- **lastTransitionTime** — When the status last changed

## Framework Conditions

These conditions are managed by the reconciliation framework and appear on **all** AIM resources.

### DependenciesReachable

Whether upstream dependencies (referenced models, templates, configs) can be fetched.

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `Reachable` | All dependencies are reachable |
| `False` | `InfrastructureError` | Cannot reach one or more dependencies |

### AuthValid

Whether authentication and authorization for referenced secrets and registries are valid.

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `AuthenticationValid` | Authentication and authorization successful |
| `False` | `AuthError` | Authentication or authorization failure |

### ConfigValid

Whether the resource's spec is valid and all referenced resources exist.

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `ConfigurationValid` | Configuration is valid |
| `False` | `InvalidSpec` | Configuration validation failed |
| `False` | `ReferenceNotFound` | A referenced resource does not exist |

### Ready

Overall readiness — the aggregate of all other conditions and component health.

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `AllComponentsReady` | All components are ready |
| `False` | `ComponentsNotReady` | One or more components are not ready |
| `False` | `Progressing` | Waiting for components to become ready |

## AIMService Conditions

In addition to the framework conditions, AIMService reports component-specific conditions.

### ModelReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `ModelResolved` | Model found and ready |
| `False` | `ModelNotFound` | Referenced model does not exist |
| `False` | `ModelNotReady` | Model exists but is not ready |
| `False` | `CreatingModel` | Auto-creating a model from image |

### TemplateReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `Resolved` | Template found and ready |
| `False` | `TemplateNotFound` | No matching template found |
| `False` | `TemplateNotReady` | Template exists but is not ready |
| `False` | `TemplateSelectionAmbiguous` | Multiple templates scored equally |

### RuntimeConfigReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `RuntimeConfigResolved` | Runtime config found |
| `False` | `ReferenceNotFound` | Referenced runtime config does not exist |

### CacheReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `CacheReady` | Model cache is populated |
| `False` | `CacheNotReady` | Cache exists but download is incomplete |
| `False` | `CacheFailed` | Cache download failed |
| `False` | `CacheLost` | Previously-ready cache is no longer available |
| `False` | `CacheCreating` | Creating template cache |

### InferenceServiceReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `RuntimeReady` | KServe InferenceService is serving |
| `False` | `CreatingRuntime` | Creating or updating InferenceService |

### InferenceServicePodsReady

Tracks whether the predictor pods are running and ready.

### HTTPRouteReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `HTTPRouteAccepted` | HTTPRoute accepted by the Gateway |
| `False` | `HTTPRoutePending` | HTTPRoute exists but is still pending acceptance |
| `False` | `PathTemplateInvalid` | Path template failed to resolve |
| `False` | `GatewayNotConfigured` | Routing enabled but no `gatewayRef` configured |

### HPAReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `HPAOperational` | HPA is active and metrics are available |
| `False` | `HPANotFound` | Waiting for KEDA to create HPA |
| `False` | `WaitingForMetrics` | InferenceService not ready yet; metrics unavailable |

## AIMModel / AIMClusterModel Conditions

### Ready

| Status | Reason | Description |
|--------|--------|-------------|
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

## AIMServiceTemplate / AIMClusterServiceTemplate Conditions

### Discovered

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `DiscoveryComplete` | Profiles successfully discovered |
| `True` | `InlineModelSources` | Template has inline model sources (no discovery needed) |
| `False` | `AwaitingDiscovery` | Discovery job not yet complete |
| `False` | `DiscoveryFailed` | Discovery job failed |

### CacheReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `AllCachesReady` | All template caches are ready |
| `False` | `CreatingCaches` | Creating caches |
| `False` | `CachesNotReady` | Some caches are not ready |
| `False` | `NoCaches` | No caches exist |

### ModelFound

Whether the referenced model exists and is accessible.

## AIMTemplateCache Conditions

### TemplateFound

Whether the parent template exists and is accessible.

### ArtifactsReady

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `AllCachesReady` | All artifacts downloaded |
| `False` | `CreatingCaches` | Creating artifact resources |
| `False` | `CachesNotReady` | Some artifacts not ready |

## AIMArtifact Conditions

### Ready

| Status | Reason | Description |
|--------|--------|-------------|
| `True` | `Verified` | Download complete and verified |
| `False` | `Downloading` | Download in progress |
| `False` | `Verifying` | Verifying downloaded data |

## Condition Polarity

All conditions follow positive polarity — `status: True` means healthy. When building dashboards or alerting:

- **Green**: condition `status: True`
- **Yellow**: condition `status: False` with reason containing `Progressing`, `Creating`, `Awaiting`
- **Red**: condition `status: False` with reason containing `Failed`, `Error`, `NotFound`, `Invalid`
