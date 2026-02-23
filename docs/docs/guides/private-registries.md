# Private Registries

This guide covers configuring authentication for private container registries, HuggingFace Hub, and S3-compatible storage.

## Container Image Pull Secrets

### Per-Service Secrets

Provide image pull secrets directly on the service:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
spec:
  model:
    image: my-registry.example.com/aim-qwen3:latest
  imagePullSecrets:
    - name: my-registry-creds
```

The secret must exist in the same namespace as the service.

### Model Source Secrets

For `AIMClusterModelSource` pulling from private registries, secrets must be in the operator namespace:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: private-models
spec:
  registry: ghcr.io
  imagePullSecrets:
    - name: ghcr-pull-secret
  filters:
    - image: "my-org/aim-*"
```

## HuggingFace Authentication

For models sourced from private HuggingFace repositories (`hf://` URLs), provide a token via environment variables in the runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-credentials
          key: token
```

Create the secret:

```bash
kubectl create secret generic hf-credentials \
  --from-literal=token=hf_your_token_here \
  -n ml-team
```

## S3-Compatible Storage

For model artifacts stored in S3-compatible storage, configure credentials via environment variables:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  env:
    - name: AWS_ACCESS_KEY_ID
      valueFrom:
        secretKeyRef:
          name: s3-credentials
          key: access-key
    - name: AWS_SECRET_ACCESS_KEY
      valueFrom:
        secretKeyRef:
          name: s3-credentials
          key: secret-key
    - name: AWS_ENDPOINT_URL
      value: "https://s3.example.com"
```

## Credential Scope

Environment variables from runtime configurations are merged in this order (highest to lowest priority):

1. `AIMService.spec.env` — per-service
2. `AIMRuntimeConfig.spec.env` — per-namespace
3. `AIMClusterRuntimeConfig.spec.env` — cluster-wide

This allows you to set cluster-wide defaults (e.g., HuggingFace token) and override per namespace or service when needed.

## Troubleshooting

### Image Pull Errors

Check pod events for pull failures:

```bash
kubectl get pods -l serving.kserve.io/inferenceservice=<service-name> -n <namespace>
kubectl describe pod <pod-name> -n <namespace>
```

Common causes:

- Secret doesn't exist in the correct namespace
- Secret has incorrect credentials
- Registry URL is wrong in the model image

### Download Authentication Failures

Check artifact download job logs:

```bash
kubectl get jobs -l aim.eai.amd.com/artifact=<artifact-name> -n <namespace>
kubectl logs job/<job-name> -n <namespace>
```

## Next Steps

- [Multi-Tenancy](multi-tenancy.md) — Per-namespace credential isolation
- [Runtime Configuration](../concepts/runtime-config.md) — Environment variable resolution
