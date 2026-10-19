#!/usr/bin/env bash

set -euo pipefail

report="${1:?usage: run-bridge-migration.sh REPORT_DIRECTORY BASELINE_DIRECTORY AFTER_UPGRADE_DIRECTORY}"
baseline="${2:?usage: run-bridge-migration.sh REPORT_DIRECTORY BASELINE_DIRECTORY AFTER_UPGRADE_DIRECTORY}"
after_upgrade="${3:?usage: run-bridge-migration.sh REPORT_DIRECTORY BASELINE_DIRECTORY AFTER_UPGRADE_DIRECTORY}"
namespace="${UPGRADE_NAMESPACE:-aim-upgrade-compatibility}"
inline_namespace="${UPGRADE_INLINE_NAMESPACE:-aim-upgrade-inline-custom}"
migration_binary="${MIGRATION_BINARY:?MIGRATION_BINARY must name the built aim-template-migration binary}"
plan_directory="${report}/migration-plan"
plan="${plan_directory}/plan.json"

resource_value() {
    local cohort="$1"
    local role="$2"
    local field="$3"
    jq -er ".resources.${role}.${field}" "${baseline}/${cohort}.json"
}

capture_desired_state() {
    local destination="$1"

    kubectl get \
        aimservices,aimtemplatecaches,aimartifacts,aimprofiles,aimprofilecaches,inferenceservices,pvc \
        -n "${namespace}" -o json |
        jq -S '[
            .items[] | {
                apiVersion,
                kind,
                metadata: {
                    name: .metadata.name,
                    uid: .metadata.uid,
                    generation: (.metadata.generation // 0),
                    labels: (.metadata.labels // {}),
                    annotations: (.metadata.annotations // {}),
                    ownerReferences: (.metadata.ownerReferences // []),
                    finalizers: (.metadata.finalizers // [])
                },
                spec: (.spec // {})
            }
        ] | sort_by(.apiVersion, .kind, .metadata.name)' >"${destination}"
}

capture_dedicated_state() {
    local destination="$1"
    local service cache artifact claim inference_service

    service="$(resource_value dedicated service name)"
    cache="$(resource_value dedicated templateCache name)"
    artifact="$(resource_value dedicated artifact name)"
    claim="$(resource_value dedicated persistentVolumeClaim name)"
    inference_service="$(resource_value dedicated inferenceService name)"

    {
        kubectl get aimservice "${service}" -n "${namespace}" -o json
        kubectl get aimtemplatecache "${cache}" -n "${namespace}" -o json
        kubectl get aimartifact "${artifact}" -n "${namespace}" -o json
        kubectl get pvc "${claim}" -n "${namespace}" -o json
        kubectl get inferenceservice "${inference_service}" -n "${namespace}" -o json
    } | jq -s -S '[
        .[] | {
            apiVersion,
            kind,
            metadata: {
                name: .metadata.name,
                uid: .metadata.uid,
                generation: (.metadata.generation // 0),
                labels: (.metadata.labels // {}),
                annotations: (.metadata.annotations // {}),
                ownerReferences: (.metadata.ownerReferences // []),
                finalizers: (.metadata.finalizers // [])
            },
            spec: (.spec // {})
        }
    ] | sort_by(.apiVersion, .kind, .metadata.name)' >"${destination}"
}

capture_inference_service() {
    local name="$1"
    local destination="$2"

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
    local inference_service="$1"
    local destination="$2"

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

capture_operator_pods() {
    local destination="$1"

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
        ] | sort_by(.name)' >"${destination}"
}

mkdir -p "${report}"
if [[ -e "${plan_directory}" ]]; then
    echo "migration plan directory already exists: ${plan_directory}" >&2
    exit 1
fi

"${migration_binary}" plan \
    --namespace "${namespace}" \
    --runtime-projection-mode Both \
    --output-dir "${plan_directory}" |
    tee "${report}/migration-plan-command.txt"

jq -e --arg namespace "${namespace}" '
    .documentVersion == "aim-template-migration/v2" and
    .runtimeProjectionMode == "Both" and
    .selectedNamespaces == [$namespace] and
    (.units | length) == 2 and
    ([.units[] | select(.id == ($namespace + "/bridge-shared"))] | length) == 1 and
    ([.units[] | select(.id == ($namespace + "/bridge-dedicated"))] | length) == 1 and
    ([.units[] | select(.id | contains("bridge-inline-custom"))] | length) == 0 and
    (.units[] | select(.id == ($namespace + "/bridge-shared")) |
        .outcome == "automatic" and
        (.manualOutcomes | length) == 0 and
        (.proposedOperations | length) == 4 and
        [.proposedOperations[] | [.action, .resource.kind]] == [
            ["create", "AIMProfile"],
            ["create", "AIMProfileCache"],
            ["patch", "AIMArtifact"],
            ["patch", "AIMService"]
        ]) and
    (.units[] | select(.id == ($namespace + "/bridge-dedicated")) |
        .outcome == "manual" and
        (.manualOutcomes | length) == 1 and
        .manualOutcomes[0].code == "DedicatedHandoffRequiresQuiescence" and
        (.proposedOperations | length) == 0)
' "${plan}" >/dev/null

if [[ "$(stat -c '%a' "${plan_directory}")" != "700" ]]; then
    echo "migration plan directory is not mode 0700" >&2
    exit 1
fi
if [[ "$(stat -c '%a' "${plan}")" != "600" ]]; then
    echo "migration plan is not mode 0600" >&2
    exit 1
fi

shopt -s nullglob
evidence_files=("${plan_directory}"/dedicated-handoff-*.json)
if [[ "${#evidence_files[@]}" != "1" ]]; then
    echo "expected exactly one Dedicated handoff evidence file, found ${#evidence_files[@]}" >&2
    exit 1
fi
evidence="${evidence_files[0]}"
if [[ "$(stat -c '%a' "${evidence}")" != "600" ]]; then
    echo "Dedicated handoff evidence is not mode 0600" >&2
    exit 1
fi

jq -e \
    --arg unit "${namespace}/bridge-dedicated" \
    --arg service_uid "$(resource_value dedicated service uid)" \
    --arg cache_uid "$(resource_value dedicated templateCache uid)" \
    --arg artifact_uid "$(resource_value dedicated artifact uid)" \
    --arg claim_uid "$(resource_value dedicated persistentVolumeClaim uid)" '
    .documentVersion == "aim-template-migration-dedicated-handoff-evidence/v1" and
    .unitId == $unit and
    .manualReason.code == "DedicatedHandoffRequiresQuiescence" and
    (.runbookReference | endswith("#manual-dedicated-handoff")) and
    .service.identity.uid == $service_uid and
    .sourceTemplateCache.identity.uid == $cache_uid and
    .sourceArtifact.identity.uid == $artifact_uid and
    .persistentVolumeClaim.identity.uid == $claim_uid and
    .persistentVolumeClaim.persistentVolumePhase == "Bound"
' "${evidence}" >/dev/null

capture_desired_state "${report}/before-dry-run.json"
"${migration_binary}" apply --plan "${plan}" |
    tee "${report}/migration-dry-run-command.txt"
jq -e --arg unit "${namespace}/bridge-shared" '
    .mode == "dry-run" and
    .summary == {
        succeeded: 1,
        failed: 0,
        manual: 1,
        skipped: 0
    } and
    .manualUnitsSkipped == 1 and
    (.operations | length) == 4 and
    all(.operations[];
        .unitId == $unit and
        .outcome == "dry-run" and
        .attempted == true and
        .completed == true)
' "${plan_directory}/apply-result.json" >/dev/null
capture_desired_state "${report}/after-dry-run.json"
diff -u "${report}/before-dry-run.json" "${report}/after-dry-run.json"
mv "${plan_directory}/apply-result.json" "${report}/apply-result-dry-run.json"

capture_dedicated_state "${report}/dedicated-before-commit.json"
"${migration_binary}" apply \
    --plan "${plan}" \
    --commit |
    tee "${report}/migration-commit-command.txt"
jq -e --arg shared "${namespace}/bridge-shared" --arg dedicated "${namespace}/bridge-dedicated" '
    .mode == "commit" and
    .summary == {
        succeeded: 1,
        failed: 0,
        manual: 1,
        skipped: 0
    } and
    .manualUnitsSkipped == 1 and
    (.units[] | select(.unitId == $shared) | .outcome == "succeeded") and
    (.units[] | select(.unitId == $dedicated) | .outcome == "manual") and
    (.operations | length) == 6 and
    all(.operations[];
        .unitId == $shared and
        .attempted == true and
        .completed == true and
        (.error // "") == "") and
    [.operations[] | [.action, .resource.kind]] == [
        ["create", "AIMProfile"],
        ["create", "AIMProfileCache"],
        ["verify-shared-adoption", "AIMProfileCache"],
        ["patch", "AIMArtifact"],
        ["patch", "AIMService"],
        ["verify-cutover", "AIMService"]
    ]
' "${plan_directory}/apply-result.json" >/dev/null
cp "${plan_directory}/apply-result.json" "${report}/apply-result-commit.json"
capture_dedicated_state "${report}/dedicated-after-commit.json"
diff -u "${report}/dedicated-before-commit.json" "${report}/dedicated-after-commit.json"

shared_service_uid="$(resource_value shared service uid)"
shared_service_generation="$(resource_value shared service generation)"
source_cache_name="$(resource_value shared templateCache name)"
source_cache_uid="$(resource_value shared templateCache uid)"
source_artifact_name="$(resource_value shared artifact name)"
source_artifact_uid="$(resource_value shared artifact uid)"
source_claim_name="$(resource_value shared persistentVolumeClaim name)"
source_claim_uid="$(resource_value shared persistentVolumeClaim uid)"
inference_service_name="$(resource_value shared inferenceService name)"
inference_service_uid="$(resource_value shared inferenceService uid)"
shared_service="$(kubectl get aimservice bridge-shared -n "${namespace}" -o json)"

jq -e \
    --arg uid "${shared_service_uid}" \
    --argjson expected_generation "$((shared_service_generation + 1))" '
    .metadata.uid == $uid and
    .metadata.generation == $expected_generation and
    .status.observedGeneration == .metadata.generation and
    .status.status == "Running" and
    .status.reconcilerPipeline == "profile" and
    .spec.profile.name == "bridge-template" and
    .spec.template == null and
    all(.status.conditions[]?; .type != "MigrationRequired") and
    .metadata.annotations["aim.eai.amd.com/reconciler-pipeline"] == "profile"
' <<<"${shared_service}" >/dev/null

target_cache_name="$(jq -er '.status.cache.profileCacheRef.name' <<<"${shared_service}")"
target_cache_uid="$(jq -er '.status.cache.profileCacheRef.uid' <<<"${shared_service}")"
target_cache_count="$(
    kubectl get aimprofilecache -n "${namespace}" \
        -l "aim.eai.amd.com/service=bridge-shared" -o json |
        jq '.items | length'
)"
if [[ "${target_cache_count}" != "1" ]]; then
    echo "expected one migrated Shared AIMProfileCache, found ${target_cache_count}" >&2
    exit 1
fi
kubectl get aimprofile bridge-template -n "${namespace}" -o json |
    jq -e '
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        .status.status == "Ready" and
        any(.status.conditions[]?;
            .type == "RuntimeProjected" and .status == "True")
    ' >/dev/null
kubectl get aimprofilecache "${target_cache_name}" -n "${namespace}" -o json |
    jq -e \
        --arg uid "${target_cache_uid}" \
        --arg artifact "${source_artifact_name}" \
        --arg artifact_uid "${source_artifact_uid}" \
        --arg claim "${source_claim_name}" '
        .metadata.uid == $uid and
        .metadata.deletionTimestamp == null and
        .metadata.generation == .status.observedGeneration and
        .spec.profileName == "bridge-template" and
        .spec.mode == "Shared" and
        .status.status == "Ready" and
        (.status.artifacts | length) == 1 and
        any(.status.artifacts[]?;
            .name == $artifact and
            .uid == $artifact_uid and
            .persistentVolumeClaim == $claim and
            .status == "Ready")
    ' >/dev/null

if [[ "$(kubectl get aimtemplatecache "${source_cache_name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')" != "${source_cache_uid}" ]]; then
    echo "predecessor Shared AIMTemplateCache was replaced" >&2
    exit 1
fi
kubectl get aimartifact "${source_artifact_name}" -n "${namespace}" -o json |
    jq -e --arg uid "${source_artifact_uid}" --arg claim "${source_claim_name}" '
        .metadata.uid == $uid and
        .status.status == "Ready" and
        .status.persistentVolumeClaim == $claim and
        .metadata.annotations["aim.eai.amd.com/eviction-protected"] == "true"
    ' >/dev/null
if [[ "$(kubectl get pvc "${source_claim_name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')" != "${source_claim_uid}" ]]; then
    echo "predecessor Shared artifact PVC was replaced" >&2
    exit 1
fi
source_claim_count="$(
    kubectl get pvc -n "${namespace}" -o json |
        jq --arg artifact "${source_artifact_name}" '[
            .items[] |
            select(any(.metadata.ownerReferences[]?;
                .kind == "AIMArtifact" and .name == $artifact))
        ] | length'
)"
if [[ "${source_claim_count}" != "1" ]]; then
    echo "expected one predecessor Shared artifact PVC after migration, found ${source_claim_count}" >&2
    exit 1
fi

capture_inference_service "${inference_service_name}" "${report}/after-migration-inferenceservice.json"
diff -u \
    "${after_upgrade}/shared-inferenceservice.json" \
    "${report}/after-migration-inferenceservice.json"
if [[ "$(jq -er '.uid' "${report}/after-migration-inferenceservice.json")" != "${inference_service_uid}" ]]; then
    echo "Shared InferenceService UID changed during migration" >&2
    exit 1
fi
capture_predictor_pods "${inference_service_name}" "${report}/after-migration-predictor-pods.json"
diff -u \
    "${after_upgrade}/shared-predictor-pods.json" \
    "${report}/after-migration-predictor-pods.json"

kubectl get --raw \
    "/api/v1/namespaces/${namespace}/services/http:${inference_service_name}-predictor:80/proxy/v1/models" \
    >"${report}/shared-models-after-migration.json"
jq -e '.data | type == "array" and length > 0' \
    "${report}/shared-models-after-migration.json" >/dev/null

inline_model_name="$(resource_value inline-custom model name)"
kubectl get aimmodel "${inline_model_name}" -n "${inline_namespace}" -o json |
    jq -S '{
        uid: .metadata.uid,
        spec: .spec,
        labels: (.metadata.labels // {}),
        annotations: (.metadata.annotations // {}),
        ownerReferences: (.metadata.ownerReferences // [])
    }' >"${report}/inline-custom-model-after-migration.json"
diff -u \
    "${after_upgrade}/inline-custom-model.json" \
    "${report}/inline-custom-model-after-migration.json"

capture_operator_pods "${report}/operator-after-migration.json"
diff -u \
    "${after_upgrade}/candidate-operator-pods.json" \
    "${report}/operator-after-migration.json"

jq -e '
    .apiVersion == "aim.eai.amd.com/v1alpha2" and
    .metadata.deletionTimestamp == null and
    ((.metadata.finalizers // []) | index("aim.eai.amd.com/cache-cleanup") != null) and
    .status.reconcilerPipeline == "profile" and
    .spec.profile.name == "bridge-template" and
    .spec.template == null
' <<<"${shared_service}" >/dev/null

kubectl delete aimservice bridge-shared -n "${namespace}" --wait=false |
    tee "${report}/shared-service-delete.txt"
if ! kubectl wait --for=delete aimservice/bridge-shared -n "${namespace}" --timeout=120s; then
    kubectl get aimservice bridge-shared -n "${namespace}" -o yaml \
        >"${report}/shared-service-delete-stuck.yaml" 2>&1 || true
    echo "migrated Shared AIMService finalization exceeded 120 seconds" >&2
    exit 1
fi
kubectl get aimprofilecache "${target_cache_name}" -n "${namespace}" -o json |
    jq -e --arg uid "${target_cache_uid}" '
        .metadata.uid == $uid and
        .metadata.deletionTimestamp == null and
        .status.status == "Ready"
    ' >/dev/null

capture_operator_pods "${report}/operator-after-delete.json"
diff -u \
    "${after_upgrade}/candidate-operator-pods.json" \
    "${report}/operator-after-delete.json"

echo "Shared bridge migration converged and finalized without an operator restart"
