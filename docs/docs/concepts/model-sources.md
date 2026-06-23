# AIM Model Sources

`AIMClusterModelSource` automatically discovers and syncs AI model images from container registries, creating `AIMClusterModel` resources for matched images.

:::{admonition} API version
:class: note

`AIMClusterModelSource` remains under `aim.eai.amd.com/v1alpha1`. The `AIMClusterModel` resources it creates use the v1alpha2 shape (`spec.image` set) so they participate in v1alpha2 discovery and produce `AIMProfile` resources. This deliberately decouples discovery from the v1alpha1 → v1alpha2 migration window.
:::
## Overview

Model sources eliminate the need to manually create model resources for every image version. They continuously sync with container registries, automatically creating models when new images are published.

Key features:

- **Automatic discovery** — continuously monitors registries for configured repositories/tags
- **Simple explicit lists** — `spec.images` for straightforward image declarations
- **Advanced filtering** — `spec.filters` with per-filter version constraints and exclusions
- **Multi-registry support** — Docker Hub, GHCR, and other OCI registries via the tags list API
- **Periodic sync** — configurable sync intervals
- **Private registries** — supports authentication via `imagePullSecrets`

## Basic Example

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: amd-models
spec:
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
    - amdenterpriseai/aim-deepseek-deepseek-r1:0.8.5
  syncInterval: 1h
```

This source discovers the listed images from Docker Hub and creates an AIMClusterModel for each.

## Configuration

### Registry

The `registry` field specifies which container registry to query. Defaults to `docker.io` if not specified.

```yaml
spec:
  registry: ghcr.io  # or docker.io, gcr.io, etc.
```

### `images` (recommended)

`images` is the simplest way to define model sources. It accepts explicit image references.

```yaml
spec:
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
    - ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1
```

Supported formats:

- `org/repo:tag`
- `org/repo` (uses `versions` constraints, if provided)
- `registry/org/repo:tag`

### `filters` (advanced)

Filters define advanced matching behavior when you need per-filter options like custom version constraints or exclusions.

Each filter is an explicit repository/image selector with optional `versions` and `exclude`.
Multiple filters are combined with OR logic.

```yaml
spec:
  filters:
    - image: amdenterpriseai/aim-qwen-qwen3-32b
      versions:
        - ">=0.8.5"
    - image: ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1
```

### Mutual Exclusivity: `images` vs `filters`

You must set exactly one of `spec.images` or `spec.filters`.

```yaml
spec:
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
  # filters: ...  # invalid when images is set
```

The CRD validates this and rejects resources that set both (or neither).

### Version Constraints

Use semantic version constraints to filter tags. Supports both global and per-filter version constraints.

#### Global Version Constraints

Apply to all filters:

```yaml
spec:
  registry: ghcr.io
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b
    - amdenterpriseai/aim-deepseek-deepseek-r1
  versions:
    - ">=0.8.0"
    - "<1.0.0"
```

#### Per-Filter Version Constraints

Override global constraints for specific filters:

```yaml
spec:
  registry: ghcr.io
  versions:
    - ">=0.8.0"  # global default
  filters:
    - image: amdenterpriseai/aim-qwen-qwen3-32b
      versions:
        - ">=0.8.5"  # overrides global for this filter
    - image: amdenterpriseai/aim-deepseek-deepseek-r1
      # uses global constraint
```

#### Version Syntax

Constraints use standard semver syntax:

- `>=1.0.0` - Version 1.0.0 or higher
- `<2.0.0` - Below version 2.0.0
- `~1.2.0` - Patch updates only (1.2.x)
- `^1.0.0` - Minor updates allowed (1.x.x)

Prerelease versions (e.g., `0.8.1-rc1`) are supported:

```yaml
versions:
  - ">=0.8.1-rc1"  # includes prereleases
```

Non-semver tags (e.g., `latest`, `dev`) are silently skipped when version constraints are specified.

### Exclusions

Exclude specific repositories from matching (advanced `filters` mode):

```yaml
spec:
  filters:
    - image: amdenterpriseai/aim-qwen-qwen3-32b
      exclude:
        - amdenterpriseai/aim-base
        - amdenterpriseai/aim-experimental
```

Exclusions match repository names exactly (not including the registry).

### Sync Interval

Control how often the source syncs with the registry:

```yaml
spec:
  syncInterval: 30m  # supports: 15m, 1h, 2h30m, etc.
