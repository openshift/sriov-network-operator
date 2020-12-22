#!/bin/sh
if [ "$IS_CONTAINER" != "" ]; then
  for TARGET in "${@}"; do
    find "${TARGET}" -name '*.go' ! -path '*/vendor/*' ! -path '*/build/*' -exec gofmt -s -w {} \+
  done
  git diff --exit-code
else
  $CONTAINER_CMD run --rm \
    --env IS_CONTAINER=TRUE \
    --volume "${PWD}:/go/src/github.com/k8snetworkplumbingwg/sriov-network-operator:z" \
    --workdir /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator \
    openshift/origin-release:golang-1.12 \
    ./hack/go-fmt.sh "${@}"
fi