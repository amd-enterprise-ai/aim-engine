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

set -eu

URL="${1:?Usage: $0 <hf://org/model or s3://bucket/path>}"
TARGET_DIR="${TARGET_DIR:-/cache}"

# Start progress monitor in background
MONITOR_PID=""
if [ -f /progress-monitor.sh ]; then
    /progress-monitor.sh "$URL" "$TARGET_DIR" &
    MONITOR_PID=$!
fi

# Stop progress monitor (call after download completes, before verification)
stop_progress_monitor() {
    if [ -n "$MONITOR_PID" ]; then
        echo "Download complete, stopping progress monitor (pid=$MONITOR_PID)"
        kill "$MONITOR_PID" 2>/dev/null || true
        wait "$MONITOR_PID" 2>/dev/null || true
        MONITOR_PID=""
    fi

    # Set progress to 100% and mark download as complete
    if [ -n "${ARTIFACT_NAME:-}" ] && [ -n "${ARTIFACT_NAMESPACE:-}" ]; then
        NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        if ! kubectl patch aimartifact "$ARTIFACT_NAME" -n "$ARTIFACT_NAMESPACE" \
            --type=merge --subresource=status \
            -p "{\"status\":{\"progress\":{\"percentage\":100,\"displayPercentage\":\"100 %\",\"downloadedBytes\":${EXPECTED_SIZE_BYTES:-0},\"totalBytes\":${EXPECTED_SIZE_BYTES:-0}}}}"; then
            echo "WARN: Failed to set progress to 100%" >&2
        fi
    fi
}

### TESTING WHEN ENV VARS ARE SET ###
if [ -n "${AIM_DEBUG_CAUSE_HANG:-}" ]; then
    echo "AIM_DEBUG_CAUSE_HANG is set, causing hang"
    python -c "import time; time.sleep(1000000)"
    exit 1
fi

if [ -n "${AIM_DEBUG_CAUSE_FAILURE:-}" ]; then
    echo "AIM_DEBUG_CAUSE_FAILURE is set, causing failure"
    exit 1
fi

if [ -n "${AIM_DEBUG_SIMULATE_DOWNLOAD:-}" ]; then
    echo "AIM_DEBUG_SIMULATE_DOWNLOAD is set, simulating download phases"
    SIM_SIZE=${EXPECTED_SIZE_BYTES:-1048576}
    HALF_SIZE=$((SIM_SIZE / 2))

    # Phase 1: Simulate downloading (~50% of expected size)
    dd if=/dev/zero of="$TARGET_DIR/simulated_data" bs=1 count="$HALF_SIZE" 2>/dev/null
    echo "Simulated download at ~50% (${HALF_SIZE}/${SIM_SIZE} bytes)"
    sleep "${AIM_DEBUG_DOWNLOAD_DURATION:-10}"

    # Phase 2: Simulate download completing (write to full size)
    dd if=/dev/zero of="$TARGET_DIR/simulated_data" bs=1 count="$SIM_SIZE" 2>/dev/null
    echo "Simulated download at 100% (${SIM_SIZE}/${SIM_SIZE} bytes)"

    # Stop progress monitor and set progress to 100%
    stop_progress_monitor

    # Phase 3: Simulate verification
    echo "Simulating verification..."
    sleep "${AIM_DEBUG_VERIFY_DURATION:-10}"

    # Optionally fail verification
    if [ -n "${AIM_DEBUG_VERIFY_FAIL:-}" ]; then
        echo "AIM_DEBUG_VERIFY_FAIL is set, simulating verification failure"
        exit 1
    fi

    echo "Simulated verification complete"
    exit 0
fi
### END TESTING ###

case "$URL" in
    hf://*)
        /hf-download.sh "$URL" "$TARGET_DIR"
        stop_progress_monitor
        /hf-verify.sh "$URL" "$TARGET_DIR"
        ;;
    s3://*)
        echo "Syncing from S3: $URL to $TARGET_DIR"
        
        S3CMD_ARGS=""
        
        # Credentials (support both naming conventions)
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
        s3cmd $S3CMD_ARGS sync --stop-on-error "${URL%/}/" "$TARGET_DIR/"
        stop_progress_monitor
        echo "Sync complete"
        ;;
    *)
        echo "Error: Unknown protocol. URL must start with hf:// or s3:// - was $URL" >&2
        exit 1
        ;;
esac