# AIM Engine

AIM Engine is a Kubernetes operator for managing AMD Inference Microservices (AIM). AIM provides optimized inference containers for running AI models on AMD hardware. This operator handles the lifecycle of AIM deployments, including model management, scaling, and configuration.

## Features

TODO

## Deployment

### Quick Install (from publish-main branch)

For quick installation using pre-built artifacts from the `publish-main` branch:

```bash
git clone -b publish-main https://github.com/amd-enterprise-ai/aim-engine.git aim-engine-deploy
cd aim-engine-deploy

# Install CRDs (distributed separately from Helm chart)
kubectl apply -f crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Install operator via Helm
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

### Manual Build and Deploy

To build and deploy from source with Helm templating:

```bash
# Generate CRDs and Helm chart
make crds
make helm

# Install CRDs
kubectl apply -f dist/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# Option 1: Install directly with Helm
helm install aim-engine ./dist/chart --namespace aim-system --create-namespace

# Option 2: Render templates first, then apply
helm template aim-engine ./dist/chart \
  --namespace aim-system \
  --values dist/chart/values.yaml > rendered.yaml
kubectl apply -f rendered.yaml
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

## License

MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
