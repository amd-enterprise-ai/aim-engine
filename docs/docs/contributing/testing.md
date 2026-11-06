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

For the full Kind suite, install the operator in the cluster with the Kind
values and use the local Zot-backed target:

```bash
make kind-create
make helm
helm upgrade --install aim-engine dist/chart \
  --namespace aim-system \
  --create-namespace \
  -f hack/kind/operator-values.yaml \
  --set acceleratorDetector.enable=false
make test-chainsaw-kind-zot KIND_CLUSTER=aim-engine
```

`make kind-create` installs an ephemeral Zot registry with a cert-manager-issued
TLS certificate. The test target builds `aim-dummy` once, pushes every fixture
tag through a temporary localhost port-forward, and kind-loads the exact
in-cluster references used by workload pods. The operator trusts the private CA
through `hack/kind/operator-values.yaml`; normal public certificate roots remain
available. Those values also select the Envoy Gateway activation contract with
external collector management, reusing the single standalone collector and
Gateway policy installed by `make kind-create`. The development `EnvoyProxy`
pushes source-side counter deltas to that collector over OTLP.

The Zot service name is only resolvable inside the cluster. Run this target
against an operator installed in Kind, rather than an operator started on the
host with `make watch`.

### Environment Selectors

Tests are filtered by environment. When `ENV=kind` (default), tests tagged with `requires=longhorn` or other infrastructure requirements are excluded automatically.

### Credentials for opt-in tests

Some GPU tests need a private-registry pull secret that is not universally provisioned. These tests are tagged with `needs-secret: dockerhub_pull_secret` and excluded from the default selectors. The shared `tests/e2e/_shared/ensure-pull-secret.sh` helper resolves the secret at runtime in this order:

1. A local Secret manifest under `~/.config/aim-engine/` (path overridable via env).
2. An identically-named secret in the `default` namespace, copied into the test namespace (provision once per shared cluster).
3. Otherwise it warns and continues, unless `PULL_SECRET_REQUIRED=1` is set.

| Secret | Default local path | Path override | Secret name / key |
|--------|--------------------|---------------|-------------------|
| Docker Hub pull secret | `~/.config/aim-engine/dockerhub-regcred.yaml` | `DOCKERHUB_PULL_SECRET_FILE` | `dockerhub-regcred` (`kubernetes.io/dockerconfigjson`) |

Create it once:

```bash
mkdir -p ~/.config/aim-engine

# Docker Hub pull secret (private AIM images).
# The manifest must NOT pin a namespace — the helper applies it with -n <test-ns>.
kubectl create secret docker-registry dockerhub-regcred \
  --docker-server=docker.io \
  --docker-username=<user> --docker-password=<token-or-password> \
  --dry-run=client -o yaml > ~/.config/aim-engine/dockerhub-regcred.yaml
```

Or create the same-named pull secret in the `default` namespace and the helper will copy it in.

Useful overrides: `PULL_SECRET_SOURCE_NS` changes the copy-from namespace, `PULL_SECRET_TARGET_NAME` copies a pull secret under a temporary test-owned name, and `PULL_SECRET_REQUIRED=1` makes a missing pull secret fail instead of warn.

#### Authenticated Hugging Face tests

Tests that can contact Hugging Face carry `hf-access: live`. A shared Chainsaw step template creates `huggingface-creds` inside each test's ephemeral namespace before any downloader resource is applied, preserving per-test log and metrics isolation. When `HF_TOKEN` is set, those requests are authenticated; without it, public-model tests continue to exercise anonymous access. Tests labeled `needs-secret: hf_token` remain excluded from the normal environment suites because they cannot reasonably run without credentials. The deliberate authentication-failure test is labeled `hf-access: anonymous` and never provisions the shared secret.

```bash
read -rsp "HF token: " HF_TOKEN
echo
export HF_TOKEN
make test-chainsaw ENV=kind
# On a matching environment, run only tests where a token is a hard prerequisite:
make test-chainsaw-hf ENV=gpu
unset HF_TOKEN
```

`needs-secret: hf_token` is reserved for tests that cannot reasonably run without credentials, such as gated or unusually large models. `test-chainsaw-hf` selects that lane by labels across the full test tree. Trusted CI passes `HF_TOKEN` to the normal suite as well, so ordinary services whose official AIM metadata contains implicit `hf://` sources are authenticated without moving them into the opt-in lane. Trusted runs fail if the repository secret is missing; GitHub does not expose it to fork PRs.

#### Running a `needs-secret` test

The `needs-secret` exclusion lives in the ENV selector, so pointing `CHAINSAW_TEST_DIR` at the directory is **not** enough — it would still be filtered out (`0 passed / 0 failed / 0 skipped`). Clear the selector to opt in:

```bash
make test-chainsaw \
  CHAINSAW_TEST_DIR=tests/e2e/aimservice/gpu/v1alpha2-profile-via-model-cpu-live \
  CHAINSAW_ENV_SELECTOR=
```

#### WAN 2.2 vLLM-Omni live reference

`tests/e2e/aimservice/gpu/wan22-vllm-omni-live` is an opt-in,
executable reference for deploying the private WAN 2.2 T2V image through the
complete v1alpha2 path. It demonstrates:

