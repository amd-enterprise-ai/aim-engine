# Observation Design

Guidelines for structuring your observation types.

## The Observation Step is Often Optional

For many controllers, the observation struct can be a thin wrapper around the fetch result. In the future it may be made option, allowing you to implement `GetComponentHealth()` directly on the fetch result.

```go
// Option 1: Thin wrapper (embed fetch result)
type MyObservation struct {
    MyFetch
}

func (r *Reconciler) ComposeState(ctx, reconcileCtx, fetch) MyObservation {
    return MyObservation{MyFetch: fetch}
}

// Option 2: Implement directly on fetch result (skip observation entirely)
func (fetch MyFetch) GetComponentHealth() []controllerutils.ComponentHealth {
    return []controllerutils.ComponentHealth{
        fetch.Model.ToComponentHealth("Model", inspectModel),
    }
}
```

Use a separate observation struct only when you need to:
- Perform expensive computations once and cache results
- Add derived state or boolean checks
- Encapsulate complex domain logic

---

## Fields vs Methods

**Rule of thumb**: Prefer methods to fields for derived state.

### Use Fields For

- Raw fetched resources you'll need in PlanResources
- Expensive computations you want to cache
- State that comes directly from fetch results

```go
type MyObservation struct {
    // Store for use in PlanResources
    template *Template

    // ComponentHealth from fetches
    modelHealth    ComponentHealth
    templateHealth ComponentHealth
}
```

### Use Methods For

- Derived boolean checks
- Logic that encapsulates domain knowledge
- Anything computed from existing fields

```go
func (obs MyObservation) AllDependenciesReady() bool {
    return obs.modelHealth.GetState() == Ready &&
           obs.templateHealth.GetState() == Ready
}

func (obs MyObservation) NeedsScaleUp(desired int32) bool {
    return obs.currentReplicas < desired
}
```

**Benefits**:
- Clear, self-documenting code in PlanResources
- Easy to test logic in isolation
- No struct bloat with one-off booleans

---

## Decision Checklist

When adding to your observation, ask:

### 1. Is this describing the current world or a future action?
- **Current world** → Observation
- **Future action** → PlanResources

### 2. Will this be used in multiple places?
- **Yes** → Field or method
- **No** → Compute inline in PlanResources

### 3. Does this encapsulate non-trivial logic?
- **Yes** → Method with clear name
- **No** → Maybe inline

### 4. Is it expensive to compute?
- **Yes** → Store in field
- **No** → Use a method

### 5. Would a teammate understand without reading internals?
- **No** → Reconsider or use a well-named method

---

## Example: Good Observation Design

```go
type ServiceObservation struct {
    // Fields: Raw state and component health
    modelHealth    ComponentHealth
    templateHealth ComponentHealth
    workloadHealth ComponentHealth
    template       *Template  // Stored for PlanResources

    currentReplicas int32
    desiredReplicas int32
}

// Methods: Derived state
func (obs ServiceObservation) GetComponentHealth() []ComponentHealth {
    return []ComponentHealth{
        obs.modelHealth,
        obs.templateHealth,
        obs.workloadHealth,
    }
}

func (obs ServiceObservation) AllDependenciesReady() bool {
    return obs.modelHealth.GetState() == Ready &&
           obs.templateHealth.GetState() == Ready
}

func (obs ServiceObservation) NeedsScaling() bool {
    return obs.currentReplicas != obs.desiredReplicas
}

func (obs ServiceObservation) CanDeploy() bool {
    return obs.AllDependenciesReady() && !obs.NeedsScaling()
}
```

Then in PlanResources:

```go
func (r *Reconciler) PlanResources(ctx, obj, obs) PlanResult {
    var apply []client.Object

    if obs.CanDeploy() {
        apply = append(apply, buildWorkload(obj, obs.template))
    }

    return PlanResult{Apply: apply}
}
```

Clean, readable, testable.

---

## Context-Aware Health Inspection

For advanced health checks that need to inspect pod logs or fetch additional resources, implement `GetComponentHealth` with context and clientset parameters:

```go
// Standard signature (no context)
func (obs MyObservation) GetComponentHealth() []controllerutils.ComponentHealth {
    return []controllerutils.ComponentHealth{
        obs.Job.ToComponentHealth("Job", controllerutils.GetJobHealth),
    }
}

// Context-aware signature (with clientset for log inspection)
func (obs MyObservation) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
    return []controllerutils.ComponentHealth{
        obs.Job.ToComponentHealth("Job", controllerutils.GetJobHealth),
        obs.Pods.ToComponentHealthWithContext(ctx, clientset, "Pods", controllerutils.GetPodsHealth),
    }
}
```

The pipeline automatically detects which signature you've implemented and calls it with the appropriate parameters.

### When to Use Context-Aware Pattern

Use the context-aware signature when you need:
- **Pod log inspection**: Categorize failures from log patterns (auth errors, storage exhaustion)
- **Additional API calls**: Fetch related resources not available in the initial fetch
- **Complex health checks**: PVC usage, service endpoint readiness, etc.

### Example: Conditional Health Checking

```go
func (fetch ArtifactFetch) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
    health := []controllerutils.ComponentHealth{
        fetch.RuntimeConfig.ToComponentHealth("RuntimeConfig", getRuntimeConfigHealth),
        fetch.PVC.ToComponentHealth("Storage", controllerutils.GetPvcHealth),
    }

    // Only check job/pods while download is in progress
    // Once Ready, job may be cleaned up by TTL - don't let its absence affect status
    if fetch.Object.Status.Status != constants.AIMStatusReady {
        health = append(health,
            fetch.Job.ToComponentHealth("DownloadJob", controllerutils.GetJobHealth),
            fetch.Pods.ToComponentHealthWithContext(ctx, clientset, "Pods", controllerutils.GetPodsHealth),
        )
    }

    return health
}
```

This pattern:
- Avoids spurious failures when cleanup happens
- Surfaces detailed error info during active operations
- Stops tracking ephemeral resources after success

### Health Inspector Utilities

The framework provides ready-to-use inspectors:

| Utility | Use For | Log Inspection | Detects |
|---------|---------|----------------|---------|
| `GetJobHealth` | Batch jobs | No | BackoffLimitExceeded, DeadlineExceeded, Evicted |
| `GetPodsHealth` | Pod lists | Yes (requires clientset) | Auth errors, storage exhaustion, OOM, image pull failures |
| `GetPvcHealth` | PVCs | No | Lost volumes, provisioning failures |

**Pod log inspection categories**:
- **Auth errors**: S3 credentials, HuggingFace tokens, registry auth
- **Storage exhaustion**: Disk full, quota exceeded, ENOSPC
- **Resource not found**: 404, Repository Not Found (HuggingFace)
- **OOM kills**: Memory limit exceeded

These inspectors return properly categorized errors that the state engine uses to set conditions (`AuthValid`, `ConfigValid`, etc.).
