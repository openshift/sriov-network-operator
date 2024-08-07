#!/bin/bash

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

GOPATH="${GOPATH:-$HOME/go}"
JUNIT_OUTPUT="${JUNIT_OUTPUT:-/tmp/artifacts}"
export PATH=$PATH:$GOPATH/bin

${root}/bin/ginkgo -output-dir=$JUNIT_OUTPUT --junit-report "unit_report.xml" -v "$SUITE" -- -report=$JUNIT_OUTPUT

# test my patch
