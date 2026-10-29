#!/usr/bin/env bash
# Execute a release: validate, tag, and push.
#
# Usage:
#   ./hack/release/release.sh <version> [--yes]
#
# Example:
#   ./hack/release/release.sh v0.2.0
#   ./hack/release/release.sh v0.2.0-rc1
#   ./hack/release/release.sh v0.2.0 --yes   # skip confirmation (for agent/CI use)
#
# Prerequisites:
#   - CHANGELOG.md updated with the new version section (final versions only)
#   - Working directory is clean (all changes committed)
#   - Local main is at origin/main
#
# The version checks are shared with the compile-release Run workflow button
# (check-version.sh).
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

CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" != "main" ]]; then
    echo "ERROR: Must be on main to release (currently on $CURRENT_BRANCH)."
    echo "Merge your release branch first, then run this from main."
    exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
    echo "ERROR: Working directory is not clean. Commit or stash changes first."
    exit 1
fi

git fetch --quiet origin main
if [[ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]]; then
    echo "ERROR: Local main is not at origin/main."
    echo "Run 'git pull', and land any local commits through a PR first."
    exit 1
fi

bash "$(dirname "$0")/check-version.sh" "$VERSION"

IS_RELEASE=false
if [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    IS_RELEASE=true
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
echo "  2. Push tag $VERSION to origin"
echo ""
echo "compile-release will then:"
echo "  - Build and push images and Helm charts to docker.io/silogenai"
echo "  - Run the e2e tests"
if [[ "$IS_RELEASE" == true ]]; then
    echo "  - Create a draft GitHub Release, published by publish-release after promotion"
fi
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

echo ">>> Pushing tag to origin..."
git push origin "$VERSION"

echo ""
echo "=== Tag $VERSION pushed ==="
echo "  compile-release is now running."
if [[ "$IS_RELEASE" == true ]]; then
    echo "  It creates a draft GitHub Release. After the release is promoted to"
    echo "  docker.io/amdenterpriseai, publish-release publishes it."
fi
