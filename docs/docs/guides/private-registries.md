# Private Registries

This guide covers configuring authentication for private container registries, HuggingFace Hub, and S3-compatible storage.

:::{important}
Container image pulls are a Kubernetes platform responsibility

AIM Engine configures image references and Kubernetes pull secrets for
official or privately published AIM images. Kubernetes performs the actual
image pull before the workload—or any init container—can start. The platform
administrator must therefore ensure that the referenced registry is reachable,
make pull credentials available through the appropriate namespaces or
ServiceAccounts, and configure each node/container runtime to trust the
registry certificate.

For air-gapped deployments, mirror every required AIM operator, downloader,
runtime, and model image into a registry available to the cluster and configure
AIM Engine to use those mirrored references. Provisioning and operating a
general-purpose container registry is outside the scope of AIM Engine and this
guide; follow the documentation for the deployed Kubernetes platform and
container runtime.

Consequently, `additionalCABundleRef` cannot affect container image pulls; it
configures outbound TLS only after the workload has started.
:::

:::{admonition} v1alpha2
:class: note

`AIMService` examples use `aim.eai.amd.com/v1alpha2`. `AIMRuntimeConfig` / `AIMClusterRuntimeConfig` remain `aim.eai.amd.com/v1alpha1` resources — both pipelines consume them unchanged. `spec.imagePullSecrets`, `spec.serviceAccountName`, and the runtime-config env fields are identical regardless of pipeline.
:::
## Container Image Pull Secrets

### Artifact-downloader image

The operator uses a release-matched artifact-downloader image in model download
and size-check Jobs and as the custom-CA init container for artifact and
inference workloads. For an air-gapped installation, mirror the official image
into the internal container registry and set the Helm install-time default:

```bash
helm upgrade --install aim-engine oci://registry.internal/aim-engine-chart \
  --version <version> \
  --namespace aim-system \
  --set manager.artifactDownloaderImage=registry.internal/aim-artifact-downloader:vX.Y.Z
```

Use the tag or digest matching the AIM Engine release. This setting is
preferred when changing only the registry location because it applies to every
artifact workload, including typed S3 workloads. It also ensures trust init
containers do not retain a reference to the public `docker.io` image.

The downloader image runs in workload namespaces. Ensure the Kubernetes
platform can pull the mirrored image there; `manager.imagePullSecrets`
configures the operator pod and is not a cluster-wide registry credential.

An administrator can instead select a downloader image through RuntimeConfig:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  artifact:
    modelDownloadImage: registry.internal/aim-artifact-downloader:vX.Y.Z
```

This is useful when different RuntimeConfigs intentionally select different
downloader implementations. `AIMRuntimeConfig` supports the same field, and
`AIMArtifact.spec.modelDownloadImage` provides a per-artifact override. Direct
S3 artifacts using typed S3 configuration intentionally ignore those
RuntimeConfig and artifact overrides so administrator-managed credentials are
not exposed to arbitrary images; they use the Helm/install-time default.

### Per-Service Secrets

Provide image pull secrets directly on the service:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    name: qwen-qwen3-32b
  imagePullSecrets:
    - name: my-registry-creds
```

The secret must exist in the same namespace as the service.

### Model discovery secrets

Secret references are resolved in the namespace where the discovery or
inference workload runs:

| Resource or workload | Secret namespace |
|---|---|
| `AIMModel` image discovery | The model's namespace |
| `AIMClusterModel` image discovery | The operator namespace |
| `AIMClusterModelSource` registry scan | The operator namespace |
| `AIMService` inference pod | The service's namespace |

For a private `AIMClusterModel`, create the discovery pull secret in the
operator namespace. A service consuming one of the resulting cluster profiles
also needs usable credentials in its own namespace. You can create a secret
with the same name in every consumer namespace, or set a namespace-local secret
through `AIMService.spec.imagePullSecrets`.

For `AIMClusterModelSource` pulling from private container registries, the same
operator-namespace rule applies:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: private-models
spec:
  registry: ghcr.io
  imagePullSecrets:
    - name: ghcr-pull-secret
  images:
    - "my-org/private-model:1.2.3"
```

## HuggingFace Authentication

For models sourced from private HuggingFace repositories (`hf://` URLs), provide a token via environment variables in the runtime configuration:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-credentials
          key: token
```

Create the secret:

```bash
kubectl create secret generic hf-credentials \
  --from-literal=token=hf_your_token_here \
  -n ml-team
