#!/usr/bin/env bash
# Ensure the HuggingFace token Secret exists in the test namespace.
#
# Mirrors ../_shared/ensure-pull-secret.sh so all local dev credentials live
# under ~/.config/aim-engine/. The gated / rate-limited HF downloads these
# tests drive genuinely need a token, so unlike the pull secret this DEFAULTS
# TO HARD-FAIL when nothing is found (set HF_TOKEN_OPTIONAL=1 to warn instead).
#
# The convention here is a v1 Secret named "huggingface-creds" with key "token"
# (referenced by the profiles via secretKeyRef: { name: huggingface-creds,
# key: token }). NOTE: this is a different convention from the aim-system
# `hf-token` / key `hf-token` secret used by profile-id-propagation and
# finetuned-latest-no-image — those tests copy from aim-system and are not
# affected by this helper.
#
# Resolution order (first hit wins):
#   1. A local Secret manifest file (HF_TOKEN_SECRET_FILE, default
#      ~/.config/aim-engine/huggingface-creds.yaml) -> kubectl apply -f.
#   2. An existing <HF_SECRET_NAME> Secret in HF_TOKEN_SOURCE_NS (default
#      "default") -> copy it into the test namespace.
#   3. Nothing found -> hard error (or warn if HF_TOKEN_OPTIONAL is set).
#
# Required env:
#   NAMESPACE  target test namespace (chainsaw passes ($namespace)).
# Optional env:
#   HF_SECRET_NAME       secret name to copy / expect (default huggingface-creds)
#   HF_TOKEN_SECRET_FILE path to a local Secret manifest
#   HF_TOKEN_SOURCE_NS   namespace to copy an existing secret from (default: default)
#   HF_TOKEN_OPTIONAL    if set (non-empty), a missing secret only warns
set -euo pipefail

NS="${NAMESPACE:?NAMESPACE must be set (chainsaw passes ($namespace))}"
SECRET_NAME="${HF_SECRET_NAME:-huggingface-creds}"
SECRET_FILE="${HF_TOKEN_SECRET_FILE:-$HOME/.config/aim-engine/huggingface-creds.yaml}"
SOURCE_NS="${HF_TOKEN_SOURCE_NS:-default}"

# 1) Local manifest file: the canonical "manage it locally" path.
if [ -f "$SECRET_FILE" ]; then
  echo "ensure-hf-secret: applying local manifest '$SECRET_FILE' into namespace '$NS'"
  kubectl -n "$NS" apply -f "$SECRET_FILE"
  exit 0
fi

# 2) Copy an existing secret from a cluster namespace (provision once, reuse).
if kubectl -n "$SOURCE_NS" get secret "$SECRET_NAME" >/dev/null 2>&1; then
  if ! command -v jq >/dev/null 2>&1; then
    echo "ensure-hf-secret: ERROR: jq is required to copy secret/$SECRET_NAME from '$SOURCE_NS'" >&2
    exit 1
  fi
  echo "ensure-hf-secret: copying secret/$SECRET_NAME from namespace '$SOURCE_NS' into '$NS'"
  kubectl -n "$SOURCE_NS" get secret "$SECRET_NAME" -o json \
    | jq 'del(.metadata.namespace, .metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields, .metadata.ownerReferences)' \
    | kubectl -n "$NS" apply -f -
  exit 0
fi

# 3) Nothing available. Fail by default; warn only if explicitly opted out.
msg=$(cat <<EOF
ensure-hf-secret: no HuggingFace token secret found for namespace '$NS'.
  - no local manifest at: $SECRET_FILE
    (override with HF_TOKEN_SECRET_FILE=/path/to/secret.yaml)
  - no secret/$SECRET_NAME in namespace '$SOURCE_NS'
    (override the source with HF_TOKEN_SOURCE_NS)
Provide a v1 Secret named '$SECRET_NAME' with key 'token', e.g.:
  kubectl create secret generic $SECRET_NAME \\
    --from-literal=token=hf_xxx \\
    --dry-run=client -o yaml > $SECRET_FILE
The gated / rate-limited downloads these tests drive will stall without it.
EOF
)

if [ -n "${HF_TOKEN_OPTIONAL:-}" ]; then
  echo "WARNING: $msg" >&2
  echo "ensure-hf-secret: continuing without an HF token (HF_TOKEN_OPTIONAL set)." >&2
  exit 0
fi

echo "ERROR: $msg" >&2
exit 1
