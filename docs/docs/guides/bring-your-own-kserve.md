<!--
Copyright © Advanced Micro Devices, Inc., or its affiliates.

SPDX-License-Identifier: MIT
-->

# Bring Your Own KServe

AIM Engine projects each deployable `AIMProfile` / `AIMClusterProfile` into a native KServe **`ServingRuntime`** (namespace-scoped) or **`ClusterServingRuntime`** (cluster-scoped). This means you don't have to go through `AIMService` at all: you can author your own KServe `InferenceService` and point it at an AIM-projected runtime by name. This guide covers how to discover those runtimes, reference them safely, and read their health.

:::{admonition} v1alpha2
:class: note

Runtime projection is a v1alpha2 feature. It is driven by `AIMProfile` / `AIMClusterProfile`, not the deprecated v1alpha1 `AIMServiceTemplate` shape.
:::

## Why projection exists

An `AIMProfile` already carries everything a runtime needs to serve a model — image, accelerator requirements, resolved resources, engine config, and (optionally) a warmed cache. Projection turns that into a first-class KServe object so that:

- **Native KServe users** can reference a runtime directly from a hand-authored `InferenceService`.
- **`AIMService`** consumes the same projected runtime instead of inlining a predictor, so the managed and bring-your-own paths converge on one object.

## Discover a projected runtime

### From the profile status

Every projectable profile publishes the name(s) of the runtime it projects on its status, so you never have to guess or reverse the (hashed) object name:

```bash
kubectl get aimclusterprofile qwen3-32b-mi300x \
  -o jsonpath='{.status.projectedRuntimeName}{"\n"}'
# aim-qwen3-32b-mi300x-1a2b3c4d

# In Reduced / Both mode, the readable model-slug primary is published too:
kubectl get aimclusterprofile qwen3-32b-mi300x \
  -o jsonpath='{.status.projectedModelSlugRuntimeName}{"\n"}'
# aim-qwen-qwen3-32b
```

### By label

Projected runtimes and their colocated ConfigMaps carry correlator labels, so you can list them without knowing names:

```bash
# All runtimes AIM Engine projects
kubectl get clusterservingruntime -l app.kubernetes.io/managed-by=aim-engine

# Runtimes for one model, with their projection state shown as a column
kubectl get servingruntime -n ml-team \
  -l aim.eai.amd.com/model=qwen-qwen3-32b \
  -L aim.eai.amd.com/runtime-projection-state
```

