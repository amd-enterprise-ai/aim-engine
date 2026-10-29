# Image URL to use all building/pushing image targets
TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "latest")
GIT_ORG ?= $(shell git remote get-url origin 2>/dev/null | sed -n 's|.*github\.com[:/]\([^/]*\)/.*|\1|p')
# Default operator image repo for local-dev builds. Fork-aware: silogen pushes
# to docker.io/silogenai (private), everyone else (notably amd-enterprise-ai)
# uses the public docker.io/amdenterpriseai mirror. CI always overrides via IMG.
ifeq ($(GIT_ORG),silogen)
IMG_REPO ?= docker.io/silogenai/aim-engine
else
IMG_REPO ?= docker.io/amdenterpriseai/aim-engine
endif
IMG ?= $(IMG_REPO):$(TAG)
# Default to the public docker.io/amdenterpriseai mirror so non-CI builds
# (and the amd-enterprise-ai public release flow) produce a binary whose
# compiled-in artifact-downloader default is publicly pullable. Silogen-private
# CI overrides this to docker.io/silogenai/aim-artifact-downloader at build time.
ARTIFACT_DOWNLOADER_IMG ?= docker.io/amdenterpriseai/aim-artifact-downloader:$(TAG)
LDFLAGS ?= -X 'github.com/amd-enterprise-ai/aim-engine/api/v1alpha1.DefaultDownloadImage=$(ARTIFACT_DOWNLOADER_IMG)'

# AIM_DUMMY_TAG pins the aim-dummy test image referenced by every e2e fixture
# (BYO base-image, custom-model, discovery, etc.). AIM_DUMMY_IMAGE is the
# fully-qualified public ref the fixtures hardcode: in kind we build it from
# images/aim-dummy/ and `kind load` it under this exact ref so kubelet's
# IfNotPresent finds it locally and never pulls; on cloud/GPU envs the same ref
# resolves to the public docker.io/amdenterpriseai mirror. Bumping the tag here
# also requires updating the AIM_DUMMY_TAG env var in
# .github/workflows/test-e2e.yml and .github/workflows/compile-release.yaml so
# dev / CI stay in sync.
AIM_DUMMY_TAG ?= 0.2.0
AIM_DUMMY_IMAGE ?= docker.io/amdenterpriseai/aim-dummy:$(AIM_DUMMY_TAG)

# --- Kind-local Zot registry -------------------------------------------------
# AIMModel discovery reads image OCI labels from inside the operator, so loading
# an image into Kind's containerd is not sufficient by itself. The Kind dev
# stack installs an ephemeral Zot registry with cert-manager TLS. Test images
# are pushed through a localhost port-forward, while in-cluster clients use the
# service FQDN. Workload images are also kind-loaded under that exact FQDN so
# kubelet never needs to resolve Kubernetes service DNS.
ZOT_NAMESPACE ?= zot-system
ZOT_SERVICE ?= zot
ZOT_CA_CONFIGMAP ?= aim-engine-zot-ca
ZOT_CLUSTER_REGISTRY ?= $(ZOT_SERVICE).$(ZOT_NAMESPACE).svc.cluster.local:5000
ZOT_AIM_DUMMY_REPO ?= $(ZOT_CLUSTER_REGISTRY)/aim-dummy
# Zot creates an inotify watcher for configuration hot-reload at startup.
# Shared CI hosts can exhaust the kernel's per-user instance limit while
# multiple Kind clusters are running, so ensure sufficient headroom before Zot
# starts. Override this if the host has a stricter policy or a larger workload.
ZOT_INOTIFY_MAX_USER_INSTANCES ?= 8192

# NVIDIA Kubernetes dependency versions
NVIDIA_DEVICE_PLUGIN_VERSION ?= 0.19.3

# --- ttl.sh ephemeral-registry fallback for external GPU clusters ------------
# AIMModel discovery reads a model image's OCI labels *inside the operator* via
# go-containerregistry remote.Get (see internal/v1alpha1/aimmodel/inspector.go).
# `kind load` only satisfies kubelet pod pulls, NOT that in-operator registry
# read, so discovery against a not-yet-public ref (e.g. the new
# docker.io/amdenterpriseai mirror before it's published) fails with
# UNAUTHORIZED -> AIMModel Degraded. Publishing to the public mirror is slow and
# gated, which is impractical when iterating on operator logic. External GPU
# environments cannot use the Kind-local Zot service, so
# `make test-chainsaw-gpu-ttl` retains the anonymous public ttl.sh fallback.
# Content-addressing lets unchanged source re-use the same push. Recursively
# expanded (=) so the find/hash only runs when a ttl target is invoked.
#
# The TAG is preserved per fixture: the operator derives
# AIMProfile/AIMServiceTemplate .status.version from the image tag (see image
# discovery), and some fixtures assert version == their tag (e.g. 0.2.0).
# Fixtures may reference multiple tags (e.g. 0.2.0 and 0.2.1) because aim-dummy
# is backwards- but not always forward-compatible: tests written against an
# older tag still pass on newer content, but newer-tag tests need newer content.
# We therefore build images/aim-dummy ONCE (always the latest, backwards-compat
# source) and push it under EVERY tag the fixtures reference, all into the same
# content-addressed repo. The rewrite only swaps the repo prefix and keeps the
# tag, so tag-derived version assertions still hold. Uniqueness/TTL lives in the
# repo NAME via the source hash, not the tag. ttl.sh accepts a non-duration tag
# and applies its default TTL (24h).
TTL_REGISTRY ?= ttl.sh
AIM_DUMMY_SRC_HASH = $(shell find images/aim-dummy -type f -exec sha256sum {} \; | sort | sha256sum | cut -c1-16)
TTL_AIM_DUMMY_REPO ?= $(TTL_REGISTRY)/aim-engine-e2e-aim-dummy-$(AIM_DUMMY_SRC_HASH)
TTL_AIM_DUMMY_IMAGE ?= $(TTL_AIM_DUMMY_REPO):$(AIM_DUMMY_TAG)

# Helm chart configuration
CHART_NAME ?= aim-engine-chart
CRDS_CHART_NAME ?= aim-engine-crds-chart
CHART_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.1.0")
APP_VERSION ?= $(TAG)
# Image baked into the packaged chart's values.yaml. CI always overrides via
# CHART_IMAGE_REPO. For local-dev runs of `make helm-package`, silogen builds
# reference docker.io/silogenai (the private dev mirror); everything else
# (notably the amd-enterprise-ai official fork) references the public
# docker.io/amdenterpriseai org that end users actually pull from.
ifeq ($(GIT_ORG),silogen)
CHART_IMAGE_REPO ?= docker.io/silogenai/aim-engine
else
CHART_IMAGE_REPO ?= docker.io/amdenterpriseai/aim-engine
endif
CHART_IMAGE_TAG  ?= $(TAG)
# OCI registry receiving the packaged Helm + CRDs charts. Same fork-aware
# split as CHART_IMAGE_REPO; CI overrides via CHART_OCI_REPO env.
CHART_OCI_REGISTRY ?= registry-1.docker.io
ifeq ($(GIT_ORG),silogen)
CHART_OCI_OWNER ?= silogenai
else
CHART_OCI_OWNER ?= amdenterpriseai
endif
CHART_OCI_REPO ?= oci://$(CHART_OCI_REGISTRY)/$(CHART_OCI_OWNER)