```

### Self-hosted Hugging Face mirrors

AIM Engine can use a self-hosted endpoint that implements the Hugging Face Hub
HTTP API. This may be a synchronized mirror or a pull-through cache:

- A **mirror** is populated independently and can operate without upstream
  network access.
- A **pull-through cache** fetches missing content from an upstream Hub. In a
  disconnected environment it must be pre-populated; otherwise a cache miss
  cannot succeed.

AIM Engine does not manage or populate the endpoint. From the workload's point
of view it is another Hugging Face Hub: `HF_ENDPOINT` selects it, `HF_TOKEN`
optionally supplies its bearer credential, and the endpoint must serve all
model metadata and files required by the installed Hugging Face and inference
client versions.

Configure endpoint and credential variables on the profile for inference and
artifact caching:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMProfile
metadata:
  name: internal-model
  namespace: ml-team
spec:
  # Other model, image, and accelerator fields are omitted.
  containerEnv:
    - name: HF_ENDPOINT
      value: https://huggingface.example.com
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: hf-credentials
          key: token
  caching:
    enabled: true
    env:
      - name: HF_ENDPOINT
        value: https://huggingface.example.com
      - name: HF_TOKEN
        valueFrom:
          secretKeyRef:
            name: hf-credentials
            key: token
      - name: AIM_DOWNLOADER_PROTOCOL
        value: HTTP
```

Most compatible mirrors implement the standard HTTP API but not the XET or
HF_TRANSFER data paths. Use `AIM_DOWNLOADER_PROTOCOL=HTTP` unless the endpoint
explicitly documents support for another protocol.

For any endpoint signed by an internal certificate authority, put the public
PEM CA certificates in a ConfigMap in every workload namespace:

```bash
kubectl create configmap company-ca \
  --from-file=ca.crt=/path/to/company-ca-bundle.pem \
  -n ml-team
```

Select the bundle once on the namespace RuntimeConfig:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  additionalCABundleRef:
    name: company-ca
    key: ca.crt
```

The `default` RuntimeConfig applies when a service, cache, or artifact does not
name a different RuntimeConfig. A namespace `AIMRuntimeConfig` overrides an
`AIMClusterRuntimeConfig` with the same name.

When `AIMProfile.spec.caching.enabled` creates a profile-owned shared cache,
that cache resolves the `default` RuntimeConfig. Setting
`AIMService.spec.runtimeConfigName` to another name does not retarget the
profile-owned cache. Put namespace-wide trust in the `default` RuntimeConfig so
both the profile cache and services inherit it. Service-owned caches continue
to use the service's selected RuntimeConfig.

AIM Engine does not replace public trust with this ConfigMap. A pod init
container starts with the standard public roots from the resolved
artifact-downloader image, validates and appends the selected certificates,
then mounts the combined bundle into inference, artifact download, size-check,
and adapter staging containers. The init container uses the same resolved
downloader image as its workload path, including supported
`modelDownloadImage` overrides. A custom downloader image used with additional
trust must therefore contain `/combine-ca.sh`, Python 3, and a public root
bundle at `/etc/ssl/certs/ca-certificates.crt`.

Common TLS environment variables contain only the mounted file path; PEM
content is never transported through environment variables. Malformed
additions produce an init-container warning and are skipped without removing
public roots. Trust projection does not add or change pod-level security
settings such as `fsGroup`; the init container has its own non-root security
context.

ConfigMap references are namespace-local even when selected by an
`AIMClusterRuntimeConfig`. Create the same ConfigMap name and key in every
consuming namespace. A controller such as
[trust-manager](https://cert-manager.io/docs/trust/trust-manager/) can distribute
one organization bundle to those namespaces.

AIM Engine reads `additionalCABundleRef` directly from the API server while
reconciling a service or artifact. If the ConfigMap or selected key is missing
at that moment, AIM Engine omits the additional-CA volume and continues with
public roots. It does not watch the ConfigMap or hash its contents.

:::{note}
CA contents are a workload-creation snapshot. After rotating an additional or
S3-specific CA, recreate the affected workload so its trust init container
runs again. Delete an inference pod to have its owning workload replace it.
Delete unfinished artifact or adapter Jobs so the corresponding controller
recreates them:

```bash
kubectl delete pod -n <namespace> <inference-pod>
kubectl delete job -n <namespace> <unfinished-artifact-job>
kubectl get jobs -n <namespace> \
  -l aim.eai.amd.com/component=adapter-stage,aim.eai.amd.com/service=<service-name>
