---
name: release
description: Manage the full release lifecycle for aim-engine. Use when the user asks to prepare a release, generate release notes, update the changelog, tag a release candidate or release, promote a release, or publish a release. Covers release prep, changelog generation, tagging, testing, promotion, and publishing.
---

# Release Workflow

This skill covers the full release lifecycle. Detect the current stage and proceed accordingly.

## How a release flows

- Every tag builds into the private `docker.io/silogenai` org. A release (`vX.Y.Z`) is then promoted byte-for-byte to the public `docker.io/amdenterpriseai` org by an external promotion tool, and only published after that.
- A release or candidate starts from the `compile-release` **Run workflow** button on `main` (or `gh workflow run compile-release.yaml --ref main -f version=vX.Y.Z`), or from `make release`. Both run `hack/release/check-version.sh`, which rejects a version that isn't `vX.Y.Z` or `vX.Y.Z-rcN`, an existing tag, and a final version without a `CHANGELOG.md` section. Never create the tag or the release on the Releases page: `compile-release` creates the draft itself, and a release published there goes public before promotion, after which `publish-release` refuses it.
- `compile-release` runs on a tag push, or creates the tag itself when started from the button. A tag created by a workflow starts no other workflows, so a button run also builds and syncs the docs in the same run. It builds the operator, the artifact downloader and both charts into `silogenai` and runs the Kind tests. For `vX.Y.Z` it also creates a draft GitHub release with internal install notes and the assets. Release candidates (`vX.Y.Z-rc1`) and `*int-test` tags get no GitHub release.
- `publish-release` runs from `main` via `workflow_dispatch`, normally started by the promotion tool. It checks the promoted artifacts can be pulled anonymously, then publishes the draft with the version's `CHANGELOG.md` section from `main` as "What's changed", followed by the public install instructions. It then force-pushes the `artifacts` branch.
- `Docs - Sync to central repo` runs on every `v*` tag push, including candidates, and on pushes to `release/**` branches that change `docs/docs/`. For a button run, `compile-release` calls it.

## Stage detection

Check these conditions in order to determine where the user is:

1. **No `release/vX.Y.Z` branch, and `CHANGELOG.md` on `main` has no version section** → Stage 1 (Prep)
2. **On `release/vX.Y.Z`, `.tmp/release-context/` exists, `CHANGELOG.md` has only `[Unreleased]`** → Stage 2 (Generate notes)
3. **On `release/vX.Y.Z`, `CHANGELOG.md` has the version section** → Stage 3 (Review and PR)
4. **On `main`, `CHANGELOG.md` has the version section, no `vX.Y.Z` tag on origin** → Stage 4 (Release candidates, optional) or Stage 5 (Tag the release)
5. **Tag `vX.Y.Z` exists, its GitHub release is missing or a draft** → Stage 6 (Test and promote)
6. **The GitHub release is published** → Stage 7 (Follow-up)

To check:
- Current branch: run `git branch --show-current`
- Release context exists: check for `.tmp/release-context/release-context.md`
- CHANGELOG has version: look for `## [X.Y.Z]` in `CHANGELOG.md`
- Tag exists: run `git ls-remote --tags origin vX.Y.Z`
- Release state: run `gh release view vX.Y.Z --json isDraft,url` (fails if there is no release yet)

## Stage 1: Prep

Prerequisites: on `main`, clean working directory.

If not met, tell the user what to fix.

If the user hasn't specified a version, ask them.

Run:
```
make release-prep VERSION=vX.Y.Z
```

This creates branch `release/vX.Y.Z` and generates `.tmp/release-context/`.

Check the `Base:` line in `release-context.md`: it should be the previous release. The script picks the most recent tag reachable from `main`, which can be a release candidate. If it is, rerun with the base explicitly:
```bash
git checkout main
bash hack/release/prep.sh vX.Y.Z vPREVIOUS
```

Proceed to Stage 2.

## Stage 2: Generate release notes

Read `.tmp/release-context/release-context.md`. Note the version, commit log, and diffstat.

**Read detail diffs selectively** based on the diffstat. The summary table shows which files have content and whether they contain a full diff or a diffstat summary:

- If `api/v1alpha1/` files appear → read `api-types.diff` (full diff)
- If `internal/` files appear → read `internal.diff` (full diff of controller/domain logic)
- If `config/crd/` files appear → read `crds.diff` (full diff -- often large, skim for field changes)
- If `config/helm/` files appear → read `helm.diff` (full diff)
- `docs.diff` and `tests.diff` contain only a diffstat (file list with line counts), not full diffs. Use them to note scope of doc/test changes.
- Skip files marked as empty in the summary table.

**Categorize changes** into:

- **Added** -- new features, new CRDs, new API fields, new controllers
- **Changed** -- behavior changes, renames, refactors, dependency updates
- **Fixed** -- bug fixes, test fixes, stability improvements
- **Removed** -- deleted features, removed fields, dropped support
- **Breaking** -- CRD schema changes requiring migration, removed/renamed API fields, changed defaults

Pay special attention to API type and internal diffs -- API changes affect users upgrading, and internal changes reveal behavioral fixes and feature logic.

**Update `CHANGELOG.md`**: replace the `## [Unreleased]` section with the new version:

```markdown
## [Unreleased]

<!-- Populate this section during release-prep, then rename to the release version -->

## [X.Y.Z] - YYYY-MM-DD

### Added
- Item (with brief context)

### Changed
- Item

### Fixed
- Item
```

