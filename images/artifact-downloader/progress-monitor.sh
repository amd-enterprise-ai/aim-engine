#!/bin/sh
# Progress monitor - updates AIMModelCache status with download progress

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
    [ -z "${CACHE_NAME:-}" ] && return
    [ -z "${CACHE_NAMESPACE:-}" ] && return
    kubectl patch aimmodelcache "$CACHE_NAME" -n "$CACHE_NAMESPACE" \
        --type=merge --subresource=status \
        -p "{\"status\":{\"progress\":{\"percentage\":$1,\"displayPercentage\":\"$1 %\",\"downloadedBytes\":$2,\"totalBytes\":$3}}}" \
        2>/dev/null || true
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
        [ "$percent" -gt 100 ] && percent=100
    else
        percent=0
    fi
    update_progress "$percent" "$current_size" "$expected_size"

    sleep "$log_interval"
done

# Log termination for debugging
current_size=$(du -sb "$TARGET_DIR" 2>/dev/null | cut -f1 || echo 0)
echo "Progress monitor terminated: final_size=${current_size} bytes" >&2