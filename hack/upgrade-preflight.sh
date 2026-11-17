#!/usr/bin/env bash
#
# Checks whether a cluster is ready to upgrade to an AIM Engine release without
# the template serving path. It only reads from the cluster (get and list) with
# your kubectl credentials, and needs kubectl and jq.
#
# Usage: upgrade-preflight.sh
#
# Exits 0 when every blocking check passes and 1 when any fails. Each failure
# names the check, the object and the section of the upgrade guide that fixes
# it. Rerun the script after each fix until it passes.

set -euo pipefail

guide="https://github.com/amd-enterprise-ai/aim-engine/blob/main/docs/docs/admin/upgrading.md"
minimum_version="0.2.7"
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

# Prints a MANUAL line for something the script can't check, which the admin
# confirms by hand. It doesn't block.
manual() {
  echo "MANUAL [$1] $3. See #$2"
  manual_checks=$((manual_checks + 1))
}

# Prints "<object>: <problem>" for each problem the jq filter $1 emits for an
# item of the JSON lists on stdin. The object is "<Kind> <namespace>/<name>",
# or "<Kind> <name>" when cluster-scoped.
problems() {
  jq -r '.items[]
    | "\(.kind) \(if .metadata.namespace then .metadata.namespace + "/" else "" end)\(.metadata.name)" as $object
    | '"$1"' | "\($object): \(.)"'
}

# Prints all objects of a resource type as a JSON list. A resource type the
# cluster doesn't have counts as no objects.
list() {
  if grep -qxF "$1" <<<"${api_resources}"; then
    kubectl get "$1" --all-namespaces -o json
  else
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

check_template_pipeline_services() {
  local found
  found="$(problems 'select((.spec.profile != null
        or .metadata.annotations["aim.eai.amd.com/reconciler-pipeline"] == "profile")
      and .status.reconcilerPipeline == "profile" | not)
    | "is not on the profile pipeline (status.reconcilerPipeline: \(.status.reconcilerPipeline // "unset"))"' \
    <<<"${services}")"
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
  found="$(for resource in aimservicetemplates aimclusterservicetemplates aimtemplatecaches; do
    list "${resource}.aim.eai.amd.com"
  done | problems '"still exists"')"
  report template-objects delete-template-objects "${found}"
}

check_template_finalizers() {
  local found
  found="$(for resource in aimservices aimtemplatecaches; do
    list "${resource}.aim.eai.amd.com"
  done | problems '.metadata.finalizers[]?
    | select(IN("aim.eai.amd.com/artifactcleanup", "aim.eai.amd.com/template-cache-cleanup",
      "aim.eai.amd.com/template-cache.cleanup"))
    | "has the finalizer \(.)"')"
  report template-finalizers delete-template-objects "${found}"
}

check_stale_owner_references() {
  local found
  found="$(for resource in inferenceservices.serving.kserve.io httproutes.gateway.networking.k8s.io \
    scaledobjects.keda.sh persistentvolumeclaims jobs.batch configmaps \
    aimartifacts.aim.eai.amd.com aimprofiles.aim.eai.amd.com aimprofilecaches.aim.eai.amd.com; do
    list "${resource}"
  done | problems '.metadata.ownerReferences[]?
    | select(.apiVersion == "aim.eai.amd.com/v1alpha1" and (.kind | IN("AIMService", "AIMModel",
      "AIMClusterModel", "AIMServiceTemplate", "AIMClusterServiceTemplate", "AIMTemplateCache")))
    | "is owned by v1alpha1 \(.kind) \(.name)"')"
  report stale-owner-references remove-stale-v1alpha1-owner-references "${found}"
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

api_resources="$(kubectl api-resources -o name)"
services="$(kubectl get aimservices.aim.eai.amd.com --all-namespaces -o json)"
models="$(kubectl get aimmodels.aim.eai.amd.com --all-namespaces -o json)"
cluster_models="$(kubectl get aimclustermodels.aim.eai.amd.com -o json)"

echo "Sections (#...) refer to ${guide}"
run_check minimum-version
run_check template-pipeline-services
run_check unservable-services
run_check models-without-native-path
run_check template-objects
run_check template-finalizers
run_check stale-owner-references
report_dedicated_caches
manual v1alpha1-clients update-v1alpha1-manifests "AIMService, AIMModel and AIMClusterModel stop serving\
 aim.eai.amd.com/v1alpha1: check GitOps repositories and scripts for such manifests"

if ((failures > 0)); then
  echo "${failures} blocking problem(s) found. Fix them on the current release and rerun before upgrading."
  exit 1
fi
echo "All blocking checks passed."
