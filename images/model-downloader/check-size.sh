#!/bin/sh
set -eu

URL="${1:?Usage: $0 <hf://org/model or s3://bucket/path>}"

case "$URL" in
    hf://*)
    MODEL_PATH="${URL#hf://}"
    
    # Capture both stdout and stderr, check exit code
    if ! SIZE_OUTPUT=$(python -c "
import sys
from huggingface_hub import HfApi
from huggingface_hub.utils import RepositoryNotFoundError, GatedRepoError

try:
    info = HfApi().model_info('$MODEL_PATH', files_metadata=True)
    print(sum(f.size or 0 for f in info.siblings))
except RepositoryNotFoundError:
    print('Model not found: $MODEL_PATH', file=sys.stderr)
    print('Check the model name or ensure it exists on HuggingFace.', file=sys.stderr)
    sys.exit(1)
except GatedRepoError:
    print('Model requires authentication: $MODEL_PATH', file=sys.stderr)
    print('Set HF_TOKEN environment variable with a valid HuggingFace token.', file=sys.stderr)
    print('You may also need to accept the model license at: https://huggingface.co/$MODEL_PATH', file=sys.stderr)
    sys.exit(2)
except Exception as e:
    print(f'Failed to fetch model info: {e}', file=sys.stderr)
    sys.exit(3)
" 2>&1); then
        echo "Error: Failed to get size for $URL" >&2
        echo "$SIZE_OUTPUT" >&2
        exit 1
    fi
    
    SIZE_BYTES="$SIZE_OUTPUT"
    ;;
    s3://*)
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
        
        # Capture both stdout and stderr
        if ! S3_OUTPUT=$(s3cmd $S3CMD_ARGS du "$URL" 2>&1); then
            echo "Error: s3cmd failed for $URL" >&2
            echo "$S3_OUTPUT" >&2
            exit 1
        fi
        
        SIZE_BYTES=$(echo "$S3_OUTPUT" | awk '{print $1}')
        ;;
esac

# Validate SIZE_BYTES is a positive integer
if [ -z "$SIZE_BYTES" ]; then
    echo "Error: Failed to determine size for $URL" >&2
    exit 1
fi

# Check it's a valid number (not empty, not negative, not garbage)
case "$SIZE_BYTES" in
    ''|*[!0-9]*)
        echo "Error: Invalid size value '$SIZE_BYTES' for $URL" >&2
        exit 1
        ;;
esac

if [ "$SIZE_BYTES" -le 0 ]; then
    echo "Error: Size is zero or negative ($SIZE_BYTES bytes) for $URL" >&2
    exit 1
fi

# Output as JSON for easy parsing
echo "{\"url\":\"$URL\",\"sizeBytes\":$SIZE_BYTES}"