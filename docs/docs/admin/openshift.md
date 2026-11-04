# Red Hat OpenShift

This guide supplements the Kubernetes installation and deployment instructions
with OpenShift-specific requirements for AIM Engine.

It is written for OpenShift cluster administrators. The installation and
validation procedures are accelerator-agnostic. The worked example uses an AMD
Instinct MI300X GPU and the Qwen3-32B FP8 AIM image; replace those
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
```

If you already have the repository, use an up-to-date `main` checkout
containing `config/samples/openshift/` instead of cloning it again.

The samples currently track `main` and are not versioned in lockstep with the
AIM Engine Helm chart. Record the printed source revision with the chart version
and verify compatibility before using a different chart release; in particular,
check sample API fields, detector labels, and the detector SCC-grant script.

## Prerequisites

Separate the prerequisites that must already exist from the components installed
by this guide. Before starting, verify that the cluster provides:

- AMD GPU Operator, or an equivalent AMD device-plugin and runtime, with the
  GPU nodes discoverable to workloads; NFD is installed or configured as
  described below
- Git, Helm 3, and the `oc` CLI
- An externally managed RWX-capable StorageClass for model caching
- `jq` and `rg` for the diagnostic commands in this guide, or equivalent
  JSON/text inspection tools
- Cluster-admin access for installing the operators and cluster-scoped charts
- Registry access that allows OpenShift nodes to pull the AIM Engine and model
  images

The setup in this guide then proceeds in this order:

1. NFD, if it is not already installed
2. cert-manager v1.16 or later
3. KServe v0.16.1 or later in Standard deployment mode (the example installs
   v0.21.0)
4. AIM Engine

The AMD GPU Operator and RWX storage are intentionally prerequisites rather
than installation steps in this guide. NFD is included below because AIM's
accelerator detector publishes labels through NFD; skip that section when the
cluster already has a supported NFD installation. KServe and cert-manager are
listed here as AIM Engine prerequisites, but their installation procedures are
included below so this document can be followed from a prepared OpenShift
cluster.

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

Use separate projects for the AIM Engine control plane and model-serving
workloads. If either project already exists, skip its `oc new-project`
command:

```bash
oc new-project aim-system
oc new-project aim-models
```

Keep AIM Engine control-plane resources in the installation namespace, usually
`aim-system`. Application users should receive namespace-scoped permissions for
their `AIMService` resources rather than cluster-admin access.

## Registry access

This guide assumes cluster administrators have configured registry access so
OpenShift nodes can pull the AIM Engine and selected model container images. For
private registries or `ImagePullBackOff`, see [Private
Registries](../guides/private-registries.md).

## Node Feature Discovery

AIM's accelerator detector writes normalized hardware features for NFD to
publish as node labels. If the cluster does not already run the Red Hat NFD
Operator, install it before AIM Engine. NFD supports a single-namespace
OperatorGroup here; an empty all-namespaces OperatorGroup is not valid for this
operator.

Check the catalog and create the operator namespace, OperatorGroup, and
Subscription. The `openshift-*` prefix is reserved, so create this namespace
with `oc create namespace`. If it already exists, skip that command:

```bash
oc get packagemanifest nfd -n openshift-marketplace \
  -o jsonpath='{.status.defaultChannel}{"\n"}'

oc create namespace openshift-nfd

oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-nfd
  namespace: openshift-nfd
spec:
  targetNamespaces:
    - openshift-nfd
EOF

oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: nfd
  namespace: openshift-nfd
spec:
  channel: stable
  installPlanApproval: Automatic
  name: nfd
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
```

Wait for the CSV, then create the catalog-provided `NodeFeatureDiscovery`
instance. Extracting the example keeps the worker configuration aligned with
the installed Red Hat Operator version:

```bash
NFD_CSV=""
for _ in $(seq 60); do
  NFD_CSV="$(oc get csv -n openshift-nfd -o json 2>/dev/null |
    jq -r '.items[] | select(.spec.displayName == "Node Feature Discovery Operator") | .metadata.name' |
    head -n 1)"
  [[ -n "$NFD_CSV" ]] && break
  sleep 10
done
if [[ -z "$NFD_CSV" ]]; then
  echo "NFD CSV did not appear in openshift-nfd" >&2
  exit 1
fi

oc wait --for=jsonpath='{.status.phase}'=Succeeded \
  -n openshift-nfd "csv/${NFD_CSV}" --timeout=10m

