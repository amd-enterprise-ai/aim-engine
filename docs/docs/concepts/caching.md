# Model Caching

AIM provides a hierarchical caching system that allows model artifacts to be pre-downloaded and shared across services in the same namespace. This document explains the caching architecture, resource lifecycle, and deletion behavior.

## Overview

Model caching in AIM uses three resource types:

1. **AIMArtifact**: Manages the model artifacts download process onto a PVC
2. **AIMTemplateCache**: Groups `AIMArtifacts` for a specific template and allows caching a cluster-scoped `AIMClusterServiceTemplate` into a specific namespace.
3. **AIMService**: Can trigger template cache creation via `spec.caching.mode: Shared|Dedicated`. See [AIM Services](./services.md) for more information.

### Shared and dedicated mode

An **AIMTemplateCache** can run in two modes, which differ by who creates it and how `AIMArtifacts` are owned:

- **Shared mode** (`spec.mode: Shared`, default): **AIMArtifact**s created by the template cache have **no** owner references. They persist independently and can be reused by other template caches or services. Used when the template creates the cache (template caching enabled; that template cache is template-owned) or when the service uses `spec.caching.mode: Shared` (service creates or reuses shared caches).
- **Dedicated mode** (`spec.mode: Dedicated`): **AIMArtifact**s are **owned** by the template cache. When the template cache is deleted, its artifacts are garbage-collected. Used when the service uses `spec.caching.mode: Dedicated`; the template cache is then owned by the service and cleaned up with it. Dedicated template caches and artifacts are only used by a single service and never shared.

## Caching Hierarchy

### Ownership Structure

**AIMTemplateCache** may be owned by an `AIMServiceTemplate`, by an `AIMService`, or by nothing (unowned). `AIMArtifact` are owned by the template cache only in Dedicated mode; in Shared mode they have no owner.

```
Who owns AIMTemplateCache (one of):
  • AIMServiceTemplate   (template-created, Shared)
  • AIMService           (service-created, Dedicated)
  • (none)               (service-created with Shared)

Resource hierarchy:
  AIMTemplateCache (Shared or Dedicated mode)
      └── AIMArtifact(s)  [created by template, owned by TemplateCache only if Dedicated]
              └── PVC(s) + Download Job(s) (owned by model cache)
```

### Creation Flow

An **AIMTemplateCache** is created by an **AIMServiceTemplate** (when the template has caching enabled and is ready), by an **AIMService** (when the service has caching enabled and no suitable cache exists), or **manually**. Ownership depends on the creator and mode: template-owned (template-created), service-owned (service-created with Dedicated), unowned (service-created with Shared), or no owner (manually created).

For each needed model (matching `sourceURI` and storage class), the **AIMTemplateCache** uses an existing artifact when possible, otherwise creates one. A **shared** template cache reuses any matching **shared** artifact in the namespace; a **dedicated** template cache uses its own dedicated artifact. New artifacts are created shared or dedicated according to the template cache's mode. The **AIMArtifact** handles the download.

## Cache Status Values

**AIMTemplateCache** and **AIMArtifact** use the same status values. The template cache's status is typically derived from its artifacts.

| Status | Description |
| ------ | ----------- |
| `Pending` | Created, waiting for processing |
| `Progressing` | Download or provisioning in progress |
| `Ready` | Ready and can be used |
| `Degraded` | Partially available or limited (e.g. some artifacts failed) |
| `NotAvailable` | Dependencies not available. **AIMTemplateCache** may report this when its template is not available (e.g. GPU not ready); **AIMArtifact** never sets this. |
| `Failed` | Creation failed (download error, storage issue, etc.) |

A `Failed` `AIMArtifact` retries the download periodically, so its status may change over time.

## Deletion Behavior

Deletion follows Kubernetes ownership: owned resources are garbage-collected when the owner is deleted. AIM finalizers additionally delete non-Ready caches so that Failed/Pending caches do not block recreation. Manually created AIMTemplateCaches and AIMArtifact (no owner) are never garbage-collected.

### When AIMService is deleted

- **Template caches owned by the service** (Dedicated, service-created): Garbage-collected with the service.
- **Service finalizer**: Deletes any template caches created by this service (by label) that are **not Ready**, so stuck Pending/Progressing/Failed caches do not block a future service from creating a new cache.
- **Template caches not owned by the service** (template-owned or unowned Shared): Unchanged; they persist and can be reused by other services.

### When AIMServiceTemplate is deleted

- **Template caches owned by the template**: When a template creates a cache, the cache is automatically deleted when the template is deleted via Kubernetes garbage collection. Template-created caches use Shared mode by default, so the cached artifacts themselves persist even after the cache is removed.

### When AIMTemplateCache is deleted

- **Template cache finalizer**: Ensure that **artifacts** created by this template cache (by label) that are **not Ready** are deleted. **Ready** model caches are left in place so they can be reused by other template caches.
- The template cache is then removed.

If a service with caching enabled was using this template cache, a new template cache will be created automatically, provided the template itself is still healthy and ready.

### When AIMArtifact is deleted

- The **PVC** and any **download Job** owned by the artifact is marked for garbage-collection.
- Any AIMService pod still using that cache keeps the PVC mounted until the pod is gone.

## Cache Reuse

