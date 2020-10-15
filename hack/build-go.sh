#!/usr/bin/env bash

set -eux

REPO=github.com/openshift/sriov-network-operator
WHAT=${WHAT:-sriov-network-operator}
GOFLAGS=${GOFLAGS:-}
GLDFLAGS=${GLDFLAGS:-}

# eval $(go env | grep -e "GOHOSTOS" -e "GOHOSTARCH")

# : "${GOOS:=${GOHOSTOS}}"
# : "${GOARCH:=${GOHOSTARCH}}"
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)

# Go to the root of the repo
cdup="$(git rev-parse --show-cdup)" && test -n "$cdup" && cd "$cdup"

if [ -z ${VERSION_OVERRIDE+a} ]; then
	echo "Using version from git..."
	VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
fi

GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"

# eval $(go env)

if [ -z ${BIN_PATH+a} ]; then
	export BIN_PATH=build/_output/${GOOS}/${GOARCH}
fi

mkdir -p ${BIN_PATH}

CGO_ENABLED=1


if [[ ${WHAT} == "manager" ]]; then
echo "Building ${WHAT} (${VERSION_OVERRIDE})"
CGO_ENABLED=${CGO_ENABLED} GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS} -s -w" -o ${BIN_PATH}/${WHAT} main.go
else
echo "Building ${REPO}/cmd/${WHAT} (${VERSION_OVERRIDE})"
CGO_ENABLED=${CGO_ENABLED} GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS} -s -w" -o ${BIN_PATH}/${WHAT} ${REPO}/cmd/${WHAT}
fi