# Deploy a composition

Use `AIMService.spec.composition` to serve an existing profile through aggregated
execution or combine profiles into separate prefill and decode roles.

## Before you begin

You need:

- AIM Engine with composition support and the installed integration's matching
  KServe CRDs, controller, presets, and supporting controllers.
- Deployable profiles for your actual model artifacts and available hardware.
  The examples use a publishing model named `qwen3-32b`.
- For P/D, a configured local-transfer preset and a common eligible node large
  enough for every role group. Ask your administrator which preset/integration
  is available; a generic profile engine label is not a transport configuration.
- Namespace-local model-cache storage, credentials, and image pull access for
  each participating execution position.
- For external requests, an administrator-provided Gateway and the applicable
  authorization policies.

The example namespace is `team-a`. Names such as `existing-profile`,
`qwen-primary`, and `local-kv` stand for entries in your catalog and platform
configuration; substitute your own. This guide does not ask you to deploy
unqualified container images.

## Start with model and mode

Save the following as `qwen-chat.yaml`, or use
[the complete manifest](examples/01-minimal.yaml):

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-chat
  namespace: team-a
spec:
  model:
    name: qwen3-32b
  composition:
    mode: PrefillDecode
```

```bash
kubectl apply --validate=strict -f qwen-chat.yaml
kubectl get aimservice qwen-chat -n team-a -o yaml
kubectl wait aimservice/qwen-chat -n team-a \
  --for=condition=Ready --timeout=10m
```

This expands to one prefill group and one decode group on the same node.
Both select profiles produced by `qwen3-32b`. Runtime-config defaults fill
unconstrained fields. For example, an administrator can configure throughput
for prefill and latency for decode. Those metric choices are not hidden built-in
ranking rules.

No `template`, top-level replica count, `spec.profile`, or pipeline annotation is
needed. A missing profile does not cause AIM to create a model or derive weights.
If no profiles match, inspect `status.composition.findings`.

The timeout above is a client wait limit, not a startup-time guarantee. Model
downloads and busy compatible hardware can take longer.

## Make role choices explicit

Mode can be inferred from `prefill` and `decode`:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-inline
  namespace: team-a
spec:
  model:
    name: qwen3-32b
  composition:
    prefill:
      profile:
        selector:
          metric: throughput
    decode:
      replicas: 2
      profile:
        selector:
          metric: latency
```

There are now three complete groups in the admission footprint: one prefill and
two decode. Omitting `prefill` while keeping `decode` would still create the
default prefill role.

The two roles can select the same source profile if their effective launch
settings pass joint validation. AIM does not require differently named profiles
as proof of role separation.

## Serve one exact profile

An aggregated composition needs no P/D transport:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: chat-aggregated
  namespace: team-a
spec:
  composition:
    main:
      profile:
        ref:
          kind: AIMProfile
          name: existing-profile
```

`main` implies `Aggregated`. Use `kind: AIMClusterProfile` for a cluster profile.
Exact references bypass automatic candidate selection and runtime selector
defaults, but not authored restrictions or construction checks. AIM materializes
the effective runtime profile YAML in the service namespace.

The comparable non-composition service uses top-level `profile.name` and stays
on the existing `InferenceService` path. Create a separate service to change
paths; do not add/remove composition on an existing object.

## Publish a reusable recipe

A model-agnostic recipe can fix the serving shape without naming a model:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterComposition
metadata:
  name: local-pd
spec:
  mode: PrefillDecode
  placement:
    scope: SameNode
  transport:
    preset: local-kv
  profileSelector:
    engine: vllm
  prefill:
    profile:
      selector:
        metric: throughput
  decode:
    profile:
      selector:
        metric: latency
```

Publishing this object starts no workload. Its model-binding classification is
`ModelAgnostic`. Instantiate it:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-recipe
  namespace: team-a
spec:
  model:
    name: qwen3-32b
  composition:
    ref:
      kind: AIMClusterComposition
      name: local-pd
    decode:
      replicas: 2
