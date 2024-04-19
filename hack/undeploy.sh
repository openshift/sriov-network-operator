#!/bin/bash

set -eo pipefail

source "$(dirname $0)/common"

repo_dir="$(dirname $0)/.."
namespace=${1:-}
if [ -n "${namespace}" ] ; then
  namespace="-n ${namespace}"
fi

pushd ${repo_dir}/deploy
files="operator.yaml sriovoperatorconfig.yaml service_account.yaml role.yaml role_binding.yaml clusterrole.yaml clusterrolebinding.yaml configmap.yaml"
for file in ${files}; do
  envsubst< ${file} | ${OPERATOR_EXEC} delete --ignore-not-found ${namespace} -f -
done
${OPERATOR_EXEC} delete cm --ignore-not-found ${namespace} device-plugin-config
${OPERATOR_EXEC} delete MutatingWebhookConfiguration --ignore-not-found ${namespace} network-resources-injector-config sriov-operator-webhook-config
${OPERATOR_EXEC} delete ValidatingWebhookConfiguration --ignore-not-found ${namespace} sriov-operator-webhook-config
popd
