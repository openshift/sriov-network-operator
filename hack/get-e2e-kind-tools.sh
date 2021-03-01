#!/usr/bin/env bash
set -o errexit

root="$(dirname "$0")/../"
VERSION="v0.10.0"
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${VERSION}/kind-$(uname)-amd64"
K8_STABLE_RELEASE_URL="https://storage.googleapis.com/kubernetes-release/release/stable.txt"
JQ_RELEASE_URL="https://github.com/stedolan/jq/releases/download/jq-1.6/jq-linux64"

if [ ! -d "${root:?}bin" ]; then
      mkdir "${root:?}bin"
fi

echo "retrieving kind"
curl --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -Lo "${root}/bin/kind" "${KIND_BINARY_URL}"
chmod +x "${root}/bin/kind"

echo "retrieving kubectl"
curl --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -Lo "${root}/bin/kubectl" "https://storage.googleapis.com/kubernetes-release/release/$(curl -s ${K8_STABLE_RELEASE_URL})/bin/linux/amd64/kubectl"
chmod +x "${root}/bin/kubectl"

echo "retrieving jq"
curl --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -Lo "${root}/bin/jq" "${JQ_RELEASE_URL}"
chmod +x "${root}/bin/jq"
