#!/bin/bash
# This script inits a cluster to allow sriov-network-operator
# to deploy.  It assumes it is capable of login as a
# user who has the cluster-admin role

# set -euxo pipefail

source "$(dirname $0)/common"

load_manifest() {
  local repo=$1
  local namespace=${2:-}
  if [ -n "${namespace}" ] ; then
    namespace="-n ${namespace}"
  fi

  pushd ${repo}/deploy
    if ! oc get ns sriov-network-operator > /dev/null 2>&1 && test -f namespace.yaml ; then
      oc apply -f namespace.yaml
    fi
    files="service_account.yaml role.yaml role_binding.yaml clusterrole.yaml clusterrolebinding.yaml crds/sriovnetwork_v1_sriovnetwork_crd.yaml crds/sriovnetwork_v1_sriovnetworknodepolicy_crd.yaml crds/sriovnetwork_v1_sriovnetworknodestate_crd.yaml operator.yaml"
    for m in ${files}; do
      if [ "$(echo ${EXCLUSIONS[@]} | grep -o ${m} | wc -w | xargs)" == "0" ] ; then
        oc apply -f ${m} ${namespace:-}
      fi
    done
  popd
}

# This is required for when running the operator locally using go run
rm -rf /tmp/_working_dir
mkdir /tmp/_working_dir

load_manifest ${repo_dir} $1
