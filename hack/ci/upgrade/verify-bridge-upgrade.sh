#!/usr/bin/env bash

set -euo pipefail

baseline="${1:?usage: verify-bridge-upgrade.sh BASELINE_DIRECTORY OUTPUT_DIRECTORY}"
output="${2:?usage: verify-bridge-upgrade.sh BASELINE_DIRECTORY OUTPUT_DIRECTORY}"
bridge_namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
inline_namespace="${UPGRADE_INLINE_NAMESPACE:-aim-upgrade-inline-custom}"

mkdir -p "${output}"

resource_value() {
    local cohort="$1"
    local role="$2"
    local field="$3"
    jq -er ".resources.${role}.${field}" "${baseline}/${cohort}.json"
}

require_uid() {
    local resource="$1"
    local namespace="$2"
    local name="$3"
    local expected_uid="$4"
    local actual_uid

    actual_uid="$(kubectl get "${resource}" "${name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
    if [[ "${actual_uid}" != "${expected_uid}" ]]; then
        echo "${resource}/${name} was replaced: expected UID ${expected_uid}, got ${actual_uid}" >&2
        return 1
    fi
}

capture_inference_service() {
    local namespace="$1"
    local name="$2"
    local destination="$3"

    kubectl get inferenceservice "${name}" -n "${namespace}" -o json |
        jq -S '{
            uid: .metadata.uid,
            generation: .metadata.generation,
            spec: .spec,
            labels: (.metadata.labels // {}),
            annotations: (.metadata.annotations // {}),
            ownerReferences: (.metadata.ownerReferences // []),
            finalizers: (.metadata.finalizers // [])
        }' >"${destination}"
}

capture_predictor_pods() {
    local namespace="$1"
    local inference_service="$2"
    local destination="$3"

    kubectl get pod -n "${namespace}" \
        -l "serving.kserve.io/inferenceservice=${inference_service}" -o json |
        jq -S '[
            .items[] | {
                name: .metadata.name,
                uid: .metadata.uid,
                creationTimestamp: .metadata.creationTimestamp,
                restartCounts: [
                    .status.containerStatuses[]? | {
                        name: .name,
                        restartCount: .restartCount
                    }
                ]
            }
        ] | sort_by(.name)' >"${destination}"
}

verify_cohort() {
    local cohort="$1"
    local namespace="$2"
    local service_name model_name template_name cache_name artifact_name claim_name inference_service_name
    local service_uid model_uid template_uid cache_uid artifact_uid claim_uid inference_service_uid
    local service cache artifact inference_service normalized_before normalized_after
    local cache_count artifact_count claim_count

    service_name="$(resource_value "${cohort}" service name)"
    model_name="$(resource_value "${cohort}" model name)"
    template_name="$(resource_value "${cohort}" template name)"
    cache_name="$(resource_value "${cohort}" templateCache name)"
    artifact_name="$(resource_value "${cohort}" artifact name)"
    claim_name="$(resource_value "${cohort}" persistentVolumeClaim name)"
    inference_service_name="$(resource_value "${cohort}" inferenceService name)"
    service_uid="$(resource_value "${cohort}" service uid)"
    model_uid="$(resource_value "${cohort}" model uid)"
    template_uid="$(resource_value "${cohort}" template uid)"
    cache_uid="$(resource_value "${cohort}" templateCache uid)"
    artifact_uid="$(resource_value "${cohort}" artifact uid)"
    claim_uid="$(resource_value "${cohort}" persistentVolumeClaim uid)"
    inference_service_uid="$(resource_value "${cohort}" inferenceService uid)"

    require_uid aimservice "${namespace}" "${service_name}" "${service_uid}"
    require_uid aimmodel "${namespace}" "${model_name}" "${model_uid}"
    require_uid aimservicetemplate "${namespace}" "${template_name}" "${template_uid}"
    require_uid aimtemplatecache "${namespace}" "${cache_name}" "${cache_uid}"
    require_uid aimartifact "${namespace}" "${artifact_name}" "${artifact_uid}"
    require_uid pvc "${namespace}" "${claim_name}" "${claim_uid}"
    require_uid inferenceservice "${namespace}" "${inference_service_name}" "${inference_service_uid}"

    cache_count="$(
        kubectl get aimtemplatecache -n "${namespace}" \
            -l "aim.eai.amd.com/service=${service_name}" -o json |
            jq '.items | length'
    )"
    if [[ "${cache_count}" != "1" ]]; then
        echo "expected one source AIMTemplateCache for ${namespace}/${service_name}, found ${cache_count}" >&2
        return 1
    fi
    artifact_count="$(
        kubectl get aimartifact -n "${namespace}" \
            -l "aim.eai.amd.com/template-cache.name=${cache_name}" -o json |
            jq '.items | length'
    )"
    if [[ "${artifact_count}" != "1" ]]; then
        echo "expected one source AIMArtifact for ${namespace}/${cache_name}, found ${artifact_count}" >&2
        return 1
    fi
    claim_count="$(
        kubectl get pvc -n "${namespace}" -o json |
            jq --arg artifact "${artifact_name}" '[
                .items[] |
                select(any(.metadata.ownerReferences[]?;
                    .kind == "AIMArtifact" and .name == $artifact))
            ] | length'
    )"
    if [[ "${claim_count}" != "1" ]]; then
        echo "expected one source PVC for ${namespace}/${artifact_name}, found ${claim_count}" >&2
        return 1
    fi

    service="$(kubectl get aimservice "${service_name}" -n "${namespace}" -o json)"
    jq -e \
        --arg model "${model_name}" \
        --arg template "${template_name}" \
        --arg cache "${cache_name}" '
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        .status.status == "Running" and
        .status.reconcilerPipeline == "template" and
        .status.resolvedModel.name == $model and
        .status.resolvedTemplate.name == $template and
        .status.cache.templateCacheRef.name == $cache and
        any(.status.conditions[]?;
            .type == "MigrationRequired" and .status == "True")
    ' <<<"${service}" >/dev/null

    cache="$(kubectl get aimtemplatecache "${cache_name}" -n "${namespace}" -o json)"
    jq -e --arg artifact "${artifact_name}" --arg uid "${artifact_uid}" --arg claim "${claim_name}" '
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        .status.status == "Ready" and
        (.status.artifacts | length) == 1 and
        any(.status.artifacts[]?;
            .name == $artifact and
            .uid == $uid and
            .persistentVolumeClaim == $claim and
            .status == "Ready")
    ' <<<"${cache}" >/dev/null

    artifact="$(kubectl get aimartifact "${artifact_name}" -n "${namespace}" -o json)"
    jq -e --arg claim "${claim_name}" '
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        .status.status == "Ready" and
        .status.persistentVolumeClaim == $claim
    ' <<<"${artifact}" >/dev/null
    kubectl get pvc "${claim_name}" -n "${namespace}" -o json |
        jq -e '.metadata.deletionTimestamp == null and .status.phase == "Bound"' >/dev/null

    capture_inference_service \
        "${namespace}" "${inference_service_name}" \
        "${output}/${cohort}-inferenceservice.json"
    inference_service="$(kubectl get inferenceservice "${inference_service_name}" -n "${namespace}" -o json)"
    jq -e --arg service_uid "${service_uid}" '
        .metadata.generation == .status.observedGeneration and
        any(.status.conditions[]?; .type == "Ready" and .status == "True") and
        any(.metadata.ownerReferences[]?;
            .apiVersion == "aim.eai.amd.com/v1alpha2" and
            .kind == "AIMService" and
            .uid == $service_uid and
            .controller == true)
    ' <<<"${inference_service}" >/dev/null
    jq -e '
        any(.ownerReferences[]?;
            .apiVersion == "aim.eai.amd.com/v1alpha1" and
            .kind == "AIMService")
    ' "${baseline}/${cohort}-inferenceservice.json" >/dev/null

    normalized_before="${output}/${cohort}-inferenceservice-before-normalized.json"
    normalized_after="${output}/${cohort}-inferenceservice-after-normalized.json"
    jq -S '
        (.ownerReferences[]? | select(.kind == "AIMService").apiVersion) =
            "aim.eai.amd.com/normalized"
    ' "${baseline}/${cohort}-inferenceservice.json" >"${normalized_before}"
    jq -S '
        (.ownerReferences[]? | select(.kind == "AIMService").apiVersion) =
            "aim.eai.amd.com/normalized"
    ' "${output}/${cohort}-inferenceservice.json" >"${normalized_after}"
    diff -u "${normalized_before}" "${normalized_after}"

    capture_predictor_pods \
        "${namespace}" "${inference_service_name}" \
        "${output}/${cohort}-predictor-pods.json"
    diff -u \
        "${baseline}/${cohort}-predictor-pods.json" \
        "${output}/${cohort}-predictor-pods.json"
}

