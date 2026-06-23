#!/usr/bin/env bash
# Ensure a Docker Hub image-pull secret exists in the test namespace.
#
# The private AIM images (docker.io/silogenai/*) need a dockerconfigjson pull
# secret. Rather than baking a personal file path into every test and HARD-
# FAILING when it is missing, this helper resolves the secret from whatever a
# developer / CI runner has available and — crucially — WARNS AND CONTINUES
# when nothing is found. That lets the suite still run on a cluster that already
# has the credentials wired in another way (node-level creds, a pull secret on
# the namespace's default ServiceAccount, or a since-published public image),
# instead of refusing to start.
#
# Resolution order (first hit wins):
#   1. A local Secret manifest file (DOCKERHUB_PULL_SECRET_FILE, default
#      ~/.config/aim-engine/dockerhub-regcred.yaml) -> kubectl apply -f.
#      This is the "share a secret locally" path: drop your dockerconfigjson
#      Secret manifest there once and every gpu test picks it up.
#   2. An existing <PULL_SECRET_NAME> Secret in a source namespace
#      (PULL_SECRET_SOURCE_NS, default "default") -> copy it into the test
#      namespace. Provision once per shared cluster; all tests reuse it.
#   3. Nothing found -> print a warning explaining how to provide it and exit 0
#      (unless PULL_SECRET_REQUIRED is set, in which case exit 1).
#
# Required env:
#   NAMESPACE  target test namespace (chainsaw passes ($namespace)).
# Optional env:
#   PULL_SECRET_NAME            secret name to copy / expect (default dockerhub-regcred)
#   DOCKERHUB_PULL_SECRET_FILE  path to a local dockerconfigjson Secret manifest
#   PULL_SECRET_SOURCE_NS       namespace to copy an existing secret from (default: default)
#   PULL_SECRET_REQUIRED        if set (non-empty), a missing secret is a hard error
set -euo pipefail

NS="${NAMESPACE:?NAMESPACE must be set (chainsaw passes ($namespace))}"
SECRET_NAME="${PULL_SECRET_NAME:-dockerhub-regcred}"
SECRET_FILE="${DOCKERHUB_PULL_SECRET_FILE:-$HOME/.config/aim-engine/dockerhub-regcred.yaml}"
SOURCE_NS="${PULL_SECRET_SOURCE_NS:-default}"

# 1) Local manifest file: the canonical "manage it locally" path.
if [ -f "$SECRET_FILE" ]; then
  echo "ensure-pull-secret: applying local manifest '$SECRET_FILE' into namespace '$NS'"
  kubectl -n "$NS" apply -f "$SECRET_FILE"
  exit 0
fi

# 2) Copy an existing secret from a cluster namespace (provision once, reuse).
if kubectl -n "$SOURCE_NS" get secret "$SECRET_NAME" >/dev/null 2>&1; then
  if ! command -v jq >/dev/null 2>&1; then
    echo "ensure-pull-secret: ERROR: jq is required to copy secret/$SECRET_NAME from '$SOURCE_NS'" >&2
    exit 1
  fi
  echo "ensure-pull-secret: copying secret/$SECRET_NAME from namespace '$SOURCE_NS' into '$NS'"
  kubectl -n "$SOURCE_NS" get secret "$SECRET_NAME" -o json \
    | jq 'del(.metadata.namespace, .metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields, .metadata.ownerReferences)' \
    | kubectl -n "$NS" apply -f -
  exit 0
fi

# 3) Nothing available. Warn (or fail if explicitly required).
msg=$(cat <<EOF
ensure-pull-secret: no Docker Hub pull secret found for namespace '$NS'.
  - no local manifest at: $SECRET_FILE
    (override with DOCKERHUB_PULL_SECRET_FILE=/path/to/secret.yaml)
  - no secret/$SECRET_NAME in namespace '$SOURCE_NS'
    (override the source with PULL_SECRET_SOURCE_NS)
If docker.io/silogenai images are private, pulls in '$NS' will ImagePullBackOff.
Create the secret once, e.g.:
  kubectl create secret docker-registry $SECRET_NAME \\
    --docker-server=docker.io --docker-username=<user> \\
    --docker-password=<token-or-password> -n $SOURCE_NS
or export a manifest to $SECRET_FILE.
EOF
)

if [ -n "${PULL_SECRET_REQUIRED:-}" ]; then
  echo "ERROR: $msg" >&2
  exit 1
fi

echo "WARNING: $msg" >&2
echo "ensure-pull-secret: continuing without an in-namespace pull secret." >&2
exit 0
