# Upgrading

This guide covers upgrading AIM Engine to a new version and operating in the
v1alpha1 → v1alpha2 migration window. The [Migration window](#migration-window)
section is the canonical reference for spec-shape dispatch, the
`aim.eai.amd.com/reconciler-pipeline` annotation, and the recommended order
for migrating cluster-wide, namespace-scoped, and per-service resources.

## Upgrade Procedure

### 1. Update CRDs First

CRDs must be updated before the operator, as new operator versions may depend on new CRD fields:

```bash
helm upgrade aim-engine-crds oci://docker.io/amdenterpriseai/aim-engine-crds-chart \
  --version <new-version> \
  --namespace aim-system
```

### 2. Upgrade the Operator

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

### 3. Verify

```bash
kubectl get pods -n aim-system
kubectl get aimservice --all-namespaces
```

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
- API group is `aim.eai.amd.com`; both `v1alpha1` and `v1alpha2` versions are served by every supported operator release until v1alpha1 is formally removed (see [Migration window](#migration-window))

The minimum supported Kubernetes version is **1.32** (see [Prerequisites](../getting-started/installation.md#prerequisites)).

## Migration window

The aim-engine operator serves `v1alpha1` and `v1alpha2` of every shared CRD
(`AIMService`, `AIMModel`, `AIMClusterModel`, etc.) side by side. The two
versions are **not** translated by a conversion webhook — they share a
single storage representation and the operator dispatches reconcile work
based on the *spec shape* the user authored, with the
`aim.eai.amd.com/reconciler-pipeline` annotation acting as an explicit
escape hatch.

This section is the source of truth for that mechanism. Other docs link
here rather than restating the details so the dispatch story can't drift.

### Storage version and conversion strategy

| CRD                    | Stored as | Conversion strategy |
| ---------------------- | --------- | ------------------- |
| `AIMService`           | v1alpha2  | `None`              |
| `AIMModel`             | v1alpha2  | `None`              |
| `AIMClusterModel`      | v1alpha2  | `None`              |
| `AIMProfile`           | v1alpha2  | `None`              |
| `AIMClusterProfile`    | v1alpha2  | `None`              |
| `AIMProfileSet`        | v1alpha2  | `None`              |
| `AIMClusterProfileSet` | v1alpha2  | `None`              |
| `AIMProfileCache`      | v1alpha2  | `None`              |

`conversionStrategy: None` means the API server stores whatever fields the
client sent under whichever version they used and serves them back
verbatim. There is no automatic field rewrite: a v1alpha1 client that
asks for an object created via v1alpha2 sees the same field values, just
under the v1alpha1 schema. **The `apiVersion` you submit never affects
which reconcile pipeline runs** — only the spec shape and the
reconciler-pipeline annotation do.

In practice this lets you:

- Run v1alpha1 and v1alpha2 clients (helm charts, GitOps repos, kubectl
  scripts) against the same cluster while migrating.
- Pin specific consumers to one version without coordinating a flag day.
- Roll back the operator without converting stored objects (the CRD
  storage layer doesn't change).

### Spec-shape dispatch

The `AIMService` controller is the only resource where two pipelines
coexist (the v1alpha1 template pipeline and the v1alpha2 profile
pipeline). Every other CRD has a single controller that handles both
versions uniformly.

| Spec shape on `AIMService`                              | Default pipeline | Notes                                                                                                                          |
| ------------------------------------------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `spec.profile.*` set                                    | profile          | The native v1alpha2 path. No annotation needed.                                                                                |
| `spec.template.*` set                                   | template (only)  | `v1alpha2` admission rejects `spec.template`; this combination is only legal on a `v1alpha1` `AIMService`.                     |
| `spec.model.name` only                                  | template         | Annotate `aim.eai.amd.com/reconciler-pipeline: profile` to use the v1alpha2 model→profile resolver instead.                    |
| `spec.model.image` only                                 | template         | Annotate `aim.eai.amd.com/reconciler-pipeline: profile` to use the v1alpha2 image path (auto-creates a dedicated `AIMModel`).  |
| `spec.model.custom.*` set                               | template         | The v1alpha2 profile pipeline does not implement the custom-model shortcut; keep the default until you migrate to `AIMModel`. |

The annotation accepts two values:

- `profile` — force the v1alpha2 profile pipeline.
- `template` — force the v1alpha1 template pipeline (for the rare case
  where a profile-shaped service needs to fall back, e.g. during
  troubleshooting).

Unknown values and the absence of the annotation both fall through to
the spec-shape default. The annotation has **higher precedence than
spec shape** — `spec.profile` + `reconciler-pipeline: template` runs on
the legacy pipeline, even though the spec is v1alpha2-shaped.

### `spec.model.image` + annotation: the v1alpha2 quick-start path

A service authored with `spec.model.image` plus the
`aim.eai.amd.com/reconciler-pipeline: profile` annotation triggers a
dedicated auto-create flow on the profile pipeline:

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMService
metadata:
  name: chat
  annotations:
    aim.eai.amd.com/reconciler-pipeline: profile
spec:
  model:
    image: ghcr.io/silogen/aim-dummy:0.2.0
```

What happens on the first reconcile:

1. The resolver looks up existing `AIMModel`/`AIMClusterModel` resources
   whose `.spec.image` matches the requested image (uses the
   `.spec.image` field indexers in
   [`internal/v1alpha2/controller/aimmodel_controller.go`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/internal/v1alpha2/controller/aimmodel_controller.go)
   and
   [`aimclustermodel_controller.go`](https://github.com/amd-enterprise-ai/aim-engine/blob/main/internal/v1alpha2/controller/aimclustermodel_controller.go)).
2. If none match, the plan step server-side-applies a v1alpha2 `AIMModel`:
    - Name: `<svcName>-auto-<6charHash(image)>`
    - Owner reference: the `AIMService` (controller=true, GC fires when the service is deleted)
    - Label: `aim.eai.amd.com/origin: auto-generated`
    - Annotation: `aim.eai.amd.com/auto-source: spec.model.image`
    - `spec.image`, `spec.imagePullSecrets`, `spec.serviceAccountName` inherited from the service
3. The v1alpha2 `AIMModel` controller runs native image discovery and
   emits one or more `AIMProfile` resources.
4. The next service reconcile (fanned out by the watch on `AIMModel`)
   treats `spec.model.image` as if you had written `spec.model.name`
   pointing at the auto-created model, runs the standard model+selector
   resolver path, and resolves a deployable `AIMProfile`.

Behavioural guarantees worth surfacing in operator runbooks:

- **Dedicated per service.** Two services that authored the same image
  get two dedicated models. To share, author an `AIMModel` explicitly
  and reference it with `spec.model.name`.
- **No reuse of pre-existing models.** The auto-create path is only
  taken when no `AIMModel`/`AIMClusterModel` with the requested image
  exists. If a user-authored or auto-created model with that image is
  already present in scope (namespace then cluster), the resolver uses
  it (no extra model is created).
- **Multiple matches refuse to guess.** If two models in scope reference
  the same image, the resolver reports
  `ProfileReady=False / reason=ProfileNotFound` with a message naming
  both candidates and instructs the user to pin via `spec.model.name`.
  No model is auto-created in this case.
- **Filtering auto-created models.** Use
  `kubectl get aimmodel -l 'aim.eai.amd.com/origin!=auto-generated'` to
  hide auto-created models from inventories. The same label also covers
  v1alpha1-style image auto-creates, so the filter works across the
  migration window.

### Deletion / GC story

When an `AIMService` that auto-created a model is deleted:

- The dedicated `AIMModel` is garbage-collected (owner reference).
- Its emitted `AIMProfile` resources are garbage-collected (the model
  controller owner-refs them).
- The `AIMProfileCache` CR is garbage-collected.
- Cached **artifact data** (PVCs / `AIMArtifact` content) is **preserved**
  while the cache stays in the default `Shared` mode — the artifacts
  carry no owner reference to the cache and persist for reuse. Switch
  the service's caching mode to `Dedicated` only when you want the
  underlying PVCs to disappear with the service.

### When the annotation becomes unnecessary

The annotation is a migration-window opt-in. Once v1alpha1 is removed
the spec-shape defaults flip — `spec.model.image` and `spec.model.name`
both route to the profile pipeline natively, and the annotation becomes
a no-op. Until then, **annotate every image-shape or model-shape service
you want on the v1alpha2 pipeline** so the dispatch behaviour is
explicit in the spec (no silent dependency on default rules that will
change).

### Recommended migration order

Migrate top-down to minimise the window where a service points at a
template that's about to disappear.

1. **Cluster-scoped resources first.** Apply v1alpha2 `AIMClusterModel`,
   `AIMClusterProfileSet`, and `AIMClusterProfile` definitions (or let
   the operator derive them from existing `AIMClusterModel`s with
   `spec.profiles`). Existing `AIMClusterServiceTemplate`s continue to
   work for v1alpha1 services in the meantime.
2. **Namespace-scoped derivations.** For teams already using
   `AIMServiceTemplate`-driven workflows, introduce `AIMModel`
   `spec.profiles.derivedFrom` or `AIMProfileSet` so the same model
   produces v1alpha2 `AIMProfile`s alongside the legacy
   `AIMServiceTemplate`s. Both ride on the single CRD storage.
3. **Services last.** Once the upstream profiles are healthy, flip
   individual services to `spec.profile.*` (or annotate `spec.model.*`
   services with `reconciler-pipeline: profile`). Deletion of the old
   `AIMServiceTemplate` references is the last step; until then the
   template pipeline can continue to serve.

See [Migrating to v1alpha2](../legacy/migrating.md) for the full
field-by-field mapping and conversion recipes.

## Next Steps

- [Installation Reference](installation.md) — Full installation options
- [Changelog](../changelog.md) — Release notes and changes
