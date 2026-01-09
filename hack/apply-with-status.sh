#!/usr/bin/env bash
#
# apply-with-status.sh - Apply a Kubernetes resource and/or patch its status subresource
#
# This script enables defining both spec and status in a single YAML file for testing.
# kubectl apply ignores the status field, so this script handles patching separately.
#
# Modes:
#   Default:      Apply the resource AND patch its status
#   --status-only: Only patch the status (use after Chainsaw apply for cleanup tracking)
#
# Usage:
#   ./apply-with-status.sh <file.yaml> [options]
#
# Options:
#   --freeze        Add the reconciliation-paused annotation after patching status
#   --status-only   Skip apply, only patch the status subresource
#   -n, --namespace Override the namespace (useful when file has no namespace)
#
# Requirements:
#   - kubectl configured with cluster access
#   - yq (https://github.com/mikefarah/yq) v4+
#   - jq
#
# Example YAML file:
#   apiVersion: aim.eai.amd.com/v1alpha1
#   kind: AIMModel
#   metadata:
#     name: test-model
#     namespace: default
#   spec:
#     image: ghcr.io/example/model:v1
#   status:
#     status: Ready
#     conditions:
#       - type: Ready
#         status: "True"
#         reason: AllComponentsReady
#         message: Model is ready
#
# Chainsaw Integration:
#   Use Chainsaw's apply step first (for namespace injection and cleanup tracking),
#   then call this script with --status-only to patch the status:
#
#   steps:
#     - name: Setup frozen model
#       try:
#         - apply:
#             file: model.yaml
#         - script:
#             env:
#               - name: NAMESPACE
#                 value: ($namespace)
#             content: |
#               ./hack/apply-with-status.sh model.yaml --status-only --freeze -n $NAMESPACE
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

error() {
    echo -e "${RED}ERROR:${NC} $1" >&2
    exit 1
}

info() {
    echo -e "${GREEN}INFO:${NC} $1"
}

warn() {
    echo -e "${YELLOW}WARN:${NC} $1"
}

# Check dependencies
check_deps() {
    if ! command -v yq &> /dev/null; then
        error "yq is required but not installed. Install from https://github.com/mikefarah/yq"
    fi
    if ! command -v kubectl &> /dev/null; then
        error "kubectl is required but not installed"
    fi
}

# Parse arguments
FREEZE=false
STATUS_ONLY=false
NAMESPACE_OVERRIDE=""
FILE=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --freeze)
            FREEZE=true
            shift
            ;;
        --status-only)
            STATUS_ONLY=true
            shift
            ;;
        -n|--namespace)
            if [[ -z "${2:-}" ]]; then
                error "--namespace requires a value"
            fi
            NAMESPACE_OVERRIDE="$2"
            shift 2
            ;;
        -*)
            error "Unknown option: $1"
            ;;
        *)
            if [[ -n "$FILE" ]]; then
                error "Multiple files specified. Only one file is supported."
            fi
            FILE="$1"
            shift
            ;;
    esac
done

if [[ -z "$FILE" ]]; then
    error "Usage: $0 <file.yaml> [--freeze] [--status-only] [-n namespace]"
fi

if [[ ! -f "$FILE" ]]; then
    error "File not found: $FILE"
fi

check_deps

# Extract resource info
KIND=$(yq '.kind' "$FILE")
NAME=$(yq '.metadata.name' "$FILE")
NAMESPACE_FROM_FILE=$(yq '.metadata.namespace // ""' "$FILE")
HAS_STATUS=$(yq 'has("status")' "$FILE")

if [[ "$KIND" == "null" ]] || [[ "$NAME" == "null" ]]; then
    error "Invalid YAML: missing kind or metadata.name"
fi

# Use namespace override if provided, otherwise use file's namespace
if [[ -n "$NAMESPACE_OVERRIDE" ]]; then
    NAMESPACE="$NAMESPACE_OVERRIDE"
elif [[ -n "$NAMESPACE_FROM_FILE" ]] && [[ "$NAMESPACE_FROM_FILE" != "null" ]]; then
    NAMESPACE="$NAMESPACE_FROM_FILE"
else
    NAMESPACE=""
fi

# Derive resource type for kubectl (lowercase)
RESOURCE_TYPE=$(echo "$KIND" | tr '[:upper:]' '[:lower:]')

# Check if cluster-scoped (no namespace)
IS_CLUSTER_SCOPED=false
if [[ -z "$NAMESPACE" ]]; then
    IS_CLUSTER_SCOPED=true
fi

# Build kubectl namespace flag
NS_FLAG=""
if [[ "$IS_CLUSTER_SCOPED" == "false" ]]; then
    NS_FLAG="-n $NAMESPACE"
fi

# Step 1: Apply the resource (unless --status-only)
if [[ "$STATUS_ONLY" == "false" ]]; then
    info "Applying $KIND/$NAME${NAMESPACE:+ in namespace $NAMESPACE}..."
    kubectl apply -f "$FILE" $NS_FLAG
else
    info "Skipping apply (--status-only mode) for $KIND/$NAME${NAMESPACE:+ in namespace $NAMESPACE}"
fi

# Step 2: Patch status if present
if [[ "$HAS_STATUS" == "true" ]]; then
    info "Patching status subresource..."

    # Extract status as JSON
    STATUS_JSON=$(yq -o=json '.status' "$FILE")

    # Build the patch payload
    PATCH_PAYLOAD=$(echo "{\"status\": $STATUS_JSON}" | jq -c .)

    # Patch the status subresource
    kubectl patch "$RESOURCE_TYPE" "$NAME" $NS_FLAG \
        --type=merge \
        --subresource=status \
        -p "$PATCH_PAYLOAD"

    info "Status patched successfully"
else
    warn "No status field found in $FILE, skipping status patch"
fi

# Step 3: Freeze if requested
if [[ "$FREEZE" == "true" ]]; then
    info "Freezing resource (adding reconciliation-paused annotation)..."
    kubectl annotate "$RESOURCE_TYPE" "$NAME" $NS_FLAG \
        aim.eai.amd.com/reconciliation-paused=true \
        --overwrite
    info "Resource frozen"
fi

info "Done: $KIND/$NAME"
