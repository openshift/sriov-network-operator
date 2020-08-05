#!/bin/bash

REDHAT_RELEASE_FILE="/host/etc/redhat-release"

RDMA_CONDITION_FILE=""
RDMA_SERVICE_NAME=""
PACKAGE_MANAGER=""

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

function enable_rdma {
  if [ -f "$RDMA_CONDITION_FILE" ]; then
    echo "$RDMA_SERVICE_NAME.service installed"
    if kmod_isloaded; then
      exit
    else
      trigger_udev_event
    fi
  else
    chroot /host/ $PACKAGE_MANAGER install -y rdma-core
    trigger_udev_event
  fi

  if kmod_isloaded; then
    exit
  else
    exit 1
  fi
}

if ! grep --quiet 'mlx4_en' <(chroot /host/ lsmod) && ! grep --quiet 'mlx5_core' <(chroot /host/ lsmod); then
  echo "No RDMA capable device"
  exit 1
fi

if [ -f "$REDHAT_RELEASE_FILE" ]; then
  if grep --quiet CoreOS "$REDHAT_RELEASE_FILE"; then
    echo "It's CoreOS, exit"
    if kmod_isloaded; then
      exit
    else
      exit 1
    fi
  else
    RDMA_CONDITION_FILE="/host/usr/libexec/rdma-init-kernel"
    RDMA_SERVICE_NAME="rdma"
    PACKAGE_MANAGER=yum

    enable_rdma
  fi
elif grep -i --quiet 'ubuntu' /host/etc/os-release ; then
  RDMA_CONDITION_FILE="/host/usr/sbin/rdma-ndd"
  RDMA_SERVICE_NAME="rdma-ndd"
  PACKAGE_MANAGER=apt-get

  enable_rdma
else
  os=$(cat /etc/os-release | grep PRETTY_NAME | cut -c 13-)
  echo "Unsupported OS: $os"
  exit 1
fi
