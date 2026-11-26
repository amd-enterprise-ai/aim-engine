# AIM Engine

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)


AIM Engine is a Kubernetes operator for running and managing AMD Inference Microservices (AIM). AIM provides optimized inference containers for running AI models on AMD hardware. This operator handles the lifecycle of AIM deployments, including model management, scaling, and configuration.

## Features

### Declarative Model Deployment
- Deploy models with a single `AIMService` resource - from container image to inference endpoint
- Automatic model discovery from AIM container images
- Support for Hugging Face Hub and S3 model sources

### Intelligent Resource Management
- **Smart profile resolution** - Automatically selects the optimal `AIMProfile` based on GPU availability, precision requirements, and optimization goals (latency vs throughput).

### Production-Ready Infrastructure
- **Model caching** - Cache system that pre-downloads model artifacts to shared PVCs, saving space and reducing load time
- **Autoscaling** - KEDA integration with OpenTelemetry metrics for demand-based scaling
- **HTTP routing** - Gateway API integration with templated path configuration

### Multi-Tenant Design
- Namespace-scoped and cluster-scoped resources for flexible access control
- Runtime configurations for team-specific credentials and routing policies


## Quick Install (from publish-main branch)

For quick installation using pre-built artifacts from the `publish-main` branch:

```bash
git clone -b publish-main https://github.com/amd-enterprise-ai/aim-engine.git aim-engine-deploy
cd aim-engine-deploy

# Install CRDs (distributed separately from Helm chart)
kubectl apply --server-side -f crd/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Install the operator. Gateway activation remains disabled by default.
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

Scale-from-zero gateway activation is an optional platform integration. To
enable it on Envoy Gateway, follow the
[optional Envoy scale-from-zero guide](docs/docs/admin/envoy-gateway-scale-from-zero.md).
In a `publish-main` checkout, the version-matched policy and standalone
collector documentation are under `prereqs/scale-from-zero/`.

## Manual Build and Deploy

To build and deploy from source with Helm templating:

```bash
# Generate CRDs and Helm chart
make crds
make helm

# Install CRDs
kubectl apply --server-side -f dist/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Option 1: Install directly with Helm
helm install aim-engine ./dist/chart --namespace aim-system --create-namespace

# Option 2: Render templates first, then apply
helm template aim-engine ./dist/chart \
  --namespace aim-system \
  --values dist/chart/values.yaml > rendered.yaml
kubectl apply -f rendered.yaml
```

Both commands leave gateway activation disabled. Enabling Envoy Gateway is a
deliberate add-on: the platform administrator installs the Gateway-targeted
`EnvoyExtensionPolicy`, then installs or upgrades AIM Engine with
`--set scaleFromZero.gatewayProvider=envoyGateway`. The chart manages the
matching collector by default but never manages the Gateway or extension.

## Example AIMService Deployment

The recommended v1alpha2 flow is to apply an `AIMClusterModel` (or `AIMModel`) and reference it by name:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterModel
metadata:
  name: qwen-qwen3-32b
spec:
  image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen3-chat
  namespace: ml-team
spec:
  model:
    name: qwen-qwen3-32b
```

The service resolves `qwen-qwen3-32b` to one of the `AIMClusterProfile`s produced by the `AIMClusterModel` above.

For a one-shot deploy from just a container image (no pre-applied `AIMModel`), the operator auto-creates a dedicated, service-owned `AIMModel` for the image:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen3-chat
  namespace: ml-team
spec:
  model:
    image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
```

## Development Quickstart

Install [mise](https://mise.jdx.dev) for tool management:

```bash
curl https://mise.run | sh
echo 'eval "$(mise activate bash)"' >> ~/.bashrc  # or zsh/fish
source ~/.bashrc
```

Clone and setup:

```bash
git clone https://github.com/amd-enterprise-ai/aim-engine.git
cd aim-engine
mise install  # installs Go, linters, and all dev tools
```

Common tasks:

```bash
make generate   # generate code from API types
make manifests  # generate CRDs and RBAC
make lint       # run linter
make test       # run unit tests
```

Create local cluster for test (adds entry to kubeconfig)
```bash
make kind-create
```

Run controller
```bash
make install # Install crds
make run     # start controller via access from kubeconfig
```

## Documentation

- [Concepts](./docs/docs/concepts/) - Core concepts and architecture
- [Guides](./docs/docs/guides/) - Practical deployment examples
- [API Reference](./docs/docs/reference/) - CRD specifications
- [Administration](./docs/docs/admin/) - Platform configuration

## Related Projects

AIM Engine is part of the [AMD Enterprise AI](https://github.com/amd-enterprise-ai) ecosystem:

- [amd-eai-suite](https://github.com/amd-enterprise-ai/amd-eai-suite) - Complete AMD Enterprise AI suite
- [aim-build](https://github.com/amd-enterprise-ai/aim-build) - Build optimized AIM container images
- [aim-deploy](https://github.com/amd-enterprise-ai/aim-deploy) - Deployment utilities and scripts
- [solution-blueprints](https://github.com/amd-enterprise-ai/solution-blueprints) - Reference architectures with AIMs

## License

[MIT License](./LICENSE) - Copyright (c) 2025 Advanced Micro Devices, Inc.
