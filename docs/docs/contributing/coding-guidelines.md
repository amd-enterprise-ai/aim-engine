# Coding Guidelines

These guidelines apply to all code in AIM Engine: Go controllers, domain packages, API types, Helm/manifests, and supporting scripts. They complement the architecture-specific guides ([Controller Patterns](controller-patterns.md), [Observation Design](observation-design.md), [Testing](testing.md)) with day-to-day conventions for writing code that is easy to read, easy to test, and safe to operate in production.

The goal is **boring code that works**: clear names, short functions, predictable behavior, and tests that reflect how the operator runs in real clusters.

---

## Core principles

### Optimize for the reader

Code is read far more often than it is written. A teammate (or future you) should understand what a function does from its name and signature, without tracing call chains or decoding abbreviations.

### Principle of least surprise

Behavior should match what the name and docs imply.

- If a function is named `fetch`, it reads remote state; it does not mutate status.
- If detection **fails**, do not treat that the same as "nothing found" — distinguish transient failure from a confident empty result.
- If a value comes from an environment variable or CRD field, validate it early and fail with a clear error rather than falling through to a silent default.

When in doubt, choose the behavior that is easiest to explain in a log message or condition reason.

### Keep it simple

- Solve the problem in front of you. Do not add abstraction layers, interfaces, or configuration knobs unless a second real use case exists or new one appears. Every struct and interface adds cognitive load; they only reduce overall complexity when reused throughout the codebase.
- Prefer explicit `if` branches over generic frameworks when the branches are few and stable.
- Copy a small amount of code rather than inventing a shared helper used once. However, don't shy away from implementing genuinely useful helper functions.
- Make maximal use of **kubebuilder** — express validation, defaults, immutability rules, print columns, and RBAC via `+kubebuilder:` markers on API types and controllers, then regenerate with `make generate` / `make manifests`. Prefer declarative markers over hand-rolled equivalents in reconcile logic or webhooks.

### Keep functions short

A function should do **one thing** and fit on one screen (~40–60 lines is a practical ceiling). When a function grows, extract named steps:


| Instead of                                                                                | Prefer                                                                                                         |
| ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| One 200-line`reconcile`                                                                   | `FetchRemoteState` → `PlanResources` → status decoration (see [Controller Patterns](controller-patterns.md)) |
| One`runDetectionCycle` that dispatches vendors, handles sticky failures, and writes files | `detectModelLabels`, `publishPartitionLabels`, `writeFeatureFileIfChanged`                                     |
| Nested conditionals five levels deep                                                      | Early returns and small helpers with descriptive names                                                         |

If you need a comment to explain *what* a block does, consider making that block a function and naming it after the comment.

---

## Naming

Names are the primary documentation. Avoid cleverness, in-jokes, and opaque abbreviations.

### Functions and methods

Use **verb phrases** that state the action and, when helpful, the subject:


| Good                            | Avoid                                                         |
| ------------------------------- | ------------------------------------------------------------- |
| `fetchResolvedProfile`          | `resolve`                                                     |
| `buildNodeAffinityForModel`     | `buildNA`                                                     |
| `detectNvidiaGPUs`              | `detect_hardware_nvidia` (inconsistent style)                 |
| `publishDefaultPartitionLabels` | `run_nvidia_partition_cycle` (sounds symmetric but does less) |
| `canonicalizeNvidiaProductName` | `_normalize_nvidia_consumer`                                  |

**Prefixes we use consistently:**


| Prefix     | Meaning                                               |
| ---------- | ----------------------------------------------------- |
| `fetch`    | Read from the Kubernetes API or another remote source |
| `build`    | Construct a spec or object in memory (no API calls)   |
| `plan`     | Decide desired state (pure; no side effects)          |
| `resolve`  | Choose among alternatives (profile, template, image)  |
| `match`    | Compare user intent against cluster state             |
| `validate` | Check inputs; return error if invalid                 |

