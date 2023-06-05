#!/bin/bash
# This script can be used by any actor to switch the Bluefield
# to NIC or DPU mode. An example to use it is by overriding
# the entrypoint in the sriov-network-config-daemon container.
#
# podman run --pull always --replace --rm --name bf-switch-mode \
# --privileged --volume /dev:/dev --entrypoint \
# /bindata/scripts/bf2-switch-mode.sh http://url/of/image && \
# systemctl reboot

set -e

mode=${1:-query}
devices=${2:-"$(mstconfig q | grep "Device:" | awk 'BEGIN {FS="/";} { print $6 }')"}

for device in $devices; do
    echo "Found device: $device"
    type=$(mstconfig -d $device q | grep "Device type:" | awk '{ print $3}')

    if [ "${type}" != "BlueField2" ]; then
        echo "Device $device is not a Bluefield2"
        devices=${devices/$device/}
    fi
done

if [ -z "$devices" ]; then
    echo "Can't find bluefield device."
    exit 120
else
    echo Configuring bluefield devices: "${devices}"
fi

# In the event no reboot is required, return 120. If reboot is required, return 0
ret=120

fwreset () {
    mstfwreset -y -d "${device}" reset
    echo "Switched to ${mode} mode."
    ret=0
}

query () {
    current_config=$(mstconfig -d "${device}" q \
        INTERNAL_CPU_MODEL \
        INTERNAL_CPU_PAGE_SUPPLIER \
        INTERNAL_CPU_ESWITCH_MANAGER \
        INTERNAL_CPU_IB_VPORT0 \
        INTERNAL_CPU_OFFLOAD_ENGINE | tail -n 5 | awk '{print $2}' | xargs echo)
    echo "Current DPU configuration: ${current_config}"
}

for device in $devices; do

    query
    case "${mode}" in
        dpu)
            if [ "${current_config}" == "EMBEDDED_CPU(1) ECPF(0) ECPF(0) ECPF(0) ENABLED(0)" ]; then
                echo "${device} Already in DPU mode."
            else
                echo "Switching ${device} to DPU mode."
                mstconfig -y -d "${device}" s \
                    INTERNAL_CPU_MODEL=EMBEDDED_CPU \
                    INTERNAL_CPU_PAGE_SUPPLIER=ECPF \
                    INTERNAL_CPU_ESWITCH_MANAGER=ECPF \
                    INTERNAL_CPU_IB_VPORT0=ECPF \
                    INTERNAL_CPU_OFFLOAD_ENGINE=ENABLED
                fwreset
            fi
            ;;
        nic)
            if [ "${current_config}" == "EMBEDDED_CPU(1) EXT_HOST_PF(1) EXT_HOST_PF(1) EXT_HOST_PF(1) DISABLED(1)" ]; then
                echo "${device} Already in NIC mode."
            else
                echo "Switching ${device} to NIC mode."
                mstconfig -y -d "${device}" s \
                    INTERNAL_CPU_MODEL=EMBEDDED_CPU \
                    INTERNAL_CPU_PAGE_SUPPLIER=EXT_HOST_PF \
                    INTERNAL_CPU_ESWITCH_MANAGER=EXT_HOST_PF \
                    INTERNAL_CPU_IB_VPORT0=EXT_HOST_PF \
                    INTERNAL_CPU_OFFLOAD_ENGINE=DISABLED
                fwreset
            fi
            ;;
        *)
            echo "Invalid mode '${mode}', expecting 'nic' or 'dpu'"
            ;;
    esac

done

exit $ret