The full label / annotation set stamped on projected runtimes is documented in [Naming and Labels — Projected runtime metadata](../reference/naming-and-labels.md#projected-runtime-metadata).

## Reference a runtime from your own InferenceService

Point `predictor.model.runtime` at the projected runtime name. Because the runtime already carries the image, resources, env, and profile mount, your `InferenceService` stays minimal:

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: my-own-endpoint
  namespace: ml-team
spec:
  predictor:
    model:
      runtime: aim-qwen-qwen3-32b   # a projected runtime name
      modelFormat:
        name: huggingface           # must match the runtime's advertised format
```

KServe resolves an explicit `runtime:` name against a namespace `ServingRuntime` first, then a `ClusterServingRuntime` of the same name.

:::{admonition} `modelFormat` must be `huggingface`
:class: important

Every projected runtime advertises a single `supportedModelFormats` entry — `huggingface` — with `autoSelect` **off**. `modelFormat.name` is required by KServe on `predictor.model`, and because you reference the runtime by explicit name, the format you set must match what the runtime advertises. Use `huggingface`; any other value (or relying on auto-selection) will not bind. `autoSelect` is deliberately off so that unrelated models sharing this one format can't collide in KServe's format-based selection.
:::

## Reference a profile from your own LLMInferenceService

When the KServe LLMInferenceService controller is installed (see [LLMInferenceService support](../admin/kserve-configuration.md#llminferenceservice-support)), every projected runtime has a KServe `LLMInferenceServiceConfig` of the **same name**: in the profile namespace for an `AIMProfile`, and in the KServe namespace (`kserve-system` by default) for an `AIMClusterProfile`. The profile's `LLMInferenceServiceConfigProjected` condition reports whether it is projected.

Reference it from `spec.baseRefs`:

```yaml
apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceService
metadata:
  name: my-own-llm-endpoint
  namespace: ml-team
spec:
  baseRefs:
    - name: aim-qwen-qwen3-32b   # a projected runtime / config name
  model:
    uri: hf://Qwen/Qwen3-32B
    name: Qwen/Qwen3-32B
  replicas: 1
```

The config carries the image, resources, node affinity, profile mount, and AIM environment, and disables KServe's storage initializer: the AIM runtime downloads the model itself, as it does behind an `InferenceService`. `model.uri` is required by KServe but only informational here. KServe looks a base config up in the service namespace first, then in the KServe namespace. For a cluster profile, AIM Engine materializes a complete namespaced copy of the config (and its profile ConfigMap) as soon as an `LLMInferenceService` references it, and KServe re-renders onto that copy within a few seconds.

## The reserved `aim-` prefix

AIM Engine owns the `aim-` name prefix for runtimes. Two naming schemes live under it:

| Name shape | Example | `autoSelect` | Referenced by |
|---|---|---|---|
| Per-profile (hashed) | `aim-qwen3-32b-mi300x-1a2b3c4d` | off | Machine/tooling — deterministic per profile |
| Model-slug primary (readable) | `aim-qwen-qwen3-32b` | on | Humans / native `InferenceService`s — one stable name per model |

:::{admonition} Do not hand-author `aim-*` runtimes
:class: warning

The `aim-` prefix is reserved. AIM Engine force-applies (authoritatively reconciles) any runtime under it, so a hand-authored `aim-...` object will have its spec overwritten. The per-profile names include an unguessable hash precisely so an operator-managed name can never collide with something you created. If you author your own runtime, give it a name that does **not** start with `aim-`.

Treat generated `aim-*` runtimes and their same-name ConfigMaps as read-only.
Mutating their fields or metadata is unsupported. Force-apply reclaims fields
present in AIM Engine's desired object, but server-side apply does not
necessarily remove extra fields owned by another field manager. If an object
does not reconverge after you undo a mutation, delete the generated runtime and
ConfigMap together so AIM Engine can recreate them. Deletion can interrupt
active consumers.
:::

## Projection modes

An administrator selects how many runtimes get projected via the operator flag `--runtime-projection-mode` (Helm value `manager.runtimeProjectionMode`):

| Mode | What is projected | Tradeoff |
|---|---|---|
| `Exhaustive` | One per-profile runtime for every deployable profile | Maximum addressability; more runtime objects |
| `Reduced` | One readable model-slug primary per model (`autoSelect` on) | Fewer objects, one stable handle per model; individual profiles aren't separately addressable |
| `Both` (default) | Per-profile runtimes **and** the model-slug primary | Most objects; both addressing styles available |

Model-slug aliases are best-effort across lifecycle and mode transitions. While
model-slug publication is active, a published alias is retained when its
winning profile becomes temporarily unprojectable, but it is not refreshed
until that profile is projectable again. In `Exhaustive` mode model-slug
election is disabled, so a retained historical alias can continue to point at
its previous winner until `Reduced` or `Both` reenables election.

Check the active mode on the running operator:

```bash
kubectl -n aim-engine-system get deployment aim-engine-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].args}'
```

## Shadow (lazy) lifecycle

A `ClusterServingRuntime` cannot mount a namespaced profile ConfigMap, so a projected CSR is a **shadow target**, not a standalone-servable runtime: it carries `AIM_PROFILE_ID` pointing at a profile file that only materializes once the runtime is completed in your namespace.

When you create an `InferenceService` that references such a CSR, AIM Engine's InferenceService watcher **materializes a namespace `ServingRuntime` shadow of the same name** in your namespace — a complete runtime plus its colocated profile ConfigMap. From then on your predictor mounts a real profile file.

:::{admonition} First-reference reconvergence window
:class: note

If your pod binds the bare CSR in the brief window before the shadow lands, it can come up pointing at a profile file that isn't mounted yet and restart until the shadow is created (a few seconds; it self-heals). The window only affects the very first consumer of a runtime in a namespace. Model-slug primaries and per-profile namespace projections created by the profile itself don't have this window.
:::

## Consistency and rollout guarantees

A projected runtime and its profile ConfigMap are two Kubernetes objects, so
their creation and update cannot be atomic. On initial force-owned publication,
AIM Engine applies the ConfigMap first and stops if that apply fails; it will
not publish the consuming runtime in that reconcile. A failure after the
ConfigMap succeeds can still leave only the ConfigMap until the next reconcile.
During updates either sibling can briefly hold the previous revision.

`AIMService` waits until the observed runtime and ConfigMap carry the expected
projection content hash before creating or updating its InferenceService. This
reduces mixed-version exposure for AIMService-managed workloads, but does not
make the sibling writes transactional and cannot control native KServe
consumers.

The ConfigMap name is stable. A ConfigMap-only data change therefore does not
change the pod template and does not by itself guarantee a KServe rollout.
Kubernetes eventually updates mounted ConfigMap volumes, but a serving process
that reads its profile only at startup keeps the old configuration until its
predictor pod restarts. Restart affected workloads explicitly when immediate
adoption is required.

## Read runtime health

Projected runtimes surface their state **on the runtime object itself**, so you don't have to hop to the backing profile:

```bash
kubectl get servingruntime aim-qwen-qwen3-32b -n ml-team \
  -L aim.eai.amd.com/runtime-projection-state
