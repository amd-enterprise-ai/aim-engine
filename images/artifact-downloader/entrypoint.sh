#!/bin/sh
set -eu

URL="${1:?Usage: $0 <hf://org/model or s3://bucket/path>}"
TARGET_DIR="${TARGET_DIR:-/cache}"

# Start progress monitor in background
if [ -f /progress-monitor.sh ]; then
    /progress-monitor.sh "$URL" "$TARGET_DIR" &
fi

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
### END TESTING ###

case "$URL" in
    hf://*)
        MODEL_PATH="${URL#hf://}"
        echo "Downloading from Hugging Face: $MODEL_PATH to $TARGET_DIR"
        hf download \
            --local-dir "$TARGET_DIR" \
            "$MODEL_PATH"
        echo "Verifying download..."
        hf cache verify \
            --local-dir "$TARGET_DIR" \
            --fail-on-missing-files \
            "$MODEL_PATH"
        echo "Download complete and verified"
        echo "Size of HF_HOME: $(du -sh "$HF_HOME")"
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
        echo "Sync complete"
        ;;
    *)
        echo "Error: Unknown protocol. URL must start with hf:// or s3:// - was $URL" >&2
        exit 1
        ;;
esac