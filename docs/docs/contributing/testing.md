# Testing

AIM Engine uses Go unit tests and [Chainsaw](https://kyverno.github.io/chainsaw/) for declarative e2e tests.

## Unit Tests

```bash
make test                           # All unit tests (excludes e2e)
go test ./internal/v1alpha1/aimservice -v    # Specific package
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

### Credentials for opt-in tests

Some GPU / live tests need credentials that aren't universally provisioned (a private-registry pull secret, a HuggingFace token). These tests are tagged with a `needs-secret` label and **excluded from the default selectors**. Provide the secret(s) locally, then opt in (see below).

Two shared helpers — `tests/e2e/_shared/ensure-pull-secret.sh` and `tests/e2e/_shared/ensure-hf-secret.sh` — resolve each secret at runtime in this order:

1. A local Secret manifest under `~/.config/aim-engine/` (path overridable via env).
2. An identically-named secret in the `default` namespace, copied into the test namespace (provision once per shared cluster).
3. Otherwise: the pull-secret step **warns and continues**; the HF-token step **fails** (the token is required for gated / rate-limited downloads).

| Secret | Default local path | Path override | Secret name / key |
|--------|--------------------|---------------|-------------------|
| Docker Hub pull secret | `~/.config/aim-engine/dockerhub-regcred.yaml` | `DOCKERHUB_PULL_SECRET_FILE` | `dockerhub-regcred` (`kubernetes.io/dockerconfigjson`) |
| HuggingFace token | `~/.config/aim-engine/huggingface-creds.yaml` | `HF_TOKEN_SECRET_FILE` | `huggingface-creds`, key `token` |

Create them once:

```bash
mkdir -p ~/.config/aim-engine

# Docker Hub pull secret (private docker.io/silogenai images).
# The manifest must NOT pin a namespace — the helper applies it with -n <test-ns>.
kubectl create secret docker-registry dockerhub-regcred \
  --docker-server=docker.io \
  --docker-username=<user> --docker-password=<token-or-password> \
  --dry-run=client -o yaml > ~/.config/aim-engine/dockerhub-regcred.yaml

# HuggingFace token (gated / rate-limited model downloads)
kubectl create secret generic huggingface-creds \
  --from-literal=token=hf_xxx \
  --dry-run=client -o yaml > ~/.config/aim-engine/huggingface-creds.yaml
```

Or, instead of local files, create the same-named secrets in the `default` namespace and the helpers will copy them in.

Useful overrides: `PULL_SECRET_SOURCE_NS` / `HF_TOKEN_SOURCE_NS` (change the copy-from namespace), `PULL_SECRET_REQUIRED=1` (make a missing pull secret fail instead of warn), `HF_TOKEN_OPTIONAL=1` (make a missing HF token warn instead of fail).

#### Running a `needs-secret` test

The `needs-secret` exclusion lives in the ENV selector, so pointing `CHAINSAW_TEST_DIR` at the directory is **not** enough — it would still be filtered out (`0 passed / 0 failed / 0 skipped`). Clear the selector to opt in:

```bash
make test-chainsaw \
  CHAINSAW_TEST_DIR=tests/e2e/aimservice/gpu/v1alpha2-profile-via-model-cpu-live \
  CHAINSAW_ENV_SELECTOR=
```

!!! note
    A few tests (`profile-id-propagation`, `finetuned-latest-no-image`) use a different HF secret — `hf-token` with key `hf-token`, copied from the `aim-system` namespace — rather than the `huggingface-creds` convention above.

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

## Test Directories

Key e2e test areas:

| Directory | What it tests |
|-----------|--------------|
| `tests/e2e/aimmodel/fine-tuned/` | aimId-based template matching for fine-tuned models |
| `tests/e2e/aimmodel/custom-models/` | Custom models with explicit hardware and modelSources |
| `tests/e2e/aimservicetemplate/` | Template discovery, inline sources, GPU availability |
| `tests/e2e/aimservice/` | Full service lifecycle including frozen models and GPU tests |
| `tests/e2e/aimartifact/` | Model artifact downloads, quotas, and protocols |

Run a specific test area:

```bash
make test-chainsaw CHAINSAW_ARGS="--test-dir tests/e2e/aimmodel/fine-tuned"
```

## Next Steps

- [Development Setup](development-setup.md) — Local environment configuration
- [Controller Patterns](controller-patterns.md) — Understanding the reconciliation framework
