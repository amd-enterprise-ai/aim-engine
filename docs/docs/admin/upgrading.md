# Upgrading

This guide covers upgrading AIM Engine to a new version. If the cluster still
has template objects or AIMServices on the template pipeline, follow
[Migration window](#migration-window) on v0.2.7 or a later 0.2.x release
before you upgrade. Upgrade older releases to the latest 0.2.x release first.

## Upgrade Procedure

### 1. Inventory derived runtime image lineage

Current releases deploy the `AIM_BASE_IMAGE_REF` recorded by an image producer
with its release tag kept exactly, instead of truncating it to `MAJOR.MINOR`;
only pre-release tags still resolve to their `MAJOR.MINOR` rolling tag. The
registry and org come from `baseImageRegistry`, or from the source model image
when that is unset. Images that don't record `AIM_BASE_IMAGE_REF` (official
images before `0.9.0`) can no longer seed weight-replacing derivations without
an explicit `overrides.image`. Before upgrading, inventory deployable profiles
that can seed weight-replacing derivations but do not yet expose runtime
lineage:

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
the cached metadata remains usable: normal model serving does not become
unready, and the non-gating `RuntimeImageLineage=False` condition
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
    baseImageRef: docker.io/amdenterpriseai/aim-base:0.11
```

Do not set only `baseImageRef` when the status object contains other metadata:
`spec.imageMetadata` replaces extracted metadata as a whole rather than merging
field by field. Obtain the exact reference from the image producer; do not
derive it by truncating the model image's tag or substituting its registry.

If inspection succeeds but the image genuinely lacks `AIM_BASE_IMAGE_REF`, the
condition reports `BaseImageRefMissing` and the controller records the current
metadata contract so it does not repeatedly inspect that image. In either case,
only new weight-replacing profile copies that need the unresolved runtime
lineage are deferred.

### 2. Update CRDs first

CRDs must be updated before the operator, as new operator versions may depend on new CRD fields:

```bash
helm upgrade aim-engine-crds oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version <new-version> \
  --namespace aim-system
```

This deletes the template CRDs. See
[Remove the template CRDs](#remove-the-template-crds) first.

Upgrade the operator right after the CRDs, with only step 3 in between. The
running v0.2.7 pod survives the CRDs upgrade, but a restarted v0.2.7 pod can't
run on the new CRDs: it exits about every two minutes on
`Could not wait for Cache to sync`, and reports Ready in between.

Wait for the CRDs to become established:

```bash
kubectl wait --for=condition=Established \
  crd/aimprofiles.aim.eai.amd.com \
  crd/aimclusterprofiles.aim.eai.amd.com \
  crd/aimruntimeconfigs.aim.eai.amd.com \
  crd/aimclusterruntimeconfigs.aim.eai.amd.com
```

### 3. Configure lineage and the aim-base registry

After updating the CRDs but before upgrading the operator:

1. Set `spec.baseImageRef` on affected hand-authored deployable profiles.
2. Derived images keep taking aim-base's registry and org from the model image,
   so public installations and mirrors that host both the model images and
   aim-base need no RuntimeConfig change.
3. If aim-base lives in a different registry from the model images, set
   `baseImageRegistry` on `default` and on each named RuntimeConfig selected by
   services; named configurations do not inherit it from `default`.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMClusterRuntimeConfig
metadata:
  name: default
spec:
  baseImageRegistry: registry.example.com/amdenterpriseai
```

Derived images now use the exact release tag that each model image records,
so confirm that tag exists in your registry. Pre-release tags, such as
`2026.11.0-rc11`, still resolve to their `MAJOR.MINOR` rolling tag.

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

Before you roll back, while the current operator still runs:

1. Annotate every AIMService that has neither `spec.profile` nor the
   `aim.eai.amd.com/reconciler-pipeline: profile` annotation, including
   services created after the upgrade with only `spec.model.name`. v0.2.7
   moves such a service back to the template pipeline. Without a matching
   template it reports `Pending` or `Failed`; with one, including a template
   that v0.2.7 creates again for an image model, v0.2.7 can rewrite its
   InferenceService and roll its pods.

   ```bash
   kubectl get aimservices --all-namespaces -o json \
     | jq -r '.items[] | select(.spec.profile == null
         and .metadata.annotations["aim.eai.amd.com/reconciler-pipeline"] != "profile")
         | "\(.metadata.namespace) \(.metadata.name)"' \
     | while read -r namespace name; do
         kubectl annotate aimservice "${name}" -n "${namespace}" --overwrite \
           aim.eai.amd.com/reconciler-pipeline=profile
       done
   ```

