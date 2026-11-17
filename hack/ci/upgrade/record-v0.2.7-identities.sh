#!/usr/bin/env bash
#
# Records the UID and owner references of the v0.2.7 lane's services, their
# InferenceServices, profile caches, artifacts and PVCs, its models, the profiles
# its models produce with their primary flag, the predictor pods of the service
# moved by aim-template-migration, and the chart-managed AIMClusterRuntimeConfig.
# The lane records them after that move and before and after the upgrade, and
# diffs the files.
#
# --diff BEFORE AFTER allows one owner-reference change: v1alpha1 to v1alpha2
# for AIMArtifact, AIMRuntimeConfig, AIMClusterRuntimeConfig and
# AIMClusterModelSource. Any other change fails. An artifact-owned PVC must
# name its artifact at v1alpha2 after the upgrade. Drop that allowance in the
# release that deletes those v1alpha1 entries.

set -euo pipefail

namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"

diff_identities() {
  local before="$1"
  local after="$2"
  local problems

  problems="$(jq -n --rawfile before "${before}" --rawfile after "${after}" '
    def parse:
      split("\n")
      | map(select(length > 0))
      | map(split("\t") | {kind: .[0], namespace: .[1], name: .[2], uid: .[3], owners: (.[4] // "")});
    def parse_owners:
      if . == "" then []
      else split(", ") | map(split(" ") | {apiVersion: .[0], kind: .[1], name: .[2], uid: .[3]})
      end;
    def kept: ["AIMArtifact", "AIMRuntimeConfig", "AIMClusterRuntimeConfig", "AIMClusterModelSource"];
    def pair_ok:
      . as $pair
      | (kept | index($pair[0].kind)) as $kept_kind
      | $pair[0].kind == $pair[1].kind and
        $pair[0].name == $pair[1].name and
        $pair[0].uid == $pair[1].uid and
        (
          $pair[0].apiVersion == $pair[1].apiVersion or
          (
            $kept_kind != null and
            $pair[0].apiVersion == "aim.eai.amd.com/v1alpha1" and
            $pair[1].apiVersion == "aim.eai.amd.com/v1alpha2"
          )
        );
    def owners_ok:
      (.[0] | parse_owners | sort_by(.kind, .name, .uid)) as $before_owners
      | (.[1] | parse_owners | sort_by(.kind, .name, .uid)) as $after_owners
      | ($before_owners | length) == ($after_owners | length) and
        ([range(0; $before_owners | length) | [$before_owners[.], $after_owners[.]]] | all(pair_ok));
    def key: "\(.kind)\t\(.namespace)\t\(.name)\t\(.uid)";
    ($before | parse) as $before_rows
    | ($after | parse) as $after_rows
    | ($before_rows | map({key: key, value: .}) | from_entries) as $before_map
    | ($after_rows | map({key: key, value: .}) | from_entries) as $after_map
    | ($before_rows | map(key)) as $before_keys
    | ($after_rows | map(key)) as $after_keys
    | (
        [($before_keys - $after_keys)[] | "missing after upgrade: \(.)"]
        + [($after_keys - $before_keys)[] | "appeared during upgrade: \(.)"]
        + [
            $before_keys[] as $object_key
            | select($after_map[$object_key] != null)
            | select(([$before_map[$object_key].owners, $after_map[$object_key].owners] | owners_ok) | not)
            | "owner references changed: \($object_key)\nbefore: \($before_map[$object_key].owners)\nafter:  \($after_map[$object_key].owners)"
          ]
        + [
            $before_rows[]
            | select(.kind == "PersistentVolumeClaim")
            | select(any(.owners | parse_owners[]; .kind == "AIMArtifact"))
            | select($after_map[key] != null)
            | select(any($after_map[key].owners | parse_owners[]; .kind == "AIMArtifact" and .apiVersion != "aim.eai.amd.com/v1alpha2"))
            | "artifact owner reference is not aim.eai.amd.com/v1alpha2: \(key)"
          ]
      )
    | .[]
  ')"
  if [[ -n "${problems}" ]]; then
    echo "${problems}" >&2
    return 1
  fi
}

if [[ "${1:-}" == "--diff" ]]; then
  diff_identities "${2:?usage: record-v0.2.7-identities.sh --diff BEFORE AFTER}" \
    "${3:?usage: record-v0.2.7-identities.sh --diff BEFORE AFTER}"
  exit
fi

output="${1:?usage: record-v0.2.7-identities.sh OUTPUT_FILE | --diff BEFORE AFTER}"

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
    -l aim.eai.amd.com/service=upgrade-migrated-service,app.kubernetes.io/component=inference -o json | identities
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
if ! grep $'^PersistentVolumeClaim\t' "${output}" | grep -q 'AIMArtifact'; then
  echo "no artifact-owned PersistentVolumeClaim recorded in ${output}" >&2
  exit 1
fi

cat "${output}"
