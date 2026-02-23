# AIM Engine

AIM (AMD Inference Microservice) Engine is a Kubernetes operator that simplifies the deployment and management of AI inference workloads on AMD GPUs. It provides a declarative, cloud-native approach to running ML models at scale.

## Quick Example

Deploy an inference service with a single resource:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

AIM images (like `amdenterpriseai/aim-qwen-qwen3-32b`) are container images that package open-source models optimized for AMD Instinct GPUs. Each image includes the model weights and a serving runtime tuned for specific GPU configurations and precision modes.

AIM Engine automatically resolves the model, selects an optimal runtime configuration for your hardware, deploys a KServe InferenceService, and optionally creates HTTP routing through Gateway API.

## Where to Start

<div class="grid cards" markdown>

-   :material-server:{ .lg .middle } **Cluster Administrators**

    ---

    Install AIM Engine, configure KServe, manage GPU resources, and set up cluster-wide defaults.

    [:octicons-arrow-right-24: Installation](getting-started/installation.md)

-   :material-code-braces:{ .lg .middle } **Developers & Integrators**

    ---

    Deploy inference services, configure scaling, set up routing, and integrate with your applications.

    [:octicons-arrow-right-24: Quickstart](getting-started/quickstart.md)

-   :material-brain:{ .lg .middle } **Data Scientists**

    ---

    Browse the model catalog, deploy models for experimentation, and tune inference parameters.

    [:octicons-arrow-right-24: Model Catalog](guides/model-catalog.md)

</div>

## Key Features

- **Simple Service Deployment** -- Deploy inference endpoints with minimal configuration using `AIMService` resources
- **Automatic Optimization** -- Smart template selection picks the best runtime profile based on GPU availability, precision, and optimization goals
- **Model Catalog** -- Maintain a catalog of available models with automatic discovery from container registries
- **Model Caching** -- Pre-download model artifacts to shared PVCs for faster startup and reduced bandwidth
- **HTTP Routing** -- Expose services through Gateway API with customizable path templates
- **Autoscaling** -- KEDA integration with OpenTelemetry metrics for demand-based scaling
- **Multi-tenancy** -- Namespace-scoped and cluster-scoped resources for flexible team isolation

## Documentation

### Getting Started

- [Installation](getting-started/installation.md) -- Prerequisites and Helm chart installation
- [Quickstart](getting-started/quickstart.md) -- Deploy your first model in 5 minutes
- [Architecture](getting-started/architecture.md) -- High-level architecture and component overview

### Guides

Task-oriented walkthroughs for common workflows:

- [Deploying Services](guides/deploying-services.md) -- Deploy and manage inference endpoints
- [Model Catalog](guides/model-catalog.md) -- Browse and select models
- [Scaling and Autoscaling](guides/scaling-and-autoscaling.md) -- Replicas, KEDA, and metrics
- [Model Caching](guides/model-caching.md) -- Pre-cache models for faster startup
- [Routing and Ingress](guides/routing-and-ingress.md) -- Gateway API patterns and path templates
- [Private Registries](guides/private-registries.md) -- Authentication for HuggingFace, S3, and OCI
- [Multi-Tenancy](guides/multi-tenancy.md) -- Namespace isolation patterns

### Administration

- [Installation Reference](admin/installation.md) -- Full install reference with all Helm values
- [KServe Configuration](admin/kserve-configuration.md) -- Install and configure KServe
- [GPU Management](admin/gpu-management.md) -- GPU allocation, node selectors, topology
- [Storage Configuration](admin/storage-configuration.md) -- PVCs, shared storage for caching
- [Upgrading](admin/upgrading.md) -- Version migration and CRD upgrades
- [Monitoring](admin/monitoring.md) -- Metrics, observability, and log formats
- [Troubleshooting](admin/troubleshooting.md) -- Common issues and diagnostic steps
- [Security](admin/security.md) -- RBAC, network policies, and secrets management

### Concepts

- [AIM Services](concepts/services.md) -- Service deployment lifecycle, template selection, and caching
- [AIM Models](concepts/models.md) -- Model catalog, discovery, and resolution
- [Model Sources](concepts/model-sources.md) -- Automatic model discovery from container registries
- [Service Templates](concepts/templates.md) -- Runtime profiles, derivation, and discovery cache
- [Runtime Configuration](concepts/runtime-config.md) -- Storage defaults, routing, and environment resolution
- [Model Caching](concepts/caching.md) -- Cache hierarchy, ownership, and deletion behavior
- [Resource Lifecycle](concepts/resource-lifecycle.md) -- Ownership, finalizers, and deletion behavior

### Reference

- [CRD API Reference](reference/api/v1alpha1.md) -- Complete API specification for all custom resources
- [Helm Chart Values](reference/helm-values.md) -- All configurable Helm chart values
- [CLI and Operator Flags](reference/cli.md) -- Operator binary flags and endpoints
- [Environment Variables](reference/environment-variables.md) -- Operator and downloader configuration
- [Naming and Labels](reference/naming-and-labels.md) -- Derived naming algorithm and label conventions
- [Conditions](reference/conditions.md) -- Full catalog of conditions across all CRDs
