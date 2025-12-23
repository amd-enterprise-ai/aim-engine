# State Engine Internals

How the automatic state engine works under the hood.

## Overview

The state engine runs after you've composed observations but before applying desired state. It analyzes component health, categorizes errors, manages conditions, and decides reconciliation behavior.

**Flow**: `Fetch → Compose → **StateEngine** → Apply → Events → Status`

---

## Error Categorization

All errors are automatically categorized using `CategorizeError()`:

### Infrastructure Errors

**Detection**: Network timeouts, connection refused, DNS failures, 5xx responses, rate limiting

**Behavior**:
- Sets `DependenciesReachable=False`
- Triggers requeue with exponential backoff
- Applies 10-second grace period before degrading components

### Auth Errors

**Detection**: 401 Unauthorized, 403 Forbidden

**Behavior**:
- Sets `AuthValid=False`
- Stops apply phase (user must fix)
- Fails the resource immediately

### InvalidSpec Errors

**Detection**: Invalid resource specs, conflicts, already exists, 4xx errors

**Behavior**:
- Sets `ConfigValid=False`
- Stops apply phase (user must fix)
- Fails the resource immediately

### MissingDependency Errors

**Detection**: 404 NotFound

**Behavior**:
- Marks component as `Progressing` (waiting state)
- Does not fail the resource
- Normal reconciliation continues

### Unknown Errors

**Behavior**: Treated as infrastructure errors (requeue)

---

## Grace Period Logic

When infrastructure errors occur, components don't immediately degrade:

1. **First occurrence**: `DependenciesReachable` set to `False`, component stays `Ready`/`Progressing`
2. **0-10 seconds**: Grace period - component maintains current state
3. **After 10 seconds**: Component degrades to `Degraded`

This prevents status flapping from transient network issues.

**Implementation**: Uses `LastTransitionTime` on the `DependenciesReachable` condition.

---

## Condition Management

### Component Conditions

For each component in `GetComponentHealth()`, the engine creates a condition:

- Component name: `"Model"` → Condition: `"ModelReady"`
- State mapping:
  - `Ready` → `ConditionTrue`
  - `Progressing` → `ConditionUnknown`
  - `Failed`/`Degraded`/`NotAvailable` → `ConditionFalse`

### Parent Conditions

Three parent conditions are created **only when relevant** (lazy creation):

**`AuthValid`**:
- Created when auth errors occur (set to `False`)
- Transitions to `True` when errors clear
- Never deleted once added

**`ConfigValid`**:
- Created when spec validation errors occur (set to `False`)
- Transitions to `True` when errors clear
- Never deleted once added

**`DependenciesReachable`**:
- Created when infrastructure errors occur (set to `False`)
- Transitions to `True` when errors clear
- Never deleted once added

### Ready Condition

Always set. Aggregates all component states:

- `True`: All components Ready, no auth/config/infra errors
- `False`: Any component failed, or auth/config/infra errors present

---

## Condition Lifecycle

Conditions follow specific lifecycle rules for creation, transition, and deletion.

### Creation

Conditions are created at different times depending on their type:

**Always-present conditions:**

| Condition | When Created |
|-----------|--------------|
| `Ready` | First reconcile (always exists) |
| `{Component}Ready` | First time component appears in `GetComponentHealth()` |

**Lazy-created conditions** (only appear when relevant):

| Condition | When Created | Why Lazy |
|-----------|--------------|----------|
| `AuthValid` | First auth error | Many controllers never have auth errors |
| `ConfigValid` | First spec validation error | Most specs are valid |
| `DependenciesReachable` | First infrastructure error | Usually infrastructure is healthy |

This keeps the conditions list clean for happy-path resources. A resource that never encounters auth errors will never show `AuthValid` - reducing noise in `kubectl get` output.

### Transitions

Conditions transition based on observed state. Only Status or Reason changes update `LastTransitionTime`. Message-only changes are informational and don't trigger a transition.

**Lazy condition lifecycle:**

```
[not present] → Error occurs → False → Error clears → True (persists)
```

Once a lazy condition appears, it stays forever. After recovery, it shows `True` as an audit trail that the error occurred and was resolved. This is useful for debugging ("this resource had auth issues at some point").

---

## Status Field

The `status.Status` field is set to the "worst" component state:

Priority (worst to best):
1. `Failed`
2. `Degraded`
3. `NotAvailable`
4. `Pending`
5. `Progressing`
6. `Ready/Running`

During grace period, the state is preserved (doesn't degrade).

---

## Reconciliation Decisions

The state engine returns a `StateEngineDecision`:

```go
type StateEngineDecision struct {
    ShouldApply   bool   // Skip apply phase?
    ShouldRequeue bool   // Return error to requeue?
    RequeueError  error  // Error for controller-runtime
}
```

**Decision logic**:

- `AuthValid=False` → `ShouldApply=false` (stop apply)
- `ConfigValid=False` → `ShouldApply=false` (stop apply)
- Infrastructure errors → `ShouldRequeue=true` (exponential backoff)

---

## Observability

The state engine automatically configures event and log emission:

### For Component Conditions

- `Ready`/`Progressing` states → `AsInfo()` (normal event, info log on transition)
- `Failed`/`Degraded`/`NotAvailable` states → `AsError()` (warning event, error log recurring)

### For Parent Conditions

- `AuthValid=False` → `AsError()` (recurring)
- `ConfigValid=False` → `AsError()` (recurring)
- `DependenciesReachable=False` → `AsError()` (recurring)
- All `=True` transitions → `AsInfo()`

---

## Manual Override

If you implement `ManualStatusController` (SetStatus), the state engine:

- ✅ Still categorizes errors for requeue decisions
- ✅ Still applies infrastructure error requeue logic
- ❌ Does NOT set any conditions or status fields (you control everything)
