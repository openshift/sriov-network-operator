#!/bin/bash

declare -x GOPATH="/home/pliu/go"
vendor/k8s.io/code-generator/generate-groups.sh all \
github.com/pliurh/sriov-network-operator/pkg/client \ github.com/pliurh/sriov-network-operator/pkg/apis \
sriovnetwork:v1