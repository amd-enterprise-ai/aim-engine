# Red Hat OpenShift

This guide supplements the Kubernetes installation and deployment instructions
with OpenShift-specific requirements for AIM Engine.

It is written for OpenShift cluster administrators. The examples use an AMD
Instinct MI355X GPU and the Qwen3-32B AIM image, but the OpenShift procedures
also apply to other supported AMD accelerators and AIM images.

## Differences from upstream Kubernetes

OpenShift adds platform controls that affect AIM Engine installation:

- Use `oc` for OpenShift administration and `oc get csv` for Operator
  Lifecycle Manager (OLM) resources.
- Security Context Constraints (SCCs) can reject pods that request privileged
  mode, host paths, host devices, or host networking even when the Kubernetes
  manifest is valid.
- OpenShift Routes are an alternative to Gateway API HTTPRoutes for exposing a
  predictor outside the cluster.
- The default StorageClass may not support `ReadWriteMany` (RWX), which AIM
  model caching requires.
- SELinux labeling and SCC admission must be considered for any hostPath-based
  test storage.

The OpenShift-specific samples are in
[`config/samples/openshift/`](https://github.com/amd-enterprise-ai/aim-engine/tree/main/config/samples/openshift).

## Prerequisites

Install or verify the following before installing AIM Engine:

- OpenShift version supported by the selected AIM Engine release
- AMD GPU Operator, or an equivalent AMD device-plugin, runtime, and NFD
  installation
- KServe v0.16.1 or later in Standard deployment mode
- cert-manager v1.16 or later
- Helm 3 and the `oc` CLI
- An RWX-capable StorageClass for model caching
- `jq` and `rg` for the diagnostic commands in this guide, or equivalent
  JSON/text inspection tools

Optional components include Gateway API with a supported gateway implementation
for AIM-managed HTTPRoutes, and KEDA/OpenTelemetry for autoscaling.

Check the cluster:

```bash
oc version
oc get clusterversion
oc get nodes -o wide
oc get csv -A
oc get storageclass
oc get nodes -o json | jq -r \
  '.items[] | [.metadata.name, (.status.allocatable["amd.com/gpu"] // "0")] | @tsv'
```

Do not reinstall shared AMD Operators only because they are listed as
prerequisites. Confirm ownership and versions first.

## Projects and permissions

Use a dedicated project for model-serving workloads:

```bash
oc new-project aim-models
```

Keep AIM Engine control-plane resources in the installation namespace, usually
`aim-system`. Application users should receive namespace-scoped permissions for
their `AIMService` resources rather than cluster-admin access.

## cert-manager and KServe

Install cert-manager using the OLM Subscription and catalog appropriate for the
OpenShift cluster. Wait for the CSV and pods to become ready:

```bash
oc get csv -A | rg -i cert-manager
oc get pods -n cert-manager
```

Install KServe in Standard mode using the AIM-compatible values from
[`KServe Configuration`](kserve-configuration.md):

```yaml
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

For example:

```bash
helm repo add kserve https://kserve.github.io/helm-charts
helm repo update
helm upgrade --install kserve-crd kserve/kserve-crd \
  --namespace kserve --create-namespace --version 0.17.1
helm upgrade --install kserve-resources kserve/kserve-resources \
  --namespace kserve --version 0.17.1 \
  -f config/samples/openshift/kserve-values.yaml
```

Verify the KServe controller and CRDs:

```bash
oc get pods -n kserve
oc get crd | rg 'inferenceservices|servingruntimes|clusterservingruntimes'
```

## Install AIM Engine

Install the AIM CRDs before the operator chart. The following uses the same
server-side CRD application pattern as the OpenShift test. Replace
`<aim-engine-version>` with the AIM Engine release being installed:

```bash
export KUBECONFIG=/path/to/openshift-kubeconfig
AIM_ENGINE_VERSION=<aim-engine-version>

helm template aim-engine-crds \
  oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version "$AIM_ENGINE_VERSION" \
  --namespace aim-system \
  | oc apply --server-side -f -

oc get crd -o name | grep 'aim.eai.amd.com' | while read -r crd; do
  oc wait --for=condition=Established "$crd" --timeout=120s
done

helm upgrade --install aim-engine \
  oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version "$AIM_ENGINE_VERSION" \
  --namespace aim-system \
  --create-namespace \
  --wait
```

Server-side application avoids the Kubernetes object-size limitation that can
occur when large rendered CRDs are stored in a Helm release Secret.

Verify the installation:

```bash
helm list -n aim-system
oc get crd | rg 'aim.eai.amd.com'
oc get pods -n aim-system
```

## Security Context Constraints

SCC admission is the most common OpenShift-specific installation issue. AIM's
accelerator detector may need host access to inspect hardware and publish NFD
labels. Determine the detector service account from the installed DaemonSet:

```bash
oc get daemonset -n aim-system \
  -o custom-columns=NAME:.metadata.name,SERVICE_ACCOUNT:.spec.template.spec.serviceAccountName
```

Review the required SCC before granting it:

```bash
oc adm policy scc-review \
  -z <detector-service-account> \
  --filename <detector-manifest.yaml>
```

If the deployment requires the built-in privileged SCC, bind it only to the
detector service account. The sample
[`detector-scc-rolebinding.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/detector-scc-rolebinding.yaml)
is intentionally parameterized.

