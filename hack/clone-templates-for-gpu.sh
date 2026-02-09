#!/usr/bin/env bash
#
# Clone AIMClusterServiceTemplate resources from one GPU profile to another.
#
# This script fetches all AIMClusterServiceTemplate resources where
# status.resolvedHardware.gpu.model (preferred) or spec.hardware.gpu.model
# matches PROFILE_GPU, clones them with the target GPU model, and outputs
# the new manifests to stdout.
#
# Owner references are preserved, so cloned templates will be cleaned up
# when their referenced model is deleted.
#
# Usage:
#   ./clone-templates-for-gpu.sh PROFILE_GPU TARGET_GPU
#   ./clone-templates-for-gpu.sh PROFILE_GPU TARGET_GPU | kubectl apply -f -
#   ./clone-templates-for-gpu.sh PROFILE_GPU TARGET_GPU > templates.yaml
#
# Example:
#   ./clone-templates-for-gpu.sh MI300X MI325X | kubectl apply -f -
#

set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "Usage: $0 PROFILE_GPU TARGET_GPU" >&2
    echo "Example: $0 MI300X MI325X | kubectl apply -f -" >&2
    echo "This will clone any AIMClusterServiceTemplate that targets MI300X, replaces the GPU model by MI325X, and adds an env var AIM_GPU_MODEL=MI300X" >&2
    exit 1
fi

PROFILE_GPU="$1"
TARGET_GPU="$2"

TARGET_GPU_LOWER=$(echo "$TARGET_GPU" | tr '[:upper:]' '[:lower:]')
PROFILE_GPU_LOWER=$(echo "$PROFILE_GPU" | tr '[:upper:]' '[:lower:]')

echo "Cloning templates from GPU profile '$PROFILE_GPU' to '$TARGET_GPU'..." >&2

# Fetch all AIMClusterServiceTemplate resources
TEMPLATES=$(kubectl get aimclusterservicetemplates.aim.eai.amd.com -o json)

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