# Cluster environment configuration
# ENV is auto-detected from kubectl context:
#   - Context starting with "kind-" -> ENV=kind
#   - Otherwise -> ENV=gpu (AMD GPU)
# NVIDIA GPU clusters are not auto-detected (context names don't encode vendor);
# select them explicitly with ENV=nvidia (or `make test-chainsaw-nvidia`).
# Can be overridden via ENV variable
CURRENT_CONTEXT := $(shell kubectl config current-context 2>/dev/null)
AUTO_ENV := $(if $(filter kind-%,$(CURRENT_CONTEXT)),kind,gpu)
ENV ?= $(AUTO_ENV)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	# maxDescLen keeps the Helm CRDs release Secret under Kubernetes' 1MiB limit.
	controller-gen rbac:roleName=manager-role crd:maxDescLen=250 webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet test-artifact-downloader ## Run tests.
	go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: test-bridge-contract
test-bridge-contract: ## Verify the minimal v1alpha1 bridge contract.
	go test ./internal/bridgecontract -run '^TestBridgeContract$$' -count=1

.PHONY: test-artifact-downloader
test-artifact-downloader: ## Run dependency-free artifact-downloader script tests.
	PYTHONPATH=images/artifact-downloader python3 -m unittest discover \
		-s images/artifact-downloader/tests -p 'test_combine_ca.py'

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= aim-engine-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: install-nvidia-dependencies
install-nvidia-dependencies: ## Install the NVIDIA device plugin and its bundled NFD.
	NVIDIA_DEVICE_PLUGIN_VERSION=$(NVIDIA_DEVICE_PLUGIN_VERSION) \
		helmfile sync -f hack/nvidia/helmfile.yaml.gotmpl

.PHONY: kind-create
kind-create: manifests ## Create kind cluster with all dependencies for local development.
	@echo "=== Setting up kind cluster for local development ==="
	@# Create cluster if it doesn't exist
	@if ! kind get clusters 2>/dev/null | grep -q "^aim-engine$$"; then \
		echo "Creating kind cluster 'aim-engine'..."; \
		kind create cluster --config hack/kind/config.yaml; \
	else \
		echo "Kind cluster 'aim-engine' already exists."; \
	fi
	@# Switch to kind context
	@kubectl config use-context kind-aim-engine
	@# Create aim-system namespace (needed for cluster-scoped resources)
	@echo "Creating aim-system namespace..."
	@kubectl create namespace aim-system --dry-run=client -o yaml | kubectl apply -f -
	@# Install core dependencies (cert-manager, Envoy Gateway, kserve)
	@echo "Installing core dependencies..."
	@helmfile sync -f hack/dependencies/helmfile.yaml.gotmpl
	@# Install the OTLP receiver before verifying the gateway's configured sink.
	@$(MAKE) install-dev-envoy-collector
	@# Verify the gateway data plane came up and the default Gateway is programmed.
	@$(MAKE) verify-gateway
	@# Install the Kind-local TLS registry after cert-manager is ready.
	@$(MAKE) kind-zot-install KIND_CLUSTER=aim-engine
	@# Install kind-specific dependencies (NFS server + csi-driver-nfs)
	@echo "Installing kind-specific dependencies..."
	@helmfile sync -f hack/kind/helmfile.yaml.gotmpl
	@# Install CRDs
	@echo "Installing CRDs..."
	@$(MAKE) install
	@echo "Installing RBAC..."
	@kustomize build config/local-dev-kind | kubectl apply -f - --server-side
	@$(MAKE) seaweedfs-init-bucket
	@# Publish aim-dummy to Zot for operator-side metadata discovery and load the
	@# exact in-cluster references into Kind for workload pods.
	@$(MAKE) aim-dummy-zot-push KIND_CLUSTER=aim-engine
	@echo ""
	@echo "=== Kind cluster setup complete ==="
	@echo "Run 'make watch' to start the operator with live reload."

.PHONY: seaweedfs-init-bucket
seaweedfs-init-bucket: ## Create the aim-cache S3 bucket in SeaweedFS.
	@echo "Creating S3 cache bucket..."
	@kubectl run seaweedfs-init --namespace=seaweedfs-system --rm -i --restart=Never \
		--image=alpine/curl:latest -- \
		sh -c 'curl -sf -X PUT http://seaweedfs-s3.seaweedfs-system:8333/aim-cache -o /dev/null -w "bucket created (HTTP %{http_code})\n"' || true

.PHONY: install-dev-envoy-collector
install-dev-envoy-collector: ## Install the externally managed Envoy collector used by local development.
	@echo "Installing local development Envoy collector (envoy-gateway-metrics-collector)..."
	@kubectl apply -f config/prereqs/scale-from-zero/envoy-gateway-metrics-collector.yaml
	@echo "Waiting for envoy-gateway-metrics-collector deployment..."
	@kubectl -n keda rollout status deploy/envoy-gateway-metrics-collector --timeout=180s
	@COLLECTOR_HOST=envoy-gateway-metrics-collector.keda.svc.cluster.local \
		bash hack/configure-envoy-collector-sink.sh

.PHONY: verify-gateway
verify-gateway: ## Verify Envoy Gateway is installed and the default Gateway is programmed.
	@echo "Verifying Envoy Gateway installation..."
	@kubectl wait --for=condition=Available deploy/envoy-gateway -n envoy-gateway-system --timeout=300s
	@kubectl wait --for=condition=Accepted gatewayclass/envoy-gateway --timeout=120s
	@kubectl wait --for=condition=Programmed gateway/kserve-ingress-gateway -n envoy-gateway-system --timeout=300s
	@echo "Envoy Gateway is ready."

.PHONY: kind-zot-inotify-limit
kind-zot-inotify-limit: ## Ensure Kind's host kernel has enough inotify instances for Zot.
	@set -eu; \
	nodes="$$(kind get nodes --name "$(KIND_CLUSTER)")"; \
	if [ -z "$$nodes" ]; then \
		echo "No nodes found for Kind cluster '$(KIND_CLUSTER)'."; \
		exit 1; \
	fi; \
	for node in $$nodes; do \
		current="$$(docker exec "$$node" sysctl -n fs.inotify.max_user_instances)"; \
		if [ "$$current" -lt "$(ZOT_INOTIFY_MAX_USER_INSTANCES)" ]; then \
			echo "Raising fs.inotify.max_user_instances on $$node from $$current to $(ZOT_INOTIFY_MAX_USER_INSTANCES)..."; \
			docker exec "$$node" sysctl -w fs.inotify.max_user_instances=$(ZOT_INOTIFY_MAX_USER_INSTANCES); \
		else \
			echo "fs.inotify.max_user_instances on $$node is $$current."; \
		fi; \
	done