```

Default is `1h`. Minimum recommended interval is `15m` to avoid rate limiting.

### Private Registries

Authenticate to private registries using imagePullSecrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ghcr-secret
  namespace: aim-system  # operator namespace
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: BASE64_CONFIG
---
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: private-models
spec:
  registry: ghcr.io
  imagePullSecrets:
    - name: ghcr-secret
  images:
    - myorg/private-model:1.0.0
```

Secrets must exist in the operator namespace (typically `aim-system`).

#### GitHub Container Registry (GHCR) Authentication

For GitHub Container Registry, use a GitHub Personal Access Token (PAT) with the minimal required scope:

**Required Scope:**
- `read:packages` - Read access to container packages

**Recommended: Use Fine-Grained Personal Access Tokens**

1. Create a fine-grained PAT at: https://github.com/settings/tokens
2. Set repository access or organization permissions
3. Grant only `read:packages` permission
4. Set expiration date
5. Create the secret:

```bash
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=YOUR_GITHUB_USERNAME \
  --docker-password=YOUR_GITHUB_PAT \
  --namespace=aim-system
```

**Security Best Practices:**
- Use fine-grained PATs instead of classic PATs when possible
- Grant minimal permissions (`read:packages` only)
- Set expiration dates on tokens
- Rotate tokens regularly
- Use separate tokens for different environments (dev/staging/prod)
- Enable encryption at rest for Kubernetes Secrets in production
- Limit Secret access via RBAC to only the operator namespace

**Token Scopes to Avoid:**
- ❌ `repo` - Grants read/write access to repositories (too broad)
- ❌ `write:packages` - Write access not needed for discovery
- ❌ `admin:org` - Organization admin access (unnecessary)
- ❌ `delete:packages` - Delete permission (unnecessary risk)

### Max Models Limit

Control the maximum number of models created to prevent runaway resource creation:

```yaml
spec:
  maxModels: 100  # CRD default: 100, range: 1-10000
  images:
    - org/model-a:1.0.0
    - org/model-b:1.0.0
```

If you omit `maxModels`, the CRD default is `100` (valid range 1–10000).

When the limit is reached:

- No new models are created, even if more matching images exist
- Existing models are never deleted
- Status shows `modelsLimitReached: true`
- `availableModels` shows total images found vs `discoveredModels` created

**Use Cases:**

- Prevent accidental model explosion from overly broad filters
- Enforce resource quotas in multi-tenant environments
- Limit cluster resource consumption during initial sync

**Example Status:**

```yaml
status:
  status: Ready
  discoveredModels: 100
  availableModels: 250
  modelsLimitReached: true
  conditions:
    - type: MaxModelsLimitReached
      status: "True"
      message: "Model creation limit reached (100 models created). 150 available images not created as models."
```

## Status

The status field tracks sync progress and discovered models:

```bash
kubectl get aimclustermodelsource
```

```
NAME             STATUS   MODELS   LASTSYNC             AGE
amd-models   Ready    12       2025-01-15T10:30:00  2d
```

### Status Values

- **Pending**: Waiting for initial sync
- **Progressing**: Sync in progress
- **Ready**: All configured selectors succeeded
- **Degraded**: Some selectors failed, but others succeeded
- **Failed**: All selectors failed

### Detailed Status

```bash
kubectl get aimclustermodelsource amd-models -o yaml
```

Key status fields:

- `status`: Overall state (Ready, Degraded, Failed, etc.)
- `discoveredModels`: Count of AIMClusterModel resources created
- `availableModels`: Total count of images matching filters in registry
- `modelsLimitReached`: Boolean indicating if maxModels limit was reached
- `lastSyncTime`: Timestamp of last successful sync
- `conditions`: Detailed conditions including Ready, Degraded, and MaxModelsLimitReached

## Examples

### Docker Hub with Explicit Images

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: dockerhub-models
spec:
  registry: docker.io
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
    - amdenterpriseai/aim-deepseek-deepseek-r1:0.8.5
  syncInterval: 2h
```

### GitHub Container Registry with Version Constraints

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: ghcr-stable-models
spec:
  registry: ghcr.io
  images:
    - amdenterpriseai/aim-qwen-qwen3-32b
    - amdenterpriseai/aim-deepseek-deepseek-r1
  versions:
    - ">=0.8.0"
    - "<1.0.0"
  syncInterval: 1h
```

