# Deploying Inference Services

`AIMService` is the primary resource for deploying inference endpoints. This guide covers the common patterns — picking a profile, configuring scaling and routing, and verifying the deployment.

:::{admonition} v1alpha2
:class: note

All examples on this page use `aim.eai.amd.com/v1alpha2`. For the deprecated v1alpha1 shape (`spec.template`), see [Legacy AIMService](../legacy/aimservice-v1alpha1.md).
:::
## Quick start

The shortest path: name a model, let the controller pick a profile.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
spec:
  model:
    name: qwen-qwen3-32b
```

This works when an `AIMModel` (or `AIMClusterModel`) named `qwen-qwen3-32b` exists and has produced at least one deployable `AIMProfile`. The controller treats `spec.model.name` as a shortcut for "every deployable profile produced by this model", then ranks the candidates (see [Ranking](#ranking)) and resolves to a single profile.

If you haven't applied a model yet, see [Model Catalog](model-catalog.md) for browsing and creating models.

## Resolution shapes

v1alpha2 supports five ways to reach an `AIMProfile`. Pick the shape that matches what you know.

| Shape | Spec | Use when |
|---|---|---|
| [By name](#by-name) | `spec.profile.name` | You know the exact profile to deploy |
| [By model name](#by-model) | `spec.model.name` | You know the model; let the controller pick |
| [Model + selector](#model--selector) | `spec.model.name` + `spec.profile.selector` | Narrow a model's pool by hardware or precision |
| [Global selector](#global-selector) | `spec.profile.selector` | Reach profiles via labels alone |
| [By image](#by-image) | `spec.model.image` + annotation | One-shot deploy from just an AIM container image |

See [Services concept](../concepts/services.md#resolution-shapes) for the full mechanics.

### Ranking

Every shape except **By name** produces a candidate pool that the controller filters and ranks. The pipeline:

1. **Selector filter** — keep only candidates that match every field set on `spec.profile.selector` (`aimId`, `modelRef`, `precision`, `metric`, `acceleratorModel`, `acceleratorCount`, `acceleratorType`, `engine`, `engineArgs`). The selector is an AND — every field provided must match the candidate exactly.
2. **Hardware filter** — drop candidates whose `acceleratorModel` is not present in the cluster (via the AcceleratorDetector node labels).
3. **Deployable filter** — drop profiles with `status.deployable=false` (base profiles).
4. **Scope preference** — namespace `AIMProfile` outranks cluster `AIMClusterProfile` when both match.
5. **Rank** — order the survivors by `primary > type > version > name`, where:
    - `primary: true` beats `primary: false`.
    - `type` ranks `optimized > general > preview > unoptimized`.
    - `version` is the highest semver from the profile's container-image tag.
    - Alphabetical name is the final, deterministic tiebreaker.

A single winner is selected. If two profiles tie on every ranking axis, the alphabetical name tiebreak makes the choice deterministic. The fields a user typically narrows on (`metric`, `precision`, `acceleratorModel`, `acceleratorCount`) are **filters**, not ranking axes — the way to influence the choice is to constrain the selector until exactly the desired profile survives, then let ranking pick among any remaining ties.

The v1alpha1 template selector performs the same filtering (availability → unoptimized → service overrides → GPU availability → namespace-over-cluster) and applies an analogous tier-based preference. The v1alpha2 ranking just centralises it on profile primary/type/version metadata.

### By name

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
spec:
  profile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
```

Resolution checks namespace-scoped `AIMProfile` first, then cluster-scoped `AIMClusterProfile`.

### By model

```yaml
spec:
  model:
    name: qwen-qwen3-32b
```

The candidate pool is every deployable profile produced by `AIMModel/qwen-qwen3-32b` (or `AIMClusterModel` of the same name). Ranking picks the best.

### Model + selector

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  profile:
    selector:
      precision: fp8
      acceleratorModel: MI300X
      metric: latency
```

Use this when a model ships multiple profiles and you want to pin precision, accelerator, or metric.

### Global selector

```yaml
spec:
  profile:
    selector:
      aimId: qwen/qwen3-32b
      precision: fp8
      acceleratorModel: MI300X
