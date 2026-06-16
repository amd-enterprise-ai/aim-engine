#!/usr/bin/env bash
# Reusable adapter-presence validation for AIM LoRA integration tests.
#
# Asserts that a LoRA adapter the control plane staged is actually LOADED by the
# runtime — vLLM surfaces each loaded LoRA as its own entry in /v1/models, keyed
# by the adapter name (which equals the on-disk subdirectory name == the
# AIMArtifact name). This is the "prove it worked end-to-end" check that the
# control plane alone cannot make: staging bytes + mounting the disk does not
# guarantee the engine picked them up.
#
# Required env:
#   HTTP_BASE_PATH  Gateway base path that contains /models (e.g.
#                   /integration/<test>/<ns>/<service>/v1).
#   ADAPTER_NAME    The adapter id expected in /v1/models (the AIMArtifact name).
# Optional env:
#   HTTP_NS, HTTP_SVC, HTTP_PORT  Gateway service coordinates (defaults below).
#   HTTP_TIMEOUT, HTTP_RETRY_DEADLINE, HTTP_RETRY_INTERVAL  Polling budget (s).
#   OPENAI_API_KEY  Sent as a bearer token if set.
set -euo pipefail

NS="${HTTP_NS:-kgateway-system}"
SVC="${HTTP_SVC:-kserve-ingress-gateway}"
SVC_PORT="${HTTP_PORT:-80}"
BASE_PATH="${HTTP_BASE_PATH:-/integration/test/v1}"
TIMEOUT="${HTTP_TIMEOUT:-60}"

# Adapter staging is decoupled from the ISVC coming up, so a freshly-Running
# service may not have the adapter loaded for a few poll cycles (dynamic mode
# polls the disk ~every 30s; static loads at launch). Poll generously.
RETRY_DEADLINE="${HTTP_RETRY_DEADLINE:-300}"
RETRY_INTERVAL="${HTTP_RETRY_INTERVAL:-5}"

: "${ADAPTER_NAME:?ADAPTER_NAME is required (the AIMArtifact/adapter id expected in /v1/models)}"

AUTH_HEADER=()
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
  AUTH_HEADER=( -H "Authorization: Bearer ${OPENAI_API_KEY}" )
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing: $1" >&2; exit 2; }; }
need kubectl; need curl; need jq

start_proxy() {
  for p in 8001 8002 8003 8004 8005; do
    if ! lsof -iTCP:"$p" -sTCP:LISTEN -P -n >/dev/null 2>&1; then
      kubectl proxy --port="$p" >/dev/null 2>&1 &
      PROXY_PID=$!
      sleep 1
      if kill -0 "$PROXY_PID" 2>/dev/null; then
        PROXY_PORT="$p"
        trap 'kill "$PROXY_PID" 2>/dev/null || true' EXIT
        return 0
      fi
    fi
  done
  echo "ERROR: could not start kubectl proxy (8001-8005 busy?)" >&2
  exit 1
}

echo "Starting kubectl proxy…"
start_proxy
echo "kubectl proxy on 127.0.0.1:${PROXY_PORT}"

MODELS_URL="http://127.0.0.1:${PROXY_PORT}/api/v1/namespaces/${NS}/services/${SVC}:${SVC_PORT}/proxy${BASE_PATH}/models"
echo "Waiting for adapter '${ADAPTER_NAME}' to appear in $MODELS_URL (up to ${RETRY_DEADLINE}s)"

deadline=$(( SECONDS + RETRY_DEADLINE ))
BODY=""
attempt=0
while (( SECONDS < deadline )); do
  attempt=$(( attempt + 1 ))
  RESP="$(curl -sS -w '\n%{http_code}' --max-time "$TIMEOUT" "${AUTH_HEADER[@]}" "$MODELS_URL" 2>&1 || true)"
  BODY="$(echo "$RESP" | head -n -1)"
  CODE="$(echo "$RESP" | tail -n 1)"
  if [[ "$CODE" == "200" ]] && echo "$BODY" | jq empty >/dev/null 2>&1; then
    if echo "$BODY" | jq -e --arg a "$ADAPTER_NAME" '.data[] | select(.id == $a)' >/dev/null 2>&1; then
      echo "✅ adapter '${ADAPTER_NAME}' is loaded (present in /v1/models) after ${attempt} attempt(s)"
      echo "Loaded models:"
      echo "$BODY" | jq -r '.data[].id'
      exit 0
    fi
    echo "attempt $attempt: HTTP 200 but adapter not listed yet"
  else
    echo "attempt $attempt: HTTP $CODE"
  fi
  sleep "$RETRY_INTERVAL"
done

echo "ERROR: adapter '${ADAPTER_NAME}' did not appear in /v1/models within ${RETRY_DEADLINE}s"
echo "Last /v1/models body:"
echo "$BODY" | head -c 800; echo
exit 1
