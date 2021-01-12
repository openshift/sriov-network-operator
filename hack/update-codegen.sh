#!/bin/bash
unset GOFLAGS
unset GO111MODULE

mkdir -p api/sriovnetwork
cd api/sriovnetwork
ln -s ../v1 ./v1
cd ../..

CODEGEN_PKG="./vendor/k8s.io/code-generator"

bash ${CODEGEN_PKG}/generate-groups.sh client,lister,informer \
      github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client \
      github.com/k8snetworkplumbingwg/sriov-network-operator/api \
      sriovnetwork:v1 \
      --go-header-file hack/boilerplate.go.txt
sed -i "s|github.com/k8snetworkplumbingwg/sriov-network-operator/api/sriovnetwork/v1|github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1|g" $(find pkg/client -type f)
rm -rf api/sriovnetwork
