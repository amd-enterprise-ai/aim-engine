#!/usr/bin/env bash
# Helm post-renderer for the kserve-runtime-configs chart, whose
# LLMInferenceServiceConfigs hard-code metadata.namespace "kserve" regardless of
# the release namespace. KServe resolves its built-in configs in its own
# namespace, so they are moved to KSERVE_NAMESPACE (default kserve-system).
set -euo pipefail

sed -E "s/^(  namespace: )kserve$/\1${KSERVE_NAMESPACE:-kserve-system}/"
