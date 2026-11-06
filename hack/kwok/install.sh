#!/usr/bin/env bash
# Installs the KWOK controller (in kube-system) and its fast stages, which mark
# KWOK nodes Ready and their pods Running at once. KWOK runs the fake GPU nodes
# from hack/fakegpu; see docs/docs/contributing/testing.md#fake-gpu-nodes-kwok.
#
#   hack/kwok/install.sh [kubectl flags, e.g. --context kind-aim-engine]
#
# KWOK_VERSION picks the release (default v0.8.0).
set -euo pipefail

KWOK_VERSION=${KWOK_VERSION:-v0.8.0}
base=https://github.com/kubernetes-sigs/kwok/releases/download/$KWOK_VERSION

kubectl "$@" apply -f "$base/kwok.yaml"
kubectl "$@" apply -f "$base/stage-fast.yaml"
kubectl "$@" -n kube-system rollout status deployment/kwok-controller --timeout=180s
