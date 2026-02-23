# Environment Variables

Environment variables used by the AIM Engine operator and artifact downloader.

## Operator Environment Variables

| Variable | Description |
|----------|-------------|
| `AIM_SYSTEM_NAMESPACE` | Namespace where the operator is deployed. Set automatically by the deployment. |
| `POD_NAME` | Operator pod name. Used for discovery lock identity. |

## Artifact Downloader Variables

These are set automatically by the operator on download jobs.

### Download Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AIM_DOWNLOADER_PROTOCOL` | `XET,HF_TRANSFER` | Comma-separated protocol sequence for HuggingFace downloads. Tried in order; falls back on failure. |
| `MOUNT_PATH` | `/cache` | PVC mount path in the download container. |
| `TARGET_DIR` | `/cache` | Download target directory. |
| `EXPECTED_SIZE_BYTES` | (computed) | Expected model size in bytes. |
| `ARTIFACT_NAME` | (from resource) | Name of the AIMArtifact resource. |
| `ARTIFACT_NAMESPACE` | (from resource) | Namespace of the AIMArtifact resource. |
| `STALL_TIMEOUT` | `120` | Seconds to wait before considering a download stalled. |
| `TMPDIR` | `/tmp/` | Temporary directory for downloads. |
| `HF_HOME` | `/tmp/.hf` | HuggingFace cache directory. |

### Download Protocols

The `AIM_DOWNLOADER_PROTOCOL` variable accepts a comma-separated list of:

| Protocol | Description |
|----------|-------------|
| `XET` | XetHub protocol — fastest for large models |
| `HF_TRANSFER` | HuggingFace Transfer — optimized multi-part download |
| `HTTP` | Standard HTTP — slowest but most compatible |

The downloader tries each protocol in order. On failure, it cleans up `.incomplete` files and moves to the next protocol.

## Debug and Simulation Variables

These are for testing only and should not be used in production.

| Variable | Description |
|----------|-------------|
| `AIM_DEBUG_SIMULATE_HF_DOWNLOAD` | Enable HuggingFace download simulation mode. |
| `AIM_DEBUG_SIMULATE_HF_FAIL_PROTOCOLS` | Comma-separated protocols to simulate failure (e.g., `XET,HF_TRANSFER`). |
| `AIM_DEBUG_SIMULATE_HF_DURATION` | Sleep duration per simulated attempt (default: `2` seconds). |
| `AIM_DEBUG_SIMULATE_DOWNLOAD` | Simulate general download phases. |
| `AIM_DEBUG_DOWNLOAD_DURATION` | Simulated download duration (default: `10` seconds). |
| `AIM_DEBUG_VERIFY_DURATION` | Simulated verify duration (default: `10` seconds). |
| `AIM_DEBUG_VERIFY_FAIL` | Simulate verification failure. |
| `AIM_DEBUG_CAUSE_HANG` | Cause the downloader to hang (testing finalizer behavior). |
| `AIM_DEBUG_CAUSE_FAILURE` | Cause immediate download failure. |

## Inference Container Variables

These are set on inference containers by the operator:

| Variable | Source | Description |
|----------|--------|-------------|
| `AIM_CACHE_PATH` | Constant | Base path for cached model artifacts. |
| `VLLM_ENABLE_METRICS` | Constant | Always `true` — enables vLLM Prometheus metrics. |
| `AIM_PROFILE_ID` | Template | Active profile identifier. |
| `AIM_METRIC` | Template | Optimization metric (`latency` or `throughput`). |
| `AIM_PRECISION` | Template | Model precision (e.g., `fp16`, `fp8`). |
| `AIM_MODEL_ID` | Template | Model identifier for custom models. |
| `AIM_ENGINE_ARGS` | Merged | JSON-encoded engine arguments, merged from service, template, runtime config, and profile. |

### Environment Variable Merge Order

When the same variable is set at multiple levels, the most specific wins:

1. `AIMService.spec.env` (highest priority)
2. `AIMServiceTemplate.spec.env` (plus template-derived vars such as metric/precision/profile)
3. Merged runtime config env (`AIMRuntimeConfig.spec.env` overriding `AIMClusterRuntimeConfig.spec.env`)
4. Operator defaults (lowest priority)

## Next Steps

- [Model Caching Guide](../guides/model-caching.md) — Download protocol configuration
- [Private Registries](../guides/private-registries.md) — Authentication environment variables
- [CLI and Operator Flags](cli.md) — Operator binary flags
