#!/usr/bin/env bash
#
# Checks whether a cluster is ready to upgrade to an AIM Engine release without
# the template serving path. It only reads from the cluster (get and list) with
# your kubectl credentials, and needs kubectl and jq.
#
# Usage: upgrade-preflight.sh
#
# Exits 0 when every blocking check passes or the cluster is already upgraded,
# and 1 when any check fails. Each failure names the check, the object and the
# section of the upgrade guide that fixes it. Rerun the script after each fix
# until it passes.

# The jq filters use jq variables such as $object, which must stay unexpanded.
# shellcheck disable=SC2016

set -euo pipefail

guide="https://github.com/amd-enterprise-ai/aim-engine/blob/main/docs/docs/admin/upgrading.md"
minimum_version="0.2.7"
template_crds=(aimservicetemplates.aim.eai.amd.com aimclusterservicetemplates.aim.eai.amd.com
  aimtemplatecaches.aim.eai.amd.com)
failures=0
manual_checks=0

# Prints a FAIL line for each "<object>: <problem>" line and counts them.
report() {
  local check="$1" section="$2" problems="$3"
  if [[ -n "${problems}" ]]; then
    sed "s|^|FAIL [${check}] |; s|\$|. See #${section}|" <<<"${problems}"
    failures=$((failures + $(wc -l <<<"${problems}")))
  fi
}

# Reports problems with template objects like report, or as MANUAL lines when
# the template CRDs are kept, because the objects are then removed after the
# upgrade.
report_template_problems() {
  local problem
  if [[ "${template_crds_kept}" != true ]]; then
    report "$@"
  elif [[ -n "$3" ]]; then
    while IFS= read -r problem; do
      manual "$1" remove-the-template-crds "${problem}"
    done <<<"$3"
  fi
}

# Fails check $1 for each list that failed since the last call.
report_list_errors() {
  report "$1" run-the-upgrade-preflight "$(<"${scratch}/list-errors")"
  : >"${scratch}/list-errors"
}

# Prints a MANUAL line for something the script can't check, which the admin
# confirms by hand. It doesn't block.
manual() {
  echo "MANUAL [$1] $3. See #$2"
  manual_checks=$((manual_checks + 1))
}

# Prints "<object>: <problem>" for each problem the jq filter $1 emits for an
# item of the JSON lists on stdin. The object is "<Kind> <namespace>/<name>",
# or "<Kind> <name>" when cluster-scoped. Further arguments go to jq.
problems() {
  jq -r "${@:2}" '.items[]
    | "\(.kind) \(if .metadata.namespace then .metadata.namespace + "/" else "" end)\(.metadata.name)" as $object
    | '"$1"' | "\($object): \(.)"'
}

# Prints all objects of a resource type as a JSON list. A resource type the
# cluster doesn't have counts as no objects. When kubectl fails, it prints an
# empty list and records the resource and kubectl's error, which
# report_list_errors reports as a failure, so an unreadable resource never passes.
list() {
  local items error
  if ! grep -qxF "$1" <<<"${api_resources}"; then
    echo '{"items": []}'
  elif items="$(kubectl get "$1" --all-namespaces -o json 2>"${scratch}/$1.stderr")"; then
    echo "${items}"
  else
    error="$(<"${scratch}/$1.stderr")"
    echo "$1: cannot list it: ${error//$'\n'/ }" >>"${scratch}/list-errors"
    echo '{"items": []}'
  fi
}

# Prints the X.Y.Z version from an image reference's tag, or nothing.
image_version() {
  local reference="${1%@*}"
  reference="${reference##*/}"
  if [[ "${reference}" == *:* && "${reference##*:}" =~ ^v?([0-9]+\.[0-9]+\.[0-9]+) ]]; then
    echo "${BASH_REMATCH[1]}"
  fi
}

# Succeeds when X.Y.Z version $1 is at least version $2.
version_at_least() {
  local -a have want
  local i
  IFS=. read -r -a have <<<"$1"
  IFS=. read -r -a want <<<"$2"
  for i in 0 1 2; do
    if ((have[i] != want[i])); then
      ((have[i] > want[i]))
      return
    fi
  done
}

