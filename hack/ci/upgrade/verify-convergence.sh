#!/usr/bin/env bash

set -euo pipefail

namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
snapshot="${1:?usage: verify-convergence.sh SNAPSHOT_FILE}"
attempts="${UPGRADE_CONVERGENCE_ATTEMPTS:-36}"
interval="${UPGRADE_CONVERGENCE_INTERVAL:-5}"
stable_checks="${UPGRADE_STABLE_CHECKS:-6}"

get_json() {
    local resource="$1"
    local scope_namespace="$2"
    local name="$3"

    if [[ "${scope_namespace}" == "-" ]]; then
        kubectl get "${resource}" "${name}" -o json
    else
        kubectl get "${resource}" "${name}" -n "${scope_namespace}" -o json
    fi
}

# Removed in the release that deletes the v1alpha1 entries.
normalize_owners() {
    printf '%s' "$1" | jq -sRr '
        @base64d | fromjson
        | map(
            if .apiVersion == "aim.eai.amd.com/v1alpha1" and
               (.kind | IN("AIMArtifact", "AIMRuntimeConfig", "AIMClusterRuntimeConfig", "AIMClusterModelSource"))
            then .apiVersion = "aim.eai.amd.com/v1alpha2"
            else .
            end
          )
        | sort_by(.apiVersion, .kind, .name, .uid)
        | tojson
    '
}

verify_identity() {
    local resource scope_namespace name expected_uid expected_owners
    local actual_uid actual_owners json

    while IFS=$'\t' read -r resource scope_namespace name expected_uid expected_owners; do
        [[ "${resource}" == "resource" ]] && continue
        json="$(get_json "${resource}" "${scope_namespace}" "${name}")"
        actual_uid="$(jq -r '.metadata.uid' <<<"${json}")"
        if [[ "${actual_uid}" != "${expected_uid}" ]]; then
            echo "${resource}/${name} was replaced: expected UID ${expected_uid}, got ${actual_uid}" >&2
            return 1
        fi
        actual_owners="$(jq -r '
            [.metadata.ownerReferences[]? | {apiVersion, kind, name, uid, controller}]
            | sort_by(.apiVersion, .kind, .name, .uid)
            | @base64
        ' <<<"${json}")"
        if [[ "$(normalize_owners "${expected_owners}")" != "$(normalize_owners "${actual_owners}")" ]]; then
            echo "${resource}/${name} owner references changed during upgrade" >&2
            echo "expected: $(base64 --decode <<<"${expected_owners}")" >&2
            echo "actual:   $(base64 --decode <<<"${actual_owners}")" >&2
            return 1
        fi
        if [[ "${resource}" == "persistentvolumeclaims" ]] &&
            base64 --decode <<<"${expected_owners}" | jq -e 'any(.[]; .kind == "AIMArtifact")' >/dev/null &&
            ! jq -e '[.metadata.ownerReferences[]? | select(.kind == "AIMArtifact").apiVersion] | all(. == "aim.eai.amd.com/v1alpha2")' >/dev/null <<<"${json}"; then
            echo "${resource}/${name} artifact owner reference is not aim.eai.amd.com/v1alpha2" >&2
            jq '.metadata.ownerReferences' <<<"${json}" >&2
            return 1
        fi
    done < "${snapshot}"
}

verify_exactly_one() {
    local resource="$1"
    local scope_namespace="$2"
    local selector="$3"
    local count

    if [[ "${scope_namespace}" == "-" ]]; then
        count="$(kubectl get "${resource}" -l "${selector}" -o json | jq '.items | length')"
    else
        count="$(kubectl get "${resource}" -n "${scope_namespace}" -l "${selector}" -o json | jq '.items | length')"
    fi
    [[ "${count}" == "1" ]] || {
        echo "expected one ${resource} matching ${selector}, found ${count}" >&2
        return 1
    }
}

verify_ready_status() {
    local resource="$1"
    local scope_namespace="$2"
    local name="$3"
    local json

    json="$(get_json "${resource}" "${scope_namespace}" "${name}")"
    jq -e '
        .status.status == "Ready" and
        any(.status.conditions[]?; .type == "Ready" and .status == "True")
    ' <<<"${json}" >/dev/null || {
        echo "${resource}/${name} is not Ready" >&2
        jq '{metadata: {name: .metadata.name, generation: .metadata.generation}, status: .status}' <<<"${json}" >&2
        return 1
    }
}

