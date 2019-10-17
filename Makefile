CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
KUBECONFIG?=$(HOME)/.kube/config
export OPERATOR_EXEC?=oc

GOLIST=go list
GOFMT=gofmt
BUILD_GOPATH=$(TARGET_DIR):$(TARGET_DIR)/vendor:$(CURPATH)/cmd
GOFMT_CHECK=$(shell find . -not \( \( -wholename './.*' -o -wholename '*/vendor/*' \) -prune \) -name '*.go' | sort -u | xargs gofmt -s -l)

IMAGE_BUILDER?=@docker
IMAGE_BUILD_OPTS?=
DOCKERFILE?=Dockerfile

export APP_NAME=sriov-network-operator
APP_REPO=github.com/openshift/$(APP_NAME)
TARGET=$(TARGET_DIR)/bin/$(APP_NAME)
IMAGE_TAG?=nfvpe/$(APP_NAME):latest
MAIN_PKG=cmd/manager/main.go
export NAMESPACE?=openshift-sriov-network-operator
export ENABLE_ADMISSION_CONTROLLER?=true
PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test|/examples')

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

.PHONY: all operator-sdk build clean fmt gendeepcopy test-unit test-e2e test-e2e-k8s run image

all: build #check install

operator-sdk:
	@if ! type -p operator-sdk ; \
	then if [ ! -d $(GOPATH)/src/github.com/operator-framework/operator-sdk ] ; \
	  then git clone https://github.com/operator-framework/operator-sdk --branch master $(GOPATH)/src/github.com/operator-framework/operator-sdk ; \
	  fi ; \
	  cd $(GOPATH)/src/github.com/operator-framework/operator-sdk ; \
	  make dep ; \
	  make install ; \
	fi

all: build plugins

build: _build-manager _build-sriov-network-config-daemon _build-webhook

_build-%:
	WHAT=$* hack/build-go.sh

run:
	hack/run-locally.sh

clean:
	@rm -rf $(TARGET_DIR)

image: ; $(info Building image...)
	$(IMAGE_BUILDER) build -f $(DOCKERFILE) -t $(IMAGE_TAG) $(CURPATH) $(IMAGE_BUILD_OPTS)

fmt: ; $(info  running gofmt...) @ ## Run gofmt on all source files
	@ret=0 && for d in $$($(GOLIST) -f '{{.Dir}}' ./... | grep -v /vendor/); do \
		$(GOFMT) -l -w $$d/*.go || ret=$$? ; \
	 done ; exit $$ret

gencode: operator-sdk
	@operator-sdk generate k8s
	@operator-sdk generate openapi

deploy-setup:
	@EXCLUSIONS=() hack/deploy-setup.sh $(NAMESPACE)

deploy-setup-k8s: export NAMESPACE=sriov-network-operator
deploy-setup-k8s: export ENABLE_ADMISSION_CONTROLLER=false
deploy-setup-k8s: export CNI_BIN_PATH=/opt/cni/bin
deploy-setup-k8s: deploy-setup

# test-unit:
# 	@go test -v $(PKGS)
test-e2e-local: operator-sdk
	@hack/run-e2e-test-locally.sh

test-e2e:
	@hack/run-e2e-test.sh

deploy-setup-k8s: export NAMESPACE=sriov-network-operator
deploy-setup-k8s: export ENABLE_ADMISSION_CONTROLLER=false
deploy-setup-k8s: export CNI_BIN_PATH=/opt/cni/bin
test-e2e-k8s: test-e2e

undeploy:
	@hack/undeploy.sh $(NAMESPACE)

undeploy-k8s: export NAMESPACE=sriov-network-operator
undeploy-k8s: undeploy

_plugin-%:
	@hack/build-plugins.sh $*

plugins: _plugin-intel _plugin-mellanox _plugin-generic

verify-gofmt:
ifeq (, $(GOFMT_CHECK))
	@echo "verify-gofmt: OK"
else
	@echo "verify-gofmt: ERROR: gofmt failed on the following files:"
	@echo "$(GOFMT_CHECK)"
	@echo ""
	@echo "For details, run: gofmt -d -s $(GOFMT_CHECK)"
	@echo ""
	@exit 1
endif

verify: verify-gofmt
