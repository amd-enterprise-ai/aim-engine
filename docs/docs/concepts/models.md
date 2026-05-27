# AIM Models

`AIMModel` and `AIMClusterModel` are the entry point for getting a model onto the cluster. Each resource describes one model and produces a set of `AIMProfile` resources that `AIMService` can deploy.

!!! info "v1alpha2"
    This page documents the `aim.eai.amd.com/v1alpha2` API. For the v1alpha1 `AIMModel` shape (with `spec.custom`, `spec.modelSources`, `spec.discovery`, etc.) see [Legacy AIMModel](../legacy/aimmodel-v1alpha1.md).

## Three flows

Every v1alpha2 AIMModel uses one of three flows. The flow is determined by which field you populate on the spec:

| Flow | When to use | Spec shape | Source of profiles | `status.kind` |
|---|---|---|---|---|
| **Official** | Deploying a published AMD-supported AIM model unmodified | `spec.image` set | The AIM container image itself (discovery) | `Image` |
| **Fine-tuned** | Deploying a fine-tune of a published architecture | `spec.profiles.derivedFrom` selecting deployable profiles | A previously-applied official AIMModel | `Derived` |
| **Custom** | Deploying a model whose architecture isn't in the catalog | `spec.profiles.derivedFrom` selecting base-image base profiles | A previously-applied base-image AIMModel | `Custom` |

The onboarding contract is enforced by CRD validation: **exactly one** of `spec.image` or `spec.profiles` must be set. Mixing them — or setting neither — is rejected at admission.