Do not use `handle`, `process`, `do`, or `manage` as standalone verbs — they hide the action.

### Types and packages

- **Packages**: lowercase, single purpose (`aimprofile`, `aimservice`, `controller/utils`). There is also a shared utils package. Avoid making it a junk drawer.
- **Structs**: noun phrases for data (`NodeMatchResult`, `PlanResult`, `FetchResult`). Observation/fetch types name what they hold (`ServiceFetch`, not `Data`).
- **Interfaces**: small, behavior-named when exported (`DomainReconciler`, `ComponentHealthProvider`). Prefer concrete types unless a second real use case exists or new one appears.
- **Constants**: describe the value's role, not its spelling (`PartitioningSchemeDefault`, not `DEFAULT`).

### Booleans and predicates

Name booleans so they read naturally in `if` statements:

```go
// Good
if gpuAbsenceConfirmed { ... }
if profileRequiresPartitioning { ... }

// Avoid
if flag { ... }
if ok2 { ... }
if confidentNoGPU { ... }  // "confident" is ambiguous without domain context
```

### Environment variables and labels

Use `SCREAMING_SNAKE_CASE` for env vars and document them in the module header or Helm values. Kubernetes label keys should follow [Naming and Labels](../reference/naming-and-labels.md); do not invent parallel naming schemes for the same concept.

### What to avoid

- **Hungarian notation** (`strName`, `bReady`).
- **Abbreviations** unless universally known in Kubernetes (`PVC`, `CRD`, `UID` are fine; `cfg`, `mgr`, `res` are not).
- **Cute or ironic names** (`magic`, `fixup`, `doTheThing`).
- **Misleading symmetry** — two functions with parallel names should do parallel things. If one path detects hardware and another publishes a placeholder, the names should reflect that difference.

---

## Go conventions

