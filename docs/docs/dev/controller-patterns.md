# Controller Patterns

This guide explains how to implement controllers in AIM Engine using our standardized reconciliation pattern.

## Quick Start

**TL;DR**: Implement these methods and you're done:

```go
// 1. Fetch resources and implement GetComponentHealth
func (r *Reconciler) FetchRemoteState(
    ctx context.Context,
    c client.Client,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
) MyFetchResult {
    pvc := &corev1.PersistentVolumeClaim{}

    return MyFetchResult{
        object:              reconcileCtx.Object,
		// The runtime config is fetched by the pipeline automatically
        mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
		
		// Use the utility Fetch function to fetch a Kubernetes resource, automatically wrapping any errors
        pvc: controllerutils.Fetch(
            ctx, c,
            client.ObjectKey{Name: getPvcName(reconcileCtx.Object), Namespace: reconcileCtx.Object.Namespace},
            pvc,
        ),
    }
}

// Implement GetComponentHealth directly on the fetch result.
// For standard components you can use an existing health fetcher, or create a new one yourself
func (result MyFetchResult) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
    return []controllerutils.ComponentHealth{
        result.mergedRuntimeConfig.ToComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
        result.pvc.ToComponentHealth("Storage", controllerutils.GetPvcHealth),
    }
}

// 2. ComposeState is a thin passthrough (might be removed in the future)
type MyObservation struct {
    MyFetchResult
}

func (r *Reconciler) ComposeState(
    ctx context.Context,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
    fetch MyFetchResult,
) MyObservation {
    return MyObservation{MyFetchResult: fetch}
}

// 3. Plan desired state
func (r *Reconciler) PlanResources(
    ctx context.Context,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
    obs MyObservation,
) controllerutils.PlanResult {
    result := controllerutils.PlanResult{}
	
	// Add a resource to the list of resource to apply
    result.Apply(buildInferenceService(reconcileCtx.Object))
    return result
}
```

**That's it.** The state engine handles the rest automatically.

---

**Note on ComposeState/Observation**: ComposeState currently just wraps the fetch result in an observation struct. This is a thin passthrough that keeps the door open for more complex observation logic in the future, but it may be removed if this structure proves sufficient. The real work happens in FetchRemoteState and GetComponentHealth.

---

## What You Get Automatically

When you implement `GetComponentHealth()`, the state engine automatically:

- ✅ Creates component conditions (ModelReady, TemplateReady, etc.)
- ✅ Creates parent conditions when needed (AuthValid, ConfigValid, DependenciesReachable) based on any errors encountered
- ✅ Sets Ready condition and status field
- ✅ Categorizes errors and decides requeue behavior
- ✅ Applies 10-second grace period for transient errors
- ✅ Emits events and logs when conditions change (and recurring ones for errors)

---

## Three Approaches

Choose based on how much control you need:

### 1. Fully Automatic (Recommended)

Just implement `GetComponentHealth()`.

**Use when**: Standard component tracking is all you need.

### 2. Automatic + Decorator

Implement `GetComponentHealth()` + `DecorateStatus()` to add custom fields.

**Use when**: You need automatic state management but want to add domain-specific status fields like `ResolvedTemplate`.

```go
func (r *Reconciler) DecorateStatus(status *MyStatus, cm *ConditionManager, obs MyObservation) {
    // State engine already set ModelReady, Ready, status.Status
    // Just add your custom fields
    if obs.template != nil {
        status.ResolvedTemplate = &ResolvedReference{Name: obs.template.Name}
    }
}
```

### 3. Fully Manual

Implement `SetStatus()` to control everything yourself.

**Use when**: Your status logic doesn't fit the component health model.

```go
func (r *Reconciler) SetStatus(status *MyStatus, cm *ConditionManager, obs MyObservation) {
    // You set ALL conditions and status.Status yourself
    h := controllerutils.NewStatusHelper(status, cm)
    h.Ready("AllGood", "Everything is working")
    cm.MarkTrue("CustomCondition", "Reason", "Message", controllerutils.AsInfo())
}
```

---

## The Four Phases

Every reconciliation follows this pattern:

### 1. FetchRemoteState

Fetch resources from Kubernetes. Use `FetchResult[T]` wrapper:

