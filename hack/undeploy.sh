#!/bin/bash
#set -euxo pipefail

source "$(dirname $0)/common"

repo_dir="$(dirname $0)/.."
namespace=${1:-}
if [ -n "${namespace}" ] ; then
  namespace="-n ${namespace}"
fi

pushd ${repo_dir}/deploy
files="operator.yaml service_account.yaml role.yaml role_binding.yaml clusterrole.yaml clusterrolebinding.yaml crds/sriovnetwork.openshift.io_sriovnetworknodepolicies_crd.yaml crds/sriovnetwork.openshift.io_sriovnetworknodestates_crd.yaml crds/sriovnetwork.openshift.io_sriovoperatorconfigs_crd.yaml service_account.yaml role.yaml role_binding.yaml clusterrole.yaml clusterrolebinding.yaml operator.yaml"
for file in ${files}; do
  envsubst< ${file} | ${OPERATOR_EXEC} delete --ignore-not-found ${namespace} -f -
done
${OPERATOR_EXEC} delete cm --ignore-not-found ${namespace} device-plugin-config
popd
