#!/usr/bin/env bash
#
# Force AIMServiceTemplate / AIMClusterServiceTemplate rediscovery.
#
# NOTE: Operators that include the in-controller identity rediscovery
# mechanism (DiscoveryState.IdentityCheckHash / ShouldRediscoverForIdentity,
# shipped in aim-engine v0.2.x and later) auto-recover from this scenario
# without operator intervention. This script remains useful for forcing a
# rediscovery sweep against older operator deployments.
#
# Why this exists:
#   Templates created before finetune support (commit 31c754a) have
#   status.status == Ready but empty spec.aimId / spec.modelId, which
#   breaks aimId-based matching for fine-tuned AIMModels. The reconciler
#   short-circuits Ready templates, so they never re-run discovery on
#   their own.
#
# How the short-circuit works:
#   PlanResources and ShouldCheckDiscoveryJob both gate on
#   `template.Status.Status == Ready`. That field is *derived* from the
#   component conditions (the ones whose type ends in "Ready"):
#   RuntimeConfigReady, ModelReady, GPUReady, DiscoveryJobReady,
#   DiscoveryPodsReady. The "Discovered" condition is NOT a component
#   condition (no "Ready" suffix), so flipping it alone does not change
#   the derived status. Patching status.status directly is also not
#   enough, because the next reconcile re-derives it from the still-True
#   component conditions.
#
#   The surgical fix is to flip the two discovery-scoped component
#   conditions (DiscoveryJobReady and DiscoveryPodsReady) to False. The
#   next reconcile then:
#     1. Derives status.status = Progressing,
#     2. ShouldCheckDiscoveryJob returns true,
#     3. Sees no (or stale) discovery Job and launches a fresh one,
#     4. Parses the current-format output,
#     5. Patches spec.aimId / spec.modelId via
#        maybePromoteDiscoveredIdentity,
#     6. Marks the template Ready again.
#
#   Running AIMServices are not disturbed: their InferenceService keeps
#   running while the template briefly dips out of Ready (the
#   AIMService plan-phase skips ISVC updates when the template is not
#   Ready - see internal/v1alpha1/aimservice/reconcile.go PlanResources).
#
#   Verified end-to-end against a live cluster on 2026-04-23 with the
#   ghcr.io/silogen/aim-dummy:0.1.10 model. Four variants were tested;
#   only flipping the component conditions actually triggers a new
#   discovery job. See the related chat for the empirical evidence.
#
# Usage:
#   ./rediscover-aim-templates.sh [--dry-run] [--namespace NS] [--yes]
#                                 [--settle-timeout SEC] [--no-wait]
#
# After patching, the script waits (up to --settle-timeout, default 300s)
# for all touched templates to leave the transient Pending/Progressing
# states, then prints any that still lack spec.aimId / spec.modelId so
# the operator can investigate them. Pass --no-wait to skip the wait and
# the final report.
#

set -euo pipefail

DRY_RUN=0
NAMESPACE=""
ASSUME_YES=0
SETTLE_TIMEOUT=300
WAIT_FOR_SETTLE=1

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)        DRY_RUN=1 ;;
        --namespace)      NAMESPACE="${2:?missing value}"; shift ;;
        --yes|-y)         ASSUME_YES=1 ;;
        --settle-timeout) SETTLE_TIMEOUT="${2:?missing value}"; shift ;;
        --no-wait)        WAIT_FOR_SETTLE=0 ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^#//; s/^ //'
            exit 0
            ;;
        *) echo "unknown arg: $1" >&2; exit 1 ;;
    esac
    shift
done

for bin in kubectl jq; do
    command -v "$bin" >/dev/null || { echo "error: '$bin' is required" >&2; exit 1; }
done

run() {
    if [[ "$DRY_RUN" == 1 ]]; then
        echo "  [dry-run] $*"
    else
        "$@"
    fi
}