```

At least one of `aimId` or `modelRef` is required so the controller can fan out watches efficiently. Note `selector.role` is reserved — see the [services concept page](../concepts/services.md#reserved-selector-fields).

### By image

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

The shortest path when you only have a container image. With the annotation, the v1alpha2 profile pipeline reuses an existing `AIMModel`/`AIMClusterModel` for that image if one is present, or creates a dedicated, service-owned `AIMModel` if not. Discovery runs against the model and the service then resolves to one of its profiles.

If you plan to share the image across many services, apply a long-lived `AIMModel`/`AIMClusterModel` once and reference it via `spec.model.name` instead — the same profiles back every service and the cache works in `Shared` mode without extra owner chains.

:::{admonition} Migration window
:class: warning

Without `aim.eai.amd.com/reconciler-pipeline: profile`, `spec.model.image` is reconciled by the **legacy v1alpha1 template pipeline**. The annotation will be removed (and the profile pipeline made the default) when v1alpha1 is dropped. See [Migration window](../admin/upgrading.md#migration-window) for the full mechanics, including the auto-created `AIMModel` naming convention, labels, and GC behaviour.
:::
## Profile overlays

To tweak a published profile for one service — typically to point it at fine-tune weights or override engine args — use `spec.profileOverrides`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-finetune
  namespace: ml-team
spec:
  profile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
  profileOverrides:
    modelSources:
      - modelId: acme/qwen3-32b-finetune
        sourceUri: hf://acme/qwen3-32b-finetune
    engineEnv:
      VLLM_LOG_LEVEL: DEBUG
```

The controller materialises a service-owned overlay `AIMProfile` named `<seed>-<service>-overlay-<hash>` with the override applied, then resolves the service to the overlay. The overlay is garbage-collected with the service.

Which container image the overlay runs is tied to whether you are replacing the model weights. If you set `modelSources` (for example to use fine-tuned weights), the controller does not keep the seed profile's model-optimized image — that image is built for the vendor's weights. Instead it uses the `aim-base` runtime for that model line so your new weights land on the standard base image. If you do not override weights and only change things like `acceleratorPartitioningMode`, `containerEnv` / `engineEnv`, or `engineArgs`, the overlay keeps the seed's model-optimized image, so changing partitioning or environment settings cannot quietly switch the service to the generic base runtime. See [Image resolution](../concepts/profilesets.md#image-resolution) for the full table.

