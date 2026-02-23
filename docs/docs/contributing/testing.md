# Testing

AIM Engine uses Go unit tests and [Chainsaw](https://kyverno.github.io/chainsaw/) for declarative e2e tests.

## Unit Tests

```bash
make test                           # All unit tests (excludes e2e)
go test ./internal/aimservice -v    # Specific package
go test ./internal/... -run TestFoo # Specific test
```

## E2E Tests (Chainsaw)

Chainsaw tests are declarative YAML files in `tests/e2e/`. Each test directory contains a `chainsaw-test.yaml` that defines steps: apply resources, assert conditions, run scripts.

### Running Tests

```bash
# Ensure the operator is running and ready
make wait-ready

# Run all tests for the current environment
make test-chainsaw

# Run a specific test directory
make test-chainsaw CHAINSAW_TEST_DIR=tests/e2e/aimservice/frozen
```

### Environment Selectors

Tests are filtered by environment. When `ENV=kind` (default), tests tagged with `requires=longhorn` or other infrastructure requirements are excluded automatically.

### Test Reports

JSON reports are written to `.tmp/chainsaw-reports/chainsaw-report.json`. Analyze failures:

```bash
# List failed tests
jq -r '.tests[] | select(.steps[].operations[].failure) | .name' \
  .tmp/chainsaw-reports/chainsaw-report.json | sort -u

# Get failure details
jq -r '.tests[] | select(.steps[].operations[].failure) |
  {name, failures: [.steps[].operations[] | select(.failure) | .failure.error]}' \
  .tmp/chainsaw-reports/chainsaw-report.json
```

### Correlating with Operator Logs

Chainsaw creates unique namespaces like `chainsaw-<adjective>-<noun>` for each test. Extract the namespace from test failures and search operator logs:

```bash
LOG=$(ls -t .tmp/logs/air-*.log | head -1)
grep "chainsaw-<namespace>" "$LOG"
```

## Writing Tests

### Test Structure

```
tests/e2e/my-feature/
  chainsaw-test.yaml    # Test definition
  resource.yaml         # Resources to apply
  assert.yaml           # Expected state assertions
```

### Example Test

```yaml
apiVersion: chainsaw.kyverno.io/v1alpha1
kind: Test
metadata:
  name: basic-service
spec:
  steps:
    - try:
        - apply:
            file: service.yaml
        - assert:
            file: assert.yaml
            timeout: 120s
```

### Debug Simulation

For tests that involve model downloads, use simulation mode to avoid real network calls:

```yaml
env:
  - name: AIM_DEBUG_SIMULATE_HF_DOWNLOAD
    value: "true"
  - name: AIM_DEBUG_SIMULATE_HF_DURATION
    value: "2"
```

## Next Steps

- [Development Setup](development-setup.md) — Local environment configuration
- [Controller Patterns](controller-patterns.md) — Understanding the reconciliation framework