# For a single template, flip the DiscoveryJobReady and DiscoveryPodsReady
# component conditions to False so that the derived status.status falls
# out of Ready and the controller re-runs discovery. Also delete any
# lingering discovery Job (they TTL after 10 min anyway, but doing it
# explicitly avoids the controller briefly seeing a stale Completed job).
rediscover() {
    local kind="$1" name="$2" ns="$3"
    local ns_args=()
    [[ -n "$ns" ]] && ns_args=(-n "$ns")

    local ts
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)

    # Build the merged conditions array: keep existing conditions for any
    # type we don't touch, and replace DiscoveryJobReady / DiscoveryPodsReady
    # with status=False. If those conditions don't exist yet they are added.
    local patch_body
    patch_body=$(kubectl get "$kind" "$name" "${ns_args[@]}" -o json | jq --arg ts "$ts" '
        def override(t): {
            type: t,
            status: "False",
            reason: "ForceRediscovery",
            message: "Forced rediscovery to backfill aimId/modelId",
            lastTransitionTime: $ts
        };
        (.status.conditions // [])
            | map(select(.type != "DiscoveryJobReady" and .type != "DiscoveryPodsReady"))
            | . + [override("DiscoveryJobReady"), override("DiscoveryPodsReady")]
            | {status: {conditions: .}}
    ')

    # Order matters: the condition patch is what triggers the next
    # reconcile, and that reconcile must NOT observe a stale completed
    # discovery Job (if it does, it will immediately flip
    # DiscoveryJobReady back to True without launching a new Job).
    # So: delete first (and wait for the Job to actually be gone), then
    # patch the conditions.
    echo "  delete jobs for $kind/${ns:+$ns/}$name"
    run kubectl delete job "${ns_args[@]}" \
        -l "aim.eai.amd.com/template=$name" --ignore-not-found --wait=true

    echo "  patch $kind/${ns:+$ns/}$name"
    run kubectl patch "$kind" "$name" "${ns_args[@]}" \
        --subresource=status --type=merge -p "$patch_body"
}

echo "==> Enumerating Ready templates missing spec.aimId..."

CLUSTER_TARGETS=$(kubectl get aimclusterservicetemplates -o json \
    | jq -r '.items[] | select(.spec.customProfile == null and (.spec.aimId // "") == "" and .status.status == "Ready") | .metadata.name')

if [[ -n "$NAMESPACE" ]]; then
    NS_TARGETS=$(kubectl get aimservicetemplates -n "$NAMESPACE" -o json)
else
    NS_TARGETS=$(kubectl get aimservicetemplates -A -o json)
fi
NS_TARGETS=$(echo "$NS_TARGETS" \
    | jq -r '.items[] | select(.spec.customProfile == null and (.spec.aimId // "") == "" and .status.status == "Ready") | "\(.metadata.namespace) \(.metadata.name)"')

CLUSTER_COUNT=$(printf '%s\n' "$CLUSTER_TARGETS" | grep -c . || true)
NS_COUNT=$(printf '%s\n' "$NS_TARGETS" | grep -c . || true)

echo "    AIMClusterServiceTemplate: $CLUSTER_COUNT"
echo "    AIMServiceTemplate:        $NS_COUNT${NAMESPACE:+ (namespace=$NAMESPACE)}"

if [[ "$CLUSTER_COUNT" == 0 && "$NS_COUNT" == 0 ]]; then
    echo "Nothing to do."
    exit 0
fi

if [[ "$DRY_RUN" == 0 && "$ASSUME_YES" == 0 ]]; then
    echo
    read -r -p "Proceed? [y/N] " reply
    case "$reply" in
        y|Y|yes|YES) ;;
        *) echo "Aborted."; exit 0 ;;
    esac
fi

# Track which templates we touched so we can settle-wait and report only on those.
TOUCHED_CLUSTER=()
TOUCHED_NS=()

if [[ "$CLUSTER_COUNT" != 0 ]]; then
    echo "==> Forcing rediscovery on AIMClusterServiceTemplates..."
    for name in $CLUSTER_TARGETS; do
        rediscover aimclusterservicetemplate "$name" ""
        TOUCHED_CLUSTER+=("$name")
    done
fi

if [[ "$NS_COUNT" != 0 ]]; then
    echo "==> Forcing rediscovery on AIMServiceTemplates..."
    while IFS=' ' read -r ns name; do
        [[ -z "${ns:-}" ]] && continue
        rediscover aimservicetemplate "$name" "$ns"
        TOUCHED_NS+=("$ns $name")
    done <<< "$NS_TARGETS"
fi

if [[ "$DRY_RUN" == 1 || "$WAIT_FOR_SETTLE" == 0 ]]; then
    echo "==> Done. Controller will launch fresh discovery jobs on the next"
    echo "    reconcile. Monitor with:"
    echo "      kubectl get aimservicetemplates -A -w"
    echo "    or check aimId backfill with:"
    echo "      kubectl get aimservicetemplates -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,STATUS:.status.status,AIMID:.spec.aimId,MODELID:.spec.modelId"
    exit 0
fi

# ---------------------------------------------------------------------
# Wait for the touched templates to settle.
#
# A template is "settled" once its status.status is one of the terminal
# values: Ready, NotAvailable, Failed, Degraded. Pending and Progressing
# are transient states that we expect to pass through while the fresh
# discovery Job runs.
#
# We keep looping until every touched template is settled or the timeout
# expires. The polling interval doubles up to a small cap to avoid
# hammering the apiserver.
# ---------------------------------------------------------------------

is_settled() {
    case "$1" in
        Ready|NotAvailable|Failed|Degraded) return 0 ;;
        *) return 1 ;;
    esac
}

echo "==> Waiting up to ${SETTLE_TIMEOUT}s for templates to settle..."

deadline=$(( $(date +%s) + SETTLE_TIMEOUT ))
interval=3
max_interval=15