2. Set `discovery.createServiceTemplates: false` on every AIMModel and
   AIMClusterModel with `spec.image`, including models created after the
   upgrade and models you removed the field from. It defaults to `true`, so
   v0.2.7 otherwise creates AIMServiceTemplates for them again:

   ```bash
   kubectl get aimmodels --all-namespaces -o json \
     | jq -r '.items[] | select(.spec.image and .metadata.annotations["aim.eai.amd.com/legacy-auto-custom-spec"] == null)
         | "\(.metadata.namespace) \(.metadata.name)"' \
     | while read -r namespace name; do
         kubectl patch aimmodel "${name}" -n "${namespace}" --type merge \
           -p '{"spec":{"discovery":{"createServiceTemplates":false}}}'
       done
   kubectl get aimclustermodels -o json \
     | jq -r '.items[] | select(.spec.image) | .metadata.name' \
     | while read -r name; do
         kubectl patch aimclustermodel "${name}" --type merge \
           -p '{"spec":{"discovery":{"createServiceTemplates":false}}}'
       done
   ```

3. Delete each AIMModel with the `aim.eai.amd.com/legacy-auto-custom-spec`
   annotation whose AIMService no longer has `spec.model`. v0.2.7 panics, and
   recovers, on every reconcile of such a model, and never reconciles it:

   ```bash
   kubectl get aimservices,aimmodels --all-namespaces -o json \
     | jq -r '[.items[] | select(.kind == "AIMService" and .spec.model != null)
         | "\(.metadata.namespace)/\(.metadata.name)"] as $with_model
       | .items[] | select(.kind == "AIMModel"
           and .metadata.annotations["aim.eai.amd.com/legacy-auto-custom-spec"] != null
           and ("\(.metadata.namespace)/\(.metadata.labels["aim.eai.amd.com/service"])" | IN($with_model[]) | not))
       | "\(.metadata.namespace) \(.metadata.name)"' \
     | while read -r namespace name; do
         kubectl delete aimmodel "${name}" -n "${namespace}"
       done
   ```

Then roll back both Helm releases, the CRDs first:

```bash
helm rollback aim-engine-crds -n aim-system
kubectl wait --for=condition=Established --timeout=60s \
  crd/aimservicetemplates.aim.eai.amd.com \
  crd/aimclusterservicetemplates.aim.eai.amd.com \
  crd/aimtemplatecaches.aim.eai.amd.com
helm rollback aim-engine -n aim-system --wait --timeout 5m
```

Without a revision, `helm rollback` returns to the previous revision;
`helm history` lists the others. If you install the CRDs with kubectl, replace
the first command with `kubectl apply --server-side -f crds.yaml`, using the
`crds.yaml` of the release you roll back to.

