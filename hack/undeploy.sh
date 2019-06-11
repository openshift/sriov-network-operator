#!/bin/bash
#set -euxo pipefail

repo_dir="$(dirname $0)/.."
namespace=${1:-}
if [ -n "${namespace}" ] ; then
  namespace="-n ${namespace}"
fi

pushd ${repo_dir}/deploy
files="operator.yaml service_account.yaml role.yaml role_binding.yaml clusterrole.yaml clusterrolebinding.yaml crds/sriovnetwork_v1_sriovnetwork_crd.yaml crds/sriovnetwork_v1_sriovnetworknodepolicy_crd.yaml crds/sriovnetwork_v1_sriovnetworknodestate_crd.yaml"
for file in ${files}; do
  oc delete -f $file --ignore-not-found ${namespace}
done
oc delete cm --ignore-not-found ${namespace} device-plugin-config
popd