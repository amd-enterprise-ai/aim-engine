# Routing and Ingress

AIM Engine uses the Kubernetes [Gateway API](https://gateway-api.sigs.k8s.io/) to expose inference services via HTTP. When routing is enabled, AIM Engine creates `HTTPRoute` resources that route traffic through a configured Gateway to the KServe predictor service.

## Enabling Routing

### Per-Service Routing

Enable routing on an individual service:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  routing:
    enabled: true
    gatewayRef:
      name: inference-gateway
      namespace: kgateway-system
    pathTemplate: "/{.metadata.namespace}/{.metadata.name}"
```

This creates an HTTPRoute matching the path `/ml-team/qwen-chat` and forwarding to the predictor service.

### Cluster-Wide Routing Defaults

Set routing defaults for all services via runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  routing:
    enabled: true
    gatewayRef:
      group: gateway.networking.k8s.io
      kind: Gateway
      name: inference-gateway
      namespace: kgateway-system
    pathTemplate: "/{.metadata.namespace}/{.metadata.name}"
```

With cluster defaults in place, services inherit routing configuration automatically. A service can override any field by specifying its own `spec.routing`.

## Path Templates

Path templates use JSONPath expressions in `{...}` to build route paths from service metadata:

| Template | Example Result |
|----------|---------------|
| `/{.metadata.namespace}/{.metadata.name}` | `/ml-team/qwen-chat` |
| `/models/{.metadata.name}` | `/models/qwen-chat` |
| `/{.metadata.namespace}/{.metadata.labels['team']}/inference` | `/ml-team/nlp/inference` |

Path templates have a maximum length of 200 characters. If no template is specified, the default path is `/{namespace}/{uid}`.

## Request Timeout

Set an HTTP request timeout for the route:

```yaml
spec:
  routing:
    requestTimeout: 120s
```

## Annotations

Add annotations to the generated HTTPRoute:

```yaml
spec:
  routing:
    annotations:
      example.com/rate-limit: "100"
```

Annotations from the runtime config are merged with service-level annotations, with service-level values taking precedence.

## Routing Precedence

Configuration is resolved in this order (highest to lowest priority):

1. `AIMService.spec.routing` — service-level overrides
2. `AIMRuntimeConfig.spec.routing` — namespace defaults
3. `AIMClusterRuntimeConfig.spec.routing` — cluster defaults

## How It Works

AIM Engine creates an HTTPRoute with:

- **Parent**: The gateway from `gatewayRef`
- **Path match**: `PathPrefix` with the resolved path template
- **Backend**: KServe predictor service (`{isvc-name}-predictor`, where `isvc-name` is the derived InferenceService name) on port 80
- **URL rewrite**: The matched prefix is rewritten to `/` so backends receive clean paths like `/v1/chat/completions`

## Next Steps

- [Deploying Services](deploying-services.md) — Full service configuration
- [Runtime Configuration](../concepts/runtime-config.md) — Routing defaults and resolution
