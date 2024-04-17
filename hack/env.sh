if [ -z $SKIP_VAR_SET ]; then
        export SRIOV_CNI_IMAGE=${SRIOV_CNI_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-cni}
        export SRIOV_INFINIBAND_CNI_IMAGE=${SRIOV_INFINIBAND_CNI_IMAGE:-ghcr.io/k8snetworkplumbingwg/ib-sriov-cni}
        # OVS_CNI_IMAGE can be explicitly set to empty value, use default only if the var is not set
        export OVS_CNI_IMAGE=${OVS_CNI_IMAGE-quay.io/kubevirt/ovs-cni-plugin}
        export SRIOV_DEVICE_PLUGIN_IMAGE=${SRIOV_DEVICE_PLUGIN_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-device-plugin}
        export NETWORK_RESOURCES_INJECTOR_IMAGE=${NETWORK_RESOURCES_INJECTOR_IMAGE:-ghcr.io/k8snetworkplumbingwg/network-resources-injector}
        export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator-config-daemon}
        export SRIOV_NETWORK_WEBHOOK_IMAGE=${SRIOV_NETWORK_WEBHOOK_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator-webhook}
        export SRIOV_NETWORK_OPERATOR_IMAGE=${SRIOV_NETWORK_OPERATOR_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator}
else
        # ensure that OVS_CNI_IMAGE is set, empty string is a valid value
        OVS_CNI_IMAGE=${OVS_CNI_IMAGE:-}
        [ -z $SRIOV_CNI_IMAGE ] && echo "SRIOV_CNI_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_INFINIBAND_CNI_IMAGE ] && echo "SRIOV_INFINIBAND_CNI_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_DEVICE_PLUGIN_IMAGE ] && echo "SRIOV_DEVICE_PLUGIN_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $NETWORK_RESOURCES_INJECTOR_IMAGE ] && echo "NETWORK_RESOURCES_INJECTOR_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_CONFIG_DAEMON_IMAGE ] && echo "SRIOV_NETWORK_CONFIG_DAEMON_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_WEBHOOK_IMAGE ] && echo "SRIOV_NETWORK_WEBHOOK_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_OPERATOR_IMAGE ] && echo "SRIOV_NETWORK_OPERATOR_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
fi

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
