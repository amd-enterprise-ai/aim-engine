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
| `MOUNT_PATH` | `/cache/models` | PVC mount path in the download container. |
| `TARGET_DIR` | `/cache/models` | Download target directory. |
| `EXPECTED_SIZE_BYTES` | (computed) | Expected model size in bytes. |
| `ARTIFACT_NAME` | (from resource) | Name of the AIMArtifact resource. |
| `ARTIFACT_NAMESPACE` | (from resource) | Namespace of the AIMArtifact resource. |
| `STALL_TIMEOUT` | `120` | Seconds without any byte-size growth before the progress monitor considers a download stalled and kills it (so the next protocol is tried). |
| `PROGRESS_INTERVAL` | `5` | Seconds between progress-monitor size measurements/status updates. |
| `DU_TIMEOUT` | `300` | Upper bound (seconds) for a single `du` size measurement. On overrun the monitor treats the filesystem as possibly stuck: it warns once, surfaces a message on `status.progress.message`, and pauses progress/stall handling (rather than blocking) until `du` returns. Kept generous so slow-but-healthy storage is not mistaken for a stall. |
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

### Verification

| Variable | Default | Description |
|----------|---------|-------------|
| `AIM_KEEP_METADATA_ON_FAILURE` | (unset) | When set to any non-empty value, preserves the HuggingFace metadata cache on integrity verification failure. By default, the metadata cache is removed on failure to ensure a clean retry. |

## Debug and Simulation Variables

These are for testing only and should not be used in production.

| Variable | Description |
|----------|-------------|
| `AIM_DEBUG_SIMULATE_HF_DOWNLOAD` | Enable HuggingFace download simulation mode. |
| `AIM_DEBUG_SIMULATE_HF_FAIL_PROTOCOLS` | Comma-separated protocols to simulate failure (e.g., `XET,HF_TRANSFER`). |
| `AIM_DEBUG_SIMULATE_HF_DURATION` | Sleep duration per simulated attempt (default: `2` seconds). |
| `AIM_DEBUG_SIMULATE_HF_HANG_PROTOCOLS` | Comma-separated protocols whose simulated download hangs (via `python sleep`) so the progress monitor's stall detection must kill it. |
| `AIM_DEBUG_SIMULATE_HF_HANG_DURATION` | Sleep duration for a hanging simulated download (default: `300` seconds). |
| `AIM_DEBUG_SIMULATE_DU_HANG_SECONDS` | Make the progress monitor's `du` size measurement sleep this many seconds (set above `DU_TIMEOUT`) to simulate a stuck filesystem without a real wedged PVC. |
| `AIM_DEBUG_SIMULATE_DU_HANG_CALLS` | How many of the first `du` measurements hang when `AIM_DEBUG_SIMULATE_DU_HANG_SECONDS` is set (default: `1`; use a large value to hang for the whole run). |
| `AIM_DEBUG_SIMULATE_DOWNLOAD` | Simulate general download phases. |
| `AIM_DEBUG_DOWNLOAD_DURATION` | Simulated download duration (default: `10` seconds). |
| `AIM_DEBUG_VERIFY_DURATION` | Simulated verify duration (default: `10` seconds). |
| `AIM_DEBUG_VERIFY_FAIL` | Simulate verification failure. |
| `AIM_DEBUG_CAUSE_HANG` | Cause the downloader to hang (testing finalizer behavior). |
| `AIM_DEBUG_CAUSE_FAILURE` | Cause immediate download failure. |

For typed S3 connections, `AIMArtifact.spec.env` accepts only
`AIM_S3_LOG_LEVEL`, `AIM_S3_MAX_WORKERS`, `AIM_S3_MAX_CONCURRENCY`, and
`AIM_S3_MULTIPART_CHUNKSIZE_MB`, with bounded literal values. Proxy, transport,
trust, process, and credential-provider variables must be configured in the
administrator-owned RuntimeConfig. `AIM_S3_LOG_LEVEL` affects only downloader
package logs; AWS SDK and HTTP wire loggers remain at `WARNING`.

## Inference Container Variables

These are set on inference containers by the operator. The "Source" column lists where the value comes from; "Profile" is the resolved `AIMProfile` or `AIMClusterProfile`.

| Variable | Source | Description |
|----------|--------|-------------|
| `AIM_CACHE_PATH` | Constant | Base path for cached model artifacts. |
| `VLLM_ENABLE_METRICS` | Constant | Always `true` — enables vLLM Prometheus metrics. |
| `AIM_ID` | Profile | AIM product family identifier (e.g., `meta-llama/Llama-3-8B`). Determines the model-specific profile search path and serves as a fallback model identifier. Mutually exclusive with `AIM_MODEL_ID`. |
| `AIM_ENGINE` | Profile | Runtime engine selected from the resolved profile's `spec.engine` (for example `vllm` or `vllm_omni`). |
| `AIM_PROFILE_ID` | Profile | Active profile identifier, `custom/{aimId}/{filename}`, naming the profile file the operator projects for the runtime so the runtime skips its own profile selection. Discovered profiles preserve `spec.profileId` as the filename; hand-authored profiles without it receive an engine-aware generated filename. |
| `AIM_METRIC` | Profile | Optimization metric (`latency` or `throughput`). |
| `AIM_PRECISION` | Profile | Model precision (e.g., `fp16`, `fp8`). |
| `AIM_MODEL_ID` | Profile | Model identifier for custom models (base container deployments). Mutually exclusive with `AIM_ID`. |
| `AIM_ENGINE_ARGS` | Merged | JSON-encoded engine arguments, merged from service `profileOverrides.engineArgs`, profile `engineArgs`, runtime config, and profile defaults. |
| `AIM_ADAPTER_MAX_RANK` | Adapter controller | Static mode: inferred from the largest declared adapter rank. Dynamic mode: `AIMService.spec.adapterRuntime.maxRank`, then namespace/cluster RuntimeConfig, then default `32`. |

### Environment Variable Merge Order

For variables that are not framework-owned, the most specific value wins:

:::{admonition} Framework-owned variables
:class: important

`AIM_ENGINE`, `AIM_PROFILE_ID`, `AIM_ID`, `AIM_MODEL_ID`, and
`AIM_CACHE_PATH` are assembled from the resolved profile and cache state after
normal environment merging. Values with those names in service or profile
`containerEnv` do not override the operator-managed values. Configure the
corresponding profile fields instead.
:::

1. `AIMService.spec.env` (highest priority)
2. `AIMService.spec.profileOverrides.engineEnv` / `containerEnv` (overlay on top of the resolved profile)
3. Resolved `AIMProfile` / `AIMClusterProfile` env (`spec.engineEnv`, `spec.containerEnv`, plus profile-derived vars such as metric/precision)
4. Merged runtime config env (`AIMRuntimeConfig.spec.env` overriding `AIMClusterRuntimeConfig.spec.env`)
5. Operator defaults (lowest priority)

## Next Steps

- [Model Caching Guide](../guides/model-caching.md) — Download protocol configuration
- [Private Registries](../guides/private-registries.md) — Authentication environment variables
- [CLI and Operator Flags](cli.md) — Operator binary flags