check_minimum_version() {
  local found deployments images object image version
  found="$(problems "select(.status.reconcilerPipeline == null)
    | \"has no status.reconcilerPipeline, so v${minimum_version} or later has not reconciled it yet\"" \
    <<<"${services}")"
  report minimum-version minimum-version "${found}"

  deployments="$(kubectl get deployments --all-namespaces \
    --field-selector metadata.name=aim-engine-controller-manager -o json)"
  images="$(jq -r '.items[] | ["Deployment \(.metadata.namespace)/\(.metadata.name)",
    (.spec.template.spec.containers[] | select(.name == "manager") | .image)] | @tsv' <<<"${deployments}")"
  if [[ -z "${images}" ]]; then
    manual minimum-version minimum-version "Deployment aim-engine-controller-manager: not found in any namespace,\
 so confirm by hand that the operator runs v${minimum_version} or later"
    return
  fi
  while IFS=$'\t' read -r object image; do
    version="$(image_version "${image}")"
    if [[ -z "${version}" ]]; then
      manual minimum-version minimum-version "${object}: cannot read a version from the image tag of ${image},\
 so confirm by hand that it runs v${minimum_version} or later"
    elif ! version_at_least "${version}" "${minimum_version}"; then
      report minimum-version minimum-version "${object}: runs v${version}, upgrade to v${minimum_version} or later first"
    fi
  done <<<"${images}"
}

