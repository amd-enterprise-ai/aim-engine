# Administrator configuration

Runtime configs provide composition-specific selector defaults and named local
transport presets. They do not impose mandatory model policy or rank candidate
profiles.

The service selects a config with top-level `runtimeConfigName`. When omitted,
AIM looks for `default`. It merges same-named cluster and namespace configs;
namespace-supplied fields win. A missing implicit `default` is allowed. A missing
explicitly selected non-default config is a resolution error.

## Provide reasonable selector defaults

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  composition:
    selectorDefaults:
      common:
        engine: vllm
      prefill:
        metric: throughput
      decode:
        metric: latency
```

This supplies a useful starting point for a catalog with these engine/metric
variants. It does not assert that every model has both variants or that they
interoperate. If a default eliminates all candidates, resolution reports that
default; it never silently drops it.

A namespace can add precision without repeating the other settings:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMRuntimeConfig
metadata:
  name: default
  namespace: team-a
spec:
  composition:
    selectorDefaults:
      common:
        precision: bf16
```

For a minimal P/D service in `team-a`, effective selectors now contain:

| Position | Shared model restriction | Engine | Metric | Precision |
| --- | --- | --- | --- | --- |
| Prefill primary/worker | The service's `modelRef` | `vllm` | `throughput` | `bf16` |
| Decode primary/worker | The service's `modelRef` | `vllm` | `latency` | `bf16` |

Only workers present in the recipe are created. Defaults never add workers.

## Selection-default algorithm

For each primary and worker binding:

1. Load the service-selected runtime config name. Merge cluster and namespace
   `selectorDefaults` by block (`common`, `main`, `prefill`, `decode`) and supplied
   scalar field. Namespace wins within the same block and field.
2. Merge the effective `common` block with that serving role's block. Role wins.
   A namespace common value therefore does **not** erase an inherited
   cluster role-specific value.
3. Intersect every applicable authored recipe and service restriction. Include
   `spec.model.name` as provenance and retain exact reference constraints.
4. Resolve `inheritSelectorDefaults`: service, then recipe, then `true`.
5. If inheritance is enabled and no exact reference fixes this position, fill
   only unconstrained fields from the merged defaults.
6. Apply existing deployable eligibility, tier policy, and candidate ordering,
   then validate combinations jointly.

Defaults are applied per binding, not once before recipe inheritance.
Explicit selectors on one role do not suppress defaults for another role.
A worker uses its own authored selector and its role's defaults, not the
primary binding's selector.

### Precedence examples

| Inputs | Result |
| --- | --- |
| Cluster common `precision: fp8`; namespace common `precision: bf16` | Effective common default is `bf16`. |
| Cluster decode `precision: fp8`; namespace common `precision: bf16` | Decode default remains `fp8`; other roles use `bf16`. |
| Runtime decode `metric: latency`; service decode selector `metric: throughput` | Decode uses `throughput`. |
| Recipe decode selector `metric: latency`; service decode selector `metric: throughput` | Contradiction; no fallback. |
| Runtime precision `bf16`; exact fp8 profile reference | Default is skipped for that position. Authored precision restrictions still apply. |
| Empty `selector: {}` | No new restriction; defaults still fill gaps. |
| `inheritSelectorDefaults: false` | No runtime selector defaults; ordinary eligibility and platform requirements remain. |

If a shared service selector already specifies `metric: throughput`, it constrains
both roles and their workers. The default decode metric cannot override it.

### Supported default fields

Common and role blocks accept:

- `engine`, `metric`, `precision`;
- `acceleratorType`, `acceleratorVendor`, `acceleratorModel`;
- `acceleratorPartitioningMode`, `acceleratorCount`.

They do not accept model identity/provenance, `engineArgs`, catalog role, tier
floors, ordered fallback lists, or scoring weights. Use profile-generation
fallbacks for generating missing profiles; selector defaults only filter existing
ones. Use authorization/admission policy for mandatory tenant restrictions.

Empty objects and omitted fields inherit rather than clear settings. To replace
a cluster role default, set that same role field in the namespace config.
To opt a service out, use `inheritSelectorDefaults: false`.

No engine, metric, precision, or accelerator is hard-coded when a default and an
authored restriction are both absent. Existing eligible-profile selection still
applies; this does not disable the optimized-tier policy.

## Configure local transfer

A P/D composition selects `transport.preset`, or uses the runtime config's
`defaultTransportPreset` when no preset is authored:

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  composition:
    defaultTransportPreset: local-kv
    transportPresets:
      - name: local-kv
        integration: amd-local-kv
```

`amd-local-kv` is a site-defined example identifier. Use an integration registered
in your installed bundle. An unknown integration, unresolved preset, or absent
required transport input blocks construction. This example is not a declaration
that a connector with that name is shipped or qualified.

Each integration contract specifies concrete versions and mappings for:

- Engine, connector, protocol, runtime and sidecar images.
- Required role launch arguments/environment and model/profile paths.
- GPU topology and accessible devices.
- Pod permissions, IPC/sharing, volumes, ports, probes, and security restrictions.
- Hard same-node placement across the complete service lifecycle.
- Statically checkable interoperability rules and observable runtime signals.

The preset names that contract; it does not provide arbitrary `PodSpec` overrides.
No blanket privileged or `hostIPC` default is introduced. Selecting a preset
does not provision fabric, grant access to namespace-local Secrets, or certify
the compatibility of arbitrary profile pairs.

Namespace transport entries replace same-named cluster entries **as a whole**.
This avoids constructing a hybrid connector contract by merging unrelated
fragments. A namespace can override `defaultTransportPreset` without redefining
the selected entry. Duplicate names within one config are invalid.

Aggregated composition does not select a P/D preset. A transport block on an
aggregated recipe is invalid rather than ignored.

## Installation and conformance

Install a matching bundle of controller, CRDs, deployment presets, Gateway,
inference extensions, engine, sidecars, and connector. Merely upgrading a Go
dependency or changing a CUDA image to a ROCm image does not establish an
operational integration.

Conformance checks inspect the **effective resources after KServe preset
merging**, not only the generated AIM config. Verify image/entrypoint, flags/env,
ports/probes, sidecars, resources, profile YAML mounts, storage, placement, and
permissions.

For AIM-managed external serving, AIM owns the HTTPRoute and associated security
intent; KServe observes the referenced route and owns its endpoint-selection
resources. Role autoscalers also belong to KServe. Do not enable the legacy
predictor-specific autoscaler against composition targets.

The public route can temporarily have unresolved backend references while
KServe creates its supporting resources. Report the wait; do not make route
resolution a prerequisite that prevents creating the native service.

## Changing defaults safely

Changes to runtime configs do not silently reselect running compositions.
Existing services retain their accepted selectors, effective launch settings,
transport content, source provenance, and bundle identity.

New services use the new config. Existing services adopt it on an explicit
execution update or `refreshToken` change. Capacity-only updates retain the
pinned execution and defaults.

The service records both namespace and cluster contributions, their content
digests, and the selector fields actually defaulted. Inspect
`status.composition.bindings[*].selectorContributions` and
`status.composition.applied.runtimeConfigSources` when explaining a selection.

Pinning does not freeze changes made directly to external controller behavior,
mutable image tags, Secrets, PVC contents, or platform presets. Changes to those
surfaces require conformance testing and drift visibility.
