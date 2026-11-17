#!/usr/bin/env bash
#
# The chart's default AIMClusterRuntimeConfig is v1alpha2, and the template
# does not choose a version from the cluster's served APIs. helm template
# does not contact a cluster: the built-in capability set has no AIM CRDs,
# and --api-versions only adds the versions named here.

set -euo pipefail

CHART_DIR="${1:-dist/chart}"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

fail() {
    echo "runtime config render check failed: $*" >&2
    exit 1
}

assert_runtime_config() {
    local manifest="$1"
    local label="$2"
    local version count v1alpha1

    version="$(
        yq eval --no-doc 'select(.kind == "AIMClusterRuntimeConfig") | .apiVersion' "${manifest}" |
            awk 'NF { print; exit }'
    )"
    count="$(
        yq eval --no-doc 'select(.kind == "AIMClusterRuntimeConfig") | .kind' "${manifest}" |
            awk 'NF { count++ } END { print count + 0 }'
    )"
    [[ "${count}" == "1" && "${version}" == "aim.eai.amd.com/v1alpha2" ]] ||
        fail "${label}: expected one AIMClusterRuntimeConfig at aim.eai.amd.com/v1alpha2, got ${count} at '${version:-<missing>}'"

    v1alpha1="$(
        yq eval --no-doc 'select(.apiVersion == "aim.eai.amd.com/v1alpha1") | .kind' "${manifest}" |
            awk 'NF { count++ } END { print count + 0 }'
    )"
    [[ "${v1alpha1}" == "0" ]] ||
        fail "${label}: rendered ${v1alpha1} aim.eai.amd.com/v1alpha1 object(s)"
}

render() {
    local name="$1"
    shift
    local manifest="${WORK_DIR}/${name}.yaml"
    helm template aim-engine "${CHART_DIR}" "$@" >"${manifest}"
    assert_runtime_config "${manifest}" "${name}"
}

render default
render only-v1alpha1 --api-versions aim.eai.amd.com/v1alpha1/AIMClusterRuntimeConfig
render with-v1alpha2 --api-versions aim.eai.amd.com/v1alpha2/AIMClusterRuntimeConfig

echo "Chart AIMClusterRuntimeConfig renders at v1alpha2."
