# AIM Engine

AIM (AMD Inference Microservice) Engine is a Kubernetes operator that simplifies the deployment and management of AI inference workloads on AMD GPUs. It provides a declarative, cloud-native approach to running ML models at scale.

## Quick example

Deploy an inference service in two resources:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterModel
metadata:
  name: qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    name: qwen3-32b
```

AIM images (like `amdenterpriseai/aim-qwen-qwen3-32b`) package open-source models optimized for AMD Instinct GPUs. Each image includes the model weights and a serving runtime tuned for specific GPU configurations and precision modes.

The `AIMModel` runs discovery on the image and publishes one `AIMProfile` per supported (GPU, precision, metric) combination. The `AIMService` resolves to the best deployable profile for your hardware, pre-warms the model cache, and creates a KServe `InferenceService`.

!!! note "Why the `reconciler-pipeline: profile` annotation?"
    AIMService dispatch is decided by **spec shape**, not by `apiVersion`. During the v1alpha1 → v1alpha2 migration window, `spec.model.name` and `spec.model.image` default to the legacy template pipeline so existing deployments keep working unchanged. The annotation forces this service onto the v1alpha2 profile pipeline, which resolves `qwen3-32b` to one of the `AIMClusterProfile`s produced by the `AIMClusterModel` above. The annotation becomes unnecessary once v1alpha1 is removed — see [Migration window](admin/upgrading.md#migration-window) for the full dispatch table.

## Three model flows

How you onboard a model depends on its relationship to AMD's published catalog:

| Flow | Use when | Read more |
|---|---|---|
| **Official** | Deploying a published AMD-supported AIM model unmodified | [AIM Models](concepts/models.md#flow-1-official-aim-model) |
| **Fine-tuned** | Deploying a fine-tune of a published architecture | [Fine-Tuned Models](guides/fine-tuned-models.md) |
| **Custom** | Deploying a model whose architecture isn't in the catalog | [Custom Models](guides/custom-models.md) |

## Where to start

<div class="grid cards" markdown>

-   :material-server:{ .lg .middle } **Cluster administrators**

    ---

    Install AIM Engine, configure KServe, manage GPU resources, and set up cluster-wide defaults.

    [:octicons-arrow-right-24: Installation](getting-started/installation.md)

-   :material-code-braces:{ .lg .middle } **Developers & integrators**

    ---

    Deploy inference services, configure scaling, set up routing, integrate with your applications.

    [:octicons-arrow-right-24: Quickstart](getting-started/quickstart.md)

-   :material-brain:{ .lg .middle } **Data scientists**

    ---

    Browse the model catalog, deploy fine-tunes or custom models, tune inference parameters.

    [:octicons-arrow-right-24: Model Catalog](guides/model-catalog.md)

</div>

## Key features

- **Three-flow model onboarding** — official AIM images, fine-tuned models, and custom models that bring their own weights, all expressed through a single `AIMModel` shape.
- **Profile-driven deployment** — `AIMService` resolves to a self-contained `AIMProfile` with everything the runtime needs (image, accelerator, engine config, model sources).
- **Smart selection** — pick a profile by name, by model, by selector, or by model+selector; the controller ranks candidates by `primary > type > version`.
- **Profile overlays** — `spec.profileOverrides` rebases a published profile onto custom weights without forking it.
- **Model caching** — pre-download artifacts to shared PVCs for faster startup; HuggingFace downloader falls back across XET / HF_TRANSFER / HTTP protocols.
- **HTTP routing** — expose services through Gateway API with customizable path templates.
- **Autoscaling** — KEDA integration with OpenTelemetry metrics for demand-based scaling.
- **Multi-tenancy** — namespace-scoped and cluster-scoped resources for flexible team isolation.

## Documentation

### Getting started

- [Installation](getting-started/installation.md) — Prerequisites and Helm chart installation
- [Quickstart](getting-started/quickstart.md) — Deploy your first model in minutes
- [Architecture](getting-started/architecture.md) — Components, CRDs, and reconciliation flow

### Guides

Task-oriented walkthroughs for common workflows:

- [Deploying Services](guides/deploying-services.md) — Resolution shapes, scaling, routing, caching
- [Model Catalog](guides/model-catalog.md) — Browse, apply, and auto-discover models
- [Fine-Tuned Models](guides/fine-tuned-models.md) — Derive deployable profiles from a published AIM model
- [Custom Models](guides/custom-models.md) — Derive deployable profiles from a base image + custom weights
- [Scaling and Autoscaling](guides/scaling-and-autoscaling.md) — Replicas, KEDA, custom metrics
- [Model Caching](guides/model-caching.md) — Cache modes and download protocols
- [Routing and Ingress](guides/routing-and-ingress.md) — Gateway API patterns and path templates
- [Private Registries](guides/private-registries.md) — Authentication for HuggingFace, S3, and OCI
- [Multi-Tenancy](guides/multi-tenancy.md) — Namespace isolation patterns

### Administration

- [Installation Reference](admin/installation.md) — Full install reference with all Helm values
- [KServe Configuration](admin/kserve-configuration.md) — Install and configure KServe
- [GPU Management](admin/gpu-management.md) — GPU allocation, node selectors, topology
- [Storage Configuration](admin/storage-configuration.md) — PVCs, shared storage for caching
- [Upgrading](admin/upgrading.md) — Version migration and CRD upgrades
- [Monitoring](admin/monitoring.md) — Metrics, observability, log formats
- [Troubleshooting](admin/troubleshooting.md) — Common issues and diagnostic steps
- [Security](admin/security.md) — RBAC, network policies, secrets management

### Concepts

- [AIM Services](concepts/services.md) — Resolution shapes, overlays, caching, status
- [AIM Models](concepts/models.md) — The three model flows
- [Profiles](concepts/profiles.md) — Self-contained runtime configurations
- [AIM Profile Sets](concepts/profilesets.md) — Derivation engine
- [Model Sources](concepts/model-sources.md) — Auto-discovery from container registries
- [Runtime Configuration](concepts/runtime-config.md) — Storage defaults, routing, environment resolution
- [Model Caching](concepts/caching.md) — Cache hierarchy, ownership, deletion behavior
- [Accelerator Detection](concepts/accelerator-detection.md) — How AIM Engine sees GPUs and CPUs
- [Resource Lifecycle](concepts/resource-lifecycle.md) — Ownership, finalizers, deletion behavior

### Reference

- [CRD API (v1alpha2)](reference/api/v1alpha2.md) — API specification for Models, Profiles, ProfileSets, Services
- [CRD API (v1alpha1)](reference/api/v1alpha1.md) — Legacy API specification
- [Helm Chart Values](reference/helm-values.md) — All configurable Helm chart values
- [CLI and Operator Flags](reference/cli.md) — Operator binary flags and endpoints
- [Environment Variables](reference/environment-variables.md) — Operator and downloader configuration
- [Naming and Labels](reference/naming-and-labels.md) — Derived naming algorithm and label conventions
- [Conditions](reference/conditions.md) — Full catalog of conditions across all CRDs

### Legacy (v1alpha1)

- [Overview](legacy/index.md) — Deprecation timeline and what changed
- [Migrating to v1alpha2](legacy/migrating.md) — Field-by-field mapping and recipes
- [Service Templates (v1alpha1)](legacy/service-templates.md) — The deprecated runtime-profile shape
- [AIMService (v1alpha1)](legacy/aimservice-v1alpha1.md) — The deprecated template-based service shape
- [AIMModel (v1alpha1)](legacy/aimmodel-v1alpha1.md) — The deprecated custom-model and `customTemplates` shapes
