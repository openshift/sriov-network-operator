#!/bin/sh
# chroot /host/ modprobe $1
kmod_name=$(tr "-" "_" <<< $1)
chroot /host/ lsmod | grep $1 >& /dev/null

if [ $? -eq 0 ]
then
        exit 0 
else
        chroot /host/ modprobe $kmod_name
fi
