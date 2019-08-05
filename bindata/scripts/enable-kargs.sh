#!/bin/bash
set -x
REDHAT_RELEASE_FILE="/host/etc/redhat-release"
declare -a kargs=( "$@" )
ret=0
if grep --quiet CoreOS "$REDHAT_RELEASE_FILE"; then
    args=$(chroot /host/ rpm-ostree kargs)
    for t in "${kargs[@]}";do
        if [[ $args != *${t}* ]];then
            chroot /host/ rpm-ostree kargs --append ${t}
            let ret++
        fi
    done
else
    eval `chroot /host/ grubby --info=DEFAULT | grep args`
    for t in "${kargs[@]}";do
        if [[ $args != *${t}* ]];then
            chroot /host/ grubby --update-kernel=DEFAULT --args=${t}
            let ret++
        fi
    done
fi
echo $ret