NFD_EXAMPLE="$(oc get csv -n openshift-nfd "$NFD_CSV" \
  -o jsonpath='{.metadata.annotations.alm-examples}')"
printf '%s\n' "$NFD_EXAMPLE" \
  | jq -c '[.[] | select(.kind == "NodeFeatureDiscovery")][0]' \
  | oc apply -f -

oc rollout status deployment/nfd-master -n openshift-nfd --timeout=5m
oc rollout status daemonset/nfd-worker -n openshift-nfd --timeout=5m
oc get nodefeaturediscovery -n openshift-nfd
```

If NFD is already managed by the platform, verify that its worker is healthy
and that the AIM `feature.node.kubernetes.io/aim-accelerator.*` labels appear
after the detector is installed.

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

Wait for the OLM CSV and all cert-manager deployments before continuing.

OLM creates the ClusterServiceVersion (CSV) some time after the Subscription is
accepted. `oc wait` with a label selector does not wait for an object that does
not exist yet; it returns immediately with `no matching resources found`. Wait
for the CSV to appear first, then wait for its phase:

```bash
CSV_SELECTOR='operators.coreos.com/openshift-cert-manager-operator.cert-manager-operator'

for _ in $(seq 60); do
  oc get csv -n cert-manager-operator -l "$CSV_SELECTOR" \
    -o name | grep -q . && break
  sleep 10
done

oc wait \
  --for=jsonpath='{.status.phase}'=Succeeded \
  -n cert-manager-operator \
  csv -l "$CSV_SELECTOR" \
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

Install KServe v0.21.0 in Standard mode. AIM Engine supports KServe v0.16.1 or
later; this example pins v0.21.0 and uses its separate CRD and resource charts.
KServe changed its Helm chart layout in v0.17, so do not use the older single
`kserve` chart command. Consult the linked KServe Configuration guide for the
values rationale only; its installation commands target v0.16.1. The
repository sample
[`kserve-values.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/kserve-values.yaml)
contains the AIM-compatible settings. For the chart-layout change, see the
[KServe v0.17 release notes](https://kserve.github.io/website/blog/kserve-0.17-release)
and the [KServe v0.21.0 release](https://github.com/kserve/kserve/releases/tag/v0.21.0).

For example:

```bash
helm upgrade --install kserve-crd \
  oci://ghcr.io/kserve/charts/kserve-crd \
  --namespace kserve --create-namespace --version v0.21.0 \
  --wait --timeout 5m
helm upgrade --install kserve-resources \
  oci://ghcr.io/kserve/charts/kserve-resources \
  --namespace kserve --version v0.21.0 \
  -f config/samples/openshift/kserve-values.yaml \
  --wait --timeout 5m
```

If the resource release remains `pending-install` while cert-manager is
issuing the KServe webhook certificate, wait for the cert-manager deployments
to become ready and rerun the same `kserve-resources` command.

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
Do not add `--force-conflicts` by default. Server-side apply can report a
conflict when another field manager owns a field whose value differs from this
manifest; a prior client-side apply alone does not make force necessary. Inspect
the conflicting fields and their owners before resolving the conflict. Forcing
the apply overrides conflicting values and transfers field ownership; see the
[Kubernetes Server-Side Apply documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/).

## Configure Security Context Constraints

Run this section immediately after installing AIM Engine and before treating
the detector DaemonSets as ready. The AIM chart must be installed first so
that its detector DaemonSets and service account exist; the initial Helm
installation above intentionally omits `--wait` for this reason.

SCC admission is the most common OpenShift-specific installation issue. AIM's
accelerator detector needs host access to inspect hardware and publish NFD
labels. The GPU detector pod template requests a privileged container and these
hostPath volumes, which is what an administrator must weigh when reviewing the
exception:

- `/dev/kfd` and `/dev/dri` — AMD GPU device nodes, mounted read-only
- `/etc/kubernetes/node-feature-discovery/features.d/` — where the detector
  writes the NFD feature files that become node labels

Determine the detector service account from the installed DaemonSet:

```bash
oc get daemonset -n aim-system \
  -o custom-columns=NAME:.metadata.name,SERVICE_ACCOUNT:.spec.template.spec.serviceAccountName
```

Review the required SCC before granting it:

```bash
export AIM_ENGINE_RELEASE="${AIM_ENGINE_RELEASE:-aim-engine}"
DETECTOR_SELECTOR="app.kubernetes.io/instance=${AIM_ENGINE_RELEASE},app.kubernetes.io/managed-by=Helm,app.kubernetes.io/component=gpu-detection"

oc get daemonset -n aim-system \
  -l "$DETECTOR_SELECTOR"

DETECTOR_DS="$(oc get daemonset -n aim-system \
  -l "$DETECTOR_SELECTOR" -o name | head -n 1)"
if [[ -z "$DETECTOR_DS" ]]; then
  echo "No AIM Engine detector DaemonSet found in aim-system" >&2
else
  DETECTOR_SA="$(oc get "$DETECTOR_DS" -n aim-system \
    -o jsonpath='{.spec.template.spec.serviceAccountName}')"
  oc get "$DETECTOR_DS" -n aim-system -o yaml \
    > /tmp/aim-detector-daemonset.yaml

  oc adm policy scc-review \
    -z "$DETECTOR_SA" \
    -n aim-system \
    -f /tmp/aim-detector-daemonset.yaml
fi
```

Keep `DETECTOR_SA` set in this shell; the cleanup section uses it to revoke the
SCC grant.

The review reports which SCC can admit the AMD GPU detector pod template. The
chart's detector DaemonSets share a service account, so granting an SCC to this
account also makes that SCC available to the other detector pods using it. The
temporary YAML file is generated from the installed DaemonSet; it is not a
repository file.

If the command prints only the column header and no row, no SCC currently
admits the detector pod template. This is expected before the workaround below
has granted the detector service account access to the required SCC.

The tested workaround for the release used here requires the built-in
`privileged` SCC. This is a high-privilege exception, not a least-privilege
configuration. The grant allows pods using this service account to request the
privileged SCC, so keep the service account dedicated to the detector and
review who can create pods that use it.

Review the script before running it and obtain approval from the cluster
administrator. It grants the built-in `privileged` SCC to the detector service
account; it does not patch any pod template. The chart already marks the AMD
GPU detector container privileged, so the SCC grant is the required admission
exception. Do not run this workaround on clusters whose policy prohibits
privileged containers:

```bash
bash config/samples/openshift/grant-detector-scc.sh

oc rollout status deployment/aim-engine-controller-manager \
  -n aim-system --timeout=120s
```

The script selects the AMD GPU DaemonSet by its Helm release labels and grants
the SCC to its service account. The
[`detector-scc-rolebinding.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/detector-scc-rolebinding.yaml)
sample grants access to the same built-in SCC; use either the script or the
RoleBinding, not both. Neither sample defines a custom SCC. This is a
high-privilege exception, not a least-privilege configuration. If policy forbids
privileged containers, stop and use a detector configuration approved for that
policy. Recheck the installed release before reusing the workaround, since a
later release may no longer need it.