Use today's date. Keep entries concise -- one line per change, written for end users of the operator (cluster admins and platform engineers), not internal developers. Omit categories with no entries.

The body of this section is published as the "What's changed" part of the GitHub release, so write it for the release page.

Reference PR numbers where available (e.g. `(#33)`). A bare `#N` links to that PR in `amd-enterprise-ai/aim-engine`, but commits from before the move carry `silogen/aim-engine` PR numbers. Before referencing `(#N)`, confirm with `gh pr view N --repo amd-enterprise-ai/aim-engine` that it is the same change; otherwise leave the number out.

Tell the user to review `CHANGELOG.md` before proceeding.

## Stage 3: Review and PR

The user should review the changelog, then commit and push the release branch.

If asked, help with:
```bash
git add CHANGELOG.md
git commit -m "Release notes for vX.Y.Z"
git push -u origin release/vX.Y.Z
```

Then open a PR to main. After the PR is merged:
```bash
git checkout main
git pull
```

`publish-release` reads `CHANGELOG.md` from `main`, not from the tag, so the section can still be corrected by a PR to `main` until the release is published.

## Stage 4: Release candidates (optional)

Candidates don't need a `CHANGELOG.md` section. Confirm with the user before tagging, then either ask them to use the Run workflow button with `vX.Y.Z-rc1`, or run:

```bash
git checkout main
git pull
make release VERSION=vX.Y.Z-rc1
```

`compile-release` pushes the images and charts to `silogenai` and runs the tests. There is no GitHub release for a candidate.

The chart and the operator binary always reference the public org, so installing a candidate needs overrides. Install the CRDs chart (`oci://registry-1.docker.io/silogenai/aim-engine-crds-chart`, same version) first, and make sure the `dockerhub-regcred` pull secret exists in `aim-system`:

```bash
helm install aim-engine oci://registry-1.docker.io/silogenai/aim-engine-chart \
  --version X.Y.Z-rc1 \
  --namespace aim-system --create-namespace \
  --set manager.image.repository=docker.io/silogenai/aim-engine \
  --set manager.imagePullSecrets[0].name=dockerhub-regcred \
  --set manager.artifactDownloaderImage=docker.io/silogenai/aim-artifact-downloader:vX.Y.Z-rc1
```

Download Jobs run in workload namespaces, so nodes need registry credentials to pull the private artifact downloader.

For fixes found during testing, merge them to `main` (with a `CHANGELOG.md` update), then tag the next candidate (`-rc2`, ...).

## Stage 5: Tag the release

Prerequisites: on `main`, local `main` at `origin/main`, clean working directory, `CHANGELOG.md` has the version section, tag doesn't exist yet.

If not met, tell the user what to fix.

Run:
```
make release VERSION=vX.Y.Z
```

The script validates everything, shows a summary, asks for confirmation (pass `YES=1` to skip for non-interactive/agent use), creates the tag, and pushes only that tag.

Alternatively, the user can start the release from the `compile-release` Run workflow button on `main` with `vX.Y.Z`. It runs the same checks and tags the `main` commit it runs on.

The release tag is always a fresh build, even on the same commit as the last candidate, because the version is baked into the images and charts.

`compile-release` then builds into `silogenai`, runs the tests, and creates the draft GitHub release.

## Stage 6: Test and promote

Follow the build. A pushed tag's run is listed under the tag; a button run is listed under `main`:
```bash
gh run list --workflow compile-release.yaml --branch vX.Y.Z
gh run list --workflow compile-release.yaml --branch main --event workflow_dispatch
gh run watch <run-id>
```

If the build fails after a button run created the tag, re-run the failed jobs (`gh run rerun <run-id> --failed`); a new button run is rejected because the tag exists.

Once the draft exists, the user tests from its internal install notes, which use the same `silogenai` overrides as a candidate. The draft's `install.yaml` references the public image, so it only works after promotion. Tell the user whether the run's tests passed before they promote.

Promotion is done by the user with the external promotion tool, outside GitHub. It copies `aim-engine`, `aim-artifact-downloader`, `aim-engine-chart` and `aim-engine-crds-chart` for the version from `silogenai` to `amdenterpriseai`, then starts `publish-release`.

If the tool didn't start it, and only after the user confirms promotion finished, start it. This publishes the release publicly, so confirm with the user first:
```bash
gh workflow run publish-release -R amd-enterprise-ai/aim-engine --ref main -f version=vX.Y.Z
gh run list --workflow publish-release.yaml
```

Before changing anything public, `publish-release` checks that the tag exists, the release is still a draft, the four artifacts can be pulled anonymously from `amdenterpriseai`, and `main` has the `CHANGELOG.md` section. If one of these fails, fix it and start the workflow again.

If the `artifacts` branch job fails, re-run the failed jobs instead of starting the workflow again; the release is already published, and a new run refuses it:
```bash
gh run rerun <run-id> --failed
```

## Stage 7: Follow-up

- Check `gh release view vX.Y.Z`: it is no longer a draft, and its notes start with "What's changed".
- Check that the `artifacts` branch README names the version.
- The docs sync that ran on the tag push opened or updated a PR in `silogen/AMDEnterpriseAISuiteDocs` from `feature/docs-from-aim-engine-repo` into `develop`. Ask the user to review and merge it. Every tag push updates the same PR, so check it holds the release's docs and not a later candidate's. If the sync failed on `DOCS_REPO_TOKEN`, the secret is missing from this repo; add it and re-run that run.
