# Setting SHELL to bash allows bash commands to be executed by recipes. 
# This is a requirement for 'setup-envtest.sh' in the test target. 
# Options are set to exit when a recipe line exits non-zero or a piped command fails. 
SHELL = /usr/bin/env bash -o pipefail 
.SHELLFLAGS = -ec 
CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
KUBECONFIG?=$(HOME)/.kube/config
export OPERATOR_EXEC?=oc

BUILD_GOPATH=$(TARGET_DIR):$(TARGET_DIR)/vendor:$(CURPATH)/cmd
IMAGE_BUILDER?=docker
IMAGE_BUILD_OPTS?=
DOCKERFILE?=Dockerfile

CRD_BASES=./config/crd/bases

export APP_NAME=sriov-network-operator
TARGET=$(TARGET_DIR)/bin/$(APP_NAME)
IMAGE_TAG?=ghcr.io/k8snetworkplumbingwg/$(APP_NAME):latest
MAIN_PKG=cmd/manager/main.go
export NAMESPACE?=openshift-sriov-network-operator
export WATCH_NAMESPACE?=openshift-sriov-network-operator
export GOFLAGS+=-mod=vendor
export GO111MODULE=on
PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test|/examples')

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

# Current Operator version
VERSION ?= 4.7.0
# Default bundle image tag
BUNDLE_IMG ?= controller-bundle:$(VERSION)
# Options for 'bundle-build'
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:crdVersions={v1},trivialVersions=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: all build clean gendeepcopy test test-e2e test-e2e-k8s run image fmt sync-manifests test-e2e-conformance manifests update-codegen

all: generate vet build plugins

build: manager _build-sriov-network-config-daemon _build-webhook

_build-%:
	WHAT=$* hack/build-go.sh

_plugin-%: vet
	@hack/build-plugins.sh $*

plugins: _plugin-intel _plugin-mellanox _plugin-generic _plugin-virtual _plugin-k8s

clean:
	@rm -rf $(TARGET_DIR)

update-codegen:
	hack/update-codegen.sh

image: ; $(info Building image...)
	$(IMAGE_BUILDER) build -f $(DOCKERFILE) -t $(IMAGE_TAG) $(CURPATH) $(IMAGE_BUILD_OPTS)

# Run tests
ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: generate vet manifests
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.8.3/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./... -coverprofile cover.out -v

# Build manager binary
manager: generate vet _build-manager

# Run against the configured Kubernetes cluster in ~/.kube/config
run: vet skopeo install
	hack/run-locally.sh

# Install CRDs into a cluster
install: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
# deploy: manifests kustomize
# 	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
# 	$(KUSTOMIZE) build config/default | kubectl apply -f -

# UnDeploy controller from the configured Kubernetes cluster in ~/.kube/config
# undeploy:
# 	$(KUSTOMIZE) build config/default | kubectl delete -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) webhook paths="./..." output:crd:artifacts:config=$(CRD_BASES)


sync-manifests-%: manifests
	@mkdir -p manifests/$*
	sed '2{/---/d}' $(CRD_BASES)/sriovnetwork.openshift.io_sriovibnetworks.yaml | awk 'NF' > manifests/$*/sriov-network-operator-sriovibnetworks_crd.yaml
	sed '2{/---/d}' $(CRD_BASES)/sriovnetwork.openshift.io_sriovnetworknodepolicies.yaml | awk 'NF' > manifests/$*/sriov-network-operator-sriovnetworknodepolicy.crd.yaml
	sed '2{/---/d}' $(CRD_BASES)/sriovnetwork.openshift.io_sriovnetworknodestates.yaml | awk 'NF' > manifests/$*/sriov-network-operator-sriovnetworknodestate.crd.yaml
	sed '2{/---/d}' $(CRD_BASES)/sriovnetwork.openshift.io_sriovoperatorconfigs.yaml | awk 'NF' > manifests/$*/sriov-network-operator-sriovoperatorconfig.crd.yaml
	sed '2{/---/d}' $(CRD_BASES)/sriovnetwork.openshift.io_sriovnetworks.yaml | awk 'NF' > manifests/$*/sriov-network-operator-sriovnetwork.crd.yaml
	@echo ""
	@echo "*************************************************************************************************************************************************"
	@echo "* Please manually update the sriov-network-operator.v4.7.0.clusterserviceversion.yaml and image-references files in the manifests/$* directory *"
	@echo "*************************************************************************************************************************************************"
	@echo ""


# Run go fmt against code

fmt: ## Go fmt your code
	CONTAINER_CMD=$(IMAGE_BUILDER) hack/go-fmt.sh .

# Run go fmt against code
fmt-code:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

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

skopeo:
	if ! which skopeo; then if [ -z ${SKIP_VAR_SET} ]; then if [ -f /etc/redhat-release ]; then dnf -y install skopeo; elif [ -f /etc/lsb-release ]; then sudo apt-get -y update; sudo apt-get -y install skopeo; fi; fi; fi

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests
	operator-sdk generate kustomize manifests --interactive=false -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | operator-sdk generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate ./bundle

# Build the bundle image.
.PHONY: bundle-build
bundle-build:
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

deploy-setup: export ENABLE_ADMISSION_CONTROLLER?=true
deploy-setup: skopeo install
	hack/deploy-setup.sh $(NAMESPACE)

deploy-setup-k8s: export NAMESPACE=sriov-network-operator
deploy-setup-k8s: export ENABLE_ADMISSION_CONTROLLER?=false
deploy-setup-k8s: export CNI_BIN_PATH=/opt/cni/bin
deploy-setup-k8s: export OPERATOR_EXEC=kubectl
deploy-setup-k8s: export CLUSTER_TYPE=kubernetes
deploy-setup-k8s: deploy-setup

test-e2e-conformance:
	SUITE=./test/conformance ./hack/run-e2e-conformance.sh

test-e2e-validation-only:
	SUITE=./test/validation ./hack/run-e2e-conformance.sh	

test-e2e: generate vet manifests skopeo
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.8.3/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); source hack/env.sh; go test ./test/e2e/... -timeout 60m -coverprofile cover.out -v

test-e2e-k8s: export NAMESPACE=sriov-network-operator
test-e2e-k8s: test-e2e

test-%: generate vet manifests
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.8.3/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./$*/... -coverprofile cover.out -v

# deploy-setup-k8s: export NAMESPACE=sriov-network-operator
# deploy-setup-k8s: export ENABLE_ADMISSION_CONTROLLER=false
# deploy-setup-k8s: export CNI_BIN_PATH=/opt/cni/bin
# test-e2e-k8s: test-e2e

deploy-wait:
	hack/deploy-wait.sh

undeploy: uninstall
	@hack/undeploy.sh $(NAMESPACE)

undeploy-k8s: export NAMESPACE=sriov-network-operator
undeploy-k8s: export OPERATOR_EXEC=kubectl
undeploy-k8s: undeploy

deps-update:
	go mod tidy && \
	go mod vendor
