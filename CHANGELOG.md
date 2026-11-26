# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

<!-- Populate this section during release-prep, then rename to the release version -->

### Breaking
- **`AIMServiceTemplate`, `AIMClusterServiceTemplate` and `AIMTemplateCache`.** The three kinds, their CRDs and the template pipeline that served AIMServices from them are removed. Delete the template objects before you upgrade, as described in `docs/docs/admin/upgrading.md#delete-template-objects`.
- **`aim-template-migration`.** The tool and its runbook are removed from this release; v0.2.7 still includes them for the move before the upgrade. Services it already moved to the profile pipeline keep running through the upgrade without a restart. Move the remaining template services on v0.2.7, with or without the tool, as described in `docs/docs/admin/upgrading.md#move-template-services`.
- **Legacy model mode.** AIMModels and AIMClusterModels, including the ones an AIMClusterModelSource discovers, no longer create AIMServiceTemplates or AIMClusterServiceTemplates. They serve only through the AIMProfiles they discover, generate or derive; see `docs/docs/admin/upgrading.md#replace-models-without-a-native-path`.
- **`aim.eai.amd.com/v1alpha1` AIMService, AIMModel and AIMClusterModel.** The API server serves these three kinds only at v1alpha2. Existing objects stay readable and updatable at v1alpha2, but applying a v1alpha1 manifest for them fails; see `docs/docs/admin/upgrading.md#update-v1alpha1-manifests`. AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and AIMClusterModelSource stay at v1alpha1.
- **Download Jobs no longer read the runtime config `spec.env`.** The model download, size check, adapter download and adapter subtree sync Jobs read only the new `spec.caching.env` of `AIMRuntimeConfig` and `AIMClusterRuntimeConfig`. Download settings left in `spec.env`, such as `HF_TOKEN`, `HF_ENDPOINT`, `AIM_DOWNLOADER_PROTOCOL`, S3 credentials (`AWS_*`) or proxy variables, no longer reach the download Jobs that the new operator creates, including a new Job after a failed one is deleted. Artifacts that are already `Ready` and Jobs that are already running are unaffected. After updating the CRDs, copy them to `spec.caching.env` before or with the operator upgrade (Helm: `clusterRuntimeConfig.spec.caching.env`), and keep in `spec.env` whatever inference containers also need. The upgrade preflight lists these variables, and after the CRD update it fails until each is in `spec.caching.env`. Default Helm installs are unaffected. See `docs/docs/admin/upgrading.md`.

### Added
- **`spec.caching.env` in runtime configs.** `AIMRuntimeConfig` and `AIMClusterRuntimeConfig` have a new `spec.caching.env` for download Jobs. The namespace runtime config wins over the cluster runtime config by name, and the `AIMArtifact` `spec.env` wins over both.
- **v1alpha2 for AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and AIMClusterModelSource.** These four kinds are served at v1alpha2 as well as v1alpha1. v1alpha1 remains the storage version, so existing objects stay stored as v1alpha1 and can be read and written at either version.
- Hand-authored `AIMProfile` and `AIMClusterProfile` resources can declare `spec.baseImageRef`, the supported clean-runtime lineage used when ProfileSets or service overlays replace model weights.
- **Tolerations for inference pods.** Set `spec.tolerations` in an `AIMRuntimeConfig`, `AIMClusterRuntimeConfig` or `AIMService` to let inference pods onto tainted nodes. An AIMService's list replaces the runtime config's, a namespace runtime config's replaces the cluster's, and an empty list removes the inherited one.
- **Tolerations for the Jobs that prepare a model.** The size check, download and discovery Jobs get the runtime config's `spec.tolerations`, and the adapter staging Jobs get the same tolerations as the inference pods. A model can now start when every node that can run these Jobs has a taint.
- **Security contexts for inference pods.** Set `spec.podSecurityContext` and `spec.securityContext` in an `AIMRuntimeConfig`, `AIMClusterRuntimeConfig` or `AIMService` to run inference pods as non-root, for example in a namespace that enforces the Pod Security Standard `restricted`. An AIMService's value replaces the runtime config's, and a namespace runtime config's replaces the cluster's, as a whole. The API server refuses values that the Pod Security Standard `baseline` forbids, such as `privileged: true`, and also refuses all added capabilities and sysctls. Without these fields, the inference pods do not change.

