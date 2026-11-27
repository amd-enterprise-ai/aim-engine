#!/usr/bin/env bash
#
# Records the UID and owner references of the v0.2.7 lane's services, their
# InferenceServices, profile caches, artifacts and PVCs, its models, the profiles
# its models produce with their primary flag, the predictor pods of the service
# moved by aim-template-migration and of the autoscaled service, and the
# chart-managed AIMClusterRuntimeConfig.
# The lane records them after that move and before and after the upgrade, and
# diffs the files.

set -euo pipefail

namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
output="${1:?usage: record-v0.2.7-identities.sh OUTPUT_FILE}"

identities() {
  jq -r '.items[] | select(.metadata.deletionTimestamp == null)
    | [.kind, .metadata.namespace // "-", .metadata.name, .metadata.uid,
      ([.metadata.ownerReferences[]? | "\(.apiVersion) \(.kind) \(.name) \(.uid)"] | sort | join(", "))]
    | @tsv'
}

primaries() {
  jq -r '.items[] | select(.metadata.deletionTimestamp == null)
    | [.kind, .metadata.namespace // "-", .metadata.name, .metadata.uid, "primary=\(.spec.primary == true)"]
    | @tsv'
}

mkdir -p "$(dirname "${output}")"
{
  for resource in aimservices inferenceservices aimprofilecaches aimartifacts aimmodels pvc; do
    kubectl get "${resource}" -n "${namespace}" -o json | identities
  done
  kubectl get pods -n "${namespace}" \
    -l 'aim.eai.amd.com/service in (upgrade-migrated-service,upgrade-autoscaled-service),app.kubernetes.io/component=inference' \
    -o json | identities
  for resource in aimclustermodelsources aimclustermodels aimclusterruntimeconfigs; do
    kubectl get "${resource}" -o json | identities
  done
  kubectl get aimprofiles -n "${namespace}" -l aim.eai.amd.com/source-model -o json | primaries
  kubectl get aimclusterprofiles -l aim.eai.amd.com/source-model -o json | primaries
} | sort > "${output}"

if ! grep -q $'^AIMClusterRuntimeConfig\t-\tdefault\t' "${output}"; then
  echo "chart AIMClusterRuntimeConfig/default is missing from ${output}" >&2
  exit 1
fi

cat "${output}"