.PHONY: kind-zot-install
kind-zot-install: kind-zot-inotify-limit ## Install the ephemeral TLS-enabled Zot registry used by Kind tests.
	@echo "Installing Kind-local Zot registry..."
	@helmfile sync -f hack/kind/zot/helmfile.yaml.gotmpl
	@kubectl create namespace aim-system --dry-run=client -o yaml | kubectl apply -f -
	@set -euo pipefail; \
	ca_file=$$(mktemp /tmp/aim-engine-zot-ca.XXXXXX); \
	trap 'rm -f "$$ca_file"' EXIT; \
	kubectl get secret zot-ca -n $(ZOT_NAMESPACE) -o jsonpath='{.data.tls\.crt}' | base64 -d >"$$ca_file"; \
	kubectl create configmap $(ZOT_CA_CONFIGMAP) -n aim-system \
		--from-file=ca.crt="$$ca_file" --dry-run=client -o yaml | kubectl apply -f -; \
	kubectl wait --for=condition=Available --timeout=120s \
		deployment/$(ZOT_SERVICE) -n $(ZOT_NAMESPACE)
	@echo "Zot is ready at https://$(ZOT_CLUSTER_REGISTRY)"

.PHONY: aim-dummy-zot-push
aim-dummy-zot-push: ## Build aim-dummy, push all fixture tags to Kind-local Zot, and load those exact refs into Kind.
	@KIND_CLUSTER="$(KIND_CLUSTER)" \
	CHAINSAW_TEST_DIR="$(CHAINSAW_TEST_DIR)" \
	ZOT_NAMESPACE="$(ZOT_NAMESPACE)" \
	ZOT_SERVICE="$(ZOT_SERVICE)" \
	ZOT_CA_CONFIGMAP="$(ZOT_CA_CONFIGMAP)" \
	ZOT_CLUSTER_REGISTRY="$(ZOT_CLUSTER_REGISTRY)" \
	ZOT_AIM_DUMMY_REPO="$(ZOT_AIM_DUMMY_REPO)" \
		./hack/kind/publish-aim-dummy.sh

.PHONY: kind-delete
kind-delete: ## Delete the kind cluster.
	@echo "Deleting kind cluster 'aim-engine'..."
	@kind delete cluster --name aim-engine || true

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

# Chainsaw test configuration
# Config and selector are applied automatically based on ENV
CHAINSAW_TEST_DIR := tests/e2e
CHAINSAW_REPORT_DIR := .tmp/chainsaw-reports
# Per-failed-test debug bundles (pod logs, describe, events) written by the
# config-level chainsaw `catch`. Exported as an absolute path because chainsaw
# runs catch scripts from each test's own directory. CI uploads it as an artifact.
CHAINSAW_DEBUG_DIR := .tmp/chainsaw-debug
CHAINSAW_CONFIG_DIR := tests/chainsaw/config

# needs-secret tags tests with environmental credential prerequisites
# (HF token, Docker Hub pull secret for docker.io/silogenai, etc.)
# that aren't universally provisioned. Excluded from the default selectors;
# opt in through a dedicated target or by overriding the selector.
CHAINSAW_NEEDS_SECRET_EXCLUDE := needs-secret notin (hf_token,dockerhub_pull_secret)

# reduced-mode gates eager-runtime-projection tests whose mutually-exclusive
# assertions only pass against an operator started in Reduced. The default lanes
# run the operator in Both, so these are excluded from every selector; run them
# via `make set-projection-mode MODE=...` then point CHAINSAW_TEST_DIR at the
# specific mode dir with the selector cleared (see
# docs/docs/contributing/testing.md). The ungated mode-both smoke runs in the
# default suite and asserts that both projection outputs coexist.
CHAINSAW_PROJECTION_MODE_EXCLUDE := reduced-mode

# The upgrade compatibility setup and verify phases share resources across an
# operator replacement. They are run explicitly by test-upgrade.yml and must
# not be discovered as independent tests by the regular Chainsaw lanes.
CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE := upgrade-workflow

# The mode-gated projection tests all live under one tree. The
# `test-chainsaw-projection-mode` target below is the inverse entry point of the
# exclusion above: it runs ONLY the gated tests for a chosen MODE by flipping the
# selector to INCLUDE `requires in (<mode>-mode)`.
CHAINSAW_PROJECTION_MODE_DIR := tests/e2e/v1alpha2/runtime-projection

# GPU vendor gating. Tests that need real accelerators carry a `requires` value
# scoped by vendor so a run only picks up tests its hardware can actually satisfy
# ("which GPU", not just "is there a GPU"):
#   requires=gpu         AMD GPU (legacy/back-compat value; means AMD today)
#   requires=gpu-amd     AMD Instinct GPU (amd-smi / MI*); preferred over bare gpu
#   requires=gpu-nvidia  NVIDIA GPU (nvidia-smi / H100, A100, ...)
# Bare `gpu` predates the vendor split; migrate AMD-specific tests to `gpu-amd`
# as NVIDIA coverage grows.

# Kind environment: no real accelerators, so exclude every GPU-vendor value plus
# longhorn storage and NFD-gated tests. Expensive / operator-gated
# tests (e.g. multi-hundred-GiB live model downloads) gate themselves via one of
# these `requires` values rather than a separate tier axis.
CHAINSAW_SELECTOR_KIND := requires notin (gpu,gpu-amd,gpu-nvidia,longhorn,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE)),$(CHAINSAW_NEEDS_SECRET_EXCLUDE)

# AMD GPU environment: runs AMD GPU tests (gpu, gpu-amd) end-to-end. Excludes
# Kind-only tests (mocked node labels), NVIDIA-only tests, and NFD-gated
# tests. Run an excluded test explicitly by invoking chainsaw directly against
# its dir without a selector.
CHAINSAW_SELECTOR_GPU := requires notin (kind,gpu-nvidia,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE)),$(CHAINSAW_NEEDS_SECRET_EXCLUDE)

# NVIDIA GPU environment: runs only NVIDIA GPU tests (gpu-nvidia). Excludes
# Kind-only tests and the AMD GPU values (bare gpu is AMD-built today), plus
# NFD-gated tests.
CHAINSAW_SELECTOR_NVIDIA := requires notin (kind,gpu,gpu-amd,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE)),$(CHAINSAW_NEEDS_SECRET_EXCLUDE)

