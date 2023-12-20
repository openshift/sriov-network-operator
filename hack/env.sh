if [ -z $SKIP_VAR_SET ]; then
        export SRIOV_CNI_IMAGE=${SRIOV_CNI_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-cni}
        export SRIOV_INFINIBAND_CNI_IMAGE=${SRIOV_INFINIBAND_CNI_IMAGE:-ghcr.io/k8snetworkplumbingwg/ib-sriov-cni}
        export SRIOV_DEVICE_PLUGIN_IMAGE=${SRIOV_DEVICE_PLUGIN_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-device-plugin}
        export NETWORK_RESOURCES_INJECTOR_IMAGE=${NETWORK_RESOURCES_INJECTOR_IMAGE:-ghcr.io/k8snetworkplumbingwg/network-resources-injector}
        export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator-config-daemon}
        export SRIOV_NETWORK_WEBHOOK_IMAGE=${SRIOV_NETWORK_WEBHOOK_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator-webhook}
        export SRIOV_NETWORK_OPERATOR_IMAGE=${SRIOV_NETWORK_OPERATOR_IMAGE:-ghcr.io/k8snetworkplumbingwg/sriov-network-operator}
else
        [ -z $SRIOV_CNI_IMAGE ] && echo "SRIOV_CNI_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_INFINIBAND_CNI_IMAGE ] && echo "SRIOV_INFINIBAND_CNI_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_DEVICE_PLUGIN_IMAGE ] && echo "SRIOV_DEVICE_PLUGIN_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $NETWORK_RESOURCES_INJECTOR_IMAGE ] && echo "NETWORK_RESOURCES_INJECTOR_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_CONFIG_DAEMON_IMAGE ] && echo "SRIOV_NETWORK_CONFIG_DAEMON_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_WEBHOOK_IMAGE ] && echo "SRIOV_NETWORK_WEBHOOK_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $SRIOV_NETWORK_OPERATOR_IMAGE ] && echo "SRIOV_NETWORK_OPERATOR_IMAGE is empty but SKIP_VAR_SET is set" && exit 1
fi

export RELEASE_VERSION=4.7.0
export OPERATOR_NAME=sriov-network-operator
export RESOURCE_PREFIX=${RESOURCE_PREFIX:-openshift.io}
export ADMISSION_CONTROLLERS__ENABLED=${ADMISSION_CONTROLLERS__ENABLED:-"true"}
export CLUSTER_TYPE=${CLUSTER_TYPE:-openshift}
export NAMESPACE=${NAMESPACE:-"openshift-sriov-network-operator"}
export WEBHOOK_CA_BUNDLE=${WEBHOOK_CA_BUNDLE:-""}
export DEV_MODE=${DEV_MODE:-"FALSE"}