verify_cohort shared "${bridge_namespace}"
verify_cohort dedicated "${bridge_namespace}"
verify_cohort inline-custom "${inline_namespace}"

inline_model_name="$(resource_value inline-custom model name)"
kubectl get aimmodel "${inline_model_name}" -n "${inline_namespace}" -o json |
    jq -S '{
        uid: .metadata.uid,
        spec: .spec,
        labels: (.metadata.labels // {}),
        annotations: (.metadata.annotations // {}),
        ownerReferences: (.metadata.ownerReferences // [])
    }' >"${output}/inline-custom-model.json"
diff -u "${baseline}/inline-custom-model.json" "${output}/inline-custom-model.json"

inline_model_count="$(
    kubectl get aimmodel -n "${inline_namespace}" -o json |
        jq '[
            .items[] |
            select(any(.spec.modelSources[]?;
                .modelId == "example/bridge-inline-custom" and
                .sourceUri == "hf://example/bridge-inline-custom"))
        ] | length'
)"
if [[ "${inline_model_count}" != "1" ]]; then
    echo "expected exactly one grandfathered inline-custom AIMModel, found ${inline_model_count}" >&2
    exit 1
fi

if kubectl get aimprofile bridge-template -n "${bridge_namespace}" >/dev/null 2>&1; then
    echo "target AIMProfile bridge-template exists before migration Plan" >&2
    exit 1
