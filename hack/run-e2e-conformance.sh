#!/bin/bash

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

GOPATH="${GOPATH:-$HOME/go}"
JUNIT_OUTPUT="${JUNIT_OUTPUT:-/tmp/artifacts}"
export PATH=$PATH:$GOPATH/bin

# Do not merge
source ${here}/get-e2e-kind-tools.sh
kubectl kustomize config/networkpolicies | kubectl apply -n openshift-sriov-network-operator -f -

${root}/bin/ginkgo --timeout=3h -output-dir=$JUNIT_OUTPUT --junit-report "unit_report.xml" -v "$SUITE" -- -report=$JUNIT_OUTPUT
