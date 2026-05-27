#!/usr/bin/env bash
#
# Clone AIMServiceTemplate / AIMClusterServiceTemplate resources from one
# GPU profile to another.
#
# Default mode (no --namespace) operates on cluster-scoped
# AIMClusterServiceTemplate resources, fetching every template in the
# cluster whose status.resolvedHardware.gpu.model (preferred) or
# spec.hardware.gpu.model matches PROFILE_GPU. With --namespace=NAME the
# script switches to namespace-scoped AIMServiceTemplate resources in NAME,
# which is what the e2e test uses so it cannot collide with templates left
# behind by parallel or earlier cluster-scoped tests.
#
# Owner references are preserved, so cloned templates will be cleaned up
# when their referenced model is deleted.
#
# Usage:
#   ./clone-templates-for-gpu.sh [--namespace NAMESPACE] PROFILE_GPU TARGET_GPU
#   ./clone-templates-for-gpu.sh PROFILE_GPU TARGET_GPU | kubectl apply -f -
#   ./clone-templates-for-gpu.sh -n my-ns PROFILE_GPU TARGET_GPU > templates.yaml
#
# Examples:
#   ./clone-templates-for-gpu.sh MI300X MI325X | kubectl apply -f -
#   ./clone-templates-for-gpu.sh -n my-ns MI300X MI308X | kubectl apply -f -
#

set -euo pipefail

usage() {
    cat >&2 <<EOF
Usage: $0 [--namespace NAMESPACE | -n NAMESPACE] PROFILE_GPU TARGET_GPU
Example: $0 MI300X MI325X | kubectl apply -f -
Example: $0 -n my-ns MI300X MI308X | kubectl apply -f -

Clones any (cluster) service template that targets PROFILE_GPU, replaces
the GPU model with TARGET_GPU, and adds env var AIM_GPU_MODEL=PROFILE_GPU.

Default scope is cluster-scoped (AIMClusterServiceTemplate). Pass
--namespace / -n to operate on namespace-scoped AIMServiceTemplate
resources instead.
EOF
}

NAMESPACE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--namespace)
            if [[ $# -lt 2 ]]; then
                echo "error: $1 requires a NAMESPACE argument" >&2
                usage
                exit 1
            fi
            NAMESPACE="$2"
            shift 2
            ;;
        --namespace=*)
            NAMESPACE="${1#--namespace=}"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo "error: unknown flag: $1" >&2
            usage
            exit 1
            ;;
        *)
            break
            ;;
    esac
done

