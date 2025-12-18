# Controller Patterns

This guide explains how to implement controllers in AIM Engine using our standardized reconciliation pattern.

## Quick Start

**TL;DR**: Implement these three methods and you're done:

```go
// 1. Fetch resources
func (r *Reconciler) FetchRemoteState(ctx, client, obj) MyFetch {
    return MyFetch{
        Model: controllerutils.Fetch(ctx, client, modelKey, &Model{}),
    }
}

// 2. Compose observations and return component health
func (r *Reconciler) ComposeState(ctx, obj, fetch) MyObservation {
    obs := MyObservation{}
    obs.modelHealth = fetch.Model.ToComponentHealth("Model", func(m *Model) ComponentState {
        if m.Status.Status == "Ready" {
            return ComponentState{State: Ready, Reason: "Ready", Message: "Model ready"}
        }
        return ComponentState{State: Progressing, Reason: "NotReady", Message: "Waiting"}
    })
    return obs
}

func (obs MyObservation) GetComponentHealth() []ComponentHealth {
    return []ComponentHealth{obs.modelHealth}
}

// 3. Plan desired state
func (r *Reconciler) PlanResources(ctx, obj, obs) PlanResult {
    return PlanResult{Apply: []client.Object{/* your resources */}}
}
```

**That's it.** The state engine handles the rest automatically.

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
    Model    controllerutils.FetchResult[*Model]
    Template controllerutils.FetchResult[*Template]
}

func (r *Reconciler) FetchRemoteState(ctx, client, obj) MyFetch {
    return MyFetch{
        Model:    controllerutils.Fetch(ctx, client, modelKey, &Model{}),
        Template: controllerutils.Fetch(ctx, client, templateKey, &Template{}),
    }
}
```

You don't have to use the `FetchResult` wrapper, but it provides a convenient way to wrap an associated error, and a method to convert it to a component health struct. The key part is that *all remote connectivity* should happen in this method only, so that the result (in this case `MyFetch`) can be mocked for testing the rest of the flow.

### 2. ComposeState

Interpret fetched resources. Use `ToComponentHealth()` to convert fetch results, or create your own conversion functions.

```go
type MyObservation struct {
    modelHealth    ComponentHealth
    templateHealth ComponentHealth
}

func (r *Reconciler) ComposeState(ctx, obj, fetch) MyObservation {
    obs := MyObservation{}

    obs.modelHealth = fetch.Model.ToComponentHealth("Model", func(m *Model) ComponentState {
		// If this function is called, there was no API / fetch error, and we must determine what the state is
        if m.Status.Status == "Ready" {
            return ComponentState{State: Ready, Reason: "Ready", Message: "Model ready"}
        }
        return ComponentState{State: Progressing, Reason: "NotReady", Message: "Waiting"}
    })

    obs.templateHealth = fetch.Template.ToComponentHealth("Template", func(t *Template) ComponentState {
        // Same pattern...
    })

    return obs
}

func (obs MyObservation) GetComponentHealth() []ComponentHealth {
    return []ComponentHealth{obs.modelHealth, obs.templateHealth}
}
```

Again, if you prefer manual control over the status update logic, you can just form your own observation struct, return that, and then update your status yourself in the `SetStatus` method.

### 3. PlanResources

Decide what to create/update/delete:

```go
func (r *Reconciler) PlanResources(ctx, obj, obs) PlanResult {
    var apply []client.Object

    // Only create InferenceService if dependencies are ready
    if obs.modelHealth.GetState() == Ready && obs.templateHealth.GetState() == Ready {
        apply = append(apply, buildInferenceService(obj))
    }

    return PlanResult{Apply: apply}
}
```

### 4. (Optional) DecorateStatus

Add custom status fields:

```go
func (r *Reconciler) DecorateStatus(status *MyStatus, cm *ConditionManager, obs MyObservation) {
    // Add domain-specific fields here
}
```

---

## Error Handling

The state engine automatically categorizes errors:

| Error Type | Behavior | Example |
|------------|----------|---------|
| **Infrastructure** | Requeue with backoff, 10s grace period | Network timeout, API server down |
| **Auth** | Stop apply, fail resource | Forbidden (403) |
| **InvalidSpec** | Stop apply, fail resource | Invalid configuration |
| **MissingDependency** | Mark Progressing | NotFound (404) |

Just return errors in `ComponentHealth.Errors` - categorization is automatic.

---

## Common Patterns

### Custom Validation

```go
func (r *Reconciler) ComposeState(ctx, obj, fetch) MyObservation {
    if obj.Spec.Replicas > 100 {
        return MyObservation{
            configHealth: ComponentHealth{
                Component: "Configuration",
                Errors: []error{
                    controllerutils.NewInvalidSpecError(
                        "ReplicaLimitExceeded",
                        "Replica count exceeds maximum of 100",
                        nil,
                    ),
                },
            },
        }
    }
    // Normal processing...
}
```

### External API Calls

```go
func (r *Reconciler) ComposeState(ctx, obj, fetch) MyObservation {
    registryHealth := ComponentHealth{Component: "ImageRegistry"}
    if err := r.checkImageExists(ctx, obj.Spec.Image); err != nil {
        registryHealth.Errors = []error{err}  // Auto-categorized
    } else {
        state := Ready
        registryHealth.State = &state
        registryHealth.Reason = "ImageFound"
        registryHealth.Message = "Image exists and accessible"
    }
    return MyObservation{registryHealth: registryHealth}
}
```

---

## Observability

Control event and log emission per condition:

```go
// Silent (default)
cm.MarkTrue(condType, reason, msg)

// Info log + Normal event on transition
cm.MarkTrue(condType, reason, msg, controllerutils.AsInfo())

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
