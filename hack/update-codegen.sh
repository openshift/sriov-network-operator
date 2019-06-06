#!/bin/bash

declare -x GOPATH="/home/pliu/go"
vendor/k8s.io/code-generator/generate-groups.sh all \
github.com/openshift/sriov-network-operator/pkg/client \ github.com/openshift/sriov-network-operator/pkg/apis \
sriovnetwork:v1