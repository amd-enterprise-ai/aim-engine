# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

Tools are managed via mise. Use `mise exec --` prefix for all commands requiring mise-managed tools (go, controller-gen, chainsaw, kubectl, helm, etc.):

```bash
mise install                      # Install all tools (Go, controller-gen, chainsaw, etc.)

mise exec -- make generate        # Generate DeepCopy methods (after editing api/v1alpha1/)
mise exec -- make manifests       # Generate CRDs and RBAC (after editing api/v1alpha1/)
mise exec -- make fmt             # Format code
mise exec -- make vet             # Vet code
mise exec -- make lint            # Run golangci-lint
mise exec -- make build           # Build binary to bin/manager

mise exec -- make run             # Run controller locally
mise exec -- make run-debug       # Run with debug logging
mise exec -- make watch           # Live reload via air (rebuilds on file change)
mise exec -- make wait-ready      # Wait for operator readiness probe (localhost:8081/readyz)
```

## Operator Logs

When running via `make watch`, operator logs are written to timestamped files in `.tmp/logs/`. The 10 most recent log files are kept.

```bash
# Find the latest log file
ls -t .tmp/logs/air-*.log | head -1

# Tail the latest log
tail -f "$(ls -t .tmp/logs/air-*.log | head -1)"

# Search for errors in latest log
grep -E '"level":"error"' "$(ls -t .tmp/logs/air-*.log | head -1)"

# Search for a specific resource/namespace across all logs
grep "my-resource-name" .tmp/logs/air-*.log

# Search for reconciliation activity for a CRD
grep '"controller":"artifact"' "$(ls -t .tmp/logs/air-*.log | head -1)"

# Find all error messages with context
grep -B2 -A2 '"level":"error"' "$(ls -t .tmp/logs/air-*.log | head -1)"
```

Log entries are JSON formatted. Key fields:
- `level`: info, error, debug
- `controller`: which controller (artifact, service, model, etc.)
- `namespace`, `name`: the resource being reconciled
- `condition`, `status`, `reason`: status updates

## Environment Switching

Switch between Kind (local) and GPU (vcluster) environments. The current ENV is persisted to `.tmp/current-env` so it's shared across terminals (e.g., operator terminal and agent terminal).

```bash
# Kind environment (default)
mise exec -- make watch                        # Restart operator after switching

# GPU environment (requires KUBE_CONTEXT_GPU set to your vcluster context)
export KUBE_CONTEXT_GPU=my-vcluster-context    # Set in .envrc or shell
export ENV=gpu
mise exec -- make watch                        # Restart operator after switching

mise exec -- make env-info                     # Show current environment (reads .tmp/current-env)
```

## Testing

```bash
mise exec -- make test                         # Unit tests (excludes e2e)
mise exec -- go test ./internal/aimservice -v  # Run specific package tests

# Chainsaw e2e tests (selector auto-applied based on ENV)
# IMPORTANT: After editing code, wait for the operator to rebuild before running tests
mise exec -- make wait-ready                   # Wait for operator to be ready (after code changes)
mise exec -- make test-chainsaw                # Run tests for current ENV (always generates JSON report)
```

Chainsaw tests are declarative YAML in `tests/e2e/*/chainsaw-test.yaml`. When `ENV=kind`, tests requiring special infrastructure (e.g., `requires=longhorn`) are excluded automatically. JSON reports are always written to `.tmp/chainsaw-reports/chainsaw-report.json`.

### Diagnosing Chainsaw Test Failures

When tests fail, use JSON reports to identify failures and correlate with operator logs.

**1. Run tests (JSON report auto-generated):**
```bash
# Run tests for current ENV
mise exec -- make test-chainsaw

# Run specific test directory
mise exec -- make test-chainsaw CHAINSAW_TEST_DIR=tests/e2e/aimservice/frozen
```

**2. Find failed tests in the report:**
```bash
# List all failed test names
mise exec -- jq -r '.tests[] | select(.steps[].operations[].failure) | .name' .tmp/chainsaw-reports/chainsaw-report.json | sort -u

# Get failure details with namespace (extracted from error message)
mise exec -- jq -r '.tests[] | select(.steps[].operations[].failure) | {name, basePath, failures: [.steps[].operations[] | select(.failure) | .failure.error]}' .tmp/chainsaw-reports/chainsaw-report.json
```

**3. Extract test namespaces from failures:**

