if [ -z $SKIP_VAR_SET ]; then
        if ! skopeo -v &> /dev/null
        then
                echo "skopeo could not be found"
                exit 1
        fi
        CNI_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-cni | jq --raw-output '.Digest')
        export SRIOV_CNI_IMAGE=${SRIOV_CNI_IMAGE:-quay.io/openshift/origin-sriov-cni@${CNI_IMAGE_DIGEST}}
        INFINIBAND_CNI_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-infiniband-cni | jq --raw-output '.Digest')
        export SRIOV_INFINIBAND_CNI_IMAGE=${SRIOV_INFINIBAND_CNI_IMAGE:-quay.io/openshift/origin-sriov-infiniband-cni@${INFINIBAND_CNI_IMAGE_DIGEST}}
        DP_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-network-device-plugin | jq --raw-output '.Digest')
        export SRIOV_DEVICE_PLUGIN_IMAGE=${SRIOV_DEVICE_PLUGIN_IMAGE:-quay.io/openshift/origin-sriov-network-device-plugin@${DP_IMAGE_DIGEST}}
        INJECTOR_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-dp-admission-controller | jq --raw-output '.Digest')
        export NETWORK_RESOURCES_INJECTOR_IMAGE=${NETWORK_RESOURCES_INJECTOR_IMAGE:-quay.io/openshift/origin-sriov-dp-admission-controller@${INJECTOR_IMAGE_DIGEST}}
        DAEMON_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-network-config-daemon | jq --raw-output '.Digest')
        export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE:-quay.io/openshift/origin-sriov-network-config-daemon@${DAEMON_IMAGE_DIGEST}}
        WEBHOOK_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-network-webhook | jq --raw-output '.Digest')
        export SRIOV_NETWORK_WEBHOOK_IMAGE=${SRIOV_NETWORK_WEBHOOK_IMAGE:-quay.io/openshift/origin-sriov-network-webhook@${WEBHOOK_IMAGE_DIGEST}}
        OPERATOR_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-sriov-network-operator | jq --raw-output '.Digest')
        export SRIOV_NETWORK_OPERATOR_IMAGE=${SRIOV_NETWORK_OPERATOR_IMAGE:-quay.io/openshift/origin-sriov-network-operator@${OPERATOR_IMAGE_DIGEST}}
else
        [ -z $CNI_IMAGE_DIGEST ] && echo "CNI_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $INFINIBAND_CNI_IMAGE_DIGEST ] && echo "INFINIBAND_CNI_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $DP_IMAGE_DIGEST ] && echo "DP_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $INJECTOR_IMAGE_DIGEST ] && echo "INJECTOR_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $DAEMON_IMAGE_DIGEST ] && echo "DAEMON_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $WEBHOOK_IMAGE_DIGEST ] && echo "WEBHOOK_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
        [ -z $OPERATOR_IMAGE_DIGEST ] && echo "OPERATOR_IMAGE_DIGEST is empty but SKIP_VAR_SET is set" && exit 1
fi

export RELEASE_VERSION=4.7.0
export OPERATOR_NAME=sriov-network-operator
export RESOURCE_PREFIX=openshift.io
export ENABLE_ADMISSION_CONTROLLER=${ENABLE_ADMISSION_CONTROLLER:-"true"}
