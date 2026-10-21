# Red Hat OpenShift

This guide supplements the Kubernetes installation and deployment instructions
with OpenShift-specific requirements for AIM Engine.

It is written for OpenShift cluster administrators. The installation and
validation procedures are accelerator-agnostic. The worked example uses an AMD
Instinct MI355X GPU and the Qwen3-32B AIM image; replace those
model-specific values when using another supported AMD accelerator or AIM
image.

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

The commands in this guide that use `config/samples/openshift/` assume that
you are working from the AIM Engine repository root. These OpenShift sample
files are maintained on the repository's `main` branch; the AIM Engine chart
release is selected separately below. Clone the repository and change to its
root before continuing:

```bash
git clone --depth 1 --branch main \
  https://github.com/amd-enterprise-ai/aim-engine.git
cd aim-engine
git rev-parse --short HEAD
```

If you already have the repository, use an up-to-date `main` checkout
containing `config/samples/openshift/` instead of cloning it again.

The samples currently track `main` and are not versioned in lockstep with the
AIM Engine Helm chart. Record the printed source revision with the chart version
and verify compatibility before using a different chart release; in particular,
check sample API fields, detector labels, and the detector patch script.

## Prerequisites

Install or verify the following before installing AIM Engine:

- OpenShift version supported by the selected AIM Engine release
- AMD GPU Operator, or an equivalent AMD device-plugin, runtime, and NFD
  installation
- KServe v0.16.1 or later in Standard deployment mode
- cert-manager v1.16 or later
- Git, Helm 3, and the `oc` CLI
- An RWX-capable StorageClass for model caching, such as a class backed by
  NFS, CephFS, Longhorn, or another shared-filesystem provider
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

Install the cert-manager Operator for Red Hat OpenShift from the Red Hat
`redhat-operators` catalog. This procedure requires `cluster-admin` access and
assumes that the cluster can reach the Red Hat catalog. Do not install the
community cert-manager Operator in the same cluster; use only one cert-manager
Operator installation.

The `stable-v1` channel follows the latest supported cert-manager Operator
release. The `AllNamespaces` `OperatorGroup` is recommended for cert-manager
Operator v1.15 and later because AIM and KServe resources can be created in
separate application namespaces.

Confirm that the Red Hat catalog exposes the package:

```bash
oc get packagemanifest openshift-cert-manager-operator \
  -n openshift-marketplace \
  -o jsonpath='{.status.defaultChannel}{"\n"}'
```

The command should print `stable-v1`. Create the operator project, an
all-namespaces `OperatorGroup`, and the Subscription. If the project already
exists, skip the `oc new-project` command:

```bash
oc new-project cert-manager-operator
oc apply -f config/samples/openshift/cert-manager-operator.yaml
```

Wait for the OLM CSV and all cert-manager deployments before continuing:

```bash
oc wait \
  --for=jsonpath='{.status.phase}'=Succeeded \
  -n cert-manager-operator \
  csv -l operators.coreos.com/openshift-cert-manager-operator.cert-manager-operator \
  --timeout=10m

oc rollout status deployment/cert-manager-operator-controller-manager \
  -n cert-manager-operator --timeout=5m
oc rollout status deployment/cert-manager \
  -n cert-manager --timeout=5m
oc rollout status deployment/cert-manager-webhook \
  -n cert-manager --timeout=5m
oc rollout status deployment/cert-manager-cainjector \
  -n cert-manager --timeout=5m

oc get subscription,csv -n cert-manager-operator
oc get pods -n cert-manager-operator
oc get pods -n cert-manager
```

