# Operations and troubleshooting

Composition separates the requested configuration from the applied serving
revision. Inspect both when troubleshooting or rolling out a change.

## Inspect a service

```bash
kubectl get aimservice qwen-chat -n team-a -o yaml
kubectl describe aimservice qwen-chat -n team-a
kubectl get aimservice qwen-chat -n team-a \
  -o jsonpath='{.status.composition.desired}'
kubectl get aimservice qwen-chat -n team-a \
  -o jsonpath='{.status.composition.applied}'
```

`status.composition.bindings` identifies the profile at every primary/worker
position and its effective selector. `selectorContributions` explains authored
restrictions and defaults. `roles` reports group counts for each serving role.

The applied record identifies the generated config, snapshot, native service,
runtime-config contributions, and content/bundle digests. Use the returned
resource names; do not assume a naming suffix:

```bash
kubectl get llminferenceserviceconfigs.serving.kserve.io -n team-a
kubectl get llminferenceservices.serving.kserve.io -n team-a
```

Do not hand-edit AIM-managed native configs, profile ConfigMaps, or `baseRefs`.
Edit the AIMService or source objects and request the appropriate refresh.

## Desired, prepared, applied, and ready

| State | Meaning |
| --- | --- |
| `desired` | Latest evaluated intent, including its service generation and refresh token. May fail before an effective revision exists. |
| `prepared` | Effective snapshot, dependencies, and generated config ready for native publication. |
| `applied` | Projection selected by the native service's managed `baseRefs`. |
| Serving health | Observed workload, route, storage, and transport state for the applied path. |

Publication proceeds in order: persist the candidate snapshot; publish its
dependencies; publish the generated config; update the native service's
`baseRefs`; observe the result. This is recoverable reconciliation, not an
atomic multi-resource transaction.

Applied does not mean healthy. KServe can still be rendering, scheduling, loading
weights, or rolling pods. Likewise, an applied healthy service can have a
rejected desired refresh.

### Conditions

All conditions carry standard `status`, `reason`, `message`,
`observedGeneration`, and `lastTransitionTime` fields.

| Condition | Tracks | Typical reasons |
| --- | --- | --- |
| `CompositionResolved` | Latest desired selection and joint validation | `Resolved`, `ReferenceNotFound`, `SelectorConflict`, `NoMatchingProfiles`, `NoFeasibleTuple`, `UnsupportedShape`, `MissingTransport`, `MixedGenerationUnsafe`. |
| `RevisionPrepared` | Latest desired snapshot, dependencies, and config | `Prepared`, `PreparingDependencies`, `DependencyUnavailable`, `ManagedObjectCollision`. |
| `ArtifactsReady` | Artifacts needed by the applied serving path | `Available`, `Downloading`, `DependencyUnavailable`. |
| `LLMInferenceServiceReady` | Applied native workload health | `Ready`, `Progressing`, `NativeFailure`. |
| `HTTPRouteReady` | Public route, if enabled | `Accepted`, `AwaitingBackends`, `RouteRejected`. |
| `CapacityAvailable` | Scheduling of requested applied groups | `Available`, `WaitingForCapacity`, `NodeRecoveryPending`. |
| `TransportHealthy` | Native/runtime transport signals for the applied path | `NoFailureObserved`, `TransferObserved`, `FallbackObserved`, `RecomputeDetected`, `LeaseFailure`, `TransportFailure`, `NotApplicable`. |
| `SourcesCurrent` | Availability/drift of recorded external sources | `Current`, `SourceChanged`, `SourceDeleted`, `BundleDrift`. Informational. |
| `Ready` | Availability of the applied requested serving path | `Serving`, `Progressing`, `ServingFailure`. |

The first two conditions describe desired intent; they are not blindly ANDed
into applied availability. `Ready=True` can remain while `CompositionResolved`
is false for a rejected update. In that case high-level `status` is `Degraded`,
and the desired reason explains what was not adopted. With no applied revision,
rejection cannot report a ready service.

Transport health with `NoFailureObserved` is not proof of KV movement.
`status.composition.transport.state: Unobserved` remains explicit until transfer
is observed. Unknown static interoperability is not by itself a readiness
failure. Known observed transport failure prevents `Ready=True` even if pods are
healthy.

When scale-to-zero is configured, availability depends on the supported native
activation path rather than a promise of continuously running pods. A missing or
failed required activation path is not a ready serving endpoint.

## Change or refresh execution

Changes to role bindings, selectors, model restrictions, launch, resources,
parallelism, runtime-config name, topology, and execution-affecting storage
settings request new execution resolution. Route-only changes do not choose new
profiles. Metadata-only changes do not refresh execution, though metadata used
by routing templates can affect the route.

Changing a source recipe, profile, or runtime config alone does not update an
accepted service. To adopt new source content:

```bash
kubectl patch aimservice qwen-chat -n team-a --type=merge \
  -p '{"spec":{"composition":{"refreshToken":"catalog-refresh-2"}}}'
```

Inspect `desired`, `observedRefreshToken`, and the applied projection afterwards.
Token values are opaque. Keeping a token in place is idempotent; a different
non-empty token requests another evaluation.

If the desired tuple cannot resolve, AIM leaves the applied config and native
baseRef unchanged. Correct the request or restore the missing source, then submit
a new token when you want another explicit source refresh. Transient dependency
failures can be retried without inventing another user request.

### Mixed-generation safety

Old prefill and old decode may work together, and new prefill and new decode may
work together, while old prefill and new decode are known-incompatible.

If the installed native rollout cannot prevent that known-unsafe mixing, AIM
rejects the refresh with `MixedGenerationUnsafe`. Create a separate service and
perform an explicitly managed traffic transition. AIM does not promise an atomic
P/D swap, automatic rollback, or zero downtime.

