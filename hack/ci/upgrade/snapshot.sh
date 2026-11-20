#!/usr/bin/env bash

set -euo pipefail

namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
output="${1:?usage: snapshot.sh OUTPUT_FILE}"

mkdir -p "$(dirname "${output}")"
printf 'resource\tnamespace\tname\tuid\towners\n' > "${output}"

record_named() {
    local resource="$1"
    local scope_namespace="$2"
    local name="$3"
    local json

    if [[ "${scope_namespace}" == "-" ]]; then
        json="$(kubectl get "${resource}" "${name}" -o json)"
    else
        json="$(kubectl get "${resource}" "${name}" -n "${scope_namespace}" -o json)"
    fi
    jq -r --arg resource "${resource}" --arg namespace "${scope_namespace}" '
        [
            $resource,
            $namespace,
            .metadata.name,
            .metadata.uid,
            (
                [.metadata.ownerReferences[]? | {apiVersion, kind, name, uid, controller}]
                | sort_by(.apiVersion, .kind, .name, .uid)
                | @base64
            )
        ] | @tsv
    ' <<<"${json}" >> "${output}"
}

record_selected() {
    local resource="$1"
    local scope_namespace="$2"
    local selector="$3"
    local json
    local count

    if [[ "${scope_namespace}" == "-" ]]; then
        json="$(kubectl get "${resource}" -l "${selector}" -o json)"
    else
        json="$(kubectl get "${resource}" -n "${scope_namespace}" -l "${selector}" -o json)"
    fi
    count="$(jq '.items | length' <<<"${json}")"
    if [[ "${count}" != "1" ]]; then
        echo "expected exactly one ${resource} matching ${selector}, found ${count}" >&2
        jq -r '.items[].metadata.name' <<<"${json}" >&2
        exit 1
    fi
    jq -r --arg resource "${resource}" --arg namespace "${scope_namespace}" '
        .items[0] |
        [
            $resource,
            $namespace,
            .metadata.name,
            .metadata.uid,
            (
                [.metadata.ownerReferences[]? | {apiVersion, kind, name, uid, controller}]
                | sort_by(.apiVersion, .kind, .name, .uid)
                | @base64
            )
        ] | @tsv
    ' <<<"${json}" >> "${output}"
}

record_named aimprofiles "${namespace}" upgrade-namespace-source
record_named aimmodels "${namespace}" upgrade-namespace-model
record_selected aimprofilesets "${namespace}" \
    'aim.eai.amd.com/source-model=upgrade-namespace-model,aim.eai.amd.com/source-model-scope=namespace'
record_selected aimprofiles "${namespace}" \
    'aim.eai.amd.com/source-model=upgrade-namespace-model,aim.eai.amd.com/profile-origin=derived'
record_named aimservices "${namespace}" upgrade-namespace-service
record_named aimservices "${namespace}" upgrade-cluster-service
record_selected aimprofilecaches "${namespace}" 'aim.eai.amd.com/service=upgrade-namespace-service'
record_selected aimprofilecaches "${namespace}" 'aim.eai.amd.com/service=upgrade-cluster-service'

# Chart-managed default. An upgrade updates it in place; this UID must not change.
record_named aimclusterruntimeconfigs - default

record_named aimclusterprofiles - upgrade-cluster-source
record_named aimclustermodels - upgrade-cluster-model
# Registry-discovered model owned by upgrade-model-source. Its UID must not change.
record_selected aimclustermodels - 'aim.eai.amd.com/model-source=upgrade-model-source'
record_selected aimclusterprofilesets - \
    'aim.eai.amd.com/source-model=upgrade-cluster-model,aim.eai.amd.com/source-model-scope=cluster'
record_selected aimclusterprofiles - \
    'aim.eai.amd.com/source-model=upgrade-cluster-model,aim.eai.amd.com/profile-origin=derived'

# Profile-cache PVCs plus the model artifact's cache and adapter-disk PVCs.
for pvc in $(kubectl get pvc -n "${namespace}" -o jsonpath='{.items[*].metadata.name}'); do
    record_named persistentvolumeclaims "${namespace}" "${pvc}"
done

# Adapter owned by upgrade-model-artifact. Its UID must not change.
record_named aimartifacts "${namespace}" upgrade-adapter-artifact

echo "Baseline object identity snapshot:"
cat "${output}"