# NAME                 ...   RUNTIME-PROJECTION-STATE
# aim-qwen-qwen3-32b   ...   projected
```

- `aim.eai.amd.com/runtime-projection-state=projected` — AIM Engine is actively projecting this runtime from a healthy, projectable profile.
- `aim.eai.amd.com/runtime-projection-message` — a human-readable companion note.
- `aim.eai.amd.com/runtime-projection` — whether the runtime was materialized `eager`ly (by the profile) or `lazy`ly (as a shadow). This is provenance, not health.

### How a runtime can be "degraded" while the object still exists

Projection is **additive with asymmetric teardown**: AIM Engine creates a runtime only while its profile is projectable, but it never deletes a runtime just because the projection gate later flips (e.g. the matching GPU nodes drain away, or the profile stops being deployable). The runtime is kept — a native `InferenceService` referencing it is not yanked out from under a running workload — and is removed only when the backing profile is **deleted** (owner-reference garbage collection).

In that kept-but-ungated state the profile reports `RuntimeProjected=False` with reason `RuntimeDegraded` (see [Conditions — RuntimeProjected](../reference/conditions.md#runtimeprojected)). The `runtime-projection-state` label on the runtime object stays `projected` today — it marks provenance, not the live transition — so the **authoritative degraded signal is the backing profile's condition**. Follow the runtime back to its profile:

```bash
# The runtime records its backing profile (untruncated) as an annotation
PROFILE=$(kubectl get clusterservingruntime aim-qwen3-32b-mi300x-1a2b3c4d \
  -o jsonpath='{.metadata.annotations.aim\.eai\.amd\.com/projected\.profile}')

kubectl get aimclusterprofile "$PROFILE" \
  -o jsonpath='{.status.conditions[?(@.type=="RuntimeProjected")]}{"\n"}'
```

The runtime also carries an owner reference to that profile, so `kubectl get ... -o jsonpath='{.metadata.ownerReferences}'` is an equivalent way back.

## See also

- [Profiles](../concepts/profiles.md) — what an `AIMProfile` carries
- [Conditions — RuntimeProjected](../reference/conditions.md#runtimeprojected) — the projection health condition
- [Naming and Labels — Projected runtime metadata](../reference/naming-and-labels.md#projected-runtime-metadata) — the full label / annotation set
- [Deploying Services](deploying-services.md) — the managed `AIMService` path that consumes the same runtimes
