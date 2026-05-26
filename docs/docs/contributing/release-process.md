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

Tagged releases are produced by the `compile-release` workflow on every `v*`
tag. The workflow:

- attaches `install.yaml`, `crds.yaml`, the Helm chart tarball, and the CRDs
  chart tarball to a draft GitHub Release
- pushes the `aim-engine-chart` and `aim-engine-crds-chart` charts to an OCI
  registry
- force-pushes the rendered chart and CRDs to the `artifacts` branch as a
  browseable mirror

Between releases, the `publish-main` workflow snapshots `main` on every commit
(when `vars.ACTIVATE_PUBLISH_MAIN_WORKFLOW == 'true'`). It pushes a chart
versioned `0.0.0-publish-main.<sha>` to the same OCI registry under the
`aim-engine-chart` name and force-pushes the rendered chart and CRDs to the
`publish-main` branch. This is for previewing `main` against consumer
integrations between releases — it is not a release channel.

The `publish-main` chart references a private DockerHub image and bakes
`manager.imagePullSecrets = [{name: dockerhub-regcred}]` into `values.yaml`.
Consumers must provision the secret themselves (the branch README has the details).

### Pushing charts locally

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
