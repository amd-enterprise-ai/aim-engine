# AIM Engine

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)


AIM Engine is a Kubernetes operator for running and managing AMD Inference Microservices (AIM). AIM provides optimized inference containers for running AI models on AMD hardware. This operator handles the lifecycle of AIM deployments, including model management, scaling, and configuration.

## Features

### Declarative Model Deployment
- Deploy models with a single `AIMService` resource - from container image to inference endpoint
- Automatic model discovery from AIM container images
- Support for Hugging Face Hub and S3 model sources

### Intelligent Resource Management  
- **Smart template selection** - Automatically selects the optimal runtime configuration based on GPU availability, precision requirements, and optimization goals (latency vs throughput)

### Production-Ready Infrastructure
- **Model caching** - Hierarchical cache system pre-downloads model artifacts to shared PVCs, eliminating cold-start delays
- **Autoscaling** - KEDA integration with OpenTelemetry metrics for demand-based scaling
- **HTTP routing** - Gateway API integration with templated path configuration

### Multi-Tenant Design
- Namespace-scoped and cluster-scoped resources for flexible access control
- Runtime configurations for team-specific credentials and routing policies
- Label propagation for cost tracking and compliance

## Example AIMService Deployment
```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: llama-chat
spec:
  model:
    image: ghcr.io/amd/aim-meta-llama-llama-3-1-8b-instruct:latest
```

## Deployment
## Requirements

| Component | Version |
|-----------|---------|
| Kubernetes | 1.28+ |
| KServe | 0.16+ |
| Gateway API | 1.3+ |

### Using Helm

```bash
# Install CRDs into kubernetes-cluster
helm install aim-engine-crds amd-eai/aim-engine-crds --namespace aim-system --create-namespace

# Install the cluster-scoped operator
helm install aim-engine amd-eai/aim-engine \
  --namespace aim-system \
  --values custom-values.yaml
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
- [Usage Guide](./docs/docs/usage/) - Practical deployment examples  
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