# Dedicated authenticated lane for tests whose model/size makes a token a hard
# prerequisite. Normal trusted suites also receive HF_TOKEN and exercise all
# other hf-access=live tests.
CHAINSAW_HF_SELECTOR_KIND := needs-secret in (hf_token),requires notin (gpu,gpu-amd,gpu-nvidia,longhorn,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE))
CHAINSAW_HF_SELECTOR_GPU := needs-secret in (hf_token),requires notin (kind,gpu-nvidia,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE))
CHAINSAW_HF_SELECTOR_NVIDIA := needs-secret in (hf_token),requires notin (kind,gpu,gpu-amd,nfd,$(CHAINSAW_PROJECTION_MODE_EXCLUDE),$(CHAINSAW_UPGRADE_WORKFLOW_EXCLUDE))

# Select appropriate config based on ENV and CI detection
# CI is detected via CI env var (set by GitHub Actions, GitLab CI, etc.)
CHAINSAW_CONFIG_KIND := $(if $(CI),$(CHAINSAW_CONFIG_DIR)/kind-ci.yaml,$(CHAINSAW_CONFIG_DIR)/kind.yaml)
CHAINSAW_CONFIG_GPU := $(CHAINSAW_CONFIG_DIR)/gpu.yaml
# NVIDIA reuses the GPU chainsaw config (timeouts / failure-catch are vendor-agnostic).
CHAINSAW_CONFIG_NVIDIA := $(CHAINSAW_CONFIG_GPU)
CHAINSAW_ENV_CONFIG := $(if $(filter nvidia,$(ENV)),$(CHAINSAW_CONFIG_NVIDIA),$(if $(filter gpu,$(ENV)),$(CHAINSAW_CONFIG_GPU),$(CHAINSAW_CONFIG_KIND)))

# Select appropriate selector and parallelism based on ENV
CHAINSAW_ENV_SELECTOR := $(if $(filter nvidia,$(ENV)),--selector "$(CHAINSAW_SELECTOR_NVIDIA)",$(if $(filter gpu,$(ENV)),--selector "$(CHAINSAW_SELECTOR_GPU)",$(if $(filter kind,$(ENV)),--selector "$(CHAINSAW_SELECTOR_KIND)",)))
CHAINSAW_ENV_PARALLEL := $(if $(filter kind,$(ENV)),--parallel 4,)
CHAINSAW_HF_ENV_SELECTOR := $(if $(filter nvidia,$(ENV)),--selector "$(CHAINSAW_HF_SELECTOR_NVIDIA)",$(if $(filter gpu,$(ENV)),--selector "$(CHAINSAW_HF_SELECTOR_GPU)",--selector "$(CHAINSAW_HF_SELECTOR_KIND)"))

.PHONY: check-chainsaw-hf
check-chainsaw-hf: ## Validate Hugging Face Chainsaw labels, setup, and secret references.
	@hack/check-chainsaw-hf.sh

.PHONY: test-chainsaw
test-chainsaw: check-chainsaw-hf ## Run chainsaw e2e tests (selector based on ENV). Pass CHAINSAW_ARGS for additional options.
	@echo "Environment: $(ENV) (context: $(CURRENT_CONTEXT))"
	@echo "Config: $(CHAINSAW_ENV_CONFIG)"
	@echo "Selector: $(if $(filter nvidia,$(ENV)),$(CHAINSAW_SELECTOR_NVIDIA),$(if $(filter gpu,$(ENV)),$(CHAINSAW_SELECTOR_GPU),$(CHAINSAW_SELECTOR_KIND)))"
	@mkdir -p $(CHAINSAW_REPORT_DIR) $(CHAINSAW_DEBUG_DIR)
	@CHAINSAW_DEBUG_DIR="$(CURDIR)/$(CHAINSAW_DEBUG_DIR)" PATH="$(CURDIR)/hack:$(PATH)" chainsaw test --full-name --test-dir $(CHAINSAW_TEST_DIR) \
		--config $(CHAINSAW_ENV_CONFIG) \
		$(CHAINSAW_ENV_SELECTOR) \
		--report-format JSON --report-name chainsaw-report --report-path $(CHAINSAW_REPORT_DIR) \
		$(CHAINSAW_ARGS)

.PHONY: test-chainsaw-kind
test-chainsaw-kind: ## Run chainsaw e2e tests for KIND environment
	$(MAKE) test-chainsaw ENV=kind

.PHONY: test-chainsaw-hf
test-chainsaw-hf: check-chainsaw-hf ## Run authenticated live-HuggingFace tests; requires HF_TOKEN.
	@if [ -z "$${HF_TOKEN:-}" ]; then echo "HF_TOKEN must be set"; exit 1; fi
	@echo "Environment: $(ENV) (context: $(CURRENT_CONTEXT))"
	@echo "Config: $(CHAINSAW_ENV_CONFIG)"
	@echo "Selector: $(if $(filter nvidia,$(ENV)),$(CHAINSAW_HF_SELECTOR_NVIDIA),$(if $(filter gpu,$(ENV)),$(CHAINSAW_HF_SELECTOR_GPU),$(CHAINSAW_HF_SELECTOR_KIND)))"
	@mkdir -p $(CHAINSAW_REPORT_DIR) $(CHAINSAW_DEBUG_DIR)
	@CHAINSAW_DEBUG_DIR="$(CURDIR)/$(CHAINSAW_DEBUG_DIR)" PATH="$(CURDIR)/hack:$(PATH)" chainsaw test --full-name \
		--test-dir $(CHAINSAW_TEST_DIR) \
		--config $(CHAINSAW_ENV_CONFIG) \
		$(CHAINSAW_HF_ENV_SELECTOR) \
		$(CHAINSAW_ENV_PARALLEL) \
		--report-format JSON --report-name chainsaw-hf-report --report-path $(CHAINSAW_REPORT_DIR) \
		$(CHAINSAW_ARGS)

.PHONY: test-chainsaw-projection-mode
test-chainsaw-projection-mode: ## Run ONLY the mode-gated projection tests (MODE=Reduced) against an operator ALREADY running in that mode. Inverse of the default lanes' CHAINSAW_PROJECTION_MODE_EXCLUDE: the selector INCLUDES requires in (<mode>-mode). Set the mode first via the Helm value manager.runtimeProjectionMode or `make set-projection-mode MODE=...`.
	@gate="$(if $(filter Reduced,$(MODE)),reduced-mode,)"; \
	if [ -z "$$gate" ]; then echo "MODE must be Reduced (got '$(MODE)')"; exit 1; fi; \
	echo "Running gated projection-mode tests for MODE=$(MODE) (selector: requires in ($$gate))"; \
	$(MAKE) test-chainsaw \
		CHAINSAW_TEST_DIR=$(CHAINSAW_PROJECTION_MODE_DIR) \
		CHAINSAW_ENV_SELECTOR="--selector \"requires in ($$gate)\""

