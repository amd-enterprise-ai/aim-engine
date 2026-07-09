# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

<!-- Populate this section during release-prep, then rename to the release version -->

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