```bash
oc adm policy add-scc-to-user privileged \
  -z <detector-service-account> \
  -n aim-system
```

Alternatively, apply a reviewed RoleBinding after replacing the service-account
placeholder in the sample. Do not grant `privileged` to an entire namespace,
group, or all service accounts. Production deployments should use the narrowest
custom SCC that permits the detector's required host paths, devices, and
security context.

The detector DaemonSets in the tested AIM Engine release also required an
OpenShift security-context patch after the SCC binding. Apply the parameterized
sample after AIM Engine is installed and before relying on detector readiness:

```bash
bash config/samples/openshift/patch-detectors.sh
```

The script discovers detector DaemonSets by label, grants the SCC to their
shared service account, and patches only those detector pods. Review the
resulting pod security context with the platform administrator. If a future AIM
Engine release no longer needs this patch, the script should not be applied
automatically.

Check the SCC that admitted a pod:

```bash
oc get pod -n aim-system <detector-pod> \
  -o jsonpath='{.metadata.annotations.openshift\\.io/scc}{"\\n"}'
oc describe pod -n aim-system <detector-pod>
```

## Validate accelerator detection

The AMD GPU Operator must advertise GPU capacity, and AIM's detector must
identify the actual product and partitioning state:

```bash
oc get nodes -o json | jq -r \
  '.items[] | [.metadata.name, (.status.allocatable["amd.com/gpu"] // "0")] | @tsv'
oc logs -n aim-system \
  daemonset/aim-engine-aim-engine-chart-accelerator-detector-gpu
oc get nodes --show-labels | rg -i 'amd.com/gpu|aim-accelerator'
```

For an MI355X node, an expected detector message resembles:

```text
Detected: type=GPU model=MI355X count=8
Partition schemes: SPX-NPS1=8 (+default sentinel)
```

If the detector log identifies a GPU but the normalized AIM labels are absent,
inspect the DaemonSet permissions, NFD worker logs, and detector logs on every
node. Do not assume that one healthy detector pod makes every node schedulable.

## Deploy a model and service