if [[ $# -ne 2 ]]; then
    usage
    exit 1
fi

PROFILE_GPU="$1"
TARGET_GPU="$2"

TARGET_GPU_LOWER=$(echo "$TARGET_GPU" | tr '[:upper:]' '[:lower:]')
PROFILE_GPU_LOWER=$(echo "$PROFILE_GPU" | tr '[:upper:]' '[:lower:]')

# Pick scope-dependent settings up front so the rest of the script reads
# uniformly. KUBECTL_LIST_ARGS and KUBECTL_RESOURCE control where we look;
# OUTPUT_KIND and OUTPUT_NAMESPACE control what we emit so the manifests
# round-trip via `kubectl apply -f -` against the right scope.
if [[ -n "$NAMESPACE" ]]; then
    KUBECTL_RESOURCE="aimservicetemplates.aim.eai.amd.com"
    KUBECTL_LIST_ARGS=(-n "$NAMESPACE")
    OUTPUT_KIND="AIMServiceTemplate"
    OUTPUT_NAMESPACE="$NAMESPACE"
    SCOPE_LABEL="namespace '$NAMESPACE'"
else
    KUBECTL_RESOURCE="aimclusterservicetemplates.aim.eai.amd.com"
    KUBECTL_LIST_ARGS=()
    OUTPUT_KIND="AIMClusterServiceTemplate"
    OUTPUT_NAMESPACE=""
    SCOPE_LABEL="cluster scope"
fi

echo "Cloning templates from GPU profile '$PROFILE_GPU' to '$TARGET_GPU' ($SCOPE_LABEL)..." >&2

TEMPLATES=$(kubectl get "$KUBECTL_RESOURCE" "${KUBECTL_LIST_ARGS[@]}" -o json)

# Count matching templates (prefer resolved hardware when available)
MATCHING_COUNT=$(echo "$TEMPLATES" | jq -r --arg gpu "$PROFILE_GPU" \
    '[.items[] | select((.status.resolvedHardware.gpu.model // .spec.hardware.gpu.model // "") == $gpu)] | length')

if [[ "$MATCHING_COUNT" -eq 0 ]]; then
    echo "No templates found matching GPU profile '$PROFILE_GPU'" >&2
    exit 0
fi

echo "Found $MATCHING_COUNT templates to clone" >&2
echo "" >&2

# Get unique model names and process templates grouped by model
MODELS=$(echo "$TEMPLATES" | jq -r --arg gpu "$PROFILE_GPU" \
    '[.items[] | select((.status.resolvedHardware.gpu.model // .spec.hardware.gpu.model // "") == $gpu) | .spec.modelName] | unique | .[]')

# Collect all output, then print with proper separators
OUTPUT=""
CURRENT_MODEL=""

while IFS= read -r MODEL_NAME; do
    if [[ "$CURRENT_MODEL" != "$MODEL_NAME" ]]; then
        echo "Model: $MODEL_NAME" >&2
        CURRENT_MODEL="$MODEL_NAME"
    fi

    # Process templates for this model
    while IFS= read -r template; do
        OLD_NAME=$(echo "$template" | jq -r '.metadata.name')

        # Calculate new name
        if echo "$OLD_NAME" | grep -qi "$PROFILE_GPU_LOWER"; then
            # Name contains PROFILE_GPU, replace it with TARGET_GPU
            NEW_NAME=$(echo "$OLD_NAME" | sed "s/${PROFILE_GPU_LOWER}/${TARGET_GPU_LOWER}/gi")
        else
            # Truncate and append TARGET_GPU
            TRUNCATE_LEN=$((${#TARGET_GPU} + 1))
            MAX_LEN=$((${#OLD_NAME} - TRUNCATE_LEN))
            if [[ $MAX_LEN -lt 1 ]]; then
                MAX_LEN=1
            fi
            NEW_NAME="${OLD_NAME:0:$MAX_LEN}-${TARGET_GPU_LOWER}"
        fi

        echo "  $OLD_NAME -> $NEW_NAME" >&2

        # Create the new resource as YAML
        YAML=$(echo "$template" | jq \
            --arg newName "$NEW_NAME" \
            --arg targetGpu "$TARGET_GPU" \
            --arg profileGpu "$PROFILE_GPU" \
            --arg outputKind "$OUTPUT_KIND" \
            --arg outputNamespace "$OUTPUT_NAMESPACE" \
            '
            # Remove read-only/server-managed fields (ownerReferences are preserved)
            del(.metadata.uid) |
            del(.metadata.resourceVersion) |
            del(.metadata.generation) |
            del(.metadata.creationTimestamp) |
            del(.metadata.managedFields) |
            del(.metadata.selfLink) |
            del(.metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]) |
            del(.status) |

            # Force the desired output kind. The list call above already
            # filtered to one scope, but stamping kind explicitly keeps
            # the output self-describing if a caller pipes it elsewhere.
            .kind = $outputKind |

            # Apply or strip namespace based on output scope. jq does not
            # accept a conditional in a single | chain, so do it as an
            # if/then/else over the whole object.
            (if $outputNamespace == "" then
                del(.metadata.namespace)
             else
                .metadata.namespace = $outputNamespace
             end) |

            # Update name
            .metadata.name = $newName |

        # Update GPU selector
        .spec.hardware = (.spec.hardware // {}) |
        .spec.hardware.gpu = (.spec.hardware.gpu // {}) |
        .spec.hardware.gpu.model = $targetGpu |

            # Add or update env var
            .spec.env = ((.spec.env // []) | map(select(.name != "AIM_GPU_MODEL"))) + [{name: "AIM_GPU_MODEL", value: $profileGpu}]
            ' | yq -P)

        if [[ -n "$OUTPUT" ]]; then
            OUTPUT="$OUTPUT
---
$YAML"
        else
            OUTPUT="$YAML"
        fi
    done < <(echo "$TEMPLATES" | jq -c --arg gpu "$PROFILE_GPU" --arg model "$MODEL_NAME" \
        '.items[] | select((.status.resolvedHardware.gpu.model // .spec.hardware.gpu.model // "") == $gpu and .spec.modelName == $model)')
done <<< "$MODELS"

echo "$OUTPUT"

echo "" >&2
echo "Done" >&2