`spec.profileOverrides` lives at the top level of the AIMService spec, **not under `spec.profile`**, because it is a separate concern: `spec.profile` describes *which* profile to resolve, `spec.profileOverrides` describes *how to mutate* the resolved profile into a service-owned overlay. The CRD only allows `profileOverrides` together with `spec.profile.name` (it can't combine with a selector — the overlay needs a single, named seed), and keeping it at the top level mirrors the v1alpha1 `spec.overrides` shape that did the same job for templates.

When you have many services that share the same override shape, build a fine-tune `AIMModel` instead — see [Fine-Tuned Models](fine-tuned-models.md).

## Scaling

### Fixed replicas

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  replicas: 3
```

### Autoscaling

When autoscaling is configured, the controller marks the InferenceService with `autoscalerClass=external` (so KServe writes no autoscaler of its own) and creates a controller-owned KEDA `ScaledObject` that targets the predictor Deployment. That `ScaledObject` is the sole authority on scaling.

The `ScaledObject` is only created when at least one trigger resolves: a scale-from-zero activation trigger (when `minReplicas: 0`) and/or the custom metrics below. Configuring autoscaling (`minReplicas`/`maxReplicas`/`autoScaling`) with `minReplicas >= 1` and no metrics yields no trigger, so the declared bounds could never be enforced — AIM Engine rejects this as `ConfigValid=False` (reason `AutoscalingRequiresMetrics`). Add a metric, set `minReplicas: 0` for scale-from-zero, or use `spec.replicas` for a fixed replica count.

For custom metrics (e.g. vLLM OpenTelemetry counters):

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  minReplicas: 1
  maxReplicas: 3
  autoScaling:
    metrics:
      - type: PodMetric
        podmetric:
          metric:
            backend: opentelemetry
            metricNames:
              - vllm:num_requests_running
            query: "vllm:num_requests_running"
            operationOverTime: avg
          target:
            type: Value
            value: "1"
```

See [Scaling and Autoscaling](scaling-and-autoscaling.md) for the full metric reference and monitoring commands.

## Resource overrides

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  resources:
    requests:
      cpu: "8"
      memory: 64Gi
    limits:
      cpu: "16"
      memory: 128Gi
```

Service-level resources are merged on top of the resolved profile's `status.resources`. Service values win where both set the same key.

## Image pull secrets

For private registries:

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  imagePullSecrets:
    - name: registry-credentials
```

Merged with the resolved profile's `spec.imagePullSecrets` and `serviceAccountName`-derived secrets.

## HTTP routing

Expose the service through Gateway API:

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: gateways
```

### Custom paths

```yaml
spec:
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: gateways
    pathTemplate: "/{.metadata.namespace}/chat/{.metadata.name}"
```

`pathTemplate` accepts JSONPath expressions wrapped in `{...}` referencing service metadata. The rendered path is lowercased, URL-encoded, and capped at 200 characters. If a referenced field doesn't exist, the service goes `Degraded` — make sure every JSONPath reference resolves.

See [Routing and Ingress](routing-and-ingress.md) for the full Gateway API integration story.

## Authentication

For models behind gated registries (HuggingFace tokens, private S3, etc.), credentials live on the profile's `modelSources[].env` and are applied during cache pre-warm. See [Private Registries](private-registries.md).

## Caching

Caching is on by default in `Shared` mode (one cache PVC per profile, reused across services). Switch to `Dedicated` for per-service isolation:

```yaml
spec:
  model:
    name: qwen-qwen3-32b
  caching:
    mode: Dedicated
```

See [Model Caching](model-caching.md) for the full cache lifecycle, including the protocol-switching downloader for HuggingFace.

## Monitoring service status

```bash
kubectl -n ml-team get aimservice qwen-chat
# NAME        STATUS    MODEL              PROFILE                                AGE
# qwen-chat   Running   qwen-qwen3-32b     qwen-qwen3-32b-mi300x-fp8-latency      2m

kubectl -n ml-team describe aimservice qwen-chat
```

### Status values

| Status | Meaning |
|---|---|
| `Pending` | Resolving the profile or waiting on dependencies |
| `Starting` | Profile resolved; creating cache and InferenceService |
| `Running` | InferenceService is ready and serving traffic |
| `Degraded` | Partially functional (e.g. routing failed, cache degraded) |
| `Failed` | Terminal error |

### Status fields

| Field | Description |
|---|---|
| `resolvedProfile` | The profile the service is using (`name`, `scope`, `kind`, `uid`) |
| `resolvedRuntimeConfig` | The runtime config (if any) that contributed defaults |
| `cache` | Cache status — `profileCacheRef` (v1alpha2) or `templateCacheRef` (v1alpha1), `retryAttempts` |
| `runtime` | Replica counts when autoscaling is active |
| `routing` | Observed routing including the rendered path |
| `conditions` | Per-component conditions |

### Conditions

The key v1alpha2 conditions:

- `ProfileReady` — profile resolution (`ProfileResolved`, `ProfileNotFound`, `BaseProfile`)
- `ProfileCacheReady` — `AIMProfileCache` artifact downloads
- `InferenceServiceReady` — KServe runtime
- `HTTPRouteReady` — Gateway API route acceptance (only when routing is enabled)
- `HPAReady` — KEDA autoscaler health (only when autoscaling is enabled)
- `Ready` — aggregate of all above

When `ProfileReady=False`, the controller suppresses the downstream `InferenceServiceReady` / `HTTPRouteReady` entries — those resources aren't being created until the profile resolves.

### Example status

```yaml
status:
  status: Running
  resolvedProfile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
    scope: Cluster
    kind: aim.eai.amd.com/v1alpha2/AIMClusterProfile
    uid: 6d8f...
  resolvedModel:
    name: qwen-qwen3-32b
    scope: Cluster
    kind: aim.eai.amd.com/v1alpha2/AIMClusterModel
    uid: 4b2a...
  routing:
    path: /ml-team/qwen-chat
  conditions:
    - type: ProfileReady
      status: "True"
      reason: ProfileResolved
    - type: ProfileCacheReady
      status: "True"
      reason: CacheReady
    - type: InferenceServiceReady
      status: "True"
      reason: RuntimeReady
    - type: HTTPRouteReady
      status: "True"
      reason: HTTPRouteAccepted
    - type: Ready
      status: "True"
      reason: AllComponentsReady
```

## Complete example

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  labels:
    team: research
spec:
  model:
    name: qwen-qwen3-32b
  profile:
    selector:
      precision: fp8
      acceleratorModel: MI300X
      metric: latency
  minReplicas: 1
  maxReplicas: 3
  autoScaling:
    metrics:
      - type: PodMetric
        podmetric:
          metric:
            backend: opentelemetry
            metricNames:
              - vllm:num_requests_running
            query: "vllm:num_requests_running"
            operationOverTime: avg
          target:
            type: Value
            value: "1"
  resources:
    limits:
      cpu: "16"
      memory: 128Gi
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: gateways
    pathTemplate: "/team/{.metadata.labels['team']}/chat"
  caching:
    mode: Shared
```


## Troubleshooting

### Service stuck in `Pending` / `ProfileNotFound`

The resolver found no candidates.

```bash
kubectl get aimservice <name> -o jsonpath='{.status.conditions[?(@.type=="ProfileReady")].message}'
```

Common causes:

- `spec.profile.name` typo (or wrong scope — namespace vs cluster).
- `spec.model.name` doesn't match any AIMModel that has produced deployable profiles. Check `kubectl get aimmodel <name> -o jsonpath='{.status.managedProfiles}'`.
- Selector is too restrictive — relax `precision` / `acceleratorModel` / `acceleratorCount`.

### `BaseProfile`

`spec.profile.name` points at a base profile (`status.deployable: false`). Reference a deployable profile instead, or run a custom-model derivation to produce one — see [Custom Models](custom-models.md).

### Service stuck in `Starting`

Either the cache is still warming up, or KServe is still creating the InferenceService.

```bash
# Cache progress
kubectl get aimprofilecache -l aim.eai.amd.com/service.name=<service-name>
kubectl get aimartifact -l aim.eai.amd.com/service.name=<service-name>

# KServe InferenceService
kubectl get inferenceservice -l aim.eai.amd.com/service.name=<service-name>
kubectl get pods -l serving.kserve.io/inferenceservice=<isvc-name>
```

Image pull errors, GPU/memory unavailability, and PVC binding failures all show up at the pod level.

### Routing not working

```bash
kubectl get httproute -l aim.eai.amd.com/service.name=<service-name>
kubectl get aimservice <name> -o jsonpath='{.status.conditions[?(@.type=="HTTPRouteReady")]}'
```

`PathTemplateInvalid` means a JSONPath expression in `pathTemplate` referenced a field that doesn't exist on the service.

### Wrong profile selected

Check `status.resolvedProfile.name`. If it's not the profile you expected, narrow `spec.profile.selector` to disambiguate — or pin with `spec.profile.name`.

## Next steps

- [Scaling and Autoscaling](scaling-and-autoscaling.md) — KEDA, OpenTelemetry metrics, monitoring
- [Routing and Ingress](routing-and-ingress.md) — Gateway API integration
- [Model Caching](model-caching.md) — Cache lifecycle and download protocols
- [Private Registries](private-registries.md) — Authentication for HF / S3 / OCI
- [Multi-Tenancy](multi-tenancy.md) — Namespace isolation patterns
- [Fine-Tuned Models](fine-tuned-models.md) — Derive profiles for a fine-tune of a published model
- [Custom Models](custom-models.md) — Derive profiles for your own model architecture
