# aim-engine

This branch contains the auto-generated Helm chart and CRDs for aim-engine.

## Installation

CRDs are distributed separately and must be installed first:

```bash
# 1. Install CRDs
kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/publish-main/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# 2. Install operator via Helm
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

Or clone this branch:

```bash
git clone -b publish-main https://github.com/amd-enterprise-ai/aim-engine.git aim-engine
cd aim-engine

kubectl apply -f crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

## Source

Generated from commit: 8ef5b60c446813feabbd51464e8a9aa1603a12de