### Changed (behavioural)
- The chart's default `AIMClusterRuntimeConfig` renders at v1alpha2. Helm updates the existing object in place; upgrade the CRDs before the operator, which is already the required order.
- **Every AIMService is served through profiles.** Services with `spec.model.name` or `spec.model.image` no longer need the `aim.eai.amd.com/reconciler-pipeline: profile` annotation, and the operator ignores it. Keep the annotation, and add it to services you create after the upgrade, as long as you might roll back to v0.2.7; see `docs/docs/admin/upgrading.md#rollback`. A service with `spec.model.image` uses or creates an AIMModel for the image.
- **Image models are served natively.** Image AIMModels and AIMClusterModels record `status.imageMetadata`, `status.version` and `status.sourceType`, take their primary profile from the image's recommended deployments, and look up their runtime config without templates.
- **`spec.model.custom` is rejected.** A new AIMService CRD validation rule rejects `spec.model.custom` on new objects and on updates that add it. Create an AIMModel for the custom weights; an existing service switches to one of its AIMProfiles with a merge patch that removes `spec.model` and sets `spec.profile.name`, as in `docs/docs/admin/upgrading.md#replace-unservable-services`.
- **Template-era fields produce a warning.** AIMServices with `spec.template`, `spec.overrides` or `spec.model.custom`, and models with `spec.discovery.createServiceTemplates: false`, `spec.defaultServiceTemplate`, `spec.custom`, `spec.customTemplates`, `spec.env` or `spec.resources`, report those fields in a `TemplateFieldsIgnored` warning condition and event. It doesn't affect readiness and clears when you remove the fields. Keep `spec.discovery.createServiceTemplates: false` as long as you might roll back to v0.2.7.
- **Template-era status is cleared.** AIMServices drop `status.reconcilerPipeline`, `status.resolvedTemplate`, the template cache reference and the `ModelReady`, `TemplateReady`, `CacheReady` and `MigrationRequired` conditions. Models drop `MigrationRequired`, `ServiceTemplatesReady`, `ClusterServiceTemplatesReady` and `LegacyAutoCustomSpecReady`.
- **Fine-tune and auto-custom models produce no profiles.** Models with `spec.modelSources` and `spec.aimId`, such as fine-tunes and the models generated for inline custom services, reconcile without producing AIMProfiles and no longer report `MigrationRequired`. Only the upgrade preflight's `models-without-native-path` check flags them; see `docs/docs/admin/upgrading.md#replace-models-without-a-native-path`.
- **Re-inspected image models can get a different primary profile.** Image models whose `status.imageMetadata` an earlier release left without the image labels are inspected again, and their primary profile now comes from the image's recommended deployments. Services already bound to a profile keep it.
- **`ImageMetadataReady=False` doesn't block a model.** Where the operator can't reach the registry, image models report `ImageMetadataReady=False` but become Ready once discovery and their profiles are Ready.
- **An AIMService isn't Ready during a predictor rollout.** While a new predictor revision isn't ready and the previous one still serves, the service reports `Ready=False` with reason `RolloutInProgress`; `status.status` stays `Running`. If the new revision crash-loops or its Deployment exceeds its progress deadline, the reason becomes `RolloutStalled` and the service emits a Warning event.
- **An AIMService that moves to another AIMProfile deletes what it created for the old one.** Once the predictor rollout finishes, the service deletes its Dedicated AIMProfileCache and overlay AIMProfile for the previous AIMProfile; Shared caches stay. To change a service's model, see `docs/docs/concepts/services.md#change-a-services-model`.
- Weight-replacing profile derivations now use the producer's exact `AIM_BASE_IMAGE_REF` or `spec.baseImageRef`. Tags and digests are no longer truncated or synthesized. The operator uses the private producer prefixes found in released images (`docker.io/silogenai` and `ghcr.io/silogen`) as public `docker.io/amdenterpriseai` compatibility fallbacks even when a resource selects a named RuntimeConfig; matching configured mappings remain authoritative for private and air-gapped mirrors. The fallbacks are built into the operator; the Helm chart does not add mappings to its default cluster RuntimeConfig.
- **The model download Job has CPU and memory requests and a memory limit.** It had none, so a namespace `LimitRange` set them: a low default memory limit could make the downloader `OOMKilled`, and a default CPU limit made it slow. The default is requests `500m` CPU and `1Gi` memory, and a limit of `8Gi` memory. The default has no CPU limit, but a `LimitRange` default CPU limit still applies. Set `spec.artifact.downloadJobResources` in an `AIMRuntimeConfig` or `AIMClusterRuntimeConfig` to change the values. See `docs/docs/concepts/caching.md`.
- **Some namespaces refuse the new download Pods after the upgrade.** The API server refuses them in a namespace with a `LimitRange` default CPU limit below `500m`, a `LimitRange` `max` memory below `8Gi`, or a `ResourceQuota` without room for these values. The artifact then does not download. To fix it, set `downloadJobResources` in the `AIMRuntimeConfig` or `AIMClusterRuntimeConfig` to values that the namespace admits, or to `{}` to let the `LimitRange` decide as before. See `docs/docs/admin/upgrading.md`.