Follow standard Go style ([Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)). This project enforces formatting and linting via `make fmt`, `make vet`, and `make lint` (see [.golangci.yml](https://github.com/amd-enterprise-ai/aim-engine/blob/main/.golangci.yml)).

### Errors

- Return `error`; do not panic in library or controller code.
- Wrap with context: `fmt.Errorf("fetch profile %q: %w", name, err)`.
- Use typed or sentinel errors only when callers need to branch (`errors.Is`, `errors.As`). See `internal/controller/utils/errors.go` for categorization patterns.
- Error messages are lowercase, no trailing punctuation, and state what failed and why it matters to the user.

### Zero values and pointers

- Prefer non-pointer fields when zero value is meaningful.
- Use pointers for optional API fields and explicit "not set" semantics in CRDs.

### Interfaces

- **Accept interfaces, return structs.**
- Define interfaces at the call site, not the implementation package.
- Do not export interfaces with a single implementation "for testing" — test against concrete types or small fakes in `_test.go` files.

### Imports

- Group: stdlib, third-party, `github.com/amd-enterprise-ai/aim-engine/...`.
- `goimports` with local prefix is configured; run `make fmt` before committing.

### Generated code

Do not hand-edit generated files (`zz_generated.deepcopy.go`, CRD YAML under `config/crd/bases/`, etc.). Change API types and run `make generate` / `make manifests`.

When adding or changing API behavior, reach for kubebuilder markers first (`+kubebuilder:validation`, `+kubebuilder:default`, `+kubebuilder:validation:XValidation`, `+kubebuilder:rbac`, and related annotations) so OpenAPI schema, RBAC manifests, and deepcopy stay in sync with the source of truth.

---

## Kubernetes operator patterns

AIM Engine separates **controller wiring** (`internal/controller/`) from **domain logic** (`internal/aim*/`, `internal/v1alpha*/`). Keep this boundary:


| Layer                         | Responsibility                                              |
| ----------------------------- | ----------------------------------------------------------- |
| Controller                    | `Reconcile()` entry, watches, indexers, scheme registration |
| Pipeline (`controller/utils`) | Fetch → Compose → Plan → Apply → Status                 |
| Domain package                | Business rules, resource builders, matching logic           |

### Phase discipline

Stick to the four phases documented in [Controller Patterns](controller-patterns.md):

1. **FetchRemoteState** — all `client.Get` / `List` calls live here.
2. **ComposeState** — thin passthrough unless derived state is genuinely needed ([Observation Design](observation-design.md)).
3. **PlanResources** — pure function: given observation, return desired objects. No API calls.
4. **Status** — via `GetComponentHealth`, `DecorateStatus`, or `SetStatus`.

Violating phase boundaries makes unit tests harder and hides side effects.

### Status and conditions

- Use the shared condition manager; do not write raw `status.conditions` slices ad hoc.
- Condition `Reason` values are PascalCase, stable, and suitable for `kubectl describe`.
- `Message` fields should be actionable for cluster admins.
- Set `Ready` only when the resource is actually usable, not merely "reconcile finished without panic".

### Reconciliation safety

- **Idempotent applies** — running reconcile twice should not create duplicate resources or flap labels.
- **Owner references** — use `PlanResult.Apply` for owned children; `ApplyWithoutOwnerRef` only when intentional.
- **Requeue** — return errors or `RequeueAfter` for transient failures; do not spin on permanent user errors.
- **Finalizers** — document what cleanup guarantees; keep finalizer logic in one place.

### Configuration

- Prefer CRD fields and Helm values over hard-coded cluster assumptions.
- Validate configuration at startup or first reconcile; reject unknown enum values explicitly.
- Document defaults in Helm values comments and run `make generate-helm-docs` when values change.

---

## Scripts and non-Go components

DaemonSet scripts (for example `detect-and-label.py`) follow the same naming and simplicity rules:

- Use `snake_case` for Python functions (PEP 8), but still prefer **descriptive verb phrases** (`detect_nvidia_gpus`, not `run_cycle`).
- Module-level docstrings list environment variables and failure semantics.
- Distinguish **failure** (`None`) from **confident empty** (`[]`) when the outcome affects production labels or scheduling.
- Keep orchestration functions thin; push parsing and normalization into testable pure functions.

---

## Testing

Tests are part of the contract. Code without tests is incomplete unless the change is purely cosmetic documentation.

### What to test

Prioritize behavior that affects production clusters:


| Area                  | Test focus                                                        |
| --------------------- | ----------------------------------------------------------------- |
| Matching / resolution | Profile→node affinity, template selection, image resolution      |
| Planning              | Correct objects created, skipped, or deleted given observation    |
| Failure modes         | API errors, missing dependencies, invalid spec, partial readiness |
| Edge cases            | Empty lists, stale status, version transitions, mixed hardware    |
| Regression            | Every bug fix gets a test that would have caught it               |

### Unit tests

- Use **table-driven tests** for functions with multiple input/output pairs.
- Name tests `Test<Function>_<scenario>` (for example `TestMatchNodes_unpartitionedProfile`).
- Build realistic fixtures — mirror real label keys, resource names, and status shapes from production (see `node_match_test.go`).
- Test pure functions (`PlanResources`, matchers, builders) without envtest when possible; they are fast and precise.
- Use envtest / fake client only when controller wiring or API machinery is under test.

**Avoid:**

- Tests that only assert "no error" without checking outputs.
- Tests that duplicate implementation line-for-line (they break on every refactor).
- Giant fixtures with no comment explaining what scenario they represent.

### Integration and e2e tests

- Chainsaw tests in `tests/e2e/` should follow [Testing](testing.md) and version conventions in `CLAUDE.md`.
- Prefer **full-chain** tests that reach `Ready` when feasible; use frozen-status tests only when live dependencies are impractical.
- Gate hardware-specific tests with `requires` labels (`gpu-amd`, `gpu-nvidia`, `longhorn`, etc.).
- Assert on **observable cluster state** (labels, conditions, pod spec) rather than guessing from indirect signals.
- When a test waits for labels or conditions, assert the specific key you expect — do not loop over a large allowlist of "maybe" values.

### Coverage expectations

There is no fixed percentage gate, but:

- New packages should ship with tests for all exported behavior.
- Bug fixes and behavior changes extend existing test files; do not shrink coverage.
- Run `make test` locally; CI must pass `make lint` and unit tests before merge.

### Test data

- Keep fixtures in the test file or `testdata/` beside the package.
- Use builders (`makeNode`, `makeService`) to reduce noise and make scenarios readable.
- Do not depend on external network calls in unit tests; use simulation flags documented in [Testing](testing.md).

---

## Comments and documentation

- **Comments explain why**, not what. The code and function name should explain what.
- Export godoc on public APIs: one sentence summary, then detail if needed.
- User-facing behavior belongs in `docs/docs/`; link from godoc when operators need runbooks.
- `TODO` comments include an issue reference or milestone (`TODO(MIG): issue #123`).

---

## Pull request checklist

Before requesting review:

- [ ]  Names read clearly without institutional knowledge.
- [ ]  Functions are focused; no new god functions.
- [ ]  Fetch / plan / apply phases respected.
- [ ]  Errors wrapped; user-visible messages actionable.
- [ ]  Unit tests cover new behavior and failure paths.
- [ ]  E2e or integration test added when behavior crosses controller boundaries.
- [ ]  `make fmt`, `make vet`, `make lint`, and `make test` pass.
- [ ]  API or Helm changes regenerated (`make generate`, `make manifests`, `make generate-helm-docs` as applicable).
- [ ]  No secrets, tokens, or cluster-specific values committed.

---

## Examples

### Naming and structure

```go
// Avoid: one function, unclear name, mixed concerns
func run(v string, t string) error {
    // 150 lines: fetch, validate, build, apply, status...
}

// Prefer: named steps, obvious responsibilities
func (r *Reconciler) PlanResources(ctx context.Context, rc ReconcileContext, obs Observation) PlanResult {
    if !obs.AllDependenciesReady() {
        return PlanResult{}
    }
    result := PlanResult{}
    result.Apply(buildInferenceService(rc.Object, obs.ResolvedProfile))
    return result
}
```

### Least surprise in detection logic

```python
# Avoid: failure and "no hardware" both become empty output → label flap
def detect_gpus():
  try:
    return run_nvidia_smi()
  except Exception:
    return []

# Prefer: explicit contract
def detect_nvidia_gpus() -> list[Detection] | None:
    """
    Returns:
      list  — detection succeeded (may be empty if no GPUs).
      None  — detection failed; caller must preserve last-good state.
    """
```

### Realistic unit test

```go
func TestResolveResources_explicitGPUCountWins(t *testing.T) {
    tests := []struct {
        name       string
        accelCount int32
        resources  *corev1.ResourceRequirements
        wantGPU    string
    }{
        {
            name:       "defaults to amd.com/gpu when count set",
            accelCount: 4,
            wantGPU:    "4",
        },
        {
            name: "explicit request overrides derived count",
            accelCount: 4,
            resources: &corev1.ResourceRequirements{
                Requests: corev1.ResourceList{
                    "amd.com/gpu": resource.MustParse("2"),
                },
            },
            wantGPU: "2",
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := ResolveResources(aimv1alpha1.AcceleratorTypeGPU, tt.accelCount, tt.resources, "MI300X", nil)
            // assert on got.Requests ...
        })
    }
}
```

---

## Related documentation

- [Development Setup](development-setup.md) — Toolchain and local cluster
- [Controller Patterns](controller-patterns.md) — Reconciliation pipeline
- [Observation Design](observation-design.md) — Structuring fetch and observation types
- [State Engine](state-engine.md) — Condition and error categorization
- [Testing](testing.md) — Chainsaw e2e and debug simulation
- [Naming and Labels](../reference/naming-and-labels.md) — Kubernetes label conventions
