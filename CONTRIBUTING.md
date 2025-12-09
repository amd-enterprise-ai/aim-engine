# Contributing Guidelines

## Code Architecture: FetchRemoteState / ComposeState / PlanResources / SetStatus Pattern

Our reconciliation logic follows a four-phase pattern that separates concerns and makes code easier to test and maintain:

### 1. **FetchRemoteState** - Retrieve raw data

Fetch resources from Kubernetes or external systems. No logic, just API calls. Returns a struct containing all fetched components and their errors.

```go
type TemplateFetchResult struct {
    Template *aimv1alpha1.AIMServiceTemplate
    Err      error
}

type ServiceFetchResult struct {
    Model         *aimv1alpha1.AIMModel
    ModelErr      error
    Template      *aimv1alpha1.AIMServiceTemplate
    TemplateErr   error
    InferenceService *kservev1beta1.InferenceService
    InferenceErr  error
}

func (r *Reconciler) FetchRemoteState(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServiceFetchResult, error) {
    result := ServiceFetchResult{}

    // Fetch model
    result.Model = &aimv1alpha1.AIMModel{}
    result.ModelErr = r.Get(ctx, client.ObjectKey{Name: service.Spec.Image}, result.Model)

    // Fetch template
    result.Template = &aimv1alpha1.AIMServiceTemplate{}
    result.TemplateErr = r.Get(ctx, client.ObjectKey{Name: service.Spec.Template}, result.Template)

    // Fetch inference service
    result.InferenceService = &kservev1beta1.InferenceService{}
    result.InferenceErr = r.Get(ctx, client.ObjectKey{Name: service.Name, Namespace: service.Namespace}, result.InferenceService)

    return result
}
```

### 2. **ComposeState** - Interpret the current state

Transform raw fetched data into a structured observation that describes **what is** (current world state). Takes the FetchResult as input.

```go
type ServiceObservation struct {
    // Model observations
    ModelFound bool
    ModelReady bool
    ModelErr   error

    // Template observations
    TemplateFound     bool
    TemplateAvailable bool
    TemplateStatus    aimv1alpha1.AIMServiceTemplateStatus
    TemplateErr       error

    // InferenceService observations
    InferenceServiceExists bool
    InferenceServiceReady  bool
    InferenceErr           error
}

func (r *Reconciler) ComposeState(ctx context.Context, service *aimv1alpha1.AIMService, fetchResult ServiceFetchResult) (ServiceObservation, error) {
    obs := ServiceObservation{}

    // Observe model
    if fetchResult.ModelErr != nil {
        obs.ModelErr = fetchResult.ModelErr
        obs.ModelFound = false
    } else {
        obs.ModelFound = true
        obs.ModelReady = fetchResult.Model.Status.Status == constants.AIMStatusReady
    }

    // Observe template
    if fetchResult.TemplateErr != nil {
        obs.TemplateErr = fetchResult.TemplateErr
        obs.TemplateFound = false
    } else {
        obs.TemplateFound = true
        obs.TemplateAvailable = fetchResult.Template.Status.Status == constants.AIMStatusReady
        obs.TemplateStatus = fetchResult.Template.Status
    }

    // Observe inference service
    if fetchResult.InferenceErr != nil {
        obs.InferenceErr = fetchResult.InferenceErr
        obs.InferenceServiceExists = !errors.IsNotFound(fetchResult.InferenceErr)
    } else {
        obs.InferenceServiceExists = true
        obs.InferenceServiceReady = fetchResult.InferenceService.Status.IsReady()
    }

    return obs
}
```

#### Deciding What Belongs in the ComposeState Result (Observation)

When you're debating adding something to Observation, walk through this checklist:

**Is this describing the current world or a future action?**
- Current world → candidate for Observation
- Future action → belongs in PlanResources

**Will this value be used in more than one place (PlanResources/SetStatus/tests/status)?**
- Yes → good candidate for an Observation field or method
- No → maybe compute inline or as a private helper in PlanResources

**Does this value encapsulate non-trivial logic or provider quirks?**
- Yes → put that logic behind an Observation method so PlanResources/SetStatus don't need to know the quirks

**Is it expensive or noisy to recompute?**
- Yes → store it in a field
- No → prefer a method or inline computation

**Would a future teammate understand this field without reading ComposeState's internals?**
- If the name can't be made self-explanatory, either don't expose it, or move it to a method where its meaning is clear from the implementation

