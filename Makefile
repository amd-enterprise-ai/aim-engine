# Default tag from git
TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "latest")

# Image URL to use for all building/pushing image targets
IMG ?= ghcr.io/amd-enterprise-ai/aim-engine:$(TAG)

# Helm chart configuration
CHART_DIR ?= chart
CHART_NAME ?= aim-engine
CHART_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.1")
APP_VERSION ?= $(TAG)
CHART_OCI_REGISTRY ?= $(shell echo $(IMG) | cut -d'/' -f1)
CHART_OCI_OWNER ?= $(shell echo $(IMG) | cut -d'/' -f2)
CHART_OCI_REPO ?= oci://$(CHART_OCI_REGISTRY)/$(CHART_OCI_OWNER)/charts

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
	controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

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
test: manifests generate fmt vet ## Run tests.
	go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

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

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: ## Run golangci-lint linter
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint linter and perform fixes
	golangci-lint run --fix

.PHONY: lint-config
lint-config: ## Verify golangci-lint linter configuration
	golangci-lint config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: run-debug
run-debug: manifests generate fmt vet ## Run a controller with debug logging enabled.
	go run ./cmd/main.go --zap-log-level=debug

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

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
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm aim-engine-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && kustomize edit set image controller=${IMG}
	kustomize build config/default > dist/install.yaml
	# Reset the image to the default
	cd config/manager && kustomize edit set image controller=ghcr.io/amd-enterprise-ai/aim-engine:v-e2e

##@ Helm

# Function to copy resources needed for Helm chart
define copy-helm-resources
	@echo "Copying RBAC resources from config/rbac..."
	@cat config/rbac/role.yaml > $(CHART_DIR)/rbac-resources.yaml
	@echo "---" >> $(CHART_DIR)/rbac-resources.yaml
	@cat config/rbac/role_binding.yaml >> $(CHART_DIR)/rbac-resources.yaml
	@echo "---" >> $(CHART_DIR)/rbac-resources.yaml
	@cat config/rbac/leader_election_role.yaml >> $(CHART_DIR)/rbac-resources.yaml
	@echo "---" >> $(CHART_DIR)/rbac-resources.yaml
	@cat config/rbac/leader_election_role_binding.yaml >> $(CHART_DIR)/rbac-resources.yaml
endef

.PHONY: copy-helm-resources
copy-helm-resources: ## Copy necessary resources into Helm chart directory
	$(call copy-helm-resources)

# Function to clean up copied resources
define clean-helm-resources
	@rm -f $(CHART_DIR)/rbac-resources.yaml
endef

.PHONY: helm-package
helm-package: build-installer ## Package the Helm chart
	@command -v helm >/dev/null 2>&1 || { \
		echo "Helm is not installed. Please install Helm."; \
		exit 1; \
	}
	@echo "Packaging Helm chart with version $(CHART_VERSION) and app version $(APP_VERSION)"
	$(call copy-helm-resources)
	@sed -i.bak 's/^version:.*/version: $(CHART_VERSION)/' $(CHART_DIR)/Chart.yaml
	@sed -i.bak 's/^appVersion:.*/appVersion: "$(APP_VERSION)"/' $(CHART_DIR)/Chart.yaml
	helm package $(CHART_DIR) --version=$(CHART_VERSION) --app-version=$(APP_VERSION) --destination=dist/
	@rm -f $(CHART_DIR)/Chart.yaml.bak
	$(call clean-helm-resources)

.PHONY: helm-install
helm-install: helm-package ## Install the Helm chart locally
	@echo "Installing Helm chart to aim-engine-system namespace..."
	helm upgrade --install aim-engine dist/$(CHART_NAME)-$(CHART_VERSION).tgz --namespace aim-engine-system --create-namespace

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the Helm chart
	helm uninstall aim-engine -n aim-engine-system

.PHONY: helm-template
helm-template: build-installer ## Generate Helm templates for inspection
	$(call copy-helm-resources)
	@sed -i.bak 's/^appVersion:.*/appVersion: "$(APP_VERSION)"/' $(CHART_DIR)/Chart.yaml
	helm template aim-engine $(CHART_DIR) --namespace aim-engine-system --output-dir dist/helm-output/
	@rm -f $(CHART_DIR)/Chart.yaml.bak
	@echo "Note: rbac-resources.yaml left in chart/ for direct helm usage. Run 'make helm-clean' to remove."

.PHONY: helm-push-oci
helm-push-oci: helm-package ## Push Helm chart to OCI registry
	@echo "Pushing Helm chart to OCI registry..."
	helm push dist/$(CHART_NAME)-$(CHART_VERSION).tgz $(CHART_OCI_REPO)

.PHONY: helm-release
helm-release: helm-package ## Package chart for release (used by CI)
	@echo "Helm chart packaged for release: dist/$(CHART_NAME)-$(CHART_VERSION).tgz"

.PHONY: helm
helm: build-installer copy-helm-resources ## Generate Helm chart for CI/CD (copies chart to dist/chart for compatibility)
	@mkdir -p dist/chart
	@cp -r $(CHART_DIR)/* dist/chart/
	@echo "Helm chart copied to dist/chart/"

.PHONY: helm-clean
helm-clean: ## Clean up generated Helm resources from chart directory
	$(call clean-helm-resources)
	@echo "Cleaned up generated Helm resources from chart/"

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( kustomize build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | kubectl apply -f -; else echo "No CRDs to install; skipping."; fi

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
	crd-ref-docs --source-path api/v1alpha1/ --renderer=markdown --output-path=docs/docs/reference/crds/v1alpha1/aim.eai.amd.com.md --config docs/crd-ref-docs-config.yaml
