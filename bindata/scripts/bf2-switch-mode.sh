#!/bin/bash
# This script can be used by any actor to switch the Bluefield
# to NIC or DPU mode. An example to use it is by overriding
# the entrypoint in the sriov-network-config-daemon container.
#
# podman run --pull always --replace --rm --name bf-switch-mode \
# --privileged --volume /dev:/dev --entrypoint \
# /bindata/scripts/bf2-switch-mode.sh http://url/of/image && \
# systemctl reboot
#
# Returns 0   if no reboot is necessary however devices may not be found.
# Returns 120 if reboot is necessary after applying firmware changes.

set -o pipefail

mode=${1:-query}
device=${2:-"$(mstconfig q | grep "Device:" | awk 'BEGIN {FS="/";} { print $6 }')"}
if [ $? -ne 0 ]; then
    echo "Failed to query for device."
    exit 0
fi

if [ -z "$device" ]; then
    echo "Can't find device."
    exit 0
else
    echo Found device: "${device}"
fi

type=$(mstconfig -d $device q | grep "Device type:" | awk '{ print $3}')
if [ $? -ne 0 ]; then
    echo "Failed to query ${device} for device type."
    exit 0
fi

if [ "${type}" != "BlueField2" ]; then
    echo "Device is not a Bluefield2"
    exit 0
fi

fwreset () {
    mstfwreset -y -d "${device}" reset
    if [ $? -ne 0 ]; then
        echo "Failed to run mstfwreset for ${device}."
    fi
    echo "Switched to ${mode} mode."
    exit 120
}

query () {
    current_config=$(mstconfig -d "${device}" q \
        INTERNAL_CPU_MODEL \
        INTERNAL_CPU_PAGE_SUPPLIER \
        INTERNAL_CPU_ESWITCH_MANAGER \
        INTERNAL_CPU_IB_VPORT0 \
        INTERNAL_CPU_OFFLOAD_ENGINE | tail -n 5 | awk '{print $2}' | xargs echo)
    if [ $? -ne 0 ]; then
        echo "Failed to get the current config for ${device}."
        exit 0
    fi
    echo "Current DPU configuration: ${current_config}"
}

query
case "${mode}" in
    dpu)
        if [ "${current_config}" == "EMBEDDED_CPU(1) ECPF(0) ECPF(0) ECPF(0) ENABLED(0)" ]; then
            echo "Already in DPU mode."
        else
            echo "Switching to DPU mode."
            mstconfig -y -d "${device}" s \
                INTERNAL_CPU_MODEL=EMBEDDED_CPU \
                INTERNAL_CPU_PAGE_SUPPLIER=ECPF \
                INTERNAL_CPU_ESWITCH_MANAGER=ECPF \
                INTERNAL_CPU_IB_VPORT0=ECPF \
                INTERNAL_CPU_OFFLOAD_ENGINE=ENABLED
            if [ $? -ne 0 ]; then
                echo "Failed to config for DPU mode on ${device}."
            fi
            fwreset
        fi
        ;;
    nic)
        if [ "${current_config}" == "EMBEDDED_CPU(1) EXT_HOST_PF(1) EXT_HOST_PF(1) EXT_HOST_PF(1) DISABLED(1)" ]; then
            echo "Already in NIC mode."
        else
            echo "Switching to NIC mode."
            mstconfig -y -d "${device}" s \
                INTERNAL_CPU_MODEL=EMBEDDED_CPU \
                INTERNAL_CPU_PAGE_SUPPLIER=EXT_HOST_PF \
                INTERNAL_CPU_ESWITCH_MANAGER=EXT_HOST_PF \
                INTERNAL_CPU_IB_VPORT0=EXT_HOST_PF \
                INTERNAL_CPU_OFFLOAD_ENGINE=DISABLED
            if [ $? -ne 0 ]; then
                echo "Failed to config for NIC mode on ${device}."
            fi
            fwreset
        fi
        ;;
    *)
        echo "Invalid mode '${mode}', expecting 'nic' or 'dpu'"
        ;;
esac

exit 0