Chainsaw creates unique namespaces like `chainsaw-<adjective>-<noun>` for each test. Extract from the error message:
```bash
# The namespace appears in error output as: "namespace/resource-name"
# Example error: aim.eai.amd.com/v1alpha1/AIMServiceTemplate/chainsaw-profound-cowbird/test-service-template
# Namespace: chainsaw-profound-cowbird
```

**4. Correlate with operator logs:**

The operator logs to timestamped files in `.tmp/logs/` when running via `mise exec -- make watch`:
```bash
# Get the latest log file
LOG=$(ls -t .tmp/logs/air-*.log | head -1)

# Search for errors related to a specific test namespace
grep "chainsaw-profound-cowbird" "$LOG"

# Search for all chainsaw-related errors
grep "chainsaw-" "$LOG" | grep -iE "(error|failed)"

# View recent operator log entries
tail -100 "$LOG"
```

**5. Common failure patterns:**
```bash
LOG=$(ls -t .tmp/logs/air-*.log | head -1)

# General errors in operator logs
grep -E '"level":"error"' "$LOG"

# Reconciliation failures
grep -E '(error|failed|Error|Failed)' "$LOG"
```

**Report structure:** Tests are in `.tests[]` array. Each test has `name`, `basePath`, and `steps[]`. Failures are in `.steps[].operations[].failure.error`. The error message contains the full resource path including namespace.

## Architecture

### CRDs (9 total)
- **AIMService**: Main resource - deploys AI models via KServe + Gateway API
- **AIMModel/AIMClusterModel**: Model definitions (namespace/cluster scoped)
- **AIMServiceTemplate/AIMClusterServiceTemplate**: Template mappings (model→runtime)
- **AIMRuntimeConfig/AIMClusterRuntimeConfig**: Runtime profiles (GPU, precision)
- **AIMArtifact**: Model pre-caching (S3/PVC downloads)
- **AIMClusterModelSource**: Registry discovery for cluster models

### Controller Pattern

This codebase separates controller wiring from domain logic:

1. **Controller Layer** (`internal/controller/*_controller.go`) - Thin adapter implementing `Reconcile()`, sets up watches
2. **Pipeline Framework** (`internal/controller/utils/reconciler.go`) - Generic state machine: Fetch → Compose → Plan → Apply → Status
3. **Domain Packages** (`internal/aim*/`) - Actual reconciliation logic per CRD

Controllers implement `DomainReconciler[T, S, F, Obs]` interface:
```go
FetchRemoteState(ctx, client, reconcileCtx) F      // Fetch all needed resources
ComposeState(ctx, reconcileCtx, fetched) Obs       // Interpret fetched state
PlanResources(ctx, reconcileCtx, obs) PlanResult   // Decide what to create/update
```

### Key Patterns

**FetchResult wrapper**: Errors are wrapped, not thrown. `FetchResult[T]` captures errors allowing `ComposeState` to always run. Check `.OK()`, `.HasError()`, `.IsNotFound()`.

**Condition management**: Framework manages DependenciesReachable, AuthValid, ConfigValid, Ready. Domain reconcilers add custom conditions via `StatusDecorator`.

**Template auto-selection**: When `service.spec.template.name` is empty, AIMService auto-selects based on model's `defaultServiceTemplate`, availability, GPU capacity. Logic in `internal/aimservice/selection.go`.

**Watch indexing**: Controllers use field indexers for efficient lookups (e.g., find all services referencing a template). See `SetupWithManager` for index setup.

## Code Generation

Pre-commit hooks auto-run `make generate` and `make manifests` when `api/` changes.

Generated files (don't edit manually):
- `api/v1alpha1/zz_generated.deepcopy.go`
- `config/crd/bases/*.yaml`
- `config/rbac/role.yaml`

Kubebuilder markers in `api/v1alpha1/*_types.go` control generation:
- `+kubebuilder:validation:Enum=...` - Enum validation
- `+kubebuilder:validation:XValidation:rule="..."` - CEL validation
- `+kubebuilder:printcolumn:...` - kubectl output columns

## Key Files

- `cmd/main.go` - Entry point, controller setup, scheme registration
- `internal/controller/utils/reconciler.go` - Pipeline & DomainReconciler interface
- `api/v1alpha1/aimservice_types.go` - Most complex CRD definition
- `internal/aimservice/reconcile.go` - Complex domain logic example

## External Dependencies

- **KServe** (v0.16.1): InferenceService API for model serving
- **Gateway API** (v1.3.0): HTTPRoute for traffic routing
- **controller-runtime** (v0.22.4): Kubernetes operator framework
