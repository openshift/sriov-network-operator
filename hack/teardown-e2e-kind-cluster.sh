#!/usr/bin/env bash
# Teardown KinD cluster

if ! command -v kind &> /dev/null; then
  echo "KinD is not available"
  exit 1
fi

if systemctl is-active vf-switcher.service -q;then
    sudo systemctl stop vf-switcher.service
fi
sudo rm -f /etc/vf-switcher/vf-switcher.yaml
kind delete cluster

