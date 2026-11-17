#!/usr/bin/env bash
# Waits until KServe's LLMInferenceServiceConfig validating webhook admits
# requests. The kserve-llmisvc Deployment can report Ready before the webhook
# server accepts connections, and configs applied in that window fail admission.
set -euo pipefail

namespace="${1:-kserve-system}"
probe="apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceServiceConfig
metadata:
  name: webhook-readiness-probe
  namespace: ${namespace}
spec: {}"

for _ in $(seq 1 60); do
  if kubectl create --dry-run=server -f - <<<"${probe}" >/dev/null 2>&1; then
    exit 0
  fi
  sleep 5
done

echo "LLMInferenceServiceConfig webhook in ${namespace} did not become ready" >&2
kubectl create --dry-run=server -f - <<<"${probe}"
