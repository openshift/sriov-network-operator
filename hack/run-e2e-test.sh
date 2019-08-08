#!/bin/bash

EXCLUSIONS=(operator.yaml) $(dirname $0)/deploy-setup.sh sriov-network-operator

export TEST_NAMESPACE=sriov-network-operator
KUBECONFIG=${KUBECONFIG:-/root/env/ign/auth/kubeconfig}
IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-network-config-daemon | jq --raw-output '.Digest')
SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE:-quay.io/openshift/origin-sriov-network-config-daemon@${IMAGE_DIGEST}}
SRIOV_NETWORK_OPERATOR_IMAGE=${SRIOV_NETWORK_OPERATOR_IMAGE:-quay.io/openshift/origin-sriov-network-operator}

echo ${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}
envsubst < operator.yaml deploy/operator.yaml | sed "s|quay.io/openshift/origin-sriov-network-config-daemon|${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}|g;s|quay.io/openshift/origin-sriov-network-operator|${SRIOV_NETWORK_OPERATOR_IMAGE}|g;s|IfNotPresent|Always|g"  > deploy/operator-init.yaml
go test ./test/e2e/... -root=$(pwd) -kubeconfig=$KUBECONFIG -globalMan deploy/crds/sriovnetwork_v1_sriovnetwork_crd.yaml -namespacedMan deploy/operator-init.yaml -v -singleNamespace true
