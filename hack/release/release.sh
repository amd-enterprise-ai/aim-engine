#!/usr/bin/env bash
# Execute a release: validate, tag, and push.
#
# Usage:
#   ./hack/release/release.sh <version> [--yes]
#
# Example:
#   ./hack/release/release.sh v0.2.0
#   ./hack/release/release.sh v0.2.0 --yes   # skip confirmation (for agent/CI use)
#
# Prerequisites:
#   - CHANGELOG.md updated with the new version section
#   - Working directory is clean (all changes committed)
set -euo pipefail

VERSION=""
YES=false

for arg in "$@"; do
    case "$arg" in
        --yes) YES=true ;;
        -*) echo "ERROR: Unknown flag: $arg"; exit 1 ;;
        *) VERSION="$arg" ;;
    esac
done

if [[ -z "$VERSION" ]]; then
    echo "Usage: $0 <version> [--yes]"
    exit 1
fi

# --- Validation ---

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    echo "ERROR: Version must be semver with 'v' prefix (e.g. v0.2.0, v1.0.0-rc1)"
    exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" != "main" ]]; then
    echo "ERROR: Must be on main to release (currently on $CURRENT_BRANCH)."
    echo "Merge your release branch first, then run this from main."
    exit 1
fi

if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "ERROR: Tag $VERSION already exists"
    exit 1
fi

if ! grep -q "## \[${VERSION#v}\]" CHANGELOG.md 2>/dev/null; then
    echo "ERROR: CHANGELOG.md does not contain a section for [${VERSION#v}]."
    echo "Update CHANGELOG.md before releasing."
    exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
    echo "ERROR: Working directory is not clean. Commit or stash changes first."
    exit 1
fi

# --- Confirmation ---

PREV_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
COMMIT_COUNT=$(git rev-list --count --no-merges "${PREV_TAG:+${PREV_TAG}..}HEAD")

echo "=== Release Summary ==="
echo "  Version:     $VERSION"
echo "  Previous:    ${PREV_TAG:-"(first release)"}"
echo "  Commits:     $COMMIT_COUNT (since ${PREV_TAG:-"beginning"})"
echo ""
echo "This will:"
echo "  1. Create tag $VERSION on current HEAD"
echo "  2. Push main and tag to origin"
echo ""
echo "Tag-triggered workflows will then:"
echo "  - Run unit and e2e tests"
echo "  - Build and push images and Helm charts"
echo "  - Create a GitHub Release from CHANGELOG.md"
echo ""

if [[ "$YES" != true ]]; then
    read -rp "Proceed? [y/N] " confirm
    if [[ "$confirm" != [yY] ]]; then
        echo "Aborted."
        exit 0
    fi
fi

# --- Execute ---

echo ""
echo ">>> Creating tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION"

echo ">>> Pushing to origin..."
git push origin main --tags

echo ""
echo "=== Release $VERSION pushed ==="
echo "  Tag-triggered workflows are now running."
echo "  Once CI passes, the GitHub Release will be created automatically."