### Deprecated
- **v1alpha1 `AIMArtifact`, `AIMRuntimeConfig`, `AIMClusterRuntimeConfig` and `AIMClusterModelSource`.** Applying these versions returns a deprecation warning that names `aim.eai.amd.com/v1alpha2`. They still apply in this release. The next release stops serving their v1alpha1.

### Fixed
- The `spec.env` of an `AIMRuntimeConfig`, an `AIMClusterRuntimeConfig` and the `AIMService` itself now reaches the inference container of every `AIMService`. Before, the only way to set such a variable was `spec.profileOverrides.containerEnv`, which creates an overlay profile and, with a Dedicated cache, downloads the model again. The env goes only into the InferenceService, so the profile and its cache do not change. The AIMService `spec.env` wins over the profile `containerEnv`, which wins over the namespace runtime config, which wins over the cluster runtime config. The operator-owned variables, such as `AIM_PROFILE_ID`, and the profile `engineEnv` win over all of them. A `spec.env` entry replaces a profile `containerEnv` entry only when both are literal values and the `spec.env` value is not empty; otherwise the profile value stays. After the upgrade, the inference pods of each such service that gets a variable from its runtime config or `spec.env` restart once. Other services do not change.
- Cached v1alpha1 image metadata from older operators is re-inspected to recover `AIM_BASE_IMAGE_REF`. Reinspection failures retain cached metadata and normal readiness while reporting a non-gating diagnostic that can recover on a later reconcile; images that genuinely omit the field are recorded without repeated registry polling.
- Missing runtime lineage now reports `BaseImageUnresolved` instead of the misleading `InvalidProfileOverrides`, while existing ProfileSet and AIMService resources remain untouched.
- ProfileSet controllers no longer requeue every set for RuntimeConfig status-only updates.
- Service overlay profiles now record canonical runtime lineage consistently in both `spec.baseImageRef` and the `aim.eai.amd.com/base-image` annotation, while `spec.image` remains the mapped deployment reference.
- **The Jobs that the operator creates pass the Pod Security Standard `restricted`.** A namespace that enforces `restricted` refused them. The size check, download, discovery and adapter Jobs now set the `RuntimeDefault` seccomp profile, and each of their containers sets `allowPrivilegeEscalation: false` and drops all capabilities. The `AIMModel` and `AIMClusterModel` discovery Job no longer runs the model image as root; it runs as UID 65532. **OpenShift users: before you upgrade, grant the built-in `nonroot-v2` SCC to the service accounts that run these Jobs**, for example `default` in the model namespace and in the operator namespace. Without it, `anyuid` refuses the Jobs because they set a seccomp profile, and the default restricted SCC refuses their fixed UIDs. See `docs/docs/admin/upgrading.md`.
- An `AIMService` now shows when the API server or an admission policy refuses its predictor pod, for example because of a LimitRange or a ResourceQuota. Before, the service stayed in `Starting` with no message. Now its status is `Failed`, and the `InferenceServiceReady` and `Ready` conditions have the reason `PodCreationFailed` and the refusal text. The service goes back to `Starting` when a pod can be created. While an older pod still serves, the service stays `Running` and the `InferenceServiceReady` message has the refusal text.
- Models whose spec produces no AIMProfiles report `ProfilesAvailable=False` with reason `NoDeployableProfiles`, naming the cause and the fix; services that resolve through them report `ModelHasNoProfiles`.
- Registry errors on image models are reported by cause: `ImageNotFound` (HTTP 404) and `InvalidImageReference` block the model, and `ImagePullAuthFailure` (HTTP 401 or 403) sets `AuthValid=False` while discovery still runs.
- Image models back off when a reconcile fails instead of retrying every 2 s, and a model blocked by a missing image or pull secret, or an invalid reference, rechecks every minute instead of reconciling in a loop. A registry answer of HTTP 401, 403 or 404 is reused for a minute, only by models in the same namespace with the same pull secrets; other registry failures are reused for 15 minutes by every model on the image.
- **A replaced predictor pod no longer fails its `AIMService`.** A rollout or scale-down deletes predictor pods, and a runtime that exits non-zero on SIGTERM leaves its pod `Failed` until the pod is gone. The service then reported status `Failed` with `Ready` and `ConfigValid` reason `InvalidSpec` and `InferenceServicePodsReady` reason `PodFailed`, stopped updating its InferenceService, and recorded Warning events on every reconcile. Those events could use up the service's event budget, so a later `RolloutStalled` Warning was not recorded. A deleted pod that failed no longer counts toward the service's pod health.
- Discovery Jobs that can't pull their image no longer hold the cluster-wide discovery slots. The operator deletes the Job and backs off with reason `DiscoveryImagePullFailed`, right away for an auth or not-found error and after 10 minutes for other pull errors. A discovery Job that hasn't finished after an hour fails.

