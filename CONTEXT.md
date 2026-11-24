# Runtime Projection

How AIM Engine projects KServe serving runtimes from AIM profiles, and how
`AIMService` consumes them. It is a glossary only — no implementation detail.

## Language

**Runtime**:
A KServe serving runtime projected from an AIM profile — a `ServingRuntime`
(namespace-scoped) or `ClusterServingRuntime` (cluster-scoped). Always a
*projection* of a profile, never hand-authored.
_Avoid_: CSR/SR when speaking generally (use "runtime"); say SR/CSR only for the
specific scope.

**Per-profile runtime**:
A runtime projected 1:1 from a single projectable profile, named
`aim-<profile.Name>` (the reserved `aim-` prefix), with `autoSelect` off. Meant
to be referenced explicitly by name.

**Model-slug primary**:
A single runtime per model, named by the model slug, projecting the *primary*
profile's spec. The portable, vendor-independent handle native KServe references
explicitly by name. `autoSelect` is off (like every projected runtime): all
runtimes share one model format, so `autoSelect` would collide across models
rather than resolve a slug. Exists only when aliasing is enabled.

**Projectable profile**:
A profile eligible to produce a runtime: deployable, has an image, and has at
least one matching node. Non-projectable profiles produce no runtime.

**Eager projection**:
Runtime creation driven by the profile reconciler, for native-KServe
discoverability. Governed by the projection mode.

**Lazy projection**:
Runtime creation driven by an `InferenceService` watch: when an ISVC references a
runtime name that does not resolve in its namespace, the runtime is projected
into that namespace on demand. Always on; never governed by the mode. The
mechanism that makes a namespaced `AIMService` referencing a cluster profile work.

**Projection mode**:
The eager-projection knob: `Exhaustive` (per-profile runtimes only), `Reduced`
(model-slug primary only), or both. Governs eager projection only — lazy
projection always runs.
_Avoid_: "creation mode", "CSR mode" (the mode is runtime-neutral).

**Framework env**:
The profile-derived `AIM_*` environment that locates the model weights and mounted
profile (`AIM_PROFILE_ID`, and the `modelSources`-conditional `AIM_ID` /
`AIM_MODEL_ID` / `AIM_CACHE_PATH` set). Profile-derived, so it belongs on the
runtime — not service-derived.

**Profile-owned cache**:
A model-weights cache (PVC) whose lifecycle is tied to a profile
(`profile.spec.caching.enabled`), colocated with it. Mountable by the runtime,
making the runtime self-contained for caching.
_Avoid_: conflating with the service-owned cache.

**Service-owned cache**:
A model-weights cache (PVC) tied to a single `AIMService`
(`service.spec.caching.mode`). Service-specific, so its volume + redirect env live
on the ISVC overlay, not the runtime.

**Overlay**:
The service-specific fields an `AIMService` layers onto the runtime it references:
service-owned cache, autoscaling, auth, the runtime config and service env, and
service-level resource / pull-secret / service-account overrides. Everything else
(image, base resources, affinity, profile ConfigMap, framework env) lives on the
runtime.

**Reserved `aim-` prefix**:
AIM Engine names every runtime it projects `aim-<profile name>` and owns that
prefix exclusively. Hand-authored runtimes use any other name and are never read,
adopted, or modified by AIM Engine — so they can never collide with a projection.

**Authoritative apply**:
Because projected runtime names live under the reserved `aim-` prefix, AIM Engine
is always authoritative over them: it force-applies (SSA + `ForceOwnership`)
unconditionally — no pre-apply `Get`, no per-name branching, no conflict
condition. SSA force reconciles drift on the fields AIM Engine owns while leaving
additive fields set only by other managers intact.