**Shared** artifacts are **deduplicated per namespace**: if two **shared** template caches request the same source (e.g. same `sourceURI` and storage class), the download runs once and both use the same artifact and PVC. Dedicated template caches only reuse artifacts they already own, so they do not share artifacts across caches.

### Automatic Reuse

Services automatically detect and use existing caches:

1. Service resolves its template
2. Controller looks for `AIMTemplateCache` matching the template. If `AIMTemplateCache` isn't available, the service waits until it is.
3. PVCs from the AIMArtifacts linked in the AIMTemplateCache are mounted into the InferenceService.
4. No re-download is needed

### Cross-Service Sharing

Multiple services can share the same cached models:

- Services using the same template reference the same `AIMTemplateCache`
- artifacts are identified by `sourceURI`, enabling reuse across templates

## Manual Cache Management

* To manually make sure a model is available create an AIMArtifact for that model.
* To make sure all models that belong to a AIMServiceTemplate or AIMClusterServiceTemplate is available, create an AIMTemplateCache with correctly set templateName in the namespace.
* **Cleanup**: `Ready` AIMArtifacts that have **no owner** (Shared, or manually created) are not garbage-collected; delete them manually if you want to free space. Artifacts owned by a template cache (Dedicated) are removed when that template cache is deleted.

### AIMService with cache enabled

```
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMService
metadata:
  name: qwen-chat
  namespace: ml-team
  labels:
    project: conversational-ai
spec:
  model:
    ref: qwen-qwen3-32b
  caching:
    mode: Shared   # default; use Dedicated for service-owned caches
```

#### AIMTemplateCache to prepopulate the namespace with caches for a AIMServiceTemplate

```
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMTemplateCache
metadata:
  name: template-cache
spec:
  templateName: name-of-service-template
```

#### AIMArtifact that uses the kserve downloader with XET disabled

```
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: kserve-smollm2-135mx
spec:
  modelDownloadImage: kserve/storage-initializer:v0.16.0
  env:
    - name: HF_HUB_DISABLE_XET
      value: "1"
  sourceUri: hf://HuggingFaceTB/SmolLM2-135Mx
  size: 500M
  storageClassName: rwx-nfs
```

## Download Protocol Strategy

AIMArtifact downloads from HuggingFace support multiple download protocols. The operator automatically tries protocols in a configurable sequence, falling back to the next protocol if the current one fails. The main reason for this approach is that some models require XET for parts of the download, while XET seems to have a hard time handling network instability in certain environments. A mixed protocol approach where different protocols are tried in sequence is default to alliviate this, but the default behavior can be changed by setting the AIM_DOWNLOADER_PROTOCOL in the default AIMClusterRuntimeConfig.

### Supported Protocols

| Protocol | Description |
| -------- | ----------- |
| `XET` | HuggingFace's content-addressable chunk-based protocol. Default in `huggingface_hub` >= 0.32. Efficient for large files with deduplication. |
| `HF_TRANSFER` | Rust-based parallel HTTP downloader (deprecated by HuggingFace in favor of XET). |
| `HTTP` | Standard HTTP range-request downloads. Most compatible, no extra dependencies. |

### Configuration

The download strategy is controlled by the `AIM_DOWNLOADER_PROTOCOL` environment variable, which specifies a comma-separated sequence of protocols to try in order.

**Default**: `XET,HF_TRANSFER`

This can be overridden at three levels (highest precedence first):

1. **Per-artifact** via `AIMArtifact.spec.env`
2. **Per-namespace** via `AIMRuntimeConfig.spec.env`
3. **Cluster-wide** via `AIMClusterRuntimeConfig.spec.env`

#### Example: Override per artifact

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMArtifact
metadata:
  name: my-model
spec:
  sourceUri: hf://Qwen/Qwen3-32B
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "HTTP"
```

#### Example: Cluster-wide default

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  env:
    - name: AIM_DOWNLOADER_PROTOCOL
      value: "XET,XET,HTTP"
```

### Observing Download Status

During downloads, the artifact's `status.download` field is updated by the downloader pod with protocol attempt metadata:

```yaml
status:
  download:
    protocol: HTTP          # Currently active protocol
    attempt: 2              # Current attempt number (1-based)
    totalAttempts: 3        # Total attempts in the sequence
    protocolSequence: "XET,XET,HTTP"
    message: Complete       # Human-readable status
```

View these fields with:

```bash
kubectl get aimart -o wide          # Protocol and Attempt columns (priority=1)
kubectl get aimart my-model -o yaml # Full status.download details
```

### How Protocol Switching Works

1. The downloader iterates through the protocol sequence left to right
2. For each protocol, the appropriate HuggingFace environment variables are set (`HF_HUB_DISABLE_XET`, `HF_HUB_ENABLE_HF_TRANSFER`)
3. If a protocol fails, any `.incomplete` files are cleaned before switching to the next protocol
4. Already-completed files are skipped regardless of protocol (metadata-based)
5. If all protocols are exhausted, the Job fails and Kubernetes retries via `backoffLimit`

## Related Documentation

- [Templates](templates.md) - Understanding ServiceTemplates and discovery
- [Services](../guides/deploying-services.md) - Deploying services with caching
- [Runtime Configuration](runtime-config.md) - Cluster-wide and namespace-scoped configuration

