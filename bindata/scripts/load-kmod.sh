#!/bin/sh
# chroot /host/ modprobe $1
kmod_name=$(tr "-" "_" <<< $1)
kmod_args="${@:2}"
chroot /host/ lsmod | grep "^$1" >& /dev/null

if [ $? -eq 0 ]
then
        exit 0 
else
        chroot /host/ modprobe $kmod_name $kmod_args
fi
