#!/usr/bin/env bash
set -o errexit
# ensure this file is sourced to add required components to PATH

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"
VERSION="v0.10.0"
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${VERSION}/kind-$(uname)-amd64"
K8_STABLE_RELEASE_URL="https://storage.googleapis.com/kubernetes-release/release/stable.txt"
JQ_RELEASE_URL="https://github.com/stedolan/jq/releases/download/jq-1.6/jq-linux64"

if [ ! -d "${root}/bin" ]; then
      mkdir "${root}/bin"
fi

echo "retrieving kind"
curl -Lo "${root}/bin/kind" "${KIND_BINARY_URL}"
chmod +x "${root}/bin/kind"

echo "retrieving kubectl"
curl -Lo "${root}/bin/kubectl" "https://storage.googleapis.com/kubernetes-release/release/$(curl -s ${K8_STABLE_RELEASE_URL})/bin/linux/amd64/kubectl"
chmod +x "${root}/bin/kubectl"

echo "retrieving jq"
curl -Lo "${root}/bin/jq" "${JQ_RELEASE_URL}"
chmod +x "${root}/bin/jq"
export PATH="$PATH:$root/bin"
