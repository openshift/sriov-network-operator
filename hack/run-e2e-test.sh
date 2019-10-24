#!/bin/bash
source hack/env.sh
EXCLUSIONS=(operator.yaml) $(dirname $0)/deploy-setup.sh ${NAMESPACE}

export TEST_NAMESPACE=${NAMESPACE}
KUBECONFIG=${KUBECONFIG:-/root/env/ign/auth/kubeconfig}
SRIOV_NETWORK_OPERATOR_IMAGE=${SRIOV_NETWORK_OPERATOR_IMAGE:-quay.io/openshift/origin-sriov-network-operator}

echo ${SRIOV_CNI_IMAGE}
echo ${SRIOV_DEVICE_PLUGIN_IMAGE}
echo ${NETWORK_RESOURCES_INJECTOR_IMAGE}
echo ${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}
envsubst < deploy/operator.yaml | sed "s|quay.io/openshift/origin-sriov-network-operator|${SRIOV_NETWORK_OPERATOR_IMAGE}|g;s|IfNotPresent|Always|g"  > deploy/operator-init.yaml
go test ./test/e2e/... -root=$(pwd) -kubeconfig=$KUBECONFIG -globalMan deploy/crds/sriovnetwork.openshift.io_sriovnetworks_crd.yaml -namespacedMan deploy/operator-init.yaml -v -singleNamespace true
