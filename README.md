# aim-engine

This branch contains the auto-generated Helm chart and CRDs for aim-engine.

## Installation

### Prerequisite: image pull secret

The operator image is hosted on a private DockerHub registry. The
packaged chart's `values.yaml` defaults `manager.imagePullSecrets`
to a secret named `dockerhub-regcred`. Provision it in the install
namespace before installing the operator (skip if the secret is
already present):

```bash
kubectl create namespace aim-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret docker-registry dockerhub-regcred \
  --namespace aim-system \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username=<your-dockerhub-username> \
  --docker-password=<your-dockerhub-token>
```

To use a different secret name, override at install time with
`--set manager.imagePullSecrets[0].name=...`.

The private `aim-artifact-downloader` image is pulled by download Jobs
in your workload namespace, which requires node-level (containerd)
registry credentials configured on each node.

CRDs are distributed separately and must be installed first:

```bash
# 1. Install CRDs
kubectl apply -f https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/publish-main/crd/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s

# 2a. Install operator via Helm OCI (recommended)
helm install aim-engine oci://registry-1.docker.io/silogenai/aim-engine-chart \
  --version 0.0.0-publish-main.22b8995 \
  --namespace aim-system --create-namespace

# 2b. Or install from this branch
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

Gateway activation is disabled by default. The base install does not
select a Gateway implementation or render a gateway metrics collector.

### Optional Envoy Gateway scale-from-zero

A platform administrator must install Envoy Gateway and the
Gateway-targeted policy independently. The Gateway's `EnvoyProxy`
must also push OTLP metrics to the collector with
`reportCountersAsDeltas: true`. Edit the policy's Gateway
namespace/name, apply it, deliberately enable the matching
Helm-managed collector, and follow the setup guide to configure the
sink endpoint:

```bash
curl -LO https://raw.githubusercontent.com/amd-enterprise-ai/aim-engine/publish-main/prereqs/scale-from-zero/envoy-gateway-route-metrics.yaml
kubectl apply -f envoy-gateway-route-metrics.yaml
helm upgrade aim-engine oci://registry-1.docker.io/silogenai/aim-engine-chart \
  --version 0.0.0-publish-main.22b8995 \
  --namespace aim-system \
  --set scaleFromZero.gatewayProvider=envoyGateway
```

See the [Envoy scale-from-zero setup guide](prereqs/scale-from-zero/README.md)
for target customization, verification, and security guidance.

Or clone this branch:

```bash
git clone -b publish-main https://github.com/amd-enterprise-ai/aim-engine.git aim-engine
cd aim-engine

kubectl apply -f crd/crds.yaml
kubectl wait --for=condition=Established crd --all --timeout=60s
helm install aim-engine ./chart --namespace aim-system --create-namespace
```

## Source

Generated from commit: 22b89959676deec00b08fba3d1bb4c0f5370ff53