verify_profile() {
    local resource="$1"
    local scope_namespace="$2"
    local name="$3"
    local json origin require_contract

    json="$(get_json "${resource}" "${scope_namespace}" "${name}")"
    origin="$(jq -r '
        .metadata.labels["aim.eai.amd.com/profile-origin"] //
        .status.origin //
        ""
    ' <<<"${json}")"
    require_contract=false
    case "${origin}" in
        discovered|derived)
            require_contract=true
            ;;
    esac
    jq -e --argjson require_contract "${require_contract}" '
        .status.status == "Ready" and
        .status.observedGeneration == .metadata.generation and
        (
            ($require_contract | not) or
            ((.metadata.annotations["aim.eai.amd.com/profile-yaml-contract"] // "") | length > 0)
        ) and
        any(.status.conditions[]?; .type == "RuntimeProjected" and .status == "True") and
        all(.status.conditions[]?; ((.type | endswith("Ready")) and .status == "False") | not)
    ' <<<"${json}" >/dev/null || {
        echo "${resource}/${name} has not converged after runtime projection backfill" >&2
        jq '{
            metadata: {
                name: .metadata.name,
                generation: .metadata.generation,
                origin: (
                    .metadata.labels["aim.eai.amd.com/profile-origin"] //
                    .status.origin
                ),
                contract: .metadata.annotations["aim.eai.amd.com/profile-yaml-contract"]
            },
            status: .status
        }' <<<"${json}" >&2
        return 1
    }
}

verify_service() {
    local name="$1"
    local expected_profile="$2"
    local expected_scope="$3"
    local json

    json="$(get_json aimservices "${namespace}" "${name}")"
    jq -e --arg profile "${expected_profile}" --arg scope "${expected_scope}" '
        .status.resolvedProfile.name == $profile and
        .status.resolvedProfile.scope == $scope and
        any(.status.conditions[]?; .type == "ProfileReady" and .status == "True" and .reason == "ProfileResolved")
    ' <<<"${json}" >/dev/null || {
        echo "aimservices/${name} lost its resolved profile" >&2
        jq '{metadata: {name: .metadata.name, generation: .metadata.generation}, status: .status}' <<<"${json}" >&2
        return 1
    }
}

verify_all() {
    local resource scope_namespace name uid owners

    verify_identity || return 1

    verify_exactly_one aimprofilesets "${namespace}" \
        'aim.eai.amd.com/source-model=upgrade-namespace-model,aim.eai.amd.com/source-model-scope=namespace' || return 1
    verify_exactly_one aimprofiles "${namespace}" \
        'aim.eai.amd.com/source-model=upgrade-namespace-model,aim.eai.amd.com/profile-origin=derived' || return 1
    verify_exactly_one aimprofilecaches "${namespace}" \
        'aim.eai.amd.com/service=upgrade-namespace-service' || return 1
    verify_exactly_one aimprofilecaches "${namespace}" \
        'aim.eai.amd.com/service=upgrade-cluster-service' || return 1
    verify_exactly_one aimclusterprofilesets - \
        'aim.eai.amd.com/source-model=upgrade-cluster-model,aim.eai.amd.com/source-model-scope=cluster' || return 1
    verify_exactly_one aimclusterprofiles - \
        'aim.eai.amd.com/source-model=upgrade-cluster-model,aim.eai.amd.com/profile-origin=derived' || return 1

    while IFS=$'\t' read -r resource scope_namespace name uid owners; do
        [[ "${resource}" == "resource" ]] && continue
        case "${resource}" in
            aimprofiles|aimclusterprofiles)
                verify_profile "${resource}" "${scope_namespace}" "${name}" || return 1
                ;;
            aimmodels|aimclustermodels|aimprofilesets|aimclusterprofilesets|aimprofilecaches)
                verify_ready_status "${resource}" "${scope_namespace}" "${name}" || return 1
                ;;
        esac
    done < "${snapshot}"

    verify_service upgrade-namespace-service upgrade-namespace-source Namespace || return 1
    verify_service upgrade-cluster-service upgrade-cluster-source Cluster || return 1
}

for ((attempt = 1; attempt <= attempts; attempt++)); do
    echo "Convergence check ${attempt}/${attempts}"
    if verify_all; then
        break
    fi
    if ((attempt == attempts)); then
        echo "resources did not converge within $((attempts * interval)) seconds" >&2
        exit 1
    fi
    sleep "${interval}"
done

echo "Resources converged; requiring ${stable_checks} consecutive stable checks"
for ((check = 1; check <= stable_checks; check++)); do
    sleep "${interval}"
    echo "Stability check ${check}/${stable_checks}"
    verify_all
done

echo "Upgrade compatibility checks passed"
