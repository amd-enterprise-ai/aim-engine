# Troubleshooting

Common issues and diagnostic steps for AIM Engine.

## Service Status

Check the overall status:

```bash
kubectl get aimservice <name> -n <namespace>
```

For detailed diagnostics, inspect conditions and component health:

```bash
kubectl get aimservice <name> -n <namespace> -o jsonpath='{.status.conditions}' | jq
```

## Common Issues

### Service Stuck in "Pending"

The service is waiting for upstream dependencies.

**Check which conditions are blocking readiness:**

```bash
kubectl get aimservice <name> -n <namespace> -o jsonpath='{.status.conditions}' | jq
```

| Blocked Component | Likely Cause |
|------------------|-------------|
| Profile (v1alpha2) | No profile matched `spec.profile` / `spec.model.name` / `spec.model.image` — verify the profile exists or that the model produces deployable profiles |
| Model | Model not found — check `model.name` spelling or `model.image` accessibility |
| Template (v1alpha1) | No matching template — verify templates exist and are `Ready` |
| RuntimeConfig | Runtime config not found or invalid |

### Service Stuck in "Starting"

Downstream resources are being created but haven't become ready.

**Check the InferenceService:**

```bash
kubectl get inferenceservice -n <namespace> -l aim.eai.amd.com/service.name=<name>
kubectl describe inferenceservice <isvc-name> -n <namespace>
```

**Check pods:**

```bash
kubectl get pods -l serving.kserve.io/inferenceservice=<isvc-name> -n <namespace>
kubectl describe pod <pod-name> -n <namespace>
```

Use the InferenceService name returned by the first command as `<isvc-name>`.

Common causes:

- **Image pull errors** — Wrong image URL or missing imagePullSecrets
- **Insufficient resources** — Not enough GPU, memory, or CPU available
- **PVC not binding** — Storage class doesn't support RWX, or insufficient capacity

### Profile Resolution Fails (v1alpha2)

**`ProfileReady=False` with reason `ProfileNotFound`:**

```bash
# List profiles in the namespace and cluster-scoped
kubectl get aimprofile -n <namespace>
kubectl get aimclusterprofile

# Filter to profiles produced by a specific model
kubectl get aimprofile -n <namespace> \
  -l aim.eai.amd.com/source-model=<model-name> \
  -o custom-columns=NAME:.metadata.name,DEPLOYABLE:.status.deployable,READY:.status.ready
```

Profiles may be filtered out because:

- `status.deployable=false` (base profile — only usable as a derivation source, not as a service target).
- `status.ready=false` (discovery / derivation hasn't completed yet).
- The required accelerator is not present in the cluster.
- `spec.profile.selector` over-narrowed and matched zero profiles.

For `spec.model.image` with the `aim.eai.amd.com/reconciler-pipeline: profile` annotation, the resolver auto-creates a dedicated `AIMModel`; check that the auto-model itself has `deployable > 0`:

```bash
kubectl get aimmodel -n <namespace> -l aim.eai.amd.com/origin=auto-generated
```

### Template Selection Fails (v1alpha1, legacy pipeline)

**"No templates found":**

```bash
# List templates for the model
kubectl get aimservicetemplates -n <namespace>
kubectl get aimclusterservicetemplates

# Check template status
kubectl get aimservicetemplates -o custom-columns=NAME:.metadata.name,STATUS:.status.status
```

Templates may be excluded because:

- Status is not `Ready` (still discovering or failed)
- Status is `NotAvailable` (required GPU not in cluster)
- Profile is `unoptimized` and `allowUnoptimized` is not set

**"Ambiguous selection":**

Multiple templates scored equally. Resolve by specifying `template.name` explicitly.

### Cache or Artifact Failures

```bash
# Check profile cache (v1alpha2)
kubectl get aimprofilecache -n <namespace>

# Or template cache (v1alpha1)
kubectl get aimtemplatecache -n <namespace>

# Check artifacts
kubectl get aimartifact -n <namespace>

# Check download job
kubectl get jobs -l aim.eai.amd.com/artifact=<artifact-name> -n <namespace>
kubectl logs job/<job-name> -n <namespace>
```

Common causes:

- **StorageSizeError** — Model size not yet discovered; typically resolves automatically
- **Download failure** — Network issues, authentication errors, or protocol incompatibility
- **Verification failure** — Files missing or corrupt after download. The downloader automatically retries with a clean state. Check the download job logs for details on which files failed verification
- **PVC binding failure** — Storage class doesn't support `ReadWriteMany`

### Routing Not Working

```bash
# Check HTTPRoute
kubectl get httproute -n <namespace>
kubectl describe httproute <name> -n <namespace>

# Check the gateway
kubectl get gateway -n <gateway-namespace>
```

Common causes:

- Gateway doesn't exist or isn't ready
- `routing.enabled` is not set (check runtime config)
- Gateway namespace mismatch in `gatewayRef`

## Operator Logs

View operator logs for detailed error information:

```bash
kubectl logs -n aim-system deployment/aim-engine-controller-manager -f
```

Filter for errors related to a specific resource:

```bash
kubectl logs -n aim-system deployment/aim-engine-controller-manager | \
  jq 'select(.name == "<resource-name>")'
```

## Status Values

| Status | Meaning |
|--------|---------|
| `Pending` | Waiting for upstream dependencies |
| `Starting` | Creating downstream resources |
| `Progressing` | Resources created, waiting for readiness |
| `Running` | Fully operational |
| `Ready` | Resource is ready (for non-service CRDs) |
| `Degraded` | Partially functional |
| `NotAvailable` | Required infrastructure not present |
| `Failed` | Critical failure |

## Next Steps

- [Monitoring](monitoring.md) — Log format and metrics details
- [CLI and Operator Flags](../reference/cli.md) — Enable debug logging
