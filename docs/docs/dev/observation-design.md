# Observation Design

Guidelines for structuring your observation types.

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
