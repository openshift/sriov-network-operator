#!/bin/bash

if [ "$CLUSTER_TYPE" == "openshift" ]; then
  echo "openshift cluster"
  exit
fi

chroot_path="/host"

function clean_services() {
  # Remove switchdev service files
  rm -f $chroot_path/etc/systemd/system/switchdev-configuration-after-nm.service
  rm -f $chroot_path/etc/systemd/system/switchdev-configuration-before-nm.service
  rm -f $chroot_path/usr/local/bin/switchdev-configuration-after-nm.sh
  rm -f $chroot_path/usr/local/bin/switchdev-configuration-before-nm.sh
  rm -f $chroot_path/etc/switchdev.conf
  rm -f $chroot_path/etc/udev/switchdev-vf-link-name.sh
  # The following files are no longer created by config daemon
  # Remove them in case of leftovers from earlier SR-IOV network operator
  rm -f $chroot_path/usr/local/bin/configure-switchdev.sh
  rm -f $chroot_path/etc/systemd/system/switchdev-configuration.service

  # clean NetworkManager and ovs-vswitchd services
  network_manager_service=$chroot_path/usr/lib/systemd/system/NetworkManager.service
  ovs_service=$chroot_path/usr/lib/systemd/system/ovs-vswitchd.service

  if [ -f $network_manager_service ]; then
    sed -i.bak '/switchdev-configuration.service/d' $network_manager_service
  fi

  if [ -f $ovs_service ]; then
    sed -i.bak '/hw-offload/d' $ovs_service
  fi
}

clean_services
# Reload host services
chroot $chroot_path /bin/bash -c systemctl daemon-reload >/dev/null 2>&1 || true

# Restart system services
chroot $chroot_path /bin/bash -c systemctl restart NetworkManager.service >/dev/null 2>&1 || true
chroot $chroot_path /bin/bash -c systemctl restart ovs-vswitchd.service >/dev/null 2>&1 || true