while :; do
    pending=()

    for name in "${TOUCHED_CLUSTER[@]}"; do
        status=$(kubectl get aimclusterservicetemplate "$name" -o jsonpath='{.status.status}' 2>/dev/null || echo "")
        if ! is_settled "$status"; then
            pending+=("aimclusterservicetemplate/$name [$status]")
        fi
    done

    for entry in "${TOUCHED_NS[@]}"; do
        ns="${entry%% *}"
        name="${entry#* }"
        status=$(kubectl get aimservicetemplate -n "$ns" "$name" -o jsonpath='{.status.status}' 2>/dev/null || echo "")
        if ! is_settled "$status"; then
            pending+=("aimservicetemplate/$ns/$name [$status]")
        fi
    done

    if [[ ${#pending[@]} -eq 0 ]]; then
        echo "    all templates settled."
        break
    fi

    now=$(date +%s)
    if (( now >= deadline )); then
        echo "    timeout reached; still unsettled:"
        printf '      - %s\n' "${pending[@]}"
        break
    fi

    remaining=$(( deadline - now ))
    echo "    ${#pending[@]} still transient (${remaining}s remaining), sleeping ${interval}s..."
    sleep "$interval"
    (( interval < max_interval )) && interval=$(( interval * 2 ))
    (( interval > max_interval )) && interval=$max_interval
done

# ---------------------------------------------------------------------
# Final report: which of the touched templates still lack aimId/modelId?
#
# Expected causes:
#   - The underlying AIM image does not emit aim_id / model_id in its
#     discovery output (older images, or deliberately generic images).
#   - The new discovery Job failed (template.status.status != Ready).
#   - The discovery Job succeeded but the template was created with
#     inline modelSources, so discovery is skipped - those templates
#     must have aimId filled in by hand on the spec.
# ---------------------------------------------------------------------

echo "==> Final state of touched templates:"
report() {
    local kind="$1" ns="$2" name="$3"
    local json
    if [[ -n "$ns" ]]; then
        json=$(kubectl get "$kind" -n "$ns" "$name" -o json 2>/dev/null || echo "{}")
    else
        json=$(kubectl get "$kind" "$name" -o json 2>/dev/null || echo "{}")
    fi
    echo "$json" | jq -r --arg kind "$kind" --arg ns "$ns" --arg name "$name" '
        [
            $kind,
            (if $ns == "" then $name else "\($ns)/\($name)" end),
            (.status.status // "-"),
            (if (.spec.aimId // "") == "" then "(missing)" else .spec.aimId end),
            (if (.spec.modelId // "") == "" then "(missing)" else .spec.modelId end)
        ] | @tsv'
}

{
    printf 'KIND\tNAME\tSTATUS\tAIMID\tMODELID\n'
    for name in "${TOUCHED_CLUSTER[@]}"; do
        report aimclusterservicetemplate "" "$name"
    done
    for entry in "${TOUCHED_NS[@]}"; do
        ns="${entry%% *}"
        name="${entry#* }"
        report aimservicetemplate "$ns" "$name"
    done
} | column -t -s $'\t' | sed 's/^/    /'

# Warn about any that still lack aimId or modelId. Count them explicitly
# so we can exit non-zero and surface the problem to CI / the caller.
missing=0
warnings=()
check_missing() {
    local kind="$1" ns="$2" name="$3"
    local json aimid modelid
    if [[ -n "$ns" ]]; then
        json=$(kubectl get "$kind" -n "$ns" "$name" -o json 2>/dev/null || echo "{}")
    else
        json=$(kubectl get "$kind" "$name" -o json 2>/dev/null || echo "{}")
    fi
    aimid=$(echo "$json"   | jq -r '.spec.aimId   // ""')
    modelid=$(echo "$json" | jq -r '.spec.modelId // ""')
    if [[ -z "$aimid" || -z "$modelid" ]]; then
        missing=$(( missing + 1 ))
        warnings+=("$kind ${ns:+$ns/}$name: aimId='${aimid:-<missing>}' modelId='${modelid:-<missing>}'")
    fi
}

for name in "${TOUCHED_CLUSTER[@]}"; do
    check_missing aimclusterservicetemplate "" "$name"
done
for entry in "${TOUCHED_NS[@]}"; do
    ns="${entry%% *}"
    name="${entry#* }"
    check_missing aimservicetemplate "$ns" "$name"
done

if (( missing > 0 )); then
    echo
    echo "!!  $missing template(s) still lack aimId and/or modelId after rediscovery:"
    printf '      - %s\n' "${warnings[@]}"
    echo "    These templates will NOT be matched by aimId-based fine-tune lookups."
    echo "    Check the discovery Job logs:"
    echo "      kubectl logs -l aim.eai.amd.com/template=<template-name> --tail=50"
    echo "    Common causes: older AIM image without aim_id/model_id in profile"
    echo "    metadata, or a failed discovery Job."
    exit 2
fi

echo "==> All touched templates have aimId and modelId populated."

