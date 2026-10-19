#!/usr/bin/env bash

set -euo pipefail

bridge_namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
inline_namespace="${UPGRADE_INLINE_NAMESPACE:-aim-upgrade-inline-custom}"
output="${1:?usage: snapshot-bridge.sh OUTPUT_DIRECTORY}"

mkdir -p "${output}"

get_single_json() {
    local resource="$1"
    local namespace="$2"
    local selector="$3"
    local list count

    list="$(kubectl get "${resource}" -n "${namespace}" -l "${selector}" -o json)"
    count="$(jq '.items | length' <<<"${list}")"
    if [[ "${count}" != "1" ]]; then
        echo "expected exactly one ${resource} in ${namespace} matching ${selector}, found ${count}" >&2
        jq -r '.items[].metadata.name' <<<"${list}" >&2
        return 1
    fi
    jq -c '.items[0]' <<<"${list}"
}

capture_cohort() {
    local cohort="$1"
    local namespace="$2"
    local service_name="$3"
    local service model template cache artifact claim inference_service pods
    local model_name template_name cache_name artifact_name claim_name inference_service_name
    local reported_artifact_uid reported_claim

    # v0.2.6 declares status.observedGeneration but does not persist it for
    # these AIM resources. The setup corpus is immutable, so terminal status
    # plus the resolved child references establishes baseline convergence.
    service="$(kubectl get aimservice "${service_name}" -n "${namespace}" -o json)"
    jq -e '
        .metadata.deletionTimestamp == null and
        .status.status == "Running" and
        (.status.resolvedModel.name | length) > 0 and
        (.status.resolvedTemplate.name | length) > 0 and
        (.status.cache.templateCacheRef.name | length) > 0
    ' <<<"${service}" >/dev/null

    model_name="$(jq -er '.status.resolvedModel.name' <<<"${service}")"
    template_name="$(jq -er '.status.resolvedTemplate.name' <<<"${service}")"
    cache_name="$(jq -er '.status.cache.templateCacheRef.name' <<<"${service}")"
    model="$(kubectl get aimmodel "${model_name}" -n "${namespace}" -o json)"
    template="$(kubectl get aimservicetemplate "${template_name}" -n "${namespace}" -o json)"
    cache="$(kubectl get aimtemplatecache "${cache_name}" -n "${namespace}" -o json)"

    jq -e '
        .metadata.deletionTimestamp == null and
        .status.status == "Ready"
    ' <<<"${model}" >/dev/null
    jq -e '
        .metadata.deletionTimestamp == null and
        .status.status == "Ready"
    ' <<<"${template}" >/dev/null
    jq -e '
        .metadata.deletionTimestamp == null and
        .status.status == "Ready" and
        (.status.artifacts | length) == 1
    ' <<<"${cache}" >/dev/null

    artifact_name="$(jq -er '[.status.artifacts[]][0].name' <<<"${cache}")"
    reported_artifact_uid="$(jq -er '[.status.artifacts[]][0].uid' <<<"${cache}")"
    reported_claim="$(jq -er '[.status.artifacts[]][0].persistentVolumeClaim' <<<"${cache}")"
    artifact="$(kubectl get aimartifact "${artifact_name}" -n "${namespace}" -o json)"
    claim_name="$(jq -er '.status.persistentVolumeClaim' <<<"${artifact}")"
    claim="$(kubectl get pvc "${claim_name}" -n "${namespace}" -o json)"

    jq -e --arg uid "${reported_artifact_uid}" --arg claim "${reported_claim}" '
        .metadata.uid == $uid and
        .metadata.deletionTimestamp == null and
        .status.status == "Ready" and
        .status.persistentVolumeClaim == $claim
    ' <<<"${artifact}" >/dev/null
    jq -e '.metadata.deletionTimestamp == null and .status.phase == "Bound"' \
        <<<"${claim}" >/dev/null

    inference_service="$(
        get_single_json inferenceservice "${namespace}" \
            "aim.eai.amd.com/service=${service_name}"
    )"
    inference_service_name="$(jq -er '.metadata.name' <<<"${inference_service}")"
    jq -e --arg uid "$(jq -er '.metadata.uid' <<<"${service}")" '
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        any(.status.conditions[]?; .type == "Ready" and .status == "True") and
        (.metadata.labels["aim.eai.amd.com/template"] | length) > 0 and
        .metadata.labels["aim.eai.amd.com/profile"] == null and
        any(.metadata.ownerReferences[]?;
            .apiVersion == "aim.eai.amd.com/v1alpha1" and
            .kind == "AIMService" and
            .uid == $uid and
            .controller == true)
    ' <<<"${inference_service}" >/dev/null

    pods="$(kubectl get pod -n "${namespace}" \
        -l "serving.kserve.io/inferenceservice=${inference_service_name}" -o json)"
    jq -e '
        (.items | length) == 1 and
        all(.items[];
            .metadata.deletionTimestamp == null and
            any(.status.conditions[]?; .type == "Ready" and .status == "True"))
    ' <<<"${pods}" >/dev/null

    jq -S '{
        uid: .metadata.uid,
        generation: .metadata.generation,
        spec: .spec,
        labels: (.metadata.labels // {}),
        annotations: (.metadata.annotations // {}),
        ownerReferences: (.metadata.ownerReferences // []),
        finalizers: (.metadata.finalizers // [])
    }' <<<"${inference_service}" >"${output}/${cohort}-inferenceservice.json"
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
    ] | sort_by(.name)' <<<"${pods}" >"${output}/${cohort}-predictor-pods.json"

    jq -n -S \
        --arg cohort "${cohort}" \
        --arg namespace "${namespace}" \
        --argjson service "${service}" \
        --argjson model "${model}" \
        --argjson template "${template}" \
        --argjson cache "${cache}" \
        --argjson artifact "${artifact}" \
        --argjson claim "${claim}" \
        --argjson inferenceService "${inference_service}" '
        def identity($object): {
            name: $object.metadata.name,
            uid: $object.metadata.uid,
            generation: ($object.metadata.generation // 0)
        };
        {
            cohort: $cohort,
            namespace: $namespace,
            resources: {
                service: identity($service),
                model: identity($model),
                template: identity($template),
                templateCache: identity($cache),
                artifact: identity($artifact),
                persistentVolumeClaim: identity($claim),
                inferenceService: identity($inferenceService)
            }
        }
    ' >"${output}/${cohort}.json"

    kubectl get --raw \
        "/api/v1/namespaces/${namespace}/services/http:${inference_service_name}-predictor:80/proxy/v1/models" \
        >"${output}/${cohort}-models.json"
    jq -e '.data | type == "array" and length > 0' \
        "${output}/${cohort}-models.json" >/dev/null
}

capture_cohort shared "${bridge_namespace}" bridge-shared
capture_cohort dedicated "${bridge_namespace}" bridge-dedicated
capture_cohort inline-custom "${inline_namespace}" bridge-inline-custom

inline_model_name="$(jq -er '.resources.model.name' "${output}/inline-custom.json")"
inline_model="$(kubectl get aimmodel "${inline_model_name}" -n "${inline_namespace}" -o json)"
jq -e '
    .status.status == "Ready" and
    ((.metadata.ownerReferences // []) | length) == 0 and
    .metadata.annotations["aim.eai.amd.com/legacy-auto-custom-spec"] == null and
    .spec.image == "docker.io/amdenterpriseai/aim-dummy:0.2.0" and
    any(.spec.modelSources[]?;
        .modelId == "example/bridge-inline-custom" and
        .sourceUri == "hf://example/bridge-inline-custom") and
    (.spec.custom.hardware.gpu.requests // 0) == 0
' <<<"${inline_model}" >/dev/null
jq -S '{
    uid: .metadata.uid,
    spec: .spec,
    labels: (.metadata.labels // {}),
    annotations: (.metadata.annotations // {}),
    ownerReferences: (.metadata.ownerReferences // [])
}' <<<"${inline_model}" >"${output}/inline-custom-model.json"

echo "Captured bridge predecessor identities in ${output}"