Unverified transition interoperability is recorded rather than treated as a
certification failure. Once accepted, native rollout behavior applies; runtime
failures remain visible and do not imply automatic rollback.

## Scale without reselecting

```bash
kubectl patch aimservice qwen-chat -n team-a --type=merge \
  -p '{"spec":{"composition":{"decode":{"replicas":2}}}}'
```

A replicas/scaling-only update preserves the selected profiles, launch,
transport, and effective selector defaults. It creates a new projection revision
if config content changes, with the same `executionDigest`. It validates capacity
and placement using pinned content, not newly edited source profiles.

When switching an already-authored role from fixed replicas to scaling, remove
the old replicas field in the same Kubernetes patch. JSON merge-patch deletion
is normal API editing; it does not introduce null-as-deletion in launch overlays:

```bash
kubectl patch aimservice qwen-chat -n team-a --type=merge \
  -p '{"spec":{"composition":{"decode":{"replicas":null,"scaling":{"minReplicas":0,"maxReplicas":2}}}}}'
```

The zero-minimum example needs role-aware activation and an enabled route.
Use [the complete zero-minimum manifest](examples/12-zero-minimum.yaml) only on
an integration supporting that path.

When effective placement is `SameNode`, capacity includes every requested fixed
group and at least `max(1, minReplicas)` groups per autoscaled role. Maxima are
growth ceilings. A compatible but busy node produces a capacity wait; profiles
are not replaced and transport is not changed. An impossible capacity-only
request is rejected without altering the previous applied capacity. `AnyNode`
transport does not acquire this combined-node feasibility requirement.

## Source deletion, restart, and cleanup

An accepted revision persists effective profile content, generated runtime YAML,
model/artifact references, launch, resources, placement, transport, and bundle
provenance. It is not merely a list of source names and resource versions.

Consequently:

- Deleting a source recipe/profile does not tear down a serving revision.
  Replacement pods use retained namespace-local generated content.
- Source deletion is reported. A fresh resolution that requires that deleted
  object cannot succeed.
- Controller restart recovers from persisted content and revision-marked
  resources, including interruptions between publication steps.
- The previous snapshot, config, and dependencies remain while any old workload
  or replacement pod can reference them.
- A native service's new baseRef alone is not proof that old resources are safe
  to delete.

Deleting an AIMService removes service-owned artifacts according to their
ownership/lifetime rules. It does not delete shared recipes, source profiles,
user-owned Secrets/PVCs, administrator Gateways, or shared caches.

Pinning does not make remote artifact bytes, image tags, Secret data, PVC contents,
or external platform behavior immutable. Use immutable references where available
and inspect drift findings for the rest.

## Troubleshooting by symptom

| Symptom | Check / action |
| --- | --- |
| `SelectorConflict` | Find the named recipe/service selector paths. Changing a runtime default cannot fix conflicting authored restrictions. |
| `NoMatchingProfiles` | Inspect `effectiveSelector` and default contributions, model provenance/scope, deployable eligibility, and explicit tier settings. Supply a matching profile or intentionally change restrictions/default inheritance. |
| `ReferenceNotFound` | Check exact kind/name and scope. A namespace ref does not fall back to a cluster ref. |
| `MissingTransport` | Configure a P/D preset and registered integration, or correct the preset name. Omission is not automatic connector detection. |
| `UnsupportedShape` | Worker/parallelism, placement, scaling, or transport cannot be rendered by the installed integration. Use a supported shape; do not weaken the selected transport's minimum placement. |
| `NoFeasibleTuple` | Inspect the binding/rule and complete capacity footprint, not each profile's individual availability alone. |
| `WaitingForCapacity` | The node is compatible but busy. Reduce groups or free capacity. Scheduling is not a reservation. |
| `ManagedObjectCollision` | The generated name is occupied by an object AIM does not own. Resolve ownership deliberately; AIM does not adopt or overwrite it. |
| `DependencyUnavailable` | Check namespace-local profile YAML, artifact/cache volumes, credentials, and all worker mounts. Cluster publication grants no cross-namespace mounts. |
| `AwaitingBackends` | Inspect `InferencePool`, endpoint-picker, and route backend resolution. Confirm the Gateway supports the installed inference-extension API and avoid a circular dependency that prevents native-service creation. |
| `TransportFailure` | Inspect engine/connector logs, request-time protocol diagnostics, transfer metrics, lease expiry, fallback, and decode recomputation for the applied revision; pod readiness alone does not establish transfer. |
| `SourceDeleted` with healthy serving | Restore the source before refresh, or deliberately choose another source. Do not delete retained revision dependencies. |
| `MixedGenerationUnsafe` | Use a separate service and explicit traffic transition, or a native mapping that proves isolation of incompatible generations. |

When reporting a failure, include desired intent digest, applied revision, affected
position, bundle digest, relevant conditions, and a small diagnostic log excerpt.
Do not include credentials or Secret contents.

## Validate the actual serving path

Treat these as separate observations:

1. AIM can construct the configuration.
2. Native workloads become healthy.
3. An authorized inference request succeeds, including streaming/cancellation.
4. Connector/runtime telemetry shows KV transfer on the selected path,
   without prefill recomputation or an unintended fallback.

One successful response does not establish step four. Static validation does not
provide benchmark results or hardware qualification. Evidence is
integration-specific and can include request-time `UCX_PROTO_INFO`, vLLM
`nixl_*` transfer metrics, transfer-byte counters, and KV lease-expiry signals;
startup capability tables alone are not proof of the request-time path.
