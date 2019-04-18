CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/_output
KUBECONFIG?=$(HOME)/.kube/config

GOBUILD=go build
BUILD_GOPATH=$(TARGET_DIR):$(TARGET_DIR)/vendor:$(CURPATH)/cmd

IMAGE_BUILDER_OPTS=
IMAGE_BUILDER?=imagebuilder
IMAGE_BUILD=$(IMAGE_BUILDER)
export IMAGE_TAG_CMD?=docker tag

export APP_NAME=sriov-network-operator
APP_REPO=github.com/pliurh/$(APP_NAME)
TARGET=$(TARGET_DIR)/bin/$(APP_NAME)
export IMAGE_TAG=quay.io/pliurh/$(APP_NAME):latest
MAIN_PKG=cmd/manager/main.go
export NAMESPACE?=sriov-network-operator
PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test|/examples')

OC?=oc

# These will be provided to the target
#VERSION := 1.0.0
#BUILD := `git rev-parse HEAD`

# Use linker flags to provide version/build settings to the target
#LDFLAGS=-ldflags "-X=main.Version=$(VERSION) -X=main.Build=$(BUILD)"

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

#.PHONY: all build clean install uninstall fmt simplify check run
.PHONY: all operator-sdk imagebuilder build clean fmt simplify gendeepcopy deploy-setup deploy-image deploy deploy-example test-unit test-e2e undeploy run

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

imagebuilder:
	@if [ $${USE_IMAGE_STREAM:-false} = false ] && ! type -p imagebuilder ; \
	then go get -u github.com/openshift/imagebuilder/cmd/imagebuilder ; \
	fi

build: fmt
	@mkdir -p $(TARGET_DIR)/src/$(APP_REPO)
	@cp -ru $(CURPATH)/pkg $(TARGET_DIR)/src/$(APP_REPO)
	@cp -ru $(CURPATH)/vendor/* $(TARGET_DIR)/src
	@GOPATH=$(BUILD_GOPATH) $(GOBUILD) $(LDFLAGS) -o $(TARGET) $(MAIN_PKG)

run:
	@operator-sdk up local --kubeconfig=$(KUBECONFIG)

clean:
	@rm -rf $(TARGET_DIR)

image: imagebuilder
	@if [ $${USE_IMAGE_STREAM:-false} = false ] && [ $${SKIP_BUILD:-false} = false ] ; \
	then $(IMAGE_BUILDER) -t $(IMAGE_TAG) . $(IMAGE_BUILDER_OPTS) ; \
	fi

fmt:
	@gofmt -l -w cmd && \
	gofmt -l -w pkg && \
	gofmt -l -w test

# simplify:
# 	@gofmt -s -l -w $(SRC)

gendeepcopy: operator-sdk
	@operator-sdk generate k8s

deploy-setup:
	hack/deploy-setup.sh

# deploy-image: image
# 	hack/deploy-image.sh

# deploy: deploy-setup deploy-image
# 	hack/deploy.sh

# deploy-no-build: deploy-setup
# 	NO_BUILD=true hack/deploy.sh

# deploy-example: deploy
# 	oc create -n $(NAMESPACE) -f hack/cr.yaml

test-unit:
	@go test -v $(PKGS)
test-e2e: operator-sdk
	@operator-sdk test local ./test/e2e

# deploy-example-no-build: deploy-no-build
# 	oc create -n $(NAMESPACE) -f hack/cr.yaml

undeploy:
	hack/undeploy.sh