.PHONY: test-chainsaw-kind-zot
test-chainsaw-kind-zot: aim-dummy-zot-push ## Run Kind e2e against the ephemeral in-cluster Zot registry.
	@set -euo pipefail; \
	mkdir -p "$(CURDIR)/.tmp"; \
	zot_dir=$$(mktemp -d "$(CURDIR)/.tmp/e2e-zot.XXXXXX"); \
	trap 'rm -rf "$$zot_dir"' EXIT; \
	cp -a "$(CHAINSAW_TEST_DIR)/." "$$zot_dir/"; \
	echo "Rewriting docker.io/amdenterpriseai/aim-dummy:<tag> -> $(ZOT_AIM_DUMMY_REPO):<tag> across $$zot_dir"; \
	matches=$$(grep -rlF "docker.io/amdenterpriseai/aim-dummy:" "$$zot_dir" || true); \
	if [ -z "$$matches" ]; then echo "ERROR: no fixtures under $$zot_dir reference docker.io/amdenterpriseai/aim-dummy:"; exit 1; fi; \
	echo "$$matches" | xargs sed -i "s|docker.io/amdenterpriseai/aim-dummy:|$(ZOT_AIM_DUMMY_REPO):|g"; \
	$(MAKE) test-chainsaw ENV=kind CHAINSAW_TEST_DIR="$$zot_dir"

.PHONY: aim-dummy-ttl-push
aim-dummy-ttl-push: ## Build aim-dummy once and push it to ephemeral ttl.sh refs under every tag the fixtures reference (also loads it into the local docker daemon).
	@set -euo pipefail; \
	tags=$$(grep -rhoE 'docker\.io/amdenterpriseai/aim-dummy:[A-Za-z0-9._-]+' "$(CHAINSAW_TEST_DIR)" | sed 's|^.*:||' | sort -u); \
	if [ -z "$$tags" ]; then echo "ERROR: no aim-dummy refs found under $(CHAINSAW_TEST_DIR)"; exit 1; fi; \
	echo "Building aim-dummy -> $(TTL_AIM_DUMMY_REPO) (source hash $(AIM_DUMMY_SRC_HASH))"; \
	echo "Fixture tags -> $$(echo $$tags | tr '\n' ' ')"; \
	first=$$(printf '%s\n' $$tags | head -n1); \
	: "--load works with both the default 'docker' driver and the"; \
	: "'docker-container' driver that setup-buildx-action installs in CI, so the"; \
	: "image lands in the local daemon for the subsequent push + kind load."; \
	docker buildx build --provenance=false --sbom=false --load -t "$(TTL_AIM_DUMMY_REPO):$$first" images/aim-dummy; \
	for t in $$tags; do \
		[ "$$t" = "$$first" ] || docker tag "$(TTL_AIM_DUMMY_REPO):$$first" "$(TTL_AIM_DUMMY_REPO):$$t"; \
		docker push "$(TTL_AIM_DUMMY_REPO):$$t"; \
	done

.PHONY: test-chainsaw-gpu
test-chainsaw-gpu: ## Run chainsaw e2e tests for GPU environment
	$(MAKE) test-chainsaw ENV=gpu

.PHONY: test-chainsaw-nvidia
test-chainsaw-nvidia: ## Run chainsaw e2e tests for NVIDIA GPU environment
	$(MAKE) test-chainsaw ENV=nvidia

.PHONY: test-chainsaw-gpu-ttl
test-chainsaw-gpu-ttl: aim-dummy-ttl-push ## Run GPU e2e against an ephemeral ttl.sh aim-dummy (use while amdenterpriseai images aren't public yet). GPU nodes pull the image from public ttl.sh; the operator reads its OCI labels from there too.
	@set -euo pipefail; \
	ttl_dir="$(CHAINSAW_TEST_DIR)-ttl"; \
	rm -rf "$$ttl_dir"; cp -r "$(CHAINSAW_TEST_DIR)" "$$ttl_dir"; \
	trap 'rm -rf "$$ttl_dir"' EXIT; \
	echo "Rewriting docker.io/amdenterpriseai/aim-dummy:<tag> -> $(TTL_AIM_DUMMY_REPO):<tag> across $$ttl_dir"; \
	matches=$$(grep -rlF "docker.io/amdenterpriseai/aim-dummy:" "$$ttl_dir" || true); \
	if [ -z "$$matches" ]; then echo "ERROR: no fixtures under $$ttl_dir reference docker.io/amdenterpriseai/aim-dummy:"; exit 1; fi; \
	echo "$$matches" | xargs sed -i "s|docker.io/amdenterpriseai/aim-dummy:|$(TTL_AIM_DUMMY_REPO):|g"; \
	$(MAKE) test-chainsaw ENV=gpu CHAINSAW_TEST_DIR="$$ttl_dir"

# Focus test configuration - per-branch local config
BRANCH_NAME := $(shell git rev-parse --abbrev-ref HEAD | tr '/' '-')
BRANCH_DIR := .local/$(shell git rev-parse --abbrev-ref HEAD)
FEATURE_FILE := $(BRANCH_DIR)/feature.yaml
FEATURE_REPORT_DIR := $(BRANCH_DIR)/test-reports

.PHONY: test-chainsaw-feature
test-chainsaw-feature: ## Run focused chainsaw tests from .local/{branch}/feature.yaml
	@if [ ! -f "$(FEATURE_FILE)" ]; then \
		echo ""; \
		echo "ERROR: Focus file not found"; \
		echo ""; \
		echo "Expected: $(FEATURE_FILE)"; \
		echo ""; \
		echo "Create it with:"; \
		echo "  mkdir -p $(BRANCH_DIR)"; \
		echo "  cat > $(FEATURE_FILE) << 'EOF'"; \
		echo "  description: \"Working on feature X\""; \
		echo "  tests:"; \
		echo "    - tests/e2e/aimservice/frozen"; \
		echo "  EOF"; \
		echo ""; \
		exit 1; \
	fi
	@TIMESTAMP=$$(date +%Y%m%d-%H%M%S); \
	COMMIT=$$(git rev-parse --short HEAD); \
	REPORT_NAME="$$TIMESTAMP-$$COMMIT"; \
	mkdir -p "$(FEATURE_REPORT_DIR)"; \
	echo "=== Focus Test Run: $$REPORT_NAME ==="; \
	echo "Branch: $(BRANCH_NAME)"; \
	echo "Focus file: $(FEATURE_FILE)"; \
	echo "Description: $$(yq -r '.description // "none"' $(FEATURE_FILE))"; \
	echo "Tests:"; \
	yq -r '.tests[]' $(FEATURE_FILE) | while read dir; do echo "  - $$dir"; done; \
	echo "Report: $(FEATURE_REPORT_DIR)/$$REPORT_NAME.json"; \
	echo ""; \
	TEST_DIRS=$$(yq -r '.tests[]' $(FEATURE_FILE) | sed 's/^/--test-dir /' | tr '\n' ' '); \
	PATH="$(CURDIR)/hack:$(PATH)" chainsaw test $$TEST_DIRS \
		$(CHAINSAW_ENV_SELECTOR) \
		--report-format JSON --report-name "$$REPORT_NAME" --report-path "$(FEATURE_REPORT_DIR)" \
		$(CHAINSAW_ARGS)

