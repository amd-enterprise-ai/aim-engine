# KV Cache

KV Cache (Key-Value Cache) is a performance optimization technique for Large Language Model (LLM) inference that significantly improves throughput and reduces latency by caching intermediate computation results.

## Overview

During LLM inference, the model processes input tokens and generates attention key-value pairs. These key-value pairs can be cached and reused across requests that share common prompt prefixes, eliminating redundant computation and dramatically improving performance for common use cases like:

- Chat applications with system prompts
- RAG (Retrieval Augmented Generation) with shared context
- Code completion with common boilerplate
- Batch processing with template prefixes

## Architecture


```
┌─────────────────┐
│   AIMService    │  References or creates
│                 │ ────────────────────┐
└─────────────────┘                     │
                                        ▼
                              ┌──────────────────┐
                              │  AIMKVCache      │
                              │  (Custom Resource)│
                              └─────────┬────────┘
                                        │ Creates & manages
                                        ▼
                              ┌──────────────────┐
                              │   StatefulSet    │
                              │   + Service      │
                              │                  │
                              │  Redis  │
                              │  Backend         │
                              └──────────────────┘
```

### Components

**AIMService**
- Specifies KV cache requirements via `spec.kvCache`
- Can create a new KV cache or reference an existing one
- Receives KV cache endpoint configuration automatically

**AIMKVCache (Custom Resource)**
- Manages the lifecycle of a KV cache backend
- Creates and maintains a StatefulSet with persistent storage
- Provides a stable Service endpoint for cache access
- Supports Redis backends

**Backend (StatefulSet)**
- Runs the actual KV cache storage (Redis)
- Uses persistent volumes for durability
- Provides network endpoint for cache operations

## Lifecycle Management

### Creation Patterns

**Pattern 1: AIMService Creates KV Cache**
When an `AIMService` specifies `kvCache.type` a new `AIMKVCache` resource is automatically created. This kvcache can be used by other services but is garbage collected when the `AIMService` is deleted.

**Pattern 2: Manually created KV Cache**
A manually created `AIMKVCache` can safely be referenced by multiple `AIMService` resources in the same namespace by specifying `kvCache.name`.

### Ownership

- When an `AIMService` creates a KV cache (Pattern 1), it owns the cache resource
- The KV cache's lifecycle is tied to the owning service
- When referencing an existing cache (Pattern 2), the cache is independent and can outlive the service

### States

An `AIMKVCache` progresses through the following states:

1. **Pending** - Resource created, StatefulSet creation queued
2. **Progressing** - StatefulSet and Service being deployed, waiting for pods to be ready
3. **Ready** - Backend is running and available for use
4. **Failed** - Deployment encountered an error (check conditions for details)

### Status Information

The `AIMKVCache` status provides comprehensive information about the cache state:

**Basic Information**:
- `status` - Current state (Pending, Progressing, Ready, Failed)
- `statefulSetName` - Name of the managed StatefulSet
- `serviceName` - Name of the Kubernetes Service providing network access

**Operational Metrics**:
- `endpoint` - Connection string for accessing the cache (e.g., `redis://service-name:6379`)
- `replicas` - Total number of replicas configured
- `readyReplicas` - Number of replicas currently ready and serving
- `storageSize` - Allocated storage capacity (e.g., `50Gi`)

## Storage Considerations

### Sizing

The storage size for a KV cache depends on several factors:

- **Model size**: Larger models have bigger key-value tensors
- **Context length**: Longer contexts require more cache storage
- **Batch size**: Higher batch sizes increase cache requirements
- **Expected request volume**: More concurrent requests need more cache space

Monitor actual usage and adjust accordingly.

### Storage Classes

The KV cache uses Kubernetes `PersistentVolumeClaims` for durable storage. If no `storageClassName` is specified, the cluster's default storage class is used.

**Recommendations**:
- Use SSD-backed storage for better performance
- Ensure the storage class supports `ReadWriteOnce` access mode
- Verify sufficient storage quota in your namespace

## Backend Types

### Redis

Redis is the default and currently only supported backend. It provides:
- High-performance in-memory caching with disk persistence
- Mature, battle-tested reliability
- Straightforward configuration

## Best Practices

1. **Size appropriately**: Start with recommended sizes and monitor actual usage
2. **Share when possible**: Use shared caches for services with overlapping use cases
3. **Monitor storage**: Set up alerts for storage capacity
4. **Use fast storage**: SSD-backed storage classes provide best performance
5. **Plan for growth**: KV cache storage needs grow with traffic volume

### Example

```
apiVersion: aim.silogen.ai/v1alpha1
kind: AIMService
metadata:
  name: llama-with-custom-kvcache
  namespace: default
spec:
  model:
    image: ghcr.io/silogen/aim-meta-llama-llama-3-1-8b-instruct:0.7.0
  kvCache:
    name: llama-kvcache
    type: redis
    image: redis:latest
    resources:
      limits:
        cpu: "0.8"
        memory: 2Gi
    lmCacheConfig: |
      local_cpu: false
      chunk_size: 100
      max_local_cpu_size: 1.0
      remote_url: "redis://{SERVICE_URL}"
      remote_serde: "naive"
    storage:
      storageClassName: rwx-nfs
```

Note that {SERVICE_URL} placeholder will automatically be updated with the proper URL by the AIM Engine when the service starts.

## See Also

- [KV Cache Usage Guide](../usage/kv-cache.md) - Practical examples and configuration
- [Deploying Inference Services](../usage/services.md) - AIMService configuration
- [Runtime Configuration](runtime-config.md) - Additional service configuration