The sample
[`aimservice-mi355x.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/aimservice-mi355x.yaml)
contains an `AIMClusterModel` and an `AIMService`. Apply the model first so
profile discovery can be observed independently:

```bash
oc apply -f config/samples/openshift/aimservice-mi355x.yaml
oc get aimclustermodel qwen3-32b-mi355x -o yaml
```

Wait for at least one deployable profile, then inspect the service:

```bash
oc get aimservice -n aim-models
oc get aimservice -n aim-models qwen3-32b-chat -o yaml
```

The sample requests one MI355X GPU and selects a latency profile. Replace the
image tag with a version supported by the AIM Engine release being installed.

## Configure RWX model caching

AIM downloads model artifacts to persistent storage before starting the
predictor. Confirm the cluster provides an RWX-capable StorageClass:

```bash
oc get storageclass
oc get storageclass <rwx-storage-class> -o yaml
```

Configure the AIM runtime default only after confirming the StorageClass is
approved for the target project. Prefer a namespace-scoped runtime configuration
for an application team:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: aim-models
spec:
  storage:
    defaultStorageClassName: <rwx-storage-class>
```

The platform must replace `<rwx-storage-class>` before applying this resource.
Use `AIMClusterRuntimeConfig/default` only when a cluster administrator
intentionally wants to change the cache default for all namespaces.
Do not use a local hostPath PV as a production RWX implementation. A hostPath
workaround is suitable only for a disposable single-node test and requires
node affinity, ownership, SELinux labeling, and a privileged preparation pod.

If a cache PVC is pending, inspect its events before deleting anything:

```bash
oc get pvc -n aim-models
oc describe pvc -n aim-models <cache-pvc>
oc get events -n aim-models --sort-by=.lastTimestamp
```

An error such as `MULTI_NODE_MULTI_WRITER` means that the selected StorageClass
does not support RWX.

## Networking

For private testing, use a temporary port-forward to the KServe predictor:

```bash
oc get inferenceservice -n aim-models
oc get svc -n aim-models
oc port-forward -n aim-models svc/<predictor-service> 18000:80
```

For approved OpenShift-native external access, use the parameterized
[`route.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/route.yaml)
after replacing the generated predictor Service name:

```bash
oc apply -f config/samples/openshift/route.yaml
oc get route -n aim-models
```

The Route example uses edge TLS termination. Production deployments must also
address authentication, authorization, rate limiting, network policy, and
certificate ownership. AIM-managed Gateway API HTTPRoutes remain available when
the cluster has a supported Gateway API implementation; see
[Routing and Ingress](../guides/routing-and-ingress.md).

## Verify readiness and inference

```bash
oc get aimservice -n aim-models
oc get inferenceservice -n aim-models
oc get pods -n aim-models -o wide
oc get pvc -n aim-models
oc get events -n aim-models --sort-by=.lastTimestamp
```

Expected healthy state:

- AIMService status is `Running`.
- InferenceService reports `Ready=True`.
- Predictor pod is `1/1 Running` on a node with the requested AMD GPU.
- Cache PVC is `Bound` using the approved RWX StorageClass.

Discover the exact served model identifier:

```bash
curl --fail --silent --show-error http://127.0.0.1:18000/v1/models
```

Use the returned `id` in the completion request:

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:18000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "<model-id-from-v1-models>",
    "messages": [{"role": "user", "content": "Say hello in one word."}],
    "max_tokens": 32,
    "chat_template_kwargs": {"enable_thinking": false}
  }'
```

## Troubleshooting

| Symptom | OpenShift checks |
| --- | --- |
| Detector pod is rejected | `oc describe pod`, SCC review, service-account binding, hostPath/device permissions |
| GPU is detected but not schedulable | `amd.com/gpu` allocatable capacity, NFD labels, AIM detector logs on every node |
| Cache PVC is Pending | StorageClass access modes, PV availability, PVC events, RWX support |
| Predictor is Pending | Selected profile, GPU capacity, cache binding, existing workloads |
| `ImagePullBackOff` | Registry reachability, pull secret, image name, catalog access |
| Route is unavailable | Route target Service, router status, TLS, network policy |
| AIMService remains Pending | Deployable profile availability and profile resource requirements |
| API reports unknown model | Query `/v1/models` and use the returned `id` exactly |

Useful commands:

```bash
oc describe aimservice -n aim-models <service>
oc describe pod -n aim-models <pod>
oc describe pvc -n aim-models <pvc>
oc get events -n aim-models --sort-by=.lastTimestamp
oc logs -n aim-models deploy/<predictor-deployment>
```

## Cleanup and shared-cluster safety

Delete application resources before changing cluster-wide components:

```bash
oc delete aimservice -n aim-models qwen3-32b-chat
oc delete aimclustermodel qwen3-32b-mi355x
```

Remove only the cache resources owned by the test deployment and restore any
runtime storage override when appropriate. Do not delete shared KServe,
cert-manager, AMD Operators, AIM CRDs, validation jobs, or unrelated GPU
workloads without confirming ownership and active users.