fi
profile_cache_count="$(kubectl get aimprofilecache -n "${bridge_namespace}" -o json | jq '.items | length')"
if [[ "${profile_cache_count}" != "0" ]]; then
    echo "expected no AIMProfileCache before migration Plan, found ${profile_cache_count}" >&2
    exit 1
fi

operator_args="$(
    kubectl get deployment aim-engine-controller-manager -n aim-system \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].args}'
)"
if [[ "${operator_args}" != *"--runtime-projection-mode=Both"* ]]; then
    echo "candidate operator is not running with --runtime-projection-mode=Both: ${operator_args}" >&2
    exit 1
fi

kubectl get pod -n aim-system -l control-plane=controller-manager -o json |
    jq -e '
        (.items | length) == 1 and
        all(.items[];
            .metadata.deletionTimestamp == null and
            any(.status.conditions[]?; .type == "Ready" and .status == "True"))
    ' >/dev/null
kubectl get pod -n aim-system -l control-plane=controller-manager -o json |
    jq -S '[
        .items[] | {
            name: .metadata.name,
            uid: .metadata.uid,
            restartCounts: [
                .status.containerStatuses[]? | {
                    name: .name,
                    restartCount: .restartCount
                }
            ]
        }
    ] | sort_by(.name)' >"${output}/candidate-operator-pods.json"

echo "Bridge predecessor resources converged without a restart or resource poke"