### Upgrade
- **Upgrade from v0.2.7 or later.** Upgrade older releases to the latest 0.2.x release first and let it reconcile your resources; see `docs/docs/admin/upgrading.md#minimum-version`.
- **Run the upgrade preflight, then follow the documented steps.** Download `upgrade-preflight.sh` from this release, fix what it reports as described in `docs/docs/admin/upgrading.md#migration-window`, and upgrade the CRDs before the operator once it passes; see `docs/docs/admin/upgrading.md#run-the-upgrade-preflight`.
- **Dedicated template-cache weights are downloaded again.** Moving a service whose template cache is Dedicated creates an AIMProfileCache that downloads the weights again; Shared template caches are adopted without a download. The preflight lists the Dedicated caches with their sizes; see `docs/docs/admin/upgrading.md#cache-outcomes`.
- **Helm fallback for the template CRDs.** Upgrading the CRDs chart deletes the template CRDs. If you can't delete every template object first, annotate the template CRDs with `helm.sh/resource-policy=keep` before the upgrade and remove them by hand afterwards; see `docs/docs/admin/upgrading.md#remove-the-template-crds`.
- **The operator's ClusterRole changes.** It now needs `get`, `list` and `watch` on `apps` Deployments in all namespaces, and no longer covers the template kinds. The Helm chart and `install.yaml` apply this. If you manage the operator's RBAC yourself, grant it before you upgrade: without it, the AIMService controller's Deployment watch times out waiting for its cache to sync and the operator exits. `install.yaml` also drops the nine admin, editor and viewer ClusterRoles of the template kinds (`aim-engine-aimservicetemplate-admin-role` through `aim-engine-aimtemplatecache-viewer-role`). `kubectl apply` doesn't delete them, so remove them by hand once nothing binds to them.
- **Check GitOps manifests.** Change v1alpha1 AIMService, AIMModel and AIMClusterModel manifests in GitOps repositories, Helm charts and scripts to v1alpha2, replace template objects with profiles, and roll the changes out before you upgrade: Helm can't upgrade or uninstall a release whose stored manifest still has v1alpha1 objects of these kinds. See `docs/docs/admin/upgrading.md#update-v1alpha1-manifests`.
- **Rollback has limits.** Until you delete the template objects, a moved service can go back to the template pipeline. Rolling the CRDs and operator back to v0.2.7 after the upgrade restores the template pipeline and v1alpha1 serving, but not the deleted template objects or Dedicated weights; see `docs/docs/admin/upgrading.md#rollback-limits`. Before you roll back, annotate the services without `spec.profile`, set `discovery.createServiceTemplates: false` on image models and delete the leftover AIMModels generated for `spec.model.custom`, as in `docs/docs/admin/upgrading.md#rollback`. On v0.2.7, deleting an AIMArtifact or AIMClusterModelSource leaves behind the PVCs, adapters and discovered models whose owner references this release moved to v1alpha2; see `docs/docs/admin/upgrading.md#rollback-limits`.

## [0.2.7] - 2026-10-08

