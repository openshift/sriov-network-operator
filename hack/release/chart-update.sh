#!/bin/bash
set -ex

# github tag e.g v1.2.3
GITHUB_TAG=${GITHUB_TAG:-}
# github api token (needed only for read access)
GITHUB_TOKEN=${GITHUB_TOKEN:-}
# github repo owner e.g k8snetworkplumbingwg
GITHUB_REPO_OWNER=${GITHUB_REPO_OWNER:-}

BASE=${PWD}
YQ_CMD="${BASE}/bin/yq"
HELM_VALUES=${BASE}/deployment/sriov-network-operator-chart/values.yaml
HELM_CHART=${BASE}/deployment/sriov-network-operator-chart/Chart.yaml


if [ -z "$GITHUB_TAG" ]; then
    echo "ERROR: GITHUB_TAG must be provided as env var"
    exit 1
fi

if [ -z "$GITHUB_TOKEN" ]; then
    echo "ERROR: GITHUB_TOKEN must be provided as env var"
    exit 1
fi

if [ -z "$GITHUB_REPO_OWNER" ]; then
    echo "ERROR: GITHUB_REPO_OWNER must be provided as env var"
    exit 1
fi

get_latest_github_tag() {
    local owner="$1"
    local repo="$2"
    local latest_tag

    # Fetch the latest tags using GitHub API and extract the latest tag name
    latest_tag=$(curl -s "https://api.github.com/repos/$owner/$repo/tags" --header "Authorization: Bearer ${GITHUB_TOKEN}" | jq -r '.[0].name')

    echo "$latest_tag"
}

# tag provided via env var
OPERATOR_TAG=${GITHUB_TAG}
IB_SRIOV_CNI_TAG=$(get_latest_github_tag k8snetworkplumbingwg ib-sriov-cni)
SRIOV_CNI_TAG=$(get_latest_github_tag k8snetworkplumbingwg sriov-cni)
OVS_CNI_TAG=$(get_latest_github_tag k8snetworkplumbingwg ovs-cni)
NETWORK_RESOURCE_INJECTOR_TAG=$(get_latest_github_tag k8snetworkplumbingwg network-resources-injector)
SRIOV_DEVICE_PLUGIN_TAG=$(get_latest_github_tag k8snetworkplumbingwg sriov-network-device-plugin)

# patch values.yaml in-place

# sriov-network-operator images:
OPERATOR_REPO=${GITHUB_REPO_OWNER} # this is used to allow to release sriov-network-operator from forks
$YQ_CMD -i ".images.operator = \"ghcr.io/${OPERATOR_REPO}/sriov-network-operator:${OPERATOR_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.sriovConfigDaemon = \"ghcr.io/${OPERATOR_REPO}/sriov-network-operator-config-daemon:${OPERATOR_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.webhook = \"ghcr.io/${OPERATOR_REPO}/sriov-network-operator-webhook:${OPERATOR_TAG}\"" ${HELM_VALUES}

# other images that sriov-network-operator uses:
$YQ_CMD -i ".images.sriovCni = \"ghcr.io/k8snetworkplumbingwg/sriov-cni:${SRIOV_CNI_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.ibSriovCni = \"ghcr.io/k8snetworkplumbingwg/ib-sriov-cni:${IB_SRIOV_CNI_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.ovsCni = \"ghcr.io/k8snetworkplumbingwg/ovs-cni-plugin:${OVS_CNI_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.sriovDevicePlugin = \"ghcr.io/k8snetworkplumbingwg/sriov-network-device-plugin:${SRIOV_DEVICE_PLUGIN_TAG}\"" ${HELM_VALUES}
$YQ_CMD -i ".images.resourcesInjector = \"ghcr.io/k8snetworkplumbingwg/network-resources-injector:${NETWORK_RESOURCE_INJECTOR_TAG}\"" ${HELM_VALUES}

# patch Chart.yaml in-place
$YQ_CMD -i ".version = \"${OPERATOR_TAG#"v"}\"" ${HELM_CHART}
$YQ_CMD -i ".appVersion = \"${OPERATOR_TAG}\"" ${HELM_CHART}
