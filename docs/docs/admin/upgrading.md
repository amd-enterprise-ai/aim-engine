# Upgrading

This guide covers upgrading AIM Engine to a new version.

## Upgrade Procedure

### 1. Update CRDs First

CRDs must be updated before the operator, as new operator versions may depend on new CRD fields:

```bash
helm upgrade aim-engine-crds oci://docker.io/amdenterpriseai/charts/aim-engine-crds \
  --version <new-version> \
  --namespace aim-system
```

### 2. Upgrade the Operator

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
  --version <new-version> \
  --namespace aim-system \
  --reuse-values
```

Or with updated values:

```bash
helm upgrade aim-engine oci://docker.io/amdenterpriseai/charts/aim-engine \
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

!!! note
    Rolling back the operator does not roll back CRD changes. New CRD fields are additive and backward compatible. If a CRD change is not backward compatible, this will be noted in the release notes.

## Version Compatibility

AIM Engine follows semantic versioning. Within a major version:

- CRD changes are additive (new optional fields)
- Existing resources continue to work without modification
- API group remains `aim.eai.amd.com/v1alpha1`

## Next Steps

- [Installation Reference](installation.md) — Full installation options
- [Changelog](../changelog.md) — Release notes and changes
