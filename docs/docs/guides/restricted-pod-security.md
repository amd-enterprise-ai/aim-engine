# Restricted Pod Security

A namespace that enforces the Pod Security Standard `restricted` refuses inference pods without a security context. AIM images run as root by default, so the Pod must also select a non-root user. This guide shows the settings that let inference pods run in such a namespace.

To make a namespace enforce `restricted`, label it:

```bash
kubectl label namespace my-models pod-security.kubernetes.io/enforce=restricted
```

Pod Security admission checks a Pod only when the Pod is created. Pods that already run in the namespace continue to run.

Two fields set the security contexts of the inference pods: `spec.podSecurityContext` for the Pod and `spec.securityContext` for the inference container. [Security contexts](../concepts/runtime-config.md#security-contexts) describes the two fields, their precedence and the values that AIM Engine refuses. For the security limits of the roles that write these fields, see the [warning in Team RBAC](../admin/security.md#team-rbac-security-contexts).

## Security Contexts

Set `spec.podSecurityContext` and `spec.securityContext`, for example in the runtime config `default` of the namespace. AIM Engine uses the runtime config `default` for each service in the namespace that does not set `runtimeConfigName`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: my-models
spec:
  podSecurityContext:
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
    fsGroup: 1000
    supplementalGroups: [44, 110]   # host GIDs of video and render; see GPU Device Groups
    seccompProfile:
      type: RuntimeDefault
  securityContext:
    allowPrivilegeEscalation: false
    capabilities:
      drop: [ALL]
```

## HOME and USER

AIM images have no user entry for a non-root UID, so the inference container also needs `HOME` set to a writable directory and `USER` set to a name. Without `HOME`, vLLM tries to write its cache below `/`, and the container stops with `Permission denied: '/.cache'`. Without `USER`, PyTorch cannot find a user name for the UID, and the container stops with `getpwuid(): uid not found`.

Set the two variables in the runtime config `spec.env`, next to the two security fields. The runtime config `spec.env` reaches the inference container of every AIMService that uses the runtime config, with or without `spec.profile`:

```yaml
spec:
  env:
    - name: HOME
      value: /tmp
    - name: USER
      value: aim
```

A value for the same name in the AIMService `spec.env`, the AIMProfile `containerEnv` or the `AIMServiceTemplate` `env` wins over the runtime config value; see [Inference container env](../concepts/runtime-config.md#inference-container-env). To set the variables for one service only, use its `spec.env`. Unlike `spec.profileOverrides.containerEnv`, it doesn't create an overlay profile, so the model weights don't download again.

## AITER Kernels

The AIM runtime installs pre-built AITER kernels into the AITER package directory of the image, which belongs to root. Use an AIM image whose runtime links the kernels into a private `AITER_JIT_DIR` when that directory is read-only; at startup, such a runtime logs `Using AITER_JIT_DIR=...`. With an older image, for example `docker.io/amdenterpriseai/aim-meta-llama-llama-3-1-8b-instruct:0.11.1`, the runtime logs `Permission denied` warnings for the kernels, the server becomes ready, and the engine stops at the first inference request with `ModuleNotFoundError`.

## GPU Device Groups

GPU device access needs `supplementalGroups`. The AMD GPU device plugin gives the container `/dev/kfd` and `/dev/dri/renderD*` with the host group of `render` and mode 0660, and `/dev/dri/card*` with the host group of `video`. `fsGroup` does not change device files. If the Pod does not have these host GIDs in `supplementalGroups`, the GPU runtime cannot open the devices and reports that no GPU is available. The GIDs depend on the operating system of the node. To find them, run this command on a GPU node:

```bash
stat -c '%g %n' /dev/kfd /dev/dri/card* /dev/dri/renderD*
```

The values 44 (`video`) and 110 (`render`) in the example are from one Ubuntu node. They are not defaults.

## CA Init Container

If the runtime config sets `additionalCABundleRef`, the inference pods also get a CA init container. It runs as UID 1000 without privilege escalation and drops all capabilities, and the Pod `seccompProfile` applies to it.

## Limitations

Autoscaled services cannot run in a namespace that enforces `restricted`. For [autoscaling](scaling-and-autoscaling.md#autoscaling-with-keda), the inference Pod gets an OpenTelemetry collector sidecar container, `otc-container`. `spec.securityContext` applies only to the inference container, and you cannot set a security context for the sidecar. The sidecar does not set `allowPrivilegeEscalation: false` and does not drop all capabilities, so Pod Security admission refuses the Pod. Run autoscaled services in a namespace that enforces `baseline`.

## Troubleshooting

If a service stays in `Starting` and its condition `InferenceServicePodsReady` has the reason `NoPods`, it is possible that Pod Security admission refused the inference Pod. The status of the service does not show the Pod Security message. To see it, look at the `FailedCreate` events of the ReplicaSet:

```bash
kubectl get events -n my-models --field-selector reason=FailedCreate
```

A message such as `violates PodSecurity "restricted:latest": runAsNonRoot != true` names the missing field and the container. A frequent cause is an AIMService that sets its own `podSecurityContext` or `securityContext`: this value replaces the runtime config value as a whole, also when it is `{}`. If the message names the container `otc-container`, the service uses autoscaling; see [Limitations](#limitations).