```go
type MyFetch struct {
    Model    controllerutils.FetchResult[*aimv1.AIMModel]
    Template controllerutils.FetchResult[*aimv1.AIMServiceTemplate]
    Pods     controllerutils.FetchResult[*corev1.PodList]
}

func (r *Reconciler) FetchRemoteState(
    ctx context.Context,
    c client.Client,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
) MyFetch {
    return MyFetch{
        Model:    controllerutils.Fetch(ctx, c, modelKey, &aimv1.AIMModel{}),
        Template: controllerutils.Fetch(ctx, c, templateKey, &aimv1.AIMServiceTemplate{}),
        Pods:     controllerutils.FetchList(ctx, c, &corev1.PodList{}, client.InNamespace(reconcileCtx.Object.Namespace)),
    }
}
```

**Key points:**
- Use `FetchResult[T]` wrapper for convenient error handling and component health conversion
- All remote API calls should happen here (no client calls in ComposeState or PlanResources)
- The `reconcileCtx` parameter provides access to the object and merged runtime config
- This separation enables easy mocking for testing

### 2. ComposeState

**Current pattern**: This is a thin passthrough that wraps the fetch result. This keeps the door open for more complex observation logic in the future, but may be removed if this structure proves sufficient.

```go
type MyObservation struct {
    MyFetchResult  // Embed the fetch result
}

func (r *Reconciler) ComposeState(
    ctx context.Context,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
    fetch MyFetchResult,
) MyObservation {
    return MyObservation{MyFetchResult: fetch}
}
```

**The real work happens in GetComponentHealth**, which you implement on the fetch result:

```go
func (result MyFetchResult) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
    health := []controllerutils.ComponentHealth{
        result.mergedRuntimeConfig.ToComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
        result.pvc.ToComponentHealth("Storage", controllerutils.GetPvcHealth),
    }

    // Conditional health checking based on status
    if result.object.Status.Status != constants.AIMStatusReady {
        if result.downloadJob != nil {
            health = append(health, result.downloadJob.ToComponentHealth("DownloadJob", controllerutils.GetJobHealth))
        }
        if result.downloadJobPods != nil {
            health = append(health, result.downloadJobPods.ToComponentHealthWithContext(ctx, clientset, "Pods", controllerutils.GetPodsHealth))
        }
    }

    return health
}
```

**Health Inspection Utilities:**

The framework provides ready-to-use health inspectors for common Kubernetes resources:

```go
// For Jobs
result.Job.ToComponentHealth("DownloadJob", controllerutils.GetJobHealth)

// For PVCs
result.PVC.ToComponentHealth("Storage", controllerutils.GetPvcHealth)

// For Pods (requires context and clientset for log inspection)
result.Pods.ToComponentHealthWithContext(ctx, clientset, "Workload", controllerutils.GetPodsHealth)
```

These utilities automatically categorize errors (auth failures, storage exhaustion, etc.) and set appropriate states.

**Custom inspection logic:**

```go
result.Model.ToComponentHealth("Model", func(m *aimv1.AIMModel) controllerutils.ComponentHealth {
    if m.Status.Status == constants.AIMStatusReady {
        return controllerutils.ComponentHealth{
            State: constants.AIMStatusReady,
            Reason: "ModelReady",
            Message: "Model is ready"
        }
    }
    return controllerutils.ComponentHealth{
        State: constants.AIMStatusProgressing,
        Reason: "ModelNotReady",
        Message: "Waiting for model"
    }
})
```

### 3. PlanResources

Decide what to create/update/delete. This is a **pure function** - no client calls, just derive desired state based on observations.

```go
func (r *Reconciler) PlanResources(
    ctx context.Context,
    reconcileCtx controllerutils.ReconcileContext[*aimv1.MyResource],
    obs MyObservation,
) controllerutils.PlanResult {
    obj := reconcileCtx.Object
    runtimeConfig := reconcileCtx.MergedRuntimeConfig.Value

    result := controllerutils.PlanResult{}

    // Only create resources when dependencies are ready
    if obs.Model.OK() && obs.Model.Value.Status.Status == constants.AIMStatusReady {
        inferenceService := buildInferenceService(obj, runtimeConfig)
        result.Apply(inferenceService)  // Creates/updates with owner reference
    }

    // Conditional resource creation
    if obj.Status.Status != constants.AIMStatusReady && obs.Job.IsNotFound() {
        job := buildDownloadJob(obj, runtimeConfig)
        result.Apply(job)
    }

    // Shared resources (no owner reference)
    configMap := buildSharedConfig(obj)
    result.ApplyWithoutOwnerRef(configMap)

    // Cleanup
    if shouldCleanup(obs) {
        oldResource := getOldResource(obj)
        result.Delete(oldResource)
    }

    return result
}
```

