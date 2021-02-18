#!/bin/bash

echo "Reload udev rules: /usr/sbin/udevadm control --reload-rules"
chroot /host/ /usr/sbin/udevadm control --reload-rules

echo "Trigger udev event: /usr/sbin/udevadm trigger --action add --attr-match subsystem=net"
chroot /host/ /usr/sbin/udevadm trigger --action add --attr-match subsystem=net
