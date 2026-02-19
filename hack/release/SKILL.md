---
name: release
description: Manage the full release lifecycle for aim-engine. Use when the user asks to prepare a release, generate release notes, update the changelog, tag a release, or push a release. Covers release prep, changelog generation, and final tagging.
---

# Release Workflow

This skill covers the full release lifecycle. Detect the current stage and proceed accordingly.

## Stage detection

Check these conditions in order to determine where the user is:

1. **No release branch exists** → Stage 1 (Prep)
2. **On a `release/*` branch, `.tmp/release-context/` exists, `CHANGELOG.md` has only `[Unreleased]`** → Stage 2 (Generate notes)
3. **On a `release/*` branch, `CHANGELOG.md` has the version section** → Stage 3 (Review and PR)
4. **On `main`, `CHANGELOG.md` has the version section, no tag exists** → Stage 4 (Tag and push)
5. **Tag already exists** → Done

To check:
- Current branch: run `git branch --show-current`
- Release context exists: check for `.tmp/release-context/release-context.md`
- CHANGELOG has version: look for `## [X.Y.Z]` in `CHANGELOG.md`
- Tag exists: run `git tag -l vX.Y.Z`

## Stage 1: Prep

Prerequisites: on `main`, clean working directory.

If not met, tell the user what to fix.

If the user hasn't specified a version, ask them.

Run:
```
make release-prep VERSION=vX.Y.Z
```

This creates branch `release/vX.Y.Z` and generates `.tmp/release-context/`.

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

Use today's date. Keep entries concise -- one line per change, written for end users of the operator (cluster admins and platform engineers), not internal developers. Reference PR numbers where available (e.g. `(#33)`). Omit categories with no entries.

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

## Stage 4: Tag and push

Prerequisites: on `main`, clean working directory, `CHANGELOG.md` has the version section, tag doesn't exist yet.

If not met, tell the user what to fix.

Run:
```
make release VERSION=vX.Y.Z
```

The script validates everything, shows a summary, asks for confirmation (pass `--yes` to skip for non-interactive/agent use), creates the tag, and pushes. Tag-triggered CI then runs tests, builds artifacts, and creates the GitHub Release from CHANGELOG.md.