The flow you used is surfaced as `status.kind` and shown as the `KIND` column in `kubectl get aimmodel`. Note that **base-image AIMModels classify as `Image`** (they're a sub-case of the Official flow with a generic image, not a fourth flow). To filter for base-image models specifically, combine `status.kind=Image` with `status.managedProfiles.base > 0` — see [Base-image models](#base-image-models) below.

End-to-end guides for the two derivation flows:

- [Fine-Tuned Models](../guides/fine-tuned-models.md)
- [Custom Models](../guides/custom-models.md)

## Cluster vs namespace scope

| Resource | Scope | Typical owner | Produces |
|---|---|---|---|
| `AIMModel` | Namespace | Application / ML team | `AIMProfile` in the same namespace |
| `AIMClusterModel` | Cluster | Platform team | `AIMClusterProfile` across the cluster |

When an `AIMService` references a model by name, AIM Engine looks for a namespace-scoped `AIMModel` first, then falls back to a cluster-scoped `AIMClusterModel` with the same name. This lets teams override a cluster default without affecting other namespaces.

The two kinds share the same `spec` and `status` shape — every spec example below applies to both unless noted.

## Flow 1 — Official AIM model

Use this when AMD publishes an AIM image for the exact model you want to deploy.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: llama-3-8b-official
  namespace: ml-team
spec:
  image: quay.io/amd/aim-llama-3-8b-instruct:0.9.0
  imagePullSecrets:
    - name: registry-creds
  serviceAccountName: aim-runtime
```

The controller:

1. Runs an in-cluster **discovery Job** that inspects the image's profile YAMLs (`/workspace/aim-runtime/profiles/<aim_id>/*.yaml`) and writes a normalised catalog into a `ConfigMap` (`status.discoveryCacheRef`).
2. Evaluates each discovered profile against the cluster's nodes — GPU model, accelerator count, resource capacity.
3. Materialises one `AIMProfile` per supported entry. Unsupported entries (no matching nodes) are counted in `status.discoveredProfiles.unsupported` but not materialised.
4. Watches node events and re-evaluates the catalog when nodes are added or removed.

The resulting profiles are **deployable** (carry `aimId` and `modelSources`) and labelled `aim.eai.amd.com/profile-role=deployable`, `aim.eai.amd.com/profile-origin=discovered`.

### Status signal

```bash
kubectl get aimmodel llama-3-8b-official -o jsonpath='{.status}' | jq
```

```json
{
  "status": "Ready",
  "aimId": "meta-llama/Llama-3-8B-Instruct",
  "baseImage": "ghcr.io/silogen/aim-base:0.11",
  "discoveryCacheRef": { "name": "llama-3-8b-official-discovery-5e8ad928" },
  "discoveredProfiles": { "total": 7, "supported": 5, "unsupported": 2 },
  "managedProfiles": { "total": 5, "ready": 5, "deployable": 5, "base": 0, "notAvailable": 0 }
}
```

## Flow 2 — Fine-tuned model

Use this when you have your own fine-tune of a model whose architecture **is** in the catalog. The official AIMModel's profiles already carry the tuned engine arguments and accelerator pairings — you reuse them and override only the model sources.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: llama-3-8b-finetune-acme
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        aimId: meta-llama/Llama-3-8B-Instruct
        acceleratorModel: MI300X
    versionPolicy: pinned
    version: "0.9.0"
    overrides:
      modelSources:
        - modelId: acme/llama-3-8b-finetune
          sourceUri: hf://acme/llama-3-8b-finetune
```

`selector.aimId` matches source profiles by their existing `spec.aimId`. The derived profile inherits `aimId` from the source and only `modelSources` and `modelId` change.

See [Fine-Tuned Models](../guides/fine-tuned-models.md) for the full walkthrough, version-policy semantics, and override merge rules.

## Flow 3 — Custom model

Use this when your model's architecture isn't in the catalog. You apply a **base-image AIMModel** first to produce base profiles (generic runtime configs with no model identity), then a custom-model AIMModel that overlays your weights and identity on top.

```yaml
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: aim-base-vllm
  namespace: ml-team
spec:
  image: amdenterpriseai/aim-base:0.11
---
apiVersion: aim.eai.amd.com/v1alpha2
kind: AIMModel
metadata:
  name: acme-custom-transformer
  namespace: ml-team
spec:
  profiles:
    derivedFrom:
      selector:
        role: base
        modelRef:
          name: aim-base-vllm
          scope: Namespace
    versionPolicy: all
    overrides:
      aimId: acme/custom-transformer
      modelId: acme/custom-transformer
      modelSources:
        - modelId: acme/custom-transformer
          sourceUri: s3://acme-models/custom-transformer
```

The shape differs from fine-tunes in two ways:

- `selector.role: base` matches only base profiles; `selector.modelRef.name` pins to a specific base-image AIMModel.
- Target identity for the derived profile lives on `overrides.{aimId,modelId}` (required by CEL when `role: base` — base profiles ship with no identity to inherit).

See [Custom Models](../guides/custom-models.md) for the full walkthrough, the identity-stamping semantics, and base-image requirements.

## Base-image models

A **base-image AIMModel** is just an official-flow AIMModel whose image happens to be a generic AIM base image — one whose profile YAMLs declare runtime/engine/accelerator settings but leave `model_id` and `model_sources` empty. The controller materialises those entries as **base** AIMProfiles:

- `status.deployable: false`
- `aim.eai.amd.com/profile-role: base`
- `aim.eai.amd.com/profile-origin: discovered`

Base profiles cannot back an `AIMService` directly. They exist as source material for a custom-model derivation (see [Flow 3](#flow-3-custom-model)).

A model is a base-image AIMModel when `status.managedProfiles.base > 0` and `status.managedProfiles.deployable == 0` (with `status.kind: Image`). The `kind` is `Image` rather than its own value because — mechanically — a base image is just a regular image whose profile YAMLs happen to leave the model identity fields blank; the image-discovery flow is the same. The `managedProfiles.base > 0` count is what flags the profiles as base-only.

## Discovery cache

For both official and base-image flows, the discovery Job writes its output into a `ConfigMap` that the controller treats as the normalised catalog. The cache stores:

- Parsed profile YAMLs from the image
- Normalised metadata in the same shape that real `AIMProfile` resources use
- The base image extracted from `AIM_BASE_IMAGE_REF` when available

`status.discoveryCacheRef` points at this `ConfigMap`. `status.discovery` tracks the job lifecycle:

| Field | Description |
|---|---|
| `discovery.attempts` | Number of discovery Job attempts so far |
| `discovery.lastAttemptTime` | Timestamp of the most recent attempt |
| `discovery.lastFailureReason` | Reason the most recent attempt failed (e.g. `LogParseFailed`, `ImagePullBackOff`) |
| `discovery.specHash` | Hash of the inputs that invalidate the cache (image, imagePullSecrets, serviceAccountName, discovery command version) |

When `specHash` changes — for example, you rotate `serviceAccountName` or upgrade the image tag — the operator drops the cache and re-runs discovery.

## Profile-set delegation

For derivation flows (fine-tuned and custom), the controller synthesises a child `AIMProfileSet` (or `AIMClusterProfileSet`) named `<model>-profiles-<hash>` and lets it produce the derived profiles. The model summarises the profile-set output back onto its own status; see [AIM Profile Sets](profilesets.md) for the mechanics.

`status.profileSetRef` points at the child resource:

```bash
kubectl get aimmodel acme-custom-transformer \
  -o jsonpath='{.status.profileSetRef.name}'
# acme-custom-transformer-profiles-2be25a47
```

Official-flow models do not create a child profile set — `status.profileSetRef` is empty.

## Spec fields

The unified `spec` shape used by all three flows:

| Field | Used by | Description |
|---|---|---|
| `image` | Official, Base | Source image to inspect for discovery. Mutually exclusive with `derivedFrom`. |
| `derivedFrom` | Fine-tuned, Custom | Derivation spec — selector, version policy, overrides. Mutually exclusive with `image`. Reuses [`AIMProfileSetSpec`](profilesets.md#spec-shape). |
| `imagePullSecrets` | All | Secrets used for image inspection and propagated to derived runtime profiles. |
| `serviceAccountName` | All | Service account propagated to managed child resources. |

The v1alpha2 CRD explicitly forbids the legacy v1alpha1 fields (`spec.aimId`, `spec.modelSources`, `spec.custom`, `spec.customTemplates`, `spec.discovery`, `spec.defaultServiceTemplate`, `spec.runtimeConfigName`, `spec.env`, `spec.imageMetadata`, `spec.profileCopy`) — admission rejects them with a targeted error message.

## Status fields

| Field | Description |
|---|---|
| `status` | Overall status (`Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`, `NotAvailable`) |
| `conditions` | Detailed reconciliation conditions (see [Conditions Reference](../reference/conditions.md)) |
| `kind` | Discriminator for the onboarding flow that produced this model's profiles: `Image` (Flow 1 — discovered from `spec.image`, including base-image AIMs), `Derived` (Flow 2 — fine-tune-style overlay on another deployable model), or `Custom` (Flow 3 — BYO weights overlaid on a base image's base profiles). Populated by the v1alpha2 controller from the spec shape; the deprecated `sourceType` field is a v1alpha1-only two-way variant that conflates Derived and Custom — read `kind` instead. |
| `aimId` | Resolved architecture identifier — populated by discovery for `Image` models, inherited from `profiles.derivedFrom.overrides.aimId` for `Custom`/`Derived` models |
| `baseImage` | Extracted `AIM_BASE_IMAGE_REF` when available |
| `discoveryCacheRef` | Reference to the normalised discovery cache `ConfigMap` (official and base-image flows only) |
| `discoveredProfiles` | Counts: `total`, `supported`, `unsupported`, plus `byHardware[]` listing each discovered hardware footprint with the `{metric, precision}` profiles shipped under it (so unsupported profiles surface explicitly). |
| `discovery` | Discovery Job state: `attempts`, `lastAttemptTime`, `lastFailureReason`, `specHash` |
| `profileSetRef` | Reference to the synthesised child profile set (derivation flows only) |
| `managedProfiles` | Counts: `total`, `ready`, `notAvailable`, `deployable`, `base` |

The `managedProfiles` counts always serialise zero values explicitly (no `omitempty`), so `kubectl` printcolumns and assertions on `.status.managedProfiles.{total,ready,...}` are stable.

## Resolution and precedence

When an `AIMService` references a model by name, AIM Engine resolves it in this order:

1. Namespace-scoped `AIMModel` with that name.
2. Cluster-scoped `AIMClusterModel` with that name.

Namespace wins. The resolved scope is recorded in the service's `status.resolvedModel.scope`.

## Troubleshooting

### Discovery fails

Check operator logs for image-inspection errors:

```bash
kubectl -n aim-system logs -l app.kubernetes.io/name=aim-engine --tail=100 \
  | grep -i "<model-name>"
```

Inspect the discovery Job directly:

```bash
kubectl -n <namespace> get jobs -l aim.eai.amd.com/model=<model-name>
kubectl -n <namespace> logs job/<discovery-job>
```

Common causes:

- Missing or invalid `imagePullSecrets`
- Image reference not found at the registry
- `LogParseFailed` — the container is not an AIM-compatible image
- Registry authentication or connectivity failures

`status.discovery.lastFailureReason` captures the most recent reason, and `status.conditions[?(@.type=='DiscoveryReady')]` carries the human-readable message including the next retry interval.

### Model is `Ready` but no profiles materialised

`status.managedProfiles.total == 0` and `status.discoveredProfiles.unsupported > 0` means discovery succeeded but none of the discovered profiles are compatible with the cluster's hardware. Compare the cluster's accelerator labels:

```bash
kubectl get nodes -o jsonpath='{.items[*].metadata.labels}' \
  | tr ',' '\n' | grep aim-accelerator
```

against the accelerator requirements in the discovery cache `ConfigMap`. Add matching nodes or, if the discovered profiles are wrong, fix the source image.

### Derivation produces no profiles

`status.profileSetRef` points at the child `AIMProfileSet`. Inspect it:

```bash
kubectl get aimprofileset $(kubectl get aimmodel <name> \
  -o jsonpath='{.status.profileSetRef.name}') -o yaml
```

`DerivationReady=False / NoMatchingProfiles` means the selector matched zero source candidates. See the troubleshooting sections in [Fine-Tuned Models](../guides/fine-tuned-models.md#troubleshooting) and [Custom Models](../guides/custom-models.md#troubleshooting).

## Related documentation

- [Fine-Tuned Models](../guides/fine-tuned-models.md) — End-to-end walkthrough for flow 2
- [Custom Models](../guides/custom-models.md) — End-to-end walkthrough for flow 3
- [Profiles](profiles.md) — Base vs deployable, provenance labels, status fields
- [AIM Profile Sets](profilesets.md) — Derivation machinery underneath `spec.profiles`
- [Services](services.md) — How `AIMService` resolves models and profiles
- [CRD API Reference (v1alpha2)](../reference/api/v1alpha2.md) — Full schema
- [Legacy AIMModel (v1alpha1)](../legacy/aimmodel-v1alpha1.md) — The deprecated v1alpha1 shape
