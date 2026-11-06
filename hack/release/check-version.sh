#!/usr/bin/env bash
# Check that a version can be tagged for release. Shared by release.sh
# (make release) and the compile-release Run workflow button.
#
# Usage:
#   ./hack/release/check-version.sh <version>
#
# Rejects:
#   - a version that isn't vX.Y.Z or vX.Y.Z-rcN
#   - a tag that already exists on origin or locally
#   - a final version (vX.Y.Z) without a CHANGELOG.md section with entries
set -euo pipefail

VERSION="${1:-}"

error() {
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
        echo "::error::$*"
    else
        echo "ERROR: $*" >&2
    fi
}

# Checked first, so the messages below only include a well-formed version.
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]]; then
    error "Version must be vX.Y.Z or vX.Y.Z-rcN (e.g. v0.2.0, v0.2.0-rc1)."
    exit 1
fi

failed=false

# Exit code 2 means no matching tag; anything else non-zero means the lookup failed.
status=0
git ls-remote --exit-code --tags origin "refs/tags/$VERSION" >/dev/null || status=$?
case "$status" in
    0)
        error "Tag $VERSION already exists on origin."
        failed=true
        ;;
    2)
        if git rev-parse --quiet --verify "refs/tags/$VERSION" >/dev/null; then
            error "Tag $VERSION exists locally but not on origin. Delete it with 'git tag -d $VERSION'."
            failed=true
        fi
        ;;
    *)
        error "Could not list tags on origin."
        failed=true
        ;;
esac

if [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    heading="## [${VERSION#v}]"
    # Same section boundaries publish-release uses for the release notes.
    if ! awk -v heading="$heading" '
        index($0, "## [") == 1 { if (found) exit; found = (index($0, heading) == 1); next }
        found && NF { entries = 1 }
        END { exit !entries }
    ' CHANGELOG.md; then
        error "CHANGELOG.md has no '$heading' section with entries."
        failed=true
    fi
fi

if [[ "$failed" == true ]]; then
    exit 1
fi
