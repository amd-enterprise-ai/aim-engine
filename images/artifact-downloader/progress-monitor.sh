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

# Progress monitor - updates AIMArtifact status with download progress

terminated=false
trap 'terminated=true' TERM

URL="${1:?Usage: $0 <hf://org/model or s3://bucket/path>}"
TARGET_DIR="${2:?Usage: $0 <target directory>}"

if [ -z "${EXPECTED_SIZE_BYTES:-}" ]; then
    case "$URL" in
        hf://*)
            # Fetch expected size if not set
            echo "Fetching model size from Hugging Face..."
            export MODEL_PATH="${URL#hf://}"
            EXPECTED_SIZE_BYTES=$(python /check-size/check-hf-size.py 2>/dev/null || echo 0)
            ;;
        s3://*)
            # Get size from S3 (s3cmd du returns human-readable, need bytes)
            EXPECTED_SIZE_BYTES=$(s3cmd du "$URL" 2>/dev/null | awk '{print $1}' || echo 0)
            ;;
    esac
    export EXPECTED_SIZE_BYTES
fi

echo "Expected size: $EXPECTED_SIZE_BYTES bytes"

expected_size=${EXPECTED_SIZE_BYTES:-0}
log_interval=${PROGRESS_INTERVAL:-5}
stall_timeout=${STALL_TIMEOUT:-120}

last_size=0
last_change_time=$(date +%s)

update_progress() {
    [ -z "${ARTIFACT_NAME:-}" ] && echo "WARN: ARTIFACT_NAME not set" >&2 && return
    [ -z "${ARTIFACT_NAMESPACE:-}" ] && echo "WARN: ARTIFACT_NAMESPACE not set" >&2 && return
    
    if ! kubectl patch aimartifact "$ARTIFACT_NAME" -n "$ARTIFACT_NAMESPACE" \
        --type=merge --subresource=status \
        -p "{\"status\":{\"progress\":{\"percentage\":$1,\"displayPercentage\":\"$1 %\",\"downloadedBytes\":$2,\"totalBytes\":$3}}}"; then
        echo "WARN: Failed to update progress: $1% ($2/$3 bytes)" >&2
    fi
}

echo "Progress monitor started: expected=${expected_size} bytes, interval=${log_interval}s, stall_timeout=${stall_timeout}s" >&2

while [ "$terminated" = "false" ]; do
    current_size=$(du -sb "$TARGET_DIR" 2>/dev/null | cut -f1 || echo 0)
    now=$(date +%s)

    # Stall detection
    if [ "$current_size" -gt "$last_size" ]; then
        last_size=$current_size
        last_change_time=$now
    elif [ $((now - last_change_time)) -ge "$stall_timeout" ]; then
        echo "Download stalled for ${stall_timeout}s, killing" >&2
        pkill -9 -f "python|s3cmd" 2>/dev/null || true
        exit 1
    fi

    # Calculate and update progress
    if [ "$expected_size" -gt 0 ] && [ "$current_size" -gt 0 ]; then
        percent=$((current_size * 100 / expected_size))
        [ "$percent" -gt 99 ] && percent=99 # We only set 100% after the download is complete
    else
        percent=0
    fi
    update_progress "$percent" "$current_size" "$expected_size"
    echo "Progress: ${percent}% (${current_size}/${expected_size} bytes)" >&2
    sleep "$log_interval"
done

# Log termination for debugging
current_size=$(du -sb "$TARGET_DIR" 2>/dev/null | cut -f1 || echo 0)
echo "Progress monitor terminated: final_size=${current_size} bytes" >&2