- copying a private-registry pull secret into both the test namespace and
  `aim-system`, where `AIMClusterModel` discovery runs;
- discovering and pinning the exact 1-GPU USP1 profile;
- preserving the embedded vLLM-Omni profile filename and projecting
  `AIM_ENGINE=vllm_omni`, `AIM_PROFILE_ID`, and a 32 GiB `/dev/shm`;
- downloading the complete Diffusers repository with `downloadFilter: {}`;
- generating a three-second MP4 through `/v1/videos/sync` and validating its
  frame count and duration.

Prerequisites are one available MI300X, Longhorn, the vLLM-Omni-capable
operator build under test, and credentials for its private Docker Hub image. A cold
namespace downloads approximately 118 GiB, so allow substantial storage and up
to 90 minutes for the cache:

```bash
make test-chainsaw \
  ENV=gpu \
  CHAINSAW_TEST_DIR=tests/e2e/aimservice/gpu/wan22-vllm-omni-live \
  CHAINSAW_ENV_SELECTOR=
```

By default the helper looks for `dockerhub-regcred` in `default` or at the
documented local manifest path. To reuse a differently named cluster secret,
set the source name and namespace; the test copies it to a unique temporary
name and removes only that copy:

```bash
PULL_SECRET_NAME=silogenai-pull \
PULL_SECRET_SOURCE_NS=aim-testing \
make test-chainsaw \
  ENV=gpu \
  CHAINSAW_TEST_DIR=tests/e2e/aimservice/gpu/wan22-vllm-omni-live \
  CHAINSAW_ENV_SELECTOR=
```

For a development cluster that already has the WAN artifact ready in a
long-lived namespace, pass that namespace to Chainsaw to reuse the existing
artifact rather than downloading another copy. The test's service, runtime
config, profile cache, cluster model, generated profiles, and temporary secrets
use reserved `wan22-vllm-omni-live` names and are removed at the end; an
already-existing shared artifact is not deleted:

```bash
PULL_SECRET_NAME=silogenai-pull \
PULL_SECRET_SOURCE_NS=aim-testing \
make test-chainsaw \
  ENV=gpu \
  CHAINSAW_TEST_DIR=tests/e2e/aimservice/gpu/wan22-vllm-omni-live \
  CHAINSAW_ENV_SELECTOR= \
  CHAINSAW_ARGS="--namespace aim-testing"
```

### Runtime projection mode tests (`Both` and `Reduced`)

Eager runtime projection is governed by the operator flag `--runtime-projection-mode` (`Exhaustive` / `Reduced` / `Both` default). The default lane runs the operator in `Both`. Tests whose mutually-exclusive assertions only hold in `Reduced` are gated with `requires: reduced-mode` and excluded from the normal ENV selectors.

The default `mode-both` smoke asserts that the per-profile and model-slug runtimes coexist. `Exhaustive` has no dedicated cluster lane because its per-profile branch is already exercised by `Both`; its unique property (absence of the model-slug runtime) remains covered by unit tests. The `Reduced` lane additionally asserts the complementary shape: the model-slug runtime exists and the per-profile runtime does not.

To run the Reduced tests, first redeploy the operator in Reduced (no rebuild needed — the flag is compiled in), then point `CHAINSAW_TEST_DIR` at the specific mode directory with the selector cleared:

```bash
# Reduced
make set-projection-mode MODE=Reduced
make test-chainsaw \
  CHAINSAW_TEST_DIR=tests/e2e/v1alpha2/runtime-projection/mode-reduced \
  CHAINSAW_ENV_SELECTOR=

# Restore the operator to the default when finished
make set-projection-mode MODE=Both
```

`make set-projection-mode` patches the in-cluster operator Deployment's `--runtime-projection-mode` arg and waits for the rollout, so it works against a kustomize/Tilt dev deploy as well as a Helm install (override `OPERATOR_NAMESPACE` / `OPERATOR_DEPLOYMENT` if they differ). Point `CHAINSAW_TEST_DIR` at exactly one mode directory: the `mode-reduced` fixture asserts the Reduced shape and only passes against a Reduced operator. The `mode-both` smoke needs no gate and runs in the default lane.

For a Helm install, set the equivalent value instead: `--set manager.runtimeProjectionMode=Reduced`.

To run **all** the gated tests for the mode in one shot (instead of pointing `CHAINSAW_TEST_DIR` at each directory), use `make test-chainsaw-projection-mode MODE=Reduced`. It is the inverse of the default lanes' exclusion: it points at `tests/e2e/v1alpha2/runtime-projection` and flips the selector to *include* `requires in (reduced-mode)`, so only the matching gated tests load. Set the operator to that mode first (Helm value or `make set-projection-mode`).

CI exercises both shapes: `.github/workflows/test-e2e.yml` runs the e2e job as a matrix over `runtime-projection-mode: [Both, Reduced]`. The `Both` leg is the full default suite (gated tests excluded); the `Reduced` leg installs the operator with `manager.runtimeProjectionMode` set, asserts the live arg, and runs only the mode-gated tests via `make test-chainsaw-projection-mode`.

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
