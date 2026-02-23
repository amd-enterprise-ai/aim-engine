# Release Process

Overview of the AIM Engine release workflow.

## Versioning

AIM Engine follows [semantic versioning](https://semver.org/). The version is derived from git tags:

```bash
git describe --tags --abbrev=0  # e.g., v0.8.5
```

## Build Artifacts

### CRDs

Generate a consolidated CRDs file:

```bash
make crds
# Output: dist/crds.yaml
```

### Helm Chart

Generate the Helm chart from kustomize output:

```bash
make helm
# Output: dist/chart/
```

Package for distribution:

```bash
make helm-package
# Output: dist/aim-engine-<version>.tgz
```

### Container Image

```bash
make docker-build IMG=docker.io/amdenterpriseai/aim-engine:v0.8.5
make docker-push IMG=docker.io/amdenterpriseai/aim-engine:v0.8.5
```

## Distribution

The `publish-main` branch contains pre-built release artifacts:

- `crds.yaml` — Consolidated CRDs
- `chart/` — Helm chart ready for `helm install`

### OCI Registry

Push Helm charts to an OCI registry:

```bash
make helm-push-oci
```

## Third-Party Licenses

Generate license information for all dependencies:

```bash
make generate-licenses
# Output: third-party-licenses/
```

## Documentation

Regenerate all documentation:

```bash
make generate-docs  # CRD API reference + Helm values reference
```

## Next Steps

- [Development Setup](development-setup.md) — Build from source
- [Changelog](../changelog.md) — Release notes