kubectl delete job -n <namespace> <unfinished-adapter-staging-job>
```

Ready artifacts are not downloaded again solely because a CA changed.
Restarting an inference pod does not restart adapter staging, and deleting the
AIMService is unnecessary.
:::

The inference container does not need the mirror endpoint or its CA when it is
guaranteed to start entirely from cached local content. That guarantee must
cover every runtime dependency—not only the tokenizer, but also model
configuration, remote code, and auxiliary files—and the runtime must not make
other startup requests to the endpoint. In that case, omit the mirror settings
from `containerEnv`.

Production CA material should normally come from your organization's PKI. For
test environments, OpenSSL's
[`req` documentation](https://docs.openssl.org/master/man1/openssl-req/)
describes its certificate-request and self-signed certificate facilities.

`additionalCABundleRef` is general workload trust. A typed
`artifact.s3.tls.caBundleRef` remains available when an S3 endpoint requires a
different CA; S3 Jobs then combine public roots, the RuntimeConfig additional
bundle, and the S3-specific bundle.

Model discovery uses separate trust paths. Legacy v1alpha1 image metadata
inspection connects to the registry from the operator process; custom CA trust
for that deprecated discovery path is not supported by
`additionalCABundleRef`. Native v1alpha2 discovery starts a Job from the model
image and walks the profiles embedded in that image. The platform
kubelet/container runtime pulls the image before an init container could run,
so configure private-container-registry credentials as described above and
install any private container registry CA in the platform's
node/container-runtime trust configuration.

Local-only adapter sync/reap also does not use this workload trust mechanism.

`HF_ENDPOINT` affects only Hugging Face clients. Container image pulls and
`s3://` model sources use their own registry and S3 configuration.

## S3-Compatible Storage

Model artifacts with an `s3://` source URI are downloaded by a boto3-backed
client (Signature V4). Validated end-to-end against MinIO. Other S3-v4 backends
(AWS S3, Cloudflare R2, Backblaze B2, GCS, Ceph RGW) use the same boto3 path
and are expected to work with the corresponding endpoint and credentials.

Prefer the typed S3 configuration. It keeps credentials out of Pod environment
variables, mounts custom trust roots consistently into size-check and download
Jobs, and applies the connection only to `s3://` artifacts:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: ml-team
spec:
  artifact:
    s3:
      endpoint: https://s3.example.com
      region: us-east-1
      addressingStyle: path
      auth:
        mode: static
        credentialsSecretRef:
          name: s3-credentials
          accessKeyIdKey: accessKeyId
          secretAccessKeyKey: secretAccessKey
      tls:
        caBundleRef:
          kind: ConfigMap
          name: s3-ca
          key: ca.crt
```

Both references are namespace-local. The Secret values are mounted as files and
never copied into the Job specification:

```bash
kubectl create secret generic s3-credentials \
  --from-literal=accessKeyId='ACCESS_KEY' \
  --from-literal=secretAccessKey='SECRET_KEY' \
  -n ml-team

kubectl create configmap s3-ca \
  --from-file=ca.crt=/path/to/internal-ca.pem \
  -n ml-team
```

The namespace-level S3 object replaces the cluster-level object as one complete
connection. Endpoint, credentials, and CA settings are never field-merged
across those trust boundaries.

### How S3 credentials are resolved

Typed `auth.mode` selects one of three modes:

| Mode | Behaviour |
| --- | --- |
| `static` | Mounts the referenced Secret and signs with those keys, plus the optional session token. |
| `anonymous` | Sends unsigned requests. Use only for public buckets. |
| `chain` | Uses boto3's standard provider chain: web identity (IRSA), EKS Pod Identity, ECS task role, EC2 instance profile, shared profile, or credential process. This is the default. |

Existing environment-based configuration remains supported. When no typed
connection is present, setting both `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` selects static credentials,
`AIM_S3_ANONYMOUS=true` selects anonymous access, and leaving both keys absent
selects the credential chain. Typed fields take precedence over corresponding
legacy variables.

:::{warning}
Absent credentials do not mean anonymous

Leaving both key variables unset means "resolve credentials the normal way",
because that is exactly how every role-based source presents itself — IRSA,
Pod Identity and instance profiles all deliberately leave
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` empty. Treating that as a
public bucket would send unsigned requests and turn a private bucket into an
opaque `403 AccessDenied`. To read a public bucket, explicitly set
`auth.mode: anonymous` (or `AIM_S3_ANONYMOUS=true` for legacy configuration).
:::

:::{note}
Role-based credentials need a ServiceAccount you can annotate

Chain mode resolves whatever the download Pod's environment offers. Download
Jobs currently run under the namespace's `default` ServiceAccount and
`AIMArtifact` has no field to select a different one, so IRSA and EKS Pod
Identity require annotating that `default` ServiceAccount — which grants the
role to every Pod in the namespace. EC2 instance profiles, shared profiles
and credential processes need no ServiceAccount changes.
:::

### S3 control knobs

Typed connection fields cover endpoint, region, authentication, addressing,
signing, and TLS. The downloader also recognises these optional environment
variables for legacy configuration and transfer tuning:

