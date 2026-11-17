# Upgrading

This guide covers upgrading AIM Engine to a new version. If the cluster still
has template objects or AIMServices on the template pipeline, follow
[Migration window](#migration-window) on your current release before you
upgrade.

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
    baseImageRef: docker.io/amdenterpriseai/aim-base:0.11
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

### 3. Configure lineage and the aim-base registry

After updating the CRDs but before upgrading the operator:

1. Set `spec.baseImageRef` on affected hand-authored deployable profiles.
2. Derived images keep taking aim-base's registry and org from the model image,
   so public installations and mirrors that host both the model images and
   aim-base need no RuntimeConfig change.
3. If aim-base lives in a different registry from the model images, set
   `baseImageRegistry` on `default` and on each named RuntimeConfig selected by
   services or legacy fine-tuned models; named configurations do not inherit it
   from `default`.

```yaml
apiVersion: aim.eai.amd.com/v1alpha1
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

Roll back to the previous Helm release:

```bash
helm rollback aim-engine -n aim-system
```

:::{note}
Rolling back the operator does not roll back CRD changes. New CRD fields are additive and backward compatible. If a CRD change is not backward compatible, this will be noted in the release notes.
:::

Rolling back past the removal of the template serving path has limits; see
[Rollback limits](#rollback-limits).

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

2. Confirm that no other cache still lists the old artifact. Shared artifacts serve every cache with a matching source, including v1alpha1 template caches, which do not read image declarations, and a profile cache that has not switched yet still serves the old one. This should print nothing:

   ```bash
   kubectl get aimprofilecache,aimtemplatecache -n <namespace> -o json | \
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
`AIMService`, `AIMModel` and `AIMClusterModel`. Prepare the cluster on its
current release, while the template path still works, and upgrade only when
the upgrade preflight passes:

1. Check that the cluster runs the [minimum version](#minimum-version).
2. [Run the upgrade preflight](#run-the-upgrade-preflight) to see what is left
   to do.
3. [Move template services](#move-template-services) to the profile pipeline.
4. Replace any [unservable services](#replace-unservable-services) and
   [models without a native path](#replace-models-without-a-native-path) the
   preflight lists.
5. [Delete template objects](#delete-template-objects).
6. [Update v1alpha1 manifests](#update-v1alpha1-manifests) in GitOps
   repositories and scripts.
7. Rerun the preflight until it passes. Then upgrade the CRDs before the
   operator, as in [Upgrade Procedure](#upgrade-procedure).

### Minimum version

Upgrade from v0.2.7 or later. If the cluster runs an older release, upgrade to
the latest 0.2.x release first and let it reconcile your resources. v0.2.7 is
the first release that records `status.reconcilerPipeline` on every AIMService
and rewrites v1alpha1 owner references to v1alpha2. The steps below rely on
both.

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

It needs `kubectl`, with permission to get and list the resources below in all
namespaces, and `jq` 1.6 or later. It only reads from the cluster. It exits
with 0 when every blocking check passes, 1 when any fails, and 2 when a
required tool is missing. Each `FAIL` line names the check, the object and the
section of this guide that fixes it:

| Check | Fails when | Section |
|---|---|---|
| `minimum-version` | The operator is older than v0.2.7, or an AIMService has no `status.reconcilerPipeline` | [Minimum version](#minimum-version) |
| `template-pipeline-services` | An AIMService is not on the profile pipeline | [Move template services](#move-template-services) |
| `unservable-services` | An AIMService uses `spec.model.custom`, or `spec.template` without `spec.model` or `spec.profile` | [Replace unservable services](#replace-unservable-services) |
| `models-without-native-path` | An AIMModel or AIMClusterModel uses `spec.custom`, `spec.customTemplates` or `spec.modelSources`; is an image model with `spec.aimId` or `spec.discovery.extractMetadata: false`; or was generated for an inline custom service | [Replace models without a native path](#replace-models-without-a-native-path) |
| `template-objects` | An AIMServiceTemplate, AIMClusterServiceTemplate or AIMTemplateCache exists | [Delete template objects](#delete-template-objects) |
| `template-finalizers` | An AIMService or AIMTemplateCache has a template cleanup finalizer | [Delete template objects](#delete-template-objects) |
| `stale-owner-references` | An InferenceService, HTTPRoute, ScaledObject, PVC, Job, ConfigMap, AIMArtifact, AIMProfile or AIMProfileCache is owned by a v1alpha1 AIMService, AIMModel, AIMClusterModel or template object | [Remove stale v1alpha1 owner references](#remove-stale-v1alpha1-owner-references) |

`INFO` lines list Dedicated template caches with their sizes (see
[Cache outcomes](#cache-outcomes)). `MANUAL` lines name what the script can't
check and you confirm by hand: the operator version when it can't be read (see
[Minimum version](#minimum-version)), and v1alpha1 manifests outside the
cluster (see [Update v1alpha1 manifests](#update-v1alpha1-manifests)). They
don't block.

### Move template services

v0.2.7 selects the pipeline of each AIMService from its spec:

- `spec.profile`, or the annotation
  `aim.eai.amd.com/reconciler-pipeline: profile`, selects the profile pipeline.
- Otherwise `spec.model.name`, `spec.model.image`, `spec.model.custom` and
  `spec.template` select the template pipeline.
- `status.reconcilerPipeline` records the active pipeline.

Move each service the `template-pipeline-services` check lists by adding the
annotation:

```bash
kubectl annotate aimservice <service> -n <namespace> aim.eai.amd.com/reconciler-pipeline=profile
```

The profile pipeline ranks the AIMProfiles of the service's model by status,
primary flag, type and version. With `spec.model.image`, it first uses or
creates an AIMModel for the image. By default it only auto-selects `optimized`
profiles. If the service sets `spec.template.allowUnoptimized: true`, allow
unoptimized profiles too:

```bash
kubectl patch aimservice <service> -n <namespace> --type merge \
  -p '{"spec":{"profile":{"selector":{"minimumType":"unoptimized"}}}}'
```

The selected profile can differ from the template the service ran, for
example in GPU count or precision. To run one specific profile, set
`spec.profile.name` instead.

Then wait until the service serves from its profile cache. If a large download
outlasts the timeout, run the command again:

```bash
kubectl wait aimservice/<service> -n <namespace> --for=condition=ProfileCacheReady --timeout=15m
kubectl wait aimservice/<service> -n <namespace> --for=jsonpath='{.status.status}'=Running --timeout=15m
```

The old InferenceService keeps serving until the profile cache is Ready. The
operator then rewrites the same InferenceService in place, and KServe rolls
new predictor pods.

To move a service back to the template pipeline, remove `spec.profile` if you
added it, and the annotation:

```bash
kubectl patch aimservice <service> -n <namespace> --type json -p '[{"op":"remove","path":"/spec/profile"}]'
kubectl annotate aimservice <service> -n <namespace> aim.eai.amd.com/reconciler-pipeline-
```

This works until you delete the template objects.

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

- `spec.model.custom`: create an AIMModel for the custom weights, as in
  [Custom models](../legacy/migrating.md#custom-models), and change the service
  to reference it with `spec.model.name`. Then move the service as above, and
  delete the AIMModel that was generated for its inline custom input. That
  model has the `aim.eai.amd.com/legacy-auto-custom-spec` annotation.
- `spec.template` without `spec.model` or `spec.profile`: create an AIMProfile
  that matches the template, as in [Migrating to v1alpha2](../legacy/migrating.md),
  and set `spec.profile.name` on the service.

### Replace models without a native path

After the upgrade, AIMModels and AIMClusterModels serve only through the
AIMProfiles they discover or generate. These models produce none:

- `spec.custom`, `spec.customTemplates` or `spec.modelSources`: create a
  v1alpha2 AIMModel as in [Custom models](../legacy/migrating.md#custom-models)
  or [Fine-tunes](../legacy/migrating.md#fine-tunes), move the services that
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
  generated for a service with `spec.model.custom`. Replace that service as in
  [Replace unservable services](#replace-unservable-services), then delete the
  model.

For an AIMClusterModel, use `aimclustermodel` and leave out `-n`.

### Delete template objects

First, stop image models from creating templates. Set
`discovery.createServiceTemplates: false` on every AIMModel and AIMClusterModel
with `spec.image`. That includes the models an AIMClusterModelSource
discovered, because the source doesn't update models it has already created:

```bash
kubectl get aimmodels --all-namespaces -o json \
  | jq -r '.items[] | select(.spec.image) | "\(.metadata.namespace) \(.metadata.name)"' \
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

### Remove stale v1alpha1 owner references

After the upgrade, the API server no longer serves the v1alpha1 AIMService,
AIMModel and AIMClusterModel, or the template kinds. Garbage collection can't
resolve owner references to them, so the objects they own would leak.

Objects owned by template objects, such as Dedicated template-cache artifacts
and template discovery Jobs, go away when you delete the template objects.
Garbage collection can take a minute, so rerun the preflight. v0.2.7 rewrites
v1alpha1 owner references to AIMService, AIMModel and AIMClusterModel when it
next reconciles the owner. If a reference stays, check the owner's status and
the operator log. References to the v1alpha1 AIMArtifact, AIMRuntimeConfig,
AIMClusterRuntimeConfig and AIMClusterModelSource are fine.

### Update v1alpha1 manifests

AIMService, AIMModel and AIMClusterModel stop serving
`aim.eai.amd.com/v1alpha1` in this release. Existing objects stay readable as
v1alpha2, but applying a v1alpha1 manifest for these kinds fails. Check GitOps
repositories, Helm charts and scripts, and change those manifests to v1alpha2,
as in [Migrating to v1alpha2](../legacy/migrating.md), before you upgrade.
AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and
AIMClusterModelSource stay at v1alpha1.

### Rollback limits

Until you delete the template objects, you can move each service back to the
template pipeline, as in [Move template services](#move-template-services).
After that, moving back needs new template objects.

After the upgrade, rolling the CRDs and operator back to v0.2.7 restores the
template pipeline and v1alpha1 serving. It doesn't restore the template
objects or the Dedicated weights you deleted before the upgrade: recreate the
templates from your own manifests if you need them, and expect Dedicated
caches to download again.

## Next Steps

- [Installation Reference](installation.md) — Full installation options
- [Changelog](../changelog.md) — Release notes and changes
