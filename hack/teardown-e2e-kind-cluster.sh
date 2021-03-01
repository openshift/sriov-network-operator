#!/usr/bin/env bash
# Teardown KinD cluster

root="$(dirname "$0")/../"
export PATH="${PATH}:${root:?}bin"

if ! command -v kind &> /dev/null; then
  echo "KinD is not available"
  exit 1
fi

kind delete cluster