#### Methods vs Fields in the Observation Struct

Any time you feel tempted to add a new field, ask: **"Is this really a stored fact, or just a computation over existing facts?"**

**Rule of thumb:**
- If it's cheap to compute and derived from existing fields → make it a **method**
- If it's used in multiple places or encapsulates logic → make it a **method**
- If it's expensive to compute or represents raw fetched state → make it a **field**

**Example - Prefer methods for derived state:**

```go
type InferenceObservation struct {
    rawInferenceService *kservev1beta1.InferenceService // unexported raw state
    Ready               bool
    CurrentReplicas     int32
	DesiredReplicas     int32
}

// Methods encapsulate derivations - no need to store these as fields
func (o InferenceObservation) NeedsScaleUp() bool {
    return o.CurrentReplicas < o.DesiredReplicas
}

func (o InferenceObservation) IsProgressing() bool {
    return !o.Ready && o.CurrentReplicas > 0
}

func (o InferenceObservation) HasFailedCondition() bool {
    if o.rawInferenceService == nil {
        return false
    }
    for _, cond := range o.rawInferenceService.Status.Conditions {
        if cond.Type == "Failed" && cond.Status == "True" {
            return true
        }
    }
    return false
}
```

**Then PlanResources reads cleanly:**

```go
func (r *Reconciler) PlanResources(ctx context.Context, service *aimv1alpha1.AIMService, obs ServiceObservation) (controllerutils.PlanResult, error) {
    // Clear, readable logic
    if obs.Inference.NeedsScaleUp(service.Spec.Replicas) {
        // plan scale up...
    }

    if obs.Inference.IsProgressing() {
        // don't make changes while progressing...
    }

    if obs.Inference.HasFailedCondition() {
        // handle failure...
    }

    // ...
}
```

**Benefits:**
- You see the derivation by jumping to the method definition
- You don't bloat the struct with a hundred one-off boolean fields
- Logic is encapsulated and testable
- Easy to refactor without changing the Observation API

### 3. **PlanResources** - Decide what actions to take

Based on observations, determine **what objects to create, update, or delete**. Returns a `PlanResult` with `Apply` and `Delete` slices.

```go
func (r *Reconciler) PlanResources(
    ctx context.Context,
    service *aimv1alpha1.AIMService,
    obs ServiceObservation,
) (controllerutils.PlanResult, error) {
    var objectsToApply []client.Object
    var objectsToDelete []client.Object

    // Plan model auto-creation if needed
    if obs.ModelFound == false && service.Spec.AutoCreateModel {
        modelObj := planServiceModel(obs, service)
        if modelObj != nil {
            objectsToApply = append(objectsToApply, modelObj)
        }
    }

    // Plan PVC creation if storage is needed
    if obs.TemplateFound && !obs.Caching.ShouldUseCache {
        pvcObj := planServicePVC(service, obs)
        if pvcObj != nil {
            objectsToApply = append(objectsToApply, pvcObj)
        }
    }

    // Plan InferenceService
    if obs.ModelReady && obs.TemplateAvailable {
        inferenceObj, err := planServiceInferenceService(service, obs)
        if err != nil {
            return controllerutils.PlanResult{}, err
        }
        if inferenceObj != nil {
            objectsToApply = append(objectsToApply, inferenceObj)
        }
    }

    // Plan cache retry - delete failed caches to allow retry
    if obs.Caching.ShouldRetry {
        for _, cache := range obs.Caching.FailedCaches {
            cacheCopy := cache
            objectsToDelete = append(objectsToDelete, &cacheCopy)
        }
    }

    return controllerutils.PlanResult{
        Apply:  objectsToApply,
        Delete: objectsToDelete,
    }, nil
}
```

The `PlanResult` struct:
```go
type PlanResult struct {
    // Apply are objects to create or update via Server-Side Apply
    Apply []client.Object

    // Delete are objects to delete
    Delete []client.Object
}
```

### 4. **SetStatus** - Update conditions and status
Set conditions using the condition manager (`cm`) and status helper (`h`), and update the status struct. This method receives the status object.