### Multiple Registries (advanced filters)

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: multi-registry-models
spec:
  registry: docker.io  # default
  filters:
    - image: amdenterpriseai/aim-qwen-qwen3-32b  # uses docker.io
    - image: ghcr.io/amdenterpriseai/aim-deepseek-deepseek-r1  # overrides to ghcr.io
  syncInterval: 1h
```

### Private Registry with Authentication

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: private-registry-creds
  namespace: aim-system
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: BASE64_ENCODED_CONFIG
---
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: private-models
spec:
  registry: private.registry.io
  imagePullSecrets:
    - name: private-registry-creds
  images:
    - myorg/model-a
    - myorg/model-b
  versions:
    - ">=1.0.0"
  syncInterval: 1h
```

### Specific Versions Only

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterModelSource
metadata:
  name: specific-versions
spec:
  registry: ghcr.io
  filters:
    - image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.5
    - image: amdenterpriseai/aim-qwen-qwen3-32b:0.8.4
    - image: amdenterpriseai/aim-deepseek-deepseek-r1:0.8.5
  syncInterval: 6h
```

## Lifecycle

### Created Models

Model sources create `AIMClusterModel` resources with auto-generated names based on the image URI. These models are owned by the source via an owner reference.

Created models use the v1alpha2 shape (`spec.image` set) and immediately enter the **official flow** — the model controller runs in-cluster image discovery and materialises one `AIMClusterProfile` per supported (accelerator, precision, metric) combination. See [AIM Models — Flow 1](models.md#flow-1-official-aim-model) for the lifecycle.

During the v1alpha1 → v1alpha2 migration window the same model **also runs the legacy OCI-discovery path** in parallel, emitting one `AIMClusterServiceTemplate` per `RecommendedDeployment` from the image's metadata so v1alpha1-shaped services still resolve. The two output sets (profiles and templates) coexist and describe the same image — pick the one that matches the consumer pipeline. To suppress the legacy template emission on a per-model basis, set `spec.discovery.createServiceTemplates: false`. See [Migration window](../admin/upgrading.md#migration-window) for the dispatch story.

If a discovered image is actually a generic AIM base image (its profile YAMLs leave `model_id` and `model_sources` empty), the resulting model produces base profiles instead — see [Base-image models](models.md#base-image-models) for how to spot one (`status.managedProfiles.base > 0`).

### Append-Only

Model sources follow an append-only lifecycle during normal operation. Once created, models are never deleted by the source, even if:

- The image is removed from the registry
- The filter is changed or removed

This ensures running services aren't disrupted when registry contents change.

### Ownership and Deletion

Created models have an owner reference to the source. **When you delete the source, Kubernetes will automatically delete all models that were created by it.**

This cascading deletion happens via Kubernetes garbage collection. To prevent accidentally disrupting running services, consider the impact before deleting a model source.

If you need to stop tracking specific models:

1. Update the source filters to exclude those models
   (or remove entries from `images` if using explicit list mode)
2. Delete the unwanted models manually:

```bash
kubectl delete aimclustermodel <model-name>
```

Note: You cannot selectively clean up models while keeping the source unchanged - any models matching the active filters will be recreated on the next sync.

## Troubleshooting

### No Models Discovered

Check the source status:

```bash
kubectl get aimclustermodelsource <name> -o yaml
```

Common causes:

- No images match configured `images` / `filters`
- Registry is unreachable
- Authentication failed (check imagePullSecrets)
- Version constraints too restrictive

### Degraded Status

Some selectors failed while others succeeded. Check conditions:

```bash
kubectl get aimclustermodelsource <name> -o jsonpath='{.status.conditions}'
```

Look for error messages indicating which selectors failed and why.

### Failed Status

All selectors failed. Common causes:

- Invalid registry hostname
- Missing or invalid imagePullSecrets
- Network connectivity issues
- Wildcard syntax (`*`) is unsupported; use explicit `images` or explicit `filters[].image`

## Related Documentation

- [AIM Models](models.md) — The three model onboarding flows (v1alpha2)
- [Profiles](profiles.md) — What gets produced by discovery of a source-created model
- [Model Catalog](../guides/model-catalog.md) — Browse and apply discovered models
- [Runtime Configuration](runtime-config.md) — Authentication and downloader defaults
- [Service Templates (v1alpha1)](../legacy/service-templates.md) — The legacy template path that source-created models also emit during the deprecation window