**Key methods:**
- `result.Apply(obj)` - Creates/updates with owner reference (garbage collected when owner deleted)
- `result.ApplyWithoutOwnerRef(obj)` - Creates/updates without owner reference (survives owner deletion)
- `result.Delete(obj)` - Deletes the resource

### 4. (Optional) DecorateStatus

Add custom status fields:

```go
func (r *Reconciler) DecorateStatus(status *MyStatus, cm *ConditionManager, obs MyObservation) {
    // Add domain-specific fields here
}
```

---

## Context-Aware Health Inspection

For advanced health checks that need to inspect logs or additional resources, use the context-aware pattern:

```go
// Implement GetComponentHealth with context and clientset parameters
func (fetch MyFetch) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
    health := []controllerutils.ComponentHealth{
        fetch.RuntimeConfig.ToComponentHealth("RuntimeConfig", getRuntimeConfigHealth),
        fetch.PVC.ToComponentHealth("Storage", controllerutils.GetPvcHealth),
    }

    // Conditionally check job/pods based on status
    if fetch.Object.Status.Status != constants.AIMStatusReady {
        health = append(health,
            fetch.Job.ToComponentHealth("DownloadJob", controllerutils.GetJobHealth),
            fetch.Pods.ToComponentHealthWithContext(ctx, clientset, "Pods", controllerutils.GetPodsHealth),
        )
    }

    return health
}
```

The pipeline will detect and call this signature automatically, passing context and clientset from the Pipeline configuration.

**Pod Health Inspection**: The `GetPodsHealth` utility inspects pod logs to categorize failures:
- Auth errors (S3 credentials, HuggingFace tokens)
- Storage exhaustion (disk full, quota exceeded)
- Resource not found (404, missing models)
- OOM kills and other termination reasons

---

## Error Handling

The state engine automatically categorizes errors:

| Error Type | Behavior | Sets Condition | Example |
|------------|----------|----------------|---------|
| **Infrastructure** | Requeue with backoff, 10s grace period | `DependenciesReachable=False` | Network timeout, API server down |
| **Auth** | Stop apply, fail resource | `AuthValid=False` | Forbidden (403), invalid credentials |
| **InvalidSpec** | Stop apply, fail resource | `ConfigValid=False` | Invalid configuration, conflicts |
| **MissingUpstreamDependency** | Stop apply, fail resource | `ConfigValid=False` | User-referenced config/secret not found |
| **MissingDownstreamDependency** | Mark Progressing, continue | Component condition | Internal resource not ready yet |
| **ResourceExhaustion** | Stop apply, fail resource | Component condition | OOM, disk full, quota exceeded |

Just return errors in `ComponentHealth.Errors` - categorization is automatic.

**Distinction**:
- **MissingDownstreamDependency**: Resources the controller is creating (pods starting, jobs running) - transient, expected to self-heal
- **MissingUpstreamDependency**: User-referenced resources (configs, secrets) - requires user intervention

---

## Observability

If you need to report custom conditions in the status update methods, you can use the ConditionManager helper.

Control event and log emission per condition:

```go
// Info log + Normal event on transition (default)
cm.MarkTrue(condType, reason, msg)

// Silent
cm.MarkTrue(condType, reason, msg, controllerutils.Silent())

// Error log + Warning event on transition
cm.MarkFalse(condType, reason, msg, controllerutils.AsWarning())

// Error log + Warning event EVERY reconcile (for critical errors)
cm.MarkFalse(condType, reason, msg, controllerutils.AsError())
```

---

## Quick Reference

**Interfaces to implement**:

```go
// Required - always implement
type DomainReconciler[T, S, F, Obs] interface {
    FetchRemoteState(ctx, client, obj) F
    ComposeState(ctx, obj, fetched) Obs
    PlanResources(ctx, obj, obs) PlanResult
}

// Automatic mode - implement in observation type
type ComponentHealthProvider interface {
    GetComponentHealth() []ComponentHealth
}

// Optional - extend automatic status
type StatusDecorator[T, S, Obs] interface {
    DecorateStatus(status, cm, obs)
}

// Manual mode - full control
type ManualStatusController[T, S, Obs] interface {
    SetStatus(status, cm, obs)
}
```

**When to use what**:

| Need | Use |
|------|-----|
| Standard component tracking | Approach 1: GetComponentHealth() |
| Custom status fields | Approach 2: GetComponentHealth() + DecorateStatus() |
| Full control | Approach 3: SetStatus() |
