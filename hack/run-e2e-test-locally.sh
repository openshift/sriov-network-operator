#!/bin/bash

EXCLUSIONS=(operator.yaml) $(dirname $0)/deploy-setup.sh ${NAMESPACE}
source hack/env.sh
operator-sdk test local ./test/e2e --namespace ${NAMESPACE} --go-test-flags "-v" --up-local
# go test ./test/e2e/... -root=$(pwd) -kubeconfig=$KUBECONFIG -globalMan deploy/crds/sriovnetwork_v1_sriovnetwork_crd.yaml -localOperator -v -singleNamespace
hack/undeploy.sh sriov-network-operator
