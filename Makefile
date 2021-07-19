# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
VERSION ?= 0.0.1

# CHANNELS define the bundle channels used in the bundle.
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "preview,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=preview,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="preview,fast,stable")
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle.
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# IMAGE_TAG_BASE defines the docker.io namespace and part of the image name for remote images.
# This variable is used to construct full image tags for bundle and catalog images.
#
# For example, running 'make bundle-build bundle-push catalog-build catalog-push' will build and push both
# example.com/tmp-sdk-bundle:$VERSION and example.com/tmp-sdk-catalog:$VERSION.
IMAGE_TAG_BASE ?= quay.io/ramendr/ramen

# BUNDLE_IMG defines the image:tag used for the bundle.
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG_HUB ?= $(IMAGE_TAG_BASE)-hub-operator-bundle:v$(VERSION)
BUNDLE_IMG_DRCLUSTER ?= $(IMAGE_TAG_BASE)-dr-cluster-operator-bundle:v$(VERSION)

# Image URL to use all building/pushing image targets
IMG ?= quay.io/ramendr/ramen-operator:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true,preserveUnknownFields=false"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=operator-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

GOLANGCI_URL := https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh
GOLANGCI_VERSION := 1.37.1
GOLANGCI_INSTALLED_VER := $(shell $(GOBIN)/golangci-lint version --format=short 2>&1)
.PHONY: golangci-bin
golangci-bin: ## Download and install goloanci-lint locally if necessary.
ifeq (,$(GOLANGCI_INSTALLED_VER))
	$(info Installing golangci-lint (version: $(GOLANGCI_VERSION)) into $(GOBIN))
	curl -sSfL $(GOLANGCI_URL) | sh -s -- -b $(GOBIN) v$(GOLANGCI_VERSION)
else ifneq ($(GOLANGCI_VERSION),$(GOLANGCI_INSTALLED_VER))
	$(error Incorrect version ($(GOLANGCI_INSTALLED_VER)) for golanci-lint found, expecting $(GOLANGCI_VERSION))
endif

.PHONY: lint
lint: golangci-bin ## Run configured golangci-lint linters against the code.
	$(GOBIN)/golangci-lint run ./...

# Run tests
ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: generate manifests ## Run tests.
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.8.3/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./... -coverprofile cover.out

##@ Build

# Build manager binary
build: generate  ## Build manager binary.
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run-hub: generate manifests ## Run DR Orchestrator controller from your host.
	go run ./main.go --config=examples/dr_hub_config.yaml

run-dr-cluster: generate manifests ## Run DR manager controller from your host.
	go run ./main.go --config=examples/dr_cluster_config.yaml

docker-build: test ## Build docker image with the manager.
	docker build -t ${IMG} .

docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

install: install-hub install-dr-cluster ## Install hub and dr-cluster CRDs into the K8s cluster specified in ~/.kube/config.

uninstall: uninstall-hub uninstall-dr-cluster ## Uninstall hub and dr-cluster CRDs from the K8s cluster specified in ~/.kube/config.

deploy: deploy-hub deploy-dr-cluster ## Deploy hub and dr-cluster controller to the K8s cluster specified in ~/.kube/config.

undeploy: undeploy-hub undeploy-dr-cluster ## Undeploy hub and dr-cluster controller from the K8s cluster specified in ~/.kube/config.

install-hub: manifests kustomize ## Install hub CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/hub/crd | kubectl apply -f -

uninstall-hub: manifests kustomize ## Uninstall hub CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/hub/crd | kubectl delete -f -

deploy-hub: manifests kustomize ## Deploy hub controller to the K8s cluster specified in ~/.kube/config.
	cd config/hub/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build --load_restrictor none config/hub/default | kubectl apply -f -

undeploy-hub: ## Undeploy hub controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/hub/default | kubectl delete -f -

install-dr-cluster: manifests kustomize ## Install dr-cluster CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/dr_cluster/crd | kubectl apply -f -

uninstall-dr-cluster: manifests kustomize ## Uninstall dr-cluster CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/dr_cluster/crd | kubectl delete -f -

