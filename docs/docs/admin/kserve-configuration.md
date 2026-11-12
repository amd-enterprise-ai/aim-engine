# KServe Configuration

AIM Engine requires KServe v0.16.1 or later with specific configuration and is tested against KServe v0.20.0. Use the values file below when installing KServe.

## Required Values

```yaml
# kserve-values.yaml
kserve:
  controller:
    deploymentMode: Standard
    gateway:
      ingressGateway:
        enableGatewayApi: false
  localmodel:
    enabled: false
  inferenceservice:
    resources:
      limits:
        cpu: ""
        memory: ""
      requests:
        cpu: ""
        memory: ""
```

## Installation

```bash
helm install kserve-crd oci://ghcr.io/kserve/charts/kserve-crd \
  --namespace kserve-system \
  --create-namespace \
  --version v0.20.0

helm install kserve oci://ghcr.io/kserve/charts/kserve-resources \
  --namespace kserve-system \
  --version v0.20.0 \
  --values kserve-values.yaml
```

KServe v0.17 renamed the controller chart from `kserve` to `kserve-resources`. The values above apply to both.

## Why These Values Matter

### Resource Limits (Critical)

:::{warning}
Without clearing the default resource limits, AIM Engine deployments will fail.
:::
KServe applies default CPU and memory resources (`cpu: 1`, `memory: 2Gi`) to InferenceService containers when those keys are omitted. AIM Engine sets CPU requests based on GPU count (4 CPUs per GPU) but intentionally does not set CPU limits, allowing inference workloads to burst and fully utilize available CPU for optimal throughput. KServe's default CPU limit of `1` conflicts with larger calculated requests, while its `2Gi` memory limit can OOM-kill model servers whose profile does not override memory.

For GPU workloads, both v1alpha1 templates and v1alpha2 profiles set defaults per GPU (`requests.cpu: 4`, `requests.memory: 32Gi`, `limits.memory: 48Gi`), unless overridden by profile/template/service resources. Clearing KServe defaults avoids accidental request/limit mismatches and hidden caps when resources are omitted or partially overridden.

Setting limits and requests to `""` removes the defaults so AIM Engine controls per-workload CPU/memory behavior.

### Deployment Mode

AIM Engine uses KServe in Standard mode (without Knative) to support KEDA-based autoscaling with OpenTelemetry metrics.

### Local Model Cache

AIM Engine manages model caching independently via `AIMArtifact` and `AIMProfileCache` (v1alpha2) / `AIMTemplateCache` (v1alpha1) resources. KServe's built-in local model feature must be disabled to avoid conflicts.

This is already configured in the values snippet above via `kserve.localmodel.enabled: false` (and in the repository's local dependency Helmfile).

## LLMInferenceService support

AIMServices with `spec.servingAPI: LLMInferenceService` are served through KServe's LLMInferenceService controller, a separate KServe component. Install it into the same namespace as the controller above. The InferenceService release keeps owning the resources both controllers share, so the LLMInferenceService release must not create them:

```bash
helm install kserve-llmisvc-crd oci://ghcr.io/kserve/charts/kserve-llmisvc-crd \
  --namespace kserve-system \
  --version v0.20.0

helm install kserve-llmisvc oci://ghcr.io/kserve/charts/kserve-llmisvc-resources \
  --namespace kserve-system \
  --version v0.20.0 \
  --set kserve.createSharedResources=false

# KServe's built-in LLMInferenceServiceConfig base templates
helm template kserve-llmisvc-configs oci://ghcr.io/kserve/charts/kserve-runtime-configs \
  --version v0.20.0 \
  --set kserve.llmisvcConfigs.enabled=true \
  --set kserve.servingruntime.enabled=false \
  | sed -E 's/^(  namespace: )kserve$/\1kserve-system/' \
  | kubectl apply --server-side -f -
```

The `kserve-runtime-configs` v0.20.0 chart writes these configs to the `kserve` namespace whatever the release namespace is, and KServe only resolves its built-in configs in its own namespace, hence the `sed`. Substitute your KServe namespace if it is not `kserve-system`. If the apply fails with a webhook connection error, the LLMInferenceService controller is still starting; retry after a few seconds.

AIM Engine detects the `serving.kserve.io/v1alpha2` LLMInferenceService and LLMInferenceServiceConfig CRDs once at startup. Restart the operator after installing them. Two chart values control the integration:

| Value | Default | Purpose |
|---|---|---|
| `manager.enableLLMInferenceService` | `true` | Set to `false` to turn the integration off even when the CRDs are installed. |
| `manager.kserveNamespace` | `kserve-system` | Namespace of the KServe control plane. AIMClusterProfiles project their LLMInferenceServiceConfigs here, because KServe resolves `spec.baseRefs` in this namespace when the service namespace has no config of that name. |

Leave LLMInferenceService TLS disabled (KServe's default; `enableLLMInferenceServiceTLS` unset in `inferenceservice-config`). AIM images serve plain HTTP on port 8000, and the probes and workload Service KServe generates switch to HTTPS when TLS is enabled.

KServe merges its built-in `kserve-config-llm-template` under every LLMInferenceService. The configs AIM Engine projects override the parts that conflict with AIM images: the container command (`./entrypoint.py` from `/workspace`), `runAsNonRoot` (AIM images run as root), `HF_HUB_CACHE`, and a one-hour startup probe. They also disable KServe's storage initializer, so AIM Engine loads models exactly as it does for InferenceServices. KServe's template drops all Linux capabilities; this cannot be undone through a config merge.

### Routing and autoscaling

An LLMInferenceService can route and scale itself through KServe, but AIM Engine does not use either mechanism. It routes and scales the workload the same way it does for an InferenceService, so `spec.routing` and `spec.replicas` / autoscaling on the AIMService mean the same thing on both serving APIs.

| Concern | KServe LLMInferenceService | AIM Engine |
|---|---|---|
| Routing | `spec.router`: a KServe-managed `HTTPRoute` on a default or referenced Gateway, optionally fronted by the endpoint picker (`spec.router.scheduler`) and an `InferencePool` for load- and prefix-aware request scheduling | Leaves `spec.router` unset, so KServe creates no route, Gateway, scheduler, or `InferencePool`. AIM Engine owns the `HTTPRoute` and points it at the `<name>-kserve-workload-svc` Service on port 8000. |
| Autoscaling | `spec.scaling`: the Workload Variant Autoscaler (WVA) computes a desired replica count from Prometheus metrics and drives an HPA or KEDA actuator | Leaves `spec.scaling` and, when autoscaling, `spec.replicas` unset, so KServe preserves the Deployment's replica count. AIM Engine owns a KEDA `ScaledObject` on the `<name>-kserve` Deployment and an OpenTelemetry Collector sidecar that pushes vLLM metrics to the KEDA OTel add-on, which also enables scale-to-zero. |

Request scheduling through the endpoint picker is therefore not available to AIMServices today. Prefill/decode disaggregation (`spec.prefill`) and multi-node workers (`spec.worker`) are not used either. AIMServices reject additional LLMInferenceServiceConfigs that set any of these fields (see [Additional LLMInferenceServiceConfigs](../concepts/services.md#additional-llminferenceserviceconfigs)).

## Verifying Configuration

After installation, verify the resource limits are removed:

```bash
kubectl get configmap -n kserve-system inferenceservice-config -o jsonpath='{.data.inferenceService}'
```

The output should show empty or missing `cpuLimit` and `memoryLimit` values.
