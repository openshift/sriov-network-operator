#!/bin/bash
set -euxo pipefail

NAMESPACE=${NAMESPACE:-openshift-sriov-network-operator}
DIR=$PWD
GOFLAGS=${GOFLAGS:-}

export TEST_NAMESPACE=${NAMESPACE}
export KUBECONFIG=${KUBECONFIG:-/root/dev-scripts/ocp/sriov/auth/kubeconfig}


cd $DIR
# GO111MODULE=on go test ./test/operator/...  -root=$OPERATOR_ROOT -kubeconfig=$KUBECONFIG -globalMan $OPERATOR_ROOT/deploy/crds/sriovnetwork.openshift.io_sriovnetworks_crd.yaml -namespacedMan $OPERATOR_ROOT/deploy/operator-init.yaml -v -singleNamespace true
ginkgo -v --progress ./test/$1 -- -root=$DIR -kubeconfig=$KUBECONFIG -globalMan $DIR/hack/dummy.yaml -namespacedMan $DIR/hack/dummy.yaml -singleNamespace true
