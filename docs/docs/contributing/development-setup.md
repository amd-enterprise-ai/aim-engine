# Development Setup

Set up a local development environment for AIM Engine.

## Prerequisites

- Docker or Podman — For building images and running Kind clusters
- `kubectl` — Kubernetes CLI
- Go 1.24+, controller-gen, golangci-lint, chainsaw — See [Tool Management](#tool-management) below

## Tool Management

This project uses [mise](https://mise.jdx.dev) to pin exact tool versions (Go, controller-gen, chainsaw, golangci-lint, etc.). This is the recommended approach to ensure you have compatible versions:

```bash
# Install mise
curl https://mise.run | sh

# Activate mise in your shell (adds tools to PATH)
echo 'eval "$(mise activate bash)"' >> ~/.bashrc
source ~/.bashrc

# Install all project tools
mise install
```

After activation, all tools are available directly on your PATH — no `mise exec --` prefix needed.

If you prefer to manage tools yourself, check `mise.toml` for the required versions.

## Getting Started

```bash
git clone https://github.com/amd-enterprise-ai/aim-engine.git
cd aim-engine
```

## Building

```bash
make build        # Build binary to bin/manager
make generate     # Generate DeepCopy methods
make manifests    # Generate CRDs and RBAC
make fmt          # Format code
make vet          # Vet code
make lint         # Run golangci-lint
```

## Local Cluster

Create a local Kind cluster with dependencies:

```bash
make kind-create  # Create Kind cluster
make install      # Install CRDs
```

## Running the Operator

### Standard Mode

```bash
make run          # Run controller against kubeconfig
make run-debug    # Run with debug logging
```

### Live Reload (Recommended for Development)

```bash
make watch        # Rebuild and restart on file changes
```

This uses [air](https://github.com/air-verse/air) for live reload. Operator logs are written to timestamped files in `.tmp/logs/`:

```bash
# Tail the latest log
tail -f "$(ls -t .tmp/logs/air-*.log | head -1)"

# Search for errors
grep -E '"level":"error"' "$(ls -t .tmp/logs/air-*.log | head -1)"
```

### Readiness Check

Wait for the operator to be ready before running tests:

```bash
make wait-ready   # Polls localhost:8081/readyz
```

## Environment Switching

Switch between local (Kind) and remote (GPU vcluster) environments:

```bash
# Kind environment (default)
make watch

# GPU environment
export KUBE_CONTEXT_GPU=my-vcluster-context
export ENV=gpu
make watch

# Check current environment
make env-info
```

The current environment is persisted to `.tmp/current-env` and shared across terminals.

## Code Generation

Pre-commit hooks auto-run `make generate` and `make manifests` when `api/` files change.

Generated files (don't edit manually):

- `api/v1alpha1/zz_generated.deepcopy.go`
- `config/crd/bases/*.yaml`
- `config/rbac/role.yaml`

## Documentation

```bash
make generate-docs    # Generate CRD and Helm values reference
make docs-serve       # Start local docs server
```

## Next Steps

- [Testing](testing.md) — Running unit and e2e tests
- [Controller Patterns](controller-patterns.md) — Architecture and implementation patterns
