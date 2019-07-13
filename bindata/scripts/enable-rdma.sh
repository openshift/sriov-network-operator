#!/bin/bash

REDHAT_RELEASE_FILE="/etc/redhat-release"

function kmod_isloaded {
  if grep --quiet '\(^ib\|^rdma\)' <(chroot /host/ lsmod); then
    echo "RDMA kernel modules loaded"
    true
  else
    echo "RDMA kernel modules not loaded"
    false
  fi
}

function trigger_udev_event {
  echo "Trigger udev event"
  chroot /host/ modprobe -r mlx4_en && chroot /host/ modprobe mlx4_en
  chroot /host/ modprobe -r mlx5_core && chroot /host/ modprobe mlx5_core
}

if [ ! -f "$REDHAT_RELEASE_FILE" ]; then
  exit 1
fi

if ! grep --quiet 'mlx4_en' <(chroot /host/ lsmod) && ! grep --quiet 'mlx5_core' <(chroot /host/ lsmod); then
  echo "No RDMA capable device"
  exit 1
fi

if grep --quiet CoreOS "$REDHAT_RELEASE_FILE"; then
  echo "It's CoreOS, exit"
  if kmod_isloaded; then
    exit
  else
    exit 1
  fi
else
  if [ -f /host/usr/libexec/rdma-init-kernel ]; then
    echo "rdma.service installed"
    if kmod_isloaded; then
      exit
    else
      trigger_udev_event
    fi
  else
    chroot /host/ yum install -y rdma-core
    trigger_udev_event
  fi

  if kmod_isloaded; then
    exit
  else
    exit 1
  fi
fi
