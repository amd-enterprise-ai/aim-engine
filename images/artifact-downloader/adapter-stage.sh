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

# adapter-stage.sh stages a single LoRA adapter into a per-AIMService subtree of a
# shared adapter-disk PVC, then atomically promotes it into the live tree via
# rename(2). The aim-engine controller orchestrates this Job; the Job is the sole
# logical writer to the live tree (the controller never mounts the PVC).
#
# Contract (env):
#   ADAPTER_PVC_ROOT  Mount path of the adapter-disk PVC (mounted RW at its root).
#   SERVICE_ID        Per-service subtree directory name (encodes the AIMService UID).
#   ADAPTER_PATH      On-disk directory name for this adapter.
#   JOB_ID            Unique suffix for the staging directory (collision/crash safe).
#   $1                Source URI (hf:// or s3://).
#
# Staging/aside areas live at the PVC ROOT (outside the pod subPath mount) so
# consumers never observe in-progress bytes, while staying on the same filesystem
# so rename stays atomic.

set -eu

URL="${1:?Usage: $0 <hf://org/model or s3://bucket/path>}"

: "${ADAPTER_PVC_ROOT:?ADAPTER_PVC_ROOT is required}"
: "${SERVICE_ID:?SERVICE_ID is required}"
: "${ADAPTER_PATH:?ADAPTER_PATH is required}"
JOB_ID="${JOB_ID:-$$}"

STAGING_DIR="${ADAPTER_PVC_ROOT}/.staging/${SERVICE_ID}/${ADAPTER_PATH}.${JOB_ID}"
ASIDE_DIR="${ADAPTER_PVC_ROOT}/.aside/${SERVICE_ID}/${ADAPTER_PATH}.${JOB_ID}"
SERVICE_ROOT="${ADAPTER_PVC_ROOT}/${SERVICE_ID}"
LIVE_DIR="${SERVICE_ROOT}/${ADAPTER_PATH}"

cleanup_staging() {
    rm -rf "$STAGING_DIR" 2>/dev/null || true
}
trap cleanup_staging EXIT

echo "Staging adapter '${ADAPTER_PATH}' for service '${SERVICE_ID}' from ${URL}"
echo "  staging dir: ${STAGING_DIR}"
echo "  live dir:    ${LIVE_DIR}"

rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR"

download_into_staging() {
    target="$1"

    if [ -n "${AIM_DEBUG_SIMULATE_PROMOTE:-}" ]; then
        echo "AIM_DEBUG_SIMULATE_PROMOTE set: writing dummy adapter into staging"
        printf '{"base_model_name_or_path":"%s","r":%s}\n' \
            "${ADAPTER_BASE_MODEL_ID:-unknown}" "${ADAPTER_RANK:-16}" \
            > "${target}/adapter_config.json"
        dd if=/dev/zero of="${target}/adapter_model.safetensors" bs=1024 count=8 2>/dev/null
        sleep "${AIM_DEBUG_DOWNLOAD_DURATION:-2}"
        return 0
    fi

    case "$URL" in
        hf://*)
            export MODEL_PATH="${URL#hf://}"
            /hf-download.sh "$URL" "$target"
            ;;
        s3://*)
            echo "Syncing from S3: $URL to $target"
            S3CMD_ARGS=""
            [ -n "${AWS_ACCESS_KEY_ID:-}" ] && S3CMD_ARGS="$S3CMD_ARGS --access_key=$AWS_ACCESS_KEY_ID"
            [ -n "${AWS_SECRET_ACCESS_KEY:-}" ] && S3CMD_ARGS="$S3CMD_ARGS --secret_key=$AWS_SECRET_ACCESS_KEY"
            [ "${S3_NO_SSL:-}" = "true" ] && S3CMD_ARGS="$S3CMD_ARGS --no-ssl"
            if [ -n "${AWS_ENDPOINT_URL:-}" ]; then
                S3_HOST=$(echo "$AWS_ENDPOINT_URL" | sed 's|^https\?://||')
                S3CMD_ARGS="$S3CMD_ARGS --host=$S3_HOST --host-bucket= --signature-v2"
                case "$AWS_ENDPOINT_URL" in
                    http://*) S3CMD_ARGS="$S3CMD_ARGS --no-ssl" ;;
                esac
            fi
            # shellcheck disable=SC2086
            s3cmd $S3CMD_ARGS sync --stop-on-error "${URL%/}/" "${target}/"
            ;;
        *)
            echo "Error: Unknown protocol. URL must start with hf:// or s3:// - was $URL" >&2
            exit 1
            ;;
    esac
}

# TARGET_DIR is consumed by hf-download.sh's verification helpers.
export TARGET_DIR="$STAGING_DIR"
download_into_staging "$STAGING_DIR"

# Reject symlinks: subPath + symlinks have a CVE history.
if find "$STAGING_DIR" -type l | grep -q .; then
    echo "Error: staged adapter contains symlinks, refusing to promote" >&2
    exit 1
fi
chmod -R u+rwX,go+rX "$STAGING_DIR" 2>/dev/null || true

# Create the service root BEFORE the live dir so pods can mount subPath:<service-id>.
# The root is a real directory and is never renamed/recreated while mounted.
mkdir -p "$SERVICE_ROOT"

# Promote atomically. rename(2) cannot replace a populated directory, so for an
# idempotent re-stage we move any existing live dir aside first (brief gap; MVP
# static services do not normally hit this path).
if [ -e "$LIVE_DIR" ]; then
    echo "Live dir already exists; moving aside before promote"
    mkdir -p "$(dirname "$ASIDE_DIR")"
    mv "$LIVE_DIR" "$ASIDE_DIR"
    rm -rf "$ASIDE_DIR" 2>/dev/null || true
fi

mv "$STAGING_DIR" "$LIVE_DIR"
echo "Promoted adapter '${ADAPTER_PATH}' into ${LIVE_DIR}"
