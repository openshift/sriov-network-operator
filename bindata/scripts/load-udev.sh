#!/bin/bash

REDHAT_RELEASE_FILE="/host/etc/redhat-release"
udevadm_bin=""

if [ -f "$REDHAT_RELEASE_FILE" ]; then
  udevadm_bin="/usr/sbin/udevadm"
elif grep -i --quiet 'ubuntu' /host/etc/os-release; then
  if grep -i --quiet '20' /host/etc/os-release; then
    udevadm_bin="/usr/bin/udevadm"
  elif grep -i --quiet '16\|18\|14' /host/etc/os-release; then
    udevadm_bin="/sbin/udevadm"
  fi
fi

if [ -z "$udevadm_bin" ]; then
  echo "udevadm not found"
  exit 1
fi

echo "Reload udev rules: $udevadm_bin control --reload-rules"
chroot /host/ $udevadm_bin control --reload-rules

echo "Trigger udev event: $udevadm_bin trigger --action add --attr-match subsystem=net"
chroot /host/ $udevadm_bin trigger --action add --attr-match subsystem=net