.PHONY: lint
lint: ## Run golangci-lint linter
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint linter and perform fixes
	golangci-lint run --fix

.PHONY: lint-config
lint-config: ## Verify golangci-lint linter configuration
	golangci-lint config verify

##@ vCluster Management

# vCluster naming convention: aim-{username}-dev
VCLUSTER_NAME := aim-$(shell whoami)-dev
# The local development stack deliberately uses Envoy Gateway even though the
# distributable Helm chart defaults gatewayProvider to none. Helmfile installs
# the Envoy Gateway dependencies and make watch passes the matching controller
# metric scope explicitly instead of relying on the binary's legacy fallback.
DEV_GATEWAY_PROVIDER := envoyGateway
DEV_GATEWAY_ACTIVATION_SCOPE := httproute

.PHONY: vcluster-create
vcluster-create: ## Create personal vcluster, install dependencies, and connect.
	@# Disconnect first to avoid creating a nested vcluster.
	@echo "Disconnecting from any active vcluster (returns to host context)..."
	@vcluster disconnect || true
	@echo "Creating vcluster '$(VCLUSTER_NAME)' on host context '$$(kubectl config current-context)'..."
	vcluster create $(VCLUSTER_NAME) --namespace $(VCLUSTER_NAME) -f hack/dependencies/vcluster.yaml
	@echo "Installing development dependencies for $(DEV_GATEWAY_PROVIDER) (including Envoy Gateway Helm charts)..."
	helmfile sync -f hack/dependencies/helmfile.yaml.gotmpl
	@# Verify the gateway data plane came up and the default Gateway is programmed.
	@$(MAKE) verify-gateway
	@# Install the standalone collector used by the explicit Envoy dev stack.
	@$(MAKE) install-dev-envoy-collector
	@echo "Creating aim-system namespace..."
	@kubectl create namespace aim-system --dry-run=client -o yaml | kubectl apply -f -
	@echo "Installing CRDs..."
	@$(MAKE) install
	@echo "Installing RBAC..."
	@kustomize build config/local-dev-kind | kubectl apply -f - --server-side
	@$(MAKE) seaweedfs-init-bucket
	@echo "vCluster '$(VCLUSTER_NAME)' ready."

.PHONY: vcluster-delete
vcluster-delete: ## Delete personal vcluster.
	@echo "Deleting vcluster '$(VCLUSTER_NAME)'..."
	vcluster delete $(VCLUSTER_NAME) --namespace $(VCLUSTER_NAME)

.PHONY: vcluster-connect
vcluster-connect: ## Connect to personal vcluster and switch context.
	@echo "Connecting to vcluster '$(VCLUSTER_NAME)'..."
	vcluster connect $(VCLUSTER_NAME) --namespace $(VCLUSTER_NAME)

##@ Environment Info

.PHONY: env-info
env-info: ## Show current environment configuration (derived from kubectl context).
	@echo "Context: $(CURRENT_CONTEXT)"
	@echo "ENV:     $(ENV) (kind-* contexts -> kind, otherwise -> gpu; ENV=nvidia is explicit)"

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -ldflags "$(LDFLAGS)" -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	AIM_GATEWAY_ACTIVATION_SCOPE=$(DEV_GATEWAY_ACTIVATION_SCOPE) go run -ldflags "$(LDFLAGS)" ./cmd/main.go

.PHONY: run-debug
run-debug: manifests generate fmt vet ## Run a controller with debug logging enabled.
	AIM_GATEWAY_ACTIVATION_SCOPE=$(DEV_GATEWAY_ACTIVATION_SCOPE) go run -ldflags "$(LDFLAGS)" ./cmd/main.go --zap-log-level=debug

.PHONY: watch
watch: manifests generate install ## Run controller with live reload on file changes.
	AIM_GATEWAY_ACTIVATION_SCOPE=$(DEV_GATEWAY_ACTIVATION_SCOPE) air

.PHONY: tilt-up
tilt-up: ## Run controller in cluster with Tilt (live reload, in-container builds).
	tilt up -f hack/tilt/Tiltfile

.PHONY: tilt-up-debug
tilt-up-debug: ## Run controller in cluster with Tilt in debug mode (Delve on port 2345).
	tilt up -f hack/tilt/Tiltfile -- --debug

.PHONY: tilt-down
tilt-down: ## Tear down Tilt resources.
	tilt down -f hack/tilt/Tiltfile

.PHONY: wait-ready
wait-ready: ## Wait for operator readiness probe to succeed.
	@until curl -sf http://localhost:8081/readyz >/dev/null 2>&1; do sleep 0.5; done
	@echo "Operator ready"

# Namespace/deployment of the in-cluster operator (kustomize/Tilt or Helm install).
OPERATOR_NAMESPACE ?= aim-system
OPERATOR_DEPLOYMENT ?= aim-engine-controller-manager

.PHONY: set-projection-mode
set-projection-mode: ## Redeploy the in-cluster operator with a chosen eager runtime projection mode (MODE=Exhaustive|Reduced|Both) and wait for rollout. No rebuild needed; the flag is compiled in. Used by the opt-in projection-mode chainsaw lane.
	@MODE="$(MODE)"; \
	case "$$MODE" in \
	  Exhaustive|Reduced|Both) ;; \
	  *) echo "MODE must be one of Exhaustive, Reduced, Both (got '$$MODE')"; exit 1 ;; \
	esac; \
	command -v jq >/dev/null 2>&1 || { echo "jq is not installed"; exit 1; }; \
	echo "Setting --runtime-projection-mode=$$MODE on $(OPERATOR_DEPLOYMENT) (namespace $(OPERATOR_NAMESPACE))..."; \
	current="$$(kubectl -n $(OPERATOR_NAMESPACE) get deployment $(OPERATOR_DEPLOYMENT) -o jsonpath='{.spec.template.spec.containers[0].args}')"; \
	[ -n "$$current" ] || current='[]'; \
	args="$$(printf '%s' "$$current" | jq -c --arg mode "$$MODE" '[.[] | select(startswith("--runtime-projection-mode=") | not)] + ["--runtime-projection-mode=" + $$mode]')"; \
	echo "Preserving existing args, setting projection mode: $$args"; \
	kubectl -n $(OPERATOR_NAMESPACE) patch deployment $(OPERATOR_DEPLOYMENT) --type=json \
	  -p "[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/args\",\"value\":$$args}]"; \
	kubectl -n $(OPERATOR_NAMESPACE) rollout status deployment/$(OPERATOR_DEPLOYMENT) --timeout=180s; \
	echo "Operator now running with --runtime-projection-mode=$$MODE."

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build \
		--build-arg VERSION=$(TAG) \
		--build-arg ARTIFACT_DOWNLOADER_IMG=$(ARTIFACT_DOWNLOADER_IMG) \
		-t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name aim-engine-builder
	$(CONTAINER_TOOL) buildx use aim-engine-builder
	- $(CONTAINER_TOOL) buildx build --push \
		--platform=$(PLATFORMS) \
		--build-arg VERSION=$(TAG) \
		--build-arg ARTIFACT_DOWNLOADER_IMG=$(ARTIFACT_DOWNLOADER_IMG) \
		--tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm aim-engine-builder
	rm Dockerfile.cross

