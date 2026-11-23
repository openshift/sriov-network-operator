#!/usr/bin/env bash
set -xeo pipefail

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

export ADMISSION_CONTROLLERS_ENABLED=true
export ADMISSION_CONTROLLERS_CERTIFICATES_CERT_MANAGER_ENABLED=true
export NAMESPACE="sriov-network-operator"
export OPERATOR_NAMESPACE="sriov-network-operator"

source hack/env.sh

HELM_VALUES_OPTS="\
  --set images.operator=${SRIOV_NETWORK_OPERATOR_IMAGE} \
  --set images.sriovConfigDaemon=${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE} \
  --set images.sriovCni=${SRIOV_CNI_IMAGE} \
  --set images.ovsCni=${OVS_CNI_IMAGE} \
  --set images.sriovDevicePlugin=${SRIOV_DEVICE_PLUGIN_IMAGE} \
  --set images.sriovDraDriver=${SRIOV_DRA_DRIVER_IMAGE} \
  --set images.resourcesInjector=${NETWORK_RESOURCES_INJECTOR_IMAGE} \
  --set images.webhook=${SRIOV_NETWORK_WEBHOOK_IMAGE} \
  --set operator.admissionControllers.enabled=${ADMISSION_CONTROLLERS_ENABLED} \
  --set operator.admissionControllers.certificates.certManager.enabled=${ADMISSION_CONTROLLERS_CERTIFICATES_CERT_MANAGER_ENABLED} \
  --set sriovOperatorConfig.deploy=true"

PATH=$PATH:${root}/bin
make helm
# upgrade --install works for both first deploy and redeploy
helm upgrade --install -n ${NAMESPACE} --create-namespace \
  $HELM_VALUES_OPTS \
  --wait sriov-network-operator ./deployment/sriov-network-operator-chart