deploy-dr-cluster: manifests kustomize ## Deploy dr-cluster controller to the K8s cluster specified in ~/.kube/config.
	cd config/dr_cluster/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build --load_restrictor none config/dr_cluster/default | kubectl apply -f -

undeploy-dr-cluster: ## Undeploy dr-cluster controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build --load_restrictor none config/dr_cluster/default | kubectl delete -f -

##@ Tools

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1)

KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize: ## Download kustomize locally if necessary.
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v3@v3.8.7)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

##@ Bundle

.PHONY: bundle
bundle: bundle-hub bundle-dr-cluster ## Generate all bundle manifests and metadata, then validate generated files.

.PHONY: bundle-build
bundle-build: bundle-hub-build bundle-dr-cluster-build ## Build all bundle images.

.PHONY: bundle-push
bundle-push: bundle-hub-push bundle-dr-cluster-push ## Push all bundle images.

.PHONY: bundle-hub
bundle-hub: manifests kustomize ## Generate hub bundle manifests and metadata, then validate generated files.
	cd config/hub/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build --load_restrictor none config/hub/manifests | operator-sdk generate bundle -q --package=ramen-hub --overwrite --output-dir=config/hub/bundle --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate config/hub/bundle

.PHONY: bundle-hub-build
bundle-hub-build: bundle-hub ## Build the hub bundle image.
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG_HUB) .

.PHONY: bundle-hub-push
bundle-hub-push: ## Push the hub bundle image.
	$(MAKE) docker-push IMG=$(BUNDLE_IMG_HUB)

.PHONY: bundle-dr-cluster
bundle-dr-cluster: manifests kustomize ## Generate dr-cluster bundle manifests and metadata, then validate generated files.
	cd config/dr_cluster/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build --load_restrictor none config/dr_cluster/manifests | operator-sdk generate bundle -q --package=ramen-dr-cluster --overwrite --output-dir=config/dr_cluster/bundle --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate config/dr_cluster/bundle

.PHONY: bundle-dr-cluster-build
bundle-dr-cluster-build: bundle-dr-cluster ## Build the dr-cluster bundle image.
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG_DRCLUSTER) .

.PHONY: bundle-dr-cluster-push
bundle-dr-cluster-push: ## Push the dr-cluster bundle image.
	$(MAKE) docker-push IMG=$(BUNDLE_IMG_DRCLUSTER)

.PHONY: opm
OPM = ./bin/opm
opm: ## Download opm locally if necessary.
ifeq (,$(wildcard $(OPM)))
ifeq (,$(shell which opm 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.15.1/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
else
OPM = $(shell which opm)
endif
endif

# A comma-separated list of bundle images (e.g. make catalog-build BUNDLE_IMGS=example.com/operator-bundle:v0.1.0,example.com/operator-bundle:v0.2.0).
# These images MUST exist in a registry and be pull-able.
BUNDLE_IMGS ?= $(BUNDLE_IMG_HUB),$(BUNDLE_IMG_DRCLUSTER)

# The image tag given to the resulting catalog image (e.g. make catalog-build CATALOG_IMG=example.com/operator-catalog:v0.2.0).
CATALOG_IMG ?= $(IMAGE_TAG_BASE)-operator-catalog:v$(VERSION)

# Set CATALOG_BASE_IMG to an existing catalog image tag to add $BUNDLE_IMGS to that image.
ifneq ($(origin CATALOG_BASE_IMG), undefined)
FROM_INDEX_OPT := --from-index $(CATALOG_BASE_IMG)
endif

# Build a catalog image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: catalog-build
catalog-build: opm ## Build a catalog image.
	$(OPM) index add --container-tool docker --mode semver --tag $(CATALOG_IMG) --bundles $(BUNDLE_IMGS) $(FROM_INDEX_OPT)

# Push the catalog image.
.PHONY: catalog-push
catalog-push: ## Push a catalog image.
	$(MAKE) docker-push IMG=$(CATALOG_IMG)