Install KServe v0.17.1 in Standard mode. AIM Engine supports KServe v0.16.1 or
later; this example uses v0.17.1's separate CRD and resource charts. KServe
changed its Helm chart layout in v0.17, so do not use the older single `kserve`
chart command with this version. Consult the linked KServe Configuration guide
for the values rationale only; its installation commands target v0.16.1. The
repository sample
[`kserve-values.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/kserve-values.yaml)
contains the AIM-compatible settings. For the chart-layout change, see the
[KServe v0.17 release notes](https://kserve.github.io/website/blog/kserve-0.17-release).

For example:

```bash
helm upgrade --install kserve-crd \
  oci://ghcr.io/kserve/charts/kserve-crd \
  --namespace kserve --create-namespace --version v0.17.1 \
  --wait --timeout 5m
helm upgrade --install kserve-resources \
  oci://ghcr.io/kserve/charts/kserve-resources \
  --namespace kserve --version v0.17.1 \
  -f config/samples/openshift/kserve-values.yaml \
  --wait --timeout 5m
```

Verify the KServe controller and CRDs:

```bash
oc get pods -n kserve
oc get crd | rg 'inferenceservices|servingruntimes|clusterservingruntimes'
```

## Install AIM Engine

Install the AIM CRDs before the operator chart. The following uses the same
server-side CRD application pattern as the OpenShift test. Set
`AIM_ENGINE_VERSION` to the AIM Engine release being installed; Helm chart
versions omit the optional leading `v`. The example below uses `v0.2.7`; change
it to the release selected for your installation.

```bash
export KUBECONFIG=/path/to/openshift-kubeconfig
# Set this to the AIM Engine release being installed.
export AIM_ENGINE_VERSION=v0.2.7

helm template aim-engine-crds \
  oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version "${AIM_ENGINE_VERSION#v}" \
  --namespace aim-system \
  --include-crds \
  | awk '
      /^---$/ { started=1; print; next }
      started && ($0 ~ /^Pulled:/ || $0 ~ /^Digest:/ || $0 ~ /^\{"(Pulled|Digest)"/) { next }
      started { print }
    ' \
  | oc apply --server-side -f -

oc get crd -o name | grep 'aim.eai.amd.com' | while read -r crd; do
  oc wait --for=condition=Established "$crd" --timeout=120s
done

# Do not use --wait on this first install. The detector DaemonSets require the
# OpenShift SCC workaround below before they can become Ready.
helm upgrade --install aim-engine \
  oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version "${AIM_ENGINE_VERSION#v}" \
  --namespace aim-system \
  --create-namespace

oc rollout status deployment/aim-engine-controller-manager \
  -n aim-system --timeout=120s

# Continue with the Security Context Constraints section before checking the
# detector DaemonSets or treating the Helm release as ready.
```

Server-side application avoids the Kubernetes object-size limitation that can
occur when large rendered CRDs are stored in a Helm release Secret.

## Configure Security Context Constraints

Run this section immediately after installing AIM Engine and before treating
the detector DaemonSets as ready. The AIM chart must be installed first so
that its detector DaemonSets and service account exist; the initial Helm
installation above intentionally omits `--wait` for this reason.

SCC admission is the most common OpenShift-specific installation issue. AIM's
accelerator detector may need host access to inspect hardware and publish NFD
labels. Determine the detector service account from the installed DaemonSet:

```bash
oc get daemonset -n aim-system \
  -o custom-columns=NAME:.metadata.name,SERVICE_ACCOUNT:.spec.template.spec.serviceAccountName
```

Review the required SCC before granting it:

```bash
AIM_ENGINE_RELEASE="${AIM_ENGINE_RELEASE:-aim-engine}"
DETECTOR_SELECTOR="app.kubernetes.io/instance=${AIM_ENGINE_RELEASE},app.kubernetes.io/managed-by=Helm"

oc get daemonset -n aim-system \
  -l "$DETECTOR_SELECTOR"

DETECTOR_DS="$(oc get daemonset -n aim-system \
  -l "$DETECTOR_SELECTOR" -o name | head -n 1)"
if [[ -z "$DETECTOR_DS" ]]; then
  echo "No AIM Engine detector DaemonSet found in aim-system" >&2
  exit 1
fi
DETECTOR_SA="$(oc get "$DETECTOR_DS" -n aim-system \
  -o jsonpath='{.spec.template.spec.serviceAccountName}')"
oc get "$DETECTOR_DS" -n aim-system -o yaml \
  > /tmp/aim-detector-daemonset.yaml

oc adm policy scc-review \
  -z "$DETECTOR_SA" \
  -n aim-system \
  -f /tmp/aim-detector-daemonset.yaml
```

The review reports which SCC can admit the detector pod template. The command
reviews the first detector DaemonSet; AIM detector DaemonSets normally share a
service account. The temporary YAML file is generated from the installed
DaemonSet; it is not a repository file.

If the command prints only the column header and no row, no SCC currently
admits the detector pod template. This is expected before the workaround below
has granted the detector service account access to the required SCC.

The tested workaround for the release used here requires the built-in
`privileged` SCC. This is a high-privilege exception, not a least-privilege
configuration. The grant allows pods using this service account to request the
privileged SCC, so keep the service account dedicated to the detector and
review who can create pods that use it.

Review the script before running it and obtain approval from the cluster
administrator. It grants `privileged` to the detector service account and
patches the detector plus every init container in the matching DaemonSets to
run privileged as root with privilege escalation enabled. Do not run this
workaround on clusters whose policy prohibits privileged containers:

```bash
bash config/samples/openshift/patch-detectors.sh

oc rollout status deployment/aim-engine-controller-manager \
  -n aim-system --timeout=120s
```

The script selects DaemonSets by the AIM Engine Helm release labels. It both
grants the SCC and patches the pod templates; no separate SCC grant is needed
when using it. The
[`detector-scc-rolebinding.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/detector-scc-rolebinding.yaml)
sample is only a binding to the same built-in `privileged` SCC. It does not
apply the security-context patch, and should not also be applied when running
the script. Neither sample defines a custom SCC. Because the patch explicitly
requests privileged containers, a custom SCC would not make this procedure
least-privilege; if policy forbids privileged containers, stop and use a
detector configuration approved for that policy. Recheck the installed release
before reusing the workaround, since a later release may no longer need it.

Check the SCC that admitted a pod:

```bash
oc get pod -n aim-system \
  -l app.kubernetes.io/name=aim-accelerator-detector

DETECTOR_POD="$(oc get pod -n aim-system \
  -l app.kubernetes.io/name=aim-accelerator-detector \
  -o jsonpath='{.items[0].metadata.name}')"

oc get pod -n aim-system "$DETECTOR_POD" \
  -o jsonpath='{.metadata.annotations.openshift\.io/scc}{"\n"}'
oc describe pod -n aim-system "$DETECTOR_POD"
```

The first command lists the generated pod name. If it returns no pods, the
detector DaemonSet has not created one yet; check SCC admission events before
continuing.

After the SCC workaround completes, verify the installation:

```bash
helm list -n aim-system
oc get crd | rg 'aim.eai.amd.com'
oc get pods -n aim-system
oc get daemonset -n aim-system
```

## Validate accelerator detection

The AMD GPU Operator must advertise GPU capacity, and AIM's detector must
identify the actual product and partitioning state. The AMD GPU detector uses
the `feature.node.kubernetes.io/amd-gpu=true` node selector by default, so the
AMD GPU Operator/NFD integration must provide that label in addition to the
`amd.com/gpu` allocatable resource:

```bash
oc get nodes -l feature.node.kubernetes.io/amd-gpu=true -o wide
oc get nodes -o json | jq -r \
  '.items[] | [.metadata.name, (.status.allocatable["amd.com/gpu"] // "0")] | @tsv'
oc get daemonset -n aim-system \
  -l 'app.kubernetes.io/instance=aim-engine,app.kubernetes.io/component=gpu-detection'
oc get pods -n aim-system \
  -l app.kubernetes.io/component=gpu-detection
oc get nodes --show-labels | rg -i 'amd-gpu=true|amd.com/gpu|aim-accelerator'
```

The GPU detector's `DESIRED` count should match the number of AMD GPU nodes.
If no nodes have the `amd-gpu=true` label, the detector cannot schedule even
when the device plugin reports `amd.com/gpu` capacity.

After the GPU detector has a scheduled pod, inspect its logs:

```bash
oc logs -n aim-system \
  daemonset/aim-engine-aim-engine-chart-accelerator-detector-gpu
```

For the MI355X worked example, an expected detector message resembles:

```text
Detected: type=GPU model=MI355X count=8
Partition schemes: SPX-NPS1=8 (+default sentinel)
```

For another supported AMD accelerator, the `model` and `count` values should
match the hardware on the node; partition output depends on the accelerator.

If the detector log identifies a GPU but the normalized AIM labels are absent,
inspect the DaemonSet permissions, NFD worker logs, and detector logs on every
node. Do not assume that one healthy detector pod makes every node schedulable.

## Configure RWX model caching

AIM Engine resolves the cache StorageClass when it provisions model artifacts.
Complete this setup before applying the `AIMService`, so its first cache PVC
uses the intended class. First list the StorageClasses installed in the cluster:

```bash
oc get storageclass \
  -o custom-columns='NAME:.metadata.name,PROVISIONER:.provisioner,DEFAULT:.metadata.annotations.storageclass\.kubernetes\.io/is-default-class'
```

Choose a class whose provisioner supports `ReadWriteMany` (RWX). Do not infer
this from the class name or from whether it is the default class. Block-storage
provisioners commonly provide `ReadWriteOnce`, while shared-filesystem and
distributed-filesystem provisioners may provide RWX. Check the provisioner's
documentation and confirm the selected class and its parameters with:

```bash
export RWX_STORAGE_CLASS=<installed-rwx-storage-class>
oc get storageclass "${RWX_STORAGE_CLASS}" -o yaml
```

Replace `<installed-rwx-storage-class>` with the actual name printed by the
first command; do not include the angle brackets. If no installed class has an
RWX-capable provisioner, install or request an approved NFS-, CephFS-,
Longhorn-, or other RWX-backed StorageClass before continuing. A PVC requesting
`ReadWriteMany` will remain `Pending` when the selected provisioner cannot
provide RWX.

If the cluster's default StorageClass is the approved RWX class, no AIM runtime
storage override is needed. Otherwise, replace
`replace-me-rwx-storage-class` in the namespace-scoped sample
[`aimruntimeconfig-rwx.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/aimruntimeconfig-rwx.yaml)
with the approved class name, then apply it:

```bash
oc apply -f config/samples/openshift/aimruntimeconfig-rwx.yaml
oc get aimruntimeconfig default -n aim-models -o yaml
```

This sample affects only `aim-models`. Use
`AIMClusterRuntimeConfig/default` only when a cluster administrator
intentionally wants to change the cache default for all namespaces.
Do not use a local hostPath PV as a production RWX implementation. A hostPath
workaround is suitable only for a disposable single-node test and requires
node affinity, ownership, SELinux labeling, and a privileged preparation pod.

## Deploy a model and service

The sample
[`aimservice-mi355x.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/aimservice-mi355x.yaml)
contains both an `AIMClusterModel` and an `AIMService`. Applying it once is
valid; the service remains pending until profile discovery produces a deployable
profile. Observe model discovery with:

```bash
oc apply -f config/samples/openshift/aimservice-mi355x.yaml
oc get aimclustermodel qwen3-32b-mi355x -o yaml
```

Wait for at least one deployable profile, then inspect the service:

```bash
oc get aimservice -n aim-models
oc get aimservice -n aim-models qwen3-32b-chat -o yaml
```

The sample requests one MI355X GPU and selects a latency profile. It is a
concrete worked example, not a hardware requirement. For another supported
AMD accelerator, update the model image, `acceleratorModel`, and
`acceleratorCount` to matching values supported by the AIM Engine release.

## Troubleshoot RWX model caching

RWX support is separate from PV placement and hostPath permissions. A PVC can
be `Bound` while its cache or download pod remains `Pending` if the bound PV's
node affinity does not match a suitable node. A hostPath download can also fail
with `Permission denied` when its directory ownership or SELinux label is not
prepared. Inspect the bound PV and the cache/download pod when diagnosing this
case:

```bash
oc get pvc -n aim-models
oc get pv <bound-pv> -o yaml
oc get pods -n aim-models
oc describe pod -n aim-models <cache-or-download-pod>
```

If a cache PVC is pending, inspect its events before deleting anything:

```bash
oc get pvc -n aim-models
oc describe pvc -n aim-models <cache-pvc>
oc get events -n aim-models --sort-by=.lastTimestamp
```

`MULTI_NODE_MULTI_WRITER` is a CSI volume access-mode value associated with
multi-node read/write access (Kubernetes `ReadWriteMany`); by itself, it is not
a diagnosis that the StorageClass lacks RWX. If it appears in an event, inspect
the complete PVC event and CSI provisioner message, then verify that the
selected provisioner can satisfy the requested access mode. See the
[CSI volume capability definitions](https://github.com/container-storage-interface/spec/blob/master/csi.proto).

## Networking

For private testing, use a temporary port-forward to the KServe predictor:

```bash
oc get inferenceservice -n aim-models
```

Use the `NAME` returned for the ready `InferenceService`. KServe's Standard
deployment normally creates the predictor Service by appending `-predictor` to
that name. Verify the generated Service before forwarding its port:

```bash
export ISVC_NAME=<inferenceservice-name>
export PREDICTOR_SERVICE="${ISVC_NAME}-predictor"
oc get svc -n aim-models "${PREDICTOR_SERVICE}"
oc port-forward -n aim-models "svc/${PREDICTOR_SERVICE}" 18000:80
```

The port-forward command stays in the foreground. Leave it running and use a
second terminal for the `curl` inference checks below; stop the port-forward
with `Ctrl-C` when finished.

Replace `<inferenceservice-name>` with the actual `NAME` from the first
command; do not include the angle brackets. If the Service is not found, list
all Services and use the KServe-generated predictor Service shown there:

```bash
oc get svc -n aim-models
```

For approved OpenShift-native external access, use the parameterized
[`route.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/route.yaml)
after replacing the generated predictor Service name:

```bash
oc apply -f config/samples/openshift/route.yaml
oc get route -n aim-models
```

The sample Route is named `qwen3-32b-chat`. Use its host to verify external
access (change `ROUTE_NAME` if you changed the sample's metadata name):

```bash
ROUTE_NAME=qwen3-32b-chat
ROUTE_HOST="$(oc get route "$ROUTE_NAME" -n aim-models \
  -o jsonpath='{.spec.host}')"
curl --fail --silent --show-error "https://${ROUTE_HOST}/v1/models"
```

If the router certificate is not trusted by the client, configure that
certificate or its CA for `curl`; do not disable certificate verification for
production checks.

An OpenShift `Route` and an AIM-managed Gateway API `HTTPRoute` are separate
ingress paths. This sample manually creates a Route to the predictor Service
through the OpenShift router. Gateway API requires a supported controller and a
configured `Gateway`; enabling `spec.routing.enabled` and setting `gatewayRef`
on an AIMService or runtime config makes AIM Engine create the `HTTPRoute`.
Applying `route.yaml` does not configure Gateway API, and AIM routing does not
create an OpenShift Route. The KServe `enableGatewayApi: false` setting disables
KServe's own Gateway integration, not AIM Engine routing. Use one path for a
single external endpoint; both may coexist when intentionally needed. See
[Routing and Ingress](../guides/routing-and-ingress.md) for the AIM-managed
Gateway API procedure.

The Route example uses edge TLS termination. Production deployments must also
address authentication, authorization, rate limiting, network policy, and
certificate ownership.

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
| Cache PVC or download pod is Pending/Failed | StorageClass access modes, bound PV node affinity, PV availability, PVC/pod events, RWX support, hostPath ownership and SELinux labeling |
| Predictor is Pending | Selected profile, GPU capacity, cache binding, existing workloads |
| `ImagePullBackOff` | Registry reachability, pull secret, image name, catalog access |
| Route is unavailable | Route target Service, router status, TLS, network policy |
| AIMService remains Pending or Starting | Deployable profile availability, profile resource requirements, cache/download status |
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