When a typed S3 connection is active, transport, proxy, trust, and credential
provider variables must come from the administrator-owned RuntimeConfig.
Artifact-level `spec.env` accepts only the bounded `AIM_S3_LOG_LEVEL`,
`AIM_S3_MAX_WORKERS`, `AIM_S3_MAX_CONCURRENCY`, and
`AIM_S3_MULTIPART_CHUNKSIZE_MB` tuning variables. Other artifact variables are
reported as an invalid configuration rather than copied into the transfer Job.

| Variable | Default | Purpose |
| --- | --- | --- |
| `AIM_S3_ANONYMOUS` | unset (credentials are resolved) | Set to `true` to read a public bucket with unsigned requests. |
| `AWS_REGION` / `AWS_DEFAULT_REGION` | `us-east-1` when a custom endpoint is set, otherwise left to boto3 | Signing region (most non-AWS backends ignore it; R2 aliases `us-east-1` → `auto`). On real AWS it is left unset so the bucket's region is discovered rather than pinned wrong. |
| `AIM_S3_ADDRESSING_STYLE` | `path` when a custom endpoint is set, else `auto` | Force `path`, `virtual`, or `auto` bucket addressing. |
| `AIM_S3_SIGNATURE_VERSION` | boto3 default (V4) | Override the signing version (e.g. `s3v4`). Rarely needed. |
| `AWS_CA_BUNDLE` | botocore's default CA bundle | Path to a PEM CA bundle. Prefer `artifact.s3.tls.caBundleRef`, which mounts it automatically. |
| `AIM_S3_INSECURE_SKIP_VERIFY` | `false` | Disable certificate and hostname verification. Prefer the typed field when this temporary diagnostic bypass is unavoidable. |
| `AIM_S3_LOG_LEVEL` | `INFO` | Downloader-only verbosity (`DEBUG`, `INFO`, `WARNING`, `ERROR`, or `CRITICAL`). Botocore, boto3, s3transfer, and urllib3 remain at `WARNING`. |
| `AIM_S3_MAX_WORKERS` | `8` | Number of objects downloaded in parallel; artifact values must be integers from 1 through 64. |
| `AIM_S3_MAX_CONCURRENCY` | boto3 default (`10`) | Multipart threads per object; artifact values must be integers from 1 through 64. |
| `AIM_S3_MULTIPART_CHUNKSIZE_MB` | boto3 default (`8`) | Multipart chunk size in MiB; artifact values must be integers from 5 through 5120. |

:::{note}
Effective connection fan-out

Total in-flight connections is roughly `AIM_S3_MAX_WORKERS` ×
`AIM_S3_MAX_CONCURRENCY` (default 8 × 10 = 80). On small/constrained gateways
(single-node MinIO, on-prem RGW) consider lowering one of these.
:::

:::{tip}
Keep secrets out of the manifest

Use `artifact.s3.auth.credentialsSecretRef` for static credentials. AIM Engine
mounts the selected keys and configures the downloader through `_FILE`
variables, keeping values out of both the resource and the process environment.
:::

S3 downloads bind each transfer to object metadata collected before the body is
written. The downloader verifies the resulting file size before its atomic
rename, so a truncated or partial transfer is never published.

:::{danger}
Disabling TLS verification

`insecureSkipVerify: true` disables both certificate-chain and hostname
verification. It is mutually exclusive with `caBundleRef`, emits a warning
event, and should only be used briefly while diagnosing a certificate problem.
:::

## Credential Scope

Environment variables from runtime configurations are merged in this order (highest to lowest priority):

1. `AIMService.spec.env` — per-service
2. `AIMRuntimeConfig.spec.env` — per-namespace
3. `AIMClusterRuntimeConfig.spec.env` — cluster-wide

This allows you to set cluster-wide defaults (e.g., HuggingFace token) and override per namespace or service when needed.

## Troubleshooting

### Image Pull Errors

Check pod events for pull failures:

```bash
kubectl get pods -l serving.kserve.io/inferenceservice=<service-name> -n <namespace>
kubectl describe pod <pod-name> -n <namespace>
```

Common causes:

- Secret doesn't exist in the correct namespace
- Secret has incorrect credentials
- Registry URL is wrong in the model image

### Download Authentication Failures

Check artifact download job logs:

```bash
kubectl get jobs -l aim.eai.amd.com/artifact=<artifact-name> -n <namespace>
kubectl logs job/<job-name> -n <namespace>
```

## Next Steps

- [Multi-Tenancy](multi-tenancy.md) — Per-namespace credential isolation
- [Runtime Configuration](../concepts/runtime-config.md) — Environment variable resolution
