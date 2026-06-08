#!/usr/bin/env bash
# Validates that /v1/models reports the max_model_len set via custom profile engine args.
# The AIM runtime unconditionally manages served-model-name, so we use max-model-len
# as a canary instead: if /v1/models reports the expected value, the engine arg made
# it through the full chain:
#
#   customProfile.engineArgs -> ConfigMap YAML -> volume mount -> AIM runtime -> vLLM CLI -> API
set -euo pipefail

NS="${HTTP_NS:-kgateway-system}"
SVC="${HTTP_SVC:-kserve-ingress-gateway}"
SVC_PORT="${HTTP_PORT:-80}"
BASE_PATH="${HTTP_BASE_PATH:?must be set}"
EXPECTED_MAX_MODEL_LEN="${EXPECTED_MAX_MODEL_LEN:?must be set}"
TIMEOUT="${HTTP_TIMEOUT:-60}"

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
echo "GET $MODELS_URL"

RESP="$(curl -sS -w '\n%{http_code}' --max-time "$TIMEOUT" "$MODELS_URL")"
BODY="$(echo "$RESP" | head -n -1)"
CODE="$(echo "$RESP" | tail -n 1)"

echo "HTTP: $CODE"
if [[ "$CODE" != "200" ]]; then
  echo "ERROR: expected 200 from /models, got $CODE"
  echo "$BODY" | head -c 600; echo
  exit 1
fi

echo "$BODY" | jq empty >/dev/null 2>&1 || { echo "ERROR: /models body is not valid JSON"; exit 1; }

ACTUAL="$(echo "$BODY" | jq -r '.data[0].max_model_len // empty')"
echo "max_model_len from /v1/models: $ACTUAL"
echo "Expected max_model_len:        $EXPECTED_MAX_MODEL_LEN"

if [[ "$ACTUAL" != "$EXPECTED_MAX_MODEL_LEN" ]]; then
  echo "ERROR: max_model_len mismatch — max-model-len engine arg did not propagate"
  echo "Full response:"
  echo "$BODY" | jq .
  exit 1
fi

echo "max-model-len engine arg propagated correctly"

# Assert the model-id annotation value is one of the names vLLM serves
# (present in /v1/models data[].id) when EXPECTED_MODEL_ID is set.
if [[ -n "${EXPECTED_MODEL_ID:-}" ]]; then
  echo "Checking model-id '$EXPECTED_MODEL_ID' is exposed at /v1/models"
  if echo "$BODY" | jq -e --arg m "$EXPECTED_MODEL_ID" '.data[] | select(.id == $m)' >/dev/null; then
    echo "model-id '$EXPECTED_MODEL_ID' is served by vLLM"
  else
    echo "ERROR: model-id '$EXPECTED_MODEL_ID' not found in /v1/models ids"
    echo "Served ids:"
    echo "$BODY" | jq -r '.data[].id'
    exit 1
  fi
fi

echo "All checks passed"
