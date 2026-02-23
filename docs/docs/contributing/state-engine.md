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
- Component state: `Degraded` (after grace period)

### Auth Errors

**Detection**: 401 Unauthorized, 403 Forbidden, auth-related log patterns (S3, HuggingFace)

**Behavior**:
- Sets `AuthValid=False`
- Stops apply phase (user must fix)
- Component state: `Failed` (requires spec/secret change)

**Log patterns detected**:
- S3: `access denied`, `InvalidAccessKeyId`, `NoCredentialProviders`
- HuggingFace: `Access to model X is restricted`, `Cannot access gated repo`, `Invalid token`

### InvalidSpec Errors

**Detection**: Invalid resource specs, conflicts, already exists, 4xx errors

**Behavior**:
- Sets `ConfigValid=False`
- Stops apply phase (user must fix)
- Component state: `Failed` (requires spec change)

### MissingUpstreamDependency Errors

**Detection**: User-referenced resources not found (runtimeConfig, secrets, configmaps)

**Behavior**:
- Sets `ConfigValid=False`
- Stops apply phase (user must fix)
- Component state: `Failed` (requires user to create the referenced resource)

**Distinction**: These are resources the *user* referenced in their spec, not internal resources the controller creates.

### MissingDownstreamDependency Errors

**Detection**: 404 NotFound for internal resources (pods, jobs, deployments being created)

**Behavior**:
- Marks component as `Progressing` (waiting state)
- Does not fail the resource
- Normal reconciliation continues
- Component state: `Progressing` (will self-heal)

**Distinction**: These are resources the *controller* is creating/managing - they're expected to be missing during initial reconciliation.

### ResourceExhaustion Errors

**Detection**: OOM kills, disk full, quota exceeded, storage exhaustion log patterns

**Behavior**:
- Component state: `Failed` (requires resource limit/quota increase)
- Does not set `ConfigValid=False` (it's a platform/capacity issue, not a config issue)

**Log patterns detected**:
- `no space left on device`
- `disk full`
- `quota exceeded`
- Pod termination reason: `OOMKilled`

### Unknown Errors

**Behavior**: Treated as infrastructure errors (requeue with backoff)

---

## Pod Log Inspection

The `GetPodsHealth` utility automatically inspects pod logs to categorize failures beyond what Kubernetes API provides:

**How it works**:
1. Checks for image pull errors (auth, not found, backoff)
2. For failed pods, fetches the last 50 lines of logs from the failed container
3. Matches log patterns to categorize the failure type
4. Returns categorized error with log excerpt for debugging

**Pattern matching order** (first match wins):
1. **Resource not found** (404, "Repository Not Found") - checked first because HuggingFace returns 401 for non-existent repos
2. **Auth errors** (credentials, tokens, access denied)
3. **Storage exhaustion** (disk full, quota exceeded)

**Example categorization**:
```go
// Pod failed with exit code 1
// Logs contain: "Access to model Qwen/Qwen3-32B is restricted"
// → Categorized as Auth error
// → Status shows: "Container download failed with exit code 1: ...\n\nLog excerpt:\nAccess to model Qwen/Qwen3-32B is restricted"
```

This automatic inspection eliminates the need for manual log checking - the controller surfaces the root cause directly in the status.

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

The `status.Status` field is set to the "worst" component state using priority ordering:

**Priority (worst to best)**:
1. `Failed` - Terminal errors requiring user intervention (auth, invalid spec, resource exhaustion)
2. `Degraded` - Recoverable issues (infrastructure errors after grace period, missing upstream dependencies)
3. `NotAvailable` - Resource exists but not available due to some reason
4. `Pending` - Waiting for scheduling/resources
5. `Progressing` - Actively working toward Ready (internal deps, jobs running)
6. `Ready/Running` - Fully operational

**State Derivation from Errors**:

When `ComponentHealth.State` is not explicitly set, the state is derived from errors:

```go
// DeriveStateFromErrors rules:
- No errors → Ready
- Auth, InvalidSpec, ResourceExhaustion → Failed (requires user action)
- MissingUpstreamDependency, Infrastructure → Degraded (may recover)
- MissingDownstreamDependency → Progressing (will self-heal)
```

**During grace period**: Component state is preserved (doesn't degrade to `Degraded`) until 10 seconds have passed.

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
