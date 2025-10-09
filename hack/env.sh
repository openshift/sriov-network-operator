#!/bin/bash

_url_detect() {
    local varname="$1"
    local url="$2"
    local digest
    local value=

    if [ "$EMPTY_IS_VALID" = 1 ] ; then
        [ -n "${!varname+set}" ] && return 0
    else
        [ -n "${!varname}" ] && return 0
    fi

    if [ -n "$url" ] ; then
        digest="$(skopeo inspect "docker://$url" | jq --raw-output '.Digest')" || :
        if [ -z "$digest" ] ; then
            echo "Failure to detect \$$varname at $url. Set the variable"
            return 1
        fi
        value="$url@$digest"
    fi

    export "$varname=$value"
}

if [ -z $SKIP_VAR_SET ]; then
        if ! skopeo -v &> /dev/null
        then
                echo "skopeo could not be found"
                exit 1
        fi

        _url_detect "SRIOV_CNI_IMAGE" "quay.io/openshift/origin-sriov-cni"
        _url_detect "SRIOV_INFINIBAND_CNI_IMAGE" "quay.io/openshift/origin-sriov-infiniband-cni"
        EMPTY_IS_VALID=1 _url_detect "OVS_CNI_IMAGE" "quay.io/kubevirt/ovs-cni-plugin"
        EMPTY_IS_VALID=1 _url_detect "RDMA_CNI_IMAGE" "quay.io/openshift/origin-rdma-cni"
        _url_detect "SRIOV_DEVICE_PLUGIN_IMAGE" "quay.io/openshift/origin-sriov-network-device-plugin"
        _url_detect "NETWORK_RESOURCES_INJECTOR_IMAGE" "quay.io/openshift/origin-sriov-dp-admission-controller"
        _url_detect "SRIOV_NETWORK_CONFIG_DAEMON_IMAGE" "quay.io/openshift/origin-sriov-network-config-daemon"
        _url_detect "SRIOV_NETWORK_WEBHOOK_IMAGE" "quay.io/openshift/origin-sriov-network-webhook"
        _url_detect "METRICS_EXPORTER_IMAGE" "quay.io/openshift/origin-sriov-network-metrics-exporter"
        _url_detect "SRIOV_NETWORK_OPERATOR_IMAGE" "quay.io/openshift/origin-sriov-network-operator"
        EMPTY_IS_VALID=1 _url_detect "METRICS_EXPORTER_KUBE_RBAC_PROXY_IMAGE" "quay.io/openshift/origin-kube-rbac-proxy"

        fail_msg_detect="is empty and failed to detect"
else
        fail_msg_detect="is empty but SKIP_VAR_SET is set"
fi

# ensure that OVS_CNI_IMAGE is set, empty string is a valid value
OVS_CNI_IMAGE=${OVS_CNI_IMAGE:-}
# ensure that RDMA_CNI_IMAGE is set, empty string is a valid value
RDMA_CNI_IMAGE=${RDMA_CNI_IMAGE:-}
METRICS_EXPORTER_KUBE_RBAC_PROXY_IMAGE=${METRICS_EXPORTER_KUBE_RBAC_PROXY_IMAGE:-}
[ -z $SRIOV_CNI_IMAGE ] && echo "SRIOV_CNI_IMAGE $fail_msg_detect" && exit 1
[ -z $SRIOV_INFINIBAND_CNI_IMAGE ] && echo "SRIOV_INFINIBAND_CNI_IMAGE $fail_msg_detect" && exit 1
[ -z $SRIOV_DEVICE_PLUGIN_IMAGE ] && echo "SRIOV_DEVICE_PLUGIN_IMAGE $fail_msg_detect" && exit 1
[ -z $NETWORK_RESOURCES_INJECTOR_IMAGE ] && echo "NETWORK_RESOURCES_INJECTOR_IMAGE $fail_msg_detect" && exit 1
[ -z $SRIOV_NETWORK_CONFIG_DAEMON_IMAGE ] && echo "SRIOV_NETWORK_CONFIG_DAEMON_IMAGE $fail_msg_detect" && exit 1
[ -z $SRIOV_NETWORK_WEBHOOK_IMAGE ] && echo "SRIOV_NETWORK_WEBHOOK_IMAGE $fail_msg_detect" && exit 1
[ -z $METRICS_EXPORTER_IMAGE ] && echo "METRICS_EXPORTER_IMAGE $fail_msg_detect" && exit 1
[ -z $SRIOV_NETWORK_OPERATOR_IMAGE ] && echo "SRIOV_NETWORK_OPERATOR_IMAGE $fail_msg_detect" && exit 1

unset fail_msg_detect

set -x

export RELEASE_VERSION=4.7.0
export OPERATOR_NAME=sriov-network-operator
export RESOURCE_PREFIX=${RESOURCE_PREFIX:-openshift.io}
export ADMISSION_CONTROLLERS_ENABLED=${ADMISSION_CONTROLLERS_ENABLED:-"true"}
export CLUSTER_TYPE=${CLUSTER_TYPE:-openshift}
export NAMESPACE=${NAMESPACE:-"openshift-sriov-network-operator"}
export ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_SECRET_NAME=${ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_SECRET_NAME:-"operator-webhook-cert"}
export ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_SECRET_NAME=${ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_SECRET_NAME:-"network-resources-injector-cert"}
export ADMISSION_CONTROLLERS_CERTIFICATES_CERT_MANAGER_ENABLED=${ADMISSION_CONTROLLERS_CERTIFICATES_CERT_MANAGER_ENABLED:-"false"}
export ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_CA_CRT=${ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_CA_CRT:-""}
export ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_CA_CRT=${ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_CA_CRT:-""}
export DEV_MODE=${DEV_MODE:-"FALSE"}
export METRICS_EXPORTER_SECRET_NAME=${METRICS_EXPORTER_SECRET_NAME:-"metrics-exporter-cert"}
export METRICS_EXPORTER_PORT=${METRICS_EXPORTER_PORT:-"9110"}
export OPERATOR_WEBHOOK_NETWORK_POLICY_PORT=${OPERATOR_WEBHOOK_NETWORK_POLICY_PORT:-"6443"}
export INJECTOR_WEBHOOK_NETWORK_POLICY_PORT=${INJECTOR_WEBHOOK_NETWORK_POLICY_PORT:-"6443"}