On RHCOS nodes, SELinux can still deny the CPU detector's root init container
when it tries to `chmod` NFD's hostPath. If the detector logs report permission
denied for `/etc/kubernetes/node-feature-discovery/features.d/`, label that
directory on every disposable test node selected by the detector:

```bash
for NODE in $(oc get nodes -l feature.node.kubernetes.io/amd-gpu=true \
  -o name); do
  oc debug "$NODE" -- chroot /host sh -c \
    'mkdir -p /etc/kubernetes/node-feature-discovery/features.d &&
     chcon -R -t container_file_t /etc/kubernetes/node-feature-discovery/features.d'
done
```

This is a hostPath workaround for disposable test nodes. Persist the SELinux
label with the platform's node configuration mechanism when nodes may be
reprovisioned; do not treat `chcon` alone as production node configuration.

Check the SCC used by the AMD GPU detector. The GPU component selector
avoids selecting CPU or other accelerator detector pods. First inspect
scheduling:

```bash
oc get daemonset -n aim-system \
  -l app.kubernetes.io/component=gpu-detection -o wide

DETECTOR_POD="$(oc get pod -n aim-system \
  -l app.kubernetes.io/component=gpu-detection -o name | head -n 1)"

if [[ -n "$DETECTOR_POD" ]]; then
  DETECTOR_POD="${DETECTOR_POD#pod/}"
  oc get pod -n aim-system "$DETECTOR_POD" \
    -o jsonpath='{.metadata.annotations.openshift\.io/scc}{"\n"}'
  oc describe pod -n aim-system "$DETECTOR_POD"
else
  echo "No AMD GPU detector pod is scheduled."
fi
```

