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

# adapter-subtree-sync.sh reconciles a single AIMService's per-service adapter
# subtree on the shared adapter disk toward the service's declared adapter set.
# It is owned by the AIMService (so it is garbage-collected with the service) and
# does two things:
#   1. Creates the per-service subtree directory if missing, so the
#      InferenceService can mount it read-only (subPath) before any adapter has
#      downloaded — the aim-runtime errors on a missing subPath at startup.
#   2. Removes (unloads) any adapter directory in the subtree that is no longer
#      in the declared keep-list, so removing an entry from spec.adapters
#      reclaims its bytes. In dynamic mode the in-pod watcher then unloads it
#      (the disk is ground truth). It never touches other services'
#      subtrees or the PVC-root control dirs (.staging/.aside) — whole-subtree
#      reclaim for deleted services is the model-artifact-owned reaper's job.
#
# Contract (env):
#   ADAPTER_PVC_ROOT     Mount path of the adapter-disk PVC (mounted RW at root).
#   SERVICE_ID           Per-service subtree directory name (the AIMService UID).
#   KEEP_ADAPTER_PATHS   Comma-separated adapter directory names to keep.

set -eu

: "${ADAPTER_PVC_ROOT:?ADAPTER_PVC_ROOT is required}"
: "${SERVICE_ID:?SERVICE_ID is required}"
KEEP="${KEEP_ADAPTER_PATHS:-}"

SERVICE_ROOT="${ADAPTER_PVC_ROOT}/${SERVICE_ID}"

# is_kept <name> -> 0 when name is in the keep list.
is_kept() {
    name="$1"
    [ -z "$name" ] && return 1
    OLDIFS="$IFS"; IFS=','
    for k in $KEEP; do
        if [ "$k" = "$name" ]; then
            IFS="$OLDIFS"
            return 0
        fi
    done
    IFS="$OLDIFS"
    return 1
}

mkdir -p "$SERVICE_ROOT"
echo "Syncing adapter subtree ${SERVICE_ROOT}; keep=[${KEEP}]"

for dir in "$SERVICE_ROOT"/*; do
    [ -e "$dir" ] || continue
    [ -d "$dir" ] || continue
    base=$(basename "$dir")
    if is_kept "$base"; then
        continue
    fi
    echo "Unloading adapter directory no longer declared: ${base}"
    rm -rf "$dir"
done

echo "Subtree sync complete for ${SERVICE_ROOT}"