# v0.2.7 records the profile pipeline before it rewrites the InferenceService,
# and the old pods keep mounting the PVC of a Dedicated template cache until
# the rollout ends. Deleting the template cache before then leaves the next pod
# unable to mount it. So a service on the profile pipeline still has template
# state while its InferenceService lacks the profile label, or while a
# predictor pod without that label hasn't finished. A service whose
# migration-preserve annotations match its generation and its InferenceService's
# UID keeps that InferenceService and its pods unchanged, without the profile
# label, so neither counts for it. An unhealthy service, such as one without an
# InferenceService or with Pending or crash-looping pods, has no template state
# and passes.
check_template_pipeline_services() {
  local found pods
  pods="$(list pods)"
  found="$(echo "${services}" | problems '. as $service
    | [$isvcs[0].items[] | select(any(.metadata.ownerReferences[]?; .uid == $service.metadata.uid))][0] as $isvc
    | ($isvc != null
        and .metadata.annotations["aim.eai.amd.com/migration-preserve-inference-service-uid"] == $isvc.metadata.uid
        and .metadata.annotations["aim.eai.amd.com/migration-preserve-inference-service-generation"]
          == "\(.metadata.generation)") as $pinned
    | if (.spec.profile != null or .metadata.annotations["aim.eai.amd.com/reconciler-pipeline"] == "profile")
        and .status.reconcilerPipeline == "profile" | not then
        "is not on the profile pipeline (status.reconcilerPipeline: \(.status.reconcilerPipeline // "unset"))"
      elif $isvc == null or $pinned then empty
      elif $isvc.metadata.labels["aim.eai.amd.com/profile"] == null then
        "has not finished moving (InferenceService \($isvc.metadata.name) still runs the template)"
      else $pods[0].items[]
        | select(.metadata.namespace == $isvc.metadata.namespace
          and .metadata.labels["serving.kserve.io/inferenceservice"] == $isvc.metadata.name
          and .metadata.labels["aim.eai.amd.com/profile"] == null
          and (.status.phase | IN("Succeeded", "Failed") | not))
        | "has not finished moving (predictor pod \(.metadata.name) still runs the template)" end' \
    --slurpfile isvcs <(list inferenceservices.serving.kserve.io) --slurpfile pods <(echo "${pods}"))"
  report template-pipeline-services move-template-services "${found}"

  found="$(echo "${pods}" | problems 'select(.status.phase | IN("Succeeded", "Failed") | not)
    | .metadata.namespace as $namespace
    | (.spec.volumes[]?.persistentVolumeClaim.claimName // empty) as $claim
    | $artifacts[0].items[]
    | select(.metadata.namespace == $namespace and .status.persistentVolumeClaim == $claim)
    | .metadata.name as $artifact
    | .metadata.ownerReferences[]? | select(.kind == "AIMTemplateCache")
    | "mounts PVC \($claim) of AIMArtifact \($artifact), which AIMTemplateCache \(.name) owns"' \
    --slurpfile artifacts <(list aimartifacts.aim.eai.amd.com))"
  report template-pipeline-services move-template-services "${found}"
}

check_unservable_services() {
  local found
  found="$(problems '
    (select(.spec.model.custom != null) | "uses spec.model.custom"),
    (select(.spec.template != null and .spec.model == null and .spec.profile == null)
      | "uses spec.template without spec.model or spec.profile")' <<<"${services}")"
  report unservable-services replace-unservable-services "${found}"
}

check_models_without_native_path() {
  local found
  found="$(printf '%s\n' "${models}" "${cluster_models}" | problems '
    (select(.spec.custom != null) | "uses spec.custom"),
    (select((.spec.customTemplates // []) | length > 0) | "uses spec.customTemplates"),
    (select((.spec.modelSources // []) | length > 0) | "uses spec.modelSources"),
    (select(.spec.image != null and (.spec.aimId // "") != "") | "sets spec.aimId on an image model"),
    (select(.metadata.annotations["aim.eai.amd.com/legacy-auto-custom-spec"] != null)
      | "was generated for an AIMService with spec.model.custom"),
    (select(.spec.image != null and .spec.discovery.extractMetadata == false)
      | "sets spec.discovery.extractMetadata: false on an image model")')"
  report models-without-native-path replace-models-without-a-native-path "${found}"
}

check_template_objects() {
  local found
  found="$(for resource in "${template_crds[@]}"; do
    list "${resource}"
  done | problems '"still exists"')"
  report_template_problems template-objects delete-template-objects "${found}"
}

check_template_finalizers() {
  local found
  found="$(printf '%s\n' "${services}" "$(list aimtemplatecaches.aim.eai.amd.com)" | problems '.metadata.finalizers[]?
    | select(IN("aim.eai.amd.com/artifactcleanup", "aim.eai.amd.com/template-cache-cleanup",
      "aim.eai.amd.com/template-cache.cleanup"))
    | "has the finalizer \(.)"')"
  report_template_problems template-finalizers delete-template-objects "${found}"
}

check_stale_owner_references() {
  local lists found
  lists="$(for resource in inferenceservices.serving.kserve.io httproutes.gateway.networking.k8s.io \
    scaledobjects.keda.sh persistentvolumeclaims jobs.batch configmaps \
    aimartifacts.aim.eai.amd.com aimprofiles.aim.eai.amd.com aimprofilecaches.aim.eai.amd.com; do
    list "${resource}"
  done)"
  found="$(problems '"kubectl patch \(.kind | ascii_downcase) \(.metadata.name) -n \(.metadata.namespace)" as $patch
    | .metadata.ownerReferences // [] | to_entries[]
    | select(.value.apiVersion == "aim.eai.amd.com/v1alpha1"
      and (.value.kind | IN("AIMService", "AIMModel", "AIMClusterModel")))
    | ([{op: "replace", path: "/metadata/ownerReferences/\(.key)/apiVersion", value: "aim.eai.amd.com/v1alpha2"}]
      | tojson | @sh) as $operations
    | "is owned by v1alpha1 \(.value.kind) \(.value.name); rewrite the reference with:"
      + " \($patch) --type json -p \($operations)"' <<<"${lists}")"
  report stale-owner-references remove-stale-v1alpha1-owner-references "${found}"

  found="$(problems '"kubectl patch \(.kind | ascii_downcase) \(.metadata.name) -n \(.metadata.namespace)" as $patch
    | .metadata.ownerReferences // [] | to_entries[]
    | select(.value.apiVersion == "aim.eai.amd.com/v1alpha1"
      and (.value.kind | IN("AIMServiceTemplate", "AIMClusterServiceTemplate", "AIMTemplateCache")))
    | ([{op: "remove", path: "/metadata/ownerReferences/\(.key)"}] | tojson | @sh) as $operations
    | "is owned by v1alpha1 \(.value.kind) \(.value.name); delete it with its owner, or remove the reference with:"
      + " \($patch) --type json -p \($operations)"' <<<"${lists}")"
  report_template_problems stale-owner-references remove-stale-v1alpha1-owner-references "${found}"
}

report_dedicated_caches() {
  printf '%s\n' "$(list aimtemplatecaches.aim.eai.amd.com)" "$(list aimartifacts.aim.eai.amd.com)" | jq -rs '
    . as [$caches, $artifacts]
    | $caches.items[] | select(.spec.mode == "Dedicated") | . as $cache
    | $artifacts.items[] | select(any(.metadata.ownerReferences[]?; .uid == $cache.metadata.uid))
    | "INFO [dedicated-template-caches] AIMTemplateCache \($cache.metadata.namespace)/\($cache.metadata.name):"
      + " AIMArtifact \(.metadata.name) uses \(.status.allocatedSize // .spec.size // "an unknown size"),"
      + " and the profile cache downloads it again. See #cache-outcomes"'
}

run_check() {
  local name="$1" failures_before="${failures}" manual_before="${manual_checks}"
  "check_${name//-/_}"
  report_list_errors "${name}"
  if ((failures == failures_before && manual_checks == manual_before)); then
    echo "ok   [${name}]"
  fi
}

for tool in kubectl jq; do
  if ! command -v "${tool}" >/dev/null; then
    echo "upgrade-preflight: ${tool} is required but was not found on PATH" >&2
    exit 2
  fi
done

scratch="$(mktemp -d)"
trap 'rm -rf "${scratch}"' EXIT
: >"${scratch}/list-errors"

# The new release's AIMService CRD stops serving v1alpha1, and the new operator
# clears status.reconcilerPipeline, so the checks below would fail every
# service of an upgraded cluster.
crds="$(kubectl get customresourcedefinitions aimservices.aim.eai.amd.com "${template_crds[@]}" \
  --ignore-not-found -o json)"
if jq -e '.items[] | select(.metadata.name == "aimservices.aim.eai.amd.com")
    | any(.spec.versions[]; .name == "v1alpha1" and .served) | not' <<<"${crds}" >/dev/null; then
  echo "Already upgraded: the AIMService CRD no longer serves aim.eai.amd.com/v1alpha1, so there is nothing to check."
  exit 0
fi
template_crds_kept="$(jq '[.items[] | select(.metadata.annotations["helm.sh/resource-policy"] == "keep")
  | .metadata.name] as $kept | all($ARGS.positional[]; IN($kept[]))' --args "${template_crds[@]}" <<<"${crds}")"

api_resources="$(kubectl api-resources -o name)"
services="$(kubectl get aimservices.aim.eai.amd.com --all-namespaces -o json)"
models="$(kubectl get aimmodels.aim.eai.amd.com --all-namespaces -o json)"
cluster_models="$(kubectl get aimclustermodels.aim.eai.amd.com -o json)"

echo "Sections (#...) refer to ${guide}"
if [[ "${template_crds_kept}" == true ]]; then
  echo "The template CRDs have helm.sh/resource-policy=keep, so template objects don't block:\
 remove them after the upgrade. See #remove-the-template-crds"
fi
run_check minimum-version
run_check template-pipeline-services
run_check unservable-services
run_check models-without-native-path
run_check template-objects
run_check template-finalizers
run_check stale-owner-references
report_dedicated_caches
report_list_errors dedicated-template-caches
manual v1alpha1-clients update-v1alpha1-manifests "AIMService, AIMModel and AIMClusterModel stop serving\
 aim.eai.amd.com/v1alpha1: check GitOps repositories and scripts for such manifests"

if ((failures > 0)); then
  echo "${failures} blocking problem(s) found. Fix them on the current release and rerun before upgrading."
  exit 1
fi
echo "All blocking checks passed."
