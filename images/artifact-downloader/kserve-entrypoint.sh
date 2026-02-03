#!/bin/sh
# KServe storage-initializer compatibility shim
# Adapts KServe's interface to our entrypoint
#
# KServe calls: /storage-initializer/scripts/initializer-entrypoint <source_uri> <dest_path>
# Our entrypoint: /entrypoint.sh <url> (uses TARGET_DIR env var)

SOURCE_URI="${1:?Usage: $0 <source_uri> <dest_path>}"
DEST_PATH="${2:?Usage: $0 <source_uri> <dest_path>}"

export TARGET_DIR="$DEST_PATH"
exec /entrypoint.sh "$SOURCE_URI"