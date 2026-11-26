#!/bin/sh
# MIT License

# Copyright (c) 2026 Advanced Micro Devices, Inc.

# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:

# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.

# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

# adapter-reap.sh reclaims per-service adapter subtrees on a shared adapter disk
# whose owning AIMService is no longer live, plus crash-orphaned .staging/.aside
# entries and atomically-unloaded bytes under .unload-tmp. Adapter subtrees are
# not Kubernetes objects, so owner-ref GC cannot reclaim them; the aim-engine
# controller periodically launches this reaper Job (the sole RW writer alongside
# staging and subtree-sync Jobs).
#
# Contract (env):
#   ADAPTER_PVC_ROOT  Mount path of the adapter-disk PVC (mounted RW at its root).
#   KEEP_SERVICE_IDS  Comma-separated list of live AIMService subtree IDs to keep.
#   MIN_AGE_SECONDS   Grace period: never reap a subtree modified more recently
#                     than this (defaults to 0 = disabled when unset/invalid).
#
# Safety: the keep-list is a snapshot taken by the controller at reconcile time
# and may be stale by the time this Job's pod actually runs, so a subtree created
# in that gap could be absent from KEEP. The MIN_AGE_SECONDS grace guard is the
# real safety net: it refuses to delete recently-touched subtrees regardless of
# keep-list freshness, so a stale/empty list leaves garbage (reclaimed next pass)
# instead of deleting a live, mounted subtree. The controller sets the Job's
# active deadline well below MIN_AGE_SECONDS, so a subtree created after the
# snapshot is always younger than the grace period when this runs.

set -eu

: "${ADAPTER_PVC_ROOT:?ADAPTER_PVC_ROOT is required}"
KEEP="${KEEP_SERVICE_IDS:-}"

# Parse the grace period; treat missing/non-numeric as 0 (guard disabled).
MIN_AGE_SECONDS="${MIN_AGE_SECONDS:-0}"
case "$MIN_AGE_SECONDS" in
    ''|*[!0-9]*) MIN_AGE_SECONDS=0 ;;
esac
NOW=$(date +%s)

# is_too_young <dir> -> 0 (true) when dir was modified within the grace period.
is_too_young() {
    [ "$MIN_AGE_SECONDS" -gt 0 ] || return 1
    mtime=$(stat -c %Y "$1" 2>/dev/null) || return 1
    age=$((NOW - mtime))
    [ "$age" -lt "$MIN_AGE_SECONDS" ]
}

# is_kept <id> -> 0 when id is in the keep list.
is_kept() {
    id="$1"
    [ -z "$id" ] && return 0
    OLDIFS="$IFS"; IFS=','
    for k in $KEEP; do
        if [ "$k" = "$id" ]; then
            IFS="$OLDIFS"
            return 0
        fi
    done
    IFS="$OLDIFS"
    return 1
}

# Cleanup is retryable garbage collection. A failed removal must not stop later
# entries from being attempted or fail the Job; the next periodic sweep retries.
remove_best_effort() {
    description="$1"
    path="$2"
    echo "$description"
    if ! rm -rf "$path"; then
        echo "Warning: failed to remove ${path}; a later reaper will retry" >&2
    fi
}

echo "Reaping adapter disk at ${ADAPTER_PVC_ROOT}; keep=[${KEEP}]"

# Reap live-tree service subtrees: any top-level dir that is not a control dir
# and whose id is not in the keep set.
for dir in "$ADAPTER_PVC_ROOT"/*; do
    [ -e "$dir" ] || continue
    [ -d "$dir" ] || continue
    base=$(basename "$dir")
    case "$base" in
        .staging|.aside|.unload-tmp|lost+found) continue ;;
    esac
    if is_kept "$base"; then
        continue
    fi
    if is_too_young "$dir"; then
        echo "Skipping recently-modified subtree (grace guard): ${base}"
        continue
    fi
    remove_best_effort "Reaping stale service subtree: ${base}" "$dir"
done

# Reap crash-orphaned staging/aside subtrees for dead services.
for ctrl in .staging .aside; do
    cdir="${ADAPTER_PVC_ROOT}/${ctrl}"
    [ -d "$cdir" ] || continue
    for dir in "$cdir"/*; do
        [ -e "$dir" ] || continue
        base=$(basename "$dir")
        if is_kept "$base"; then
            continue
        fi
        if is_too_young "$dir"; then
            echo "Skipping recently-modified ${ctrl} subtree (grace guard): ${base}"
            continue
        fi
        remove_best_effort "Reaping orphaned ${ctrl} subtree: ${base}" "$dir"
    done
done

# Reap atomically-unloaded adapter slots last. Unlike .staging/.aside, every
# entry here is garbage even when its service is still live. Apply the age guard
# per slot so a stale reaper snapshot cannot race a subtree-sync move.
unload_root="${ADAPTER_PVC_ROOT}/.unload-tmp"
if [ -d "$unload_root" ]; then
    for service_dir in "$unload_root"/*; do
        [ -d "$service_dir" ] || continue
        for slot in "$service_dir"/*; do
            [ -e "$slot" ] || continue
            if is_too_young "$slot"; then
                echo "Skipping recently-unloaded adapter (grace guard): ${slot}"
                continue
            fi
            remove_best_effort "Reaping unloaded adapter bytes: ${slot}" "$slot"
        done
        rmdir "$service_dir" 2>/dev/null || true
    done
    rmdir "$unload_root" 2>/dev/null || true
fi

echo "Reap complete"