```

The consumer inherits both metric restrictions and changes decode capacity.
Its model restriction does not make the shared recipe model-specific.

Use `AIMComposition` with `metadata.namespace: team-a` for a namespace-scoped
recipe and `ref.kind: AIMComposition` when consuming it. See
[the namespace recipe example](examples/05-namespace-recipe.yaml).
Recipes cannot refer to other recipes.

## Select by actual model identity

If profiles are not published by a model resource, use their declared model
identity instead:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: qwen-by-artifact
  namespace: team-a
spec:
  composition:
    mode: PrefillDecode
    profileSelector:
      modelId: Qwen/Qwen3-32B
      engine: vllm
```

This filters existing profiles; it does not download arbitrary replacement
weights into a profile. Author or derive profiles separately for custom weights.
Joint validation still compares concrete artifact and runtime requirements.

## Customize launch, resources, and capacity

Role `launch` is a service-specific overlay. It does not mutate the source
profile. For example:

```yaml
spec:
  composition:
    ref:
      kind: AIMClusterComposition
      name: local-pd
    decode:
      replicas: 2
      launch:
        containerEnv:
          - name: DEPLOYMENT_PURPOSE
            value: team-chat
        engineArgs:
          max-model-len: 8192
      resources:
        requests:
          cpu: "8"
          memory: 64Gi
        limits:
          cpu: "16"
          memory: 128Gi
```

This is a spec fragment to combine with the named-consumer example. The GPU
requirements remain inherited from the selected profile. CPU/memory values and
the engine argument are examples, not sizing recommendations; use values
supported by the actual profile and integration.

For demand-based scaling, replace `replicas` with a complete `scaling` block.
[The scaling manifest](examples/09-launch-scaling.yaml) includes a role-local
OpenTelemetry metric, launch/resources, and Gateway routing. It targets complete
decode groups, not the legacy predictor deployment.

If a recipe sets `replicas: 2` and the consumer supplies `scaling`, the consumer's
capacity choice replaces the inherited fixed count. Do not put both fields in
one role object.

For a supported cooperating-worker mapping, use
[the worker manifest](examples/10-worker.yaml). No separate worker replica count
is needed. The configured parallelism must be realizable by that bundle on one
node; a syntactically valid worker shape is not automatically supported.

## Control inherited defaults

An explicit selector can replace a runtime default:

```yaml
spec:
  model:
    name: qwen3-32b
  composition:
    mode: PrefillDecode
    decode:
      profile:
        selector:
          metric: throughput
```

This selects throughput decode even if the runtime config defaults decode to
latency. It would **not** override `metric: latency` authored in a named recipe:
that combination is a contradiction.

To skip runtime selector defaults entirely, set:

```yaml
spec:
  model:
    name: qwen3-32b
  composition:
    mode: PrefillDecode
    inheritSelectorDefaults: false
```

Platform transport/security requirements, deployable-profile eligibility, and
authored selectors still apply. Empty selectors do not opt out. See
[the full defaulting algorithm](runtime-config.md#selection-default-algorithm).

## Inspect and refresh

```bash
kubectl get aimservice qwen-chat -n team-a \
  -o jsonpath='{.status.composition.bindings}'
kubectl get aimservice qwen-chat -n team-a \
  -o jsonpath='{.status.composition.applied}'
```

The bindings show each primary/worker choice, effective selector, and which
fields came from defaults. The applied projection points to the config and
persisted content used by replacement pods.

To adopt changed source profiles, recipe content, or runtime defaults:

```bash
kubectl patch aimservice qwen-chat -n team-a --type=merge \
  -p '{"spec":{"composition":{"refreshToken":"catalog-refresh-1"}}}'
```

Use a new token for each request. Leaving this token in place does not reselect
on later catalog changes. The legacy `force-rebind` annotation is not the
composition refresh interface.

Always inspect the desired attempt as well as `Ready` after requesting refresh:
a rejected desired revision can leave the previous applied revision healthy.
See [Operations and troubleshooting](operations.md).
