#!/bin/bash
set -x
declare -a kargs=( "$@" )
eval `grubby --info=DEFAULT | grep args`
ret=0

for t in "${kargs[@]}";do
    if [[ $args != *${t}* ]];then
        chroot /host/ grubby --update-kernel=DEFAULT --args=${t}
        let ret++
    fi
done
echo $ret