If no AMD GPU detector pod is scheduled, there is no pod SCC annotation to
check yet. Confirm that an AMD GPU node has the required feature label and
that the detector DaemonSet's desired count is nonzero.

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
when the device plugin reports `amd.com/gpu` capacity. Stop and have the
platform integration provide the label. For a disposable single-node test
only, the label can be applied explicitly:

```bash
NODE="$(oc get nodes -o name | head -n 1)"
oc label "$NODE" feature.node.kubernetes.io/amd-gpu=true --overwrite
```

Do not use this manual label as a substitute for the platform's node-labeling
configuration on reprovisioned or production nodes.

After the GPU detector has a scheduled pod, inspect its logs:

```bash
GPU_DETECTOR_DS="$(oc get daemonset -n aim-system \
  -l app.kubernetes.io/component=gpu-detection -o name | head -n 1)"
oc logs -n aim-system "$GPU_DETECTOR_DS"
```

The DaemonSet's generated name depends on the Helm release and chart names, so
select it by label rather than by a literal name.

For the MI300X worked example, an expected detector message resembles:

```text
Detected: type=GPU model=MI300X count=8
Partition schemes: SPX-NPS1=8 (+default sentinel)
```

For another supported AMD accelerator, the `model` and `count` values should
match the hardware on the node; partition output depends on the accelerator.

The chart installs separate CPU and GPU detector DaemonSets. Pulling the CPU
detector image is expected: it publishes CPU/vendor labels used during profile
matching. It is not the model-serving image and does not mean the service was
scheduled on a CPU-only node.

If the detector log identifies a GPU but the normalized AIM labels are absent,
inspect the DaemonSet permissions, NFD worker logs, and detector logs on every
node. Do not assume that one healthy detector pod makes every node schedulable.

## Configure RWX model caching

AIM Engine requires an externally managed StorageClass that supports
`ReadWriteMany` (RWX) for model caching. Provisioning and operating that
StorageClass are outside this guide. Select and configure the approved class
before applying the `AIMService`. Ensure the target application namespace
exists first:

```bash
oc get namespace aim-models >/dev/null 2>&1 || oc new-project aim-models
oc get storageclass \
  -o custom-columns='NAME:.metadata.name,PROVISIONER:.provisioner,DEFAULT:.metadata.annotations.storageclass\.kubernetes\.io/is-default-class'

if [[ -z "${RWX_STORAGE_CLASS:-}" ]]; then
  RWX_PROBE_PVC=aim-rwx-storage-probe
  RWX_STORAGE_CLASS=""
  oc delete pvc "${RWX_PROBE_PVC}" -n aim-models \
    --ignore-not-found --wait=true --timeout=60s >/dev/null

  while IFS= read -r CANDIDATE; do
    [[ -z "${CANDIDATE}" ]] && continue
    oc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${RWX_PROBE_PVC}
  namespace: aim-models
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Mi
  storageClassName: ${CANDIDATE}
EOF
    if oc wait --for=jsonpath='{.status.phase}'=Bound \
      "pvc/${RWX_PROBE_PVC}" -n aim-models --timeout=60s >/dev/null 2>&1; then
      RWX_STORAGE_CLASS="${CANDIDATE}"
      break
    fi
    oc delete pvc "${RWX_PROBE_PVC}" -n aim-models \
      --ignore-not-found --wait=true --timeout=60s >/dev/null
  done < <(oc get storageclass -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | sort)

  oc delete pvc "${RWX_PROBE_PVC}" -n aim-models \
    --ignore-not-found --wait=true --timeout=60s >/dev/null
  if [[ -z "${RWX_STORAGE_CLASS}" ]]; then
    echo "No installed StorageClass provisioned a ReadWriteMany PVC" >&2
    exit 1
  fi
else
  oc get storageclass "${RWX_STORAGE_CLASS}" >/dev/null
fi
export RWX_STORAGE_CLASS
echo "Using RWX StorageClass: ${RWX_STORAGE_CLASS}"
```

The probe tests actual `ReadWriteMany` provisioning instead of inferring support
from the StorageClass name or provisioner. Export `RWX_STORAGE_CLASS` before
this block when cluster policy requires a particular approved class; that class
is validated for existence and the probe is skipped.

