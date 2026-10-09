# OpenShift samples

These samples supplement the AIM Engine Kubernetes examples with OpenShift
platform resources. They are templates, not a single cluster-wide bundle.

## Before applying

1. Install and verify the AMD GPU Operator, KServe, cert-manager, and AIM
   Engine.
2. Create or select an application project. The examples use `aim-models`.
3. Confirm that the cluster provides an approved RWX-capable StorageClass.
4. Replace every value marked `REPLACE_ME` or `replace-me-*`.
5. Review SCC and Route changes with the OpenShift platform administrator.

Do not commit kubeconfigs, passwords, pull secrets, tokens, node names, or
cluster-specific hostPath paths.

## Files

- `detector-scc-rolebinding.yaml` binds the privileged SCC to one detector
  service account after AIM Engine is installed. Replace the service-account
  name before applying it.
- `patch-detectors.sh` applies the tested post-install detector security-context
  workaround. Review it before use and run it only when the installed release
  requires it.
- `aimservice-mi355x.yaml` deploys a Qwen3-32B model and an AIMService that
  selects one MI355X GPU. Change the image tag and namespace as needed.
- `kserve-values.yaml` contains the KServe values required by AIM Engine.
- `route.yaml` is a Route template. Replace the generated predictor Service
  name after the InferenceService exists.

## Apply order

```bash
cd config/samples/openshift
oc new-project aim-models
oc get daemonset -n aim-system \
  -o custom-columns=NAME:.metadata.name,SERVICE_ACCOUNT:.spec.template.spec.serviceAccountName
# Replace the detector service-account placeholder in the SCC sample.
oc apply -f detector-scc-rolebinding.yaml
bash patch-detectors.sh
oc apply -f aimservice-mi355x.yaml
oc get aimclustermodel,aimservice -n aim-models
oc get inferenceservice -n aim-models
```

The `AIMClusterModel` is cluster-scoped. The `AIMService` is namespace-scoped.
Wait for profile discovery and cache readiness before exposing a Route.

Configure the RWX StorageClass through a namespace `AIMRuntimeConfig/default`
or a cluster `AIMClusterRuntimeConfig/default` according to the platform's
storage policy. Prefer the namespace-scoped resource for application teams.
The samples intentionally do not select a StorageClass by name.

## Verify SCC admission

```bash
oc get daemonset -n aim-system \
  -o custom-columns=NAME:.metadata.name,SERVICE_ACCOUNT:.spec.template.spec.serviceAccountName
oc auth can-i use scc/privileged \
  --as=system:serviceaccount:aim-system:REPLACE_ME
oc get pod -n aim-system <detector-pod> \
  -o jsonpath='{.metadata.annotations.openshift\\.io/scc}{"\\n"}'
```

The detector service account name can vary with the Helm release name. Derive
it from the installed DaemonSet instead of assuming a fixed name.

## Verify inference

```bash
oc get aimservice,inferenceservice,pods,pvc -n aim-models -o wide
oc port-forward -n aim-models svc/REPLACE_ME 18000:80
curl --fail --silent --show-error http://127.0.0.1:18000/v1/models
```
