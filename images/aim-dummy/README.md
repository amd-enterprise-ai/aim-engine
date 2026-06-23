# aim-dummy

Minimal test image that emulates an AMD Inference Microservice (AIM) container. It is used by chainsaw e2e tests as a lightweight stand-in for real AIM images so tests can run on kind without pulling multi-GB engines.

Previously maintained in `kaiwo/test/aimdummy/`. Moved here so aim-engine owns both the image shape and the chainsaw tests that depend on it.

## What it provides

- **HTTP mock server** on port `8000` implementing a subset of the OpenAI API (`/v1/models`, `/v1/chat/completions`) plus `/health`.
- **`dry-run` profiler** at `/dry-run` that emits a single discovery result as JSON. Used by aim-engine discovery jobs.
- **v1alpha1 AIM labels** (`com.amd.aim.*`) describing model metadata and recommended deployments.
- **v1alpha2 profile YAMLs** at `/workspace/aim-runtime/profiles/` laid out as `<aim_id>/<profile_id>.yaml` plus a `general/` fallback. These are consumed by the v1alpha2 `AIMModel` image inspector (`internal/v1alpha2/aimmodel/inspector.go`).

## Profile layout

| File | `profileId` | Accelerator | Metric | Primary | Notes |
|---|---|---|---|---|---|
| `HuggingFaceTB/SmolLM2-135M/mi300x-fp8-latency.yaml` | `mi300x-fp8-latency` | MI300X x1 | latency | yes (fields match rec) | optimized |
| `HuggingFaceTB/SmolLM2-135M/mi300x-fp16-latency-unopt.yaml` | `mi300x-fp16-latency-unopt` | MI300X x1 | latency | yes (explicit profileId) | unoptimized |
| `HuggingFaceTB/SmolLM2-135M/mi300x-fp8-throughput.yaml` | `mi300x-fp8-throughput` | MI300X x1 | throughput | yes (fields match rec) | optimized |
| `HuggingFaceTB/SmolLM2-135M/mi325x-fp8-latency.yaml` | `mi325x-fp8-latency` | MI325X x1 | latency | yes (explicit profileId) | optimized |
| `HuggingFaceTB/SmolLM2-135M/mi325x-fp8-throughput.yaml` | `mi325x-fp8-throughput` | MI325X x1 | throughput | yes (explicit profileId) | optimized |
| `HuggingFaceTB/SmolLM2-135M/mi300x-fp8-tp2-throughput.yaml` | `mi300x-fp8-tp2-throughput` | MI300X x2 | throughput | no | preview, manual-selection-only |
| `HuggingFaceTB/SmolLM2-135M/cpu-bf16-latency.yaml` | `cpu-bf16-latency` | none (CPU) | latency | no | kind-supported (no accelerator requirement) |
| `general/general-fp8-latency.yaml` | `general-fp8-latency` | MI300X x1 | latency | no | general fallback (no aim_id) |

The CPU profile has no `accelerator_model` or `accelerator_count`, so `HasAcceleratorRequirement` returns `false` and the profile is reported as **Supported** on kind clusters with no GPU nodes. This gives deterministic `managedProfiles.{total,ready}` counts in tests that run on kind.

## Profile file format

Each profile YAML follows the `aim-build` on-image format:

```yaml
aim_id: <org>/<model>       # required for model-scoped profiles
model_id: <org>/<model>     # optional
metadata:
  engine: vllm
  accelerator_type: gpu     # or cpu
  accelerator_model: MI300X # or legacy: gpu
  accelerator_count: 1      # or legacy: gpu_count
  metric: latency           # latency | throughput
  precision: fp8            # fp8 | fp16 | bf16 | ...
  type: optimized           # optimized | unoptimized | preview | general
  manual_selection_only: false
  primary: true             # optional; falls back to recommendedDeployments
engine_args: { ... }
env_vars: { KEY: value, ... }
```

Reference profiles from real AIM builds live at:
`../aim-build/assets/<accel-family>/<org>/<model>/profiles/*.yaml`.

## Image env vars

| Env | Purpose |
|---|---|
| `AIM_ID` | Fallback for `aimId` when `com.amd.aim.model.canonicalName` label is absent. |
| `AIM_BASE_IMAGE_REF` | Copied verbatim into `AIMModel.status.baseImage`. |

## Building locally

```bash
docker build -t aim-dummy:dev images/aim-dummy/
# For kind:
kind load docker-image aim-dummy:dev --name <your-cluster>
```

## CI usage

The image is built locally inside every CI run that needs it (`test-e2e.yml`, `compile-release.yaml`) and loaded into the Kind node cache under the tags the e2e fixtures reference (`aim-dummy:0.1.8`, `aim-dummy:0.1.9`, `aim-dummy:0.1.10`). It is not pushed to any registry; chainsaw fixtures rely on the kubelet's `IfNotPresent` pull policy finding the kind-loaded image locally.