If the approved class is not the cluster default, replace
`replace-me-rwx-storage-class` in the namespace-scoped
[`aimruntimeconfig-rwx.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/aimruntimeconfig-rwx.yaml)
with `${RWX_STORAGE_CLASS}`, then apply it:

```bash
sed "s/replace-me-rwx-storage-class/${RWX_STORAGE_CLASS}/" \
  config/samples/openshift/aimruntimeconfig-rwx.yaml \
  | oc apply -f -
oc get aimruntimeconfig default -n aim-models -o yaml
```

The runtime configuration affects only `aim-models`. Omit it when the approved
RWX class is already the default. Verify the externally managed storage by
checking that the cache PVC created by AIM is `Bound` with access mode `RWX`.

## Deploy a model and service

The sample
[`aimservice-mi300x.yaml`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/config/samples/openshift/aimservice-mi300x.yaml)
contains both an `AIMClusterModel` and an `AIMService`. Both use
`aim.eai.amd.com/v1alpha2`, and the service carries the
`aim.eai.amd.com/reconciler-pipeline: profile` annotation, which selects the
profile-based reconciler rather than the legacy template-based one. Applying
the file once is valid; the service remains pending until profile discovery
produces a deployable profile.

On this OpenShift release, the artifact check-size/download Jobs and the
generated predictor use the `aim-models/default` service account with UID 1000.
The default restricted SCC rejects that UID. Review the policy with the
cluster administrator, then grant `anyuid` only to this namespace-local account
for the release being tested before applying the model:

```bash
oc adm policy add-scc-to-user anyuid -z default -n aim-models
oc get rolebinding system:openshift:scc:anyuid -n aim-models
```

Use an approved custom SCC instead when cluster policy forbids `anyuid`. Check
the resulting pod annotation and do not grant the SCC to a shared service
account:

```bash
oc get pod -n aim-models \
  -o 'custom-columns=NAME:.metadata.name,SCC:.metadata.annotations.openshift\.io/scc'
```

For the MI300X hardware used in this test, apply the sample. The explicit
`precision` makes the profile selection deterministic and matches the image's
MI300X latency deployment recommendation:

```bash
oc apply -f config/samples/openshift/aimservice-mi300x.yaml
oc get aimclustermodel qwen3-32b-mi300x -o yaml
```

Wait for at least one deployable profile, then inspect the service:

```bash
oc get aimservice -n aim-models
oc get aimservice -n aim-models qwen3-32b-chat -o yaml
```

The example requests one GPU and selects a latency profile. For another
supported AMD accelerator or deployment goal, update the model image,
`acceleratorModel`, `acceleratorCount`, `metric`, and `precision` together so
they match a deployable profile provided by the AIM Engine release.

### Speed up large FP8 model startup

Large FP8 checkpoints can spend most of startup processing safetensors weights
on the CPU before the weights are transferred to the AMD GPU. For the Qwen3-32B
FP8 example, configure bounded CPU parallelism before waiting for the predictor
to become ready:

```bash
oc patch aimservice -n aim-models qwen3-32b-chat --type=merge -p '{
  "spec": {
    "profileOverrides": {
      "engineEnv": {
        "OMP_NUM_THREADS": "8"
      }
    },
    "resources": {
      "requests": {
        "cpu": "8"
      }
    }
  }
}'
```

`OMP_NUM_THREADS` and the CPU request should be kept aligned. Start with eight
threads for this model and adjust only after measuring the node's available CPU
and steady-state serving behavior. In this MI300X test, the change reduced
seven-shard weight loading from roughly 90 minutes to about 20 seconds. The
service-owned overlay survives profile discovery and avoids editing a generated
`AIMClusterProfile` directly. vLLM documents that weight loading benefits from
bounded CPU parallelism and that externally set `OMP_NUM_THREADS` is honored;
see the [vLLM torch utilities](https://docs.vllm.ai/en/latest/api/vllm/utils/torch_utils/).

Verify the rollout and inspect the weight-loading log before running inference:

```bash
oc get aimservice -n aim-models qwen3-32b-chat
oc get inferenceservice -n aim-models
oc get pods -n aim-models -o wide
PREDICTOR_POD="$(oc get pod -n aim-models \
  -l serving.kserve.io/inferenceservice \
  -o jsonpath='{.items[0].metadata.name}')"
oc logs -n aim-models "${PREDICTOR_POD}" | rg \
  'Loading safetensors checkpoint shards|Application startup complete'