.PHONY: sync-detector-images
sync-detector-images: ## Sync accelerator-detector image refs in config/accelerator-detector/kustomization.yaml from config/helm/values.yaml.
	@# Keeps the kustomize image transformers in lockstep with the Helm chart's
	@# acceleratorDetector.{gpu,cpu}.image values. Without this, install.yaml
	@# ships with placeholder accelerator-detector-{gpu,cpu}:latest refs that
	@# don't resolve anywhere. Wired into build-installer (release builds) and
	@# the sync-detector-images pre-commit hook (drift detection).
	@command -v yq >/dev/null 2>&1 || { echo "yq is not installed"; exit 1; }
	@DETECTOR_GPU_REPO="$$(yq '.acceleratorDetector.gpu.image.repository' config/helm/values.yaml)"; \
	 DETECTOR_GPU_TAG="$$(yq '.acceleratorDetector.gpu.image.tag' config/helm/values.yaml)"; \
	 DETECTOR_CPU_REPO="$$(yq '.acceleratorDetector.cpu.image.repository' config/helm/values.yaml)"; \
	 DETECTOR_CPU_TAG="$$(yq '.acceleratorDetector.cpu.image.tag' config/helm/values.yaml)"; \
	 DETECTOR_NVIDIA_REPO="$$(yq '.acceleratorDetector.nvidia.image.repository' config/helm/values.yaml)"; \
	 DETECTOR_NVIDIA_TAG="$$(yq '.acceleratorDetector.nvidia.image.tag' config/helm/values.yaml)"; \
	 for v in "$${DETECTOR_GPU_REPO}" "$${DETECTOR_GPU_TAG}" "$${DETECTOR_CPU_REPO}" "$${DETECTOR_CPU_TAG}" "$${DETECTOR_NVIDIA_REPO}" "$${DETECTOR_NVIDIA_TAG}"; do \
	   if [ -z "$${v}" ] || [ "$${v}" = "null" ]; then \
	     echo "ERROR: missing acceleratorDetector image value in config/helm/values.yaml"; exit 1; \
	   fi; \
	 done; \
	 echo "  - GPU detector image: $${DETECTOR_GPU_REPO}:$${DETECTOR_GPU_TAG}"; \
	 echo "  - CPU detector image: $${DETECTOR_CPU_REPO}:$${DETECTOR_CPU_TAG}"; \
	 echo "  - NVIDIA detector image: $${DETECTOR_NVIDIA_REPO}:$${DETECTOR_NVIDIA_TAG}"; \
	 cd config/accelerator-detector && \
	   kustomize edit set image accelerator-detector-gpu="$${DETECTOR_GPU_REPO}:$${DETECTOR_GPU_TAG}" && \
	   kustomize edit set image accelerator-detector-cpu="$${DETECTOR_CPU_REPO}:$${DETECTOR_CPU_TAG}" && \
	   kustomize edit set image accelerator-detector-nvidia="$${DETECTOR_NVIDIA_REPO}:$${DETECTOR_NVIDIA_TAG}"

.PHONY: build-installer
build-installer: manifests generate sync-detector-images ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && kustomize edit set image controller=${IMG}
	kustomize build config/default > dist/install.yaml

.PHONY: helm
helm: build-installer ## Generate Helm chart from kustomize output.
	kubebuilder edit --plugins=helm/v2-alpha
	@./hack/patch-helm-chart.sh
	@echo "Helm chart generated at dist/chart/"

.PHONY: crds
crds: manifests ## Generate consolidated CRDs file for distribution.
	@mkdir -p dist
	@cat config/crd/bases/*.yaml > dist/crds.yaml
	@echo "CRDs generated at dist/crds.yaml"

##@ Helm

.PHONY: helm-package
helm-package: helm ## Package the Helm chart into a .tgz file.
	@command -v helm >/dev/null 2>&1 || { echo "Helm is not installed"; exit 1; }
	@command -v yq >/dev/null 2>&1 || { echo "yq is not installed"; exit 1; }
	@echo "Packaging Helm chart with version $(CHART_VERSION) and app version $(APP_VERSION)"
	@echo "  - Setting manager.image.repository=$(CHART_IMAGE_REPO), manager.image.tag=$(CHART_IMAGE_TAG)"
	@CHART_IMAGE_REPO='$(CHART_IMAGE_REPO)' CHART_IMAGE_TAG='$(CHART_IMAGE_TAG)' \
		yq -i '.manager.image.repository = strenv(CHART_IMAGE_REPO) | .manager.image.tag = strenv(CHART_IMAGE_TAG)' dist/chart/values.yaml
	@sed -i.bak 's/^name:.*/name: $(CHART_NAME)/' dist/chart/Chart.yaml
	@sed -i.bak 's/^version:.*/version: $(CHART_VERSION)/' dist/chart/Chart.yaml
	@sed -i.bak 's/^appVersion:.*/appVersion: "$(APP_VERSION)"/' dist/chart/Chart.yaml
	helm package dist/chart --version=$(CHART_VERSION) --app-version=$(APP_VERSION) --destination=dist/
	@rm -f dist/chart/Chart.yaml.bak

.PHONY: helm-push-oci
helm-push-oci: ## Push Helm chart to OCI registry (requires helm-package first).
	@echo "Pushing Helm chart to OCI registry $(CHART_OCI_REPO)..."
	helm push dist/$(CHART_NAME)-$(CHART_VERSION).tgz $(CHART_OCI_REPO)

