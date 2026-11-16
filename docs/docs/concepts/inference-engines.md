<!--
Copyright © Advanced Micro Devices, Inc., or its affiliates.

SPDX-License-Identifier: MIT
-->

# Inference Engines

An `AIMProfile` declares the inference engine and the engine-specific runtime
configuration used by its projected KServe runtime. AIM Engine currently has
explicit serving behavior for `vllm` and `vllm_omni`.

## Common runtime contract

For v1alpha2 profiles, AIM Engine projects `spec.engine` onto the inference
container as `AIM_ENGINE`. The runtime uses that value to select the matching
engine configuration before loading the projected profile.

The following identity and cache variables are framework-owned:

- `AIM_ENGINE`
- `AIM_PROFILE_ID`
- `AIM_ID`
- `AIM_MODEL_ID`
- `AIM_CACHE_PATH`

Values supplied through profile or service `containerEnv` cannot override these
variables. Use profile fields such as `engine`, `profileId`, `aimId`, and
`modelSources` instead.

Engine configuration is split across two profile fields:

| Field | Purpose |
|---|---|
| `engineArgs` | Engine command-line arguments assembled by the AIM runtime. |
| `engineEnv` | Environment variables passed to the engine subprocess. |

### Projected profile filename

For profiles discovered from an AIM image, `spec.profileId` is authoritative:
the projected ConfigMap key and `AIM_PROFILE_ID` retain that exact filename
stem. This preserves names such as
`vllm_omni-mi300x-fp16-tp4-latency-usp4`. Derived profiles and service overlays
that override the accelerator model or count regenerate a `profileId` that is in
the generated form below, so the filename names the derived hardware.

For a hand-authored profile without `spec.profileId`, AIM Engine generates:

```text
<engine>-<accelerator>-<precision>-tp<count>-<metric>[-<variant>].yaml
```

Legacy hand-authored profiles with an empty `spec.engine` retain `vllm` as the
fallback filename prefix.

### Shared memory

Every projected runtime mounts a memory-backed `/dev/shm`. Its size currently
depends on the engine:

| Engine | `/dev/shm` size |
|---|---:|
| `vllm` and other engines | 8 GiB |
| `vllm_omni` | 32 GiB |

## vLLM

Use the canonical identifier:

```yaml
spec:
  engine: vllm
```

AIM Engine emits `AIM_ENGINE=vllm` and projects the profile's `engineArgs` and
`engineEnv`. Parallelism remains profile-defined; the generated filename's
historical `tp<count>` segment identifies the accelerator count but does not
replace explicit engine arguments.

vLLM runtimes use the default 8 GiB `/dev/shm`.

## vLLM-Omni

Use the canonical identifier with an underscore:

```yaml
spec:
  engine: vllm_omni
  variant: usp4
```

AIM Engine emits `AIM_ENGINE=vllm_omni` and allocates 32 GiB for `/dev/shm`.

### USP profiles and variants

vLLM-Omni profiles can carry variants such as `usp1`, `usp2`, or `usp4`.
Discovery preserves the image profile's `metadata.variant` in
`AIMProfile.spec.variant` and preserves its original `profileId`.

`variant` is identity metadata, not currently an AIMService selector or ranking
axis. To require a specific USP recipe, pin the exact profile:

```yaml
spec:
  profile:
    name: wan2-2-t2v-a14b-diffusers-vllm-omni-mi300x-fp16-tp4-la-<hash>
```

`acceleratorCount` controls Kubernetes accelerator allocation. USP execution is
controlled by the profile's engine configuration (for example an `usp` engine
argument). Tensor parallelism and USP are not interchangeable, and AIM Engine
does not infer one from the other.

### Multimodal APIs and model layouts

Endpoints such as `/v1/videos` are provided by the selected AIM runtime image;
they are not represented by separate AIM Engine CRDs. Consult the image's model
documentation for its supported request and response schema.

Diffusers-style model repositories commonly require files in nested
subdirectories. That is a model artifact layout concern rather than an engine
setting. See
[Repositories with required subdirectories](../guides/model-caching.md#repositories-with-required-subdirectories)
for the required download-filter configuration.

## Related documentation

- [Profiles](profiles.md) — Profile fields, selection, and runtime projection
- [Environment Variables](../reference/environment-variables.md) — Complete
  inference-container variable reference
- [Model Caching](../guides/model-caching.md) — Artifact download configuration
