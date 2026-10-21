# OpenShift samples

These AMD-focused samples support the [OpenShift administration
guide](../../../docs/docs/admin/openshift.md). They are templates, not a
bundle to apply all at once. Follow the guide for prerequisites, installation,
SCC and RWX setup, networking, and verification.

The samples assume AIM Engine is installed in `aim-system` and application
resources use the `aim-models` namespace. If you change a namespace, update all
related manifests and commands consistently.

These samples track `main` and are not versioned in lockstep with the AIM
Engine Helm chart. Before using them with a different chart release, verify the
sample API fields, detector labels, and the detector SCC-grant script.

## Sample files

| File | Purpose and notes |
| --- | --- |
| `cert-manager-operator.yaml` | Installs the Red Hat cert-manager Operator from `redhat-operators` with an all-namespaces OperatorGroup. Create the `cert-manager-operator` project first. The sample follows the recommended installation mode for Operator v1.15 and later; use the guide for installation and compatibility details. |
| `kserve-values.yaml` | Helm values for the KServe resources chart. Pass this file to Helm as shown in the guide; do not apply it with `oc`. |
| `detector-scc-rolebinding.yaml` | Binds the built-in `privileged` SCC to the detector service account. Replace the service-account placeholder with the account used by the installed detector DaemonSet. This file grants SCC access but does not patch pod security contexts. |
| `grant-detector-scc.sh` | Release-specific workaround that finds the AMD GPU detector DaemonSet and grants its shared detector service account the built-in `privileged` SCC. It does not patch pod templates. Run only when the guide's checks and installed release require it and the cluster administrator approves. Do not also apply the RoleBinding sample. Export `AIM_ENGINE_RELEASE` or `AIM_SYSTEM_NAMESPACE` if using non-default values. |
| `aimruntimeconfig-rwx.yaml` | Optional, namespace-scoped cache StorageClass override for `aim-models`. Replace the placeholder and apply this before the AIMService only when an explicit override is needed. The guide explains how to select and verify an RWX-capable class. |
| `aimservice-mi355x.yaml` | AMD MI355X worked example: a cluster-scoped `AIMClusterModel` and a namespaced `AIMService` for Qwen3-32B, requesting one MI355X GPU. Update the model image and accelerator profile together when adapting this example to another supported AMD GPU. |
| `route.yaml` | Optional OpenShift Route template. Replace the target with the generated KServe predictor Service and apply only after that Service exists; see the guide's networking section. |
