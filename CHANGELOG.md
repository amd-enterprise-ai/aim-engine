# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

<!-- Populate this section during release-prep, then rename to the release version -->

### Added
- Server-side field-selector filtering of profiles by model architecture: `spec.aimId` is now a selectable field on `AIMProfile` and `AIMClusterProfile` (e.g. `kubectl get aimprofile --field-selector spec.aimId=qwen/qwen3-32b`).

### Changed
- Minimum supported Kubernetes version raised to **1.32**, required for the `CustomResourceFieldSelectors` feature (GA in 1.32) that backs the new profile selectable field.
- Serving containers now derive their `ImagePullPolicy` from the image tag (`PullAlways` for `:latest`/tagless, `IfNotPresent` for versioned/digest tags) instead of always pulling, matching kubelet's default and letting pre-loaded (e.g. `kind load`) images be used; re-pushed mutable versioned tags will no longer be re-pulled on nodes with a cached layer.

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
