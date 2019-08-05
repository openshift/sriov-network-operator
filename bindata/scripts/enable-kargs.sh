#!/bin/bash
set -x

chroot /host/ which grubby

# if grubby is not there, let's send a message
if [ $? -ne 0 ]; then
    exit 127
fi

declare -a kargs=( "$@" )
eval `chroot /host/ grubby --info=DEFAULT | grep args`
ret=0

for t in "${kargs[@]}";do
    if [[ $args != *${t}* ]];then
        chroot /host/ grubby --update-kernel=DEFAULT --args=${t}
        let ret++
    fi
done
echo $ret