```go
func (r *Reconciler) SetStatus(
    status *aimv1alpha1.AIMServiceStatus,
    cm *controllerutils.ConditionManager,
    obs ServiceObservation,
) {
    h := controllerutils.NewStatusHelper(status, cm)

    if !obs.TemplateFound {
        h.Degraded(aimv1alpha1.AIMServiceReasonTemplateNotFound, "Template not found")
        cm.MarkFalse(aimv1alpha1.AIMServiceConditionTemplateResolved,
            aimv1alpha1.AIMServiceReasonTemplateNotFound, "Template not found",
            controllerutils.AsWarning())
        return
    }

    if !obs.TemplateAvailable {
        h.Progressing(aimv1alpha1.AIMServiceReasonTemplateNotReady, "Waiting for template")
        cm.MarkFalse(aimv1alpha1.AIMServiceConditionTemplateResolved,
            aimv1alpha1.AIMServiceReasonTemplateNotReady, "Template not ready",
            controllerutils.AsInfo())
        return
    }

    cm.MarkTrue(aimv1alpha1.AIMServiceConditionTemplateResolved,
        aimv1alpha1.AIMServiceReasonResolved, "Template resolved",
        controllerutils.AsInfo())
    status.ResolvedTemplate = &aimv1alpha1.AIMResolvedReference{
        Name: obs.TemplateStatus.Name,
    }
}
```

## Conditions and Status

### Always Use Constants for Condition Types and Reasons

**Never use inline strings for condition types or reasons.** All condition types and reasons must be defined as constants in the API types files:

- `api/v1alpha1/aimservice_types.go` - AIMService conditions
- `api/v1alpha1/aimservicetemplate_shared.go` - AIMServiceTemplate conditions
- `api/v1alpha1/aimmodel_shared.go` - AIMModel conditions

```go
// ❌ BAD - inline strings
cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, "ModelNotFound", "Model not found", controllerutils.LevelWarning)

// ✅ GOOD - using constants
cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonModelNotFound, "Model not found", controllerutils.LevelWarning)
```

Message strings (the descriptive text parameter) can and should remain as inline strings or formatted strings.

### Condition Manager and Status Helper

Use the condition manager (`cm`) and status helper (`sh` or `h`) consistently:

- `cm.MarkTrue/MarkFalse/Set` - Set condition status with type, reason, message, and observability options
- `h.Progressing/Degraded/Failed/Ready` - Set overall status with reason and message

### Observability Options for Conditions

By default, conditions are **silent** - they update status but don't emit events or logs. You can use high-level options for common patterns:

```go
// Silent (default) - just update status
cm.MarkFalse(condType, reason, msg)

// Informational/progress updates - info log + normal event on transition
cm.MarkTrue(condType, reason, msg, controllerutils.AsInfo())

// Warnings/transient errors - error log + warning event on transition
cm.MarkFalse(condType, reason, msg, controllerutils.AsWarning())

// Critical/persistent errors - error log + warning event EVERY reconcile
cm.MarkFalse(condType, reason, msg, controllerutils.AsError())

// Explicitly silent (for clarity)
cm.MarkFalse(condType, reason, msg, controllerutils.Silent())
```

**High-level options (recommended):**

- **`AsInfo()`**: Info log (V(1)) + Normal event on transition - for progress/informational updates
- **`AsWarning()`**: Error log (V(0)) + Warning event on transition - for transient errors/warnings
- **`AsError()`**: Error log (V(0)) + Warning event RECURRING - for critical/persistent errors
- **`Silent()`**: No events or logs (default, but explicit)

**Fine-grained options** (for advanced cases):

- **Log Options**: `WithErrorLog()`, `WithInfoLog()`, `WithDebugLog()`, `WithLog(level)`
- **Event Options**: `WithNormalEvent()`, `WithWarningEvent()`
- **Recurring**: `WithRecurring()`, `WithRecurringErrorLog()`, `WithRecurringWarningEvent()`
- **Message Overrides**: `WithEventReason()`, `WithEventMessage()`, `WithLogMessage()`

**Common patterns:**

| Situation | Pattern |
|-----------|---------|
| Progress/success | `AsInfo()` |
| Transient error/degraded | `AsWarning()` |
| Critical/persistent error | `AsError()` |
| Internal state tracking | (no options - silent) |

## Pull Requests

- Keep changes focused and atomic
- Write clear commit messages explaining the "why" not just the "what"
- Add tests for new functionality
- Update relevant documentation

## Questions?

Open an issue or reach out to the team for clarification.
