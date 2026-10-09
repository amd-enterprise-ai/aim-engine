#!/usr/bin/env bash
set -euo pipefail

# OpenShift-only workaround for AIM Engine releases whose detector pods are
# rejected by SCC admission. Review the required privileges before use.

namespace="${AIM_SYSTEM_NAMESPACE:-aim-system}"
selector='app.kubernetes.io/name=aim-accelerator-detector'

service_account="${AIM_DETECTOR_SERVICE_ACCOUNT:-}"
if [[ -z "$service_account" ]]; then
  service_account="$(oc get daemonset -n "$namespace" -l "$selector" \
    -o jsonpath='{.items[0].spec.template.spec.serviceAccountName}')"
fi

if [[ -z "$service_account" ]]; then
  echo "No AIM accelerator detector service account found in $namespace" >&2
  exit 1
fi

oc adm policy add-scc-to-user privileged \
  -z "$service_account" \
  -n "$namespace"

while IFS= read -r daemonset; do
  [[ -z "$daemonset" ]] && continue

  oc patch "$daemonset" -n "$namespace" --type=strategic -p '{
    "spec": {
      "template": {
        "spec": {
          "containers": [{
            "name": "detector",
            "securityContext": {
              "privileged": true,
              "runAsUser": 0,
              "runAsNonRoot": false,
              "allowPrivilegeEscalation": true
            }
          }]
        }
      }
    }
  }'

  while IFS= read -r init_container; do
    [[ -z "$init_container" ]] && continue
    oc patch "$daemonset" -n "$namespace" --type=strategic -p "{
      \"spec\": {
        \"template\": {
          \"spec\": {
            \"initContainers\": [{
              \"name\": \"$init_container\",
              \"securityContext\": {
                \"privileged\": true,
                \"runAsUser\": 0,
                \"runAsNonRoot\": false,
                \"allowPrivilegeEscalation\": true
              }
            }]
          }
        }
      }
    }"
  done < <(oc get "$daemonset" -n "$namespace" \
    -o jsonpath='{range .spec.template.spec.initContainers[*]}{.name}{"\n"}{end}')

  oc rollout status "$daemonset" -n "$namespace" --timeout=120s
done < <(oc get daemonset -n "$namespace" -l "$selector" -o name)
