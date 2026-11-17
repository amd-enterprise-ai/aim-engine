#!/usr/bin/env bash
set -euo pipefail

# OpenShift-only workaround for the AMD GPU detector's privileged container.
namespace="${AIM_SYSTEM_NAMESPACE:-aim-system}"
release="${AIM_ENGINE_RELEASE:-aim-engine}"
selector="app.kubernetes.io/component=gpu-detection,app.kubernetes.io/instance=${release},app.kubernetes.io/managed-by=Helm"

daemonset="$(oc get daemonset -n "$namespace" -l "$selector" -o name | head -n 1)"
if [[ -z "$daemonset" ]]; then
  echo "No AMD GPU detector DaemonSet found for Helm release $release in $namespace" >&2
  exit 1
fi

service_account="$(oc get "$daemonset" -n "$namespace" \
  -o jsonpath='{.spec.template.spec.serviceAccountName}')"
if [[ -z "$service_account" ]]; then
  echo "The AMD GPU detector DaemonSet has no service account" >&2
  exit 1
fi

oc adm policy add-scc-to-user privileged \
  -z "$service_account" \
  -n "$namespace"

oc rollout status "$daemonset" -n "$namespace" --timeout=120s
