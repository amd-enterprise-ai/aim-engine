# Quickstart

Deploy your first inference service in minutes.

## Prerequisites

- AIM Engine [installed](installation.md) on your cluster
- AMD GPUs available in the cluster
- `kubectl` configured to access your cluster

## Step 1: Check Available Models

If you enabled model discovery during installation, models are already available:

```bash
kubectl get aimclustermodels
```

If no models are listed, create one manually:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModel
metadata:
  name: qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

```bash
kubectl apply -f model.yaml
```

## Step 2: Deploy an Inference Service

Create an `AIMService` to deploy the model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: default
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

```bash
kubectl apply -f service.yaml
```

AIM Engine automatically:

1. Resolves or creates a matching model
2. Selects the best runtime template for your GPU hardware
3. Downloads the model weights (this can take several minutes for large models)
4. Creates a KServe InferenceService once the download completes
5. Starts serving the model

### Caching

Model weights are always downloaded to a persistent volume before the InferenceService starts. The caching mode controls whether that PVC is shared or isolated:

- **`Shared`** (default) — The PVC is shared across all services using the same template. Once one service downloads the model, others reuse it immediately.
- **`Dedicated`** — Each service gets its own PVC, isolated from other services.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: default
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  caching:
    mode: Dedicated
```

See [Model Caching](../guides/model-caching.md) for more on caching modes and configuration.

## Step 3: Monitor Progress

Watch the service status:

```bash
kubectl get aimservice qwen-chat -w
```

The status progresses through: `Pending` → `Starting` → `Running`. The service pauses in `Starting` while model weights are downloaded.

For more detail, check the conditions:

```bash
kubectl get aimservice qwen-chat -o jsonpath='{.status.conditions}' | jq
```

## Step 4: Send a Request

Once the service is `Running`, find the inference endpoint:

```bash
kubectl get inferenceservice -n default -l aim.eai.amd.com/service.name=qwen-chat
```

InferenceService names are derived, so use the name returned by the command above and port-forward its predictor service:

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

## Next Steps

- [Deploying Services](../guides/deploying-services.md) — Scaling, caching, routing, and more configuration options
- [Model Catalog](../guides/model-catalog.md) — Browse and manage available models
- [Architecture](architecture.md) — Understand how AIM Engine components work together
