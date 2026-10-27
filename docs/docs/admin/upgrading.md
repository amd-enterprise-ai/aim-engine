# Upgrading

This guide covers upgrading AIM Engine to a new version and operating in the
v1alpha1 → v1alpha2 bridge window. The [Bridge window](#migration-window) section
is the canonical operator reference for installation order, served and storage
versions, AIMService dispatch, retained model outputs, and manual recovery.

## Upgrade Procedure

### 1. Inventory derived runtime image lineage

Current releases use the exact `AIM_BASE_IMAGE_REF` recorded by an image
producer. They no longer truncate the tag to `MAJOR.MINOR` or move the
reference implicitly to the source model image's registry. Before upgrading,
inventory deployable profiles that can seed weight-replacing derivations but
do not yet expose runtime lineage:

```bash
kubectl get aimprofile -A -o json | jq -r '
  .items[]
  | select((.spec.aimId // "") != "")
  | select(((.spec.modelSources // []) | length) > 0)
  | select((.spec.baseImageRef // "") == "")
  | select((.metadata.annotations["aim.eai.amd.com/base-image"] // "") == "")
  | select((.status.baseImage // "") == "")
  | [.metadata.namespace, .metadata.name, .spec.image] | @tsv'

kubectl get aimclusterprofile -o json | jq -r '
  .items[]
  | select((.spec.aimId // "") != "")
  | select(((.spec.modelSources // []) | length) > 0)
  | select((.spec.baseImageRef // "") == "")
  | select((.metadata.annotations["aim.eai.amd.com/base-image"] // "") == "")
  | select((.status.baseImage // "") == "")
  | [.metadata.name, .spec.image] | @tsv'
```

These profiles continue to serve normally. They need `spec.baseImageRef` only
if a ProfileSet or `AIMService.spec.profileOverrides.modelSources` will replace
their weights.

Also inventory models whose cached metadata predates base-image extraction:

```bash
kubectl get aimmodel -A -o json | jq -r '
  .items[]
  | select(.status.imageMetadata != null)
  | select((.status.imageMetadata.baseImageRef // "") == "")
  | [.metadata.namespace, .metadata.name, .spec.image] | @tsv'

kubectl get aimclustermodel -o json | jq -r '
  .items[]
  | select(.status.imageMetadata != null)
  | select((.status.imageMetadata.baseImageRef // "") == "")
  | [.metadata.name, .spec.image] | @tsv'
```

The upgraded controller re-inspects these cached images. If reinspection fails,
the cached metadata and existing templates remain usable: normal model serving
does not become unready, and the non-gating `RuntimeImageLineage=False` condition
reports `BaseImageRefInspectionFailed`. Fix registry connectivity or the
model's `imagePullSecrets`; a later reconcile can retry the migration.
If changing only Secret data does not enqueue the model in your installation,
update any harmless annotation on the affected `AIMModel` or
`AIMClusterModel` after fixing access to trigger that reconcile.

When registry access cannot be restored, copy the complete object from
`status.imageMetadata` into `spec.imageMetadata` and add the producer's exact
`baseImageRef`:

```yaml
spec:
  imageMetadata:
    # Preserve model, oci, originalLabels, and any other existing metadata.
    # This spec object becomes authoritative and disables remote extraction.
    baseImageRef: ghcr.io/silogen/aim-base:0.11
```

Do not set only `baseImageRef` when the status object contains other metadata:
`spec.imageMetadata` replaces extracted metadata as a whole rather than merging
field by field. Obtain the exact reference from the image producer; do not
derive it by truncating the model image's tag or substituting its registry.

If inspection succeeds but the image genuinely lacks `AIM_BASE_IMAGE_REF`, the
condition reports `BaseImageRefMissing` and the controller records the current
metadata contract so it does not repeatedly inspect that image. In either case,
only new fine-tuned copies that replace weights and require the unresolved
runtime lineage are deferred.

### 2. Update CRDs first

CRDs must be updated before the operator, as new operator versions may depend on new CRD fields:

```bash
helm upgrade aim-engine-crds oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version <new-version> \
  --namespace aim-system
```

Wait for the CRDs to become established:

```bash
kubectl wait --for=condition=Established \
  crd/aimprofiles.aim.eai.amd.com \
  crd/aimclusterprofiles.aim.eai.amd.com \
  crd/aimruntimeconfigs.aim.eai.amd.com \
  crd/aimclusterruntimeconfigs.aim.eai.amd.com
```

### 3. Configure lineage and registry mappings

After updating the CRDs but before upgrading the operator:

1. Set `spec.baseImageRef` on affected hand-authored deployable profiles.
2. Review mappings for the producer references recorded by released AIM images.
   The operator applies the public compatibility mappings shown below as
   fallbacks regardless of the selected RuntimeConfig. Fresh chart installations
   also expose them on the default cluster RuntimeConfig.
3. Private and air-gapped installations must configure their mirror explicitly.
   Repeat that policy on named RuntimeConfigs selected by services or legacy
   fine-tuned models; a matching configured mapping wins over the public
   fallback, while other mappings on `default` are not inherited by name.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  imageRegistry:
    mappings:
      - from: docker.io/silogenai
        to: docker.io/amdenterpriseai
      - from: ghcr.io/silogen
        to: docker.io/amdenterpriseai
```

Matching is literal, so use the `from` prefix exactly as recorded in
`AIM_BASE_IMAGE_REF`. Confirm that the exact resulting tag or digest exists in
the destination registry. Private and air-gapped installations should map both
producer prefixes directly to their internal mirror and add a
`docker.io/amdenterpriseai` mapping for newer public references. Mapping is
single-pass, so `silogenai -> amdenterpriseai -> internal` does not chain. AIM
Engine does not fall back to a rolling tag.

Existing derived profiles are updated in place when the upgraded controller
reconciles exact producer lineage. For example, a previously synthesized
`aim-base:0.13` can become `aim-base:0.13.1`, and a date tag such as
`aim-base:2026.11` can become `aim-base:2026.11.0`. Running fine-tuned services
that consume those profiles can therefore roll and pull a new image of about
9 GB; plan registry bandwidth and rollout capacity accordingly.

### 4. Upgrade the operator

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <new-version> \
  --namespace aim-system \
  --reuse-values
```

Or with updated values:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/aim-engine-chart \
  --version <new-version> \
  --namespace aim-system \
  --values my-values.yaml
```

### Scale-from-zero Helm value migration

The gateway provider now selects both the controller metric contract and the
matching collector. Replace legacy values before upgrading:

- The new default is `scaleFromZero.gatewayProvider=none`. Set a provider
  explicitly before upgrading any installation with `minReplicas: 0`
  AIMServices.
- `scaleFromZero.gatewayActivationScope=httproute` becomes
  `scaleFromZero.gatewayProvider=envoyGateway`.
  Selecting this provider manages the collector only; install the
  Gateway-targeted `EnvoyExtensionPolicy` and configure the `EnvoyProxy`
  source-side delta OTLP sink independently for every Gateway used by
  AIMServices. Follow the
  [optional Envoy scale-from-zero guide](envoy-gateway-scale-from-zero.md).
- `scaleFromZero.gatewayActivationScope=deployment` becomes
  `scaleFromZero.gatewayProvider=kgateway`.
- `scaleFromZero.gatewayMetricsCollector.enable=true|false` becomes
  `scaleFromZero.gatewayMetricsCollector.management=helm|external`.
- `scaleFromZero.gatewayMetricsCollector.gatewayName` moves to
  `scaleFromZero.gatewayMetricsCollector.kgateway.gatewayName`.

Changing an existing installation from the kgateway provider to Envoy Gateway
also changes routing and metric identity. Follow
[Migrate from kgateway to Envoy Gateway](migrating-kgateway-to-envoy-gateway.md)
rather than treating it as only a Helm value rename.

The chart rejects legacy keys rather than silently selecting the wrong
collector. When a release has stored those keys, upgrade with a migrated values
file and `--reset-values` instead of `--reuse-values`.

Existing Envoy Gateway installations must coordinate the collector and
platform-owned `EnvoyProxy` change. To avoid losing a cold activation request
during the handoff:

1. Temporarily set affected AIMServices to `minReplicas >= 1`.
2. Upgrade or replace the collector so its OTLP receiver Service is Ready.
3. Add the `reportCountersAsDeltas: true` OTLP sink to each relevant
   `EnvoyProxy`.
4. Verify that normalized activation metrics reach keda-otel-add-on.
5. Restore the intended `minReplicas: 0` settings.

Helm removes its obsolete pod-discovery ClusterRole and binding automatically.
For the standalone collector, remove the legacy resources after applying the
new manifest:

```bash
kubectl delete clusterrole envoy-gateway-metrics-collector \
  --ignore-not-found
kubectl delete clusterrolebinding envoy-gateway-metrics-collector \
  --ignore-not-found
```

### 5. Verify

```bash
kubectl get pods -n aim-system
kubectl get aimservice --all-namespaces
kubectl get aimprofileset,aimclusterprofileset -A
```

Investigate `BaseImageUnresolved` before changing or deleting existing
profiles. The controllers retain existing managed profiles and inference
resources while runtime-image lineage is unresolved.

## Rollback

Roll back to the previous Helm release:

```bash
helm rollback aim-engine -n aim-system
```

:::{note}
Rolling back the operator does not roll back CRD changes. New CRD fields are additive and backward compatible. If a CRD change is not backward compatible, this will be noted in the release notes.
:::
## Version Compatibility

AIM Engine follows semantic versioning. Within a major version:

- CRD changes are additive (new optional fields)
- Existing resources continue to work without modification
- API group is `aim.eai.amd.com`. During the bridge release, only
  `AIMService`, `AIMModel`, and `AIMClusterModel` serve both versions (see
  [Migration window](#migration-window)).

The minimum supported Kubernetes version is **1.32** (see [Prerequisites](../getting-started/installation.md#prerequisites)).

### Profile YAML contract backfill

Current operators record the source runtime's profile-YAML codec, modeled-field presence, and opaque runtime extensions on discovered and derived profiles with the `aim.eai.amd.com/profile-yaml-contract` annotation. Profiles created by an older operator may not have this annotation. Generated and independently hand-authored profiles use the current strict `accelerator_*` v1 format. Discovered and derived profiles do not guess: runtime projection retains any existing runtime and reports the missing contract until the owning producer backfills the annotation.

After upgrading an installation that still serves legacy runtimes requiring `gpu` / `gpu_count`, allow the owning `AIMModel`, `AIMClusterModel`, `AIMProfileSet`, or `AIMClusterProfileSet` to reconcile. Existing projected runtimes remain untouched while the contract is unknown, avoiding a transient rewrite to the strict schema. Reconcile upstream image-backed models before real-profile-backed profile sets so the sets inherit the recovered source contract. If an object is not backfilled through normal reconciliation, update the owning resource in a way that its controller watches, or restart the operator to enqueue its resources again. You can audit profiles that still lack the contract with:

```bash
kubectl get aimprofile -A -o json | \
  jq -r '.items[] | select(.metadata.annotations["aim.eai.amd.com/profile-yaml-contract"] == null) | [.metadata.namespace, .metadata.name] | @tsv'

kubectl get aimclusterprofile -o json | \
  jq -r '.items[] | select(.metadata.annotations["aim.eai.amd.com/profile-yaml-contract"] == null) | .metadata.name'
```

Generated and independently hand-authored profiles have no source YAML to inspect and continue to use the strict canonical format by design.

After the contract is backfilled, AIM Engine reconciles the projected ServingRuntime and ConfigMap automatically. Existing predictor pods are not rolled by a ConfigMap data update and must be restarted to read the corrected startup profile. Newly created pods, including scale-from-zero pods, use the corrected projection. For an AIMService, restart its predictor pods with:

```bash
kubectl delete pod -n <namespace> \
  -l aim.eai.amd.com/service=<service-name>
```

## Migration window

The bridge keeps supported predecessor objects reconciling while native
v1alpha2 resources use the production controllers. It is compatibility
software, not a migration executor or proof that a workload serves inference.

### Install CRDs before the operator

Use the CRD and operator charts from the same bridge release:

1. Upgrade `aim-engine-crds`.
2. Wait for the AIM CRDs to report `Established`.
3. Upgrade `aim-engine`.
4. Check operator events and the affected AIM resources before changing any
   desired state.

Do not install the operator before its CRDs, remove v1alpha1 endpoints, or
rewrite stored objects as part of bridge installation. The operator has no
permission to mutate CRD definitions or `status.storedVersions`.

### Served and storage behavior

Exactly three CRDs overlap:

- `AIMService`, `AIMModel`, and `AIMClusterModel` serve both `v1alpha1` and
  `v1alpha2`; only `v1alpha2` is storage.
- `AIMArtifact`, `AIMClusterModelSource`, `AIMRuntimeConfig`,
  `AIMClusterRuntimeConfig`, `AIMServiceTemplate`,
  `AIMClusterServiceTemplate`, and `AIMTemplateCache` remain served and stored
  only as `v1alpha1`.
- `AIMProfile`, `AIMClusterProfile`, `AIMProfileSet`,
  `AIMClusterProfileSet`, and `AIMProfileCache` remain v1alpha2-only.

The overlapping CRDs use conversion strategy `None`: there is no conversion
webhook or semantic field translation. The API server persists writes through
the storage version and serves the shared wire fields through the requested
served endpoint. Applying a v1alpha1 manifest does not create a second object.

### AIMService pipeline selection

One v1alpha2-rooted controller dispatches each `AIMService`:

- `spec.profile` selects the profile pipeline.
- `spec.template`, `spec.model.name`, `spec.model.image`, and
  `spec.model.custom` select the template pipeline by default.
- `aim.eai.amd.com/reconciler-pipeline: profile` explicitly selects the
  profile pipeline. Use it only with a profile-supported shape such as
  `spec.profile`, `spec.model.name`, or `spec.model.image`.
- `aim.eai.amd.com/reconciler-pipeline: template` explicitly selects the
  template pipeline only when `spec.model` is present.

An empty or unknown marker, or `template` without `spec.model`, is malformed.
The controller does not fail or repair the object solely for that marker. It
falls back to the rules above and emits an `InvalidReconcilerPipeline` Warning
event describing the ignored value. `status.reconcilerPipeline` records the
active path.

The supported grandfathered service shapes are template-backed services using
model name, image, or inline custom input, including existing template
selection and legacy overrides. Retained legacy model inputs include image
metadata/recommended deployments, model sources, custom templates, custom
hardware, and fine-tuned template matching. Existing legacy-only `AIMModel`
fields may continue through the shared storage object; v1alpha2 AIMModel
admission prevents new objects from introducing those fields. Custom or
ambiguous shapes are not automatically converted to profiles.

### AIMModel outputs and readiness

The v1alpha2 `AIMModel` and `AIMClusterModel` controllers are the only
production roots for both served versions. They retain two bounded output
paths:

- legacy inputs can continue producing `AIMServiceTemplate` or
  `AIMClusterServiceTemplate`;
- native image discovery, model-ID generation, or profile derivation can
  produce `AIMProfile`, `AIMClusterProfile`, or a profile set; and
- a grandfathered image-backed object can retain both template and profile
  outputs when both paths apply.

Output children keep their normal scope and controller owner. Cross-version
owner references converge to the v1alpha2 owner identity without changing an
existing `controller` or `blockOwnerDeletion` value.

Readiness remains path-aware: when one applicable serving path is healthy, a
failure reported only by the inactive compatibility path is non-blocking. A
failure in the active path remains visible and blocking. This bridge rule does
not qualify model serving or replace cluster observation.

### Inline auto-custom compatibility

A supported v1alpha1 `AIMService.spec.model.custom` can create or reuse a
v1alpha2-valid compatibility `AIMModel`, but reuse is limited to reconciliation
of the same live source `AIMService` UID. Distinct `AIMService` objects with
identical inline custom inputs intentionally receive distinct compatibility
models; cross-service sharing is unsupported because each model has one
UID-bound controller owner. The persisted model stores the exact predecessor
custom input in the internal
`aim.eai.amd.com/legacy-auto-custom-spec` annotation and anchors it to the live
source service with labels and a UID-bound controller owner reference.

Before generating a legacy template, the model controller strictly decodes the
annotation, verifies the stored v1alpha2 fields and source provenance, and
hydrates predecessor-only fields on an in-memory copy. It does not write those
legacy fields back to the stored model.

Malformed, contradictory, ownerless, or orphaned compatibility state produces
`LegacyAutoCustomSpecReady=False` with reason
`InvalidLegacyAutoCustomSpec`, emits a Warning event, and plans no template or
profile child mutation. The ordinary controllers do not adopt or reconstruct
such a model from labels.

For manual recovery:

1. Export the `AIMService`, compatibility `AIMModel`, conditions, and events
   before changing anything.
2. Compare the model annotation, labels, owner UID, and v1alpha2-safe fields
   with the authoritative source service. Do not synthesize the opaque
   annotation or owner reference from labels alone.
3. If the original service is still live and its exact inline custom input is
   authoritative, an administrator may remove the invalid auto-generated
   compatibility model and let that service reconcile a replacement. This can
   recreate children or cached data; it is not an artifact-preserving repair.
4. If the source service is gone, the UID differs, or intent is ambiguous,
   leave automatic repair disabled. Remove an unneeded orphan only after
   confirming no workload depends on it; otherwise record it for manual
   migration handling.

### Migration boundary

The bridge performs no storage rewrite, Plan/Apply operation, legacy cleanup,
artifact handoff, rollback, API-removal decision, GPU qualification, or
inference request. Migration execution is governed by
[ADR-0002](https://github.com/amd-enterprise-ai/aim-engine/blob/main/ADR-0002-bounded-plan-apply-template-migration.md)
and begins only after the bridge is installed and legacy serving has been
checked by an administrator. The controlled, non-production operating steps
and unresolved safety limitations are documented in
[Bounded v1alpha1 template migration](template-migration.md).

The local bridge contract is a static contract over API markers, generated
CRDs, controller roots, ownership semantics, RBAC, and release ordering. A
passing contract does not prove migration, artifact preservation, GPU serving,
or inference.

## Next Steps

- [Installation Reference](installation.md) — Full installation options
- [Changelog](../changelog.md) — Release notes and changes