### Added
- **Template-to-profile migration.** Existing template-based services keep running after upgrading, and resources that still use the template API now show a `MigrationRequired` condition. A new `aim-template-migration` tool plans the move to profile-based serving and applies it without restarting running models. It is a dry run unless you pass `--commit`. See `docs/docs/admin/template-migration.md`. (#197)
- **Private CA certificates.** Set `spec.additionalCABundleRef` in a runtime config to a ConfigMap containing your CA certificates. Inference pods and download jobs will trust them alongside the standard public roots, which helps in air-gapped clusters with internal Hugging Face or S3 endpoints. (#199)
- **More flexible profile derivation.** Profile sets and `AIMModel.spec.profiles` can now derive from any profile in a model's discovery catalog, even when the cluster has no hardware matching that source profile. For example, you can derive partitioned-GPU profiles for your cluster from a model that only ships unpartitioned profiles. Existing derived profile names are preserved. (#201, #203)
- **Chained derivations.** Set `outputPolicy.copyable: true` on a profile set or model to let other derivations build on its profiles. (#211)

### Changed
- **Upgrade the CRDs chart before the operator.** Upgrade `aim-engine-crds`, wait for the CRDs to be `Established`, then upgrade `aim-engine`. (#197)

### Fixed
- The `aim-engine-crds` Helm chart can be installed and upgraded again; it had grown past Kubernetes' 1 MiB release limit. As a side effect, `kubectl explain` now shows shortened field descriptions. (#206)
- Services deployed by image (`spec.model.image`) no longer get stuck in `Starting` after their model cache is ready. (#208)

### Known limitations
- `aim-template-migration` is not yet intended for production. Services using a Dedicated cache must be migrated manually, and there is no rollback.
- Changes to the CA ConfigMap only take effect when pods are recreated.

## [0.2.6] - 2026-09-15

### Added
- **NVIDIA GPU serving end to end.** `AIMProfile`/`AIMClusterProfile` gain `spec.acceleratorVendor` (`amd` | `nvidia`), which resolves both the node-affinity term (`feature.node.kubernetes.io/aim-accelerator.vendor.GPU.<VENDOR>`) and the device-plugin resource (`amd.com/gpu` vs `nvidia.com/gpu`). A generic NVIDIA profile can omit `acceleratorModel` and match any NVIDIA GPU node. Empty vendor keeps the previous AMD behaviour, so existing profiles are unaffected. The accelerator detector's vendor labels, previously published for discovery only, are now consumed by the operator. `acceleratorVendor` is also available on profile selectors and on `AIMService.spec.profile.overrides`.
- **Direct upstream vLLM runtime contract.** A profile with `acceleratorVendor: nvidia` and `engine: vllm` is projected as a `vllm serve` invocation against the mounted artifact — AIM Engine generates the model path, `--served-model-name`, `--host`, `--port` and `--tensor-parallel-size`, promotes `engineEnv` to container env, and forces Hugging Face offline mode. `engineArgs` becomes optional tuning. These runtimes expose vLLM's native OpenAI API and do not declare KServe protocol v2. AMD AIM images keep their baked entrypoint and the existing AIM-runtime env contract.
- **Generated-profile model onboarding (`spec.modelId`).** A v1alpha2 `AIMModel`/`AIMClusterModel` can be onboarded by model identity instead of by image; profiles are generated from `AIMRuntimeConfig` `spec.model.profileGeneration.fallbacks` matched against observed cluster hardware. `aimId` defaults to `modelId` and weights default to `hf://<modelId>`, both overridable (`spec.aimId`, `spec.source`). `spec.accelerator` requests the hardware shape (`count`, `vendor`, `model`, `partitioningMode`); `count` drives both the device request and tensor-parallel size and defaults to 1. Reported as `status.kind: Generated` with `status.profileGeneration.strategy` (`RuntimeFallback` today) so a future AMD catalog lookup can be added without reclassifying existing models. Profiles carry `profile-origin: generated`. **This flow does not yet perform an AMD catalog lookup** — AMD catalog onboarding remains the image-backed Official flow.
- **Out-of-the-box NVIDIA model-ID runtime policy.** Helm now installs a chart-managed `AIMClusterRuntimeConfig/default` containing a generic NVIDIA vLLM fallback. The fallback omits generic CPU/memory requests, remains truthfully `unoptimized`, and uses `autoSelectionPolicy: any` so it is selected only when no optimized profile matches and the service did not explicitly set `minimumType`. It can be overridden or disabled when a platform manages its own runtime policy.
- **Static model declarations on `AIMClusterModelSource`** via `spec.models[]` (`modelId`, optional `aimId`, `source`, `accelerator`), which can be combined with `filters` or `images`. Declared models materialize as v1alpha2 `AIMClusterModel` resources — v1alpha1 CEL rejects `spec.modelId`, so only this path uses the v1alpha2 client; registry discovery continues to write v1alpha1 unchanged. Unlike discovered images, models a source owns are kept in sync with their declaration; neither kind is ever deleted.
- `make install-nvidia-dependencies` installs the NVIDIA Kubernetes device plugin and its bundled NFD (`hack/nvidia/`). Host driver, container toolkit and `nvidia` RuntimeClass must already exist.
- **`s3://` artifact downloads rewritten on boto3**, replacing the `s3cmd` shell path. Credentials now resolve through the standard boto3 chain when `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` are unset, so IRSA, EKS Pod Identity, ECS task roles, EC2 instance profiles, shared profiles and credential processes work for the first time. Note that download Jobs run under the namespace's `default` ServiceAccount, so IRSA and Pod Identity require annotating it. New `AIM_S3_*` env knobs tune addressing style, signature version, region and multipart concurrency. (#158)
- **Anonymous (unsigned) S3 access** for public buckets, requested explicitly with `AIM_S3_ANONYMOUS=true` or by setting a credential variable to `anonymous`. Omitting the credentials instead means "resolve them from the environment" — it is not read as a public bucket, because every role-based credential source deliberately leaves those variables unset. Partial credentials, and a real key combined with an `anonymous` sentinel, are now rejected with a specific error instead of silently falling back to unsigned requests. (#158)

### Changed
- **CRDs must now be installed with `kubectl apply --server-side`.** The v1alpha2 schemas exceed the 262144-byte annotation limit that client-side apply relies on. `make install`, the README and the installation guide are updated. Existing client-side-applied installs are migrated automatically by kubectl; if CRDs are also managed by the Helm CRD chart, verify there is no field-manager conflict before upgrading.
- `AIMClusterModelSource` now watches v1alpha2 `AIMClusterModel`. v1alpha2 is the storage version, so this observes the same objects as before.
- **`AIMArtifact` events now reach every `AIMTemplateCache` referencing the artifact**, not just the cache that created it. Caches are indexed by `status.artifacts[].name`; the creator-label path is retained for newly created artifacts and legacy caches. Shared artifacts previously left non-creator caches stuck until their next resync.
- **`s3://` downloads against a custom `AWS_ENDPOINT_URL` now sign with Signature V4 by default**, where the previous `s3cmd`-based downloader forced Signature V2. Backends that only accept V2 need `AIM_S3_SIGNATURE_VERSION=s3`. (#158)

### Fixed
- Runtime projection now preserves the last-known cache mount when artifact listing or cache readiness is temporarily unknown, instead of interpreting the outage as cache absence. Initial force-owned projection applies the ConfigMap before the consuming runtime and stops on the first failure.
- Runtime profile ConfigMaps now reproduce the source profile's versioned YAML contract instead of always emitting a fixed union of known fields. AIM Engine keeps one canonical `AIMProfile` API, records the source codec and field presence, preserves unmodeled metadata/top-level extensions, and propagates that contract through profile-set/custom-model derivation, service overlays, and eager/lazy projection without parsing image tags. Generated and hand-authored profiles default to the strict canonical schema.
- `AIM_S3_LOG_LEVEL=DEBUG` now increases only downloader-package verbosity; botocore, boto3, s3transfer, urllib3, and the process root remain at `WARNING`, preventing SDK wire logs from emitting signed headers or temporary credential responses.
- S3 object transfers now verify every downloaded object's listed size before atomic publication, so truncated or partial transfers are never published.
- Typed S3 connections now separate administrator RuntimeConfig environment from artifact-level environment. RuntimeConfig can still select infrastructure proxies and credential providers, while `AIMArtifact.spec.env` is limited to validated downloader tuning; proxy, process, trust, and credential-source overrides are rejected with `ConfigValid=False`.
- Generated-model `NoCompatibleRuntime` conditions and warning events now explain why each configured fallback was rejected, including missing accelerator labels, model/request mismatches, and insufficient per-node resources such as `nvidia.com/gpu requested 1, allocatable 0`. Diagnostics are bounded for large clusters instead of collapsing every mismatch into the generic "no runtime matches" message.
- S3 authentication failures are now classified as auth errors rather than an invalid spec. `AccessDenied` and botocore's "Unable to locate credentials" matched none of the existing patterns, so a credentials problem surfaced as `ConfigValid=False`/`InvalidSpec` and pointed at the artifact's `sourceUri`. Missing buckets and empty prefixes still classify as source problems. (#158)

### Known limitations
- ServingRuntime and ConfigMap projection updates are eventually consistent; Kubernetes does not provide a transaction across the pair.
- ConfigMap-only profile changes do not guarantee a KServe pod rollout. Restart affected predictor pods when the serving process must reload configuration immediately.
- Out-of-band mutation of generated `aim-*` runtimes or their same-name ConfigMaps is unsupported. Server-side apply can reclaim desired fields but does not necessarily prune extra fields owned by another manager.
- Retained model-slug aliases are best-effort while their profile is unprojectable or model-slug election is disabled; they can retain a previous revision or winner until projectability or election resumes.

## [0.2.5] - 2026-07-09

### Added
- **v1alpha2 API (`aim.eai.amd.com/v1alpha2`)** — new `AIMModel`/`AIMClusterModel`, `AIMProfile`/`AIMClusterProfile`, `AIMProfileSet`/`AIMClusterProfileSet`, and `AIMProfileCache` resources, introducing a profile-based deployment path alongside the existing template path. `AIMService` can resolve a profile by `name` or by `selector`. (#111, #122)
- **LoRA adapter serving** — `AIMArtifact` gains `spec.type: model|adapter` and a shared ReadWriteMany adapter disk (`spec.adapterDisk`) on the base-model artifact; `AIMService.spec.adapters` plus `spec.adapterMode` (`static`/`dynamic`) serve adapters, with dynamic mode hot-loading/unloading without restarting the pod. (#130)
- **Scale-to-zero** — set `AIMService.spec.minReplicas: 0` to idle a service to zero replicas and reactivate on the next request via KEDA. Adds `spec.autoScaling.pollingInterval` and `spec.autoScaling.cooldownPeriod`, a memory-derived cooldown heuristic, and a chart-managed gateway-metrics OpenTelemetry collector. Requires routing to be enabled. (#117)
- **GPU partitioning (ADR-009a/009b)** — profiles can target GPU partition slices via `acceleratorPartitioningMode`; the accelerator detector now publishes current partition state as NFD labels. (#120)
- Server-side field-selector filtering of profiles by model architecture: `spec.aimId` is a selectable field on `AIMProfile`/`AIMClusterProfile` (e.g. `kubectl get aimprofile --field-selector spec.aimId=qwen/qwen3-32b`). (#133)
- Expanded `AIMService.spec.profileOverrides` (v1alpha2): override `modelSources`, `acceleratorModel`, `acceleratorCount`, `acceleratorPartitioningMode`, and `engineEnv`, and merge `containerEnv`/`engineArgs` onto a referenced profile without forking it.
- Helm: `manager.artifactDownloaderImage` install-time override, an optional `clusterModelSource` block for cluster-wide model discovery, and a `scaleFromZero` tuning block.

### Changed
- **Minimum supported Kubernetes version raised to 1.32**, required for the `CustomResourceFieldSelectors` feature (GA in 1.32) that backs the new profile selectable field.
- Serving containers now derive their `ImagePullPolicy` from the image tag (`Always` for `:latest`/tagless, `IfNotPresent` for versioned/digest tags) instead of always pulling, matching kubelet defaults and allowing pre-loaded (e.g. `kind load`) images to be used.
- Template selection now uses a minimum-type floor instead of manual selection. (#139)
- Autoscaling specs are validated more strictly: an empty `autoScaling` block and under-specified metric/target entries are now rejected by CEL.
- `acceleratorCount` now matches guaranteed CPU QoS for EPYC CPU profiles. (#134)

### Deprecated
- v1alpha1 `AIMModel` and `AIMClusterModel` are deprecated in favor of their v1alpha2 equivalents. They still function (with a deprecation warning) but are scheduled for removal — the v1alpha1 reconciliation logic will be deleted in a future release and these resources will no longer be served. Plan your migration to v1alpha2 now. (#111)

### Fixed
- Inference route auth bypass on multi-listener gateways (EAI-6951): generated HTTPRoutes are now pinned to the configured `spec.routing.hostnames`, and a routing-enabled service on a Gateway with more than one listener now requires a hostname — without one no route is created and the service reports `ConfigValid=False`/`RouteHostnameRequired`. Single-listener gateways are unaffected. (#142)
- Empty `AIMService` profile name is now treated as omitted rather than a validation error. (#151)
- Eliminated AIMClusterModelSource/AIMModel reconcile error loops. (#131)
- Prevented AIMClusterModelSource from hammering the registry. (#123)
- Fixed v1alpha2 cache download token handling. (#140)
- Keep the optimized image when profile derivation leaves weights unchanged. (#136)
- Allow zero-byte files in download verification. (#105)
- Delete stale `*.lock` files on cleanup. (#135)
- Artifact downloader: install `click` so the HuggingFace CLI runs (#127), and emit a warning if the disk-usage call hangs in the stall detector (#154).
- v1alpha2 AIMService endpoint allows updating an existing v1alpha1 object. (#122)
- Removed the API version from the controller name. (#153)

## [0.2.4] - 2026-05-26

### Added
- `aimId`-based template matching for fine-tuned models — specify `spec.aimId` and `spec.modelSources` on an AIMModel and the controller automatically finds matching official templates, filters by version policy, and creates copies with custom weight sources baked in. No `customTemplates` or `custom.hardware` required.
- `spec.aimId` field on AIMModelSpec for identifying the base model family when onboarding fine-tuned weights.
- `spec.custom.versionPolicy` field on AIMCustomModelSpec (`pinned`, `latest`, `any`) for controlling which template versions are matched.
- `status.version` field on AIMServiceTemplateStatus, populated from the owning model's image tag during reconciliation.
- E2E tests for fine-tuned model flows: pinned version matching, no-match negative case, and cluster-to-namespace cross-scope matching (`tests/e2e/aimmodel/fine-tuned/`).
- Support for AMD Radeon Pro W7900 and Radeon AI Pro R9700 as first-class GPU targets in AIMModel and AIMServiceTemplate, including device-ID node affinity and GPU-preference ranking (ranked below all Instinct models).
- Discovery jobs now also emit `AIM_ACCELERATOR_MODEL` / `AIM_ACCELERATOR_COUNT` alongside the legacy `AIM_GPU_*` variables, and accept `accelerator_*` fields in profile metadata, so v1alpha1 works against AIM images that use the newer accelerator-oriented schema.

### Changed
- Controller image and Helm/CRDs OCI charts now publish to `docker.io/amdenterpriseai/*` instead of `ghcr.io/silogen/*`; the chart's `manager.image.repository` defaults to `docker.io/amdenterpriseai/aim-engine` (#114).
- `manager.imagePullSecrets` default renamed from `regcred` to `dockerhub-regcred`;

### Fixed
- Tilt dev deployment uses `Recreate` strategy to prevent pod restart deadlocks caused by RWO PVC contention during rolling updates.

## [0.2.2] - 2026-03-20

### Added
- Artifact storage quota enforcement with configurable cluster-wide and per-namespace limits via `artifactStorageQuota` in RuntimeConfig (#60)
- Automatic artifact eviction when storage quota is exceeded, with priority-based ordering via `retentionPriority` and `defaultRetentionPriority` (#60)
- Eviction protection annotation (`aim.eai.amd.com/eviction-protected`) to exempt specific artifacts from automatic eviction (#60)
- Download filter support (`downloadFilter`) on AIMArtifact and RuntimeConfig to control which files are included/excluded during HuggingFace downloads — subdirectory files are now excluded by default (#56)
- Custom profile support for AIMServiceTemplate and AIMModel custom templates — define inline engine args and env vars without needing a pre-built AIM image (#61)
- `priorityClassName` field on AIMService to set Kubernetes PriorityClass on inference pods (#50)
- AIMService annotations are now included in generated HTTPRoutes, with service annotations taking precedence over runtime config annotations (#55)
- Template health checks surfaced in AIMService status with selection reason and message (#31)

### Changed
- Artifact downloader image version is now set at build time via ldflags to match the release tag, replacing the hardcoded default (#15)
- Runtime status replica fields (`currentReplicas`, `desiredReplicas`, `minReplicas`, `maxReplicas`) are now always reported, including zero values on startup (#58)
- AIMService status remains `Running` while scaling when ready pods still exist, using new `RuntimeScaling` reason (#59)
- Cache status messages now propagate root cause from template cache conditions instead of generic strings (#53)
- CI workflows updated: artifact downloader builds on tags with configurable version input, publish-main workflow for package repository (#15, #57)

### Fixed
- Environment variable expansion and kustomize build overwrite bug resolved (#49)

## [0.2.0] - 2026-02-20

Initial public release of AIM Engine — a Kubernetes operator for deploying AI inference workloads on AMD Instinct GPUs.

## Highlights

- **Declarative inference deployment** — Deploy production-ready endpoints with a single `AIMService` resource. The operator handles model resolution, template selection, KServe InferenceService creation, and HTTP routing.
- **Built for optimized AIM images** — Pre-built containers packaging open-source models tuned for AMD Instinct GPUs with optimized serving runtimes.
- **Automatic model catalog** — Discover AIM images from container registries with wildcard filters, version constraints, and periodic sync.
- **Smart template selection** — Multi-stage algorithm selects the optimal runtime profile based on GPU availability, precision, and optimization metric.
- **Model caching** — Pre-download model weights to shared PVCs. Shared and Dedicated modes control lifecycle and reuse.
- **Multi-protocol downloads** — HuggingFace downloads with configurable protocol fallback (XET, HF_TRANSFER, HTTP).
- **Gateway API routing** — HTTPRoute creation with customizable path templates.
- **KEDA autoscaling** — Scale on OpenTelemetry metrics from the vLLM runtime.
- **Multi-tenancy** — Namespace and cluster-scoped resource variants with resolution order and label propagation.

## Custom Resources

| CRD | Scope | Purpose |
|-----|-------|---------|
| `AIMService` | Namespace | Inference endpoints |
| `AIMModel` / `AIMClusterModel` | NS / Cluster | Model catalog |
| `AIMServiceTemplate` / `AIMClusterServiceTemplate` | NS / Cluster | Runtime profiles |
| `AIMRuntimeConfig` / `AIMClusterRuntimeConfig` | NS / Cluster | Defaults and routing |
| `AIMClusterModelSource` | Cluster | Registry discovery |
| `AIMArtifact` | Namespace | Model downloads |