If you [kept the template CRDs](#remove-the-template-crds) and haven't removed
them yet, the current CRDs release no longer includes them, so
`helm rollback aim-engine-crds` fails with
`no CustomResourceDefinition with the name ... found`. Upgrade the CRDs release
to the chart version you roll back to instead. Helm adopts the kept CRDs,
because they still carry the release's labels and annotations. This also works
after a failed rollback. Then roll back the operator as above:

```bash
helm upgrade aim-engine-crds oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version <previous-version> \
  --namespace aim-system
helm rollback aim-engine -n aim-system --wait --timeout 5m
```

This release removes the template CRDs and stops serving v1alpha1 for
AIMService, AIMModel and AIMClusterModel. v0.2.7 needs both, so rolling back
only the operator fails: the v0.2.7 pod exits on
`no matches for kind "AIMTemplateCache"`, and the rolling update keeps the
current operator running. Without `--wait`, `helm rollback` still reports
success. If you already rolled back only the operator, roll back the CRDs, then
delete the crash-looping operator pod.

Between the two rollbacks, the current operator runs on the v0.2.7 CRDs. It
logs failed applies, such as `.spec.baseImageRef: field not declared in schema`,
and deletes nothing.

v0.2.7 doesn't restore everything; see [Rollback limits](#rollback-limits).

## Artifact, runtime config and model source versions

This release serves `AIMArtifact`, `AIMRuntimeConfig`, `AIMClusterRuntimeConfig`
and `AIMClusterModelSource` at `aim.eai.amd.com/v1alpha2`. `v1alpha1` still
applies, and the API server returns a deprecation warning that names
`aim.eai.amd.com/v1alpha2`. The next release stops serving `v1alpha1` for these
kinds. Switch GitOps manifests, Helm charts and scripts to `v1alpha2` now.

Upgrade the CRDs before the operator, as in
[Update CRDs first](#2-update-crds-first). The chart manifest contains a
v1alpha2 `AIMClusterRuntimeConfig`, so that object applies only after the new
CRDs are established.

## Version Compatibility

AIM Engine follows semantic versioning. Within a major version:

- CRD changes are additive (new optional fields). This release is an
  exception: it removes the template CRDs and stops serving `v1alpha1` for
  `AIMService`, `AIMModel` and `AIMClusterModel`
- Existing resources continue to work without modification, except the ones
  the [Migration window](#migration-window) replaces
- API group is `aim.eai.amd.com`. Every AIM kind serves `v1alpha2`.
  `AIMArtifact`, `AIMRuntimeConfig`, `AIMClusterRuntimeConfig` and
  `AIMClusterModelSource` also still serve deprecated `v1alpha1` (see
  [Artifact, runtime config and model source versions](#artifact-runtime-config-and-model-source-versions)).

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

### Download filters declared by AIM images

AIM images can declare the files their model needs with `download_filter` in their profile YAML, and discovered profiles carry it onto their model source as `downloadFilter`. Upgrading the operator does not change profiles discovered from images that make no declaration.

A profile's model source changes when the operator first reads a declaration for it. Usually that is the discovery of an image or image version that declares one. If the cluster discovered such an image under an earlier release, the declaration is applied by the first reconcile of its `AIMModel` or `AIMClusterModel` after the upgrade, because profiles are rebuilt from the stored discovery results.

When a model source gains a filter, the filter becomes part of its artifact's identity, so the profile cache creates a new `AIMArtifact` under a new name and downloads the model again. An existing artifact with `downloadFilter: {}` in its own spec holds the whole repository and keeps serving the source, so no second download happens.

Artifacts downloaded through a runtime config filter are not reused this way, even when that filter was `downloadFilter: {}`, the documented workaround for repositories with required subdirectories. Such an artifact has no filter in its spec: the runtime config filter is applied at download time and not recorded, and it may have changed since. The cache cannot tell what the artifact holds, so a source that gains a declaration downloads again, even if the artifact already holds every file. For large models plan for the transfer time and for storage for a second copy, and reclaim the old artifact afterwards as described below.

Otherwise the cache keeps serving the artifact it already lists in `status.artifacts` while the new one downloads, as long as that artifact is still `Ready`. The cache and its services stay `Ready`, and the cache's `ArtifactsReplacing` condition names both artifacts and reports the replacement's status, or its error if it fails or stalls. The namespace needs storage for both copies until the old one is deleted. The served artifact is never evicted to make room: under an artifact storage quota without enough space, the replacement is blocked with `StorageQuotaExceeded` until you free space or raise the quota. When the new artifact is `Ready`, the cache switches `status.artifacts` to it, sets `ArtifactsReplacing` to `False` with reason `ArtifactsReplaced`, and emits an event naming the old and new artifacts. Services then roll over to the new artifact's volume. See [Replacing an artifact](../concepts/caching.md#replacing-an-artifact).

The old artifact and its PVC stay behind after the switch. A shared artifact has no owner, and a dedicated artifact is owned by its cache and deleted only with it. Profile cache cleanup removes only artifacts that are not `Ready`, and only while the cache itself is being deleted. Deleting the old artifact before the switch takes the cache out of service until the new download finishes. To reclaim the storage:

1. Wait until the switch has happened: the cache's `status.artifacts` names the new artifact and no longer lists the old one. `status.artifacts` is the check to rely on. `ArtifactsReplacing` turns `False` with reason `ArtifactsReplaced` after a switch, but stays `True` while another model source of the same profile is still being replaced:

   ```bash
   kubectl get aimprofilecache -n <namespace> <profile-cache-name> \
     -o jsonpath='{.status.artifacts}' | jq
   kubectl get aimprofilecache -n <namespace> <profile-cache-name> \
     -o jsonpath='{.status.conditions[?(@.type=="ArtifactsReplacing")]}' | jq
   ```

2. Confirm that no other cache still lists the old artifact. Shared artifacts serve every cache with a matching source, and a profile cache that has not switched yet still serves the old one. This should print nothing:

   ```bash
   kubectl get aimprofilecache -n <namespace> -o json | \
     jq -r '.items[] | .metadata.name as $cache
       | (.status.artifacts // {})[] | select(.name == "<old-artifact-name>") | $cache'
   ```

3. Delete the old artifact:

   ```bash
   kubectl delete aimartifact -n <namespace> <old-artifact-name>
   ```

## Migration window

This release removes the template serving path: the template pipeline for
`AIMService`, the `AIMServiceTemplate`, `AIMClusterServiceTemplate` and
`AIMTemplateCache` kinds, and template output from `AIMModel` and
`AIMClusterModel`. It also stops serving `aim.eai.amd.com/v1alpha1` for
`AIMService`, `AIMModel` and `AIMClusterModel`. Prepare the cluster on v0.2.7
or a later 0.2.x release, while the template path still works, and upgrade
only when the upgrade preflight passes:

1. Check that the cluster runs the [minimum version](#minimum-version).
2. [Run the upgrade preflight](#run-the-upgrade-preflight) to see what is left
   to do.
3. [Move template services](#move-template-services) to the profile pipeline.
4. Replace any [unservable services](#replace-unservable-services) and
   [models without a native path](#replace-models-without-a-native-path) the
   preflight lists.
5. [Delete template objects](#delete-template-objects).
6. [Remove stale v1alpha1 owner references](#remove-stale-v1alpha1-owner-references).
7. [Update v1alpha1 manifests](#update-v1alpha1-manifests) in GitOps
   repositories and scripts.
8. Rerun the preflight until it passes. Then upgrade the CRDs before the
   operator, as in [Upgrade Procedure](#upgrade-procedure).
9. [Remove the template CRDs](#remove-the-template-crds) if you install with
   kubectl, or kept them with Helm.

### Minimum version

Upgrade from v0.2.7 or later. If the cluster runs an older release, upgrade to
the latest 0.2.x release first and let it reconcile your resources. v0.2.7 is
the first release that records `status.reconcilerPipeline` on every AIMService
and rewrites v1alpha1 owner references to v1alpha2. The steps below rely on
both. Run them on that release: its operator serves both pipelines and moves
a service to the profile pipeline in place.

The preflight reads the operator version from the image tag of the `manager`
container in the `aim-engine-controller-manager` Deployment, in any namespace.
It also checks that every AIMService has `status.reconcilerPipeline`. If the
image reference has no version tag, for example when it is pinned by digest
only, or the Deployment has a different name, the preflight can't read the
version. It then prints a `MANUAL [minimum-version]` line instead of failing:
confirm by hand that the operator runs v0.2.7 or later. The
`status.reconcilerPipeline` check still blocks in that case.

### Run the upgrade preflight

The preflight script ships with each release. Download it from the release you
upgrade to, and run it with the kubectl context of the cluster:

```bash
curl -fsSLO https://github.com/amd-enterprise-ai/aim-engine/releases/download/<release-tag>/upgrade-preflight.sh
bash upgrade-preflight.sh
```

It needs `kubectl`, with permission to get and list the resources below,
Deployments and pods in all namespaces, and to get the AIMService,
AIMServiceTemplate, AIMClusterServiceTemplate and AIMTemplateCache
CustomResourceDefinitions, and `jq` 1.6 or later. It only reads from the
cluster. It exits with 0 when every blocking check passes, 1 when any fails,
and 2 when a required tool is missing. If the preflight can't list a resource,
each check that reads it fails with the resource and kubectl's error: fix the
permission and rerun. Each `FAIL` line names the check, the object and the
section of this guide that fixes it:

| Check | Fails when | Section |
|---|---|---|
| `minimum-version` | The operator is older than v0.2.7, or an AIMService has no `status.reconcilerPipeline` | [Minimum version](#minimum-version) |
| `template-pipeline-services` | An AIMService is not on the profile pipeline, or hasn't finished moving: its InferenceService lacks the `aim.eai.amd.com/profile` label, or one of its predictor pods without that label hasn't finished. A service whose `aim.eai.amd.com/migration-preserve-inference-service-uid` and `aim.eai.amd.com/migration-preserve-inference-service-generation` annotations match its InferenceService's UID and its own generation keeps that InferenceService and its pods unchanged, so neither counts. A service that is only unhealthy, for example with no InferenceService or with Pending or crash-looping pods, passes. Also fails for each pod that still mounts the PVC of an AIMArtifact owned by an AIMTemplateCache | [Move template services](#move-template-services) |
| `unservable-services` | An AIMService uses `spec.model.custom`, or `spec.template` without `spec.model` or `spec.profile` | [Replace unservable services](#replace-unservable-services) |
| `models-without-native-path` | An AIMModel or AIMClusterModel uses `spec.custom`, `spec.customTemplates` or `spec.modelSources`; is an image model with `spec.aimId` or `spec.discovery.extractMetadata: false`; or was generated for an inline custom service | [Replace models without a native path](#replace-models-without-a-native-path) |
| `template-objects` | An AIMServiceTemplate, AIMClusterServiceTemplate or AIMTemplateCache exists | [Delete template objects](#delete-template-objects) |
| `template-finalizers` | An AIMService or AIMTemplateCache has a template cleanup finalizer | [Delete template objects](#delete-template-objects) |
| `stale-owner-references` | An InferenceService, HTTPRoute, ScaledObject, PVC, Job, ConfigMap, AIMArtifact, AIMProfile or AIMProfileCache is owned by a v1alpha1 AIMService, AIMModel, AIMClusterModel or template object. Each line ends with the `kubectl patch` command that fixes the reference | [Remove stale v1alpha1 owner references](#remove-stale-v1alpha1-owner-references) |

`INFO` lines list Dedicated template caches with their sizes (see
[Cache outcomes](#cache-outcomes)). `MANUAL` lines name what the script can't
check and you confirm by hand: the operator version when it can't be read (see
[Minimum version](#minimum-version)), and v1alpha1 manifests outside the
cluster (see [Update v1alpha1 manifests](#update-v1alpha1-manifests)). They
don't block.

If all three template CRDs carry `helm.sh/resource-policy=keep`, as in
[Remove the template CRDs](#remove-the-template-crds), the `template-objects`
and `template-finalizers` findings, and owner references to template objects,
are `MANUAL` lines for the cleanup after the upgrade. Services that haven't
finished moving, pods that still mount a template cache's PVC, and v1alpha1
AIMService, AIMModel or AIMClusterModel owner references still block.

Once the new CRDs are applied, the AIMService CRD no longer serves v1alpha1.
The preflight then prints a single `Already upgraded` line and exits 0.

### Move template services

v0.2.7 selects the pipeline of each AIMService from its spec:

- `spec.profile`, or the annotation
  `aim.eai.amd.com/reconciler-pipeline: profile`, selects the profile pipeline.
- Otherwise `spec.model.name`, `spec.model.image`, `spec.model.custom` and
  `spec.template` select the template pipeline.
- `status.reconcilerPipeline` records the active pipeline.

Move each service the `template-pipeline-services` check lists by adding the
annotation. The profile pipeline ranks the AIMProfiles of the service's model
by status, primary flag, type and version. With `spec.model.image`, it first
uses or creates an AIMModel for the image. By default it only auto-selects
`optimized` profiles.

If the service sets `spec.template.allowUnoptimized: true`, first allow
unoptimized profiles too, and filter on the template's accelerator: otherwise
optimized GPU profiles rank first, and a CPU service moves to a GPU. Patch
before you annotate, because the annotation alone moves the service with the
default selector. For a template that requests no GPU:

```bash
kubectl patch aimservice <service> -n <namespace> --type merge \
  -p '{"spec":{"profile":{"selector":{"minimumType":"unoptimized","acceleratorType":"cpu"}}}}'
```

For a GPU template, replace `"acceleratorType":"cpu"` with
`"acceleratorModel":"<model>"`, using the template's `spec.hardware.gpu.model`,
for example `MI300X`.

The selected profile can still differ from the template, for example in GPU
count or precision. Add `acceleratorCount` or `precision` to the selector to
match them, or set `spec.profile.name` to run one specific profile.

Then add the annotation:

```bash
kubectl annotate aimservice <service> -n <namespace> aim.eai.amd.com/reconciler-pipeline=profile
```

The old InferenceService keeps serving until the profile cache is Ready. The
operator then rewrites the same InferenceService in place and adds the
`aim.eai.amd.com/profile` label, which KServe copies to the predictor
Deployment when it rolls new predictor pods. Wait for that label on the
service's predictor Deployment, then for the rollout. If a large download
outlasts the timeout, run it again:

```bash
kubectl wait deployment -n <namespace> --timeout=15m \
  --for=jsonpath='{.metadata.labels.aim\.eai\.amd\.com/profile}' \
  -l aim.eai.amd.com/service=<service>
kubectl rollout status deployment -n <namespace> --timeout=15m \
  -l aim.eai.amd.com/service=<service>,aim.eai.amd.com/profile
```

`kubectl rollout status` succeeds only when every predictor pod runs the new
spec and is ready. Don't wait on the AIMService instead: v0.2.7 reports it
`Running` and `Ready` while any predictor pod is ready.

On GPU clusters, the rollout needs free GPUs for one extra predictor pod: with
fewer than four replicas, the predictor Deployment doesn't stop an old pod
until a new one is ready. Without free GPUs, the new pod stays Pending, the old
pod keeps serving, and `kubectl rollout status` times out. Deleting the old pod
doesn't help: its ReplicaSet recreates it, and the new copy can take the GPUs
back. To finish the move, scale the old ReplicaSet to 0, then delete the
Pending pod. The service is unavailable until the new pod is ready:

```bash
kubectl get replicasets,pods -n <namespace> -l aim.eai.amd.com/service=<service>
kubectl scale replicaset <old-replicaset> -n <namespace> --replicas=0
kubectl delete pod <pending-pod> -n <namespace>
```

The old ReplicaSet is the one with Running pods; the Deployment doesn't scale
it up again. Then run `kubectl rollout status` again.

To move a service back to the template pipeline, remove `spec.profile` if you
added it, and the annotation:

```bash
kubectl patch aimservice <service> -n <namespace> --type json -p '[{"op":"remove","path":"/spec/profile"}]'
kubectl annotate aimservice <service> -n <namespace> aim.eai.amd.com/reconciler-pipeline-
```

This works until you delete the template objects.

v0.2.7 also includes `aim-template-migration`, which plans this move with a
backup and a server-side dry run. It sets `spec.profile.name` to an AIMProfile
that matches the service's template, and the existing InferenceService keeps
running without a rollout. It moves only a narrow set of services with a
Shared template cache and marks the rest manual, including every service with
a Dedicated cache or adapters; move those with the steps above. It isn't
intended for production and has no rollback. This release doesn't include it:
build it from the v0.2.7 tag and follow the
[v0.2.7 runbook](https://github.com/amd-enterprise-ai/aim-engine/blob/v0.2.7/docs/docs/admin/template-migration.md):

```bash
git clone --branch v0.2.7 --depth 1 https://github.com/amd-enterprise-ai/aim-engine.git
cd aim-engine
go build -o /tmp/aim-template-migration ./cmd/aim-template-migration
```

### Cache outcomes

Moving a service creates an AIMProfileCache for it. What happens to the cached
weights depends on the mode of its template cache:

- **Shared**: the profile cache adopts the existing AIMArtifact, so nothing is
  downloaded again. This needs the same model source URI, caching environment
  and storage class as the template cache.
- **Dedicated**: the template cache owns its artifact, so the profile cache
  downloads the weights again. Both copies use storage until you delete the
  template cache. The preflight's `dedicated-template-caches` lines list these
  artifacts and their sizes, so you can plan the extra space.

### Replace unservable services

The profile pipeline can't serve these service shapes, so the annotation alone
doesn't move them:

- `spec.model.custom`: `spec.model` can't be edited in place, so the service
  switches through `spec.profile`, as in
  [Change a service's model](../concepts/services.md#change-a-services-model).
  Create an AIMModel for the custom weights, as in
  [Custom Models](../guides/custom-models.md), and pick one of its
  AIMProfiles. In one merge patch, remove `spec.model` and set
  `spec.profile.name` to that AIMProfile. Then delete the AIMModel that v0.2.7
  generated for the inline custom input, which has the
  `aim.eai.amd.com/legacy-auto-custom-spec` annotation. Do it right away:
  v0.2.7 can't reconcile that model once the service has no `spec.model`.
  Deleting it also deletes any AIMProfiles it owns, so always use an AIMProfile
  of the new AIMModel:

  ```bash
  kubectl get aimprofile -n <namespace> -l aim.eai.amd.com/source-model=<new-model>
  kubectl patch aimservice <service> -n <namespace> --type merge \
    -p '{"spec":{"model":null,"profile":{"name":"<AIMProfile of the new AIMModel>"}}}'
  kubectl delete aimmodel -n <namespace> \
    -l aim.eai.amd.com/custom-model=true,aim.eai.amd.com/service=<service>
  ```

  The service moves to the profile pipeline and keeps its InferenceService.
  With a Shared cache, the AIMProfileCache adopts the existing AIMArtifact, as
  in [Cache outcomes](#cache-outcomes), so nothing is downloaded again. Wait
  for the rollout as in [Move template services](#move-template-services).
- `spec.template` without `spec.model` or `spec.profile`: create an AIMProfile
  that matches the template, as in [Update v1alpha1 manifests](#update-v1alpha1-manifests),
  and set `spec.profile.name` on the service.

### Replace models without a native path

After the upgrade, AIMModels and AIMClusterModels serve only through the
AIMProfiles they discover or generate. These models produce none:

- `spec.custom`, `spec.customTemplates` or `spec.modelSources`: create a
  v1alpha2 AIMModel as in [Custom Models](../guides/custom-models.md)
  or [Fine-Tuned Models](../guides/fine-tuned-models.md), move the services that
  use the old model to it, then delete the old model.
- `spec.aimId` on an image model: unless the model is a fine-tune, remove the
  field. Discovery reads the AIM ID from the image:

  ```bash
  kubectl patch aimmodel <model> -n <namespace> --type json -p '[{"op":"remove","path":"/spec/aimId"}]'
  ```

- `spec.discovery.extractMetadata: false` on an image model: allow discovery,
  which runs the image in a Job and reads its labels from the registry:

  ```bash
  kubectl patch aimmodel <model> -n <namespace> --type merge -p '{"spec":{"discovery":{"extractMetadata":true}}}'
  ```

- The `aim.eai.amd.com/legacy-auto-custom-spec` annotation: the model was
  generated for a service with `spec.model.custom`. Replacing that service as
  in [Replace unservable services](#replace-unservable-services) deletes the
  model.

For an AIMClusterModel, use `aimclustermodel` and leave out `-n`.

### Delete template objects

First, stop image models from creating templates. Set
`discovery.createServiceTemplates: false` on every AIMModel and AIMClusterModel
with `spec.image`. That includes the models an AIMClusterModelSource
discovered, because the source doesn't update models it has already created.
The commands skip the AIMModels v0.2.7 generated for `spec.model.custom`
services: v0.2.7 fails the service when such a model's spec changes.
[Replace unservable services](#replace-unservable-services) deletes them:

```bash
kubectl get aimmodels --all-namespaces -o json \
  | jq -r '.items[] | select(.spec.image and .metadata.annotations["aim.eai.amd.com/legacy-auto-custom-spec"] == null)
      | "\(.metadata.namespace) \(.metadata.name)"' \
  | while read -r namespace name; do
      kubectl patch aimmodel "${name}" -n "${namespace}" --type merge \
        -p '{"spec":{"discovery":{"createServiceTemplates":false}}}'
    done
kubectl get aimclustermodels -o json \
  | jq -r '.items[] | select(.spec.image) | .metadata.name' \
  | while read -r name; do
      kubectl patch aimclustermodel "${name}" --type merge \
        -p '{"spec":{"discovery":{"createServiceTemplates":false}}}'
    done
```

Then, once every service runs on the profile pipeline, delete the template
objects while the current operator still runs. Its finalizers then clean up,
and garbage collection deletes the objects the templates own:

```bash
kubectl delete aimtemplatecaches --all --all-namespaces
kubectl delete aimservicetemplates --all --all-namespaces
kubectl delete aimclusterservicetemplates --all
```

`kubectl delete` waits for the finalizers. If a template cache stays in
`Terminating`, check the operator log. v0.2.7 removes the legacy
`aim.eai.amd.com/template-cache-cleanup` finalizer from an AIMService when it
reconciles the service.

If a GitOps tool manages template objects, remove them from the repository
first, or the tool recreates them.

After the upgrade `discovery.createServiceTemplates` has no effect. The models
report it in a `TemplateFieldsIgnored` condition, which doesn't affect
readiness. Keep it as long as you might [roll back](#rollback): v0.2.7 creates
templates for image models without it. Remove it only once you've decided not
to roll back:

```bash
kubectl patch aimmodel <model> -n <namespace> --type json \
  -p '[{"op":"remove","path":"/spec/discovery/createServiceTemplates"}]'
```

### Remove stale v1alpha1 owner references

After the upgrade, the API server no longer serves the v1alpha1 AIMService,
AIMModel and AIMClusterModel, or the template kinds. Garbage collection can't
resolve owner references to them: deleting the owner leaves the dependent
behind, and a foreground delete of the owner, which Argo CD uses by default,
hangs in `foregroundDeletion`.

v0.2.7 rewrites v1alpha1 owner references to AIMService, AIMModel and
AIMClusterModel on the objects it manages when it next reconciles the owner.
Before the upgrade, rewrite the ones left, for example on objects your own
tools created, to `aim.eai.amd.com/v1alpha2`:

```bash
resources="$(kubectl api-resources -o name)"
for resource in inferenceservices.serving.kserve.io httproutes.gateway.networking.k8s.io \
  scaledobjects.keda.sh persistentvolumeclaims jobs.batch configmaps \
  aimartifacts.aim.eai.amd.com aimprofiles.aim.eai.amd.com aimprofilecaches.aim.eai.amd.com; do
  grep -qxF "${resource}" <<<"${resources}" || continue
  kubectl get "${resource}" --all-namespaces -o json \
    | jq -r '.items[] | . as $item | .metadata.ownerReferences // [] | to_entries[]
        | select(.value.apiVersion == "aim.eai.amd.com/v1alpha1"
          and (.value.kind | IN("AIMService", "AIMModel", "AIMClusterModel")))
        | [$item.metadata.namespace, $item.metadata.name,
          ([{op: "test", path: "/metadata/ownerReferences/\(.key)/uid", value: .value.uid},
            {op: "replace", path: "/metadata/ownerReferences/\(.key)/apiVersion",
              value: "aim.eai.amd.com/v1alpha2"}] | tojson)] | join(" ")' \
    | while read -r namespace name patch; do
        kubectl patch "${resource}" "${name}" -n "${namespace}" --type json -p "${patch}"
      done
done
```

The commands still work after the upgrade, but garbage collection can then
take several minutes to act on a rewritten reference. References to the
v1alpha1 AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and
AIMClusterModelSource are fine.

Objects owned by template objects, such as Dedicated template-cache artifacts
and template discovery Jobs, go away when you delete the template objects while
their CRDs exist. Garbage collection can take a minute, so rerun the preflight.
Once the template CRDs are gone, garbage collection no longer deletes such
objects. So before you remove the CRDs, delete each object the preflight still
lists, or remove its owner reference if you need the object:

```bash
kubectl delete <resource> <name> -n <namespace>
kubectl patch <resource> <name> -n <namespace> --type json \
  -p '[{"op":"remove","path":"/metadata/ownerReferences/<index>"}]'
```

### Update v1alpha1 manifests

AIMService, AIMModel and AIMClusterModel stop serving
`aim.eai.amd.com/v1alpha1` in this release. Existing objects stay readable as
v1alpha2, but applying a v1alpha1 manifest for these kinds fails. Check GitOps
repositories, Helm charts and scripts, and change those manifests to v1alpha2
before you upgrade. Replace template objects in those manifests too:

| v1alpha1 | v1alpha2 |
|---|---|
| `AIMServiceTemplate.spec` | `AIMProfile.spec`, one profile per template profile |
| `AIMClusterServiceTemplate.spec` | `AIMClusterProfile.spec` |
| `AIMTemplateCache` | `AIMProfileCache`, created from `AIMProfile.spec.caching` |
| `AIMService.spec.template` | `AIMService.spec.profile` |
| `AIMService.spec.overrides` | `AIMService.spec.profileOverrides`, see [Profile overlays](../concepts/services.md#profile-overlays) |
| `AIMModel.spec.custom` | `AIMModel.spec.image` (base) and `AIMModel.spec.profiles`, see [Custom Models](../guides/custom-models.md) |
| `AIMModel.spec.customTemplates` | `AIMProfileSet` |
| `AIMModel.spec.profileCopy` | `AIMModel.spec.profiles`, see [Fine-Tuned Models](../guides/fine-tuned-models.md) |
| `AIMModel.spec.modelSources` | `AIMProfile.spec.modelSources` |

AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and
AIMClusterModelSource still apply at v1alpha1 in this release. They are
deprecated; switch them to v1alpha2 before the next release. See
[Artifact, runtime config and model source versions](#artifact-runtime-config-and-model-source-versions).

### Remove the template CRDs

This release's CRDs don't include `aimservicetemplates.aim.eai.amd.com`,
`aimclusterservicetemplates.aim.eai.amd.com` and
`aimtemplatecaches.aim.eai.amd.com`. Deleting a CRD deletes every object of
its kind, and the new operator doesn't remove the finalizers of template
objects. So delete the template objects before the upgrade, as in
[Delete template objects](#delete-template-objects); the preflight's
`template-objects` check enforces this.

**Helm**: upgrading the CRDs chart deletes the template CRDs. When the
preflight passes, no template objects are left, so nothing else is needed.

If you can't delete every template object before the upgrade, keep the
template CRDs and remove them by hand after it. This only covers leftover
template objects: every AIMService must still be on the profile pipeline
before the upgrade, and the preflight's `template-pipeline-services` check
still blocks. The new operator ignores `spec.template` and selects an
AIMProfile with the default selector, which can pick a GPU AIMProfile for a CPU
service. Without a free GPU, the rollout stalls, the old pod keeps mounting the
template cache's PVC, and removing the template objects deletes that PVC.

1. Before the upgrade, annotate the template CRDs. Helm doesn't delete a
   resource whose live object has this annotation. The preflight then reports
   template objects as `MANUAL` lines, as in
   [Run the upgrade preflight](#run-the-upgrade-preflight):

   ```bash
   kubectl annotate crd aimservicetemplates.aim.eai.amd.com \
     aimclusterservicetemplates.aim.eai.amd.com aimtemplatecaches.aim.eai.amd.com \
     helm.sh/resource-policy=keep
   ```

2. Upgrade the CRDs and the operator.
3. Remove the template objects and CRDs as for a kubectl install.

**kubectl**: `kubectl apply` doesn't delete CRDs, so the template CRDs stay
after the upgrade. Strip the finalizers from any template objects that are
left, delete the objects while their CRDs still exist, so garbage collection
deletes what they own, then delete the CRDs. The commands first stop, and
change nothing, while a pod mounts the PVC of an AIMArtifact that an
AIMTemplateCache owns:

```bash
(
  set -euo pipefail
  mounted="$({ kubectl get aimartifacts --all-namespaces -o json; kubectl get pods --all-namespaces -o json; } \
    | jq -rs '.[0].items as $artifacts | .[1].items[]
        | select(.status.phase | IN("Succeeded", "Failed") | not) | . as $pod
        | (.spec.volumes[]?.persistentVolumeClaim.claimName // empty) as $claim
        | $artifacts[] | select(.metadata.namespace == $pod.metadata.namespace
          and .status.persistentVolumeClaim == $claim
          and any(.metadata.ownerReferences[]?; .kind == "AIMTemplateCache"))
        | "\($pod.metadata.namespace)/\($pod.metadata.name) mounts PVC \($claim)"')"
  if [[ -n "${mounted}" ]]; then
    printf 'Stopped. These pods mount the PVC of an AIMArtifact that an AIMTemplateCache owns:\n%s\n' \
      "${mounted}" >&2
    exit 1
  fi
  for resource in aimtemplatecaches aimservicetemplates aimclusterservicetemplates; do
    kubectl get "${resource}.aim.eai.amd.com" --all-namespaces \
      -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.namespace}{"\n"}{end}' \
      | while read -r name namespace; do
          kubectl patch "${resource}.aim.eai.amd.com" "${name}" --namespace "${namespace}" \
            --type merge -p '{"metadata":{"finalizers":null}}'
        done
    kubectl delete "${resource}.aim.eai.amd.com" --all --all-namespaces
  done
  kubectl delete crd aimtemplatecaches.aim.eai.amd.com \
    aimservicetemplates.aim.eai.amd.com aimclusterservicetemplates.aim.eai.amd.com
)
```

If they stop, finish moving the services of the listed pods, as in
[Move template services](#move-template-services), and run them again.

Without its finalizer, a template cache deleted this way leaves behind the
AIMArtifacts it hadn't finished downloading. Delete them by hand if no
AIMProfileCache uses them.

### Rollback limits

Until you delete the template objects, you can move each service back to the
template pipeline, as in [Move template services](#move-template-services).
After that, moving back needs new template objects.

After the upgrade, rolling the CRDs and operator back to v0.2.7, as in
[Rollback](#rollback), restores the template pipeline and v1alpha1 serving. It
doesn't restore the template objects or the Dedicated weights you deleted
before the upgrade: recreate the templates from your own manifests if you need
them, and expect Dedicated caches to download again. Do the steps at the start
of [Rollback](#rollback) first; v0.2.7 otherwise moves services back to the
template pipeline, creates templates again and stops reconciling some models.

## Next Steps

- [Installation Reference](installation.md) — Full installation options
- [Changelog](../changelog.md) — Release notes and changes
