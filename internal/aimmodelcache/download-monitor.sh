#!/bin/sh
# Progress monitor for model download
# This runs as a native sidecar (init container with restartPolicy=Always).
# Kubernetes automatically sends SIGTERM when all regular containers terminate.
#
# Environment variables:
#   EXPECTED_SIZE_BYTES - Expected size of the download in bytes
#   MOUNT_PATH - Path where the model is being downloaded
#
# JSON output types:
#   - "start": Initial message when monitor starts
#   - "progress": Periodic progress update
#   - "complete": Download finished successfully (detected via marker file)
#   - "terminated": Received SIGTERM from kubelet (main container finished)

# Handle SIGTERM gracefully - kubelet sends this when main container terminates
terminated=false
trap 'terminated=true' TERM

# Output a JSON log message
# Usage: log_json <type> [key=value ...]
log_json() {
    type=$1
    shift
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    json="{\"timestamp\":\"$timestamp\",\"type\":\"$type\""
    for kv in "$@"; do
        key="${kv%%=*}"
        value="${kv#*=}"
        # Check if value is numeric
        case "$value" in
            ''|*[!0-9]*) json="$json,\"$key\":\"$value\"" ;;  # string
            *) json="$json,\"$key\":$value" ;;                 # number
        esac
    done
    echo "$json}"
}

expected_size=${EXPECTED_SIZE_BYTES:-0}
mount_path=${MOUNT_PATH:-/cache}
interval=10

log_json "start" "expectedBytes=$expected_size" "intervalSeconds=$interval"

while true; do
    # Check if we received SIGTERM (main container terminated)
    if [ "$terminated" = "true" ]; then
        current_size=$(du -sb "$mount_path" 2>/dev/null | cut -f1 || echo 0)
        log_json "terminated" "currentBytes=$current_size" "expectedBytes=$expected_size" "message=Main container terminated"
        exit 0
    fi

    # Check if download completed successfully (marker file from main container)
    if [ -f "$mount_path/.download-complete" ]; then
        current_size=$(du -sb "$mount_path" 2>/dev/null | cut -f1 || echo 0)
        log_json "complete" "currentBytes=$current_size" "expectedBytes=$expected_size"
        exit 0
    fi

    current_size=$(du -sb "$mount_path" 2>/dev/null | cut -f1 || echo 0)

    if [ "$expected_size" -gt 0 ] && [ "$current_size" -gt 0 ]; then
        percent=$((current_size * 100 / expected_size))
        # Cap at 100% (during download, temp files may exceed expected size)
        if [ $percent -gt 100 ]; then
            percent=100
        fi
        log_json "progress" "percent=$percent" "currentBytes=$current_size" "expectedBytes=$expected_size"
    elif [ "$current_size" -gt 0 ]; then
        log_json "progress" "currentBytes=$current_size" "expectedBytes=0" "message=Expected size unknown"
    else
        log_json "progress" "currentBytes=0" "expectedBytes=$expected_size" "message=Waiting for download to start"
    fi

    # Use a loop with short sleeps so we can check for SIGTERM more frequently
    # sleep in busybox doesn't get interrupted by signals, so we poll
    i=0
    while [ $i -lt $interval ] && [ "$terminated" = "false" ]; do
        sleep 1
        i=$((i + 1))
    done
done
