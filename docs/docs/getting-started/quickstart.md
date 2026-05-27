# Quickstart

Deploy your first inference service in minutes.

## Prerequisites

- AIM Engine [installed](installation.md) on your cluster
- AMD GPUs available in the cluster (or a CPU-only profile for testing)
- `kubectl` configured to access your cluster

## Step 1: Apply a model

Apply an AMD-published AIM model. This is the [official flow](../concepts/models.md#flow-1-official-aim-model) — `spec.image` points at the AIM container image and discovery materialises deployable profiles.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterModel
metadata:
  name: qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

```bash
kubectl apply -f model.yaml
```

If you set up [model discovery](../concepts/model-sources.md) during installation, official models are already in the catalog:

```bash
kubectl get aimclustermodels
```

Wait for discovery to complete and at least one deployable profile to land:

```bash
kubectl get aimclustermodel qwen3-32b -o jsonpath='{.status.managedProfiles}'
# {"deployable":5,"notAvailable":0,"ready":5,"base":0,"total":5}
```

## Step 2: Deploy an AIMService

Reference the model and let the controller pick the best deployable profile for your hardware.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: default
spec:
  model:
    name: qwen3-32b
```

```bash
kubectl apply -f service.yaml
```

AIM Engine then:

1. Resolves the model to a deployable `AIMProfile` (ranking by `primary > type > version`).
2. Pre-warms the cache by creating an `AIMProfileCache` and downloading model artifacts to a PVC.
3. Creates a KServe `InferenceService` mounting the cache and the profile's container image.
4. Optionally creates an `HTTPRoute` if routing is enabled.

## Step 3: Monitor progress

```bash
kubectl get aimservice qwen-chat -w
```

The status progresses through `Pending` → `Starting` → `Running`. The service pauses in `Starting` while the cache fills (this can take several minutes for large models).

For detail:

```bash
kubectl get aimservice qwen-chat -o jsonpath='{.status.conditions}' | jq
```

The key conditions to watch:

- `ProfileReady` → `ProfileResolved`
- `ProfileCacheReady` → `CacheReady`
- `InferenceServiceReady` → `RuntimeReady`
- `Ready` → `AllComponentsReady`

## Step 4: Send a request

The InferenceService name is derived from the service — look it up by label:

```bash
kubectl get inferenceservice -n default -l aim.eai.amd.com/service.name=qwen-chat
```

Port-forward the predictor service:

```bash
kubectl port-forward -n default svc/<isvc-name>-predictor 8080:80
```

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen-chat",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Variations

### Pick a specific profile

Bypass automatic selection by referencing a profile directly:

```yaml
spec:
  profile:
    name: qwen-qwen3-32b-mi300x-fp8-latency
```

### Narrow by precision or accelerator

```yaml
spec:
  model:
    name: qwen3-32b
  profile:
    selector:
      precision: fp8
      acceleratorModel: MI300X
      metric: latency
```

### Dedicated cache

The default `Shared` caching mode reuses the PVC across services. Use `Dedicated` for per-service isolation:

```yaml
spec:
  model:
    name: qwen3-32b
  caching:
    mode: Dedicated
```

See [Model Caching](../guides/model-caching.md) for the full lifecycle.

## Next steps

- [Deploying Services](../guides/deploying-services.md) — All resolution shapes, scaling, routing
- [Model Catalog](../guides/model-catalog.md) — Browse, apply, and auto-discover models
- [Fine-Tuned Models](../guides/fine-tuned-models.md) — Deploy your fine-tune of a published model
- [Custom Models](../guides/custom-models.md) — Deploy a model that isn't in the catalog
- [Architecture](architecture.md) — How AIM Engine components fit together
