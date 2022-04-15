#!/bin/bash
set -x

declare -a kargs=( "$@" )
ret=0
args=$(chroot /host/ cat /proc/cmdline)

if chroot /host/ test -f /run/ostree-booted ; then
    for t in "${kargs[@]}";do
        if [[ $args != *${t}* ]];then
            if chroot /host/ rpm-ostree kargs | grep -vq ${t}; then
                chroot /host/ rpm-ostree kargs --append ${t} > /dev/null 2>&1
            fi
            let ret++
        fi
    done
else
    chroot /host/ which grubby > /dev/null 2>&1
    # if grubby is not there, let's tell it
    if [ $? -ne 0 ]; then
        exit 127
    fi
    for t in "${kargs[@]}";do
        if [[ $args != *${t}* ]];then
            if chroot /host/ grubby --info=DEFAULT | grep args | grep -vq ${t}; then
                chroot /host/ grubby --update-kernel=DEFAULT --args=${t} > /dev/null 2>&1
            fi
            let ret++
        fi
    done
fi

echo $ret
