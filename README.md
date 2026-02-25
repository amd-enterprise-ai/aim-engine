# aim-engine

This branch contains the auto-generated Helm chart and CRDs for aim-engine.

## Installation

CRDs are distributed separately and must be installed first:

```bash
# 1. Install CRDs
kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/artifacts/crd/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# 2a. Install operator via Helm OCI (recommended)
helm install aim-engine oci://ghcr.io/amd-enterprise-ai/aim-engine-chart \
  --version 0.2.1 \
  --namespace aim-system --create-namespace

# 2b. Or install from this branch
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

Or clone this branch:

```bash
git clone -b artifacts https://github.com/amd-enterprise-ai/aim-engine.git aim-engine
cd aim-engine

kubectl apply -f crd/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

## Source

Generated from tag: v0.2.1 (commit: 9223504d74b01fec2e99d250933ea2884aef3b87)
