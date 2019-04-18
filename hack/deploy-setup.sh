#!/bin/bash
# This script inits a cluster to allow node-network-operator
# to deploy.  It assumes it is capable of login as a
# user who has the cluster-admin role

set -euxo pipefail

source "$(dirname $0)/common"

load_manifest() {
  local repo=$1
  local namespace=${2:-}
  if [ -n "${namespace}" ] ; then
    namespace="-n ${namespace}"
  fi

  pushd ${repo}/deploy
    if ! oc get project node-network-operator > /dev/null 2>&1 && test -f namespace.yaml ; then
      oc apply -f namespace.yaml
    fi
    files="service_account.yaml role.yaml role_binding.yaml operator.yaml crds/nodenetwork_v1alpha1_nodenetworkconfigurationpolicy_crd.yaml"
    for m in ${files}; do
      if [ "$(echo ${EXCLUSIONS[@]} | grep -o ${m} | wc -w)" == "0" ] ; then
        oc apply -f ${m} ${namespace:-}
      fi
    done
  popd
}

# This is required for when running the operator locally using go run
rm -rf /tmp/_working_dir
mkdir /tmp/_working_dir

load_manifest ${repo_dir}
