#!/usr/bin/env bash
#
# Records the UID and owner references of the v0.2.7 lane's services, their
# InferenceServices, profile caches and artifacts, and its models. The lane
# records them before and after the upgrade and diffs the two files.

set -euo pipefail

namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
output="${1:?usage: record-v0.2.7-identities.sh OUTPUT_FILE}"

identities() {
  jq -r '.items[] | select(.metadata.deletionTimestamp == null)
    | [.kind, .metadata.namespace // "-", .metadata.name, .metadata.uid,
      ([.metadata.ownerReferences[]? | "\(.apiVersion) \(.kind) \(.name) \(.uid)"] | sort | join(", "))]
    | @tsv'
}

mkdir -p "$(dirname "${output}")"
{
  for resource in aimservices inferenceservices aimprofilecaches aimartifacts aimmodels; do
    kubectl get "${resource}" -n "${namespace}" -o json | identities
  done
  for resource in aimclustermodelsources aimclustermodels; do
    kubectl get "${resource}" -o json | identities
  done
} | sort > "${output}"

cat "${output}"
