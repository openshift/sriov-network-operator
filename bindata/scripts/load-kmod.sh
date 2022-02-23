#!/bin/sh
# chroot /host/ modprobe $1
kmod_name=$(tr "-" "_" <<< $1)
kmod_args="${@:2}"
chroot /host/ lsmod | grep "^$1" >& /dev/null

if [ $? -eq 0 ]
then
        # NOTE: We do not check if the module is loaded with specific options
        #       so a manual reload is required if the module is loaded with
        #       new or different options.
        echo "Module $kmod_name already loaded; no change will be applied..."
        exit 0 
else
        chroot /host/ modprobe $kmod_name $kmod_args
fi
