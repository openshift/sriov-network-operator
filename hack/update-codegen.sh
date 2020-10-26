#!/bin/bash

chmod +x vendor/k8s.io/code-generator/generate-groups.sh
./vendor/k8s.io/code-generator/generate-groups.sh client,lister,informer \
      github.com/openshift/sriov-network-operator/pkg/client \
      github.com/openshift/sriov-network-operator/apis \
      sriovnetwork:v1 \
      --go-header-file hack/boilerplate.go.txt
chmod -x vendor/k8s.io/code-generator/generate-groups.sh
