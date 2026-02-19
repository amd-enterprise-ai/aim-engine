#!/usr/bin/env bash
# Generates release context and creates a release branch for changelog preparation.
#
# Usage:
#   ./hack/release/prep.sh <version> [base-ref]
#
# Example:
#   ./hack/release/prep.sh v0.2.0
#   ./hack/release/prep.sh v0.2.0 v0.1.0   # explicit base
#
# Creates branch release/<version>, generates .tmp/release-context/ with:
#   release-context.md  - summary (commit log, diffstat, file references)
#   api-types.diff      - API type changes (api/v1alpha1/*_types.go)
#   internal.diff       - Controller and domain logic changes (internal/)
#   crds.diff           - CRD schema changes (config/crd/)
#   helm.diff           - Helm values/template changes (config/helm/)
#   workflows.diff      - CI workflow changes (.github/workflows/)
#   docs.diff           - Documentation changes (docs/) - diffstat only
#   tests.diff          - Test changes (tests/) - diffstat only
#
# base-ref defaults to the most recent git tag (i.e. the previous release).
set -euo pipefail

VERSION="${1:?Usage: $0 <version> [base-ref]}"
shift

BASE="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo "")}"
BRANCH="release/${VERSION}"

OUT_DIR=".tmp/release-context"

# --- Validation ---

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    echo "ERROR: Version must be semver with 'v' prefix (e.g. v0.2.0)"
    exit 1
fi

if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "ERROR: Tag $VERSION already exists"
    exit 1
fi

if [[ -z "$BASE" ]]; then
    echo "ERROR: No previous tag found and no base-ref provided."
    echo "For the first release, pass the base commit explicitly:"
    echo "  $0 $VERSION <commit-sha>"
    exit 1
fi

if ! git rev-parse --verify "$BASE" >/dev/null 2>&1; then
    echo "ERROR: base ref '$BASE' not found."
    exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" != "main" ]]; then
    echo "ERROR: Must be on main to create a release branch (currently on $CURRENT_BRANCH)"
    exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
    echo "ERROR: Working directory is not clean. Commit or stash changes first."
    exit 1
fi

# --- Create release branch ---

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

if git show-ref --verify --quiet "refs/heads/$BRANCH" 2>/dev/null; then
    echo "Branch $BRANCH already exists. Switching to it."
    git checkout "$BRANCH"
else
    echo "Creating branch $BRANCH..."
    git checkout -b "$BRANCH"
fi

# --- Generate detail diffs ---

RANGE="${BASE}..HEAD"

git diff "$RANGE" -- 'api/v1alpha1/*_types.go' > "$OUT_DIR/api-types.diff" 2>/dev/null || true
git diff "$RANGE" -- internal/                  > "$OUT_DIR/internal.diff" 2>/dev/null || true
git diff "$RANGE" -- config/crd/                > "$OUT_DIR/crds.diff" 2>/dev/null || true
git diff "$RANGE" -- config/helm/               > "$OUT_DIR/helm.diff" 2>/dev/null || true
git diff "$RANGE" -- .github/workflows/         > "$OUT_DIR/workflows.diff" 2>/dev/null || true
git diff --stat "$RANGE" -- docs/               > "$OUT_DIR/docs.diff" 2>/dev/null || true
git diff --stat "$RANGE" -- tests/              > "$OUT_DIR/tests.diff" 2>/dev/null || true

# --- Generate summary ---

COMMIT_COUNT=$(git rev-list --count --no-merges "$RANGE")

non_empty() { [[ -s "$OUT_DIR/$1" ]] && echo "yes" || echo "no"; }

cat > "$OUT_DIR/release-context.md" <<EOF
# Release Context

Generated: $(date -u +"%Y-%m-%d %H:%M UTC")
Version: ${VERSION}
Base: ${BASE} ($(git rev-parse --short "$BASE"))
Head: $(git rev-parse --short HEAD)
Commits: ${COMMIT_COUNT} (excluding merges)

## Commit log

\`\`\`
$(git log --oneline --no-merges "$RANGE")
\`\`\`

## Diffstat

\`\`\`
$(git diff --stat "$RANGE")
\`\`\`

## Detail diffs

Read these files as needed based on the diffstat above.
Only read a file if the diffstat shows relevant changes in that area.

| File | Has changes | Content | Description |
|------|-------------|---------|-------------|
| \`api-types.diff\` | $(non_empty api-types.diff) | full diff | API type changes (api/v1alpha1/*_types.go) |
| \`internal.diff\` | $(non_empty internal.diff) | full diff | Controller and domain logic (internal/) |
| \`crds.diff\` | $(non_empty crds.diff) | full diff | CRD schema changes (config/crd/) |
| \`helm.diff\` | $(non_empty helm.diff) | full diff | Helm values and templates (config/helm/) |
| \`workflows.diff\` | $(non_empty workflows.diff) | full diff | CI workflow changes (.github/workflows/) |
| \`docs.diff\` | $(non_empty docs.diff) | diffstat | Documentation changes (docs/) |
| \`tests.diff\` | $(non_empty tests.diff) | diffstat | Test changes (tests/) |

All files are in \`.tmp/release-context/\`.
EOF

echo ""
echo "Release context written to ${OUT_DIR}/"
echo "  ${COMMIT_COUNT} commits since ${BASE}"
echo "  Now on branch: ${BRANCH}"
echo ""
echo "  release-context.md  $(wc -l < "$OUT_DIR/release-context.md") lines (summary)"
for f in api-types internal crds helm workflows docs tests; do
    if [[ -s "$OUT_DIR/${f}.diff" ]]; then
        echo "  ${f}.diff$(printf '%*s' $((18 - ${#f})) '')$(wc -l < "$OUT_DIR/${f}.diff") lines"
    fi
done
echo ""
echo "Next: ask any AI agent to generate release notes."
echo "  Instructions: hack/release/SKILL.md"
echo "  Context:      ${OUT_DIR}/release-context.md"
echo ""
echo "Examples:"
echo "  Cursor:     \"Generate release notes for ${VERSION}\""
echo "  Claude CLI: claude \"Follow hack/release/SKILL.md to generate release notes for ${VERSION}\""
echo ""
echo "Then: review CHANGELOG.md, commit, push, PR ${BRANCH} → main"
echo "After merge: git checkout main && make release VERSION=${VERSION}"
