# Setting SHELL to bash allows bash commands to be executed by recipes. 
# This is a requirement for 'setup-envtest.sh' in the test target. 
# Options are set to exit when a recipe line exits non-zero or a piped command fails. 
SHELL = /usr/bin/env bash -o pipefail 
.SHELLFLAGS = -ec 
CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
BIN_DIR=$(CURPATH)/bin
KUBECONFIG?=$(HOME)/.kube/config
export OPERATOR_EXEC?=oc

BUILD_GOPATH=$(TARGET_DIR):$(TARGET_DIR)/vendor:$(CURPATH)/cmd
IMAGE_BUILDER?=docker
IMAGE_BUILD_OPTS?=
DOCKERFILE?=Dockerfile

CRD_BASES=./config/crd/bases

export APP_NAME?=sriov-network-operator
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
CRD_OPTIONS ?= "crd:crdVersions={v1}"

GOLANGCI_LINT = $(BIN_DIR)/golangci-lint
# golangci-lint version should be updated periodically
# we keep it fixed to avoid it from unexpectedly failing on the project
# in case of a version bump
GOLANGCI_LINT_VER = v1.54.2


.PHONY: all build clean gendeepcopy test test-e2e test-e2e-k8s run image fmt sync-manifests test-e2e-conformance manifests update-codegen

all: generate vet build

build: manager _build-sriov-network-config-daemon _build-webhook

_build-%:
	WHAT=$* hack/build-go.sh

clean:
	@rm -rf $(TARGET_DIR)
	@rm -rf $(BIN_DIR)

update-codegen:
	hack/update-codegen.sh

image: ; $(info Building image...)
	$(IMAGE_BUILDER) build -f $(DOCKERFILE) -t $(IMAGE_TAG) $(CURPATH) $(IMAGE_BUILD_OPTS)

# Run tests
test: generate vet manifests envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir=/tmp -p path)" HOME="$(shell pwd)" go test ./... -coverprofile cover.out -v

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
	cp ./config/crd/bases/* ./deployment/sriov-network-operator/crds/


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

mock-generate: gomock
	go generate ./...

CONTROLLER_GEN = $(BIN_DIR)/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.0)

KUSTOMIZE = $(BIN_DIR)/kustomize
kustomize: ## Download kustomize locally if necessary.
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v4@v4.5.5)

ENVTEST = $(BIN_DIR)/setup-envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

GOMOCK = $(shell pwd)/bin/mockgen
gomock:
	$(call go-get-tool,$(GOMOCK),github.com/golang/mock/mockgen@v1.6.0)

# go-install-tool will 'go install' any package $2 and install it to $1.
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(BIN_DIR) go install -mod=mod $(2) ;\
}
endef

skopeo:
	if ! which skopeo; then if [ -z ${SKIP_VAR_SET} ]; then if [ -f /etc/redhat-release ]; then dnf -y install skopeo; elif [ -f /etc/lsb-release ]; then sudo apt-get -y update; sudo apt-get -y install skopeo; fi; fi; fi

fakechroot:
	if ! which fakechroot; then if [ -f /etc/redhat-release ]; then dnf -y install fakechroot; elif [ -f /etc/lsb-release ]; then sudo apt-get -y update; sudo apt-get -y install fakechroot; fi; fi

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

test-e2e: generate vet manifests skopeo envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir=/tmp -p path)"; source hack/env.sh; HOME="$(shell pwd)" go test ./test/e2e/... -timeout 60m -coverprofile cover.out -v

test-e2e-k8s: export NAMESPACE=sriov-network-operator
test-e2e-k8s: test-e2e

test-bindata-scripts: fakechroot
	fakechroot ./test/scripts/enable-kargs_test.sh

test-%: generate vet manifests envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir=/tmp -p path)" HOME="$(shell pwd)" go test ./$*/... -coverprofile cover-$*.out -coverpkg ./... -v

# deploy-setup-k8s: export NAMESPACE=sriov-network-operator
# deploy-setup-k8s: export ENABLE_ADMISSION_CONTROLLER=false
# deploy-setup-k8s: export CNI_BIN_PATH=/opt/cni/bin
# test-e2e-k8s: test-e2e

GOCOVMERGE = $(BIN_DIR)/gocovmerge
gocovmerge: ## Download gocovmerge locally if necessary.
	$(call go-install-tool,$(GOCOVMERGE),github.com/shabbyrobe/gocovmerge/cmd/gocovmerge@latest)

GCOV2LCOV = $(BIN_DIR)/gcov2lcov
gcov2lcov:
	$(call go-install-tool,$(GCOV2LCOV),github.com/jandelgado/gcov2lcov@v1.0.5)

merge-test-coverage: gocovmerge gcov2lcov
	$(GOCOVMERGE) cover-*.out > cover.out
	$(GCOV2LCOV) -infile cover.out -outfile lcov.out

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

$(GOLANGCI_LINT): ; $(info installing golangci-lint...)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VER))

.PHONY: lint
lint: | $(GOLANGCI_LINT) ; $(info  running golangci-lint...) @ ## Run golangci-lint
	$(GOLANGCI_LINT) run --timeout=10m
