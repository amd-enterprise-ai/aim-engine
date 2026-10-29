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

A release (`vX.Y.Z`) or release candidate (`vX.Y.Z-rcN`) starts in one of two
ways:

- **Run workflow** on the `compile-release` workflow's Actions page, run from
  `main`, with the version. The run tags the `main` commit it runs on, then
  builds the version and syncs the docs itself, because a tag created by a
  workflow doesn't start other workflows.
- `make release VERSION=vX.Y.Z` from an up-to-date `main`, which tags `HEAD`
  and pushes the tag.

Both reject a version in any other format, a tag that already exists, and a
final version without a `CHANGELOG.md` section
(`hack/release/check-version.sh`). Pushing a tag by hand still works, and is
how `*int-test` tags are created. If a build fails after tagging, use **Re-run
failed jobs** on that run: the tag already exists, so a new run is rejected.

The `compile-release` workflow builds every `v*` and `*int-test` tag:

- regenerates CRDs, then runs `make test-bridge-contract` before packaging or
  publishing Helm/CRD artifacts
- for `vX.Y.Z` tags, attaches `install.yaml`, `crds.yaml`, the Helm chart
  tarball, and the CRDs chart tarball to a draft GitHub Release
- pushes the `aim-engine-chart` and `aim-engine-crds-chart` charts to an OCI
  registry

The bridge contract is static: it checks API markers and generated CRDs,
controller roots, owner-reference semantics, RBAC, and release ordering. It
does not qualify a migration, artifact preservation, GPU serving, or inference.

Once a release has been tested and promoted to `docker.io/amdenterpriseai`,
the `publish-release` workflow publishes it:

```bash
gh workflow run publish-release --ref main -f version=vX.Y.Z
```

The workflow:

- checks that the operator and artifact-downloader images and both charts can
  be pulled anonymously from `docker.io/amdenterpriseai`, before changing
  anything public
- replaces the draft's internal notes with the version's `CHANGELOG.md` section
  (read from `main`) and the public install instructions, then publishes the
  release
- force-pushes the released chart and CRDs to the `artifacts` branch as a
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