```

## Networking

For private testing, use a temporary port-forward to the KServe predictor:

```bash
oc get inferenceservice -n aim-models
```

KServe's Standard deployment normally creates the predictor Service by
appending `-predictor` to the ready `InferenceService` name. The following
selects the first `Ready=True` service automatically; export `ISVC_NAME` first
when choosing among multiple ready services:

```bash
if [[ -z "${ISVC_NAME:-}" ]]; then
  ISVC_NAME="$(oc get inferenceservice -n aim-models -o json \
    | jq -r '.items[] | select(any(.status.conditions[]?; .type == "Ready" and .status == "True")) | .metadata.name' \
    | head -n 1)"
fi
if [[ -z "${ISVC_NAME}" ]]; then
  echo "No ready InferenceService found in aim-models" >&2
  exit 1
fi
export ISVC_NAME
export PREDICTOR_SERVICE="${ISVC_NAME}-predictor"
oc get svc -n aim-models "${PREDICTOR_SERVICE}"
oc port-forward -n aim-models "svc/${PREDICTOR_SERVICE}" 18000:80
```

The port-forward command stays in the foreground. Leave it running and use a
second terminal for the `curl` inference checks below; stop the port-forward
with `Ctrl-C` when finished.

If the Service is not found, list all Services and use the KServe-generated
predictor Service shown there:

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

Discover the exact served model identifier and use it for inference:

```bash
MODEL_RESPONSE="$(curl --fail --silent --show-error \
  http://127.0.0.1:18000/v1/models)" || exit 1
printf '%s\n' "${MODEL_RESPONSE}"
MODEL_ID="$(printf '%s' "${MODEL_RESPONSE}" \
  | jq -r '.data[0].id // empty')"
if [[ -z "${MODEL_ID}" ]]; then
  echo "No served model identifier found" >&2
  exit 1
fi

curl --fail --silent --show-error \
  http://127.0.0.1:18000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --arg model "${MODEL_ID}" '{
    model: $model,
    messages: [{role: "user", content: "Say hello in one word."}],
    max_tokens: 32,
    chat_template_kwargs: {enable_thinking: false}
  }')"
```

## Troubleshooting

| Symptom | OpenShift checks |
| --- | --- |
| Detector pod is rejected | `oc describe pod`, SCC review, service-account binding, hostPath/device permissions |
| GPU is detected but not schedulable | `amd.com/gpu` allocatable capacity, NFD labels, AIM detector logs on every node |
| Cache PVC or download pod is Pending/Failed | Verify the externally managed StorageClass supports RWX and consult the storage administrator |
| Predictor is Pending | Selected profile, GPU capacity, cache binding, existing workloads |
| Predictor startup is slow | Check the predictor CPU request, `OMP_NUM_THREADS`, model-cache state, and safetensors-loading logs |
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
oc delete aimclustermodel qwen3-32b-mi300x --ignore-not-found
```

If you applied the SCC workaround, revoke it when the detector is no longer
needed, before uninstalling AIM Engine. Neither `helm uninstall` nor deleting
the AIM resources removes the grant. Keep the `DETECTOR_SA` value from the SCC
section; if you are in a new shell, retrieve it from the still-installed GPU
detector DaemonSet before uninstalling the release. Leaving the grant in place
lets anyone who can create pods using that service account request the
privileged SCC:

```bash
oc adm policy remove-scc-from-user privileged \
  -z "$DETECTOR_SA" -n aim-system
```

If you granted `anyuid` to the model namespace for this test, remove that
namespace-local grant after the AIM workloads have stopped:

```bash
oc adm policy remove-scc-from-user anyuid -z default -n aim-models
```

If you applied `detector-scc-rolebinding.yaml` instead of the script, remove
that exact RoleBinding:

```bash
oc delete rolebinding aim-detector-privileged -n aim-system
```

The current SCC-grant script does not patch DaemonSets. If you ran an older
revision of the sample script that patched pod templates and are keeping the
release, recreate the affected DaemonSets from the Helm chart to remove those
manual pod-template changes.

If you created `AIMRuntimeConfig/default` only for this test, remove it after
the AIM resources are gone. If you installed NFD only for this test, remove
its `NodeFeatureDiscovery` and Operator resources separately; do not remove
shared KServe, cert-manager, AMD Operators, AIM CRDs, validation jobs, or
unrelated GPU workloads without confirming ownership and active users.

```bash
oc delete aimruntimeconfig default -n aim-models --ignore-not-found
```