.PHONY: crds-package
crds-package: crds ## Package CRDs as a Helm chart .tgz for OCI distribution.
	@echo "Packaging CRDs as Helm chart with version $(CHART_VERSION)..."
	@rm -rf dist/crds-chart
	@mkdir -p dist/crds-chart/templates
	@printf 'apiVersion: v2\nname: %s\ndescription: CRDs for aim-engine operator\ntype: application\nversion: %s\nappVersion: "%s"\n' \
		"$(CRDS_CHART_NAME)" "$(CHART_VERSION)" "$(APP_VERSION)" > dist/crds-chart/Chart.yaml
	@cp dist/crds.yaml dist/crds-chart/templates/crds.yaml
	helm package dist/crds-chart --version=$(CHART_VERSION) --app-version=$(APP_VERSION) --destination=dist/
	@echo "CRDs chart packaged at dist/$(CRDS_CHART_NAME)-$(CHART_VERSION).tgz"

.PHONY: crds-push-oci
crds-push-oci: ## Push CRDs Helm chart to OCI registry (requires crds-package first).
	@echo "Pushing CRDs chart to OCI registry $(CHART_OCI_REPO)..."
	helm push dist/$(CRDS_CHART_NAME)-$(CHART_VERSION).tgz $(CHART_OCI_REPO)

##@ Release

.PHONY: release-prep
release-prep: ## Create release branch and generate context. Usage: make release-prep VERSION=v0.2.0
	@test -n "$(VERSION)" || { echo "Usage: make release-prep VERSION=vX.Y.Z"; exit 1; }
	@bash hack/release/prep.sh $(VERSION)

.PHONY: release
release: ## Tag and push a release. Usage: make release VERSION=v0.2.0 [YES=1]
	@test -n "$(VERSION)" || { echo "Usage: make release VERSION=vX.Y.Z"; exit 1; }
	@bash hack/release/release.sh $(VERSION) $(if $(YES),--yes)

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( kustomize build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | kubectl apply --server-side -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( kustomize build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | kubectl delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && kustomize edit set image controller=${IMG}
	kustomize build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	kustomize build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

.PHONY: third-party-licenses
third-party-licenses: ## Generate third-party licenses directory.
	@echo "Generating third-party licenses..."
	@rm -rf third-party-licenses
	go-licenses save ./... --save_path=third-party-licenses --ignore github.com/amd-enterprise-ai/aim-engine 2>/dev/null || true
	@git add third-party-licenses/ 2>/dev/null || true

.PHONY: generate-crd-docs
generate-crd-docs:
	go install github.com/elastic/crd-ref-docs@latest
	crd-ref-docs --source-path api/v1alpha1/ --renderer=markdown --output-path=docs/docs/reference/api/v1alpha1.md --config docs/crd-ref-docs-config.yaml
	crd-ref-docs --source-path api/v1alpha2/ --renderer=markdown --output-path=docs/docs/reference/api/v1alpha2.md --config docs/crd-ref-docs-config.yaml
	python3 -B hack/test_format_crd_docs.py
	python3 hack/format-crd-docs.py docs/docs/reference/api/v1alpha1.md docs/docs/reference/api/v1alpha2.md

.PHONY: generate-helm-docs
generate-helm-docs: ## Generate Helm chart values reference from config/helm/values.yaml.
	go run hack/generate-helm-values-docs.go

.PHONY: generate-docs
generate-docs: generate-crd-docs generate-helm-docs ## Generate all documentation (CRD API reference + Helm values).

# ---------------------------------------------------------------------------
# Documentation (Sphinx + D2)
# ---------------------------------------------------------------------------
DOCS_PORT ?= 8000
DOCS_BUILD := docs/docs/_build/html
DIAGRAM_SRC_DIR := docs/diagrams
DIAGRAM_OUT_DIR := docs/docs/assets/diagrams

.PHONY: diagrams
diagrams: ## Render D2 diagram sources (docs/diagrams/*.d2) to committed SVGs.
	@mkdir -p $(DIAGRAM_OUT_DIR)
	@for f in $(DIAGRAM_SRC_DIR)/*.d2; do \
		base=$$(basename $$f .d2); \
		case $$base in _*) continue ;; esac; \
		echo "d2: $$f -> $$base.svg + $$base-dark.svg"; \
		d2 fmt $$f >/dev/null; \
		d2 $$f $(DIAGRAM_OUT_DIR)/$$base.svg || exit 1; \
		d2 --theme 200 $$f $(DIAGRAM_OUT_DIR)/$$base-dark.svg || exit 1; \
	done
	@DIAGRAM_OUT_DIR=$(DIAGRAM_OUT_DIR) hack/inject-svg-license.sh

.PHONY: diagrams-watch
diagrams-watch: ## Live-preview one diagram: make diagrams-watch DIAGRAM=architecture-overview
	@if [ -z "$(DIAGRAM)" ]; then \
		echo "Usage: make diagrams-watch DIAGRAM=<name>  (a file in $(DIAGRAM_SRC_DIR) without .d2)"; \
		echo "Available diagrams:"; \
		ls $(DIAGRAM_SRC_DIR)/*.d2 | sed 's#.*/##; s#\.d2$$##; /^_/d' | sed 's/^/  /'; \
		exit 1; \
	fi
	d2 --watch $(DIAGRAM_SRC_DIR)/$(DIAGRAM).d2 $(DIAGRAM_OUT_DIR)/$(DIAGRAM).svg

.PHONY: diagrams-check
diagrams-check: diagrams ## Fail if committed SVGs drift from their D2 sources (used by CI / pre-commit).
	@if [ -n "$$(git status --porcelain -- $(DIAGRAM_OUT_DIR))" ]; then \
		echo "ERROR: rendered diagrams are out of date or uncommitted. Run 'make diagrams' and commit the result."; \
		git --no-pager status --porcelain -- $(DIAGRAM_OUT_DIR); \
		exit 1; \
	fi

.PHONY: docs-deps
docs-deps:
	@cd docs && \
	if [ ! -d ".venv" ]; then \
		echo "Creating virtual environment in docs/.venv..."; \
		python3 -m venv .venv; \
	fi && \
	. .venv/bin/activate && \
	echo "Installing dependencies..." && \
	pip install -q -r requirements.txt

.PHONY: docs-serve
docs-serve: docs-deps ## Live-reloading Sphinx dev server (rebuilds on save; pair with diagrams-watch).
	@cd docs && . .venv/bin/activate && \
	WSL_IP=$$(hostname -I | awk '{print $$1}') && \
	echo "Starting Sphinx dev server..." && \
	echo "  http://localhost:$(DOCS_PORT)/" && \
	echo "  http://$$WSL_IP:$(DOCS_PORT)/" && \
	sphinx-autobuild docs docs/_build/html --host 0.0.0.0 --port $(DOCS_PORT)

.PHONY: docs-build
docs-build: docs-deps ## Build the docs site, treating warnings (broken refs, etc.) as errors.
	@cd docs && . .venv/bin/activate && \
	sphinx-build -b html -W --keep-going docs docs/_build/html
	@echo "Docs built to $(DOCS_BUILD)"
