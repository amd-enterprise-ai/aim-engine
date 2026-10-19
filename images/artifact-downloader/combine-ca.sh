#!/bin/sh
# MIT License
#
# Copyright (c) 2026 Advanced Micro Devices, Inc.

# Build one trust bundle from this image's public roots and zero or more
# additional PEM bundles. Certificate parsing is deliberately isolated in this
# init-container process; malformed additions are warned about and skipped.
set -eu

OUTPUT="${1:?Usage: $0 <output-file> [additional-ca-file ...]}"
shift

SYSTEM_CA="${AIM_SYSTEM_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}"
if [ ! -s "$SYSTEM_CA" ]; then
    printf 'ERROR: system CA bundle is unavailable at %s\n' "$SYSTEM_CA" >&2
    exit 1
fi

OUTPUT_DIR=$(dirname "$OUTPUT")
if ! mkdir -p "$OUTPUT_DIR"; then
    printf 'ERROR: cannot create combined CA directory %s\n' "$OUTPUT_DIR" >&2
    exit 1
fi

TEMP="${OUTPUT}.tmp"
if ! cp "$SYSTEM_CA" "$TEMP"; then
    printf 'ERROR: cannot initialize combined CA bundle %s\n' "$TEMP" >&2
    exit 1
fi

for bundle in "$@"; do
    if [ ! -s "$bundle" ]; then
        printf 'WARNING: additional CA bundle is empty or missing at %s; skipping it\n' "$bundle" >&2
        continue
    fi

    if python3 - "$bundle" <<'PY'
import ssl
import sys

try:
    ssl.create_default_context(cafile=sys.argv[1])
except Exception as error:
    print(f"WARNING: additional CA bundle could not be parsed: {error}", file=sys.stderr)
    raise SystemExit(1)
PY
    then
        printf '\n' >>"$TEMP"
        cat "$bundle" >>"$TEMP"
        printf '\n' >>"$TEMP"
    else
        printf 'WARNING: additional CA bundle validation failed at %s; skipping it\n' "$bundle" >&2
    fi
done

chmod 0444 "$TEMP"
mv "$TEMP" "$OUTPUT"
